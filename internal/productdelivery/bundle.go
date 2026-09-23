package productdelivery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxBundleFiles                = 20
	BundleMIME                    = "application/zip"
	bundleManifestName            = "HCAI-MANIFEST.json"
	bundleFlags            uint16 = 0x808 // UTF-8 names and a signed data descriptor.
	maxBundleManifestBytes        = 16384
)

var ErrBundleInvalid = errors.New("invalid product file bundle")

// BundleSource is server-side source evidence. Never expose its storage locator
// in public listings, downloaded manifests or purchaser-facing API responses.
type BundleSource struct{ Name, MIMEType, Backend, Key string }

type BundleFile struct {
	Name      string `json:"name"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// Version fixes the exact ZIP encoding and the ordered, public-safe manifest.
// A later format must not silently reserialize an accepted version-1 package.
type BundleManifest struct {
	Version int          `json:"version"`
	Files   []BundleFile `json:"files"`
}

func bundleNameKey(name string) string { return cases.Fold().String(norm.NFKC.String(name)) }

func ValidBundleName(name string) bool {
	if len(name) < 1 || len(name) > 200 || !utf8.ValidString(name) {
		return false
	}
	compatible := norm.NFKC.String(name)
	if !norm.NFC.IsNormalString(name) || strings.TrimSpace(name) != name || strings.HasSuffix(compatible, ".") || strings.ContainsAny(compatible, `/\:*?"<>|`) || compatible == "." || compatible == ".." || bundleNameKey(name) == bundleNameKey(bundleManifestName) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.In(c, unicode.Cf) {
			return false
		}
	}
	// Archives may be unpacked on a case-insensitive Windows filesystem.
	base := strings.ToUpper(strings.TrimRight(strings.SplitN(compatible, ".", 2)[0], " ."))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func validBundleMIME(value string) bool {
	// Upload detection records UTF-8 text with its charset. Preserve that exact
	// source evidence in the accepted manifest, including during reconstruction.
	// Other parameters remain unsupported rather than being silently stripped.
	if value == "text/plain; charset=utf-8" {
		return true
	}
	if len(value) > 200 {
		return false
	}
	base, params, err := mime.ParseMediaType(value)
	return err == nil && len(params) == 0 && base == value && strings.Contains(base, "/")
}

// ValidateBundleNames is also used when accepting a seller's real file list,
// before byte sizes and hashes are available. One shared collision rule applies
// to publication and to the eventual archive on all supported filesystems.
func ValidateBundleNames(names []string) error {
	if len(names) < 2 || len(names) > MaxBundleFiles {
		return ErrBundleInvalid
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !ValidBundleName(name) {
			return ErrBundleInvalid
		}
		key := bundleNameKey(name)
		if seen[key] {
			return ErrBundleInvalid
		}
		seen[key] = true
	}
	return nil
}

func (m BundleManifest) Validate() error {
	if m.Version != 1 || len(m.Files) < 2 || len(m.Files) > MaxBundleFiles {
		return ErrBundleInvalid
	}
	names := make([]string, len(m.Files))
	var total int64
	for i, file := range m.Files {
		names[i] = file.Name
		if !validBundleMIME(file.MIMEType) || len(file.SHA256) != 64 || file.SizeBytes < 1 || file.SizeBytes > MaxBytes-total {
			return ErrBundleInvalid
		}
		digest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(digest) != 32 || file.SHA256 != strings.ToLower(file.SHA256) {
			return ErrBundleInvalid
		}
		total += file.SizeBytes
	}
	return ValidateBundleNames(names)
}

