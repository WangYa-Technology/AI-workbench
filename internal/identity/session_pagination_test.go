package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/jackc/pgx/v5"
)

func TestSessionHistoryStablePaginationIsolationAndExactRevocation(t *testing.T) {
	pool, cleanup := identityTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Session Owner','member','active'),
		($4,$5,$6,'Session Outsider','member','active')`,
		ownerID, ownerID.String()+"@test.local", "session_owner_"+ownerID.String()[:8],
		outsiderID, outsiderID.String()+"@test.local", "session_outsider_"+outsiderID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	currentToken := "current-session-history-token"
	base := time.Now().UTC()
	ids := make([]uuid.UUID, 106)
	batch := &pgx.Batch{}
	for index := range ids {
		ids[index] = uuid.New()
		token := "historical-session-" + ids[index].String()
		if index == 0 {
			token = currentToken
		}
		lastSeen := base.Add(-time.Duration(index) * time.Second)
		batch.Queue(`INSERT INTO sessions(id,user_id,token_hash,expires_at,client_label,last_seen_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, ids[index], ownerID, identity.HashToken(token), base.Add(24*time.Hour), "Pagination device", lastSeen)
	}
	batch.Queue(`INSERT INTO sessions(user_id,token_hash,expires_at,client_label,last_seen_at) VALUES($1,$2,$3,'Outsider device',$4)`, outsiderID, identity.HashToken("outsider-session-token"), base.Add(24*time.Hour), base.Add(time.Hour))
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	repository := identity.NewRepository(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := repository.ListSessions(ctx, ownerID, currentToken, identity.SessionListInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate session %s", item.ID)
			}
			if item.Current != (item.ID == ids[0]) {
				t.Fatalf("current-session evidence changed: %#v", item)
			}
			seen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 owned sessions, got %d", len(seen))
	}
	if _, err := repository.ListSessions(ctx, ownerID, currentToken, identity.SessionListInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, identity.ErrInvalidSessionFilter) {
		t.Fatalf("modified session cursor was accepted: %v", err)
	}
	if _, err := repository.ListSessions(ctx, ownerID, currentToken, identity.SessionListInput{Limit: 51}); !errors.Is(err, identity.ErrInvalidSessionFilter) {
		t.Fatalf("oversized session page was accepted: %v", err)
	}
	current, err := repository.RevokeSession(ctx, ownerID, ids[100], currentToken, "revoke-deep-session")
	if err != nil || current {
		t.Fatalf("deep historical session was mistaken for current: current=%v err=%v", current, err)
	}
	current, err = repository.RevokeSession(ctx, ownerID, ids[0], currentToken, "revoke-current-session")
	if err != nil || !current {
		t.Fatalf("current session was not recognized exactly: current=%v err=%v", current, err)
	}
}
