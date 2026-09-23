package config

import (
	"strings"
	"testing"
)

func TestMediaStageConfig(t *testing.T) {
	keys := []string{"MEDIA_STAGE_MAX_BYTES", "MEDIA_STAGE_MAX_OBJECTS", "MEDIA_STAGE_MIN_FREE_BYTES", "MEDIA_STAGE_TEMP_DIR"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	got, err := loadMediaStageConfig()
	if err != nil || got != (MediaStageConfig{}).WithDefaults() {
		t.Fatal(got, err)
	}
	for _, tc := range []struct{ key, value string }{
		{keys[0], "0"}, {keys[0], "-1"}, {keys[0], "268435455"}, {keys[0], "8589934593"}, {keys[0], "no"},
		{keys[1], "1"}, {keys[1], "129"}, {keys[1], "1.5"}, {keys[2], "0"}, {keys[2], "1099511627777"}, {keys[3], "relative/path"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := loadMediaStageConfig()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatal(err)
			}
		})
	}
	t.Setenv(keys[0], "268435456")
	t.Setenv(keys[1], "2")
	t.Setenv(keys[3], t.TempDir())
	got, err = loadMediaStageConfig()
	if err != nil || got.MaxBytes != 256<<20 || got.MaxObjects != 2 || got.TempDir == "" {
		t.Fatal(got, err)
	}
}
