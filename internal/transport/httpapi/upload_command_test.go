package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestUploadCommandHTTPRequiresKeyAndReplaysBothRoutes(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "command_owner")
	send := func(path, key, note, content string, duplicated bool) (int, assets.Asset, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("title", "Idempotent HTTP original"); err != nil {
			t.Fatal(err)
		}
		if note != "" {
			if err := writer.WriteField("note", note); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("file", "original.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+path, &body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", writer.FormDataContentType())
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		if duplicated {
			request.Header.Add("Idempotency-Key", key)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var a assets.Asset
		if response.StatusCode < 300 {
			if err = json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode, a, string(raw)
	}
	for _, key := range []string{"", "short", "has spaces key", strings.Repeat("x", 129), "非ASCII重试标识"} {
		status, _, body := send("/api/v1/assets/uploads", key, "", "Original HTTP bytes", false)
		if status != 422 || !strings.Contains(body, "invalid_upload_key") {
			t.Fatal(status, body)
		}
	}
	key := uuid.NewString()
	status, _, body := send("/api/v1/assets/uploads", key, "", "Original HTTP bytes", true)
	if status != 422 {
		t.Fatal(status, body)
	}
	status, base, body := send("/api/v1/assets/uploads", key, "", "Original HTTP bytes", false)
	if status != 201 {
		t.Fatal(status, body)
	}
	status, replay, body := send("/api/v1/assets/uploads", key, "", "Original HTTP bytes", false)
	if status != 200 || base.ID != replay.ID {
		t.Fatal(status, body)
	}
	status, _, body = send("/api/v1/assets/uploads", key, "", "Changed HTTP bytes!", false)
	if status != 409 || !strings.Contains(body, "upload_idempotency_conflict") {
		t.Fatal(status, body)
	}
	path := "/api/v1/assets/" + base.ID.String() + "/versions"
	status, _, body = send(path, "", "Updated version", "Version HTTP bytes", false)
	if status != 422 {
		t.Fatal(status, body)
	}
	versionKey := uuid.NewString()
	status, version, body := send(path, versionKey, "Updated version", "Version HTTP bytes", false)
	if status != 201 || version.VersionNumber != 2 {
		t.Fatal(status, body)
	}
	status, replay, body = send(path, versionKey, "Updated version", "Version HTTP bytes", false)
	if status != 200 || replay.ID != version.ID {
		t.Fatal(status, body)
	}
	status, _, body = send(path, versionKey, "Different note", "Version HTTP bytes", false)
	if status != 409 {
		t.Fatal(status, body)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM assets WHERE owner_id=$1`, owner.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("HTTP retries duplicated assets", count, err)
	}
}
