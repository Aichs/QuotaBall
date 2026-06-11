package wailsui

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/lxn/win"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windowsOptions "github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
	"krill_monitor/internal/paths"
	"krill_monitor/internal/secret"
	"krill_monitor/internal/ui"
)

const (
	panelWidth  = 540
	panelHeight = 820
)

//go:embed all:frontend/src
var assets embed.FS

type App struct {
	ctx context.Context

	paths paths.Paths
	cfg   config.Config
	svc   *krill.Service

	mu         sync.Mutex
	snap       krill.Snapshot
	refreshing bool
	visible    bool
	quitting   bool
	authGen    uint64

	stop     chan struct{}
	commands chan appCommand
	glass    *ui.GlassController
	tray     *ui.TrayController
}

type appCommandKind int

const (
	commandTogglePanel appCommandKind = iota
	commandRefresh
	commandLogout
	commandQuit
)

type appCommand struct {
	kind   appCommandKind
	reveal bool
}

func Run() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	frontend, err := fs.Sub(assets, "frontend/src")
	if err != nil {
		return err
	}
	return wails.Run(&options.App{
		Title:             "Krill AI 额度监控",
		Width:             panelWidth,
		Height:            panelHeight,
		MinWidth:          500,
		MinHeight:         740,
		MaxWidth:          panelWidth,
		MaxHeight:         panelHeight,
		DisableResize:     true,
		Frameless:         true,
		AlwaysOnTop:       app.cfg.OnTop,
		HideWindowOnClose: true,
		BackgroundColour:  options.NewRGBA(0, 0, 0, 0),
		AssetServer: &assetserver.Options{
			Assets: frontend,
		},
		Bind: []interface{}{app},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
		},
		OnBeforeClose: func(ctx context.Context) bool {
			app.beforeClose(ctx)
			return !app.isQuitting()
		},
		OnShutdown: func(ctx context.Context) {
			app.shutdown()
		},
		Windows: &windowsOptions.Options{
			WebviewIsTransparent:                true,
			WindowIsTranslucent:                 true,
			DisableFramelessWindowDecorations:   true,
			Theme:                               windowsOptions.Light,
			WebviewGpuIsDisabled:                false,
			WebviewDisableRendererCodeIntegrity: false,
		},
	})
}

func NewApp() (*App, error) {
	p := paths.Resolve()
	cfg, err := config.Load(p.Config)
	if err != nil {
		return nil, err
	}
	st := secret.NewStore(p.Secret)
	if cfg.Password != "" {
		_ = st.Set("password", cfg.Password)
		cfg.Password = ""
		_ = config.Save(p.Config, cfg)
	}
	app := &App{
		paths:    p,
		cfg:      cfg,
		stop:     make(chan struct{}),
		commands: make(chan appCommand, 32),
	}
	app.svc = &krill.Service{
		Client:    krill.NewClient(),
		Config:    &app.cfg,
		Secrets:   st,
		LegacyTok: p.LegacyTok,
	}
	app.svc.Configure(app.cfg)
	app.snap = krill.EmptySnapshot("正在检查登录状态...")
	if !app.svc.HasLoginState() {
		app.snap = krill.EmptySnapshot("请登录 Krill AI")
	}
	return app, nil
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.visible = true
	cfg := a.cfg
	snap := a.snap
	a.mu.Unlock()

	a.positionWindow(ctx, cfg)
	wailsruntime.WindowSetAlwaysOnTop(ctx, cfg.OnTop)
	hideMainWindowFromTaskbar()
	go a.commandLoop()
	_ = a.ensureTrayController()
	a.syncGlass(snap)
	go a.scheduleLoop()
	if a.svc.HasLoginState() {
		go func() {
			_, _ = a.refresh(false)
		}()
	}
}

func (a *App) beforeClose(ctx context.Context) {
	a.saveWindowPosition(ctx)
	a.mu.Lock()
	a.visible = false
	a.mu.Unlock()
	if !a.isQuitting() {
		wailsruntime.WindowHide(ctx)
	}
}

func (a *App) shutdown() {
	a.mu.Lock()
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	glass := a.glass
	a.glass = nil
	tray := a.tray
	a.tray = nil
	a.mu.Unlock()
	if glass != nil {
		glass.Close()
	}
	if tray != nil {
		tray.Close()
	}
}

