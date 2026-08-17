package systemsettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrDisabled = errors.New("platform operation disabled")

const (
	Registrations = "registrations"
	Generations   = "generations"
	Publishing    = "publishing"
	Checkout      = "marketplace_checkout"
	TaskCreation  = "task_creation"
)

func RequireTx(ctx context.Context, tx pgx.Tx, capability string) error {
	columns := map[string]string{Registrations: "registrations_enabled", Generations: "generations_enabled", Publishing: "publishing_enabled", Checkout: "marketplace_checkout_enabled", TaskCreation: "task_creation_enabled"}
	column, ok := columns[capability]
	if !ok {
		return fmt.Errorf("unknown system capability %q", capability)
	}
	var enabled bool
	query := `SELECT r.` + column + ` FROM system_setting_state s JOIN system_setting_revisions r ON r.id=s.active_revision_id WHERE s.singleton=true`
	if err := tx.QueryRow(ctx, query).Scan(&enabled); err != nil {
		return fmt.Errorf("load system setting: %w", err)
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}
