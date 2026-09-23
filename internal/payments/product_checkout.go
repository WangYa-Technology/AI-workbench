package payments

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrOfferChanged = errors.New("product offer changed; review the current offer")

func productCheckoutReturnURL(raw string, orderID, paymentID uuid.UUID) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	} // BeginProductCheckout validates both URLs first.
	query := parsed.Query()
	query.Set("orderId", orderID.String())
	query.Set("paymentId", paymentID.String())
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func validOfferVersion(version string) bool {
	if len(version) != 64 {
		return false
	}
	for _, c := range version {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateProductContractVersion(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, version string) error {
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_order_contracts WHERE order_id=$1 AND offer_version=$2)`, orderID, version).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return ErrOfferChanged
	}
	return nil
}

func bindProductCheckoutCommand(ctx context.Context, tx pgx.Tx, buyerID uuid.UUID, key string, paymentID uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO product_checkout_commands(buyer_id,idempotency_key,payment_id) VALUES($1,$2,$3)`, buyerID, key, paymentID)
	return err
}

func validProductCheckoutReplay(checkout Checkout) error {
	if checkout.Purpose != "product" || !oneOf(checkout.Status, "checkout_pending", "checkout_open") {
		return ErrCheckoutConflict
	}
	if checkout.Status == "checkout_open" && (checkout.CheckoutURL == "" || !checkout.ExpiresAt.After(time.Now())) {
		return ErrCheckoutConflict
	}
	return nil
}

// All replay paths, including returning an existing URL, must preserve a
// recorded ambiguity until its funds have been reconciled.
func validateProductCheckoutReconciliation(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	if err := validateProductCheckoutEvidence(ctx, tx, paymentID); err != nil {
		return err
	}
	var review bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_waffo_checkout_review WHERE payment_id=$1)`, paymentID).Scan(&review); err != nil {
		return err
	}
	if review {
		return ErrCheckoutReconciliation
	}
	return nil
}

func validateProductCheckoutEvidence(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	var review bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=$1) OR EXISTS(SELECT 1 FROM product_webhook_quarantine_review WHERE payment_id=$1)`, paymentID).Scan(&review); err != nil {
		return err
	}
	if review {
		return ErrCheckoutReconciliation
	}
	return nil
}

func validateProductCheckoutEligibility(ctx context.Context, tx pgx.Tx, buyerID, productID uuid.UUID) error {
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM public_products p
		JOIN users buyer ON buyer.id=$1 AND buyer.status='active'
		WHERE p.id=$2 AND p.seller_id<>$1)`, buyerID, productID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrInvalidCheckout
	}
	return nil
}

// New contracts lock accounts before products, then all source/preview assets
// in UUID order and finally the license. This matches publication and account
// deletion. Existing payments must be resolved before entering this function:
// acquiring their locks after these rows would invert cleanup's lock order.
// Public eligibility and the offer must be read in a NEW statement afterwards;
// a joined SELECT FOR UPDATE can retain a view's pre-wait MVCC snapshot.
func lockProductCheckoutSources(ctx context.Context, tx pgx.Tx, buyerID, productID uuid.UUID) error {
	var sellerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT seller_id FROM products WHERE id=$1`, productID).Scan(&sellerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidCheckout
	} else if err != nil {
		return err
	}
	if sellerID == buyerID {
		return ErrInvalidCheckout
	}
	rows, err := tx.Query(ctx, `SELECT id FROM users WHERE id=ANY($1::uuid[]) AND status='active' ORDER BY id FOR SHARE`, []uuid.UUID{buyerID, sellerID})
	if err != nil {
		return err
	}
	accounts, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(accounts) != 2 {
		return ErrInvalidCheckout
	}
	var currentSeller uuid.UUID
	var license string
	if err = tx.QueryRow(ctx, `SELECT seller_id,license_code FROM products WHERE id=$1 FOR UPDATE`, productID).Scan(&currentSeller, &license); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidCheckout
	} else if err != nil {
		return err
	}
	if currentSeller != sellerID {
		return ErrOfferChanged
	}
	// The product lock serializes replacement of the complete file list. Include
	// legacy roots and the preview in the same ordering as publication writers.
	const sources = `WITH originals AS (
 SELECT asset_id AS id FROM products WHERE id=$1
 UNION SELECT preview_asset_id FROM products WHERE id=$1 AND preview_asset_id IS NOT NULL
 UNION SELECT asset_id FROM product_listing_files WHERE product_id=$1
), sources AS (
 SELECT id FROM originals
 UNION SELECT a.origin_asset_id FROM assets a JOIN originals o ON o.id=a.id WHERE a.origin_asset_id IS NOT NULL
) `
	rows, err = tx.Query(ctx, sources+`SELECT a.id FROM assets a JOIN sources s ON s.id=a.id ORDER BY a.id FOR UPDATE OF a`, productID)
	if err != nil {
		return err
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	// A legacy origin can move while waiting for an asset lock. Do not acquire
	// additional rows out of order or freeze an unlocked new root.
	var complete bool
	if err = tx.QueryRow(ctx, sources+`SELECT NOT EXISTS(SELECT 1 FROM sources WHERE NOT(id=ANY($2::uuid[])))`, productID, locked).Scan(&complete); err != nil {
		return err
	}
	if !complete || len(locked) == 0 {
		return ErrOfferChanged
	}
	if err = tx.QueryRow(ctx, `SELECT code FROM licenses WHERE code=$1 AND status='active' FOR SHARE`, license).Scan(&license); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidCheckout
	} else if err != nil {
		return err
	}
	// Every real member must still satisfy the same source admission rule used
	// by seller publication. In particular a clean first file cannot stand in
	// for a quarantined, foreign, referenced or publicly exposed later member.
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(
 SELECT 1 FROM product_listing_files f
 LEFT JOIN product_source_candidates c ON c.asset_id=f.asset_id AND c.owner_id=$2
 WHERE f.product_id=$1 AND c.asset_id IS NULL)`, productID, sellerID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrInvalidCheckout
	}
	return nil
}
