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

	"quotaball/internal/codexfast"
	"quotaball/internal/config"
	"quotaball/internal/krill"
	"quotaball/internal/newapi"
	"quotaball/internal/paths"
	"quotaball/internal/secret"
	"quotaball/internal/sub2"
	"quotaball/internal/ui"
)

const (
	panelWidth        = 540
	panelHeight       = 820
	loginWindowWidth  = 446
	loginWindowHeight = 486
	loginGlassSize    = 190
	appWindowTitle    = "QuotaBall"
	loginTransition   = 3 * time.Second
)

//go:embed all:frontend/src
var assets embed.FS

var (
	applyCodexFastProxy  = codexfast.Apply
	detectCodexFastProxy = codexfast.DetectEnabled
)

type App struct {
	ctx context.Context

	paths  paths.Paths
	cfg    config.Config
	svc    *krill.Service
	newSvc *newapi.Service
	subSvc *sub2.Service

	mu          sync.Mutex
	snap        krill.Snapshot
	operation   appOperation
	operationID uint64
	visible     bool
	quitting    bool
	authGen     uint64

	stop     chan struct{}
	commands chan appCommand
	glass    *ui.GlassController
	tray     *ui.TrayController
	oauth    *oauthCapture
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

type appOperation int

const (
	operationIdle appOperation = iota
	operationRefreshing
	operationLoggingIn
	operationOAuthStarting
	operationOAuthCompleting
)

type appOperationToken struct {
	id appOperationID
	op appOperation
}

type appOperationID uint64

func Run() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	windowWidth, windowHeight := app.initialWindowSize()
	frontend, err := fs.Sub(assets, "frontend/src")
	if err != nil {
		return err
	}
	return wails.Run(&options.App{
		Title:             appWindowTitle,
		Width:             windowWidth,
		Height:            windowHeight,
		MinWidth:          loginWindowWidth,
		MinHeight:         loginWindowHeight,
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
	cfg = migratePlaintextPassword(cfg, p.Config, st, config.Save)
	if enabled, err := detectCodexFastProxy(); err == nil {
		cfg.CodexFastProxyEnabled = enabled
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
	app.newSvc = &newapi.Service{
		Config:  &app.cfg,
		Secrets: st,
	}
	app.newSvc.Configure(app.cfg)
	app.subSvc = &sub2.Service{
		Config:  &app.cfg,
		Secrets: st,
	}
	app.subSvc.Configure(app.cfg)
	app.snap = krill.EmptySnapshot("正在登录...")
	app.snap.Provider = normalizeProvider(cfg.Provider)
	if app.hasLoginState() {
		app.snap.Loading = true
	} else {
		app.snap = krill.EmptySnapshot(app.loginRequiredMessage())
		app.snap.Provider = normalizeProvider(cfg.Provider)
	}
	return app, nil
}

type secretSetter interface {
	Set(key, value string) error
}

type configSaver func(string, config.Config) error

func migratePlaintextPassword(cfg config.Config, configPath string, secrets secretSetter, save configSaver) config.Config {
	if cfg.Password == "" || secrets == nil || save == nil {
		return cfg
	}
	if err := secrets.Set("password", cfg.Password); err != nil {
		return cfg
	}
	cfg.Password = ""
	_ = save(configPath, cfg)
	return cfg
}

func (a *App) startup(ctx context.Context) {
	hasLogin := a.hasLoginState()
	a.mu.Lock()
	a.ctx = ctx
	a.visible = true
	cfg := a.cfg
	snap := a.snap
	a.mu.Unlock()

	windowWidth, windowHeight := windowSizeForSnapshot(snap)
	a.positionWindow(ctx, cfg, windowWidth, windowHeight)
	a.syncWindowSize(windowWidth, windowHeight)
	wailsruntime.WindowSetAlwaysOnTop(ctx, cfg.OnTop)
	hideMainWindowFromTaskbar()
	if snap.Loading {
		a.mu.Lock()
		cfg := a.positionGlassAtLoginAnimationLocked(ctx)
		a.visible = false
		a.mu.Unlock()
		_ = config.Save(a.paths.Config, cfg)
		wailsruntime.WindowHide(ctx)
	}
	go a.commandLoop()
	_ = a.ensureTrayController()
	a.syncGlass(snap)
	go a.scheduleLoop()
	if hasLogin {
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
	oauth := a.oauth
	a.oauth = nil
	a.mu.Unlock()
	if glass != nil {
		glass.Close()
	}
	if tray != nil {
		tray.Close()
	}
	if oauth != nil {
		oauth.Close()
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
	hasSavedLogin := a.hasSavedLoginState()
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
	provider := normalizeProvider(req.Provider)
	if provider != "" && provider != config.ProviderKrill && provider != config.ProviderSub2 {
		return SnapshotDTO{}, errors.New("该登录方式不支持邮箱密码")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || req.Password == "" {
		return SnapshotDTO{}, errors.New("请输入邮箱和密码")
	}

	op, authGen, err := a.beginAuthOperation(operationLoggingIn)
	if err != nil {
		return SnapshotDTO{}, err
	}
	defer a.finishOperation(op)
	transitionStarted := a.startLoginGlassAnimation(provider)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if provider == config.ProviderSub2 {
		baseURL, err := sub2.NormalizeBaseURL(req.BaseURL)
		if err != nil {
			a.failLoginGlassAnimation(provider, err.Error())
			return SnapshotDTO{}, err
		}
		snap, err := a.subSvc.Login(ctx, baseURL, email, req.Password, req.RememberLogin)
		if err != nil {
			a.failLoginGlassAnimation(provider, err.Error())
			return SnapshotDTO{}, err
		}
		if current, stale := a.snapshotIfAuthChanged(authGen); stale {
			return snapshotDTO(current), krill.ErrAuthRequired
		}

		a.mu.Lock()
		a.cfg.Provider = config.ProviderSub2
		a.cfg.Sub2BaseURL = baseURL
		a.cfg.Sub2Email = email
		a.cfg.RememberLogin = req.RememberLogin
		a.cfg.Password = ""
		a.authGen++
		cfg := a.cfg
		a.mu.Unlock()
		a.subSvc.Configure(cfg)
		if err := config.Save(a.paths.Config, cfg); err != nil {
			a.failLoginGlassAnimation(provider, err.Error())
			return SnapshotDTO{}, err
		}
		if snap.Provider == "" {
			snap.Provider = config.ProviderSub2
		}
		waitForLoginTransition(transitionStarted)
		a.applySnapshot(snap, false)
		return snapshotDTO(snap), nil
	}
	if err := a.svc.Login(ctx, email, req.Password, req.RememberLogin); err != nil {
		a.failLoginGlassAnimation(provider, err.Error())
		return SnapshotDTO{}, err
	}
	if current, stale := a.snapshotIfAuthChanged(authGen); stale {
		return snapshotDTO(current), krill.ErrAuthRequired
	}

	a.mu.Lock()
	a.cfg.Provider = config.ProviderKrill
	a.cfg.Email = email
	a.cfg.RememberLogin = req.RememberLogin
	a.cfg.Password = ""
	a.authGen++
	cfg := a.cfg
	a.mu.Unlock()
	a.svc.Configure(cfg)
	err = config.Save(a.paths.Config, cfg)
	if err != nil {
		a.failLoginGlassAnimation(provider, err.Error())
		return SnapshotDTO{}, err
	}

	snap, err := a.fetch(ctx)
	if err != nil {
		a.failLoginGlassAnimation(provider, err.Error())
		return SnapshotDTO{}, err
	}
	waitForLoginTransition(transitionStarted)
	a.applySnapshot(snap, false)
	return snapshotDTO(snap), nil
}

func (a *App) StartNewAPIOAuth(req NewAPIOAuthStartRequest) (dto NewAPIOAuthStartDTO, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			oauthLogf("StartNewAPIOAuth panic: %v", recovered)
			dto = NewAPIOAuthStartDTO{}
			err = errors.New("NewAPI 登录启动异常，请重试")
		}
	}()
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return NewAPIOAuthStartDTO{}, errors.New("请输入 NewAPI 网站地址")
	}

	op, _, err := a.beginAuthOperation(operationOAuthStarting)
	if err != nil {
		return NewAPIOAuthStartDTO{}, err
	}
	defer a.finishOperation(op)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var start newapi.OAuthStart
	if req.AutoCallback {
		start, err = a.newSvc.StartLinuxDoBrowser(ctx, baseURL, req.RememberLogin)
	} else {
		start, err = a.newSvc.StartLinuxDo(ctx, baseURL, req.RememberLogin)
	}
	if err != nil {
		return NewAPIOAuthStartDTO{}, err
	}

	a.mu.Lock()
	wailsCtx := a.ctx
	a.mu.Unlock()
	var capture *oauthCapture
	if req.AutoCallback {
		capture, err = startOAuthBrowserCapture(context.Background(), start.AuthorizeURL, start.BaseURL, start.LinuxDoClientID)
		if err != nil {
			return NewAPIOAuthStartDTO{}, err
		}
	}
	if capture != nil {
		a.replaceOAuthCapture(capture)
		go a.waitNewAPIOAuthCallback(start.BaseURL, req.RememberLogin, capture)
	} else if wailsCtx != nil {
		wailsruntime.BrowserOpenURL(wailsCtx, start.AuthorizeURL)
	}
	return NewAPIOAuthStartDTO{
		BaseURL:      start.BaseURL,
		AuthorizeURL: start.AuthorizeURL,
		AutoCapture:  capture != nil,
	}, nil
}

func (a *App) CompleteNewAPIOAuth(req NewAPIOAuthCompleteRequest) (dto SnapshotDTO, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			oauthLogf("CompleteNewAPIOAuth panic: %v", recovered)
			dto = SnapshotDTO{}
			err = errors.New("NewAPI 登录完成异常，请重试")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.completeNewAPIOAuth(ctx, req)
}

func (a *App) completeNewAPIOAuth(ctx context.Context, req NewAPIOAuthCompleteRequest) (SnapshotDTO, error) {
	baseURL, err := newapi.NormalizeBaseURL(req.BaseURL)
	if err != nil {
		return SnapshotDTO{}, err
	}
	callbackURL := strings.TrimSpace(req.CallbackURL)
	sessionCookies := strings.TrimSpace(req.SessionCookies)
	accessToken := strings.TrimSpace(req.AccessToken)
	userID := strings.TrimSpace(req.UserID)
	if callbackURL == "" && sessionCookies == "" && accessToken == "" {
		return SnapshotDTO{}, errors.New("请粘贴登录完成后的回调 URL")
	}

	op, authGen, err := a.beginAuthOperation(operationOAuthCompleting)
	if err != nil {
		return SnapshotDTO{}, err
	}
	defer a.finishOperation(op)
	transitionStarted := a.startLoginGlassAnimation(config.ProviderNewAPI)

	var snap krill.Snapshot
	if accessToken != "" {
		snap, err = a.newSvc.CompleteBrowserToken(ctx, baseURL, accessToken, userID, req.RememberLogin)
	} else if callbackURL != "" && sessionCookies != "" {
		snap, err = a.newSvc.CompleteLinuxDoWithCookies(ctx, baseURL, callbackURL, sessionCookies, req.RememberLogin)
		if err != nil {
			oauthLogf("browser callback completion failed, falling back to session cookies: %v", err)
			snap, err = a.newSvc.CompleteBrowserSession(ctx, baseURL, sessionCookies, userID, req.RememberLogin)
		}
	} else if sessionCookies != "" {
		snap, err = a.newSvc.CompleteBrowserSession(ctx, baseURL, sessionCookies, userID, req.RememberLogin)
	} else {
		snap, err = a.newSvc.CompleteLinuxDo(ctx, baseURL, callbackURL, req.RememberLogin)
	}
	if err != nil {
		a.failLoginGlassAnimation(config.ProviderNewAPI, err.Error())
		return SnapshotDTO{}, err
	}
	snap.Provider = config.ProviderNewAPI
	if current, stale := a.snapshotIfAuthChanged(authGen); stale {
		return snapshotDTO(current), krill.ErrAuthRequired
	}

	a.mu.Lock()
	a.cfg.Provider = config.ProviderNewAPI
	a.cfg.NewAPIBaseURL = baseURL
	a.cfg.RememberLogin = req.RememberLogin
	a.cfg.Password = ""
	a.authGen++
	cfg := a.cfg
	oauth := a.oauth
	a.oauth = nil
	a.mu.Unlock()
	if oauth != nil {
		oauth.Close()
	}
	a.newSvc.Configure(cfg)
	if err := config.Save(a.paths.Config, cfg); err != nil {
		a.failLoginGlassAnimation(config.ProviderNewAPI, err.Error())
		return SnapshotDTO{}, err
	}
	waitForLoginTransition(transitionStarted)
	a.applySnapshot(snap, false)
	return snapshotDTO(snap), nil
}

func (a *App) completeCapturedNewAPIOAuth(req NewAPIOAuthCompleteRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := a.completeNewAPIOAuth(ctx, req)
	if err != nil {
		oauthLogf("oauth backend completion failed: %v", err)
		a.emitOAuthError(err.Error())
		return
	}
	oauthLogf("oauth backend completion succeeded loggedIn=%t", snap.LoggedIn)
}

func (a *App) Logout() (SnapshotDTO, error) {
	a.stopOAuthCapture()
	provider := a.activeProvider()
	switch provider {
	case config.ProviderNewAPI:
		a.newSvc.Logout()
	case config.ProviderSub2:
		if a.subSvc != nil {
			a.subSvc.Logout()
		}
	default:
		a.svc.Logout()
	}
	snap := krill.EmptySnapshot("已退出登录")
	snap.Provider = provider
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
	a.newSvc.Configure(cfg)
	if a.subSvc != nil {
		a.subSvc.Configure(cfg)
	}
	if glass != nil {
		glass.Hide()
		glass.SetSnapshot(snap)
	}
	if tray != nil {
		tray.SetSnapshot(snap)
	}
	a.syncWindowForSnapshot(snap)
	a.emitSnapshot(snap)
	return snapshotDTO(snap), err
}

func (a *App) waitNewAPIOAuthCallback(baseURL string, remember bool, capture *oauthCapture) {
	defer a.recoverOAuthCallbackPanic()
	callback, ok := nextOAuthCallback(capture.Callbacks, capture.Done, a.stop)
	if !ok {
		a.emitOAuthError("NewAPI 自动登录窗口已关闭或连接中断，请重新打开 LinuxDo 登录页")
		return
	}
	if !oauthCallbackHasCredential(callback) {
		message := strings.TrimSpace(callback.Error)
		if message == "" {
			message = "NewAPI 自动登录窗口已关闭或连接中断，请重新打开 LinuxDo 登录页"
		}
		a.emitOAuthError(message)
		return
	}
	req := NewAPIOAuthCompleteRequest{
		BaseURL:        baseURL,
		CallbackURL:    callback.CallbackURL,
		SessionCookies: callback.SessionCookies,
		AccessToken:    callback.AccessToken,
		UserID:         callback.UserID,
		RememberLogin:  remember,
	}
	oauthLogf(
		"oauth callback captured callback=%t session=%t token=%t user=%t",
		strings.TrimSpace(req.CallbackURL) != "",
		strings.TrimSpace(req.SessionCookies) != "",
		strings.TrimSpace(req.AccessToken) != "",
		strings.TrimSpace(req.UserID) != "",
	)
	a.completeCapturedNewAPIOAuth(req)
}

func (a *App) recoverOAuthCallbackPanic() {
	if recovered := recover(); recovered != nil {
		a.emitOAuthError("NewAPI 自动登录异常，请重试")
	}
}

func (a *App) emitOAuthCallbackToFrontend(req NewAPIOAuthCompleteRequest) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "oauth:callback", req)
	}
}