// BuildBundle assembles an uncompressed version-1 ZIP, staging and hashing it
// before any durable object write. ZIP headers are explicit rather than using
// zip.Writer's evolving defaults, so repair can reconstruct identical bytes.
// Callers must authorize and freeze every source before invoking this helper.
func BuildBundle(ctx context.Context, stores *media.Catalog, sources []BundleSource) (*media.StagedObject, BundleManifest, error) {
	empty := BundleManifest{}
	if stores == nil || len(sources) < 2 || len(sources) > MaxBundleFiles {
		return nil, empty, ErrBundleInvalid
	}
	names, locations := make([]string, len(sources)), map[string]bool{}
	for i, source := range sources {
		names[i] = source.Name
		if !validBundleMIME(source.MIMEType) || (source.Backend != "local_file" && source.Backend != "s3") || source.Key == "" || len(source.Key) > 1024 || strings.ContainsRune(source.Key, 0) {
			return nil, empty, ErrBundleInvalid
		}
		location := source.Backend + "\x00" + source.Key
		if locations[location] {
			return nil, empty, ErrBundleInvalid
		}
		locations[location] = true
	}
	if err := ValidateBundleNames(names); err != nil {
		return nil, empty, err
	}
	manifest := BundleManifest{Version: 1, Files: make([]BundleFile, 0, len(sources))}
	stage, err := stores.StageWrite(ctx, MaxBytes, func(target io.Writer) error {
		w := &bundleWriter{target: target}
		entries := make([]bundleDirectoryEntry, 0, len(sources)+1)
		for _, source := range sources {
			if err := ctx.Err(); err != nil {
				return err
			}
			store, err := stores.Get(source.Backend)
			if err != nil {
				return err
			}
			object, err := store.Open(ctx, source.Key, nil)
			if err != nil {
				return err
			}
			entry, file, err := writeBundleSource(ctx, w, source, object)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
			manifest.Files = append(manifest.Files, file)
		}
		if err := manifest.Validate(); err != nil {
			return err
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		if len(data) > maxBundleManifestBytes {
			return ErrBundleInvalid
		}
		entry, _, err := writeBundleSource(ctx, w, BundleSource{Name: bundleManifestName, MIMEType: "application/json"}, media.Object{Body: io.NopCloser(bytes.NewReader(data)), Info: media.ObjectInfo{Size: int64(len(data))}})
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return w.finish(entries)
	})
	if err != nil {
		return nil, empty, err
	}
	return stage, manifest, nil
}

type bundleWriter struct {
	target io.Writer
	offset int64
}

func (w *bundleWriter) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	w.offset += int64(n)
	return n, err
}

type bundleDirectoryEntry struct {
	name              string
	offset, size, crc uint32
}

func writeBundleSource(ctx context.Context, w *bundleWriter, source BundleSource, object media.Object) (entry bundleDirectoryEntry, file BundleFile, err error) {
	if object.Body == nil {
		return entry, file, media.ErrIntegrity
	}
	defer func() { err = errors.Join(err, object.Body.Close()) }()
	if object.Info.Size < 1 || object.Info.Size > MaxBytes || object.Info.Size > MaxBytes-w.offset {
		return entry, file, media.ErrIntegrity
	}
	entry = bundleDirectoryEntry{name: source.Name, offset: uint32(w.offset)}
	// Version 1: STORE, UTF-8 + descriptor, DOS 1980-01-01, no extra fields.
	header := make([]byte, 30)
	binary.LittleEndian.PutUint32(header, 0x04034b50)
	binary.LittleEndian.PutUint16(header[4:], 20)
	binary.LittleEndian.PutUint16(header[6:], bundleFlags)
	binary.LittleEndian.PutUint16(header[12:], 33)
	binary.LittleEndian.PutUint16(header[26:], uint16(len(source.Name)))
	if _, err = w.Write(header); err != nil {
		return
	}
	if _, err = io.WriteString(w, source.Name); err != nil {
		return
	}
	hash, crc := sha256.New(), crc32.NewIEEE()
	var n int64
	n, err = io.Copy(io.MultiWriter(w, hash, crc), io.LimitReader(&bundleContextReader{ctx, object.Body}, object.Info.Size+1))
	if err != nil {
		return
	}
	if n != object.Info.Size {
		err = media.ErrIntegrity
		return
	}
	entry.size, entry.crc = uint32(n), crc.Sum32()
	file = BundleFile{Name: source.Name, MIMEType: source.MIMEType, SizeBytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	descriptor := make([]byte, 16)
	binary.LittleEndian.PutUint32(descriptor, 0x08074b50)
	binary.LittleEndian.PutUint32(descriptor[4:], entry.crc)
	binary.LittleEndian.PutUint32(descriptor[8:], entry.size)
	binary.LittleEndian.PutUint32(descriptor[12:], entry.size)
	_, err = w.Write(descriptor)
	return
}

func (w *bundleWriter) finish(entries []bundleDirectoryEntry) error {
	start := uint32(w.offset)
	for _, e := range entries {
		header := make([]byte, 46)
		binary.LittleEndian.PutUint32(header, 0x02014b50)
		binary.LittleEndian.PutUint16(header[4:], 0x314) // Unix, version 2.0.
		binary.LittleEndian.PutUint16(header[6:], 20)
		binary.LittleEndian.PutUint16(header[8:], bundleFlags)
		binary.LittleEndian.PutUint16(header[14:], 33)
		binary.LittleEndian.PutUint32(header[16:], e.crc)
		binary.LittleEndian.PutUint32(header[20:], e.size)
		binary.LittleEndian.PutUint32(header[24:], e.size)
		binary.LittleEndian.PutUint16(header[28:], uint16(len(e.name)))
		binary.LittleEndian.PutUint32(header[38:], 0100644<<16)
		binary.LittleEndian.PutUint32(header[42:], e.offset)
		if _, err := w.Write(header); err != nil {
			return err
		}
		if _, err := io.WriteString(w, e.name); err != nil {
			return err
		}
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(entries)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(entries)))
	binary.LittleEndian.PutUint32(end[12:], uint32(w.offset)-start)
	binary.LittleEndian.PutUint32(end[16:], start)
	_, err := w.Write(end)
	return err
}

