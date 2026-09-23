package payments

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Insert original clocks instead of weakening immutable snapshot guards to age fixtures.
func operationalSettlementFixture(t *testing.T, pool *pgxpool.Pool, live bool, status string, availableAt any, net int) (uuid.UUID, uuid.UUID) {
	t.Helper()
	payment := operationalPaymentFixture(t, pool, live, time.Now().Add(-5*time.Hour))
	if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET status='paid',paid_at=created_at WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET status='fulfilled' WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`, payment); err != nil {
		t.Fatal(err)
	}
	var settlement uuid.UUID
	err := pool.QueryRow(t.Context(), `INSERT INTO product_settlements(order_id,payment_id,seller_id,provider,live_mode,
 gross_amount_cents,fee_bps,fee_cents,net_amount_cents,currency,status,available_at,transfer_idempotency_key,created_at)
 SELECT order_id,id,payee_id,provider,live_mode,amount_cents,0,amount_cents-$4,$4,currency,$2,$3,
 'metric-settlement:'||id::text,now()-interval '4 hours' FROM payment_intents WHERE id=$1 RETURNING id`,
		payment, status, availableAt, net).Scan(&settlement)
	if err != nil {
		t.Fatal(err)
	}
	return payment, settlement
}

func reserveOperationalSettlement(t *testing.T, pool *pgxpool.Pool, settlement uuid.UUID, reservedAt any) uuid.UUID {
	t.Helper()
	var job uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO jobs(kind,payload,status)
 VALUES($1,jsonb_build_object('settlementId',$2::text),'queued') RETURNING id`, ProductSettlementJobKind, settlement).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_settlement_dispatches(settlement_id,job_id,reserved_at)
 VALUES($1,$2,$3)`, settlement, job, reservedAt); err != nil {
		t.Fatal(err)
	}
	// Terminal jobs retain the first-send evidence recorded while executable.
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status='succeeded' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProductOperationalMetricsSettlementDueClocksAndModes(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	now := time.Now()
	live, settlement := operationalSettlementFixture(t, pool, true, "pending_hold", now.Add(-2*time.Hour), 1900)
	operationalSettlementFixture(t, pool, false, "available", now.Add(-3*time.Hour), 1900)
	operationalSettlementFixture(t, pool, true, "pending_hold", now.Add(time.Hour), 1900)
	operationalSettlementFixture(t, pool, true, "refund_hold", now.Add(-24*time.Hour), 1900)
	operationalSettlementFixture(t, pool, true, "cancelled", now.Add(-24*time.Hour), 1900)
	operationalSettlementFixture(t, pool, true, "pending_hold", now.Add(-24*time.Hour), 0)
	paused, _ := operationalSettlementFixture(t, pool, true, "available", now.Add(-24*time.Hour), 1900)
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET status='refund_requested' WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`, paused); err != nil {
		t.Fatal(err)
	}
	// Missing dispatches must remain visible; mutable parent mode/update clocks
	// cannot relabel or rejuvenate an original settlement obligation.
	if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET live_mode=false,updated_at=now() WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE product_settlements SET status='provider_unsupported',updated_at=now(),version=version+1 WHERE id=$1`, settlement); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "settlement_due", "live", 1, 7200, 7260)
	assertOperationalMetric(t, pool, "backlog", "settlement_due", "test", 1, 10800, 10860)
	assertOperationalMetric(t, pool, "problem", "settlement_missing", "live", 0, 0, 0)
	reserveOperationalSettlement(t, pool, settlement, now.Add(-time.Hour))
	assertOperationalMetric(t, pool, "backlog", "settlement_due", "live", 0, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "live", 1, 3600, 3660)
}

func TestProductOperationalMetricsSettlementUnresolvedClocks(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	now := time.Now()
	payment, settlement := operationalSettlementFixture(t, pool, true, "transfer_pending", now.Add(-3*time.Hour), 1900)
	job := reserveOperationalSettlement(t, pool, settlement, now.Add(-2*time.Hour))
	operationalSettlementFixture(t, pool, false, "recovery_required", now.Add(-3*time.Hour), 1900)
	assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "live", 1, 7200, 7260)
	// If a historical dispatch is absent, retain the original creation clock.
	assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "test", 1, 14400, 14460)
	if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET status='refunded',updated_at=now() WHERE id=$1`, payment); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET status='refunded' WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`, payment); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "cancelled", "queued", "succeeded"} {
		if _, err := pool.Exec(t.Context(), `UPDATE jobs SET status=$2,updated_at=now() WHERE id=$1`, job, status); err != nil {
			t.Fatal(err)
		}
		assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "live", 1, 7200, 7260)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE product_settlements SET status='recovery_required',provider_transfer_id='tr_metrics_debt',
 transferred_at=now(),recovery_amount_cents=net_amount_cents,updated_at=now() WHERE id=$1`, settlement); err != nil {
		t.Fatal(err)
	}
	assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "live", 1, 7200, 7260)
}