func nextOAuthCallback(callbacks <-chan oauthCallbackResult, done <-chan struct{}, appStop <-chan struct{}) (oauthCallbackResult, bool) {
	select {
	case callbackURL, ok := <-callbacks:
		return callbackURL, ok
	case <-done:
		select {
		case callbackURL, ok := <-callbacks:
			return callbackURL, ok
		default:
			return oauthCallbackResult{}, false
		}
	case <-appStop:
		return oauthCallbackResult{}, false
	}
}

func (a *App) replaceOAuthCapture(capture *oauthCapture) {
	a.mu.Lock()
	old := a.oauth
	a.oauth = capture
	a.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (a *App) stopOAuthCapture() {
	a.mu.Lock()
	oauth := a.oauth
	a.oauth = nil
	a.mu.Unlock()
	if oauth != nil {
		oauth.Close()
	}
}

func (a *App) emitOAuthError(message string) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "oauth:error", message)
	}
}

func (a *App) Refresh() (SnapshotDTO, error) {
	return a.refresh(true)
}

func (a *App) Settings() (PublicConfigDTO, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	hasSavedLogin := a.hasSavedLoginState()
	return configDTO(cfg, hasSavedLogin), nil
}

func (a *App) SaveSettings(req SettingsRequest) (PublicConfigDTO, error) {
	a.mu.Lock()
	oldCfg := a.cfg
	cfg := a.cfg
	oldProvider := a.cfg.Provider
	oldNewAPIBaseURL := a.cfg.NewAPIBaseURL
	oldSub2BaseURL := a.cfg.Sub2BaseURL
	oldRememberLogin := a.cfg.RememberLogin
	oldCodexFastProxyEnabled := a.cfg.CodexFastProxyEnabled
	cfg.RefreshSec = clampInt(req.RefreshSec, 3, 3600)
	cfg.OnTop = req.OnTop
	cfg.RememberLogin = req.RememberLogin
	cfg.CodexFastProxyEnabled = req.CodexFastProxyEnabled
	if provider := normalizeProvider(req.Provider); provider == config.ProviderKrill || provider == config.ProviderNewAPI || provider == config.ProviderSub2 {
		cfg.Provider = provider
	}
	if cfg.Provider != config.ProviderNewAPI {
		cfg.TbarEnabled = req.GlassEnabled
	}
	if strings.TrimSpace(req.NewAPIBaseURL) != "" {
		cfg.NewAPIBaseURL = strings.TrimRight(strings.TrimSpace(req.NewAPIBaseURL), "/")
	}
	if strings.TrimSpace(req.Sub2BaseURL) != "" {
		cfg.Sub2BaseURL = strings.TrimRight(strings.TrimSpace(req.Sub2BaseURL), "/")
	}
	if cfg.Provider != oldProvider || cfg.NewAPIBaseURL != oldNewAPIBaseURL || cfg.Sub2BaseURL != oldSub2BaseURL || cfg.RememberLogin != oldRememberLogin {
		a.authGen++
	}
	ctx := a.ctx
	a.mu.Unlock()

	cfg.Password = ""
	err := config.Save(a.paths.Config, cfg)
	if err != nil {
		return PublicConfigDTO{}, err
	}
	if cfg.CodexFastProxyEnabled != oldCodexFastProxyEnabled {
		proxyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := applyCodexFastProxy(proxyCtx, cfg.CodexFastProxyEnabled); err != nil {
			_ = config.Save(a.paths.Config, oldCfg)
			return PublicConfigDTO{}, err
		}
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	a.svc.Configure(cfg)
	a.newSvc.Configure(cfg)
	if a.subSvc != nil {
		a.subSvc.Configure(cfg)
	}
	if !cfg.RememberLogin {
		var subErr error
		if a.subSvc != nil {
			subErr = a.subSvc.ClearSavedLogin()
		}
		if err := errors.Join(a.svc.ClearSavedLogin(), a.newSvc.ClearSavedLogin(), subErr); err != nil {
			return PublicConfigDTO{}, err
		}
	}
	if ctx != nil {
		wailsruntime.WindowSetAlwaysOnTop(ctx, cfg.OnTop)
	}
	a.syncGlassCurrent()
	hasSavedLogin := a.hasSavedLoginState()
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
	if !a.hasLoginState() {
		snap := krill.EmptySnapshot(a.loginRequiredMessage())
		a.mu.Lock()
		a.snap = snap
		a.mu.Unlock()
		a.syncWindowForSnapshot(snap)
		a.syncGlass(snap)
		a.syncTray(snap)
		a.emitSnapshot(snap)
	}

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
	transitionStarted := time.Now()
	op, previousSnap, authGen, ok := a.beginRefreshOperation()
	if !ok {
		return snapshotDTO(previousSnap), nil
	}
	defer a.finishOperation(op)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := a.fetch(ctx)
	if err != nil && (errors.Is(err, krill.ErrAuthRequired) || errors.Is(err, newapi.ErrAuthRequired) || errors.Is(err, sub2.ErrAuthRequired)) {
		snap = krill.EmptySnapshot(a.loginRequiredMessage())
		snap.Provider = a.activeProvider()
	}
	if current, stale := a.snapshotIfAuthChanged(authGen); stale {
		return snapshotDTO(current), nil
	}
	if previousSnap.Loading && !previousSnap.LoggedIn {
		waitForLoginTransition(transitionStarted)
	}
	a.applySnapshot(snap, reveal)
	return snapshotDTO(snap), err
}

func waitForLoginTransition(started time.Time) {
	if started.IsZero() {
		return
	}
	if remaining := loginTransition - time.Since(started); remaining > 0 {
		time.Sleep(remaining)
	}
}

func (a *App) startLoginGlassAnimation(provider string) time.Time {
	started := time.Now()
	snap := krill.EmptySnapshot("正在登录...")
	snap.Provider = normalizeProvider(provider)
	snap.Loading = true
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return started
	}
	a.snap = snap
	ctx := a.ctx
	var cfgToSave *config.Config
	if ctx != nil {
		cfg := a.positionGlassAtLoginAnimationLocked(ctx)
		cfgToSave = &cfg
		a.visible = false
	}
	a.mu.Unlock()
	if cfgToSave != nil {
		_ = config.Save(a.paths.Config, *cfgToSave)
	}
	if ctx != nil {
		wailsruntime.WindowHide(ctx)
	}
	a.syncGlass(snap)
	a.syncTray(snap)
	a.emitSnapshot(snap)
	return started
}

