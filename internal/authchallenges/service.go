// Package authchallenges owns the short-lived email-code flow used by the
// unified sign-in/register screen. It deliberately has no user foreign key:
// registration challenges must exist before an account is created.
package authchallenges

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DeliveryJobKind   = "identity.auth_challenge.deliver"
	ExpiryJobKind     = "identity.auth_challenge.expire"
	LoginCode         = "login_code"
	RegistrationCode  = "registration_code"
	challengeLifetime = 10 * time.Minute
	maxVerifyAttempts = 10
)

var (
	ErrInvalid     = errors.New("invalid authentication challenge")
	ErrNotFound    = errors.New("authentication challenge not found")
	ErrExpired     = errors.New("authentication challenge expired")
	ErrConflict    = errors.New("authentication challenge state conflict")
	ErrInvalidCode = errors.New("authentication code is invalid")
	ErrRateLimited = errors.New("authentication code rate limited")
)

type Challenge struct {
	ID             uuid.UUID `json:"challengeId"`
	Purpose        string    `json:"purpose"`
	EmailHint      string    `json:"emailHint"`
	ExpiresAt      time.Time `json:"expiresAt"`
	ResendAfterSec int       `json:"resendAfterSeconds"`
}

type Service struct {
	pool     *pgxpool.Pool
	key      []byte
	mode     string
	mailRoot string
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, key []byte, mode, mediaRoot string) *Service {
	return &Service{pool: pool, key: append([]byte(nil), key...), mode: mode, mailRoot: filepath.Join(mediaRoot, "mailbox"), now: time.Now}
}

