package payments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrQuarantineForbidden   = errors.New("payment evidence permission required")
	ErrQuarantineInvalid     = errors.New("invalid payment evidence request")
	ErrQuarantineConflict    = errors.New("payment evidence changed")
	ErrQuarantineNotFound    = errors.New("payment evidence not found")
	ErrWebhookAdmissionBusy  = errors.New("payment event admission is in progress")
	errProductPaymentUnknown = fmt.Errorf("original product payment is unknown: %w", ErrInvalidEvent)
)

func quarantineCode(err error) string {
	switch {
	case errors.Is(err, errProductPaymentUnknown):
		return "payment_unknown"
	case errors.Is(err, ErrEventConflict):
		return "event_identity_conflict"
	case errors.Is(err, ErrCheckoutReconciliation):
		return "original_identity_missing"
	case errors.Is(err, ErrInvalidEvent):
		return "payment_binding_conflict"
	case errors.Is(err, ErrDisabled):
		return "provider_routing_unavailable"
	default:
		return ""
	}
}

// Called only after signature verification AND complete normalization. Invalid
// signatures, malformed envelopes and unknown schema versions are never replayable
// evidence. Never persist the raw body, signature, email or arbitrary metadata.
func (s *Service) receiveSignedProductEvent(ctx context.Context, provider string, event minimizedProviderEvent) (Receipt, error) {
	item, err := s.receiveProviderEvent(ctx, provider, event)
	if err == nil {
		return item, nil
	}
	if code := quarantineCode(err); code != "" {
		if recordErr := s.quarantineProductEvent(ctx, provider, event, code); recordErr != nil {
			// Preserve retryability: a failure to record evidence must not be
			// returned as a definitive 4xx rejection with nothing left to review.
			return Receipt{}, fmt.Errorf("persist rejected signed payment evidence: %w", recordErr)
		}
	}
	return item, err
}