func (a *App) failLoginGlassAnimation(provider, message string) {
	snap := krill.EmptySnapshot(message)
	snap.Provider = normalizeProvider(provider)
	a.applySnapshot(snap, false)
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
	var snap krill.Snapshot
	var err error
	provider := a.activeProvider()
	switch provider {
	case config.ProviderNewAPI:
		snap, err = a.newSvc.Fetch(ctx)
	case config.ProviderSub2:
		if a.subSvc == nil {
			return krill.EmptySnapshot("Sub2 服务未初始化"), errors.New("Sub2 服务未初始化")
		}
		snap, err = a.subSvc.Fetch(ctx)
	default:
		snap, err = a.svc.Fetch(ctx)
	}
	if snap.Provider == "" {
		snap.Provider = provider
	}
	if snap.Email == "" {
		a.mu.Lock()
		if provider == config.ProviderSub2 {
			snap.Email = a.cfg.Sub2Email
		} else {
			snap.Email = a.cfg.Email
		}
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
	previous := a.snap
	a.snap = snap
	ctx := a.ctx
	loginSuccessWithoutReveal := !reveal && snap.LoggedIn && !previous.LoggedIn
	hideOnLoginSuccess := loginSuccessWithoutReveal && a.shouldShowGlassLocked(snap)
	showPanelOnLoginSuccess := loginSuccessWithoutReveal && !hideOnLoginSuccess
	showLoginAfterLoadingFailure := !reveal && previous.Loading && !snap.LoggedIn
	var cfgToSave *config.Config
	if hideOnLoginSuccess && ctx != nil {
		cfg := a.positionGlassAtLoginAnimationLocked(ctx)
		cfgToSave = &cfg
	}
	if hideOnLoginSuccess {
		a.visible = false
	}
	a.mu.Unlock()
	if cfgToSave != nil {
		_ = config.Save(a.paths.Config, *cfgToSave)
	}
	if hideOnLoginSuccess && ctx != nil {
		wailsruntime.WindowHide(ctx)
	}
	a.syncWindowForSnapshot(snap)
	a.syncGlass(snap)
	a.syncTray(snap)
	a.emitSnapshot(snap)
	if ((reveal || showPanelOnLoginSuccess) && snap.LoggedIn || showLoginAfterLoadingFailure) && ctx != nil {
		wailsruntime.WindowShow(ctx)
		hideMainWindowFromTaskbar()
		a.mu.Lock()
		a.visible = true
		a.mu.Unlock()
	}
}

func (a *App) shouldShowGlassLocked(snap krill.Snapshot) bool {
	return snap.LoggedIn && (snap.Provider == config.ProviderNewAPI || a.cfg.TbarEnabled)
}

func (a *App) positionGlassAtLoginAnimationLocked(ctx context.Context) config.Config {
	x, y := wailsruntime.WindowGetPosition(ctx)
	glassX := x + (loginWindowWidth-loginGlassSize)/2
	glassY := y + (loginWindowHeight-loginGlassSize)/2
	a.cfg.TbarX = &glassX
	a.cfg.TbarY = &glassY
	a.cfg.Password = ""
	cfg := a.cfg
	return cfg
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
			if a.hasLoginState() {
				_, _ = a.refresh(false)
			}
		case <-a.stop:
			return
		}
	}
}

