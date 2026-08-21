package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
)

func TestWebhookHTTPIsolationOneTimeSecretsAndAdminReplay(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	key := sha256.Sum256([]byte("http-webhook-contract-encryption-key"))
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment:          "test",
		MediaRoot:            t.TempDir(),
		WebOrigin:            "http://localhost:5173",
		LocalProviderEnabled: true,
		WebhookEncryptionKey: key[:],
		WebhookAllowLocal:    true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	ownerClient, otherClient, adminClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "webhook_owner")
	other := registerGovernanceUser(t, otherClient, server.URL, "webhook_other")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "webhook_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks", map[string]any{
		"name": "Contract receiver", "url": "http://127.0.0.1:43210/hcai-events", "eventTypes": []string{"developer.webhook.test"},
	}, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("default-off Webhook control accepted creation: %d", response.StatusCode)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE developer_access_control SET enabled=true,version=version+1`); err != nil {
		t.Fatal(err)
	}

	var credential webhooks.Credential
	response = requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks", map[string]any{
		"name": "Contract receiver", "url": "http://127.0.0.1:43210/hcai-events", "eventTypes": []string{"developer.webhook.test"},
	}, &credential)
	if response.StatusCode != http.StatusCreated || !strings.HasPrefix(credential.SigningSecret, "whsec_") || credential.ID.String() == "" {
		t.Fatalf("Webhook creation contract failed: status=%d item=%#v", response.StatusCode, credential)
	}

	var ownerAccess webhooks.Access
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks", nil, &ownerAccess)
	encodedOwnerAccess, err := json.Marshal(ownerAccess)
	if response.StatusCode != http.StatusOK || err != nil || len(ownerAccess.Endpoints) != 1 || strings.Contains(string(encodedOwnerAccess), credential.SigningSecret) || strings.Contains(string(encodedOwnerAccess), "signingSecret") || strings.Contains(string(encodedOwnerAccess), "ciphertext") {
		t.Fatalf("owner inventory exposed one-time secret material: status=%d err=%v body=%s", response.StatusCode, err, encodedOwnerAccess)
	}
	var otherAccess webhooks.Access
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks", nil, &otherAccess)
	if response.StatusCode != http.StatusOK || len(otherAccess.Endpoints) != 0 {
		t.Fatalf("another account observed owned Webhook endpoints: status=%d item=%#v", response.StatusCode, otherAccess)
	}
	response = requestJSON(t, otherClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/test", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("another account queued an owned Webhook: %d", response.StatusCode)
	}

	var delivery webhooks.Delivery
	response = requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/test", nil, &delivery)
	if response.StatusCode != http.StatusAccepted || delivery.EndpointID != credential.ID || delivery.Status != "queued" {
		t.Fatalf("Webhook test queue contract failed: status=%d item=%#v", response.StatusCode, delivery)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE developer_webhook_deliveries SET status='dead_letter',version=version+1,attempt_count=1,last_status_code=400,dead_lettered_at=now(),updated_at=now() WHERE id=$1`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		var queued webhooks.Delivery
		response = requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/test", nil, &queued)
		if response.StatusCode != http.StatusAccepted || queued.EndpointID != credential.ID {
			t.Fatalf("additional Webhook delivery %d failed: status=%d item=%#v", index, response.StatusCode, queued)
		}
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks", nil, &ownerAccess)
	if response.StatusCode != http.StatusOK || len(ownerAccess.Endpoints) != 1 || len(ownerAccess.Endpoints[0].Deliveries) != 5 || ownerAccess.Endpoints[0].DeliveryNextCursor == nil {
		t.Fatalf("bounded owner Webhook inventory mismatch: status=%d item=%#v", response.StatusCode, ownerAccess)
	}
	var ownerDeliveries webhooks.DeliveryPage
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/deliveries?limit=5&cursor="+*ownerAccess.Endpoints[0].DeliveryNextCursor, nil, &ownerDeliveries)
	encodedOwnerDeliveries, err := json.Marshal(ownerDeliveries)
	if response.StatusCode != http.StatusOK || err != nil || len(ownerDeliveries.Items) != 1 || ownerDeliveries.NextCursor != nil || strings.Contains(string(encodedOwnerDeliveries), "http://") || strings.Contains(string(encodedOwnerDeliveries), credential.SigningSecret) || strings.Contains(string(encodedOwnerDeliveries), "responseBody") {
		t.Fatalf("owner Webhook continuation contract failed: status=%d err=%v body=%s", response.StatusCode, err, encodedOwnerDeliveries)
	}
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/deliveries?limit=5&cursor=modified", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("Webhook delivery ownership was not enforced before cursor parsing: %d", response.StatusCode)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/deliveries?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized owner Webhook delivery page was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/deliveries?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified owner Webhook delivery cursor was accepted: %d", response.StatusCode)
	}

	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/admin/developer/webhooks/dead-letters", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member opened Admin Webhook dead letters: %d", response.StatusCode)
	}
	var deadLetters struct {
		Items      []webhooks.Delivery `json:"items"`
		NextCursor *string             `json:"nextCursor"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/developer/webhooks/dead-letters?q=contract&eventType=developer.webhook.test&limit=1", nil, &deadLetters)
	encodedDeadLetters, err := json.Marshal(deadLetters)
	if response.StatusCode != http.StatusOK || err != nil || len(deadLetters.Items) != 1 || deadLetters.Items[0].ID != delivery.ID || strings.Contains(string(encodedDeadLetters), "http://") || strings.Contains(string(encodedDeadLetters), credential.SigningSecret) || strings.Contains(string(encodedDeadLetters), "responseBody") {
		t.Fatalf("Admin dead-letter evidence contract failed: status=%d err=%v body=%s", response.StatusCode, err, encodedDeadLetters)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/developer/webhooks/dead-letters?eventType=unsupported", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported Admin Webhook event type was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/developer/webhooks/dead-letters?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized Admin Webhook page was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/developer/webhooks/dead-letters?cursor=modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified Admin Webhook cursor was accepted: %d", response.StatusCode)
	}

	replayInput := map[string]any{"expectedVersion": deadLetters.Items[0].Version}
	response = requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/admin/developer/webhooks/deliveries/"+delivery.ID.String()+"/replay", replayInput, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member replayed an Admin Webhook delivery: %d", response.StatusCode)
	}
	var replayed webhooks.Delivery
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/developer/webhooks/deliveries/"+delivery.ID.String()+"/replay", replayInput, &replayed)
	if response.StatusCode != http.StatusAccepted || replayed.OriginalDeliveryID == nil || *replayed.OriginalDeliveryID != delivery.ID {
		t.Fatalf("Admin Webhook replay contract failed: status=%d item=%#v", response.StatusCode, replayed)
	}

	var rotated webhooks.Credential
	response = requestJSON(t, ownerClient, http.MethodPost, server.URL+"/api/v1/account/developer-webhooks/"+credential.ID.String()+"/rotate", map[string]any{
		"expectedVersion": credential.Version, "reason": "Rotate after completing one-time secret contract verification", "confirmed": true,
	}, &rotated)
	if response.StatusCode != http.StatusCreated || !strings.HasPrefix(rotated.SigningSecret, "whsec_") || rotated.SigningSecret == credential.SigningSecret {
		t.Fatalf("Webhook secret rotation contract failed: status=%d item=%#v", response.StatusCode, rotated)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/account/developer-webhooks", nil, &ownerAccess)
	encodedOwnerAccess, err = json.Marshal(ownerAccess)
	if response.StatusCode != http.StatusOK || err != nil || strings.Contains(string(encodedOwnerAccess), rotated.SigningSecret) || strings.Contains(string(encodedOwnerAccess), "signingSecret") {
		t.Fatalf("rotated one-time secret reappeared in inventory: status=%d err=%v body=%s", response.StatusCode, err, encodedOwnerAccess)
	}

	var notificationCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='security.webhook_replayed' AND resource_id=$2`, owner.ID, replayed.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || other.ID == owner.ID {
		t.Fatalf("Webhook replay notification evidence mismatch: notifications=%d", notificationCount)
	}
}
