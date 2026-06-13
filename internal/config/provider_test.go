package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultProviderIsKrill(t *testing.T) {
	cfg := Default()
	cfg.Normalize()

	if cfg.Provider != ProviderKrill {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, ProviderKrill)
	}
}

func TestLoadNormalizesUnknownProviderToKrill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	baseURL := testNewAPIBaseURL()
	if err := os.WriteFile(path, []byte(`{"provider":"unknown","newapi_base_url":"`+baseURL+`/"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Provider != ProviderKrill {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, ProviderKrill)
	}
	if cfg.NewAPIBaseURL != baseURL {
		t.Fatalf("NewAPIBaseURL = %q, want normalized base URL", cfg.NewAPIBaseURL)
	}
}

func TestLoadNormalizesSub2PlaceholderToKrill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"sub2"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Provider != ProviderKrill {
		t.Fatalf("Provider = %q, want %q while Sub2 is only a placeholder", cfg.Provider, ProviderKrill)
	}
}

func TestSaveNewAPIConfigDoesNotPersistSensitiveTokenFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Provider = ProviderNewAPI
	cfg.NewAPIBaseURL = testNewAPIBaseURL() + "/"
	cfg.Password = "test-secret-password"

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(bytes.ToLower(raw), []byte("newapi_token")) ||
		bytes.Contains(bytes.ToLower(raw), []byte("access_token")) ||
		bytes.Contains(bytes.ToLower(raw), []byte("bearer")) {
		t.Fatalf("config must not contain NewAPI token material: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"password": "test-secret-password"`)) {
		t.Fatalf("config must not contain plaintext password: %s", raw)
	}
}

func testNewAPIBaseURL() string {
	return "https://newapi.example.test"
}