func (a *App) initialWindowSize() (int, int) {
	a.mu.Lock()
	snap := a.snap
	a.mu.Unlock()
	return windowSizeForSnapshot(snap)
}

func windowSizeForSnapshot(snap krill.Snapshot) (int, int) {
	return windowSizeForLoginState(snap.LoggedIn)
}

func windowSizeForLoginState(loggedIn bool) (int, int) {
	if loggedIn {
		return panelWidth, panelHeight
	}
	return loginWindowWidth, loginWindowHeight
}

func (a *App) syncWindowForSnapshot(snap krill.Snapshot) {
	a.syncWindowSize(windowSizeForSnapshot(snap))
}

func (a *App) syncWindowSize(width, height int) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	x, y := wailsruntime.WindowGetPosition(ctx)
	wailsruntime.WindowSetSize(ctx, width, height)
	screenW := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	screenH := int(win.GetSystemMetrics(win.SM_CYSCREEN))
	nextX := clampInt(x, 14, maxInt(14, screenW-width-14))
	nextY := clampInt(y, 14, maxInt(14, screenH-height-14))
	if nextX != x || nextY != y {
		wailsruntime.WindowSetPosition(ctx, nextX, nextY)
	}
}

func (a *App) activeProvider() string {
	a.mu.Lock()
	provider := a.cfg.Provider
	a.mu.Unlock()
	return normalizeProvider(provider)
}

