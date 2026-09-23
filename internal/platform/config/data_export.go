package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DataExportConfig is a per-process preparation budget, not a cluster-wide
// storage reservation. The deployment must also bound its temporary filesystem.
type DataExportConfig struct {
	MaxBytes     int64
	MaxRowBytes  int64
	MinFreeBytes int64
	TempDir      string
}

func (c DataExportConfig) WithDefaults() DataExportConfig {
	if c.MaxBytes == 0 {
		c.MaxBytes = 512 << 20
	}
	if c.MaxRowBytes == 0 {
		c.MaxRowBytes = 8 << 20
	}
	if c.MinFreeBytes == 0 {
		c.MinFreeBytes = 64 << 20
	}
	return c
}

func loadDataExportConfig() (DataExportConfig, error) {
	c := (DataExportConfig{}).WithDefaults()
	for _, item := range []struct {
		key      string
		target   *int64
		min, max int64
	}{
		{"DATA_EXPORT_MAX_BYTES", &c.MaxBytes, 1 << 20, 8 << 30},
		{"DATA_EXPORT_MAX_ROW_BYTES", &c.MaxRowBytes, 64 << 10, 64 << 20},
		{"DATA_EXPORT_MIN_FREE_BYTES", &c.MinFreeBytes, 1 << 20, 1 << 40},
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
	if c.MaxRowBytes > c.MaxBytes {
		return c, fmt.Errorf("DATA_EXPORT_MAX_ROW_BYTES must not exceed DATA_EXPORT_MAX_BYTES")
	}
	c.TempDir = strings.TrimSpace(os.Getenv("DATA_EXPORT_TEMP_DIR"))
	if c.TempDir != "" && !filepath.IsAbs(c.TempDir) {
		return c, fmt.Errorf("DATA_EXPORT_TEMP_DIR must be an absolute path")
	}
	return c, nil
}
