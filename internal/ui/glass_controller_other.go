//go:build !windows

package ui

import (
	"errors"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
)

type GlassControllerOptions struct {
	LoadConfig   func() config.Config
	UpdateConfig func(func(*config.Config))
	TogglePanel  func()
	Refresh      func(bool)
	Quit         func()
}

type GlassController struct{}

func StartGlassController(_ GlassControllerOptions) (*GlassController, error) {
	return nil, errors.New("glass ball is only supported on Windows")
}

func (c *GlassController) SetSnapshot(_ krill.Snapshot) {}
func (c *GlassController) Show()                        {}
func (c *GlassController) Hide()                        {}
func (c *GlassController) Close()                       {}
