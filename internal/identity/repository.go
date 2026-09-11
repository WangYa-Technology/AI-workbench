package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookie      = "hcai_session"
	DemoToken          = "hcai_local_demo_creator_session_v1"
	DemoPublisherToken = "hcai_local_demo_publisher_session_v1"
	DemoAdminToken     = "hcai_local_demo_admin_session_v1"
	DemoUserID         = "00000000-0000-4000-8000-000000000002"
	DemoPublisherID    = "00000000-0000-4000-8000-000000000003"
	DemoAdminID        = "00000000-0000-4000-8000-000000000004"
	sessionLifetime    = 30 * 24 * time.Hour
)

var (
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrInvalid              = errors.New("invalid identity input")
	ErrInvalidLogin         = errors.New("invalid email or password")
	ErrConflict             = errors.New("identity already exists")
	ErrInactive             = errors.New("account is not active")
	ErrNotFound             = errors.New("identity resource not found")
	ErrInvalidSessionFilter = errors.New("invalid session filter")
	handlePattern           = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)
)

type User struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"displayName"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	Locale        string    `json:"locale"`
	Timezone      string    `json:"timezone"`
	EmailVerified bool      `json:"emailVerified"`
	Permissions   []string  `json:"permissions"`
}

type ClientInfo struct {
	Label       string
	NetworkHash string
	RequestID   string
}

type RegisterInput struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AccountExists reports whether an active account owns the normalized email.
// The unified auth flow intentionally exposes this branch so the UI can offer
// the appropriate password/code or registration form.
func (r *Repository) AccountExists(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return false, ErrInvalid
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1 AND status='active')`, email).Scan(&exists); err != nil {
		return false, fmt.Errorf("check account email: %w", err)
	}
	return exists, nil
}

// UserByIDTx reads a user while the caller owns the transaction. It is used by
// atomic authentication challenge completion.
func (r *Repository) UserByIDTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (User, error) {
	var user User
	err := tx.QueryRow(ctx, `
		SELECT u.id,u.email,u.handle,u.display_name,u.role,u.status,u.locale,u.timezone,u.email_verified_at IS NOT NULL,
		       ARRAY(SELECT rp.permission_id FROM role_permissions rp WHERE rp.role=u.role ORDER BY rp.permission_id)
		FROM users u WHERE u.id=$1`, userID).Scan(
		&user.ID, &user.Email, &user.Handle, &user.DisplayName, &user.Role, &user.Status, &user.Locale, &user.Timezone, &user.EmailVerified, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read account in transaction: %w", err)
	}
	return user, nil
}

// LoginWithCodeTx creates a session for an already verified challenge. The
// challenge service calls this before committing its consume transaction.
func (r *Repository) LoginWithCodeTx(ctx context.Context, tx pgx.Tx, email string, client ClientInfo) (User, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return User{}, "", ErrInvalidLogin
	}
	var userID uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, `SELECT id,status FROM users WHERE lower(email)=$1`, email).Scan(&userID, &status); errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrInvalidLogin
	} else if err != nil {
		return User{}, "", fmt.Errorf("read code login account: %w", err)
	}
	if status != "active" {
		return User{}, "", ErrInactive
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()),updated_at=now() WHERE id=$1 AND status='active'`, userID); err != nil {
		return User{}, "", fmt.Errorf("verify code login email: %w", err)
	}
	token, sessionID, err := issueSession(ctx, tx, userID, client)
	if err != nil {
		return User{}, "", err
	}
	if err := insertAudit(ctx, tx, userID, "identity.login_code", "session", sessionID, client.RequestID, nil); err != nil {
		return User{}, "", err
	}
	user, err := r.UserByIDTx(ctx, tx, userID)
	return user, token, err
}

