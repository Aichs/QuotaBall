package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"quotaball/internal/config"
	"quotaball/internal/krill"
	"quotaball/internal/secret"
)

const asyncTestTimeout = 5 * time.Second

type serviceResult struct {
	snap krill.Snapshot
	err  error
}

func waitForSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(asyncTestTimeout):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForServiceResult(t *testing.T, ch <-chan serviceResult, name string) serviceResult {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(asyncTestTimeout):
		t.Fatalf("timed out waiting for %s", name)
	}
	return serviceResult{}
}

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

func TestServiceConfigureWaitsForAuthPersistenceBoundary(t *testing.T) {
	const baseA = "https://a.example"
	const baseB = "https://b.example"

	svc := &Service{}
	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: baseA,
		RememberLogin: true,
	})
	svc.mu.Lock()
	initialGen := svc.authGen
	svc.mu.Unlock()

	svc.persistMu.Lock()
	var unlockOnce sync.Once
	unlock := func() {
		unlockOnce.Do(func() { svc.persistMu.Unlock() })
	}
	t.Cleanup(unlock)

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		svc.Configure(config.Config{
			Provider:      config.ProviderNewAPI,
			NewAPIBaseURL: baseB,
			RememberLogin: true,
		})
		close(done)
	}()

	waitForSignal(t, started, "Configure goroutine start")
	select {
	case <-done:
		t.Fatal("Configure completed while auth persistence boundary was held")
	case <-time.After(50 * time.Millisecond):
	}

	svc.mu.Lock()
	blockedBase := svc.baseURL
	blockedGen := svc.authGen
	svc.mu.Unlock()
	if blockedBase != baseA || blockedGen != initialGen {
		t.Fatalf("Configure mutated auth state while persistence boundary was held, base=%q gen=%d", blockedBase, blockedGen)
	}

	unlock()
	waitForSignal(t, done, "Configure completion")

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.baseURL != baseB || svc.authGen != initialGen+1 {
		t.Fatalf("Configure did not apply after boundary release, base=%q gen=%d", svc.baseURL, svc.authGen)
	}
}

