package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestAdminUserDirectoryFiltersAndTraversesBeyondFirstPage(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	administratorID, targetID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'Directory Administrator','admin','active',$5),
		($4,$6,$7,'Directory Target','member','active',$5 - interval '2 hours')`,
		administratorID, administratorID.String()+"@test.local", "directory_admin_"+administratorID.String()[:8], targetID,
		time.Now().UTC(), targetID.String()+"@test.local", "directory_target_"+targetID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at)
		SELECT gen_random_uuid(), 'directory_pressure_'||value||'@test.local', 'directory_pressure_'||value,
		       'Directory Pressure '||value, 'member', 'active', now() - interval '1 hour'
		FROM generate_series(1,22) value`); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	input := admin.UserListInput{Query: "directory_", Role: "member", Status: "active", Limit: 10}
	seen := map[uuid.UUID]bool{}
	foundTarget := false
	for {
		page, err := service.ListUsers(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("user repeated across cursor pages: %s", item.ID)
			}
			seen[item.ID] = true
			foundTarget = foundTarget || item.ID == targetID
		}
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	if len(seen) != 23 || !foundTarget {
		t.Fatalf("incomplete directory traversal: count=%d target=%t", len(seen), foundTarget)
	}
	if _, err := service.ListUsers(ctx, admin.UserListInput{Cursor: "modified", Limit: 10}); !errors.Is(err, admin.ErrInvalidUserFilter) {
		t.Fatalf("modified user cursor was accepted: %v", err)
	}

	updated, err := service.UpdateUser(ctx, administratorID, targetID, admin.UserUpdate{
		Role: "creator", Status: "suspended"}, "directory-update")
	if err != nil || updated.ID != targetID || updated.Role != "creator" || updated.Status != "suspended" {
		t.Fatalf("exact user update retrieval failed: %#v %v", updated, err)
	}
}
