package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

const acceptanceContentType = "text/plain"

type AcceptanceResult struct {
	Status          string `json:"status"`
	StorageAdapter  string `json:"storageAdapter"`
	ScannerAdapter  string `json:"scannerAdapter"`
	ObjectKeySHA256 string `json:"objectKeySha256"`
	PayloadBytes    int    `json:"payloadBytes"`
	RangeBytes      int    `json:"rangeBytes"`
	ScanStatus      string `json:"scanStatus"`
	ScanReasonCode  string `json:"scanReasonCode"`
	ScannerEngine   string `json:"scannerEngine"`
	ScannerVersion  string `json:"scannerVersion"`
	ImmutableWrite  bool   `json:"immutableWrite"`
	Deleted         bool   `json:"deleted"`
}

// RunAcceptance exercises one disposable object and removes it on every exit path.
func RunAcceptance(ctx context.Context, store Store, scanner Scanner, suffix string) (result AcceptanceResult, err error) {
	if store == nil || scanner == nil || !acceptanceSuffix(suffix) {
		return AcceptanceResult{}, fmt.Errorf("media acceptance input is invalid")
	}
	key, err := store.ObjectKey("hcai-acceptance-" + suffix + ".txt")
	if err != nil {
		return AcceptanceResult{}, fmt.Errorf("create media acceptance object key: %w", err)
	}
	payload := []byte("HCAI media staging acceptance " + suffix)
	keyDigest := sha256.Sum256([]byte(key))
	result = AcceptanceResult{
		StorageAdapter: store.Backend(), ScannerAdapter: scanner.Adapter(), ObjectKeySHA256: fmt.Sprintf("%x", keyDigest[:]),
		PayloadBytes: len(payload), RangeBytes: min(16, len(payload)),
	}
	stored := false
	defer func() {
		if !stored {
			return
		}
		if cleanupErr := store.Delete(context.WithoutCancel(ctx), key); cleanupErr != nil && err == nil {
			err = fmt.Errorf("clean media acceptance object: %w", cleanupErr)
		}
	}()

	if err = store.Put(ctx, key, payload, acceptanceContentType); err != nil {
		return result, fmt.Errorf("write media acceptance object: %w", err)
	}
	stored = true
	if conflictErr := store.Put(ctx, key, payload, acceptanceContentType); !errors.Is(conflictErr, ErrConflict) {
		return result, fmt.Errorf("media acceptance immutable write was not rejected")
	}
	result.ImmutableWrite = true

	info, statErr := store.Stat(ctx, key)
	if statErr != nil || info.Size != int64(len(payload)) {
		return result, fmt.Errorf("stat media acceptance object failed")
	}
	full, openErr := store.Open(ctx, key, nil)
	if openErr != nil {
		return result, fmt.Errorf("open media acceptance object: %w", openErr)
	}
	fullBody, readErr := readAcceptanceObject(full, int64(len(payload)))
	if readErr != nil || !bytes.Equal(fullBody, payload) {
		return result, fmt.Errorf("read media acceptance object failed")
	}

	rangeEnd := int64(result.RangeBytes - 1)
	partial, openErr := store.Open(ctx, key, &ByteRange{Start: 0, End: rangeEnd})
	if openErr != nil {
		return result, fmt.Errorf("open media acceptance range: %w", openErr)
	}
	partialBody, readErr := readAcceptanceObject(partial, int64(result.RangeBytes))
	if readErr != nil || !bytes.Equal(partialBody, payload[:result.RangeBytes]) {
		return result, fmt.Errorf("read media acceptance range failed")
	}

	scan, scanErr := scanner.Scan(ctx, key, acceptanceContentType, fullBody)
	if scanErr != nil {
		return result, fmt.Errorf("scan media acceptance object: %w", scanErr)
	}
	result.ScanStatus, result.ScanReasonCode = scan.Status, scan.ReasonCode
	result.ScannerEngine, result.ScannerVersion = scan.Engine, scan.Version
	if scan.Status != "clean" {
		return result, fmt.Errorf("media acceptance scanner did not return clean")
	}

	if err = store.Delete(ctx, key); err != nil {
		return result, fmt.Errorf("delete media acceptance object: %w", err)
	}
	if _, statErr = store.Stat(ctx, key); !errors.Is(statErr, ErrNotFound) {
		return result, fmt.Errorf("deleted media acceptance object remained visible")
	}
	stored = false
	result.Status, result.Deleted = "passed", true
	return result, nil
}

func readAcceptanceObject(object Object, expected int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(object.Body, expected+1))
	closeErr := object.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(body)) != expected {
		return nil, fmt.Errorf("unexpected media acceptance body length")
	}
	return body, nil
}

func acceptanceSuffix(value string) bool {
	if len(value) < 16 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