func (a *App) hasLoginState() bool {
	switch a.activeProvider() {
	case config.ProviderNewAPI:
		return a.newSvc.HasLoginState()
	case config.ProviderSub2:
		return a.subSvc != nil && a.subSvc.HasLoginState()
	}
	return a.svc.HasLoginState()
}

func (a *App) hasSavedLoginState() bool {
	switch a.activeProvider() {
	case config.ProviderNewAPI:
		return a.newSvc.HasSavedLoginState()
	case config.ProviderSub2:
		return a.subSvc != nil && a.subSvc.HasSavedLoginState()
	}
	return a.svc.HasSavedLoginState()
}

func (a *App) loginRequiredMessage() string {
	switch a.activeProvider() {
	case config.ProviderNewAPI:
		return "请登录 NewAPI"
	case config.ProviderSub2:
		return "请登录 Sub2"
	}
	return "请登录 Krill AI"
}

func normalizeProvider(provider string) string {
	switch provider {
	case "", config.ProviderKrill:
		return config.ProviderKrill
	case config.ProviderNewAPI:
		return config.ProviderNewAPI
	case config.ProviderSub2:
		return config.ProviderSub2
	default:
		return config.ProviderKrill
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
	show := snap.Loading || (snap.LoggedIn && (snap.Provider == config.ProviderNewAPI || enabled))
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

func (a *App) beginRefreshOperation() (appOperationToken, krill.Snapshot, uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.operation != operationIdle {
		return appOperationToken{}, a.snap, a.authGen, false
	}
	a.operationID++
	a.operation = operationRefreshing
	token := appOperationToken{id: appOperationID(a.operationID), op: operationRefreshing}
	return token, a.snap, a.authGen, true
}

func (a *App) beginAuthOperation(op appOperation) (appOperationToken, uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.operation != operationIdle && a.operation != operationRefreshing {
		return appOperationToken{}, 0, errors.New(a.operation.busyMessage())
	}
	a.operationID++
	a.operation = op
	a.authGen++
	token := appOperationToken{id: appOperationID(a.operationID), op: op}
	return token, a.authGen, nil
}

func (a *App) finishOperation(token appOperationToken) {
	a.mu.Lock()
	if appOperationID(a.operationID) == token.id && a.operation == token.op {
		a.operation = operationIdle
	}
	a.mu.Unlock()
}

func (op appOperation) busyMessage() string {
	switch op {
	case operationLoggingIn, operationOAuthStarting, operationOAuthCompleting:
		return "正在登录，请稍后"
	case operationRefreshing:
		return "正在刷新，请稍后"
	default:
		return "操作进行中，请稍后"
	}
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

func (a *App) positionWindow(ctx context.Context, cfg config.Config, width int, height int) {
	x := int(win.GetSystemMetrics(win.SM_CXSCREEN)) - width - 24
	y := 70
	if cfg.WX != nil && cfg.WY != nil {
		x = *cfg.WX
		y = *cfg.WY
	}
	screenW := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	screenH := int(win.GetSystemMetrics(win.SM_CYSCREEN))
	x = clampInt(x, 14, maxInt(14, screenW-width-14))
	y = clampInt(y, 14, maxInt(14, screenH-height-14))
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
