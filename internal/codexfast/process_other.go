//go:build !windows

package codexfast

import (
	"context"
	"errors"
)

func enableAutoStart(string) error {
	return nil
}

func disableAutoStart() error {
	return nil
}

func startProxy(context.Context, string, string, int) error {
	return errors.New("Codex Fast proxy switch is only supported on Windows")
}

func stopProxy(context.Context, string, int) error {
	return nil
}