func TestProductOperationalMetricsSettlementMissingAndCompensation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, mode := range []string{"live", "test"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name, status, paymentStatus, compensation, evidence string
				want                                                int64
			}{
				{"pending", "payment_pending", "checkout_pending", "", "", 0},
				{"fulfilled", "fulfilled", "paid", "", "", 1},
				{"ordinary_refund", "refund_requested", "refund_pending", "", "", 1},
				{"compensation", "refund_requested", "refund_pending", "buyer_unavailable", "", 0},
				{"compensated", "refunded", "refunded", "buyer_unavailable", "", 0},
				{"historical_fulfillment", "refunded", "refunded", "", "event", 1},
				{"conflicting_compensation", "refund_requested", "refund_pending", "buyer_unavailable", "event", 1},
				{"retained_entitlement", "refunded", "refunded", "", "entitlement", 1},
				{"conflicting_entitlement", "refunded", "refunded", "buyer_unavailable", "entitlement", 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					payment := operationalPaymentFixture(t, pool, mode == "live", time.Now())
					if _, err := pool.Exec(t.Context(), `UPDATE orders SET status=$2 WHERE id=(SELECT order_id FROM payment_intents WHERE id=$1)`, payment, tc.status); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(t.Context(), `UPDATE payment_intents SET compensation_reason=NULLIF($2,''),status=$3 WHERE id=$1`, payment, tc.compensation, tc.paymentStatus); err != nil {
						t.Fatal(err)
					}
					if tc.evidence == "event" {
						if _, err := pool.Exec(t.Context(), `INSERT INTO order_events(order_id,to_status,reason,sequence)
 SELECT order_id,'fulfilled','Preserved fulfillment history',1 FROM payment_intents WHERE id=$1`, payment); err != nil {
							t.Fatal(err)
						}
					}
					if tc.evidence == "entitlement" {
						if _, err := pool.Exec(t.Context(), `WITH delivery AS (
 INSERT INTO assets(owner_id,kind,title,media_url,mime_type,scan_status,source_type,source_id,license_code)
 SELECT pi.payer_id,'image','Retained delivery','/api/v1/assets/retained/content','image/jpeg','clean','purchase',pi.order_id,
 p.license_code FROM payment_intents pi JOIN products p ON p.id=pi.resource_id WHERE pi.id=$1
 RETURNING id,owner_id,source_id,license_code
 ) INSERT INTO entitlements(user_id,product_id,order_id,asset_id,license_code,status,revoked_at)
 SELECT d.owner_id,pi.resource_id,d.source_id,d.id,d.license_code,'refunded',now()
 FROM delivery d JOIN payment_intents pi ON pi.order_id=d.source_id`, payment); err != nil {
							t.Fatal(err)
						}
					}
					assertOperationalMetric(t, pool, "problem", "settlement_missing", mode, tc.want, 0, 0)
					otherMode := "test"
					if mode == "test" {
						otherMode = "live"
					}
					assertOperationalMetric(t, pool, "problem", "settlement_missing", otherMode, 0, 0, 0)
					// A retained matching snapshot resolves the gap without removing history.
					if _, err := pool.Exec(t.Context(), `INSERT INTO product_settlements(order_id,payment_id,seller_id,provider,live_mode,
 gross_amount_cents,fee_bps,fee_cents,net_amount_cents,currency,status,available_at,transfer_idempotency_key)
 SELECT order_id,id,payee_id,provider,live_mode,1900,0,0,1900,currency,'refund_hold',now(),'metric-gap:'||id::text
 FROM payment_intents WHERE id=$1`, payment); err != nil {
						t.Fatal(err)
					}
					assertOperationalMetric(t, pool, "problem", "settlement_missing", mode, 0, 0, 0)
				})
			}
		})
	}
}

