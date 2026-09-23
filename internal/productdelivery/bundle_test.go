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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func bundleFixture(t *testing.T) (*media.Catalog, []BundleSource, [][]byte, string) {
	t.Helper()
	root := t.TempDir()
	stores := media.NewCatalogFromConfig(config.Config{MediaRoot: root, MediaStage: config.MediaStageConfig{MaxBytes: 2*MaxBytes + 16, MaxObjects: 2, MinFreeBytes: 1, TempDir: t.TempDir()}})
	sources := []BundleSource{{Name: "说明.txt", MIMEType: "text/plain", Backend: "local_file", Key: "private-a"}, {Name: "render.bin", MIMEType: "application/octet-stream", Backend: "local_file", Key: "private-b"}}
	bodies := [][]byte{[]byte("Original recipe\n"), []byte("\x00Binary payload\x00")}
	for i, source := range sources {
		if err := stores.Primary().Put(t.Context(), source.Key, bodies[i], source.MIMEType); err != nil {
			t.Fatal(err)
		}
	}
	return stores, sources, bodies, root
}

func TestBundleUploadedUTF8TextPreservesMIMEAndReconstruction(t *testing.T) {
	stores, sources, bodies, _ := bundleFixture(t)
	sources[0].MIMEType = "text/plain; charset=utf-8"
	stage, manifest, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if manifest.Files[0].MIMEType != sources[0].MIMEType {
		t.Fatal("uploaded source MIME was rewritten", manifest)
	}
	if err := stage.Put(t.Context(), stores.Primary(), "utf8.zip", BundleMIME); err != nil {
		t.Fatal(err)
	}
	digest, size := stage.SHA256, stage.Size
	_ = stage.Close()
	rebuilt, again, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if rebuilt.SHA256 != digest || rebuilt.Size != size || !reflect.DeepEqual(manifest, again) {
		t.Fatal("UTF-8 source could not be reconstructed exactly")
	}
	_ = rebuilt.Close()
	object, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "utf8.zip", digest, size, manifest, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(body, bodies[0]) {
		t.Fatal("UTF-8 member download", readErr, closeErr)
	}
}

func TestBundleDeterministicManifestAndIndividualRanges(t *testing.T) {
	stores, sources, bodies, root := bundleFixture(t)
	stage, manifest, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = stage.Put(t.Context(), stores.Primary(), "order.zip", BundleMIME); err != nil {
		t.Fatal(err)
	}
	size, digest := stage.Size, stage.SHA256
	raw, err := io.ReadAll(io.NewSectionReader(stage, 0, size))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(archive.File) != 3 {
		t.Fatal(archive, err)
	}
	for i, member := range archive.File {
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if member.Method != zip.Store || !member.Mode().IsRegular() || member.Modified.Year() != 1980 {
			t.Fatal(member.FileHeader)
		}
		if i < len(bodies) {
			if !bytes.Equal(body, bodies[i]) || member.Name != sources[i].Name {
				t.Fatal(member.Name, body)
			}
			sum := sha256.Sum256(body)
			if manifest.Files[i].SHA256 != hex.EncodeToString(sum[:]) || manifest.Files[i].SizeBytes != int64(len(body)) {
				t.Fatal(manifest)
			}
		} else {
			if member.Name != bundleManifestName {
				t.Fatal(member.Name)
			}
			var decoded BundleManifest
			if json.Unmarshal(body, &decoded) != nil || !reflect.DeepEqual(decoded, manifest) || bytes.Contains(body, []byte("private-")) || bytes.Contains(body, []byte("local_file")) {
				t.Fatal(string(body))
			}
		}
	}
	_ = stage.Close()
	// Source metadata and staging directory names must not alter the package.
	for _, source := range sources {
		if err := os.Chtimes(filepath.Join(root, source.Key), time.Unix(1500000000, 0), time.Unix(1700000000, 0)); err != nil {
			t.Fatal(err)
		}
	}
	second, again, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	if second.SHA256 != digest || second.Size != size || !reflect.DeepEqual(again, manifest) {
		t.Fatal("nondeterministic package", digest, second.SHA256)
	}
	_ = second.Close()
	if digest != "dd791d392952c7bea383e15ce8725c11fb4c1d2ea5b12cfe1dcd73cc32b00487" || size != 717 {
		t.Fatal("version-1 wire format changed", digest, size)
	}
	for i := range sources {
		object, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "order.zip", digest, size, manifest, i, &media.ByteRange{Start: 1, End: 4})
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(object.Body)
		closeErr := object.Body.Close()
		if err != nil || closeErr != nil || !bytes.Equal(body, bodies[i][1:5]) || object.Info.Size != int64(len(bodies[i])) || object.Info.ETag != `"sha256-`+manifest.Files[i].SHA256+`"` {
			t.Fatal(body, object.Info, err, closeErr)
		}
	}
	object, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "order.zip", digest, size, manifest, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A range of the first member must fail when any other package byte changes.
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, "order.zip"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil || !bytes.Equal(got, bodies[0]) {
		t.Fatal("in-flight source changed verified response", got, err)
	}
	if _, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "order.zip", digest, size, manifest, 0, &media.ByteRange{Start: 0, End: 0}); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("corrupt unrelated bytes exposed", err)
	}
}

