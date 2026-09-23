package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sellerExportFixture(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) (*datarights.Service, uuid.UUID, jobs.Job) {
	t.Helper()
	request := uuid.New()
	job := jobs.Job{Kind: datarights.ExportJobKind}
	if _, err := pool.Exec(t.Context(), `INSERT INTO data_rights_requests(id,user_id,request_type,status,subject_ref,execute_after)
 VALUES($1,$2,'data_export','queued',$3,now())`, request, owner, "subject_"+strings.Repeat("a", 24)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO jobs(kind,payload,max_attempts)
 VALUES($1,jsonb_build_object('requestId',$2::text),1) RETURNING id,payload`, job.Kind, request).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	return datarights.NewService(pool, t.TempDir()), request, job
}

func sellerExportData(t *testing.T, service *datarights.Service, owner, request uuid.UUID, job jobs.Job) (map[string][]map[string]any, []byte) {
	t.Helper()
	if err := service.HandleExportJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	body, checksum, err := service.Download(t.Context(), owner, request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("export checksum does not match the downloaded snapshot")
	}
	var pkg struct {
		Data struct {
			Marketplace struct {
				Data map[string][]map[string]any `json:"data"`
			} `json:"marketplace"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg.Data.Marketplace.Data, body
}

func TestSellerPayoutExportHistoryOwnershipAndPrivacy(t *testing.T) {
	pool, service, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	input.Reason = "Private review note: never publish this audit marker."
	input.SellerMessage = "Your request is approved, but no bank payout has been sent."
	if _, err := service.ReviewSellerPayout(ctx, actor, request.ID, input, "private-export-review-key", "private-export-trace"); err != nil {
		t.Fatal(err)
	}
	exporter, exportID, job := sellerExportFixture(t, pool, request.SellerID)
	before, original := sellerExportData(t, exporter, request.SellerID, exportID, job)
	if len(before["sellerPayoutReviews"]) != 1 || before["sellerPayoutReviews"][0]["sellerMessage"] != input.SellerMessage {
		t.Fatal("approved seller explanation missing")
	}
	if rows := before["sellerPayoutBankTargets"]; len(rows) != 1 || rows[0]["bankName"] != "Example Bank" || rows[0]["last4"] != "6789" {
		t.Fatalf("bank snapshot missing in export: %v", rows)
	}
	input.ExpectedRevision = 1
	input.Decision = "rejected"
	input.SellerMessage = "The request was cancelled and its reservation released."
	if _, err := service.ReviewSellerPayout(ctx, actor, request.ID, input, "private-export-reject-key", "private-export-reject-trace"); err != nil {
		t.Fatal(err)
	}
	_, replay := sellerExportData(t, exporter, request.SellerID, exportID, job)
	if string(replay) != string(original) {
		t.Fatal("replaying an existing export replaced its historical snapshot")
	}
	// Retire the fixture's first request before requesting another export;
	// production permits only one active export per account at a time.
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET status='completed',completed_at=now() WHERE id=$1`, exportID); err != nil {
		t.Fatal(err)
	}

	// A new reservation for the same settlement retains both allocation
	// histories. Record a source reservation without calling any provider.
	second, err := service.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, input.SettlementID, input.AmountCents, "private-export-second-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindSellerPayoutBankTarget(ctx, request.SellerID, second.ID, "ba_original"); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = 0
	input.Decision = "approved"
	if _, err := service.ReviewSellerPayout(ctx, actor, second.ID, input, "private-export-second-review", "private-export-second-trace"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, second.ID, input.SettlementID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_transfers SET status='reconciliation_required',evidence='{"private":"raw-provider-export-marker"}' WHERE payout_request_id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
 VALUES($1,$2,17,11,'USD','open')`, request.SellerID, input.SettlementID); err != nil {
		t.Fatal(err)
	}
	var buyer uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT o.buyer_id FROM orders o JOIN product_settlements s ON s.order_id=o.id WHERE s.id=$1`, input.SettlementID).Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	// Exact schemas make a later addition of private operational fields fail
	// loudly. All nine sections contain real records in this fixture.
	fields := map[string]string{
		"sellerSettlements":           "id accountId orderId provider liveMode grossAmountCents feeBps feeCents netAmountCents currency status availableAt recoveryAmountCents transferredAt createdAt updatedAt",
		"sellerLedger":                "id accountId entryType amountCents currency settlementId payoutRequestId availableAt createdAt",
		"sellerRecoveryObligations":   "id accountId settlementId amountCents remainingCents currency status createdAt updatedAt",
		"sellerPayoutRequests":        "id accountId amountCents currency status failureCode createdAt updatedAt",
		"sellerPayoutAllocations":     "payoutRequestId settlementId amountCents createdAt releasedAt",
		"sellerPayoutBankTargets":     "payoutRequestId settlementId destinationId bankDestinationId bankName last4 amountCents currency observedAt createdAt",
		"sellerPayoutReviews":         "payoutRequestId revision decision sellerMessage createdAt",
		"sellerPayoutEvents":          "id payoutRequestId eventType fromStatus toStatus createdAt",
		"sellerPayoutSourceTransfers": "id payoutRequestId settlementId provider liveMode amountCents currency status errorCode createdAt updatedAt",
	}
	for _, owner := range []uuid.UUID{request.SellerID, actor, buyer} {
		exporter, id, job := sellerExportFixture(t, pool, owner)
		data, body := sellerExportData(t, exporter, owner, id, job)
		for section, keys := range fields {
			rows, ok := data[section]
			if !ok || rows == nil {
				t.Fatalf("missing array %s", section)
			}
			if owner != request.SellerID {
				if len(rows) != 0 {
					t.Fatalf("%s exported another seller's %s", owner, section)
				}
				continue
			}
			if len(rows) == 0 {
				t.Fatalf("missing seller evidence %s", section)
			}
			for _, row := range rows {
				if len(row) != len(strings.Fields(keys)) {
					t.Fatalf("unexpected fields in %s: %v", section, row)
				}
				for _, key := range strings.Fields(keys) {
					if _, ok := row[key]; !ok {
						t.Fatalf("missing %s.%s", section, key)
					}
				}
			}
		}
		if owner == request.SellerID {
			if len(data["sellerPayoutRequests"]) != 2 || data["sellerPayoutRequests"][0]["status"] != "cancelled" || len(data["sellerPayoutReviews"]) != 3 {
				t.Fatal("cancelled request or full decision history lost")
			}
			allocations := data["sellerPayoutAllocations"]
			if len(allocations) != 2 || allocations[0]["releasedAt"] == nil || allocations[1]["releasedAt"] != nil {
				t.Fatal("allocation release history is incorrect")
			}
			if data["sellerRecoveryObligations"][0]["remainingCents"] != float64(11) || data["sellerPayoutSourceTransfers"][0]["status"] != "reconciliation_required" {
				t.Fatal("debt or unknown source state lost")
			}
			var reserved float64
			for _, row := range data["sellerLedger"] {
				switch row["entryType"] {
				case "payout_reservation":
					reserved += row["amountCents"].(float64)
				case "payout_release":
					reserved -= row["amountCents"].(float64)
				}
			}
			if reserved != float64(request.AmountCents) {
				t.Fatal("ledger no longer explains the active reservation")
			}
			for _, secret := range []string{input.Reason, actor.String(), buyer.String(), "private-export-", "raw-provider-export-marker", "transfer-evidence-test"} {
				if strings.Contains(string(body), secret) {
					t.Fatalf("seller export exposed private value %q", secret)
				}
			}
			for _, target := range []uuid.UUID{id, uuid.New()} {
				if body, sum, err := exporter.Download(ctx, buyer, target); !errors.Is(err, datarights.ErrNotReady) || len(body) != 0 || sum != "" {
					t.Fatalf("foreign or missing export exposed data: %v", err)
				}
			}
		}
	}
}