// OpenBundleFile verifies the complete immutable package and every member
// before exposing even a range of one file. No archive path is ever extracted.
// This helper is not authorization: the caller must check the current buyer's
// entitlement and scan state for every applicable source.
func OpenBundleFile(ctx context.Context, stores *media.Catalog, store media.Store, key, digest string, size int64, expected BundleManifest, index int, requested *media.ByteRange) (media.Object, error) {
	if stores == nil || store == nil || expected.Validate() != nil || index < 0 || index >= len(expected.Files) {
		return media.Object{}, ErrBundleInvalid
	}
	file := expected.Files[index]
	start, length := int64(0), file.SizeBytes
	if requested != nil {
		if requested.Start < 0 || requested.End < requested.Start || requested.End >= file.SizeBytes {
			return media.Object{}, media.ErrInvalidKey
		}
		start, length = requested.Start, requested.End-requested.Start+1
	}
	stage, _, err := stores.OpenVerifiedStage(ctx, store, key, digest, size)
	if err != nil {
		return media.Object{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = stage.Close()
		}
	}()
	// Bound metadata before archive/zip allocates directory entries. A small
	// payload containing many zero-sized entries must not expand into an
	// unbounded in-memory directory, even with a matching outer checksum.
	if !validBundleDirectory(stage, len(expected.Files)+1) {
		return media.Object{}, media.ErrIntegrity
	}
	archive, err := zip.NewReader(stage, stage.Size)
	if err != nil || archive.Comment != "" || len(archive.File) != len(expected.Files)+1 {
		return media.Object{}, media.ErrIntegrity
	}
	manifestBytes, _ := json.Marshal(expected)
	for i, member := range archive.File {
		name, memberSize := bundleManifestName, int64(len(manifestBytes))
		if i < len(expected.Files) {
			name, memberSize = expected.Files[i].Name, expected.Files[i].SizeBytes
		}
		if member.Name != name || member.Method != zip.Store || member.Flags != bundleFlags || member.Comment != "" || len(member.Extra) != 0 || !member.Mode().IsRegular() || member.UncompressedSize64 != uint64(memberSize) || member.CompressedSize64 != uint64(memberSize) {
			return media.Object{}, media.ErrIntegrity
		}
		reader, err := member.Open()
		if err != nil {
			return media.Object{}, media.ErrIntegrity
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(&bundleContextReader{ctx, reader}, memberSize+1))
		closeErr := reader.Close()
		if ctx.Err() != nil {
			return media.Object{}, ctx.Err()
		}
		if readErr != nil || closeErr != nil || n != memberSize {
			return media.Object{}, media.ErrIntegrity
		}
		want := ""
		if i < len(expected.Files) {
			want = expected.Files[i].SHA256
		} else {
			sum := sha256.Sum256(manifestBytes)
			want = hex.EncodeToString(sum[:])
		}
		if hex.EncodeToString(hash.Sum(nil)) != want {
			return media.Object{}, media.ErrIntegrity
		}
	}
	offset, err := archive.File[index].DataOffset()
	if err != nil || offset < 0 || offset > stage.Size-file.SizeBytes {
		return media.Object{}, media.ErrIntegrity
	}
	complete = true
	return media.Object{Body: &bundleReadCloser{Reader: io.NewSectionReader(stage, offset+start, length), stage: stage}, Info: media.ObjectInfo{Size: file.SizeBytes, ETag: `"sha256-` + file.SHA256 + `"`}}, nil
}

func validBundleDirectory(stage *media.StagedObject, count int) bool {
	if count < 3 || count > MaxBundleFiles+1 || stage.Size < 22 {
		return false
	}
	var end [22]byte
	if _, err := stage.ReadAt(end[:], stage.Size-22); err != nil {
		return false
	}
	if binary.LittleEndian.Uint32(end[:]) != 0x06054b50 || binary.LittleEndian.Uint16(end[4:]) != 0 || binary.LittleEndian.Uint16(end[6:]) != 0 || int(binary.LittleEndian.Uint16(end[8:])) != count || int(binary.LittleEndian.Uint16(end[10:])) != count || binary.LittleEndian.Uint16(end[20:]) != 0 {
		return false
	}
	size, offset := int64(binary.LittleEndian.Uint32(end[12:])), int64(binary.LittleEndian.Uint32(end[16:]))
	return size >= int64(count*46) && size <= int64(count*(46+200)) && offset+size == stage.Size-22
}

type bundleReadCloser struct {
	io.Reader
	stage *media.StagedObject
}

func (b *bundleReadCloser) Close() error { return b.stage.Close() }

type bundleContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *bundleContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
