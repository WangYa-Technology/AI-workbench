package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestAdminTaskOperationsTraverseBeyondLegacyWindow(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	administratorID, clientID, creatorID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'Task Directory Administrator','admin','active',now() - interval '5 hours'),
		($4,$5,$6,'Task Directory Client','publisher','active',now() - interval '5 hours'),
		($7,$8,$9,'Task Directory Creator','creator','active',now() - interval '5 hours')`,
		administratorID, administratorID.String()+"@test.local", "task_directory_admin_"+administratorID.String()[:8],
		clientID, clientID.String()+"@test.local", "task_directory_client_"+clientID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "task_directory_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}

	targetTaskID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary,created_at,updated_at)
		VALUES($1,$2,'Task directory backlog target','A complete task operations directory backlog brief.','image',18000,'USD',now()+interval '7 days','disputed',$3,'Task directory backlog evidence.',now()-interval '3 hours',now()-interval '3 hours')`, targetTaskID, clientID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary,created_at,updated_at)
		SELECT $1,'Task directory backlog pressure '||value,'A complete task operations directory pressure brief.','image',18000,'USD',now()+interval '7 days','disputed',$2,'Task directory backlog evidence.',now()-interval '1 hour',now()-interval '1 hour'
		FROM generate_series(1,205) value`, clientID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_disputes(demand_id,opened_by,reason,status,idempotency_key,created_at)
		SELECT id,$2,'Task directory backlog dispute evidence.','open','task-directory-'||id::text,created_at
		FROM demands WHERE client_id=$1`, clientID, clientID); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	input := admin.TaskOperationListInput{Query: "task directory backlog", Status: "disputed", DisputeStatus: "open", Limit: 50}
	seen := map[uuid.UUID]bool{}
	foundTarget := false
	for {
		page, err := service.ListTaskOperations(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("task repeated across cursor pages: %s", item.ID)
			}
			seen[item.ID] = true
			foundTarget = foundTarget || item.ID == targetTaskID
		}
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	if len(seen) != 206 || !foundTarget {
		t.Fatalf("incomplete task traversal: count=%d target=%t", len(seen), foundTarget)
	}
	if _, err := service.ListTaskOperations(ctx, admin.TaskOperationListInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidTaskFilter) {
		t.Fatalf("modified task cursor was accepted: %v", err)
	}
	if _, err := service.ListTaskOperations(ctx, admin.TaskOperationListInput{Status: "settling"}); !errors.Is(err, admin.ErrInvalidTaskFilter) {
		t.Fatalf("unsupported task status was accepted: %v", err)
	}

	resolved, err := service.ResolveTaskDispute(ctx, administratorID, targetTaskID, admin.TaskDisputeResolution{Reason: "Reviewed the task evidence and funding before this decision.", Confirm: true,
		Decision: "cancel_without_settlement", ExpectedVersion: 1}, "task-directory-backlog")
	if err != nil || resolved.ID != targetTaskID || resolved.Status != "cancelled" || resolved.DisputeStatus == nil || *resolved.DisputeStatus != "resolved_client" {
		t.Fatalf("exact older task resolution response failed: %#v %v", resolved, err)
	}
}