func (a *App) enqueueCommand(cmd appCommand) {
	select {
	case a.commands <- cmd:
	case <-a.stop:
	default:
		go func() {
			select {
			case a.commands <- cmd:
			case <-a.stop:
			}
		}()
	}
}

func (a *App) commandLoop() {
	for {
		select {
		case cmd := <-a.commands:
			a.handleCommand(cmd)
		case <-a.stop:
			return
		}
	}
}

func (a *App) handleCommand(cmd appCommand) {
	if cmd.kind != commandQuit && a.isQuitting() {
		return
	}
	switch cmd.kind {
	case commandTogglePanel:
		_ = a.TogglePanel()
	case commandRefresh:
		_, _ = a.refresh(cmd.reveal)
	case commandLogout:
		_, _ = a.Logout()
	case commandQuit:
		_ = a.Quit()
	}
}

func (a *App) Bootstrap() (AppStateDTO, error) {
	a.mu.Lock()
	cfg := a.cfg
	snap := a.snap
	a.mu.Unlock()
	hasSavedLogin := a.svc.HasSavedLoginState()
	return AppStateDTO{
		Config:   configDTO(cfg, hasSavedLogin),
		Snapshot: snapshotDTO(snap),
	}, nil
}

func (a *App) Snapshot() (SnapshotDTO, error) {
	a.mu.Lock()
	snap := a.snap
	a.mu.Unlock()
	return snapshotDTO(snap), nil
}

func (a *App) Login(req LoginRequest) (SnapshotDTO, error) {
	email := strings.TrimSpace(req.Email)
	if email == "" || req.Password == "" {
		return SnapshotDTO{}, errors.New("请输入邮箱和密码")
	}

	a.mu.Lock()
	if a.refreshing {
		a.mu.Unlock()
		return SnapshotDTO{}, errors.New("正在刷新，请稍后")
	}
	a.refreshing = true
	authGen := a.authGen
	a.mu.Unlock()
	defer a.setRefreshing(false)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.svc.Login(ctx, email, req.Password, req.RememberLogin); err != nil {
		return SnapshotDTO{}, err
	}
	if current, stale := a.snapshotIfAuthChanged(authGen); stale {
		return snapshotDTO(current), krill.ErrAuthRequired
	}

	a.mu.Lock()
	a.cfg.Email = email
	a.cfg.RememberLogin = req.RememberLogin
	a.cfg.Password = ""
	a.authGen++
	cfg := a.cfg
	a.mu.Unlock()
	a.svc.Configure(cfg)
	err := config.Save(a.paths.Config, cfg)
	if err != nil {
		return SnapshotDTO{}, err
	}

	snap, err := a.fetch(ctx)
	if err != nil {
		return SnapshotDTO{}, err
	}
	a.applySnapshot(snap, true)
	return snapshotDTO(snap), nil
}

func (a *App) Logout() (SnapshotDTO, error) {
	a.svc.Logout()
	snap := krill.EmptySnapshot("已退出登录")
	a.mu.Lock()
	a.cfg.Password = ""
	a.authGen++
	cfg := a.cfg
	err := config.Save(a.paths.Config, cfg)
	a.snap = snap
	glass := a.glass
	tray := a.tray
	a.mu.Unlock()
	a.svc.Configure(cfg)
	if glass != nil {
		glass.Hide()
		glass.SetSnapshot(snap)
	}
	if tray != nil {
		tray.SetSnapshot(snap)
	}
	a.emitSnapshot(snap)
	return snapshotDTO(snap), err
}

func (a *App) Refresh() (SnapshotDTO, error) {
	return a.refresh(true)
}

func (a *App) Settings() (PublicConfigDTO, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	hasSavedLogin := a.svc.HasSavedLoginState()
	return configDTO(cfg, hasSavedLogin), nil
}

