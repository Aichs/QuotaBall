package sub2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeBaseURLStripsAPIPrefix(t *testing.T) {
	got, err := NormalizeBaseURL("https://sub2.example.test/api/v1/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://sub2.example.test" {
		t.Fatalf("NormalizeBaseURL = %q, want root base URL", got)
	}
}

func TestClientUsesAPIV1AndBearerToken(t *testing.T) {
	var sawLogin bool
	var sawProfile bool
	var sawSummary bool
	var sawProgress bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			sawLogin = true
			writeTestResponse(t, w, map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
				"token_type":    "Bearer",
				"user": map[string]any{
					"id":      7,
					"email":   "user@example.test",
					"balance": 8.5,
				},
			})
		case "/api/v1/auth/me":
			sawProfile = true
			requireBearer(t, r)
			writeTestResponse(t, w, map[string]any{
				"id":      7,
				"email":   "user@example.test",
				"balance": 8.5,
			})
		case "/api/v1/subscriptions/summary":
			sawSummary = true
			requireBearer(t, r)
			writeTestResponse(t, w, map[string]any{
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
			sawProgress = true
			requireBearer(t, r)
			writeTestResponse(t, w, []any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/api/v1", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := client.Login(context.Background(), "user@example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != "access-token" || auth.RefreshToken != "refresh-token" {
		t.Fatalf("auth = %#v", auth)
	}
	if _, err := client.Profile(context.Background(), auth.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscriptionSummary(context.Background(), auth.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscriptionProgress(context.Background(), auth.AccessToken); err != nil {
		t.Fatal(err)
	}
	if !sawLogin || !sawProfile || !sawSummary || !sawProgress {
		t.Fatalf("missing endpoint calls login=%v profile=%v summary=%v progress=%v", sawLogin, sawProfile, sawSummary, sawProgress)
	}
}

func TestClientProgressDecodesSub2QuotaWindowFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		writeTestResponse(t, w, []any{
			map[string]any{
				"subscription": map[string]any{"id": 11},
				"progress": map[string]any{
					"subscription_id": 11,
					"daily": map[string]any{
						"used_usd":          "12.5",
						"limit_usd":         "100",
						"remaining_usd":     "87.5",
						"percentage":        12.5,
						"resets_in_seconds": 3600,
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	progress, err := client.SubscriptionProgress(context.Background(), "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 1 || progress[0].Progress == nil || progress[0].Progress.Daily == nil {
		t.Fatalf("progress = %#v", progress)
	}
	daily := progress[0].Progress.Daily
	if !daily.usedSet || daily.Used != 12.5 || daily.Limit.Float64() != 100 || daily.Remaining.Float64() != 87.5 {
		t.Fatalf("daily progress = %#v", daily)
	}
	if got, ok := daily.ResetInSeconds.Int(); !ok || got != 3600 {
		t.Fatalf("daily reset = %v/%v, want 3600/true", got, ok)
	}
}

func TestClientLoginRejectsTwoFactorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestResponse(t, w, map[string]any{
			"requires_2fa": true,
			"temp_token":   "tmp",
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Login(context.Background(), "user@example.test", "secret")
	if err == nil || !errors.Is(err, ErrAuthRequired) && err.Error() != "该 Sub2 账号启用了 2FA，当前版本暂不支持" {
		t.Fatalf("Login error = %v, want unsupported 2FA error", err)
	}
}

func TestClientLoginReportsUnsupportedTurnstile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestError(t, w, http.StatusBadRequest, "turnstile token required")
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Login(context.Background(), "user@example.test", "secret")
	if err == nil || err.Error() != "该 Sub2 站点启用了 Turnstile 验证码，当前版本暂不支持" {
		t.Fatalf("Login error = %v, want unsupported Turnstile error", err)
	}
}

func TestClientLoginReportsUnsupportedTurnstileFromNonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>turnstile challenge</html>"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Login(context.Background(), "user@example.test", "secret")
	if err == nil || err.Error() != "该 Sub2 站点启用了 Turnstile 验证码，当前版本暂不支持" {
		t.Fatalf("Login error = %v, want unsupported Turnstile error", err)
	}
}

func writeTestResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    0,
		"message": "ok",
		"data":    data,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeTestError(t *testing.T, w http.ResponseWriter, status int, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    status,
		"message": message,
		"data":    nil,
	}); err != nil {
		t.Fatal(err)
	}
}

func requireBearer(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}