func (s *Service) quarantineProductEvent(ctx context.Context, provider string, event minimizedProviderEvent, code string) error {
	if !event.Supported || event.PaymentID == nil || !oneOf(event.EventType, "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "payment_intent.succeeded", "payment_intent.payment_failed", "refund.updated", "order.completed", "refund.succeeded", "refund.failed") {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize the appearance of a new hold with the buyer's next checkout.
	var buyer, product uuid.UUID
	lookupErr := tx.QueryRow(ctx, `SELECT payer_id,resource_id FROM payment_intents WHERE id=$1 AND purpose='product'`, event.PaymentID).Scan(&buyer, &product)
	if lookupErr == nil {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+buyer.String()+":"+product.String()); err != nil {
			return err
		}
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return lookupErr
	}
	var purpose string
	var candidate *uuid.UUID
	// A metadata claim locates possible evidence; only a matching original
	// remote identifier, provider and environment can put funds on hold.
	err = tx.QueryRow(ctx, `SELECT purpose,CASE WHEN provider=$2 AND live_mode=$3 AND purpose='product' AND (
 (provider_checkout_id IS NOT NULL AND provider_checkout_id=$4) OR
 (provider_payment_id IS NOT NULL AND provider_payment_id=$5) OR
 (provider_charge_id IS NOT NULL AND provider_charge_id=$6)) THEN id END
 FROM payment_intents WHERE id=$1 FOR UPDATE`, event.PaymentID, provider, event.LiveMode, event.ObjectID, event.ProviderPaymentID, event.ProviderChargeID).Scan(&purpose, &candidate)
	if errors.Is(err, pgx.ErrNoRows) {
		code = "payment_unknown"
	} else if err != nil {
		return err
	}
	if purpose != "product" && (event.Purpose == nil || *event.Purpose != "product") {
		return nil
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO product_webhook_quarantines(provider,provider_event_id,payload_sha256,live_mode,claimed_payment_id,candidate_payment_id,event,rejection_code,last_error_code)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) ON CONFLICT(provider,provider_event_id,payload_sha256) DO NOTHING RETURNING id`, provider, event.ProviderEventID, event.PayloadSHA256, event.LiveMode, event.PaymentID, candidate, raw, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func admitMatchingQuarantinesTx(ctx context.Context, tx pgx.Tx, provider string, event minimizedProviderEvent) error {
	_, err := tx.Exec(ctx, `UPDATE product_webhook_quarantines q SET state='admitted',admitted_event_id=e.id,checked_at=now(),last_error_code=NULL,version=q.version+1
 FROM payment_provider_events e WHERE q.state='pending' AND q.provider=$1 AND q.provider_event_id=$2 AND q.payload_sha256=$3
 AND e.provider=q.provider AND e.provider_event_id=q.provider_event_id AND e.payload_sha256=q.payload_sha256 AND e.live_mode=q.live_mode AND e.evidence_source='webhook'`, provider, event.ProviderEventID, event.PayloadSHA256)
	return err
}

// Ingress inserts an event before updating quarantines; operator review locks
// the quarantine before inserting that event. Serialize both by event identity
// before acquiring either resource. Never wait here: a provider may be calling
// back synchronously while another transaction holds the payment lock.
func lockWebhookAdmission(ctx context.Context, tx pgx.Tx, provider, eventID string) error {
	var locked bool
	err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "webhook-admission:"+provider+":"+eventID).Scan(&locked)
	if err != nil {
		return err
	}
	if !locked {
		return ErrWebhookAdmissionBusy
	}
	return nil
}

type WebhookQuarantine struct {
	ID                 uuid.UUID  `json:"id"`
	Provider           string     `json:"provider"`
	ProviderEventID    string     `json:"providerEventId"`
	EventType          string     `json:"eventType"`
	LiveMode           bool       `json:"liveMode"`
	ClaimedPaymentID   *uuid.UUID `json:"claimedPaymentId,omitempty"`
	CandidatePaymentID *uuid.UUID `json:"candidatePaymentId,omitempty"`
	HasReviewHold      bool       `json:"hasReviewHold"`
	AmountCents        *int64     `json:"amountCents,omitempty"`
	Currency           *string    `json:"currency,omitempty"`
	State              string     `json:"state"`
	RejectionCode      string     `json:"rejectionCode"`
	LastErrorCode      *string    `json:"lastErrorCode,omitempty"`
	AdmittedEventID    *uuid.UUID `json:"admittedEventId,omitempty"`
	ReceivedAt         time.Time  `json:"receivedAt"`
	CheckedAt          *time.Time `json:"checkedAt,omitempty"`
	Version            int        `json:"version"`
}
type WebhookQuarantineFilter struct {
	State, Provider, Mode, Cursor string
	Limit                         int
}
type WebhookQuarantinePage struct {
	Items      []WebhookQuarantine `json:"items"`
	NextCursor *string             `json:"nextCursor,omitempty"`
}
type quarantineCursor struct {
	Version               int
	Actor                 uuid.UUID
	State, Provider, Mode string
	Time                  time.Time
	ID                    uuid.UUID
}

const quarantineSelect = `SELECT id,provider,provider_event_id,event->>'EventType',live_mode,claimed_payment_id,candidate_payment_id,
 (event->>'AmountCents')::bigint,event->>'Currency',state,rejection_code,last_error_code,admitted_event_id,received_at,checked_at,version,
 EXISTS(SELECT 1 FROM product_webhook_quarantine_matches m WHERE m.quarantine_id=product_webhook_quarantines.id) FROM product_webhook_quarantines`

func scanQuarantine(row pgx.Row) (WebhookQuarantine, error) {
	var q WebhookQuarantine
	err := row.Scan(&q.ID, &q.Provider, &q.ProviderEventID, &q.EventType, &q.LiveMode, &q.ClaimedPaymentID, &q.CandidatePaymentID, &q.AmountCents, &q.Currency, &q.State, &q.RejectionCode, &q.LastErrorCode, &q.AdmittedEventID, &q.ReceivedAt, &q.CheckedAt, &q.Version, &q.HasReviewHold)
	return q, err
}
func quarantinePermission(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	err := paymentFinanceAuthority(ctx, tx, actor, false)
	if errors.Is(err, ErrFinanceForbidden) {
		return ErrQuarantineForbidden
	}
	return err
}

// Rechecks retain Serializable isolation for payment evidence consistency.
// A second snapshot-only permission read would still see revoked authority.
// Lock the matching account and permission after resource locks: a committed
// change since the snapshot aborts this transaction, and later revocations
// wait until the already-authorized admission has committed or rolled back.
func lockQuarantinePermission(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	err := paymentFinanceAuthority(ctx, tx, actor, true)
	if errors.Is(err, ErrFinanceForbidden) {
		return ErrQuarantineForbidden
	}
	return err
}

func (s *Service) ListWebhookQuarantines(ctx context.Context, actor uuid.UUID, f WebhookQuarantineFilter) (WebhookQuarantinePage, error) {
	page := WebhookQuarantinePage{Items: []WebhookQuarantine{}}
	if f.State == "" {
		f.State = "pending"
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if actor == uuid.Nil || f.Limit < 1 || f.Limit > 50 || len(f.Cursor) > 1024 || !oneOf(f.State, "pending", "admitted", "all") || !oneOf(f.Provider, "", "stripe", "waffo_pancake") || !oneOf(f.Mode, "", "live", "test") {
		return page, ErrQuarantineInvalid
	}
	c := quarantineCursor{Version: 1, Actor: actor, State: f.State, Provider: f.Provider, Mode: f.Mode}
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		var got quarantineCursor
		if err != nil || json.Unmarshal(raw, &got) != nil || got.Version != c.Version || got.Actor != actor || got.State != c.State || got.Provider != c.Provider || got.Mode != c.Mode || got.ID == uuid.Nil || got.Time.IsZero() || got.Time.Year() < 1 || got.Time.Year() > 9999 {
			return page, ErrQuarantineInvalid
		}
		c = got
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)
	if err = quarantinePermission(ctx, tx, actor); err != nil {
		return page, err
	}
	cutoff := ""
	args := []any{f.State, f.Provider, f.Mode, f.Limit + 1}
	if f.Cursor != "" {
		cutoff = " AND (received_at,id)<($5,$6)"
		args = append(args, c.Time, c.ID)
	}
	rows, err := tx.Query(ctx, quarantineSelect+` WHERE ($1='all' OR state=$1) AND ($2='' OR provider=$2) AND ($3='' OR live_mode=($3='live'))`+cutoff+` ORDER BY received_at DESC,id DESC LIMIT $4`, args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		q, err := scanQuarantine(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		last := page.Items[len(page.Items)-1]
		c.Time, c.ID = last.ReceivedAt, last.ID
		raw, _ := json.Marshal(c)
		value := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &value
	}
	return page, tx.Commit(ctx)
}

type RecheckWebhookInput struct {
	ExpectedVersion int    `json:"expectedVersion"`
	Reason          string `json:"reason"`
}

func (s *Service) RecheckWebhookQuarantine(ctx context.Context, actor, id uuid.UUID, in RecheckWebhookInput) (WebhookQuarantine, error) {
	item, err := s.recheckWebhookQuarantine(ctx, actor, id, in)
	if errors.Is(err, ErrWebhookAdmissionBusy) {
		return WebhookQuarantine{}, ErrQuarantineConflict
	}
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && (conflict.Code == "40001" || conflict.Code == "40P01") {
		return WebhookQuarantine{}, ErrQuarantineConflict
	}
	return item, err
}

func (s *Service) recheckWebhookQuarantine(ctx context.Context, actor, id uuid.UUID, in RecheckWebhookInput) (WebhookQuarantine, error) {
	var empty WebhookQuarantine
	in.Reason = strings.TrimSpace(in.Reason)
	if actor == uuid.Nil || id == uuid.Nil || in.ExpectedVersion < 1 || in.ExpectedVersion >= 2147483647 || !utf8.ValidString(in.Reason) || strings.ContainsRune(in.Reason, 0) || utf8.RuneCountInString(in.Reason) < 10 || utf8.RuneCountInString(in.Reason) > 1000 {
		return empty, ErrQuarantineInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	if err = quarantinePermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	q, err := scanQuarantine(tx.QueryRow(ctx, quarantineSelect+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrQuarantineNotFound
	}
	if err != nil {
		return empty, err
	}
	if err = lockWebhookAdmission(ctx, tx, q.Provider, q.ProviderEventID); err != nil {
		return empty, err
	}
	// Rejected receipt persistence also locks the payment before quarantine.
	if q.ClaimedPaymentID != nil {
		if _, err = tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE`, q.ClaimedPaymentID); err != nil {
			return empty, err
		}
	}
	q, err = scanQuarantine(tx.QueryRow(ctx, quarantineSelect+` WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return empty, err
	}
	if q.Version != in.ExpectedVersion || q.State != "pending" {
		return empty, ErrQuarantineConflict
	}
	if !s.config.Enabled {
		return empty, ErrDisabled
	}
	var raw []byte
	var event minimizedProviderEvent
	if err = tx.QueryRow(ctx, `SELECT event FROM product_webhook_quarantines WHERE id=$1`, id).Scan(&raw); err != nil {
		return empty, err
	}
	if json.Unmarshal(raw, &event) != nil {
		return empty, ErrQuarantineInvalid
	}
	if !event.Supported || event.ProviderEventID != q.ProviderEventID || event.LiveMode != q.LiveMode || event.PaymentID == nil || q.ClaimedPaymentID == nil || *event.PaymentID != *q.ClaimedPaymentID {
		return empty, ErrQuarantineInvalid
	}
	if err = lockQuarantinePermission(ctx, tx, actor); err != nil {
		return empty, err
	}
	_, receiveErr := s.receiveProviderEventTx(ctx, tx, q.Provider, event)
	code := quarantineCode(receiveErr)
	if receiveErr != nil && code == "" {
		return empty, receiveErr
	}
	if receiveErr != nil {
		_, err = tx.Exec(ctx, `UPDATE product_webhook_quarantines SET checked_at=now(),last_error_code=$2,version=version+1 WHERE id=$1`, id, code)
	} else {
		err = admitMatchingQuarantinesTx(ctx, tx, q.Provider, event)
	}
	if err != nil {
		return empty, err
	}
	q, err = scanQuarantine(tx.QueryRow(ctx, quarantineSelect+` WHERE id=$1`, id))
	if err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_webhook_quarantine_checks(quarantine_id,actor_id,expected_version,outcome,reason) VALUES($1,$2,$3,$4,$5)`, id, actor, in.ExpectedVersion, q.State, in.Reason); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata) VALUES($1,'payment.webhook_rechecked','payment_webhook_quarantine',$2,$3,jsonb_build_object('outcome',$4::text,'expectedVersion',$5::integer))`, actor, id, "webhook-check:"+uuid.NewString(), q.State, in.ExpectedVersion); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return q, nil
}
