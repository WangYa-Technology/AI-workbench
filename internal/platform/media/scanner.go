package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type ScanResult struct {
	Status     string
	ReasonCode string
	Engine     string
	Version    string
}

type Scanner interface {
	Adapter() string
	Scan(context.Context, string, string, []byte) (ScanResult, error)
}

type HTTPScanner struct {
	url    string
	token  string
	client *http.Client
}

func NewHTTPScanner(url, token string, timeout time.Duration) *HTTPScanner {
	return &HTTPScanner{url: url, token: token, client: &http.Client{Timeout: timeout}}
}

func (s *HTTPScanner) Adapter() string { return "http" }

func (s *HTTPScanner) Scan(ctx context.Context, objectKey, mimeType string, data []byte) (ScanResult, error) {
	digest := sha256.Sum256(data)
	digestText := hex.EncodeToString(digest[:])
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return ScanResult{}, scannerFailure{code: "scanner_request_invalid", retry: false}
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", mimeType)
	request.ContentLength = int64(len(data))
	request.Header.Set("X-HCAI-Scanner-Contract", "1")
	request.Header.Set("X-HCAI-Object-Key", objectKey)
	request.Header.Set("X-HCAI-Content-SHA256", digestText)
	response, err := s.client.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ScanResult{}, scannerFailure{code: "scanner_timeout", retry: true}
		}
		return ScanResult{}, scannerFailure{code: "scanner_unavailable", retry: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return ScanResult{}, scannerFailure{code: "scanner_http_error", retry: retry}
	}
	var result struct {
		Status        string `json:"status"`
		ReasonCode    string `json:"reasonCode"`
		ContentSHA256 string `json:"contentSha256"`
		Engine        string `json:"engine"`
		Version       string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.ContentSHA256 != digestText || !closedToken(result.ReasonCode, 3, 80) ||
		!closedToken(result.Engine, 1, 80) || !closedToken(result.Version, 1, 80) || !oneOf(result.Status, "clean", "review", "rejected") {
		return ScanResult{}, scannerFailure{code: "scanner_response_invalid", retry: false}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ScanResult{}, scannerFailure{code: "scanner_response_invalid", retry: false}
	}
	return ScanResult{Status: result.Status, ReasonCode: result.ReasonCode, Engine: result.Engine, Version: result.Version}, nil
}

type scannerFailure struct {
	code  string
	retry bool
}

func (e scannerFailure) Error() string     { return e.code }
func (e scannerFailure) ErrorCode() string { return e.code }
func (e scannerFailure) Retryable() bool   { return e.retry }

func closedToken(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' && char != '.' {
			return false
		}
	}
	return !strings.Contains(value, "..")
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
