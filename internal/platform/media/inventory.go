package media

import (
	"context"
	"errors"
)

var ErrInventoryLimit = errors.New("media inventory object limit reached")

const InventoryBatchSize = 100

// Inventory is an optional, read-only capability. It does not open object
// bodies or certify an atomic snapshot, retention eligibility or deletion safety.
type Inventory interface {
	InventoryScope() InventoryScope
	WalkInventory(context.Context, int, func([]InventoryEntry) error) error
}

type InventoryScope struct {
	Backend        string `json:"backend"`
	Location       string `json:"location"`
	Prefix         string `json:"prefix,omitempty"`
	EndpointSHA256 string `json:"endpointSha256,omitempty"`
}

type InventoryEntry struct {
	Key  string `json:"-"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

func validInventoryLimit(limit int) error {
	if limit < 1 || limit > 1_000_000 {
		return ErrInventoryLimit
	}
	return nil
}
