package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"

	"quotaball/internal/atomicfile"
)

const (
	ThemeLight = "light"
	ThemeDark  = "dark"

	ProviderKrill  = "krill"
	ProviderNewAPI = "newapi"
	ProviderSub2   = "sub2"
)

type Config struct {
	Email                 string  `json:"email"`
	Password              string  `json:"password"`
	Provider              string  `json:"provider"`
	NewAPIBaseURL         string  `json:"newapi_base_url"`
	Sub2BaseURL           string  `json:"sub2_base_url"`
	Sub2Email             string  `json:"sub2_email"`
	RememberLogin         bool    `json:"remember_login"`
	RefreshSec            int     `json:"refresh_sec"`
	Opacity               float64 `json:"opacity"`
	OnTop                 bool    `json:"on_top"`
	Theme                 string  `json:"theme"`
	WX                    *int    `json:"wx"`
	WY                    *int    `json:"wy"`
	TbarX                 *int    `json:"tbar_x"`
	TbarY                 *int    `json:"tbar_y"`
	TbarEnabled           bool    `json:"tbar_enabled"`
	TbarMetric            string  `json:"tbar_metric"`
	CodexFastProxyEnabled bool    `json:"codex_fast_proxy_enabled"`
}

func Default() Config {
	return Config{
		Provider:      ProviderKrill,
		RememberLogin: true,
		RefreshSec:    60,
		Opacity:       0.96,
		OnTop:         true,
		Theme:         ThemeLight,
		TbarEnabled:   true,
		TbarMetric:    "weekly",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return cfg, err
	}
	migrateLegacy(legacy)

	merged, err := json.Marshal(legacy)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return cfg, err
	}
	cfg.Normalize()
	return cfg, nil
}

func Save(path string, cfg Config) error {
	cfg.Normalize()
	cfg.Password = ""
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, raw, 0o600)
}

func (c *Config) Normalize() {
	switch c.Provider {
	case ProviderKrill, ProviderNewAPI, ProviderSub2:
	default:
		c.Provider = ProviderKrill
	}
	c.NewAPIBaseURL = normalizeBaseURL(c.NewAPIBaseURL)
	c.Sub2BaseURL = normalizeBaseURL(c.Sub2BaseURL)
	c.Sub2Email = strings.TrimSpace(c.Sub2Email)
	if c.RefreshSec < 3 {
		c.RefreshSec = 3
	}
	if c.Opacity <= 0 || c.Opacity > 1 {
		c.Opacity = Default().Opacity
	}
	if c.Theme != ThemeDark {
		c.Theme = ThemeLight
	}
	switch c.TbarMetric {
	case "daily", "monthly":
	default:
		c.TbarMetric = "weekly"
	}
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func migrateLegacy(raw map[string]json.RawMessage) {
	aliases := map[string]string{
		"refresh_interval_sec": "refresh_sec",
		"window_opacity":       "opacity",
		"always_on_top":        "on_top",
		"window_x":             "wx",
		"window_y":             "wy",
	}
	for oldKey, newKey := range aliases {
		if _, hasNew := raw[newKey]; hasNew {
			continue
		}
		if v, ok := raw[oldKey]; ok {
			raw[newKey] = v
		}
	}
}
