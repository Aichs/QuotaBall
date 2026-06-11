package newapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBaseURLTrimsTrailingSlashAndRequiresHTTPSForPublicHosts(t *testing.T) {
	base, err := NormalizeBaseURL(" https://x666.me/ ")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://x666.me" {
		t.Fatalf("base = %q, want https://x666.me", base)
	}

	if _, err := NormalizeBaseURL("ftp://x666.me"); err == nil {
		t.Fatal("NormalizeBaseURL should reject non-http schemes")
	}
	if _, err := NormalizeBaseURL("http://x666.me"); err == nil {
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

	withRedirect := LinuxDoAuthorizeURL("client-id", "state-value", "http://127.0.0.1:27182/oauth/linuxdo")
	if !strings.Contains(withRedirect, "redirect_uri=http%3A%2F%2F127.0.0.1%3A27182%2Foauth%2Flinuxdo") {
		t.Fatalf("authorize URL should support an explicit local redirect_uri for owned LinuxDo apps: %q", withRedirect)
	}
}

func TestExtractLinuxDoCallbackRequiresMatchingBaseURLCodeAndState(t *testing.T) {
	cb, err := ExtractLinuxDoCallback("https://x666.me", "https://x666.me/oauth/linuxdo?code=abc&state=def")
	if err != nil {
		t.Fatal(err)
	}
	if cb.Code != "abc" || cb.State != "def" {
		t.Fatalf("callback = %#v", cb)
	}

	local, err := ExtractLinuxDoCallback("https://x666.me", "http://127.0.0.1:27182/oauth/linuxdo?code=abc&state=def")
	if err != nil {
		t.Fatal(err)
	}
	if local.Code != "abc" || local.State != "def" {
		t.Fatalf("local callback = %#v", local)
	}

	if _, err := ExtractLinuxDoCallback("https://x666.me", "https://other.example/oauth/linuxdo?code=abc&state=def"); err == nil {
		t.Fatal("callback from a different host must be rejected")
	}
	if _, err := ExtractLinuxDoCallback("https://x666.me", "https://x666.me/oauth/linuxdo?state=def"); err == nil {
		t.Fatal("callback without code must be rejected")
	}
}

func TestOAuthStatePreservesSessionCookieForCompletion(t *testing.T) {
	var sawSessionCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "oauth-session", Path: "/", HttpOnly: true})
			writeAPI(t, w, map[string]any{"success": true, "data": "state-123"})
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
	if state != "state-123" {
		t.Fatalf("state = %q", state)
	}
	user, err := client.CompleteLinuxDoOAuth(context.Background(), "code-123", state)
	if err != nil {
		t.Fatal(err)
	}
	if user.Token != "user-token" || !sawSessionCookie {
		t.Fatalf("user = %#v, sawSessionCookie = %v", user, sawSessionCookie)
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
