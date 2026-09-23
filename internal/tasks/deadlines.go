package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const ExpiryJobKind = "task.expire_open"

// Only unassigned tasks expire automatically. Assigned work requires participant
// review or arbitration; a clock alone cannot authorize a creator payout.
func (s *Service) HandleExpiryJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		TaskID uuid.UUID `json:"taskId"`
	}
	if json.Unmarshal(job.Payload, &payload) != nil || payload.TaskID == uuid.Nil {
		return ErrInvalid
	}
	var clientID uuid.UUID
	var status string
	var deadline time.Time
	err := s.pool.QueryRow(ctx, `SELECT client_id,status,deadline FROM demands WHERE id=$1`, payload.TaskID).Scan(&clientID, &status, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "open" || deadline.After(time.Now()) {
		return nil
	}
	_, err = s.Cancel(ctx, clientID, payload.TaskID, "The published deadline passed without assignment; unused funding will be refunded.", "expire:"+payload.TaskID.String())
	if errors.Is(err, ErrConflict) {
		var current string
		if e := s.pool.QueryRow(ctx, `SELECT status FROM demands WHERE id=$1`, payload.TaskID).Scan(&current); e != nil {
			return e
		}
		if current != "open" {
			return nil
		}
	} // Assignment/cancellation won the locked-row race.
	return err
}

type DeadlineChange struct {
	ID         uuid.UUID `json:"id"`
	ProposedBy uuid.UUID `json:"proposedBy"`
	Deadline   time.Time `json:"deadline"`
	Reason     string    `json:"reason"`
}

type DeadlineInput struct {
	Decision string    `json:"decision"`
	ChangeID uuid.UUID `json:"changeId"`
	Deadline time.Time `json:"deadline"`
	Reason   string    `json:"reason"`
}

// Neither party can unilaterally move a production deadline.
func (s *Service) ChangeDeadline(ctx context.Context, actorID, demandID uuid.UUID, input DeadlineInput, key string) (Detail, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Decision != "propose" && input.Decision != "accept" && input.Decision != "reject" {
		return Detail{}, ErrInvalid
	}
	if input.Decision == "propose" && (!input.Deadline.After(time.Now()) || utf8.RuneCountInString(input.Reason) < 10 || utf8.RuneCountInString(input.Reason) > 2000) {
		return Detail{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Detail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayID, replay, err := registerCommand(ctx, tx, actorID, demandID, "deadline", key, input)
	if err != nil {
		return Detail{}, err
	}
	if replay {
		_ = tx.Rollback(ctx)
		return s.Get(ctx, actorID, replayID)
	}
	var clientID uuid.UUID
	var assigneeID *uuid.UUID
	var status, title string
	var old time.Time
	err = tx.QueryRow(ctx, `SELECT client_id,assignee_id,status,title,deadline FROM demands WHERE id=$1 FOR UPDATE`, demandID).Scan(&clientID, &assigneeID, &status, &title, &old)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	if assigneeID == nil || (actorID != clientID && actorID != *assigneeID) {
		return Detail{}, ErrForbidden
	}
	if status != "assigned" && status != "revision" {
		return Detail{}, ErrConflict
	}
	note := input.Reason
	event := "deadline_proposed"
	var changeID uuid.UUID
	if input.Decision == "propose" {
		if !input.Deadline.After(old) {
			return Detail{}, ErrInvalid
		}
		err = tx.QueryRow(ctx, `INSERT INTO task_deadline_changes(demand_id,proposed_by,previous_deadline,deadline,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING id`, demandID, actorID, old, input.Deadline, input.Reason).Scan(&changeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, ErrConflict
		}
		if err != nil {
			return Detail{}, err
		}
	} else {
		var proposer uuid.UUID
		var deadline, previous time.Time
		err = tx.QueryRow(ctx, `SELECT id,proposed_by,deadline,previous_deadline,reason FROM task_deadline_changes WHERE id=$1 AND demand_id=$2 AND status='pending' FOR UPDATE`, input.ChangeID, demandID).Scan(&changeID, &proposer, &deadline, &previous, &note)
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, ErrConflict
		}
		if err != nil {
			return Detail{}, err
		}
		if input.Decision == "accept" {
			if proposer == actorID {
				return Detail{}, ErrForbidden
			}
			if !deadline.After(time.Now()) || !old.Equal(previous) {
				return Detail{}, ErrConflict
			}
			if _, err = tx.Exec(ctx, `UPDATE demands SET deadline=$2,updated_at=now() WHERE id=$1`, demandID, deadline); err != nil {
				return Detail{}, err
			}
			event = "deadline_accepted"
		} else {
			event = "deadline_rejected"
		}
		if _, err = tx.Exec(ctx, `UPDATE task_deadline_changes SET status=$2,resolved_by=$3,resolved_at=now() WHERE id=$1`, changeID, map[string]string{"accept": "accepted", "reject": "rejected"}[input.Decision], actorID); err != nil {
			return Detail{}, err
		}
	}
	if err = insertEvent(ctx, tx, demandID, actorID, event, &status, status, note, map[string]any{"changeId": changeID, "decision": input.Decision}); err != nil {
		return Detail{}, err
	}
	recipient := clientID
	if actorID == clientID {
		recipient = *assigneeID
	}
	if err = notifyTask(ctx, tx, recipient, "task.deadline_changed", "Task deadline update", "Review the deadline agreement for “"+title+"”.", demandID, "deadline:"+changeID.String()+":"+input.Decision); err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, actorID, demandID)
}