// RegisterVerifiedTx creates an account only after the caller has verified the
// email challenge in the same transaction. The resulting session is returned
// to the caller so registration is immediately usable.
func (r *Repository) RegisterVerifiedTx(ctx context.Context, tx pgx.Tx, input RegisterInput, client ClientInfo) (User, string, error) {
	email, handle, displayName, locale, timezone, err := validateRegistration(input)
	if err != nil {
		return User{}, "", err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", fmt.Errorf("hash password: %w", err)
	}
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Registrations); err != nil {
		return User{}, "", err
	}
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users(email,handle,display_name,password_hash,role,status,locale,timezone,email_verified_at)
		VALUES($1,$2,$3,$4,'member','active',$5,$6,now())
		RETURNING id`, email, handle, displayName, string(passwordHash), locale, timezone).Scan(&userID)
	if isUniqueViolation(err) {
		return User{}, "", ErrConflict
	}
	if err != nil {
		return User{}, "", fmt.Errorf("create verified account: %w", err)
	}
	token, sessionID, err := issueSession(ctx, tx, userID, client)
	if err != nil {
		return User{}, "", err
	}
	if _, err := risk.RecordAccountLinkTx(ctx, tx, userID, normalizedClient(client).NetworkHash); err != nil {
		return User{}, "", fmt.Errorf("record account-link risk: %w", err)
	}
	if err := insertAudit(ctx, tx, userID, "identity.registered_verified", "user", userID, client.RequestID, map[string]any{"sessionId": sessionID}); err != nil {
		return User{}, "", err
	}
	user, err := r.UserByIDTx(ctx, tx, userID)
	return user, token, err
}

type ProfileInput struct {
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

type Session struct {
	ID          uuid.UUID  `json:"id"`
	ClientLabel string     `json:"clientLabel"`
	NetworkHint string     `json:"networkHint,omitempty"`
	Status      string     `json:"status"`
	Current     bool       `json:"current"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

type SessionListInput struct {
	Cursor string
	Limit  int
}

type SessionPage struct {
	Items      []Session `json:"items"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type sessionCursor struct {
	LastSeenAt time.Time `json:"lastSeenAt"`
	ID         uuid.UUID `json:"id"`
}

type OAuthProvider struct {
	Provider   string `json:"provider"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func HashNetwork(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("hcai-session-network:" + value))
	return hex.EncodeToString(sum[:])
}

func (r *Repository) Authenticate(ctx context.Context, token string) (User, error) {
	user, _, err := r.authenticate(ctx, token)
	return user, err
}

func (r *Repository) authenticate(ctx context.Context, token string) (User, uuid.UUID, error) {
	if token == "" {
		return User{}, uuid.Nil, ErrUnauthenticated
	}
	var user User
	var sessionID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT s.id,u.id,u.email,u.handle,u.display_name,u.role,u.status,u.locale,u.timezone,u.email_verified_at IS NOT NULL,
		       ARRAY(SELECT rp.permission_id FROM role_permissions rp WHERE rp.role=u.role ORDER BY rp.permission_id)
		FROM sessions s
		JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > now() AND u.status='active'`, HashToken(token)).Scan(
		&sessionID, &user.ID, &user.Email, &user.Handle, &user.DisplayName, &user.Role, &user.Status,
		&user.Locale, &user.Timezone, &user.EmailVerified, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, uuid.Nil, ErrUnauthenticated
	}
	if err != nil {
		return User{}, uuid.Nil, fmt.Errorf("authenticate session: %w", err)
	}
	_, _ = r.pool.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '1 minute'`, sessionID)
	return user, sessionID, nil
}

func (r *Repository) Register(ctx context.Context, input RegisterInput, client ClientInfo) (User, string, error) {
	email, handle, displayName, locale, timezone, err := validateRegistration(input)
	if err != nil {
		return User{}, "", err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", fmt.Errorf("hash password: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, "", fmt.Errorf("begin registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := systemsettings.RequireTx(ctx, tx, systemsettings.Registrations); err != nil {
		return User{}, "", err
	}
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users(email,handle,display_name,password_hash,role,status,locale,timezone)
		VALUES($1,$2,$3,$4,'member','active',$5,$6)
		RETURNING id`, email, handle, displayName, string(passwordHash), locale, timezone).Scan(&userID)
	if isUniqueViolation(err) {
		return User{}, "", ErrConflict
	}
	if err != nil {
		return User{}, "", fmt.Errorf("create account: %w", err)
	}
	token, sessionID, err := issueSession(ctx, tx, userID, client)
	if err != nil {
		return User{}, "", err
	}
	if _, err := risk.RecordAccountLinkTx(ctx, tx, userID, normalizedClient(client).NetworkHash); err != nil {
		return User{}, "", fmt.Errorf("record account-link risk: %w", err)
	}
	if err := insertAudit(ctx, tx, userID, "identity.registered", "user", userID, client.RequestID, map[string]any{"sessionId": sessionID}); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, "", fmt.Errorf("commit registration: %w", err)
	}
	user, err := r.userByID(ctx, userID)
	return user, token, err
}

func (r *Repository) Login(ctx context.Context, input LoginInput, client ClientInfo) (User, string, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) || len(input.Password) < 1 || len(input.Password) > 128 {
		return User{}, "", ErrInvalidLogin
	}
	var userID uuid.UUID
	var passwordHash, status string
	err := r.pool.QueryRow(ctx, `SELECT id,COALESCE(password_hash,''),status FROM users WHERE lower(email)=$1`, email).Scan(&userID, &passwordHash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrInvalidLogin
	}
	if err != nil {
		return User{}, "", fmt.Errorf("read credentials: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)) != nil {
		return User{}, "", ErrInvalidLogin
	}
	if status != "active" {
		return User{}, "", ErrInactive
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, "", fmt.Errorf("begin login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	token, sessionID, err := issueSession(ctx, tx, userID, client)
	if err != nil {
		return User{}, "", err
	}
	if err := insertAudit(ctx, tx, userID, "identity.login", "session", sessionID, client.RequestID, nil); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, "", fmt.Errorf("commit login: %w", err)
	}
	user, err := r.userByID(ctx, userID)
	return user, token, err
}

func (r *Repository) UpdateProfile(ctx context.Context, userID uuid.UUID, input ProfileInput, requestID string) (User, error) {
	displayName, locale, timezone, err := validateProfile(input.DisplayName, input.Locale, input.Timezone)
	if err != nil {
		return User{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin profile update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE users SET display_name=$2,locale=$3,timezone=$4,updated_at=now() WHERE id=$1 AND status='active'`, userID, displayName, locale, timezone)
	if err != nil {
		return User{}, fmt.Errorf("update profile: %w", err)
	}
	if result.RowsAffected() == 0 {
		return User{}, ErrNotFound
	}
	if err := insertAudit(ctx, tx, userID, "identity.profile_updated", "user", userID, requestID, map[string]any{"locale": locale, "timezone": timezone}); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit profile update: %w", err)
	}
	return r.userByID(ctx, userID)
}

