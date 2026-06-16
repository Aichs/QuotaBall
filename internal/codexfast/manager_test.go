package codexfast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureCodexConfigInjectsFastProxyIntoActiveProvider(t *testing.T) {
	raw := `model_provider = "custom"

[model_providers.custom]
name = "custom"
base_url = "https://api.example.test/codex/v1"

[features]
other = true
`
	got, original, err := ConfigureCodexConfig(raw, "http://127.0.0.1:48251/codex/v1")
	if err != nil {
		t.Fatal(err)
	}
	if original != "https://api.example.test/codex/v1" {
		t.Fatalf("original base URL = %q", original)
	}
	for _, want := range []string{
		`model_provider = "custom"`,
		`service_tier = "priority"`,
		`base_url = "http://127.0.0.1:48251/codex/v1"`,
		`fast_mode = true`,
		`fast_default_opt_out = false`,
		`other = true`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("configured config missing %q:\n%s", want, got)
		}
	}
}

func TestConfigureCodexConfigCreatesProviderTableWhenMissing(t *testing.T) {
	got, original, err := ConfigureCodexConfig("", "http://127.0.0.1:48251/codex/v1")
	if err != nil {
		t.Fatal(err)
	}
	if original != "" {
		t.Fatalf("original base URL = %q, want empty", original)
	}
	for _, want := range []string{
		`model_provider = "custom"`,
		`[model_providers.custom]`,
		`name = "custom"`,
		`base_url = "http://127.0.0.1:48251/codex/v1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("new config missing %q:\n%s", want, got)
		}
	}
}

func TestRestoreCodexConfigOnlyRestoresProxyBaseURL(t *testing.T) {
	raw := `model_provider = "custom"

[model_providers.custom]
base_url = "http://127.0.0.1:48251/codex/v1"
`
	got, err := RestoreCodexConfig(raw, "http://127.0.0.1:48251/codex/v1", "https://api.example.test/codex/v1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `base_url = "https://api.example.test/codex/v1"`) {
		t.Fatalf("restored config did not restore original URL:\n%s", got)
	}

	unchanged, err := RestoreCodexConfig(got, "http://127.0.0.1:48251/codex/v1", "https://other.example.test/codex/v1")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != got {
		t.Fatalf("restore should not overwrite a non-proxy base URL")
	}
}

func TestEnabledInCodexConfigDetectsProxyBaseURL(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`model_provider = "custom"

[model_providers.custom]
base_url = "http://127.0.0.1:48251/codex/v1"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{CodexHome: home, Host: DefaultHost, Port: DefaultPort}
	enabled, err := manager.EnabledInCodexConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("EnabledInCodexConfig = false, want true")
	}
}