func (a *App) SaveSettings(req SettingsRequest) (PublicConfigDTO, error) {
	a.mu.Lock()
	a.cfg.RefreshSec = clampInt(req.RefreshSec, 3, 3600)
	a.cfg.OnTop = req.OnTop
	a.cfg.TbarEnabled = req.GlassEnabled
	a.cfg.RememberLogin = req.RememberLogin
	a.cfg.Password = ""
	cfg := a.cfg
	ctx := a.ctx
	a.mu.Unlock()
	a.svc.Configure(cfg)
	if !cfg.RememberLogin {
		a.svc.ClearSavedLogin()
	}
	err := config.Save(a.paths.Config, cfg)
	if err != nil {
		return PublicConfigDTO{}, err
	}
	if ctx != nil {
		wailsruntime.WindowSetAlwaysOnTop(ctx, cfg.OnTop)
	}
	a.syncGlassCurrent()
	hasSavedLogin := a.svc.HasSavedLoginState()
	return configDTO(cfg, hasSavedLogin), nil
}

func (a *App) SaveWindowPosition() error {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return nil
	}
	return a.saveWindowPosition(ctx)
}

func (a *App) HidePanel() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	ctx := a.ctx
	a.visible = false
	a.mu.Unlock()
	if ctx == nil {
		return nil
	}
	if err := a.saveWindowPosition(ctx); err != nil {
		return err
	}
	wailsruntime.WindowHide(ctx)
	return nil
}

func (a *App) ShowPanel() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	ctx := a.ctx
	a.visible = true
	a.mu.Unlock()
	if ctx == nil {
		return nil
	}
	wailsruntime.WindowShow(ctx)
	hideMainWindowFromTaskbar()
	return nil
}

func (a *App) TogglePanel() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	visible := a.visible
	a.mu.Unlock()
	if visible {
		return a.HidePanel()
	}
	return a.ShowPanel()
}

func (a *App) Quit() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	a.quitting = true
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		_ = a.saveWindowPosition(ctx)
	}
	a.shutdown()
	if ctx != nil {
		wailsruntime.Quit(ctx)
	}
	return nil
}

func (a *App) refresh(reveal bool) (SnapshotDTO, error) {
	a.mu.Lock()
	if a.refreshing {
		snap := a.snap
		a.mu.Unlock()
		return snapshotDTO(snap), nil
	}
	a.refreshing = true
	authGen := a.authGen
	a.mu.Unlock()
	defer a.setRefreshing(false)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := a.fetch(ctx)
	if err != nil && errors.Is(err, krill.ErrAuthRequired) {
		snap = krill.EmptySnapshot("请登录 Krill AI")
	}
	if current, stale := a.snapshotIfAuthChanged(authGen); stale {
		return snapshotDTO(current), nil
	}
	a.applySnapshot(snap, reveal)
	return snapshotDTO(snap), err
}

func (a *App) snapshotIfAuthChanged(gen uint64) (krill.Snapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authGen == gen {
		return krill.Snapshot{}, false
	}
	return a.snap, true
}

func (a *App) fetch(ctx context.Context) (krill.Snapshot, error) {
	snap, err := a.svc.Fetch(ctx)
	if snap.Email == "" {
		a.mu.Lock()
		snap.Email = a.cfg.Email
		a.mu.Unlock()
	}
	return snap, err
}

func (a *App) applySnapshot(snap krill.Snapshot, reveal bool) {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.snap = snap
	ctx := a.ctx
	a.mu.Unlock()
	a.syncGlass(snap)
	a.syncTray(snap)
	a.emitSnapshot(snap)
	if reveal && snap.LoggedIn && ctx != nil {
		wailsruntime.WindowShow(ctx)
		hideMainWindowFromTaskbar()
		a.mu.Lock()
		a.visible = true
		a.mu.Unlock()
	}
}

func (a *App) emitSnapshot(snap krill.Snapshot) {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "snapshot:update", snapshotDTO(snap))
	}
}

func (a *App) scheduleLoop() {
	for {
		a.mu.Lock()
		refreshSec := maxInt(3, a.cfg.RefreshSec)
		a.mu.Unlock()
		select {
		case <-time.After(time.Duration(refreshSec) * time.Second):
			if a.svc.HasLoginState() {
				_, _ = a.refresh(false)
			}
		case <-a.stop:
			return
		}
	}
}

