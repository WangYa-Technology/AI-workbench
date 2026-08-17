package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

type jobPayload struct {
	DeliveryID uuid.UUID `json:"deliveryId"`
}

type deliveryFailure struct {
	code  string
	delay time.Duration
}

func (e deliveryFailure) Error() string             { return e.code }
func (e deliveryFailure) RetryDelay() time.Duration { return e.delay }

type deliveryTarget struct {
	deliveryID    uuid.UUID
	endpointID    uuid.UUID
	endpointURL   string
	endpointOn    bool
	secretID      uuid.UUID
	secretVersion int
	nonce         []byte
	ciphertext    []byte
	payload       []byte
	eventID       uuid.UUID
}

func (s *Service) HandleJob(ctx context.Context, job jobs.Job) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.DeliveryID == uuid.Nil {
		return errors.New("invalid_webhook_job")
	}
	target, skip, err := s.startDelivery(ctx, payload.DeliveryID, job.Attempts)
	if err != nil || skip {
		return err
	}
	started := time.Now()
	statusCode, responseHash, errorCode, retryable := s.send(ctx, target)
	duration := int(time.Since(started).Milliseconds())
	if duration < 0 {
		duration = 0
	}
	terminal := errorCode == "" || !retryable || job.Attempts >= job.MaxAttempts
	delay := retryDelay(job.Attempts)
	if err := s.recordAttempt(ctx, target.deliveryID, job.Attempts, statusCode, responseHash, errorCode, duration, terminal, delay); err != nil {
		return err
	}
	if errorCode == "" || !retryable {
		return nil
	}
	return deliveryFailure{code: errorCode, delay: delay}
}

func (s *Service) startDelivery(ctx context.Context, deliveryID uuid.UUID, attempt int) (deliveryTarget, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return deliveryTarget{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var target deliveryTarget
	var deliveryStatus, endpointStatus string
	err = tx.QueryRow(ctx, `
		SELECT d.id,e.id,e.url,e.status,d.status,r.id,r.version,r.nonce,r.ciphertext,v.payload,v.id
		FROM developer_webhook_deliveries d
		JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id
		JOIN developer_webhook_secret_revisions r ON r.id=d.secret_revision_id
		JOIN developer_webhook_events v ON v.id=d.event_id
		WHERE d.id=$1 FOR UPDATE`, deliveryID).Scan(&target.deliveryID, &target.endpointID, &target.endpointURL, &endpointStatus, &deliveryStatus, &target.secretID, &target.secretVersion, &target.nonce, &target.ciphertext, &target.payload, &target.eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliveryTarget{}, true, nil
	}
	if err != nil {
		return deliveryTarget{}, false, err
	}
	if deliveryStatus == "succeeded" || deliveryStatus == "dead_letter" || deliveryStatus == "cancelled" {
		return deliveryTarget{}, true, nil
	}
	if endpointStatus != "active" {
		if _, err := tx.Exec(ctx, `UPDATE developer_webhook_deliveries SET status='cancelled',version=version+1,updated_at=now(),next_attempt_at=NULL WHERE id=$1`, deliveryID); err != nil {
			return deliveryTarget{}, false, err
		}
		return deliveryTarget{}, true, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE developer_webhook_deliveries SET status='delivering',attempt_count=GREATEST(attempt_count,$2),version=version+1,updated_at=now(),next_attempt_at=NULL WHERE id=$1`, deliveryID, attempt); err != nil {
		return deliveryTarget{}, false, err
	}
	return target, false, tx.Commit(ctx)
}

func (s *Service) send(ctx context.Context, target deliveryTarget) (*int, *string, string, bool) {
	secret, err := s.decryptSecret(target.endpointID, target.secretVersion, target.nonce, target.ciphertext)
	if err != nil {
		return nil, nil, "secret_decryption_failed", false
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(target.payload)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.endpointURL, strings.NewReader(string(target.payload)))
	if err != nil {
		return nil, nil, "invalid_target", false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "HCAI-Webhook/1.0")
	request.Header.Set("HCAI-Webhook-Id", target.eventID.String())
	request.Header.Set("HCAI-Webhook-Timestamp", timestamp)
	request.Header.Set("HCAI-Webhook-Signature", signature)
	client := &http.Client{Timeout: 10 * time.Second, Transport: s.transport(), CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect_blocked") }}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, "network_error", true
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	hash := sha256Hex(body)
	code := response.StatusCode
	if code >= 200 && code < 300 {
		return &code, &hash, "", false
	}
	retryable := code == http.StatusRequestTimeout || code == http.StatusConflict || code == http.StatusTooEarly || code == http.StatusTooManyRequests || code >= 500
	return &code, &hash, "http_" + strconv.Itoa(code), retryable
}

func (s *Service) recordAttempt(ctx context.Context, deliveryID uuid.UUID, attempt int, statusCode *int, responseHash *string, errorCode string, duration int, terminal bool, delay time.Duration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO developer_webhook_delivery_attempts(delivery_id,attempt_number,status_code,error_code,response_sha256,duration_ms) VALUES($1,$2,$3,NULLIF($4,''),$5,$6) ON CONFLICT(delivery_id,attempt_number) DO NOTHING`, deliveryID, attempt, statusCode, errorCode, responseHash, duration); err != nil {
		return err
	}
	status := "succeeded"
	if errorCode != "" && terminal {
		status = "dead_letter"
	} else if errorCode != "" {
		status = "retry_scheduled"
	}
	_, err = tx.Exec(ctx, `
		UPDATE developer_webhook_deliveries SET status=$2,version=version+1,attempt_count=GREATEST(attempt_count,$3),
		last_status_code=$4,last_error_code=NULLIF($5,''),next_attempt_at=CASE WHEN $2='retry_scheduled' THEN now()+$6::interval ELSE NULL END,
		succeeded_at=CASE WHEN $2='succeeded' THEN now() ELSE succeeded_at END,
		dead_lettered_at=CASE WHEN $2='dead_letter' THEN now() ELSE dead_lettered_at END,updated_at=now()
		WHERE id=$1`, deliveryID, status, attempt, statusCode, errorCode, fmt.Sprintf("%f seconds", delay.Seconds()))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) transport() *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                  nil,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		MaxResponseHeaderBytes: 64 * 1024,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid_target_address")
			}
			resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, errors.New("target_resolution_failed")
			}
			for _, ip := range resolved {
				if allowedIP(ip, s.allowLocal) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				}
			}
			return nil, errors.New("target_network_denied")
		},
	}
}

func retryDelay(attempt int) time.Duration {
	schedule := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	if attempt < 1 {
		return schedule[0]
	}
	if attempt > len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempt-1]
}
