package billing

import (
	"context"

	"github.com/google/uuid"
)

// SubscriptionModel exposes only the labels needed to configure a plan. Provider
// endpoints, credentials, pricing and runtime configuration stay admin:providers-only.
type SubscriptionModel struct {
	ID           uuid.UUID `json:"id"`
	DisplayName  string    `json:"displayName"`
	Mode         string    `json:"mode"`
	ProviderName string    `json:"providerName"`
}

func (s *Service) ListSubscriptionModels(ctx context.Context) ([]SubscriptionModel, error) {
	// Disabled models remain selectable, as in the provider catalog previously
	// used by the plan editor. Archived models/providers are not candidates.
	rows, err := s.pool.Query(ctx, `
		SELECT m.id,m.display_name,m.mode,p.name
		FROM provider_config_models m JOIN provider_configs p ON p.id=m.provider_id
		WHERE m.archived_at IS NULL AND p.archived_at IS NULL
		ORDER BY lower(p.name),p.id,lower(m.display_name),m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SubscriptionModel, 0)
	for rows.Next() {
		var item SubscriptionModel
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Mode, &item.ProviderName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
