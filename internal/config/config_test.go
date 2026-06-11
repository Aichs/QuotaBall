package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyKeysAndNormalizesValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{
		"email":"user@example.com",
		"refresh_interval_sec":1,
		"window_opacity":0.5,
		"always_on_top":false,
		"window_x":120,
		"window_y":240,
		"tbar_metric":"bad-value"
	}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Email != "user@example.com" {
		t.Fatalf("Email = %q", cfg.Email)
	}
	if cfg.RefreshSec != 3 {
		t.Fatalf("RefreshSec = %d, want minimum 3", cfg.RefreshSec)
	}
	if cfg.Opacity != 0.5 {
		t.Fatalf("Opacity = %v", cfg.Opacity)
	}
	if cfg.OnTop {
		t.Fatal("OnTop should honor legacy false value")
	}
	if cfg.WX == nil || *cfg.WX != 120 || cfg.WY == nil || *cfg.WY != 240 {
		t.Fatalf("window position was not migrated: %#v %#v", cfg.WX, cfg.WY)
	}
	if cfg.TbarMetric != "daily" {
		t.Fatalf("TbarMetric = %q, want daily", cfg.TbarMetric)
	}
}

func TestLoadMissingFileReturnsDefaultsAndCreatesParentSafeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.RefreshSec != 60 || !cfg.RememberLogin || cfg.Theme != "light" || !cfg.TbarEnabled {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadAcceptsUTF8BOMConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"email":"bom@example.com"}`)...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Email != "bom@example.com" {
		t.Fatalf("Email = %q", cfg.Email)
	}
}
