package password

import (
	"strings"
	"testing"
)

func TestNewPasswordBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"minimum", strings.Repeat("a", 10), true},
		{"too short", strings.Repeat("a", 9), false},
		{"72 ASCII bytes", strings.Repeat("a", 72), true},
		{"73 ASCII bytes", strings.Repeat("a", 73), false},
		{"72 UTF8 bytes", strings.Repeat("密", 24), true},
		{"75 UTF8 bytes", strings.Repeat("密", 25), false},
		{"9 Unicode characters", strings.Repeat("密", 9), false},
		{"72 emoji bytes", strings.Repeat("🔑", 18), true},
		{"76 emoji bytes", strings.Repeat("🔑", 19), false},
		{"invalid UTF8", strings.Repeat("a", 10) + "\xff", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidNew(tc.value); got != tc.valid {
				t.Fatalf("valid=%v, want %v", got, tc.valid)
			}
		})
	}
}
