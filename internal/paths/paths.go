package paths

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Base         string
	Config       string
	Secret       string
	LegacySecret string
	LegacyTok    string
}

func Resolve() Paths {
	base := executableDir()
	if override := os.Getenv("QUOTABALL_BASE_DIR"); override != "" {
		base = filepath.Clean(override)
	}
	secret := filepath.Join(base, ".quotaball_secret.json")
	legacySecret := filepath.Join(base, ".krill_secret.json")
	return Paths{
		Base:         base,
		Config:       filepath.Join(base, "config.json"),
		Secret:       resolveSecretPath(secret, legacySecret),
		LegacySecret: legacySecret,
		LegacyTok:    filepath.Join(base, ".krill_token"),
	}
}

func resolveSecretPath(secret, legacySecret string) string {
	if _, err := os.Stat(secret); err == nil {
		return secret
	}
	if _, err := os.Stat(legacySecret); err != nil {
		return secret
	}
	if err := os.Rename(legacySecret, secret); err == nil {
		return secret
	}
	return legacySecret
}

func executableDir() string {
	exe, err := os.Executable()
	if err == nil {
		return filepath.Dir(exe)
	}
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	return "."
}