func (s *Service) Start(ctx context.Context, email, purpose, locale, requestID string) (Challenge, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	locale = strings.TrimSpace(locale)
	if !validEmail(email) || (purpose != LoginCode && purpose != RegistrationCode) || (locale != "en-US" && locale != "zh-CN") || len(s.key) != 32 {
		return Challenge{}, ErrInvalid
	}
	code, err := newCode()
	if err != nil {
		return Challenge{}, err
	}
	challengeID := uuid.New()
	nonce, ciphertext, err := s.encrypt(challengeID, purpose, code)
	if err != nil {
		return Challenge{}, err
	}
	expiresAt := s.now().UTC().Add(challengeLifetime)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Challenge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var recentlyRequested bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_auth_challenges WHERE lower(email_snapshot)=lower($1) AND purpose=$2 AND created_at > now()-interval '30 seconds')`, email, purpose).Scan(&recentlyRequested); err != nil {
		return Challenge{}, err
	}
	if recentlyRequested {
		return Challenge{}, ErrRateLimited
	}
	rows, err := tx.Query(ctx, `
		UPDATE identity_auth_challenges
		SET status='cancelled',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,cancelled_at=now(),updated_at=now()
		WHERE lower(email_snapshot)=lower($1) AND purpose=$2 AND status IN ('queued','delivered')
		RETURNING id`, email, purpose)
	if err != nil {
		return Challenge{}, err
	}
	var stale []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Challenge{}, err
		}
		stale = append(stale, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Challenge{}, err
	}
	if len(stale) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'challengeId'=ANY($3::text[])`, DeliveryJobKind, ExpiryJobKind, uuidStrings(stale)); err != nil {
			return Challenge{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_auth_challenges(id,email_snapshot,purpose,locale,code_hash,code_nonce,code_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, challengeID, email, purpose, locale, hashCode(code), nonce, ciphertext, expiresAt); err != nil {
		return Challenge{}, err
	}
	payload, _ := json.Marshal(map[string]any{"challengeId": challengeID})
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,5),($3,$2,3)`, DeliveryJobKind, payload, ExpiryJobKind); err != nil {
		return Challenge{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET available_at=$2 WHERE kind=$1 AND payload->>'challengeId'=$3 AND status='queued'`, ExpiryJobKind, expiresAt, challengeID.String()); err != nil {
		return Challenge{}, err
	}
	if strings.TrimSpace(requestID) == "" {
		requestID = "auth-challenge-local"
	}
	metadata, _ := json.Marshal(map[string]any{"purpose": purpose})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES(NULL,$1,'identity_auth_challenge',$2,$3,$4)`, "identity.auth_challenge_requested", challengeID, requestID, metadata); err != nil {
		return Challenge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Challenge{}, err
	}
	for _, id := range stale {
		_ = s.removeMailbox(id)
	}
	return Challenge{ID: challengeID, Purpose: purpose, EmailHint: maskEmail(email), ExpiresAt: expiresAt, ResendAfterSec: 30}, nil
}

func (s *Service) ConfirmLogin(ctx context.Context, challengeID uuid.UUID, email, code string, client identity.ClientInfo) (identity.User, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if challengeID == uuid.Nil || !validEmail(email) || !validCode(code) {
		return identity.User{}, "", ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.User{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storedEmail, purpose, status string
	var expiresAt time.Time
	var verifyAttempts int
	err = tx.QueryRow(ctx, `SELECT email_snapshot,purpose,status,expires_at,verify_attempt_count FROM identity_auth_challenges WHERE id=$1 FOR UPDATE`, challengeID).Scan(&storedEmail, &purpose, &status, &expiresAt, &verifyAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.User{}, "", ErrNotFound
	}
	if err != nil {
		return identity.User{}, "", err
	}
	if !strings.EqualFold(storedEmail, email) || purpose != LoginCode {
		return identity.User{}, "", ErrNotFound
	}
	if !expiresAt.After(s.now()) {
		_, _ = tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='expired',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,updated_at=now() WHERE id=$1`, challengeID)
		_ = tx.Commit(ctx)
		return identity.User{}, "", ErrExpired
	}
	if status != "queued" && status != "delivered" {
		return identity.User{}, "", ErrConflict
	}
	if verifyAttempts >= maxVerifyAttempts {
		return identity.User{}, "", ErrRateLimited
	}
	if hashCode(code) != currentCodeHash(ctx, tx, challengeID) {
		_, _ = tx.Exec(ctx, `UPDATE identity_auth_challenges SET verify_attempt_count=verify_attempt_count+1,updated_at=now() WHERE id=$1`, challengeID)
		_ = tx.Commit(ctx)
		return identity.User{}, "", ErrInvalidCode
	}
	user, token, err := s.consumeLogin(ctx, tx, challengeID, storedEmail, client)
	if err != nil {
		return identity.User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.User{}, "", err
	}
	_ = s.removeMailbox(challengeID)
	return user, token, nil
}

func (s *Service) consumeLogin(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, email string, client identity.ClientInfo) (identity.User, string, error) {
	user, token, err := identity.NewRepository(s.pool).LoginWithCodeTx(ctx, tx, email, client)
	if err != nil {
		return identity.User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='consumed',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,consumed_at=now(),updated_at=now() WHERE id=$1`, challengeID); err != nil {
		return identity.User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'challengeId'=$3`, DeliveryJobKind, ExpiryJobKind, challengeID.String()); err != nil {
		return identity.User{}, "", err
	}
	return user, token, nil
}

