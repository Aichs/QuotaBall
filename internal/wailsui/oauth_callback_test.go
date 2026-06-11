package wailsui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartLocalOAuthCallbackCapturesLinuxDoRedirect(t *testing.T) {
	capture, err := startLocalOAuthCallback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()

	resp, err := http.Get(localOAuthRedirectURI + "?code=code-123&state=state-123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "登录已完成") {
		t.Fatalf("callback response should tell the user login is complete: %s", body)
	}

	select {
	case callbackURL := <-capture.Callbacks:
		if callbackURL != localOAuthRedirectURI+"?code=code-123&state=state-123" {
			t.Fatalf("callbackURL = %q", callbackURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for callback URL")
	}
}