func TestServiceCompleteLinuxDoUsesSessionOnlyLoginForFetch(t *testing.T) {
	stateValue := testOAuthState(t)
	code, _ := testOAuthCodeState(t)
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
			writeServiceAPI(t, w, map[string]any{"success": true, "data": stateValue})
		case "/api/oauth/linuxdo":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "oauth-session" {
				t.Fatalf("OAuth completion did not reuse state session cookie")
			}
			if r.URL.Query().Get("state") != stateValue {
				t.Fatalf("state = %q, want %q", r.URL.Query().Get("state"), stateValue)
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
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "logged-in-session" {
				t.Fatalf("fetch did not reuse logged-in session cookie")
			}
			sawUserSelf = true
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":            42,
				"username":      "linuxdo_user",
				"quota":         900000,
				"used_quota":    100000,
				"request_count": 618,
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
	callbackURL := testOAuthCallbackURL(t, server.URL, "/oauth/linuxdo", code, stateValue)
	snap, err := svc.CompleteLinuxDo(context.Background(), server.URL, callbackURL, false)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Email != "linuxdo_user" || !sawUserSelf {
		t.Fatalf("snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}
	if snap.Req != "618" {
		t.Fatalf("Req = %q, want 618", snap.Req)
	}

	sawUserSelf = false
	snap, err = svc.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || !sawUserSelf {
		t.Fatalf("Fetch should keep using session login, snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}
	if snap.Req != "618" {
		t.Fatalf("Fetch Req = %q, want 618", snap.Req)
	}
}

func TestServicePersistsSessionOnlyLoginWhenRemembered(t *testing.T) {
	stateValue := testOAuthState(t)
	code, _ := testOAuthCodeState(t)
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
			writeServiceAPI(t, w, map[string]any{"success": true, "data": stateValue})
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
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
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
	callbackURL := testOAuthCallbackURL(t, server.URL, "/oauth/linuxdo", code, stateValue)
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

func TestServiceSaveAuthPersistsTokenAuthAndClearsSession(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI-backed secret writes are Windows-only")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	store := secret.NewStore(filepath.Join(t.TempDir(), "secrets.json"))
	if err := store.Set(sessionKey(server.URL), `[{"name":"session","value":"stale-session"}]`); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Secrets: store}
	if err := svc.saveAuth(server.URL, "new-token", nil, "user@example.com", "42"); err != nil {
		t.Fatal(err)
	}

	token, err := store.Get(tokenKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-token" {
		t.Fatalf("token = %q", token)
	}
	session, err := store.Get(sessionKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		t.Fatalf("session = %q, want cleared", session)
	}
	email, err := store.Get(emailKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" {
		t.Fatalf("email = %q", email)
	}
	userID, err := store.Get(userIDKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if userID != "42" {
		t.Fatalf("userID = %q", userID)
	}
}

func TestServiceSaveAuthPersistsSessionAuthAndClearsToken(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI-backed secret writes are Windows-only")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	store := secret.NewStore(filepath.Join(t.TempDir(), "secrets.json"))
	if err := store.Set(tokenKey(server.URL), "stale-token"); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ImportSessionCookies(`[{"name":"session","value":"fresh-session"}]`); err != nil {
		t.Fatal(err)
	}

	svc := &Service{Secrets: store}
	if err := svc.saveAuth(server.URL, "", client, "user@example.com", "42"); err != nil {
		t.Fatal(err)
	}

	token, err := store.Get(tokenKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Fatalf("token = %q, want cleared", token)
	}
	session, err := store.Get(sessionKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(session, "fresh-session") {
		t.Fatalf("session = %q, want fresh session", session)
	}
	email, err := store.Get(emailKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" {
		t.Fatalf("email = %q", email)
	}
	userID, err := store.Get(userIDKey(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if userID != "42" {
		t.Fatalf("userID = %q", userID)
	}
}

func TestServiceFetchResultIsIgnoredAfterLogout(t *testing.T) {
	userSelfStarted := make(chan struct{})
	releaseUserSelf := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"system_name":        "Test NewAPI",
				"quota_per_unit":     500000,
				"quota_display_type": "USD",
			}})
		case "/api/user/self":
			once.Do(func() { close(userSelfStarted) })
			<-releaseUserSelf
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
	defer releaseOnce.Do(func() { close(releaseUserSelf) })

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.UserID = "42"
	svc := &Service{}
	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: server.URL,
		RememberLogin: false,
	})
	svc.mu.Lock()
	svc.sessionClient = client
	svc.userID = "42"
	svc.mu.Unlock()

	done := make(chan serviceResult, 1)
	go func() {
		snap, err := svc.Fetch(context.Background())
		done <- serviceResult{snap: snap, err: err}
	}()

	waitForSignal(t, userSelfStarted, "/api/user/self request")
	svc.Logout()
	releaseOnce.Do(func() { close(releaseUserSelf) })

	got := waitForServiceResult(t, done, "Fetch result")
	if !errors.Is(got.err, ErrAuthRequired) {
		t.Fatalf("Fetch error = %v, want ErrAuthRequired", got.err)
	}
	if got.snap.LoggedIn {
		t.Fatalf("stale Fetch returned logged-in snapshot: %#v", got.snap)
	}
	if svc.HasLoginState() {
		t.Fatal("stale Fetch restored NewAPI login state")
	}
}

func TestServiceCompleteBrowserSessionResultIsIgnoredAfterLogout(t *testing.T) {
	userSelfStarted := make(chan struct{})
	releaseUserSelf := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"system_name":        "Test NewAPI",
				"quota_per_unit":     500000,
				"quota_display_type": "USD",
			}})
		case "/api/user/self":
			once.Do(func() { close(userSelfStarted) })
			<-releaseUserSelf
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
	defer releaseOnce.Do(func() { close(releaseUserSelf) })

	svc := &Service{}
	cookies := `[{"name":"session","value":"browser-session"}]`
	done := make(chan serviceResult, 1)
	go func() {
		snap, err := svc.CompleteBrowserSession(context.Background(), server.URL, cookies, "42", false)
		done <- serviceResult{snap: snap, err: err}
	}()

	waitForSignal(t, userSelfStarted, "/api/user/self request")
	svc.Logout()
	releaseOnce.Do(func() { close(releaseUserSelf) })

	got := waitForServiceResult(t, done, "CompleteBrowserSession result")
	if !errors.Is(got.err, ErrAuthRequired) {
		t.Fatalf("CompleteBrowserSession error = %v, want ErrAuthRequired", got.err)
	}
	if got.snap.LoggedIn {
		t.Fatalf("stale CompleteBrowserSession returned logged-in snapshot: %#v", got.snap)
	}
	if svc.HasLoginState() {
		t.Fatal("stale CompleteBrowserSession restored NewAPI login state")
	}
}

func TestServiceStartLinuxDoBrowserDoesNotCreateBackendOAuthState(t *testing.T) {
	var sawState bool
	var sawLogout bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"system_name":       "Test NewAPI",
				"linuxdo_oauth":     true,
				"linuxdo_client_id": "client-id",
			}})
		case "/api/user/logout":
			sawLogout = true
			writeServiceAPI(t, w, map[string]any{"success": true, "data": nil})
		case "/api/oauth/state":
			sawState = true
			writeServiceAPI(t, w, map[string]any{"success": true, "data": testOAuthState(t)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{}
	start, err := svc.StartLinuxDoBrowser(context.Background(), server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if start.BaseURL != server.URL || start.LinuxDoClientID != "client-id" {
		t.Fatalf("start=%#v", start)
	}
	if sawState || sawLogout {
		t.Fatalf("browser start must not consume backend OAuth state, sawState=%v sawLogout=%v", sawState, sawLogout)
	}
}

func TestServiceCompleteBrowserSessionUsesImportedCookies(t *testing.T) {
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
		case "/api/user/self":
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("browser-session fetch must not send Authorization header")
			}
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "browser-session" {
				t.Fatalf("fetch did not import browser session cookie")
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

	cookies := `[{"name":"session","value":"browser-session"}]`
	svc := &Service{}
	snap, err := svc.CompleteBrowserSession(context.Background(), server.URL, cookies, "42", false)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn || snap.Email != "linuxdo_user" {
		t.Fatalf("snapshot=%#v", snap)
	}

	snap, err = svc.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LoggedIn {
		t.Fatalf("Fetch should reuse imported browser session, snapshot=%#v", snap)
	}
}

func TestServiceCompleteBrowserTokenUsesBearerAuthorization(t *testing.T) {
	accessToken := "token-" + strings.ToLower(t.Name())
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
		case "/api/user/self":
			sawUserSelf = true
			if r.Header.Get("Authorization") != "Bearer "+accessToken {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
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

	svc := &Service{}
	snap, err := svc.CompleteBrowserToken(context.Background(), server.URL, accessToken, "42", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sawUserSelf || !snap.LoggedIn || snap.Email != "linuxdo_user" {
		t.Fatalf("snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}

	sawUserSelf = false
	snap, err = svc.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sawUserSelf || !snap.LoggedIn {
		t.Fatalf("Fetch should reuse token login, snapshot=%#v sawUserSelf=%v", snap, sawUserSelf)
	}
}

func TestServiceCompleteLinuxDoWithBrowserCookiesUsesCallbackBeforeBrowserConsumesCode(t *testing.T) {
	code := "code-" + strings.ToLower(t.Name())
	state := "state-" + strings.ToLower(t.Name())
	stateCookie := "oauth-state-cookie"
	var sawOAuthCallback bool
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
		case "/api/oauth/linuxdo":
			sawOAuthCallback = true
			if r.URL.Query().Get("code") != code || r.URL.Query().Get("state") != state {
				t.Fatalf("callback query = %q", r.URL.RawQuery)
			}
			cookie, err := r.Cookie("oauth_state")
			if err != nil || cookie.Value != stateCookie {
				t.Fatalf("OAuth completion did not reuse browser state cookie")
			}
			writeServiceAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":       42,
				"username": "linuxdo_user",
				"token":    "newapi-token",
			}})
		case "/api/user/self":
			sawUserSelf = true
			wantAuth := "Bearer " + "newapi-token"
			if r.Header.Get("Authorization") != wantAuth {
				t.Fatalf("user self Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
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

	callbackURL := server.URL + "/oauth/linuxdo?code=" + code + "&state=" + state
	cookies := `[{"name":"oauth_state","value":"` + stateCookie + `"}]`
	svc := &Service{}
	snap, err := svc.CompleteLinuxDoWithCookies(context.Background(), server.URL, callbackURL, cookies, false)
	if err != nil {
		t.Fatal(err)
	}
	if !sawOAuthCallback || !sawUserSelf {
		t.Fatalf("expected OAuth callback and user self requests, sawOAuthCallback=%v sawUserSelf=%v", sawOAuthCallback, sawUserSelf)
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
