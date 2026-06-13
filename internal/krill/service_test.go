package krill

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"quotaball/internal/config"
)

func TestFetchAutoLoginResultIsIgnoredAfterLogout(t *testing.T) {
	loginStarted := make(chan struct{})
	releaseLogin := make(chan struct{})
	var once sync.Once
	loginRequests := 0
	subscriptionRequests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginRequests++
			once.Do(func() { close(loginStarted) })
			<-releaseLogin
			writeAPISuccess(t, w, map[string]string{"token": testJWT(time.Now().Add(time.Hour))})
		case "/api/subscription":
			subscriptionRequests++
			writeAPISuccess(t, w, minimalPayload())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{Client: &Client{BaseURL: server.URL, HTTPClient: server.Client()}}
	svc.Configure(config.Config{Email: "user@example.com", Password: "secret", RememberLogin: true})

	done := make(chan struct {
		snap Snapshot
		err  error
	}, 1)
	go func() {
		snap, err := svc.Fetch(context.Background())
		done <- struct {
			snap Snapshot
			err  error
		}{snap: snap, err: err}
	}()

	<-loginStarted
	svc.Logout()
	close(releaseLogin)

	got := <-done
	if !errors.Is(got.err, ErrAuthRequired) {
		t.Fatalf("Fetch error = %v, want ErrAuthRequired", got.err)
	}
	if got.snap.LoggedIn {
		t.Fatalf("stale auto-login returned a logged-in snapshot: %#v", got.snap)
	}
	if svc.HasLoginState() {
		t.Fatalf("stale auto-login restored service login state")
	}
	if loginRequests != 1 {
		t.Fatalf("loginRequests = %d, want 1", loginRequests)
	}
	if subscriptionRequests != 0 {
		t.Fatalf("stale auto-login should not continue to subscription after logout, got %d requests", subscriptionRequests)
	}
}

func TestFetchSubscriptionResultIsIgnoredAfterLogout(t *testing.T) {
	subscriptionStarted := make(chan struct{})
	releaseSubscription := make(chan struct{})
	var once sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/subscription":
			once.Do(func() { close(subscriptionStarted) })
			<-releaseSubscription
			writeAPISuccess(t, w, minimalPayload())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := &Service{Client: &Client{BaseURL: server.URL, HTTPClient: server.Client()}}
	svc.Configure(config.Config{Email: "user@example.com", RememberLogin: true})
	svc.mu.Lock()
	svc.memToken = testJWT(time.Now().Add(time.Hour))
	svc.mu.Unlock()

	done := make(chan struct {
		snap Snapshot
		err  error
	}, 1)
	go func() {
		snap, err := svc.Fetch(context.Background())
		done <- struct {
			snap Snapshot
			err  error
		}{snap: snap, err: err}
	}()

	<-subscriptionStarted
	svc.Logout()
	close(releaseSubscription)

	got := <-done
	if !errors.Is(got.err, ErrAuthRequired) {
		t.Fatalf("Fetch error = %v, want ErrAuthRequired", got.err)
	}
	if got.snap.LoggedIn {
		t.Fatalf("stale subscription returned a logged-in snapshot: %#v", got.snap)
	}
	if svc.HasLoginState() {
		t.Fatalf("stale subscription restored service login state")
	}
}

func writeAPISuccess(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(apiResponse[any]{Success: true, Data: data}); err != nil {
		t.Fatal(err)
	}
}

func minimalPayload() map[string]any {
	return map[string]any{
		"summary": map[string]any{
			"total_used_usd":        1,
			"total_daily_quota_usd": 10,
		},
		"subscriptions": []any{},
	}
}

func testJWT(exp time.Time) string {
	payload, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
