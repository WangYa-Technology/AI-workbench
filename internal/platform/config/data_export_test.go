package config

import (
	"strings"
	"testing"
)

func TestDataExportBudgetConfig(t *testing.T) {
	keys := []string{"DATA_EXPORT_MAX_BYTES", "DATA_EXPORT_MAX_ROW_BYTES", "DATA_EXPORT_MIN_FREE_BYTES", "DATA_EXPORT_TEMP_DIR"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	got, err := loadDataExportConfig()
	if err != nil || got != (DataExportConfig{}).WithDefaults() {
		t.Fatal(got, err)
	}
	for _, tc := range []struct{ key, value string }{
		{keys[0], "0"}, {keys[0], "-1"}, {keys[0], "8589934593"}, {keys[0], "one"},
		{keys[1], "65535"}, {keys[1], "67108865"}, {keys[2], "0"}, {keys[3], "relative/path"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := loadDataExportConfig()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatal(err)
			}
		})
	}
	t.Setenv(keys[0], "1048576")
	if _, err = loadDataExportConfig(); err == nil {
		t.Fatal("row may exceed package")
	}
	t.Setenv(keys[1], "65536")
	t.Setenv(keys[3], t.TempDir())
	got, err = loadDataExportConfig()
	if err != nil || got.MaxBytes != 1<<20 || got.MaxRowBytes != 64<<10 {
		t.Fatal(got, err)
	}
}
