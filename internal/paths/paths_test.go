package paths

import (
	"os"
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
	if got.Secret != filepath.Join(filepath.Clean(base), ".quotaball_secret.json") {
		t.Fatalf("Secret = %q", got.Secret)
	}
	if got.LegacySecret != filepath.Join(filepath.Clean(base), ".krill_secret.json") {
		t.Fatalf("LegacySecret = %q", got.LegacySecret)
	}
}

func TestResolveMigratesLegacySecretName(t *testing.T) {
	base := t.TempDir()
	t.Setenv("QUOTABALL_BASE_DIR", base)
	legacy := filepath.Join(base, ".krill_secret.json")
	if err := os.WriteFile(legacy, []byte(`{"token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Resolve()
	if got.Secret != filepath.Join(base, ".quotaball_secret.json") {
		t.Fatalf("Secret = %q", got.Secret)
	}
	if _, err := os.Stat(got.Secret); err != nil {
		t.Fatalf("new secret was not created: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy secret should be moved away, stat err=%v", err)
	}
}
