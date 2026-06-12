package newapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBaseURLTrimsTrailingSlashAndRequiresHTTPSForPublicHosts(t *testing.T) {
	publicBaseURL := testPublicBaseURL(t)
	publicHost := strings.TrimPrefix(publicBaseURL, "https://")
	base, err := NormalizeBaseURL(" " + publicBaseURL + "/ ")
	if err != nil {
		t.Fatal(err)
	}
	if base != publicBaseURL {
		t.Fatalf("base = %q, want %q", base, publicBaseURL)
	}

	if _, err := NormalizeBaseURL("ftp://" + publicHost); err == nil {
		t.Fatal("NormalizeBaseURL should reject non-http schemes")
	}
	if _, err := NormalizeBaseURL("http://" + publicHost); err == nil {
		t.Fatal("NormalizeBaseURL should reject plaintext HTTP for public hosts")
	}
	if base, err := NormalizeBaseURL("http://127.0.0.1:3000/"); err != nil || base != "http://127.0.0.1:3000" {
		t.Fatalf("NormalizeBaseURL should allow loopback HTTP for local testing, base=%q err=%v", base, err)
	}
}

func TestLinuxDoAuthorizeURLMatchesNewAPISiteFlow(t *testing.T) {
	got := LinuxDoAuthorizeURL("client-id", "state-value")

	if !strings.HasPrefix(got, "https://connect.linux.do/oauth2/authorize?") {
		t.Fatalf("authorize URL = %q", got)
	}
	if !strings.Contains(got, "response_type=code") ||
		!strings.Contains(got, "client_id=client-id") ||
		!strings.Contains(got, "state=state-value") {
		t.Fatalf("authorize URL missing required query params: %q", got)
	}
	if strings.Contains(got, "redirect_uri=") {
		t.Fatalf("LinuxDo URL should not add redirect_uri; NewAPI sites configure it server-side: %q", got)
	}
}

func TestExtractLinuxDoCallbackRequiresMatchingBaseURLCodeAndState(t *testing.T) {
	baseURL := testPublicBaseURL(t)
	code, state := testOAuthCodeState(t)
	cb, err := ExtractLinuxDoCallback(baseURL, testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state))
	if err != nil {
		t.Fatal(err)
	}
	if cb.Code != code || cb.State != state {
		t.Fatalf("callback = %#v", cb)
	}

	otherBaseURL := testPublicBaseURLWithSuffix(t, "other")
	if _, err := ExtractLinuxDoCallback(baseURL, testOAuthCallbackURL(t, otherBaseURL, "/oauth/linuxdo", code, state)); err == nil {
		t.Fatal("callback from a different host must be rejected")
	}
	if _, err := ExtractLinuxDoCallback(baseURL, testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", "", state)); err == nil {
		t.Fatal("callback without code must be rejected")
	}
}

func TestExtractLinuxDoCallbackAcceptsNewAPIBackendCallback(t *testing.T) {
	baseURL := testPublicBaseURL(t)
	code, state := testOAuthCodeState(t)
	cb, err := ExtractLinuxDoCallback(baseURL, testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state))
	if err != nil {
		t.Fatal(err)
	}
	if cb.Code != code || cb.State != state {
		t.Fatalf("callback = %#v", cb)
	}
}

func TestOAuthStatePreservesSessionCookieForCompletion(t *testing.T) {
	stateValue := testOAuthState(t)
	code, _ := testOAuthCodeState(t)
	var sawSessionCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "oauth-session", Path: "/", HttpOnly: true})
			writeAPI(t, w, map[string]any{"success": true, "data": stateValue})
		case "/api/oauth/linuxdo":
			if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "oauth-session" {
				sawSessionCookie = true
			}
			writeAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"token": "user-token",
				"email": "user@example.com",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.OAuthState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state != stateValue {
		t.Fatalf("state = %q", state)
	}
	user, err := client.CompleteLinuxDoOAuth(context.Background(), code, state)
	if err != nil {
		t.Fatal(err)
	}
	if user.Token != "user-token" || !sawSessionCookie {
		t.Fatalf("user = %#v, sawSessionCookie = %v", user, sawSessionCookie)
	}
}