func (r *Repository) ListSessions(ctx context.Context, userID uuid.UUID, currentToken string, input SessionListInput) (SessionPage, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return SessionPage{}, ErrInvalidSessionFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeSessionCursor(input.Cursor)
		if err != nil {
			return SessionPage{}, err
		}
		cursorTime, cursorID = &cursor.LastSeenAt, &cursor.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,client_label,COALESCE(left(network_hash,8),''),
		       CASE WHEN revoked_at IS NOT NULL THEN 'revoked' WHEN expires_at <= now() THEN 'expired' ELSE 'active' END,
		       token_hash=$2,created_at,last_seen_at,expires_at,revoked_at
		FROM sessions WHERE user_id=$1
		  AND ($3::timestamptz IS NULL OR (last_seen_at,id)<($3,$4::uuid))
		ORDER BY last_seen_at DESC,id DESC LIMIT $5`, userID, HashToken(currentToken), cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return SessionPage{}, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	items := make([]Session, 0)
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.ClientLabel, &item.NetworkHint, &item.Status, &item.Current, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &item.RevokedAt); err != nil {
			return SessionPage{}, fmt.Errorf("scan session: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return SessionPage{}, err
	}
	page := SessionPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeSessionCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (r *Repository) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, currentToken, requestID string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current bool
	err = tx.QueryRow(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2 RETURNING token_hash=$3`, sessionID, userID, HashToken(currentToken)).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	if err := insertAudit(ctx, tx, userID, "identity.session_revoked", "session", sessionID, requestID, nil); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit session revocation: %w", err)
	}
	return current, nil
}

func (r *Repository) RevokeOtherSessions(ctx context.Context, userID uuid.UUID, currentToken, requestID string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin other session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND token_hash<>$2 AND revoked_at IS NULL AND expires_at>now()`, userID, HashToken(currentToken))
	if err != nil {
		return 0, fmt.Errorf("revoke other sessions: %w", err)
	}
	count := result.RowsAffected()
	if err := insertAudit(ctx, tx, userID, "identity.other_sessions_revoked", "user", userID, requestID, map[string]any{"count": count}); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit other session revocation: %w", err)
	}
	return count, nil
}

func (r *Repository) ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error) {
	rows, err := r.pool.Query(ctx, `SELECT provider,display_name,enabled,client_id IS NOT NULL AND redirect_uri IS NOT NULL AND secret_reference IS NOT NULL FROM oauth_provider_configs ORDER BY display_name`)
	if err != nil {
		return nil, fmt.Errorf("list oauth providers: %w", err)
	}
	defer rows.Close()
	items := make([]OAuthProvider, 0, 2)
	for rows.Next() {
		var item OAuthProvider
		var enabled bool
		if err := rows.Scan(&item.Provider, &item.Name, &enabled, &item.Configured); err != nil {
			return nil, fmt.Errorf("scan oauth provider: %w", err)
		}
		item.Available = false
		if !enabled || !item.Configured {
			item.Reason = "External credentials are not configured in this environment."
		} else {
			item.Reason = "OAuth exchange is disabled until the provider adapter passes staging verification."
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) StartDemoSession(ctx context.Context, actor string, client ...ClientInfo) (User, string, error) {
	userID := uuid.MustParse(DemoUserID)
	token := DemoToken
	if actor == "publisher" {
		userID = uuid.MustParse(DemoPublisherID)
		token = DemoPublisherToken
	} else if actor == "admin" {
		userID = uuid.MustParse(DemoAdminID)
		token = DemoAdminToken
	}
	info := ClientInfo{Label: "Local demo session", RequestID: "local-demo"}
	if len(client) > 0 {
		info = normalizedClient(client[0])
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, "", fmt.Errorf("begin demo session: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions(user_id,token_hash,expires_at,client_label,network_hash,last_seen_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (token_hash) DO UPDATE SET user_id=excluded.user_id,expires_at=excluded.expires_at,
		  client_label=excluded.client_label,network_hash=excluded.network_hash,last_seen_at=now(),revoked_at=NULL`,
		userID, HashToken(token), time.Now().Add(sessionLifetime), info.Label, nullable(info.NetworkHash),
	)
	if err != nil {
		return User{}, "", fmt.Errorf("start demo session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, "", fmt.Errorf("commit demo session: %w", err)
	}
	user, err := r.Authenticate(ctx, token)
	return user, token, err
}

