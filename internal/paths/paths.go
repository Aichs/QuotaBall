package paths

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Base      string
	Config    string
	Secret    string
	LegacyTok string
}

func Resolve() Paths {
	base := executableDir()
	if override := os.Getenv("QUOTABALL_BASE_DIR"); override != "" {
		base = filepath.Clean(override)
	}
	return Paths{
		Base:      base,
		Config:    filepath.Join(base, "config.json"),
		Secret:    filepath.Join(base, ".krill_secret.json"),
		LegacyTok: filepath.Join(base, ".krill_token"),
	}
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
