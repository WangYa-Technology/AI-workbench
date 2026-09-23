//go:build !unix

package media

import "context"

func (s *LocalStore) InventoryScope() InventoryScope {
	return InventoryScope{Backend: s.Backend(), Location: s.root}
}

func (s *LocalStore) WalkInventory(context.Context, int, func([]InventoryEntry) error) error {
	return errLocalPlatform
}
