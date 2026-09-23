package datarights

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const exportPartBytes = 5 << 20

// OpenExport verifies every stored part and the full package before returning
// any bytes to HTTP. Readers never hold database locks while a client downloads.
func (s *Service) OpenExport(ctx context.Context, userID, requestID uuid.UUID) (*ExportFile, string, error) {
	file, err := newExportFile(ctx, s.exportLimits)
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if !success {
			_ = file.Close()
		}
	}()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var checksum, status string
	var expiry time.Time
	var purged *time.Time
	var size int64
	var count int
	err = tx.QueryRow(ctx, `SELECT a.checksum_sha256,a.size_bytes,a.part_count,a.expires_at,a.purged_at,r.status
 FROM data_rights_requests r JOIN data_rights_export_artifacts a ON a.request_id=r.id
 JOIN users u ON u.id=r.user_id
 WHERE r.id=$1 AND r.user_id=$2 AND r.request_type='data_export' AND u.status='active'`, requestID, userID).
		Scan(&checksum, &size, &count, &expiry, &purged, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotReady
	}
	if err != nil {
		return nil, "", err
	}
	if purged != nil || !time.Now().Before(expiry) {
		return nil, "", ErrExpired
	}
	if status != "ready" {
		return nil, "", ErrNotReady
	}
	if size < 0 || size > file.limits.MaxBytes {
		return nil, "", ErrExportTooLarge
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	var written int64
	if count == 0 {
		var body []byte
		if err = tx.QueryRow(ctx, `SELECT body FROM data_rights_export_artifacts WHERE request_id=$1`, requestID).Scan(&body); err != nil {
			return nil, "", err
		}
		n, e := writer.Write(body)
		written = int64(n)
		if e != nil {
			return nil, "", e
		}
	} else {
		// One part per query: memory remains bounded even when the driver buffers
		// multiple rows in a network read. The snapshot prevents mixed purge states.
		for part := 1; part <= count; part++ {
			if err = ctx.Err(); err != nil {
				return nil, "", err
			}
			var body []byte
			var partSum string
			var partSize int
			if err = tx.QueryRow(ctx, `SELECT body,checksum_sha256,size_bytes FROM data_rights_export_parts
    WHERE request_id=$1 AND part_number=$2 AND purged_at IS NULL`, requestID, part).Scan(&body, &partSum, &partSize); err != nil {
				return nil, "", fmt.Errorf("read export part %d: %w", part, err)
			}
			sum := sha256.Sum256(body)
			if len(body) != partSize || hex.EncodeToString(sum[:]) != partSum {
				return nil, "", errors.New("export part integrity mismatch")
			}
			n, e := writer.Write(body)
			written += int64(n)
			if e != nil {
				return nil, "", e
			}
		}
	}
	if written != size || hex.EncodeToString(hash.Sum(nil)) != checksum {
		return nil, "", errors.New("export package integrity mismatch")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	if !time.Now().Before(expiry) {
		return nil, "", ErrExpired
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	success = true
	return file, checksum, nil
}

func (s *Service) storeExport(ctx context.Context, tx pgx.Tx, file *ExportFile, requestID, userID uuid.UUID, subject string, generatedAt, expiresAt time.Time) (string, int64, error) {
	// Bound sort/hash work per operation. PostgreSQL role-level temp_file_limit
	// and instance memory limits remain deployment responsibilities.
	if _, err := tx.Exec(ctx, `SELECT set_config('work_mem','4MB',true),set_config('statement_timeout','60s',true)`); err != nil {
		return "", 0, err
	}
	var err error
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	if err = writeExportSnapshot(ctx, tx, writer, requestID, userID, subject, generatedAt, file.limits.MaxRowBytes); err != nil {
		return "", 0, err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", 0, err
	}
	count := (size + exportPartBytes - 1) / exportPartBytes
	checksum := hex.EncodeToString(hash.Sum(nil))
	if _, err = tx.Exec(ctx, `INSERT INTO data_rights_export_artifacts(request_id,checksum_sha256,size_bytes,expires_at,part_count) VALUES($1,$2,$3,$4,$5)`, requestID, checksum, size, expiresAt, count); err != nil {
		return "", 0, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	buffer := make([]byte, exportPartBytes)
	for part := int64(1); part <= count; part++ {
		n, readErr := io.ReadFull(file, buffer)
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return "", 0, readErr
		}
		sum := sha256.Sum256(buffer[:n])
		if _, err = tx.Exec(ctx, `INSERT INTO data_rights_export_parts(request_id,part_number,body,checksum_sha256,size_bytes) VALUES($1,$2,$3,$4,$5)`, requestID, part, buffer[:n], hex.EncodeToString(sum[:]), n); err != nil {
			return "", 0, err
		}
	}
	return checksum, size, nil
}

// Keep the established JSON schema, but stream top-level records rather than
// aggregating each domain (or the entire package) in PostgreSQL or Go memory.
func writeExportSnapshot(ctx context.Context, tx pgx.Tx, w io.Writer, requestID, userID uuid.UUID, subject string, generatedAt time.Time, maxRowBytes int64) error {
	header, err := json.Marshal(struct {
		SchemaVersion int       `json:"schemaVersion"`
		RequestID     uuid.UUID `json:"requestId"`
		SubjectRef    string    `json:"subjectRef"`
		GeneratedAt   time.Time `json:"generatedAt"`
	}{1, requestID, subject, generatedAt})
	if err != nil {
		return err
	}
	if _, err = w.Write(header[:len(header)-1]); err != nil {
		return err
	}
	if _, err = io.WriteString(w, `,"data":{`); err != nil {
		return err
	}
	keys := make([]string, 0, len(accountExportQueries))
	for key := range accountExportQueries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i > 0 {
			if _, err = io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err = writeExportSection(ctx, tx, w, key, accountExportQueries[key], userID, key == "account", maxRowBytes, exportNestedQueries[key]); err != nil {
			return err
		}
	}
	if _, err = io.WriteString(w, `,"marketplace":{"schemaVersion":1,"evidenceScope":"buyer_transactions_and_own_seller_products_sales_funds_payouts; private_operational_fields_omitted; no_file_contents","data":{`); err != nil {
		return err
	}
	for i, section := range marketplaceExportQueries {
		if i > 0 {
			if _, err = io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err = writeExportSection(ctx, tx, w, section.name, section.query, userID, false, maxRowBytes, exportNestedQueries[section.name]); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "}}}}")
	return err
}