func TestBundleStandardUnzipCompatibility(t *testing.T) {
	tool, err := exec.LookPath("unzip")
	if err != nil {
		t.Skip("standard unzip is not installed")
	}
	stores, sources, _, root := bundleFixture(t)
	stage, _, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.Put(t.Context(), stores.Primary(), "interop.zip", BundleMIME); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), tool, "-t", filepath.Join(root, "interop.zip")).CombinedOutput()
	if err != nil {
		t.Fatalf("standard unzip rejected package: %v\n%s", err, output)
	}
}

func TestBundleFileResponseOwnsStagingBudget(t *testing.T) {
	stores, sources, _, _ := bundleFixture(t)
	stage, manifest, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Put(t.Context(), stores.Primary(), "order.zip", BundleMIME); err != nil {
		t.Fatal(err)
	}
	size, digest := stage.Size, stage.SHA256
	_ = stage.Close()
	response, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "order.zip", digest, size, manifest, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	held, err := stores.Stage(t.Context(), strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := stores.Stage(t.Context(), strings.NewReader("x"), 1); !errors.Is(err, media.ErrStageBusy) {
		t.Fatal("download released its slot before Close", err)
	}
	_ = response.Body.Close()
	next, err := stores.Stage(t.Context(), strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal("download Close leaked slot", err)
	}
	defer next.Close()
	_ = response.Body.Close()
	if _, err := stores.Stage(t.Context(), strings.NewReader("x"), 1); !errors.Is(err, media.ErrStageBusy) {
		t.Fatal("double Close over-released", err)
	}
}

type bundleTrackingStore struct {
	media.Store
	opens, closes int
	open          func(context.Context, string) (media.Object, error)
}

func (s *bundleTrackingStore) Open(ctx context.Context, key string, _ *media.ByteRange) (media.Object, error) {
	s.opens++
	return s.open(ctx, key)
}

type bundleTrackedBody struct {
	io.Reader
	close func() error
}

func (b bundleTrackedBody) Close() error { return b.close() }

func TestBundleRejectsInvalidMetadataBeforeReadingSources(t *testing.T) {
	stores, sources, _, _ := bundleFixture(t)
	tracked := &bundleTrackingStore{Store: stores.Primary(), open: func(context.Context, string) (media.Object, error) {
		t.Fatal("invalid input read a source")
		return media.Object{}, nil
	}}
	catalog := media.NewCatalog(tracked)
	for _, name := range []string{"", "../a", "a/b", "a\\b", "a:b", "a\x00b", "a\nb", "a\u202eb", "a\u200bb", "a.", " a", "a ", "CON.txt", "con .txt", "Lpt1", "com¹.txt", "CONIN$", "conout$", "ConIn$.txt", "CONOUT$ .txt", "ＣＯＮＩＮ＄", "HCAI-manifest.JSON", "e\u0301.txt", "．", "a／b", strings.Repeat("a", 201)} {
		invalid := append([]BundleSource(nil), sources...)
		invalid[0].Name = name
		if stage, _, err := BuildBundle(t.Context(), catalog, invalid); !errors.Is(err, ErrBundleInvalid) || stage != nil {
			t.Fatal(name, err)
		}
	}
	for _, bad := range []func([]BundleSource){
		func(s []BundleSource) { s[1].Name = "说明.TXT" },
		func(s []BundleSource) { s[0].Name = "Ｋ.txt"; s[1].Name = "k.txt" },
		func(s []BundleSource) { s[1].Key = s[0].Key },
		func(s []BundleSource) { s[0].MIMEType = "text/plain\r\nX: secret" },
		func(s []BundleSource) { s[0].MIMEType = "text/plain; charset=utf-8; name=untrusted.txt" },
		func(s []BundleSource) { s[0].MIMEType = "text/plain; charset=unknown" },
		func(s []BundleSource) { s[0].Backend = "unknown" },
	} {
		invalid := append([]BundleSource(nil), sources...)
		bad(invalid)
		if stage, _, err := BuildBundle(t.Context(), catalog, invalid); !errors.Is(err, ErrBundleInvalid) || stage != nil {
			t.Fatal(invalid, err)
		}
	}
	for _, bad := range [][]BundleSource{nil, sources[:1], make([]BundleSource, MaxBundleFiles+1)} {
		if _, _, err := BuildBundle(t.Context(), catalog, bad); !errors.Is(err, ErrBundleInvalid) {
			t.Fatal(err)
		}
	}
	if tracked.opens != 0 {
		t.Fatal(tracked.opens)
	}
}

func TestBundleSourceFailuresAndQuotaRecovery(t *testing.T) {
	stores, sources, _, _ := bundleFixture(t)
	for _, scenario := range []string{"missing", "short", "long", "close", "cancel", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tracked := &bundleTrackingStore{Store: stores.Primary()}
			tracked.open = func(context.Context, string) (media.Object, error) {
				if scenario == "missing" {
					return media.Object{}, media.ErrNotFound
				}
				body := io.Reader(strings.NewReader("data"))
				size := int64(4)
				switch scenario {
				case "short":
					size = 5
				case "long":
					size = 3
				case "cancel":
					cancel()
				case "panic":
					body = panicBundleReader{}
				}
				return media.Object{Body: bundleTrackedBody{Reader: body, close: func() error {
					tracked.closes++
					if scenario == "close" {
						return io.ErrUnexpectedEOF
					}
					return nil
				}}, Info: media.ObjectInfo{Size: size}}, nil
			}
			catalog := media.NewCatalog(tracked)
			if scenario == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("panic not propagated")
						}
					}()
					_, _, _ = BuildBundle(ctx, catalog, sources)
				}()
			} else if stage, manifest, err := BuildBundle(ctx, catalog, sources); err == nil || stage != nil || len(manifest.Files) != 0 {
				t.Fatal(stage, manifest, err)
			}
			if scenario != "missing" && tracked.closes != 1 {
				t.Fatal("source not closed", tracked.opens, tracked.closes)
			}
			// Each normal bundle reserves 100 MiB and a slot. Two simultaneous
			// objects succeeding proves the failed producer returned its quota.
			a, _, err := BuildBundle(t.Context(), stores, sources)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, _, err := BuildBundle(t.Context(), stores, sources)
			if err != nil {
				t.Fatal(err)
			}
			_ = b.Close()
		})
	}
}

