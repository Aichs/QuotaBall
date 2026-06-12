package paths

import (
	"path/filepath"
	"testing"
)

func TestResolveUsesBaseDirOverride(t *testing.T) {
	base := filepath.Join(t.TempDir(), "debug-base")
	t.Setenv("QUOTABALL_BASE_DIR", base)

	got := Resolve()
	if got.Base != filepath.Clean(base) {
		t.Fatalf("Base = %q, want %q", got.Base, filepath.Clean(base))
	}
	if got.Config != filepath.Join(filepath.Clean(base), "config.json") {
		t.Fatalf("Config = %q", got.Config)
	}
}
