//go:build !windows

package ui

import (
	"errors"

	"quotaball/internal/krill"
)

type TrayControllerOptions struct {
	TogglePanel func()
	Refresh     func(bool)
	Logout      func()
	Quit        func()
}

type TrayController struct{}

func StartTrayController(_ TrayControllerOptions) (*TrayController, error) {
	return nil, errors.New("system tray is only supported on Windows")
}

func (c *TrayController) SetSnapshot(_ krill.Snapshot) {}
func (c *TrayController) Close()                       {}
