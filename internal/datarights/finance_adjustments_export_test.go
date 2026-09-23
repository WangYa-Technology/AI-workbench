package datarights_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestFinanceAdjustmentExportOwnership(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	t.Cleanup(cleanup)
	actor := cleanupUser(t, pool, "admin", "active")
	owner := cleanupUser(t, pool, "member", "active")
	other := cleanupUser(t, pool, "member", "active")
	result, err := admin.NewService(pool, true).AdjustFinance(t.Context(), actor, owner, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "private-adjustment-key", "private-adjustment-request")
	if err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, t.TempDir())
	for _, user := range []uuid.UUID{owner, other} {
		request, jobID := exportRecoveryFixture(t, pool, user)
		raw, _ := json.Marshal(map[string]any{"requestId": request})
		if err := service.HandleExportJob(t.Context(), jobs.Job{ID: jobID, Payload: raw}); err != nil {
			t.Fatal(err)
		}
		file, _, err := service.OpenExport(t.Context(), user, request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &pkg); err != nil {
			t.Fatal(err)
		}
		var receipts []map[string]any
		if err := json.Unmarshal(pkg.Data["walletAdjustments"], &receipts); err != nil {
			t.Fatal(err)
		}
		if user == owner {
			if len(receipts) != 1 || receipts[0]["operationId"] != result.OperationID.String() || receipts[0]["deltaCents"] != float64(500) || len(receipts[0]) != 4 {
				t.Fatalf("incorrect owner receipt: %s", pkg.Data["walletAdjustments"])
			}
		} else if len(receipts) != 0 {
			t.Fatal("foreign adjustment leaked")
		}
		if strings.Contains(string(pkg.Data["walletAdjustments"]), actor.String()) || strings.Contains(string(body), "private-adjustment-key") {
			t.Fatal("private command metadata leaked")
		}
	}
}
