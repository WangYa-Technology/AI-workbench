package developer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDisabled        = errors.New("developer access disabled")
	ErrInvalid         = errors.New("invalid developer access input")
	ErrNotFound        = errors.New("developer access resource not found")
	ErrConflict        = errors.New("developer access conflict")
	ErrUnauthenticated = errors.New("invalid api key")
	ErrScope           = errors.New("api key scope required")
	ErrIP              = errors.New("api key ip address denied")
	namePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{1,78}[A-Za-z0-9]$`)
)

const identityReadScope = "developer:identity:read"

type Control struct {
	Enabled            bool      `json:"enabled"`
	MaxServiceAccounts int       `json:"maxServiceAccounts"`
	MaxActiveKeys      int       `json:"maxActiveKeys"`
	DefaultTTLDays     int       `json:"defaultTtlDays"`
	Version            int       `json:"version"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type APIKey struct {
	ID           uuid.UUID  `json:"id"`
	PublicPrefix string     `json:"publicPrefix"`
	DisplayHint  string     `json:"displayHint"`
	Scopes       []string   `json:"scopes"`
	IPAllowlist  []string   `json:"ipAllowlist"`
	Status       string     `json:"status"`
	Version      int        `json:"version"`
	UsageCount   int64      `json:"usageCount"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	RotatedToID  *uuid.UUID `json:"rotatedToId,omitempty"`
}

type ServiceAccount struct {
	ID          uuid.UUID `json:"id"`
	OwnerID     uuid.UUID `json:"ownerId"`
	OwnerHandle string    `json:"ownerHandle,omitempty"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Keys        []APIKey  `json:"keys"`
}

type Access struct {
	Control  Control          `json:"control"`
	Accounts []ServiceAccount `json:"accounts"`
}

type AccountCreate struct {
	Name string `json:"name"`
}

type KeyCreate struct {
	Scopes      []string `json:"scopes"`
	IPAllowlist []string `json:"ipAllowlist"`
	TTLDays     int      `json:"ttlDays"`
}

type Credential struct {
	APIKey
	PlaintextKey string `json:"plaintextKey"`
}

type Transition struct {
	ExpectedVersion int    `json:"expectedVersion"`
	Reason          string `json:"reason"`
	Confirmed       bool   `json:"confirmed"`
}

type ControlUpdate struct {
	Enabled            bool   `json:"enabled"`
	MaxServiceAccounts int    `json:"maxServiceAccounts"`
	MaxActiveKeys      int    `json:"maxActiveKeys"`
	DefaultTTLDays     int    `json:"defaultTtlDays"`
	ExpectedVersion    int    `json:"expectedVersion"`
	Reason             string `json:"reason"`
	Confirmed          bool   `json:"confirmed"`
}

