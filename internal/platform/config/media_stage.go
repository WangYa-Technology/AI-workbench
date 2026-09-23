package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MediaStageConfig bounds verified delivery files held by one process, including
// open download responses. It is independent of the account export budget.
type MediaStageConfig struct {
	MaxBytes     int64
	MaxObjects   int
	MinFreeBytes int64
	TempDir      string
}

func (c MediaStageConfig) WithDefaults() MediaStageConfig {
	if c.MaxBytes == 0 {
		c.MaxBytes = 512 << 20
	}
	if c.MaxObjects == 0 {
		c.MaxObjects = 16
	}
	if c.MinFreeBytes == 0 {
		c.MinFreeBytes = 64 << 20
	}
	return c
}

func loadMediaStageConfig() (MediaStageConfig, error) {
	c := (MediaStageConfig{}).WithDefaults()
	for _, item := range []struct {
		key      string
		target   *int64
		min, max int64
	}{
		// A repair can hold both its 100 MiB source and verified destination.
		{"MEDIA_STAGE_MAX_BYTES", &c.MaxBytes, 256 << 20, 8 << 30},
		{"MEDIA_STAGE_MIN_FREE_BYTES", &c.MinFreeBytes, 1 << 20, 1 << 40},
	} {
		v, err := integer64(item.key, *item.target)
		if err != nil {
			return c, err
		}
		if v < item.min || v > item.max {
			return c, fmt.Errorf("%s must be between %d and %d", item.key, item.min, item.max)
		}
		*item.target = v
	}
	n, err := integer("MEDIA_STAGE_MAX_OBJECTS", c.MaxObjects)
	if err != nil {
		return c, err
	}
	if n < 2 || n > 128 {
		return c, fmt.Errorf("MEDIA_STAGE_MAX_OBJECTS must be between 2 and 128")
	}
	c.MaxObjects = n
	c.TempDir = strings.TrimSpace(os.Getenv("MEDIA_STAGE_TEMP_DIR"))
	if c.TempDir != "" && (!filepath.IsAbs(c.TempDir) || strings.ContainsRune(c.TempDir, 0)) {
		return c, fmt.Errorf("MEDIA_STAGE_TEMP_DIR must be an absolute path")
	}
	return c, nil
}