func (r *Repository) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, HashToken(token))
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (r *Repository) userByID(ctx context.Context, userID uuid.UUID) (User, error) {
	var user User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id,u.email,u.handle,u.display_name,u.role,u.status,u.locale,u.timezone,u.email_verified_at IS NOT NULL,
		       ARRAY(SELECT rp.permission_id FROM role_permissions rp WHERE rp.role=u.role ORDER BY rp.permission_id)
		FROM users u WHERE u.id=$1`, userID).Scan(
		&user.ID, &user.Email, &user.Handle, &user.DisplayName, &user.Role, &user.Status, &user.Locale, &user.Timezone, &user.EmailVerified, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read account: %w", err)
	}
	return user, nil
}

func issueSession(ctx context.Context, tx pgx.Tx, userID uuid.UUID, client ClientInfo) (string, uuid.UUID, error) {
	client = normalizedClient(client)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", uuid.Nil, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	var sessionID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO sessions(user_id,token_hash,expires_at,client_label,network_hash,last_seen_at)
		VALUES($1,$2,$3,$4,$5,now()) RETURNING id`,
		userID, HashToken(token), time.Now().Add(sessionLifetime), client.Label, nullable(client.NetworkHash)).Scan(&sessionID)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("issue session: %w", err)
	}
	return token, sessionID, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action, resourceType string, resourceID uuid.UUID, requestID string, metadata map[string]any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "identity-local"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode identity audit: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,$2,$3,$4,$5,$6)`, actorID, action, resourceType, resourceID, requestID, body)
	if err != nil {
		return fmt.Errorf("write identity audit: %w", err)
	}
	return nil
}

func validateRegistration(input RegisterInput) (string, string, string, string, string, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	handle := strings.ToLower(strings.TrimSpace(input.Handle))
	if !validEmail(email) || !handlePattern.MatchString(handle) || len(input.Password) < 10 || len(input.Password) > 128 {
		return "", "", "", "", "", ErrInvalid
	}
	displayName, locale, timezone, err := validateProfile(input.DisplayName, input.Locale, input.Timezone)
	return email, handle, displayName, locale, timezone, err
}

func validateProfile(displayName, locale, timezone string) (string, string, string, error) {
	displayName = strings.TrimSpace(displayName)
	locale = strings.TrimSpace(locale)
	timezone = strings.TrimSpace(timezone)
	if len(displayName) < 2 || len(displayName) > 80 || (locale != "en-US" && locale != "zh-CN") || timezone == "" || len(timezone) > 80 {
		return "", "", "", ErrInvalid
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", "", "", ErrInvalid
	}
	return displayName, locale, timezone, nil
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(parsed.Address, value) && strings.Contains(value, "@")
}

func normalizedClient(client ClientInfo) ClientInfo {
	client.Label = strings.TrimSpace(client.Label)
	if client.Label == "" {
		client.Label = "Unknown client"
	}
	if len(client.Label) > 80 {
		client.Label = client.Label[:80]
	}
	if len(client.NetworkHash) > 64 {
		client.NetworkHash = client.NetworkHash[:64]
	}
	return client
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func encodeSessionCursor(item Session) string {
	body, _ := json.Marshal(sessionCursor{LastSeenAt: item.LastSeenAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeSessionCursor(value string) (sessionCursor, error) {
	var cursor sessionCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.LastSeenAt.IsZero() || cursor.ID == uuid.Nil {
		return sessionCursor{}, ErrInvalidSessionFilter
	}
	return cursor, nil
}

func SortedPermissions(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
