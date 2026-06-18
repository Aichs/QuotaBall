//go:build windows

package ui

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

type TrayControllerOptions struct {
	TogglePanel func()
	Refresh     func(bool)
	Logout      func()
	Quit        func()
}

type TrayController struct {
	opts TrayControllerOptions

	ready chan error
	done  chan struct{}

	mu     sync.Mutex
	host   *walk.MainWindow
	notify *walk.NotifyIcon
}

func StartTrayController(opts TrayControllerOptions) (*TrayController, error) {
	if opts.TogglePanel == nil || opts.Quit == nil {
		return nil, errors.New("tray controller requires panel and quit callbacks")
	}
	c := &TrayController{
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

func (c *TrayController) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.done)

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

	notify, err := walk.NewNotifyIcon(host)
	if err != nil {
		host.Dispose()
		c.ready <- err
		return
	}
	icon, err := walk.NewIconFromImage(makeTrayImage())
	if err != nil {
		_ = notify.Dispose()
		host.Dispose()
		c.ready <- err
		return
	}
	if err := notify.SetIcon(icon); err != nil {
		_ = notify.Dispose()
		host.Dispose()
		c.ready <- err
		return
	}
	if err := notify.SetToolTip("QuotaBall"); err != nil {
		_ = notify.Dispose()
		host.Dispose()
		c.ready <- err
		return
	}
	notify.MouseDown().Attach(func(_ int, _ int, button walk.MouseButton) {
		if button == walk.LeftButton {
			c.togglePanel()
		}
	})
	if err := c.installMenu(notify); err != nil {
		_ = notify.Dispose()
		host.Dispose()
		c.ready <- err
		return
	}
	if err := notify.SetVisible(true); err != nil {
		_ = notify.Dispose()
		host.Dispose()
		c.ready <- err
		return
	}

	c.mu.Lock()
	c.host = host
	c.notify = notify
	c.mu.Unlock()
	c.ready <- nil

	host.Run()

	_ = notify.Dispose()
	host.Dispose()
}

func (c *TrayController) installMenu(notify *walk.NotifyIcon) error {
	for _, item := range []struct {
		text string
		fn   func()
	}{
		{"显示/隐藏面板", c.togglePanel},
		{"立即刷新", func() { c.refresh(true) }},
		{"退出登录", c.logout},
		{"退出", c.quit},
	} {
		act := walk.NewAction()
		act.SetText(item.text)
		fn := item.fn
		act.Triggered().Attach(fn)
		if err := notify.ContextMenu().Actions().Add(act); err != nil {
			return err
		}
	}
	return nil
}

func (c *TrayController) SetSnapshot(s krill.Snapshot) {
	c.synchronize(func() {
		if c.notify == nil {
			return
		}
		_ = c.notify.SetToolTip(trayTooltip(s))
	})
}

func (c *TrayController) Close() {
	c.synchronize(func() {
		if c.notify != nil {
			_ = c.notify.SetVisible(false)
			c.notify = nil
		}
		if c.host != nil {
			c.host.Close()
		}
	})
	<-c.done
}

func (c *TrayController) synchronize(fn func()) {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()
	if host == nil || host.IsDisposed() {
		return
	}
	host.Synchronize(fn)
}

func (c *TrayController) togglePanel() {
	go c.opts.TogglePanel()
}

func (c *TrayController) refresh(reveal bool) {
	if c.opts.Refresh != nil {
		go c.opts.Refresh(reveal)
	}
}

func (c *TrayController) logout() {
	if c.opts.Logout != nil {
		go c.opts.Logout()
	}
}

func (c *TrayController) quit() {
	go c.opts.Quit()
}

func trayTooltip(s krill.Snapshot) string {
	if s.OK {
		if s.Provider == config.ProviderNewAPI {
			return fmt.Sprintf("QuotaBall - 余额 $%.2f / 消耗 $%.2f", s.Wallet, s.Spend)
		}
		if s.Provider == config.ProviderSub2 {
			return fmt.Sprintf("QuotaBall - 余额 $%.2f / 本月剩余 $%.2f", s.Wallet, s.Summary.TotalMonthlyRemainingUSD)
		}
		return fmt.Sprintf("QuotaBall - 周剩余 $%.2f / 已用 $%.2f", s.RemainingWeekly(), s.Spend)
	}
	if s.Err != "" {
		return "QuotaBall - " + s.Err
	}
	return "QuotaBall"
}
