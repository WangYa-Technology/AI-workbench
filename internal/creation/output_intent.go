package creation

import (
	"context"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/generationoutput"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func (s *Service) prepareOutputIntent(ctx context.Context, owner, generation, asset uuid.UUID, output ProviderOutput, job jobs.Job) (*generationoutput.Intent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = requireActiveGenerationAccount(ctx, tx, owner); err != nil {
		if err == ErrForbidden {
			return nil, ErrAccountUnavailable
		}
		return nil, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM generations WHERE id=$1 FOR UPDATE`, generation).Scan(&status); err != nil {
		return nil, err
	}
	if status == "succeeded" || status == "cancelled" || status == "failed" {
		return nil, tx.Commit(ctx)
	}
	if _, err = lockExecution(ctx, tx, generation, job); err != nil {
		return nil, err
	}
	intent, err := generationoutput.RegisterTx(ctx, tx, owner, generation, asset, s.stores.Primary(), output.Content, output.Extension)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &intent, nil
}