func TestProductOperationalMetricsSettlementRecoveryLifecycle(t *testing.T) {
	for _, debt := range []bool{false, true} {
		name := "transfer_recovered"
		if debt {
			name = "refund_debt_retained"
		}
		t.Run(name, func(t *testing.T) {
			pool, service, runtime, checkout, _, settlement := settlementCheckFixture(t)
			assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "test", 1, 0, 60)
			job := scheduleSettlementCheck(t, service, settlement)
			finishSettlementCheckJob(t, pool, job, "failed")
			assertOperationalMetric(t, pool, "problem", "settlement_check_stopped", "test", 1, 0, 0)
			assertOperationalMetric(t, pool, "problem", "settlement_check_stopped", "live", 0, 0, 0)
			recovery := scheduleSettlementCheck(t, service, settlement)
			assertOperationalMetric(t, pool, "problem", "settlement_check_stopped", "test", 0, 0, 0)
			assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "test", 1, 0, 60)
			if debt {
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				if _, err = tx.Exec(t.Context(), `UPDATE payment_intents SET status='refunded' WHERE id=$1`, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), `UPDATE orders SET status='refunded' WHERE id=$1`, checkout.OrderID); err != nil {
					t.Fatal(err)
				}
				if err = markProductSettlementRefundTx(t.Context(), tx, checkout.PaymentID); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.HandleProductSettlementCheckJob(t.Context(), recovery); err != nil {
				t.Fatal(err)
			}
			finishSettlementCheckJob(t, pool, recovery, "succeeded")
			want := int64(0)
			if debt {
				want = 1
			}
			assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "test", want, 0, 60)
			assertOperationalMetric(t, pool, "problem", "settlement_check_stopped", "test", 0, 0, 0)
			assertOperationalMetric(t, pool, "problem", "settlement_missing", "test", 0, 0, 0)
			if runtime.transfers.Load() != 1 || runtime.reads.Load() != 1 {
				t.Fatalf("monitoring caused external writes: transfers=%d reads=%d", runtime.transfers.Load(), runtime.reads.Load())
			}
		})
	}
}

func TestProductOperationalMetricsSettlementInvalidClocks(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	for _, timestamp := range []any{"infinity", "-infinity"} {
		operationalSettlementFixture(t, pool, true, "pending_hold", timestamp, 1900)
	}
	for _, timestamp := range []any{"infinity", "-infinity", time.Now().Add(time.Hour)} {
		_, settlement := operationalSettlementFixture(t, pool, true, "transfer_pending", time.Now().Add(-time.Hour), 1900)
		reserveOperationalSettlement(t, pool, settlement, timestamp)
	}
	assertOperationalMetric(t, pool, "problem", "invalid_timestamp", "live", 5, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "settlement_due", "live", 2, 0, 0)
	assertOperationalMetric(t, pool, "backlog", "settlement_unresolved", "live", 3, 0, 0)
}