type panicBundleReader struct{}

func (panicBundleReader) Read([]byte) (int, error) { panic("bundle source failed") }

func TestBundleRejectsMalformedArchiveAndManifest(t *testing.T) {
	stores, sources, _, root := bundleFixture(t)
	stage, manifest, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.NewSectionReader(stage, 0, stage.Size))
	_ = stage.Close()
	if err != nil {
		t.Fatal(err)
	}
	central := bytes.Index(raw, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("missing directory")
	}
	for _, change := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"compressed", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[central+10:], zip.Deflate); return b }},
		{"symlink", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[central+38:], 0120777<<16); return b }},
		{"encrypted", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[central+8:], bundleFlags|1); return b }},
		{"bomb-size", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[central+24:], 0xffffffff); return b }},
		{"directory-count", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[len(b)-12:], 65535); return b }},
		{"directory-size", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[len(b)-10:], 0xffffffff); return b }},
		{"crc", func(b []byte) []byte { b[central+16] ^= 1; return b }},
		{"comment", func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[len(b)-2:], 6)
			return append(b, []byte("secret")...)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			body := change.mutate(append([]byte(nil), raw...))
			sum := sha256.Sum256(body)
			if err := os.WriteFile(filepath.Join(root, "malformed.zip"), body, 0600); err != nil {
				t.Fatal(err)
			}
			// Even a matching outer checksum cannot replace manifest/ZIP checks.
			if object, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "malformed.zip", hex.EncodeToString(sum[:]), int64(len(body)), manifest, 0, nil); !errors.Is(err, media.ErrIntegrity) || object.Body != nil {
				t.Fatal(object, err)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "valid.zip"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	wrong := manifest
	wrong.Files = append([]BundleFile(nil), manifest.Files...)
	wrong.Files[1].SHA256 = strings.Repeat("a", 64)
	if _, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "valid.zip", digest, int64(len(raw)), wrong, 0, nil); !errors.Is(err, media.ErrIntegrity) {
		t.Fatal("unrequested member was not verified", err)
	}
	for _, r := range []*media.ByteRange{{Start: -1, End: 1}, {Start: 2, End: 1}, {Start: 0, End: manifest.Files[0].SizeBytes}} {
		if _, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "valid.zip", digest, int64(len(raw)), manifest, 0, r); !errors.Is(err, media.ErrInvalidKey) {
			t.Fatal(err)
		}
	}
	for _, index := range []int{-1, 2} {
		if _, err := OpenBundleFile(t.Context(), stores, stores.Primary(), "valid.zip", digest, int64(len(raw)), manifest, index, nil); !errors.Is(err, ErrBundleInvalid) {
			t.Fatal(err)
		}
	}
	a, _, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal("parse failure leaked quota", err)
	}
	defer a.Close()
	b, _, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal("parse failure leaked quota", err)
	}
	_ = b.Close()
}

