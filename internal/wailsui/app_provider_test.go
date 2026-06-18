package wailsui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"quotaball/internal/config"
	"quotaball/internal/krill"
	"quotaball/internal/newapi"
	"quotaball/internal/paths"
	"quotaball/internal/secret"
	"quotaball/internal/sub2"
)

func TestStartNewAPIOAuthDoesNotPersistProviderBeforeCompletion(t *testing.T) {
	restore := stubOAuthCapture(t, nil, nil)
	defer restore()
	_, stateValue := testOAuthCodeState(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeWailsAPITestResponse(t, w, map[string]any{
				"system_name":       "Test NewAPI",
				"linuxdo_oauth":     true,
				"linuxdo_client_id": "linuxdo-client-id",
			})
		case "/api/oauth/state":
			writeWailsAPITestResponse(t, w, stateValue)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Provider = config.ProviderKrill
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		paths: paths.Paths{Config: configPath},
		cfg:   cfg,
		svc:   &krill.Service{},
	}
	app.newSvc = &newapi.Service{Config: &app.cfg}
	app.newSvc.Configure(app.cfg)

	if _, err := app.StartNewAPIOAuth(NewAPIOAuthStartRequest{BaseURL: server.URL, RememberLogin: true}); err != nil {
		t.Fatal(err)
	}

	if app.cfg.Provider != config.ProviderKrill {
		t.Fatalf("StartNewAPIOAuth must not switch active provider before completion, got %q", app.cfg.Provider)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Provider != config.ProviderKrill {
		t.Fatalf("StartNewAPIOAuth must not persist NewAPI before completion, saved provider=%q", saved.Provider)
	}
}

func TestStartNewAPIOAuthStartsAutomaticCallbackWithBrowserSessionStrategy(t *testing.T) {
	callbacks := make(chan oauthCallbackResult)
	var started bool
	var gotAuthorizeURL string
	var gotBaseURL string
	var gotClientID string
	var sawState bool
	var sawLogout bool
	_, stateValue := testOAuthCodeState(t)
	restore := stubOAuthCapture(t, callbacks, func(authorizeURL, baseURL string) {
		started = true
		gotAuthorizeURL = authorizeURL
		gotBaseURL = baseURL
	}, func(clientID string) {
		gotClientID = clientID
	})
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeWailsAPITestResponse(t, w, map[string]any{
				"system_name":       "Test NewAPI",
				"linuxdo_oauth":     true,
				"linuxdo_client_id": "linuxdo-client-id",
			})
		case "/api/user/logout":
			sawLogout = true
			writeWailsAPITestResponse(t, w, nil)
		case "/api/oauth/state":
			sawState = true
			writeWailsAPITestResponse(t, w, stateValue)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	app := &App{
		cfg: cfg,
		svc: &krill.Service{},
	}
	app.newSvc = &newapi.Service{Config: &app.cfg}
	app.newSvc.Configure(app.cfg)

	start, err := app.StartNewAPIOAuth(NewAPIOAuthStartRequest{BaseURL: server.URL, RememberLogin: true, AutoCallback: true})
	if err != nil {
		t.Fatal(err)
	}

	if !started {
		t.Fatalf("StartNewAPIOAuth should start the local callback listener when requested")
	}
	if !start.AutoCapture {
		t.Fatalf("StartNewAPIOAuth should report automatic capture is active")
	}
	if sawState || sawLogout {
		t.Fatalf("automatic NewAPI login should let the browser own OAuth state, sawState=%v sawLogout=%v", sawState, sawLogout)
	}
	if start.AuthorizeURL != "" {
		t.Fatalf("browser-session automatic capture should not expose an app-owned authorize URL: %q", start.AuthorizeURL)
	}
	if gotAuthorizeURL != start.AuthorizeURL {
		t.Fatalf("capture authorize URL = %q, want %q", gotAuthorizeURL, start.AuthorizeURL)
	}
	if gotBaseURL != start.BaseURL {
		t.Fatalf("capture base URL = %q, want %q", gotBaseURL, start.BaseURL)
	}
	if gotClientID != "linuxdo-client-id" {
		t.Fatalf("capture client ID = %q, want browser-session client id", gotClientID)
	}
	_ = stateValue
}

func TestSaveSettingsActivatesSub2Provider(t *testing.T) {
	cfg := config.Default()
	configPath := filepath.Join(t.TempDir(), "config.json")
	app := &App{
		paths:  paths.Paths{Config: configPath},
		cfg:    cfg,
		svc:    &krill.Service{},
		newSvc: &newapi.Service{},
		subSvc: &sub2.Service{},
	}

	got, err := app.SaveSettings(SettingsRequest{
		RefreshSec:    cfg.RefreshSec,
		OnTop:         cfg.OnTop,
		GlassEnabled:  cfg.TbarEnabled,
		RememberLogin: cfg.RememberLogin,
		Provider:      config.ProviderSub2,
		Sub2BaseURL:   "https://sub2.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != config.ProviderSub2 || app.cfg.Provider != config.ProviderSub2 {
		t.Fatalf("Sub2 provider must become active, dto=%q cfg=%q", got.Provider, app.cfg.Provider)
	}
	if got.Sub2BaseURL != "https://sub2.example.test" || app.cfg.Sub2BaseURL != "https://sub2.example.test" {
		t.Fatalf("Sub2 base URL not saved, dto=%q cfg=%q", got.Sub2BaseURL, app.cfg.Sub2BaseURL)
	}
}

func TestLoginSupportsSub2EmailPasswordProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeSub2WailsAPITestResponse(t, w, map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
				"token_type":    "Bearer",
				"user": map[string]any{
					"id":      7,
					"email":   "sub2@example.test",
					"balance": 8.5,
				},
			})
		case "/api/v1/auth/me":
			if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
				t.Fatalf("Authorization = %q, want bearer token", got)
			}
			writeSub2WailsAPITestResponse(t, w, map[string]any{
				"id":      7,
				"email":   "sub2@example.test",
				"balance": 8.5,
			})
		case "/api/v1/subscriptions/summary":
			writeSub2WailsAPITestResponse(t, w, map[string]any{
				"active_count":   1,
				"total_used_usd": 1500,
				"subscriptions": []any{
					map[string]any{
						"id":                11,
						"group_name":        "Pro",
						"daily_used_usd":    25,
						"daily_limit_usd":   100,
						"weekly_used_usd":   140,
						"weekly_limit_usd":  700,
						"monthly_used_usd":  1500,
						"monthly_limit_usd": 3000,
					},
				},
			})
		case "/api/v1/subscriptions/progress":
			writeSub2WailsAPITestResponse(t, w, []any{
				map[string]any{
					"subscription": map[string]any{
						"id":         11,
						"starts_at":  "2026-06-18T12:00:00Z",
						"expires_at": "2026-07-18T12:00:00Z",
						"group": map[string]any{
							"name":     "Pro",
							"platform": "openai",
						},
					},
					"progress": map[string]any{
						"subscription_id": 11,
						"daily": map[string]any{
							"used_usd":      "25",
							"limit_usd":     "100",
							"remaining_usd": "75",
							"percentage":    25,
						},
						"weekly": map[string]any{
							"used_usd":      "140",
							"limit_usd":     "700",
							"remaining_usd": "560",
							"percentage":    20,
						},
						"monthly": map[string]any{
							"used_usd":      "1500",
							"limit_usd":     "3000",
							"remaining_usd": "1500",
							"percentage":    50,
						},
						"expires_at":     "2026-07-18T12:00:00Z",
						"days_remaining": 30,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := config.Default()
	configPath := filepath.Join(dir, "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	secretStore := secret.NewStore(filepath.Join(dir, "secrets.json"))
	app := &App{
		paths: paths.Paths{Config: configPath},
		cfg:   cfg,
		svc:   &krill.Service{},
	}
	app.newSvc = &newapi.Service{Config: &app.cfg, Secrets: secretStore}
	app.subSvc = &sub2.Service{Config: &app.cfg, Secrets: secretStore}
	app.subSvc.Configure(app.cfg)

	snap, err := app.Login(LoginRequest{
		Provider:      config.ProviderSub2,
		BaseURL:       server.URL + "/api/v1",
		Email:         "sub2@example.test",
		Password:      "secret",
		RememberLogin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Provider != config.ProviderSub2 || snap.Wallet != 8.5 {
		t.Fatalf("Sub2 login snapshot = %#v", snap)
	}
	if snap.Summary.TotalDailyQuotaUSD != 100 || snap.Summary.TotalWeeklyQuotaUSD != 700 || snap.Summary.TotalMonthlyQuotaUSD != 3000 {
		t.Fatalf("Sub2 quota summary = %#v", snap.Summary)
	}
	if len(snap.Subscriptions) != 1 || snap.Subscriptions[0].DailyRemaining != 75 || snap.Subscriptions[0].WeeklyRemaining != 560 || snap.Subscriptions[0].MonthlyRemaining != 1500 {
		t.Fatalf("Sub2 subscription progress = %#v", snap.Subscriptions)
	}
	if app.cfg.Provider != config.ProviderSub2 || app.cfg.Sub2BaseURL != server.URL || app.cfg.Sub2Email != "sub2@example.test" {
		t.Fatalf("app cfg after Sub2 login = %#v", app.cfg)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Provider != config.ProviderSub2 || saved.Sub2BaseURL != server.URL || saved.Sub2Email != "sub2@example.test" {
		t.Fatalf("saved cfg after Sub2 login = %#v", saved)
	}
}

func TestSaveSettingsIgnoresGlassToggleForNewAPIProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = config.ProviderNewAPI
	cfg.TbarEnabled = false
	configPath := filepath.Join(t.TempDir(), "config.json")
	app := &App{
		paths:  paths.Paths{Config: configPath},
		cfg:    cfg,
		svc:    &krill.Service{},
		newSvc: &newapi.Service{},
	}

	got, err := app.SaveSettings(SettingsRequest{
		RefreshSec:    cfg.RefreshSec,
		OnTop:         cfg.OnTop,
		GlassEnabled:  true,
		RememberLogin: cfg.RememberLogin,
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: "https://newapi.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.cfg.TbarEnabled || got.GlassEnabled {
		t.Fatalf("NewAPI settings must not expose or change glass toggle, cfg=%t dto=%t", app.cfg.TbarEnabled, got.GlassEnabled)
	}
}

func TestSaveSettingsAppliesCodexFastProxyToggle(t *testing.T) {
	oldApply := applyCodexFastProxy
	var calls []bool
	applyCodexFastProxy = func(_ context.Context, enabled bool) error {
		calls = append(calls, enabled)
		return nil
	}
	defer func() { applyCodexFastProxy = oldApply }()

	cfg := config.Default()
	configPath := filepath.Join(t.TempDir(), "config.json")
	app := &App{
		paths:  paths.Paths{Config: configPath},
		cfg:    cfg,
		svc:    &krill.Service{},
		newSvc: &newapi.Service{},
	}

	got, err := app.SaveSettings(SettingsRequest{
		RefreshSec:            cfg.RefreshSec,
		OnTop:                 cfg.OnTop,
		GlassEnabled:          cfg.TbarEnabled,
		RememberLogin:         cfg.RememberLogin,
		Provider:              config.ProviderKrill,
		CodexFastProxyEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("applyCodexFastProxy calls = %#v, want [true]", calls)
	}
	if !app.cfg.CodexFastProxyEnabled || !got.CodexFastProxyEnabled {
		t.Fatalf("Codex Fast proxy switch was not committed, cfg=%t dto=%t", app.cfg.CodexFastProxyEnabled, got.CodexFastProxyEnabled)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CodexFastProxyEnabled {
		t.Fatalf("saved CodexFastProxyEnabled = false, want true")
	}
}

func TestSaveSettingsRollsBackCodexFastProxyConfigWhenApplyFails(t *testing.T) {
	oldApply := applyCodexFastProxy
	applyCodexFastProxy = func(context.Context, bool) error {
		return errors.New("proxy failed")
	}
	defer func() { applyCodexFastProxy = oldApply }()

	cfg := config.Default()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{
		paths:  paths.Paths{Config: configPath},
		cfg:    cfg,
		svc:    &krill.Service{},
		newSvc: &newapi.Service{},
	}

	_, err := app.SaveSettings(SettingsRequest{
		RefreshSec:            cfg.RefreshSec,
		OnTop:                 cfg.OnTop,
		GlassEnabled:          cfg.TbarEnabled,
		RememberLogin:         cfg.RememberLogin,
		Provider:              config.ProviderKrill,
		CodexFastProxyEnabled: true,
	})
	if err == nil {
		t.Fatal("SaveSettings should return proxy apply error")
	}
	if app.cfg.CodexFastProxyEnabled {
		t.Fatal("SaveSettings should not commit Codex Fast switch after apply failure")
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CodexFastProxyEnabled {
		t.Fatal("SaveSettings should roll back persisted Codex Fast switch after apply failure")
	}
}

func TestSaveSettingsDoesNotCommitOrClearSavedLoginWhenConfigSaveFails(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.Save(configPath, config.Config{
		Email:         "user@example.com",
		Provider:      config.ProviderKrill,
		RememberLogin: true,
	}); err != nil {
		t.Fatal(err)
	}
	secretStore := secret.NewStore(filepath.Join(dir, "secrets.json"))
	if err := secretStore.Set("password", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := secretStore.Set("token", "token"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, config.Default()); err != nil {
		t.Fatal(err)
	}
	if err := makePathDirectory(configPath); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Email = "user@example.com"
	cfg.RememberLogin = true
	app := &App{
		paths: paths.Paths{Config: configPath},
		cfg:   cfg,
		svc: &krill.Service{
			Config:  &cfg,
			Secrets: secretStore,
		},
		newSvc: &newapi.Service{Secrets: secretStore},
	}

	_, err := app.SaveSettings(SettingsRequest{
		RefreshSec:    cfg.RefreshSec,
		OnTop:         cfg.OnTop,
		GlassEnabled:  cfg.TbarEnabled,
		RememberLogin: false,
		Provider:      config.ProviderKrill,
	})
	if err == nil {
		t.Fatal("SaveSettings should return config save error")
	}
	if !app.cfg.RememberLogin {
		t.Fatal("SaveSettings should not commit in-memory config after save failure")
	}
	password, err := secretStore.Get("password")
	if err != nil {
		t.Fatal(err)
	}
	if password != "secret" {
		t.Fatalf("saved password = %q, want preserved secret", password)
	}
	token, err := secretStore.Get("token")
	if err != nil {
		t.Fatal(err)
	}
	if token != "token" {
		t.Fatalf("saved token = %q, want preserved token", token)
	}
}

func TestLoginCanSupersedeInFlightRefresh(t *testing.T) {
	subscriptionStarted := make(chan struct{})
	releaseStaleRefresh := make(chan struct{})
	var subscriptionOnce sync.Once
	var mu sync.Mutex
	subscriptionRequests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			writeKrillAPITestResponse(t, w, map[string]string{"token": testWailsJWT(time.Now().Add(time.Hour))})
		case "/api/subscription":
			mu.Lock()
			subscriptionRequests++
			requestNo := subscriptionRequests
			mu.Unlock()
			if requestNo == 1 {
				subscriptionOnce.Do(func() { close(subscriptionStarted) })
				<-releaseStaleRefresh
				writeKrillAPITestResponse(t, w, minimalWailsKrillPayload(10))
				return
			}
			writeKrillAPITestResponse(t, w, minimalWailsKrillPayload(99))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	secretStore := secret.NewStore(filepath.Join(dir, "secrets.json"))
	if err := secretStore.Set("token", testWailsJWT(time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Email = "old@example.com"
	cfg.RememberLogin = true
	configPath := filepath.Join(dir, "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{
		paths: paths.Paths{Config: configPath},
		cfg:   cfg,
		snap:  krill.EmptySnapshot("old"),
	}
	app.svc = &krill.Service{
		Client:  &krill.Client{BaseURL: server.URL, HTTPClient: server.Client()},
		Config:  &app.cfg,
		Secrets: secretStore,
	}
	app.svc.Configure(app.cfg)
	app.newSvc = &newapi.Service{Config: &app.cfg, Secrets: secretStore}
	app.newSvc.Configure(app.cfg)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := app.refresh(false)
		refreshDone <- err
	}()
	<-subscriptionStarted

	snap, err := app.Login(LoginRequest{
		Provider:      config.ProviderKrill,
		Email:         "new@example.com",
		Password:      "secret",
		RememberLogin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Summary.TotalDailyQuotaUSD != 99 {
		t.Fatalf("login snapshot = %#v, want logged-in quota 99", snap)
	}

	close(releaseStaleRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatalf("stale refresh returned error: %v", err)
	}

	got, err := app.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "new@example.com" || got.Summary.TotalDailyQuotaUSD != 99 {
		t.Fatalf("stale refresh overwrote login snapshot: %#v", got)
	}
	if subscriptionRequests != 2 {
		t.Fatalf("subscriptionRequests = %d, want refresh + login fetch", subscriptionRequests)
	}
}

func TestMigratePlaintextPasswordKeepsLegacyPasswordWhenSecretWriteFails(t *testing.T) {
	cfg := config.Default()
	cfg.Password = "legacy-secret"
	storeErr := errors.New("secret write failed")
	saveCalled := false

	got := migratePlaintextPassword(cfg, "config.json", failingSecretSetter{err: storeErr}, func(string, config.Config) error {
		saveCalled = true
		return nil
	})

	if got.Password != "legacy-secret" {
		t.Fatalf("Password = %q, want legacy password preserved", got.Password)
	}
	if saveCalled {
		t.Fatal("config save should not run when secret migration fails")
	}
}

func TestMigratePlaintextPasswordKeepsSecretWhenConfigCleanupFails(t *testing.T) {
	cfg := config.Default()
	cfg.Password = "legacy-secret"
	store := recordingSecretSetter{}
	saveErr := errors.New("config save failed")

	got := migratePlaintextPassword(cfg, "config.json", &store, func(string, config.Config) error {
		return saveErr
	})

	if got.Password != "" {
		t.Fatalf("Password = %q, want in-memory password cleared after secret write", got.Password)
	}
	if store.values["password"] != "legacy-secret" {
		t.Fatalf("migrated secret = %q, want legacy password", store.values["password"])
	}
}

func TestNextOAuthCallbackPrefersBufferedCallbackWhenBrowserDone(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	callbacks := make(chan oauthCallbackResult, 1)
	callbacks <- oauthCallbackResult{CallbackURL: callbackURL}
	done := make(chan struct{})
	close(done)
	appStop := make(chan struct{})

	callback, ok := nextOAuthCallback(callbacks, done, appStop)
	if !ok {
		t.Fatal("expected buffered callback to win over closed browser done channel")
	}
	if callback.CallbackURL != callbackURL {
		t.Fatalf("callbackURL = %q", callback.CallbackURL)
	}
}

func TestWaitNewAPIOAuthCallbackCompletesBrowserSessionInBackend(t *testing.T) {
	var sawUserSelf bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeWailsAPITestResponse(t, w, map[string]any{
				"system_name":        "Test NewAPI",
				"linuxdo_oauth":      true,
				"linuxdo_client_id":  "linuxdo-client-id",
				"quota_per_unit":     500000,
				"quota_display_type": "USD",
			})
		case "/api/user/self":
			sawUserSelf = true
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "browser-session" {
				t.Fatalf("user/self did not receive captured browser session")
			}
			writeWailsAPITestResponse(t, w, map[string]any{
				"id":         42,
				"username":   "linuxdo_user",
				"quota":      900000,
				"used_quota": 100000,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{
		paths: paths.Paths{Config: configPath},
		cfg:   cfg,
		svc:   &krill.Service{},
		stop:  make(chan struct{}),
	}
	app.newSvc = &newapi.Service{Config: &app.cfg}
	app.newSvc.Configure(app.cfg)

	callbacks := make(chan oauthCallbackResult, 1)
	callbacks <- oauthCallbackResult{
		SessionCookies: `[{"name":"session","value":"browser-session"}]`,
		UserID:         "42",
	}
	app.waitNewAPIOAuthCallback(server.URL, true, &oauthCapture{
		Callbacks: callbacks,
		Done:      make(chan struct{}),
		close:     func() {},
	})

	if !sawUserSelf {
		t.Fatal("captured browser session was not completed in backend")
	}
	if !app.snap.LoggedIn || app.cfg.Provider != config.ProviderNewAPI || app.cfg.NewAPIBaseURL != server.URL {
		t.Fatalf("app state not updated after captured login: provider=%q base=%q snap=%#v", app.cfg.Provider, app.cfg.NewAPIBaseURL, app.snap)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Provider != config.ProviderNewAPI || saved.NewAPIBaseURL != server.URL {
		t.Fatalf("captured login did not persist NewAPI config: provider=%q base=%q", saved.Provider, saved.NewAPIBaseURL)
	}
}

func stubOAuthCapture(t *testing.T, callbacks <-chan oauthCallbackResult, onStart func(authorizeURL, baseURL string), onClientID ...func(clientID string)) func() {
	t.Helper()
	old := startOAuthBrowserCapture
	startOAuthBrowserCapture = func(ctx context.Context, authorizeURL, baseURL, clientID string) (*oauthCapture, error) {
		if onStart != nil {
			onStart(authorizeURL, baseURL)
		}
		if len(onClientID) > 0 && onClientID[0] != nil {
			onClientID[0](clientID)
		}
		return &oauthCapture{
			Callbacks: callbacks,
			Done:      make(chan struct{}),
			close:     func() {},
		}, nil
	}
	return func() {
		startOAuthBrowserCapture = old
	}
}

func writeWailsAPITestResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data}); err != nil {
		t.Fatal(err)
	}
}

func writeKrillAPITestResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data}); err != nil {
		t.Fatal(err)
	}
}

func writeSub2WailsAPITestResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data}); err != nil {
		t.Fatal(err)
	}
}

func minimalWailsKrillPayload(totalDailyQuota float64) map[string]any {
	return map[string]any{
		"summary": map[string]any{
			"total_used_usd":        1,
			"total_daily_quota_usd": totalDailyQuota,
		},
		"subscriptions": []any{},
	}
}

func testWailsJWT(exp time.Time) string {
	payload, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func makePathDirectory(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Mkdir(path, 0o700)
}

type failingSecretSetter struct {
	err error
}

func (s failingSecretSetter) Set(string, string) error {
	return s.err
}

type recordingSecretSetter struct {
	values map[string]string
}

func (s *recordingSecretSetter) Set(key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}
