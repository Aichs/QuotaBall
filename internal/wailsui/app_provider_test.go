package wailsui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"krill_monitor/internal/config"
	"krill_monitor/internal/krill"
	"krill_monitor/internal/newapi"
	"krill_monitor/internal/paths"
)

func TestStartNewAPIOAuthDoesNotPersistProviderBeforeCompletion(t *testing.T) {
	restore := stubOAuthCapture(t, nil, nil)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeWailsAPITestResponse(t, w, map[string]any{
				"system_name":       "Test NewAPI",
				"linuxdo_oauth":     true,
				"linuxdo_client_id": "linuxdo-client-id",
			})
		case "/api/oauth/state":
			writeWailsAPITestResponse(t, w, "state-123")
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

func TestStartNewAPIOAuthStartsAutomaticCallbackWhenRequested(t *testing.T) {
	callbacks := make(chan string)
	var started bool
	restore := stubOAuthCapture(t, callbacks, func() {
		started = true
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
		case "/api/oauth/state":
			writeWailsAPITestResponse(t, w, "state-123")
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
	if !strings.Contains(start.AuthorizeURL, "redirect_uri=http%3A%2F%2F127.0.0.1%3A27182%2Foauth%2Flinuxdo") {
		t.Fatalf("authorize URL must include the local callback redirect_uri when automatic callback is requested: %q", start.AuthorizeURL)
	}
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

func stubOAuthCapture(t *testing.T, callbacks <-chan string, onStart func()) func() {
	t.Helper()
	old := startOAuthCallback
	startOAuthCallback = func(ctx context.Context) (*oauthCapture, error) {
		if onStart != nil {
			onStart()
		}
		return &oauthCapture{
			RedirectURI: localOAuthRedirectURI,
			Callbacks:   callbacks,
			Done:        make(chan struct{}),
			close:       func() {},
		}, nil
	}
	return func() {
		startOAuthCallback = old
	}
}

func writeWailsAPITestResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data}); err != nil {
		t.Fatal(err)
	}
}
