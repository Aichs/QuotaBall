package newapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"krill_monitor/internal/config"
	"krill_monitor/internal/secret"
)

func TestServiceConfigureClearsMemoryTokenWhenBaseURLChanges(t *testing.T) {
	svc := &Service{}
	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: "https://a.example",
		RememberLogin: true,
	})

	svc.mu.Lock()
	svc.memToken = "token-for-a"
	svc.email = "a@example.com"
	svc.pending = &pendingOAuth{baseURL: "https://a.example", state: "state-a"}
	svc.mu.Unlock()

	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: "https://b.example",
		RememberLogin: true,
	})

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.memToken != "" || svc.email != "" || svc.pending != nil {
		t.Fatalf("Configure must clear in-memory auth when base URL changes, token=%q email=%q pending=%v", svc.memToken, svc.email, svc.pending != nil)
	}
}

func TestServiceCompleteLinuxDoUsesSessionOnlyLoginForFetch(t *testing.T) {
	var sawUserSelf bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"system_name":        "Test NewAPI",
				"linuxdo_oauth":      true,
				"linuxdo_client_id":  "client-id",
				"quota_per_unit":     500000,
				"quota_display_type": "USD",
			}})
		case "/api/user/logout":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": nil})
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "oauth-session", Path: "/", HttpOnly: true})
			writeServiceAPI(t, w, map[string]any{"success": true, "data": "state-123"})
		case "/api/oauth/linuxdo":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "oauth-session" {
				t.Fatalf("OAuth completion did not reuse state session cookie")
			}
			if r.URL.Query().Get("state") != "state-123" {
				t.Fatalf("state = %q, want state-123", r.URL.Query().Get("state"))
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "logged-in-session", Path: "/", HttpOnly: true})
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":       42,
				"username": "linuxdo_user",
			}})
		case "/api/user/self":
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("session-only NewAPI fetch must not send Authorization header")
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "logged-in-session" {
				t.Fatalf("fetch did not reuse logged-in session cookie")
			}
			sawUserSelf = true
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":         42,
				"username":   "linuxdo_user",
				"quota":      900000,
				"used_quota": 100000,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{}
	start, err := svc.StartLinuxDo(context.Background(), server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if start.BaseURL != server.URL {
		t.Fatalf("BaseURL = %q, want %q", start.BaseURL, server.URL)
	}
	callbackURL := server.URL + "/oauth/linuxdo?code=code-123&state=state-123"
	snap, err := svc.CompleteLinuxDo(context.Background(), server.URL, callbackURL, false)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Email != "linuxdo_user" || !sawUserSelf {
		t.Fatalf("snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}

	sawUserSelf = false
	snap, err = svc.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || !sawUserSelf {
		t.Fatalf("Fetch should keep using session login, snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}
}

func TestServicePersistsSessionOnlyLoginWhenRemembered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"system_name":        "Test NewAPI",
				"linuxdo_oauth":      true,
				"linuxdo_client_id":  "client-id",
				"quota_per_unit":     500000,
				"quota_display_type": "USD",
			}})
		case "/api/user/logout":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": nil})
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "oauth-session", Path: "/", HttpOnly: true})
			writeServiceAPI(t, w, map[string]any{"success": true, "data": "state-123"})
		case "/api/oauth/linuxdo":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "logged-in-session", Path: "/", HttpOnly: true})
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":       42,
				"username": "linuxdo_user",
			}})
		case "/api/user/self":
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("remembered session fetch must not send Authorization header")
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "logged-in-session" {
				t.Fatalf("remembered fetch did not restore logged-in session cookie")
			}
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":         42,
				"username":   "linuxdo_user",
				"quota":      900000,
				"used_quota": 100000,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := secret.NewStore(filepath.Join(t.TempDir(), "secrets.json"))
	svc := &Service{Secrets: store}
	if _, err := svc.StartLinuxDo(context.Background(), server.URL, true); err != nil {
		t.Fatal(err)
	}
	callbackURL := server.URL + "/oauth/linuxdo?code=code-123&state=state-123"
	if _, err := svc.CompleteLinuxDo(context.Background(), server.URL, callbackURL, true); err != nil {
		t.Fatal(err)
	}

	resumed := &Service{Secrets: store}
	resumed.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: server.URL,
		RememberLogin: true,
	})
	if !resumed.HasLoginState() {
		t.Fatal("remembered session cookie should count as login state")
	}
	snap, err := resumed.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Email != "linuxdo_user" {
		t.Fatalf("snapshot=%#v", snap)
	}
}

func writeServiceAPI(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}
