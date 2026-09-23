package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPayoutAccountMissingRequirementsCannotVerify(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	service := NewServiceWithRuntimes(pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion,
		WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	for _, scenario := range []string{"missing", "null", "empty", "missing_currently_due", "missing_past_due", "missing_pending_verification"} {
		t.Run(scenario, func(t *testing.T) {
			user, destination := payoutOrderingOwner(t, pool)
			base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			var body map[string]any
			if err := json.Unmarshal(stripeAccountEvent("evt_"+fmt.Sprintf("%x", uuid.New()), base.Unix(), user, true, true, true, false), &body); err != nil {
				t.Fatal(err)
			}
			object := body["data"].(map[string]any)["object"].(map[string]any)
			object["id"] = destination
			switch scenario {
			case "missing":
				delete(object, "requirements")
			case "null":
				object["requirements"] = nil
			case "empty":
				object["requirements"] = map[string]any{}
			default:
				delete(object["requirements"].(map[string]any), scenario[len("missing_"):])
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			receipt := receivePaymentWorkflowEvent(t, service, encoded, time.Now().UTC())
			processStripeReceipt(t, service, pool, receipt)
			status, err := service.GetPayoutStatus(context.Background(), user)
			if err != nil || status.Status != "restricted" || !status.RequirementsDue || status.VerifiedAt != nil {
				t.Fatalf("missing requirements authorized payout: %+v err=%v", status, err)
			}
			fresh := payoutAccountReceipt(t, service, user, destination, base.Add(time.Second), true, true, true, false)
			processStripeReceipt(t, service, pool, fresh)
			status, err = service.GetPayoutStatus(context.Background(), user)
			if err != nil || status.Status != "verified" || status.RequirementsDue || status.VerifiedAt == nil {
				t.Fatalf("complete evidence did not restore payout: %+v err=%v", status, err)
			}
		})
	}
}
