//go:build windows

package ui

import (
	"errors"
	"runtime"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

type GlassControllerOptions struct {
	LoadConfig   func() config.Config
	UpdateConfig func(func(*config.Config))
	TogglePanel  func()
	Refresh      func(bool)
	Quit         func()
}

type GlassController struct {
	opts GlassControllerOptions

	ready chan error
	done  chan struct{}

	mu   sync.Mutex
	host *walk.MainWindow
	ball *glassBall

	uiThreadID uint32
	closing    bool
	closeOnce  sync.Once
}

func StartGlassController(opts GlassControllerOptions) (*GlassController, error) {
	if opts.LoadConfig == nil || opts.UpdateConfig == nil {
		return nil, errors.New("glass controller requires config callbacks")
	}
	c := &GlassController{
		opts:  opts,
		ready: make(chan error, 1),
		done:  make(chan struct{}),
	}
	go c.run()
	if err := <-c.ready; err != nil {
		return nil, err
	}
	return c, nil
}

func (c *GlassController) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.done)

	c.mu.Lock()
	c.uiThreadID = win.GetCurrentThreadId()
	c.mu.Unlock()

	var host *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &host,
		Title:    "QuotaBall",
		Size:     Size{Width: 1, Height: 1},
		Layout:   VBox{MarginsZero: true},
	}).Create(); err != nil {
		c.ready <- err
		return
	}
	host.SetVisible(false)

	gb, err := newGlassBall(c)
	if err != nil {
		host.Dispose()
		c.ready <- err
		return
	}

	c.mu.Lock()
	c.host = host
	c.ball = gb
	c.mu.Unlock()
	c.ready <- nil

	host.Run()
}

func (c *GlassController) SetSnapshot(s krill.Snapshot) {
	c.synchronize(func() {
		if c.ball != nil {
			c.ball.setSnapshot(s)
		}
	})
}

func (c *GlassController) Show() {
	c.synchronize(func() {
		if c.ball != nil {
			c.ball.show()
		}
	})
}

func (c *GlassController) Hide() {
	c.synchronize(func() {
		if c.ball != nil {
			c.ball.hide()
		}
	})
}

func (c *GlassController) Close() {
	isUIThread := c.isUIThread()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closing = true
		host := c.host
		c.mu.Unlock()
		if host == nil || host.IsDisposed() {
			return
		}

		closeFn := func() {
			c.closeOnUIThread()
		}
		if isUIThread {
			closeFn()
			return
		}
		host.Synchronize(closeFn)
	})
	if !isUIThread {
		<-c.done
	}
}

func (c *GlassController) closeOnUIThread() {
	if c.ball != nil {
		c.ball.close()
		c.ball = nil
	}
	if c.host != nil {
		c.host.Close()
	}
}

func (c *GlassController) synchronize(fn func()) {
	c.mu.Lock()
	host := c.host
	closing := c.closing
	c.mu.Unlock()
	if closing || host == nil || host.IsDisposed() {
		return
	}
	if c.isUIThread() {
		fn()
		return
	}
	host.Synchronize(func() {
		c.mu.Lock()
		closing := c.closing
		c.mu.Unlock()
		if closing {
			return
		}
		fn()
	})
}

func (c *GlassController) isUIThread() bool {
	c.mu.Lock()
	uiThreadID := c.uiThreadID
	c.mu.Unlock()
	return uiThreadID != 0 && win.GetCurrentThreadId() == uiThreadID
}

func (c *GlassController) loadGlassConfig() config.Config {
	return c.opts.LoadConfig()
}

func (c *GlassController) mutateGlassConfig(fn func(*config.Config)) {
	c.opts.UpdateConfig(fn)
}

func (c *GlassController) togglePanel() {
	if c.opts.TogglePanel != nil {
		go c.opts.TogglePanel()
	}
}

func (c *GlassController) refresh(reveal bool) {
	if c.opts.Refresh != nil {
		go c.opts.Refresh(reveal)
	}
}

func (c *GlassController) quit() {
	if c.opts.Quit != nil {
		go c.opts.Quit()
	}
}