type Principal struct {
	APIKeyID         uuid.UUID `json:"apiKeyId"`
	ServiceAccountID uuid.UUID `json:"serviceAccountId"`
	ServiceName      string    `json:"serviceAccountName"`
	OwnerID          uuid.UUID `json:"ownerId"`
	OwnerHandle      string    `json:"ownerHandle"`
	Scopes           []string  `json:"scopes"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) GetAccess(ctx context.Context, ownerID uuid.UUID) (Access, error) {
	control, err := s.GetControl(ctx)
	if err != nil {
		return Access{}, err
	}
	accounts, err := s.listAccounts(ctx, &ownerID)
	return Access{Control: control, Accounts: accounts}, err
}

func (s *Service) GetControl(ctx context.Context) (Control, error) {
	var item Control
	err := s.pool.QueryRow(ctx, `SELECT enabled,max_service_accounts,max_active_keys,default_ttl_days,version,updated_at FROM developer_access_control WHERE singleton=true`).Scan(
		&item.Enabled, &item.MaxServiceAccounts, &item.MaxActiveKeys, &item.DefaultTTLDays, &item.Version, &item.UpdatedAt)
	return item, err
}

func (s *Service) ListAll(ctx context.Context) (Access, error) {
	control, err := s.GetControl(ctx)
	if err != nil {
		return Access{}, err
	}
	accounts, err := s.listAccounts(ctx, nil)
	return Access{Control: control, Accounts: accounts}, err
}

func (s *Service) listAccounts(ctx context.Context, ownerID *uuid.UUID) ([]ServiceAccount, error) {
	query := `SELECT a.id,a.owner_id,u.handle,a.name,a.status,a.version,a.created_at,a.updated_at FROM developer_service_accounts a JOIN users u ON u.id=a.owner_id`
	args := []any{}
	if ownerID != nil {
		query += ` WHERE a.owner_id=$1`
		args = append(args, *ownerID)
	}
	query += ` ORDER BY a.created_at DESC,a.id DESC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ServiceAccount{}
	for rows.Next() {
		var item ServiceAccount
		if err := rows.Scan(&item.ID, &item.OwnerID, &item.OwnerHandle, &item.Name, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		keys, err := s.listKeys(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Keys = keys
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,public_prefix,display_hint,scopes,ip_allowlist,status,version,usage_count,last_used_at,expires_at,created_at,revoked_at,rotated_to_id FROM developer_api_keys WHERE service_account_id=$1 ORDER BY created_at DESC,id DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []APIKey{}
	for rows.Next() {
		var item APIKey
		if err := rows.Scan(&item.ID, &item.PublicPrefix, &item.DisplayHint, &item.Scopes, &item.IPAllowlist, &item.Status, &item.Version, &item.UsageCount, &item.LastUsedAt, &item.ExpiresAt, &item.CreatedAt, &item.RevokedAt, &item.RotatedToID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateAccount(ctx context.Context, ownerID uuid.UUID, input AccountCreate, requestID string) (ServiceAccount, error) {
	name := strings.TrimSpace(input.Name)
	if !namePattern.MatchString(name) {
		return ServiceAccount{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ServiceAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	control, err := controlTx(ctx, tx, true)
	if err != nil {
		return ServiceAccount{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM developer_service_accounts WHERE owner_id=$1 AND status='active'`, ownerID).Scan(&count); err != nil {
		return ServiceAccount{}, err
	}
	if count >= control.MaxServiceAccounts {
		return ServiceAccount{}, ErrConflict
	}
	var item ServiceAccount
	err = tx.QueryRow(ctx, `INSERT INTO developer_service_accounts(owner_id,name) VALUES($1,$2) RETURNING id,owner_id,name,status,version,created_at,updated_at`, ownerID, name).Scan(&item.ID, &item.OwnerID, &item.Name, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if uniqueViolation(err) {
		return ServiceAccount{}, ErrConflict
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	item.Keys = []APIKey{}
	if err := audit(ctx, tx, ownerID, "developer.service_account_created", "developer_service_account", item.ID, "", requestID, map[string]any{"name": name}); err != nil {
		return ServiceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccount{}, err
	}
	return item, nil
}

func (s *Service) IssueKey(ctx context.Context, ownerID, accountID uuid.UUID, input KeyCreate, requestID string) (Credential, error) {
	return s.issueKey(ctx, ownerID, accountID, nil, input, requestID)
}

func (s *Service) RotateKey(ctx context.Context, ownerID, accountID, keyID uuid.UUID, input KeyCreate, transition Transition, requestID string) (Credential, error) {
	if !validTransition(transition) {
		return Credential{}, ErrInvalid
	}
	return s.issueKey(ctx, ownerID, accountID, &keyRotation{id: keyID, expectedVersion: transition.ExpectedVersion, reason: strings.TrimSpace(transition.Reason)}, input, requestID)
}

type keyRotation struct {
	id              uuid.UUID
	expectedVersion int
	reason          string
}

func (s *Service) issueKey(ctx context.Context, ownerID, accountID uuid.UUID, rotation *keyRotation, input KeyCreate, requestID string) (Credential, error) {
	scopes, allowlist, ttl, err := validateKeyInput(input)
	if err != nil {
		return Credential{}, err
	}
	prefix, secret, hash, hint, err := newCredentialMaterial()
	if err != nil {
		return Credential{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	control, err := controlTx(ctx, tx, true)
	if err != nil {
		return Credential{}, err
	}
	if input.TTLDays == 0 {
		ttl = control.DefaultTTLDays
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM developer_service_accounts WHERE id=$1 AND owner_id=$2 FOR UPDATE`, accountID, ownerID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	} else if err != nil {
		return Credential{}, err
	}
	if status != "active" {
		return Credential{}, ErrConflict
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM developer_api_keys WHERE service_account_id=$1 AND status='active' AND expires_at>now()`, accountID).Scan(&active); err != nil {
		return Credential{}, err
	}
	if rotation == nil && active >= control.MaxActiveKeys {
		return Credential{}, ErrConflict
	}
	if rotation != nil {
		var oldStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM developer_api_keys WHERE id=$1 AND service_account_id=$2 AND version=$3 FOR UPDATE`, rotation.id, accountID, rotation.expectedVersion).Scan(&oldStatus); errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, ErrConflict
		} else if err != nil {
			return Credential{}, err
		}
		if oldStatus != "active" {
			return Credential{}, ErrConflict
		}
	}
	var item APIKey
	expires := time.Now().UTC().Add(time.Duration(ttl) * 24 * time.Hour)
	err = tx.QueryRow(ctx, `INSERT INTO developer_api_keys(service_account_id,public_prefix,secret_hash,display_hint,scopes,ip_allowlist,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,public_prefix,display_hint,scopes,ip_allowlist,status,version,usage_count,last_used_at,expires_at,created_at,revoked_at,rotated_to_id`, accountID, prefix, hash, hint, scopes, allowlist, expires).Scan(&item.ID, &item.PublicPrefix, &item.DisplayHint, &item.Scopes, &item.IPAllowlist, &item.Status, &item.Version, &item.UsageCount, &item.LastUsedAt, &item.ExpiresAt, &item.CreatedAt, &item.RevokedAt, &item.RotatedToID)
	if err != nil {
		return Credential{}, err
	}
	action := "developer.api_key_issued"
	reason := ""
	if rotation != nil {
		action = "developer.api_key_rotated"
		reason = rotation.reason
		if tag, err := tx.Exec(ctx, `UPDATE developer_api_keys SET status='rotated',revoked_at=now(),rotated_to_id=$1,version=version+1 WHERE id=$2 AND status='active'`, item.ID, rotation.id); err != nil {
			return Credential{}, err
		} else if tag.RowsAffected() != 1 {
			return Credential{}, ErrConflict
		}
	}
	if err := audit(ctx, tx, ownerID, action, "developer_api_key", item.ID, reason, requestID, map[string]any{"serviceAccountId": accountID, "publicPrefix": prefix, "scopes": scopes, "expiresAt": expires}); err != nil {
		return Credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, err
	}
	return Credential{APIKey: item, PlaintextKey: "hcai_sk_" + prefix + "_" + secret}, nil
}

func (s *Service) RevokeKey(ctx context.Context, ownerID, accountID, keyID uuid.UUID, input Transition, requestID string) (APIKey, error) {
	if !validTransition(input) {
		return APIKey{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item APIKey
	err = tx.QueryRow(ctx, `UPDATE developer_api_keys k SET status='revoked',revoked_at=now(),version=k.version+1 FROM developer_service_accounts a WHERE k.id=$1 AND k.service_account_id=$2 AND a.id=k.service_account_id AND a.owner_id=$3 AND k.status='active' AND k.version=$4 RETURNING k.id,k.public_prefix,k.display_hint,k.scopes,k.ip_allowlist,k.status,k.version,k.usage_count,k.last_used_at,k.expires_at,k.created_at,k.revoked_at,k.rotated_to_id`, keyID, accountID, ownerID, input.ExpectedVersion).Scan(&item.ID, &item.PublicPrefix, &item.DisplayHint, &item.Scopes, &item.IPAllowlist, &item.Status, &item.Version, &item.UsageCount, &item.LastUsedAt, &item.ExpiresAt, &item.CreatedAt, &item.RevokedAt, &item.RotatedToID)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrConflict
	}
	if err != nil {
		return APIKey{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.api_key_revoked", "developer_api_key", keyID, input.Reason, requestID, map[string]any{"serviceAccountId": accountID, "publicPrefix": item.PublicPrefix}); err != nil {
		return APIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, err
	}
	return item, nil
}

func (s *Service) RevokeAccount(ctx context.Context, ownerID, accountID uuid.UUID, input Transition, requestID string) (ServiceAccount, error) {
	if !validTransition(input) {
		return ServiceAccount{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item ServiceAccount
	err = tx.QueryRow(ctx, `UPDATE developer_service_accounts SET status='revoked',revoked_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND owner_id=$2 AND status='active' AND version=$3 RETURNING id,owner_id,name,status,version,created_at,updated_at`, accountID, ownerID, input.ExpectedVersion).Scan(&item.ID, &item.OwnerID, &item.Name, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceAccount{}, ErrConflict
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE developer_api_keys SET status='revoked',revoked_at=now(),version=version+1 WHERE service_account_id=$1 AND status='active'`, accountID); err != nil {
		return ServiceAccount{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.service_account_revoked", "developer_service_account", accountID, input.Reason, requestID, nil); err != nil {
		return ServiceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccount{}, err
	}
	item.Keys = []APIKey{}
	return item, nil
}

func (s *Service) AdminRevokeKey(ctx context.Context, actorID, keyID uuid.UUID, input Transition, requestID string) (APIKey, error) {
	if !validTransition(input) {
		return APIKey{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item APIKey
	var accountID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE developer_api_keys SET status='revoked',revoked_at=now(),version=version+1 WHERE id=$1 AND status='active' AND version=$2 RETURNING id,service_account_id,public_prefix,display_hint,scopes,ip_allowlist,status,version,usage_count,last_used_at,expires_at,created_at,revoked_at,rotated_to_id`, keyID, input.ExpectedVersion).Scan(&item.ID, &accountID, &item.PublicPrefix, &item.DisplayHint, &item.Scopes, &item.IPAllowlist, &item.Status, &item.Version, &item.UsageCount, &item.LastUsedAt, &item.ExpiresAt, &item.CreatedAt, &item.RevokedAt, &item.RotatedToID)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrConflict
	}
	if err != nil {
		return APIKey{}, err
	}
	if err := audit(ctx, tx, actorID, "admin.developer_api_key_revoked", "developer_api_key", keyID, input.Reason, requestID, map[string]any{"serviceAccountId": accountID, "publicPrefix": item.PublicPrefix}); err != nil {
		return APIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, err
	}
	return item, nil
}

func (s *Service) AdminRevokeAccount(ctx context.Context, actorID, accountID uuid.UUID, input Transition, requestID string) (ServiceAccount, error) {
	if !validTransition(input) {
		return ServiceAccount{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item ServiceAccount
	err = tx.QueryRow(ctx, `UPDATE developer_service_accounts SET status='revoked',revoked_at=now(),version=version+1,updated_at=now() WHERE id=$1 AND status='active' AND version=$2 RETURNING id,owner_id,name,status,version,created_at,updated_at`, accountID, input.ExpectedVersion).Scan(&item.ID, &item.OwnerID, &item.Name, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceAccount{}, ErrConflict
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE developer_api_keys SET status='revoked',revoked_at=now(),version=version+1 WHERE service_account_id=$1 AND status='active'`, accountID); err != nil {
		return ServiceAccount{}, err
	}
	if err := audit(ctx, tx, actorID, "admin.developer_service_account_revoked", "developer_service_account", accountID, input.Reason, requestID, map[string]any{"ownerId": item.OwnerID}); err != nil {
		return ServiceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccount{}, err
	}
	item.Keys = []APIKey{}
	return item, nil
}

func (s *Service) UpdateControl(ctx context.Context, actorID uuid.UUID, input ControlUpdate, requestID string) (Control, error) {
	if !input.Confirmed || len(strings.TrimSpace(input.Reason)) < 10 || input.ExpectedVersion < 1 || input.MaxServiceAccounts < 1 || input.MaxServiceAccounts > 20 || input.MaxActiveKeys < 1 || input.MaxActiveKeys > 10 || input.DefaultTTLDays < 1 || input.DefaultTTLDays > 365 {
		return Control{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Control{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item Control
	err = tx.QueryRow(ctx, `UPDATE developer_access_control SET enabled=$1,max_service_accounts=$2,max_active_keys=$3,default_ttl_days=$4,version=version+1,updated_at=now() WHERE singleton=true AND version=$5 RETURNING enabled,max_service_accounts,max_active_keys,default_ttl_days,version,updated_at`, input.Enabled, input.MaxServiceAccounts, input.MaxActiveKeys, input.DefaultTTLDays, input.ExpectedVersion).Scan(&item.Enabled, &item.MaxServiceAccounts, &item.MaxActiveKeys, &item.DefaultTTLDays, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Control{}, ErrConflict
	}
	if err != nil {
		return Control{}, err
	}
	if err := audit(ctx, tx, actorID, "admin.developer_access_updated", "developer_access_control", uuid.Nil, input.Reason, requestID, map[string]any{"enabled": item.Enabled, "version": item.Version, "maxServiceAccounts": item.MaxServiceAccounts, "maxActiveKeys": item.MaxActiveKeys, "defaultTtlDays": item.DefaultTTLDays}); err != nil {
		return Control{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Control{}, err
	}
	return item, nil
}

func (s *Service) Authenticate(ctx context.Context, raw, remoteAddress, requiredScope string) (Principal, error) {
	prefix, secret, ok := parseCredential(raw)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	var item Principal
	var secretHash, status, userStatus string
	var expires time.Time
	var allowlist []string
	err := s.pool.QueryRow(ctx, `SELECT k.id,a.id,a.name,a.owner_id,u.handle,k.scopes,k.secret_hash,k.status,k.expires_at,k.ip_allowlist,u.status FROM developer_api_keys k JOIN developer_service_accounts a ON a.id=k.service_account_id JOIN users u ON u.id=a.owner_id JOIN developer_access_control c ON c.singleton=true WHERE k.public_prefix=$1 AND c.enabled AND a.status='active'`, prefix).Scan(&item.APIKeyID, &item.ServiceAccountID, &item.ServiceName, &item.OwnerID, &item.OwnerHandle, &item.Scopes, &secretHash, &status, &expires, &allowlist, &userStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	sum := sha256.Sum256([]byte(secret))
	decoded, err := hex.DecodeString(secretHash)
	if err != nil || subtle.ConstantTimeCompare(sum[:], decoded) != 1 || status != "active" || userStatus != "active" || !expires.After(time.Now()) {
		return Principal{}, ErrUnauthenticated
	}
	if !contains(item.Scopes, requiredScope) {
		return Principal{}, ErrScope
	}
	ip := remoteIP(remoteAddress)
	if len(allowlist) > 0 && !allowedIP(ip, allowlist) {
		return Principal{}, ErrIP
	}
	ipHash := ""
	if ip.IsValid() {
		h := sha256.Sum256([]byte("hcai-api-network:" + ip.String()))
		ipHash = hex.EncodeToString(h[:])
	}
	_, err = s.pool.Exec(ctx, `UPDATE developer_api_keys SET usage_count=usage_count+1,last_used_at=now(),last_ip_hash=NULLIF($2,'') WHERE id=$1 AND status='active'`, item.APIKeyID, ipHash)
	return item, err
}

func controlTx(ctx context.Context, tx pgx.Tx, requireEnabled bool) (Control, error) {
	var c Control
	err := tx.QueryRow(ctx, `SELECT enabled,max_service_accounts,max_active_keys,default_ttl_days,version,updated_at FROM developer_access_control WHERE singleton=true FOR UPDATE`).Scan(&c.Enabled, &c.MaxServiceAccounts, &c.MaxActiveKeys, &c.DefaultTTLDays, &c.Version, &c.UpdatedAt)
	if err == nil && requireEnabled && !c.Enabled {
		return Control{}, ErrDisabled
	}
	return c, err
}
func validTransition(v Transition) bool {
	return v.Confirmed && v.ExpectedVersion > 0 && len(strings.TrimSpace(v.Reason)) >= 10 && len(strings.TrimSpace(v.Reason)) <= 500
}
func validateKeyInput(v KeyCreate) ([]string, []string, int, error) {
	scopes := append([]string(nil), v.Scopes...)
	if len(scopes) == 0 {
		scopes = []string{identityReadScope}
	}
	sort.Strings(scopes)
	if len(scopes) != 1 || scopes[0] != identityReadScope {
		return nil, nil, 0, ErrInvalid
	}
	allow := []string{}
	seen := map[string]bool{}
	for _, raw := range v.IPAllowlist {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, nil, 0, ErrInvalid
		}
		p = p.Masked()
		value := p.String()
		if seen[value] {
			return nil, nil, 0, ErrInvalid
		}
		seen[value] = true
		allow = append(allow, value)
	}
	sort.Strings(allow)
	if len(allow) > 10 || v.TTLDays < 0 || v.TTLDays > 365 {
		return nil, nil, 0, ErrInvalid
	}
	return scopes, allow, v.TTLDays, nil
}
func newCredentialMaterial() (string, string, string, string, error) {
	prefixBytes := make([]byte, 6)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", "", "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", "", "", err
	}
	prefix := hex.EncodeToString(prefixBytes)
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	sum := sha256.Sum256([]byte(secret))
	return prefix, secret, hex.EncodeToString(sum[:]), secret[len(secret)-4:], nil
}
func parseCredential(raw string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), "_", 4)
	returnValue := len(parts) == 4 && parts[0] == "hcai" && parts[1] == "sk" && regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(parts[2]) && len(parts[3]) >= 40
	if !returnValue {
		return "", "", false
	}
	return parts[2], parts[3], true
}
func remoteIP(address string) netip.Addr {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}
func allowedIP(ip netip.Addr, ranges []string) bool {
	if !ip.IsValid() {
		return false
	}
	for _, raw := range ranges {
		p, err := netip.ParsePrefix(raw)
		if err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}
func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}
func audit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason, requestID string, metadata any) error {
	raw := []byte(`{}`)
	if metadata != nil {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		raw = encoded
	}
	var target any = resourceID
	if resourceID == uuid.Nil {
		target = nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7)`, actorID, action, resourceType, target, strings.TrimSpace(reason), requestID, raw)
	return err
}

func BearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func IdentityReadScope() string { return identityReadScope }

func APIErrorRegistry() []map[string]any {
	return []map[string]any{
		{"code": "AUTHENTICATION_REQUIRED", "retryable": false},
		{"code": "SCOPE_REQUIRED", "retryable": false},
		{"code": "SOURCE_IP_DENIED", "retryable": false},
		{"code": "INTERNAL_ERROR", "retryable": true},
	}
}

func APIContract() map[string]any {
	return map[string]any{"apiVersion": "v1", "authentication": "bearer_api_key", "routes": []string{"GET /api/v1", "GET /api/v1/principal", "GET /api/v1/errors"}, "scopes": []string{identityReadScope}}
}

func (p Principal) String() string { return fmt.Sprintf("%s/%s", p.OwnerHandle, p.ServiceName) }