type bundleZeroReader struct{}

func (bundleZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestBundleFileCountAndPayloadBounds(t *testing.T) {
	local := media.NewLocalStore(t.TempDir())
	tracked := &bundleTrackingStore{Store: local}
	tracked.open = func(context.Context, string) (media.Object, error) {
		return media.Object{Body: bundleTrackedBody{Reader: strings.NewReader("x"), close: func() error { tracked.closes++; return nil }}, Info: media.ObjectInfo{Size: 1}}, nil
	}
	stores := media.NewCatalog(tracked)
	sources := make([]BundleSource, MaxBundleFiles)
	for i := range sources {
		sources[i] = BundleSource{Name: fmt.Sprintf("file-%02d.bin", i), MIMEType: "application/octet-stream", Backend: "local_file", Key: fmt.Sprintf("private-%02d", i)}
	}
	stage, manifest, err := BuildBundle(t.Context(), stores, sources)
	if err != nil {
		t.Fatal(err)
	}
	_ = stage.Close()
	if len(manifest.Files) != MaxBundleFiles || tracked.opens != MaxBundleFiles || tracked.closes != MaxBundleFiles {
		t.Fatal(manifest, tracked.opens, tracked.closes)
	}
	tracked.opens, tracked.closes = 0, 0
	tracked.open = func(_ context.Context, key string) (media.Object, error) {
		size := int64(98 << 20)
		if key == sources[1].Key {
			size = 2 << 20
		}
		return media.Object{Body: bundleTrackedBody{Reader: io.LimitReader(bundleZeroReader{}, size), close: func() error { tracked.closes++; return nil }}, Info: media.ObjectInfo{Size: size}}, nil
	}
	if stage, _, err := BuildBundle(t.Context(), stores, sources[:2]); !errors.Is(err, media.ErrIntegrity) || stage != nil {
		t.Fatal("archive overhead escaped total budget", err)
	}
	if tracked.opens != 2 || tracked.closes != 2 {
		t.Fatal("size rejection leaked source", tracked.opens, tracked.closes)
	}
}
