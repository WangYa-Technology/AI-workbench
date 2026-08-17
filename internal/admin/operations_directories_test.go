package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestAdminOperationsDirectoriesTraverseBeyondLegacyWindows(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	administratorID, targetOwnerID, targetGenerationID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'Scale Test Administrator','admin','active',now()-interval '5 hours'),
		($4,$5,$6,'Operations Directory Target','creator','active',now()-interval '4 hours')`,
		administratorID, administratorID.String()+"@test.local", "ops_scale_admin_"+administratorID.String()[:8],
		targetOwnerID, targetOwnerID.String()+"@test.local", "ops_directory_target_"+targetOwnerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(email,handle,display_name,role,status,created_at)
		SELECT 'ops-directory-'||value||'@test.local','ops_directory_'||value,'Operations Directory Pressure '||value,'creator','active',now()-interval '2 hours'
		FROM generate_series(1,205) value`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE billing_accounts b SET updated_at=CASE WHEN b.user_id=$1 THEN now()-interval '3 hours' ELSE now()-interval '1 hour' END
		FROM users u WHERE u.id=b.user_id AND (u.id=$1 OR u.handle LIKE 'ops_directory_%')`, targetOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,estimated_cost_cents,created_at,updated_at)
		VALUES($1,$2,'image','local_test','hcai-local-image-v1','Operations directory generation target','queued',5,now()-interval '3 hours',now()-interval '3 hours')`, targetGenerationID, targetOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO generations(owner_id,mode,provider,model_name,prompt,status,estimated_cost_cents,created_at,updated_at)
		SELECT id,'image','local_test','hcai-local-image-v1','Operations directory generation pressure '||handle,'queued',5,now()-interval '1 hour',now()-interval '1 hour'
		FROM users WHERE handle LIKE 'ops_directory_%' AND id<>$1`, targetOwnerID); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	generationInput := admin.GenerationListInput{Query: "operations directory", Mode: "image", Status: "queued", Limit: 50}
	seenGenerations := map[uuid.UUID]bool{}
	foundGeneration := false
	for {
		page, err := service.ListGenerations(ctx, generationInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenGenerations[item.ID] {
				t.Fatalf("generation repeated across cursor pages: %s", item.ID)
			}
			seenGenerations[item.ID] = true
			foundGeneration = foundGeneration || item.ID == targetGenerationID
		}
		if page.NextCursor == nil {
			break
		}
		generationInput.Cursor = *page.NextCursor
	}
	if len(seenGenerations) != 206 || !foundGeneration {
		t.Fatalf("incomplete generation traversal: count=%d target=%t", len(seenGenerations), foundGeneration)
	}
	if _, err := service.ListGenerations(ctx, admin.GenerationListInput{Cursor: "modified"}); !errors.Is(err, admin.ErrInvalidGenerationFilter) {
		t.Fatalf("modified generation cursor was accepted: %v", err)
	}
	cancelled, err := service.CancelGeneration(ctx, administratorID, targetGenerationID, "Verified older generation beyond the legacy operations window.", "operations-directory-generation")
	if err != nil || cancelled.ID != targetGenerationID || cancelled.Status != "cancelled" {
		t.Fatalf("exact older generation cancellation failed: %#v %v", cancelled, err)
	}

	financeInput := admin.FinanceListInput{Query: "operations directory", State: "available", Limit: 50}
	seenAccounts := map[uuid.UUID]bool{}
	foundAccount := false
	for {
		page, err := service.ListFinance(ctx, financeInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenAccounts[item.UserID] {
				t.Fatalf("finance account repeated across cursor pages: %s", item.UserID)
			}
			seenAccounts[item.UserID] = true
			foundAccount = foundAccount || item.UserID == targetOwnerID
		}
		if page.NextCursor == nil {
			break
		}
		financeInput.Cursor = *page.NextCursor
	}
	if len(seenAccounts) != 206 || !foundAccount {
		t.Fatalf("incomplete finance traversal: count=%d target=%t", len(seenAccounts), foundAccount)
	}
	if _, err := service.ListFinance(ctx, admin.FinanceListInput{State: "overdrawn"}); !errors.Is(err, admin.ErrInvalidFinanceFilter) {
		t.Fatalf("unsupported finance state was accepted: %v", err)
	}
	adjusted, err := service.AdjustFinance(ctx, administratorID, targetOwnerID, admin.FinanceAdjustment{
		DeltaCents: 1, Currency: "USD", Reason: "Verified older finance account beyond the legacy operations window.", Confirmed: true,
	}, "operations-directory-finance")
	if err != nil || adjusted.UserID != targetOwnerID || adjusted.BalanceCents != 250001 {
		t.Fatalf("exact older finance adjustment failed: %#v %v", adjusted, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,reason,request_id,metadata,created_at)
		SELECT $1,'admin.operations_directory_probe','operations_probe','Audit directory backlog '||value,'audit-directory-'||value,jsonb_build_object('ordinal',value),now()-interval '2 hours'
		FROM generate_series(1,306) value`, administratorID); err != nil {
		t.Fatal(err)
	}
	auditInput := admin.AuditListInput{Query: "audit directory backlog", Action: "admin.operations_directory_probe", ResourceType: "operations_probe", Limit: 50}
	seenAudit := map[uuid.UUID]bool{}
	for {
		page, err := service.ListAudit(ctx, auditInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenAudit[item.ID] {
				t.Fatalf("audit event repeated across cursor pages: %s", item.ID)
			}
			seenAudit[item.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		auditInput.Cursor = *page.NextCursor
	}
	if len(seenAudit) != 306 {
		t.Fatalf("incomplete audit traversal: count=%d", len(seenAudit))
	}
	if _, err := service.ListAudit(ctx, admin.AuditListInput{Cursor: "modified"}); !errors.Is(err, admin.ErrInvalidAuditFilter) {
		t.Fatalf("modified audit cursor was accepted: %v", err)
	}
}