func (s *Service) CompleteRegistration(ctx context.Context, challengeID uuid.UUID, input identity.RegisterInput, code string, client identity.ClientInfo) (identity.User, string, error) {
	if challengeID == uuid.Nil || !validCode(code) {
		return identity.User{}, "", ErrInvalid
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	input.Email = email
	if !validEmail(email) {
		return identity.User{}, "", ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.User{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var storedEmail, purpose, status string
	var codeHash *string
	var expiresAt time.Time
	var attempts int
	err = tx.QueryRow(ctx, `SELECT email_snapshot,purpose,status,code_hash,expires_at,verify_attempt_count FROM identity_auth_challenges WHERE id=$1 FOR UPDATE`, challengeID).Scan(&storedEmail, &purpose, &status, &codeHash, &expiresAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.User{}, "", ErrNotFound
	}
	if err != nil {
		return identity.User{}, "", err
	}
	if !strings.EqualFold(storedEmail, email) || purpose != RegistrationCode {
		return identity.User{}, "", ErrNotFound
	}
	if !expiresAt.After(s.now()) {
		_, _ = tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='expired',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,updated_at=now() WHERE id=$1`, challengeID)
		_ = tx.Commit(ctx)
		return identity.User{}, "", ErrExpired
	}
	if status != "queued" && status != "delivered" {
		return identity.User{}, "", ErrConflict
	}
	if attempts >= maxVerifyAttempts {
		return identity.User{}, "", ErrRateLimited
	}
	if codeHash == nil || hashCode(code) != *codeHash {
		_, _ = tx.Exec(ctx, `UPDATE identity_auth_challenges SET verify_attempt_count=verify_attempt_count+1,updated_at=now() WHERE id=$1`, challengeID)
		_ = tx.Commit(ctx)
		return identity.User{}, "", ErrInvalidCode
	}
	user, token, err := identity.NewRepository(s.pool).RegisterVerifiedTx(ctx, tx, input, client)
	if err != nil {
		return identity.User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='consumed',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,consumed_at=now(),updated_at=now() WHERE id=$1`, challengeID); err != nil {
		return identity.User{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'challengeId'=$3`, DeliveryJobKind, ExpiryJobKind, challengeID.String()); err != nil {
		return identity.User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.User{}, "", err
	}
	_ = s.removeMailbox(challengeID)
	return user, token, nil
}

func (s *Service) HandleDeliveryJob(ctx context.Context, job jobs.Job) error {
	id, err := challengeIDFromJob(job)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var email, purpose, locale, status, codeHash string
	var nonce, ciphertext []byte
	var expiresAt time.Time
	var attemptCount int
	err = tx.QueryRow(ctx, `SELECT email_snapshot,purpose,locale,status,code_hash,code_nonce,code_ciphertext,expires_at,attempt_count FROM identity_auth_challenges WHERE id=$1 FOR UPDATE`, id).Scan(&email, &purpose, &locale, &status, &codeHash, &nonce, &ciphertext, &expiresAt, &attemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "queued" {
		return nil
	}
	if !expiresAt.After(s.now()) {
		_, _ = tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='expired',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,updated_at=now() WHERE id=$1`, id)
		_ = tx.Commit(ctx)
		return nil
	}
	attemptNumber := attemptCount + 1
	if s.mode == "disabled" {
		return s.deliveryFailure(ctx, tx, job, id, attemptNumber, "delivery_disabled")
	}
	code, err := s.decrypt(id, purpose, nonce, ciphertext)
	if err != nil {
		return s.deliveryFailure(ctx, tx, job, id, attemptNumber, "code_decryption_failed")
	}
	content := s.render(email, locale, purpose, code, expiresAt)
	if err := s.writeMailbox(id, content); err != nil {
		return s.deliveryFailure(ctx, tx, job, id, attemptNumber, "local_mailbox_write_failed")
	}
	receipt := sha256.Sum256(content)
	if _, err := tx.Exec(ctx, `INSERT INTO identity_auth_challenge_delivery_attempts(challenge_id,attempt_number,adapter,status,receipt_sha256) VALUES($1,$2,'local_file','delivered',$3)`, id, attemptNumber, hex.EncodeToString(receipt[:])); err != nil {
		_ = s.removeMailbox(id)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='delivered',attempt_count=$2,delivered_at=now(),updated_at=now() WHERE id=$1`, id, attemptNumber); err != nil {
		_ = s.removeMailbox(id)
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) deliveryFailure(ctx context.Context, tx pgx.Tx, job jobs.Job, id uuid.UUID, attempt int, code string) error {
	if attempt > 15 {
		return errors.New("auth challenge attempt limit reached")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_auth_challenge_delivery_attempts(challenge_id,attempt_number,adapter,status,error_code) VALUES($1,$2,$3,'failed',$4)`, id, attempt, s.mode, code); err != nil {
		return err
	}
	terminal := job.Attempts >= job.MaxAttempts || attempt >= 15
	var updateErr error
	if terminal {
		_, updateErr = tx.Exec(ctx, `UPDATE identity_auth_challenges SET status='dead_letter',attempt_count=$2,dead_lettered_at=now(),updated_at=now() WHERE id=$1`, id, attempt)
	} else {
		_, updateErr = tx.Exec(ctx, `UPDATE identity_auth_challenges SET attempt_count=$2,updated_at=now() WHERE id=$1`, id, attempt)
	}
	if updateErr != nil {
		return updateErr
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if terminal {
		return nil
	}
	return retryError{code: code, delay: time.Duration(job.Attempts*job.Attempts) * time.Second}
}

func (s *Service) HandleExpiryJob(ctx context.Context, job jobs.Job) error {
	id, err := challengeIDFromJob(job)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE identity_auth_challenges SET status='expired',code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,updated_at=now() WHERE id=$1 AND status IN ('queued','delivered') AND expires_at<=now()`, id)
	_ = s.removeMailbox(id)
	return err
}

func (s *Service) encrypt(id uuid.UUID, purpose, value string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, []byte(value), []byte(id.String()+":"+purpose)), nil
}

func (s *Service) decrypt(id uuid.UUID, purpose string, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(id.String()+":"+purpose))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) render(email, locale, purpose, code string, expiresAt time.Time) []byte {
	subject, intro, label := "Your HCAI CHAT sign-in code", "Use this one-time code to continue signing in:", "Sign-in code"
	if purpose == RegistrationCode {
		subject, intro, label = "Complete your HCAI CHAT registration", "Use this one-time code to verify your email and create your account:", "Registration code"
	}
	if locale == "zh-CN" {
		if purpose == RegistrationCode {
			subject, intro, label = "完成 HCAI CHAT 注册", "使用以下一次性验证码验证邮箱并创建账户：", "注册验证码"
		} else {
			subject, intro, label = "HCAI CHAT 登录验证码", "使用以下一次性验证码继续登录：", "登录验证码"
		}
	}
	body := fmt.Sprintf("From: HCAI CHAT <no-reply@hcai.local>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n%s: %s\r\n\r\nExpires: %s\r\n\r\nIf you did not request this code, you can ignore this message.\r\n", email, subject, intro, label, code, expiresAt.UTC().Format(time.RFC3339))
	return []byte(body)
}

func (s *Service) writeMailbox(id uuid.UUID, content []byte) error {
	directory := filepath.Join(s.mailRoot, "auth-challenges")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(directory, 0o700)
	temporary, err := os.CreateTemp(directory, ".email-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(directory, id.String()+".eml"))
}

func (s *Service) removeMailbox(id uuid.UUID) error {
	err := os.Remove(filepath.Join(s.mailRoot, "auth-challenges", id.String()+".eml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func currentCodeHash(ctx context.Context, tx pgx.Tx, id uuid.UUID) string {
	var value string
	_ = tx.QueryRow(ctx, `SELECT code_hash FROM identity_auth_challenges WHERE id=$1`, id).Scan(&value)
	return value
}
func challengeIDFromJob(job jobs.Job) (uuid.UUID, error) {
	var payload struct {
		ChallengeID uuid.UUID `json:"challengeId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ChallengeID == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}
	return payload.ChallengeID, nil
}
func newCode() (string, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", (uint32(raw[0])<<24|uint32(raw[1])<<16|uint32(raw[2])<<8|uint32(raw[3]))%1000000), nil
}
func validCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
func validEmail(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 254 && strings.Contains(value, "@")
}
func hashCode(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func maskEmail(value string) string {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return parts[0][:1] + "***@" + parts[1]
}
func uuidStrings(values []uuid.UUID) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}

type retryError struct {
	code  string
	delay time.Duration
}

func (e retryError) Error() string             { return e.code }
func (e retryError) RetryDelay() time.Duration { return e.delay }