func TestOAuthCompletionCanUseSessionOnlyLogin(t *testing.T) {
	stateValue := testOAuthState(t)
	code, _ := testOAuthCodeState(t)
	var sawOAuthSessionCookie bool
	var sawLoggedInSessionCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "oauth-session", Path: "/", HttpOnly: true})
			writeAPI(t, w, map[string]any{"success": true, "data": stateValue})
		case "/api/oauth/linuxdo":
			if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "oauth-session" {
				sawOAuthSessionCookie = true
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "logged-in-session", Path: "/", HttpOnly: true})
			writeAPI(t, w, map[string]any{"success": true, "data": map[string]any{
				"id":       42,
				"username": "linuxdo_user",
			}})
		case "/api/user/self":
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("session-only NewAPI login must not send Authorization header")
			}
			if r.Header.Get("New-Api-User") != "42" {
				t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
			}
			if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "logged-in-session" {
				sawLoggedInSessionCookie = true
			}
			writeAPI(t, w, map[string]any{"success": true, "data": map[string]any{
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

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.OAuthState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	user, err := client.CompleteLinuxDoOAuth(context.Background(), code, state)
	if err != nil {
		t.Fatal(err)
	}
	if user.Token != "" || user.Username != "linuxdo_user" || !sawOAuthSessionCookie {
		t.Fatalf("user=%#v sawOAuthSessionCookie=%v", user, sawOAuthSessionCookie)
	}
	client.UserID = strconv.Itoa(user.ID)
	self, err := client.UserSelf(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if self.Username != "linuxdo_user" || !sawLoggedInSessionCookie {
		t.Fatalf("self=%#v sawLoggedInSessionCookie=%v", self, sawLoggedInSessionCookie)
	}
}

func TestUserSelfSendsNewAPIUserHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("New-Api-User") != "42" {
			t.Fatalf("New-Api-User = %q, want 42", r.Header.Get("New-Api-User"))
		}
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeAPI(t, w, map[string]any{"success": true, "data": map[string]any{
			"id":       42,
			"username": "linuxdo_user",
		}})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.UserID = "42"
	self, err := client.UserSelf(context.Background(), "user-token")
	if err != nil {
		t.Fatal(err)
	}
	if self.ID != 42 {
		t.Fatalf("self=%#v", self)
	}
}

func TestOAuthLogoutCanResetSessionBeforeState(t *testing.T) {
	stateValue := testOAuthState(t)
	var sawLogout bool
	var sawState bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/logout":
			sawLogout = true
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "reset-session", Path: "/", HttpOnly: true})
			writeAPI(t, w, map[string]any{"success": true, "data": nil})
		case "/api/oauth/state":
			sawState = true
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "reset-session" {
				t.Fatalf("state request did not reuse reset session cookie")
			}
			writeAPI(t, w, map[string]any{"success": true, "data": stateValue})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := client.OAuthState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state != stateValue || !sawLogout || !sawState {
		t.Fatalf("state=%q sawLogout=%v sawState=%v", state, sawLogout, sawState)
	}
}

func TestUserSelfMapsQuotaSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 30, 0, 0, time.UTC)
	user := UserSelf{
		ID:        42,
		Username:  "mint",
		Email:     "mint@example.com",
		Quota:     900000,
		UsedQuota: 100000,
	}
	status := Status{
		SystemName:       "薄荷 API",
		QuotaPerUnit:     500000,
		QuotaDisplayType: "USD",
	}

	snap := user.ToSnapshot(status, now)

	if !snap.LoggedIn || !snap.OK {
		t.Fatalf("snapshot should be logged in and ok: %#v", snap)
	}
	if snap.Email != "mint@example.com" {
		t.Fatalf("Email = %q", snap.Email)
	}
	if snap.Spend != 0.2 || snap.Wallet != 1.8 {
		t.Fatalf("Spend/Wallet = %v/%v, want 0.2/1.8", snap.Spend, snap.Wallet)
	}
	if len(snap.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(snap.Subscriptions))
	}
	sub := snap.Subscriptions[0]
	if sub.Name != "薄荷 API 账户额度" || sub.DailyLimit != 2 || sub.DailyUsed != 0.2 || sub.DailyRemaining != 1.8 {
		t.Fatalf("subscription = %#v", sub)
	}
}

func TestTokenUsageMapsQuotaSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 30, 0, 0, time.UTC)
	usage := TokenUsage{
		Name:           "Default Token",
		TotalGranted:   1000000,
		TotalUsed:      12345,
		TotalAvailable: 987655,
	}
	status := Status{
		SystemName:   "New API",
		QuotaPerUnit: 500000,
	}

	snap := usage.ToSnapshot(status, "user@example.com", now)

	if snap.Spend != 0.02469 {
		t.Fatalf("Spend = %v, want 0.02469", snap.Spend)
	}
	if snap.Wallet != 1.97531 {
		t.Fatalf("Wallet = %v, want 1.97531", snap.Wallet)
	}
	if len(snap.Subscriptions) != 1 || snap.Subscriptions[0].DailyPercent != 1.2 {
		t.Fatalf("snapshot subscription mapping wrong: %#v", snap.Subscriptions)
	}
}

func writeAPI(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

func testPublicBaseURL(t *testing.T) string {
	t.Helper()
	return testPublicBaseURLWithSuffix(t, "primary")
}

func testPublicBaseURLWithSuffix(t *testing.T, suffix string) string {
	t.Helper()
	return "https://" + testNameSlug(t) + "-" + suffix + ".example.test"
}

func testOAuthCodeState(t *testing.T) (string, string) {
	t.Helper()
	slug := testNameSlug(t)
	return "code-" + slug, "state-" + slug
}

func testOAuthState(t *testing.T) string {
	t.Helper()
	_, state := testOAuthCodeState(t)
	return state
}

func testOAuthCallbackURL(t *testing.T, baseURL, path, code, state string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = path
	q := u.Query()
	if code != "" {
		q.Set("code", code)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func testNameSlug(t *testing.T) string {
	t.Helper()
	return strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
}