func (a *App) ensureGlassController() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	if a.glass != nil {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	glass, err := ui.StartGlassController(ui.GlassControllerOptions{
		LoadConfig: func() config.Config {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.cfg
		},
		UpdateConfig: func(fn func(*config.Config)) {
			a.mu.Lock()
			fn(&a.cfg)
			a.cfg.Password = ""
			cfg := a.cfg
			_ = config.Save(a.paths.Config, cfg)
			a.mu.Unlock()
		},
		TogglePanel: func() {
			a.enqueueCommand(appCommand{kind: commandTogglePanel})
		},
		Refresh: func(reveal bool) {
			a.enqueueCommand(appCommand{kind: commandRefresh, reveal: reveal})
		},
		Quit: func() {
			a.enqueueCommand(appCommand{kind: commandQuit})
		},
	})
	if err != nil {
		return err
	}

	a.mu.Lock()
	if !a.quitting && a.glass == nil {
		a.glass = glass
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	glass.Close()
	return nil
}

func (a *App) ensureTrayController() error {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return nil
	}
	if a.tray != nil {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	tray, err := ui.StartTrayController(ui.TrayControllerOptions{
		TogglePanel: func() {
			a.enqueueCommand(appCommand{kind: commandTogglePanel})
		},
		Refresh: func(reveal bool) {
			a.enqueueCommand(appCommand{kind: commandRefresh, reveal: reveal})
		},
		Logout: func() {
			a.enqueueCommand(appCommand{kind: commandLogout})
		},
		Quit: func() {
			a.enqueueCommand(appCommand{kind: commandQuit})
		},
	})
	if err != nil {
		return err
	}

	a.mu.Lock()
	if !a.quitting && a.tray == nil {
		a.tray = tray
		snap := a.snap
		a.mu.Unlock()
		tray.SetSnapshot(snap)
		return nil
	}
	a.mu.Unlock()
	tray.Close()
	return nil
}

func (a *App) syncGlassCurrent() {
	a.mu.Lock()
	snap := a.snap
	a.mu.Unlock()
	a.syncGlass(snap)
}

func (a *App) syncTray(snap krill.Snapshot) {
	a.mu.Lock()
	tray := a.tray
	a.mu.Unlock()
	if tray != nil {
		tray.SetSnapshot(snap)
	}
}

func (a *App) syncGlass(snap krill.Snapshot) {
	a.mu.Lock()
	enabled := a.cfg.TbarEnabled
	glass := a.glass
	quitting := a.quitting
	a.mu.Unlock()
	if quitting {
		if glass != nil {
			glass.Hide()
		}
		return
	}
	show := enabled && snap.LoggedIn
	if show && glass == nil {
		_ = a.ensureGlassController()
		a.mu.Lock()
		glass = a.glass
		a.mu.Unlock()
	}
	if glass == nil {
		return
	}
	glass.SetSnapshot(snap)
	if show {
		glass.Show()
	} else {
		glass.Hide()
	}
}

func (a *App) setRefreshing(v bool) {
	a.mu.Lock()
	a.refreshing = v
	a.mu.Unlock()
}

func (a *App) isQuitting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quitting
}

func (a *App) saveWindowPosition(ctx context.Context) error {
	x, y := wailsruntime.WindowGetPosition(ctx)
	a.mu.Lock()
	a.cfg.WX = &x
	a.cfg.WY = &y
	a.cfg.Password = ""
	err := config.Save(a.paths.Config, a.cfg)
	a.mu.Unlock()
	return err
}

func (a *App) positionWindow(ctx context.Context, cfg config.Config) {
	x := int(win.GetSystemMetrics(win.SM_CXSCREEN)) - panelWidth - 24
	y := 70
	if cfg.WX != nil && cfg.WY != nil {
		x = *cfg.WX
		y = *cfg.WY
	}
	screenW := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	screenH := int(win.GetSystemMetrics(win.SM_CYSCREEN))
	x = clampInt(x, 14, maxInt(14, screenW-panelWidth-14))
	y = clampInt(y, 14, maxInt(14, screenH-panelHeight-14))
	wailsruntime.WindowSetPosition(ctx, x, y)
}

func clampInt(v, lo, hi int) int {
	return int(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
