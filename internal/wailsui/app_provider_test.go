package wailsui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
	"krill_monitor/internal/newapi"
	"krill_monitor/internal/paths"
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

func TestSaveSettingsDoesNotActivateSub2Placeholder(t *testing.T) {
	cfg := config.Default()
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
		GlassEnabled:  cfg.TbarEnabled,
		RememberLogin: cfg.RememberLogin,
		Provider:      config.ProviderSub2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != config.ProviderKrill || app.cfg.Provider != config.ProviderKrill {
		t.Fatalf("Sub2 placeholder must not become active provider, dto=%q cfg=%q", got.Provider, app.cfg.Provider)
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
