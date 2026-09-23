package tasks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
)

func TestInactivePublisherTasksAreHiddenFromPublicButKeepParticipantHistory(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, uuid.New())
	service := tasks.NewService(pool)
	task, err := service.Create(ctx, clientID, validTaskInput(), "retired-publisher-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, clientID); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []uuid.UUID{uuid.Nil, outsiderID} {
		items, err := service.List(ctx, viewer, tasks.ListFilter{})
		if err != nil || len(items) != 0 {
			t.Fatalf("retired task publicly visible: %d %v", len(items), err)
		}
		if _, err := service.Get(ctx, viewer, task.ID); !errors.Is(err, tasks.ErrNotFound) {
			t.Fatalf("retired detail publicly visible: %v", err)
		}
	}
	if _, err := service.Get(ctx, clientID, task.ID); err != nil {
		t.Fatalf("participant history lost: %v", err)
	}
}
