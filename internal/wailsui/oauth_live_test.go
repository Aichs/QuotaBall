//go:build liveoauth

package wailsui

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"quotaball/internal/newapi"
)

func TestLiveNewAPILinuxDoOAuthCapture(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_NEWAPI_BASE_URL"))
	if baseURL == "" {
		t.Skip("set LIVE_NEWAPI_BASE_URL to run live OAuth capture")
	}
	base, err := newapi.NormalizeBaseURL(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(oauthDebugPortEnv, "27183")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client, err := newapi.NewClient(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.LinuxDoOAuth || strings.TrimSpace(status.LinuxDoClientID) == "" {
		t.Fatal("site does not expose LinuxDo OAuth")
	}

	go clickLinuxDoAuthorizeButtons(ctx, 27183, status.LinuxDoClientID)

	capture, err := startDefaultOAuthBrowserCapture(ctx, "", base, status.LinuxDoClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()

	var callback oauthCallbackResult
	select {
	case callback = <-capture.Callbacks:
	case <-capture.Done:
		t.Fatal("OAuth capture ended before credentials were captured")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !oauthCallbackHasCredential(callback) {
		t.Fatalf("OAuth capture did not return usable credentials: %s", callback.Error)
	}
	t.Logf(
		"captured callback=%t session=%t token=%t user=%t",
		strings.TrimSpace(callback.CallbackURL) != "",
		strings.TrimSpace(callback.SessionCookies) != "",
		strings.TrimSpace(callback.AccessToken) != "",
		strings.TrimSpace(callback.UserID) != "",
	)

	svc := &newapi.Service{}
	var snapErr error
	var loggedIn bool
	switch {
	case strings.TrimSpace(callback.AccessToken) != "":
		snap, err := svc.CompleteBrowserToken(ctx, base, callback.AccessToken, callback.UserID, false)
		loggedIn = snap.LoggedIn
		snapErr = err
	case strings.TrimSpace(callback.SessionCookies) != "":
		snap, err := svc.CompleteBrowserSession(ctx, base, callback.SessionCookies, callback.UserID, false)
		loggedIn = snap.LoggedIn
		snapErr = err
	default:
		snapErr = newapi.ErrAuthRequired
	}
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if !loggedIn {
		t.Fatal("captured credentials did not produce a logged-in snapshot")
	}
	t.Log("captured credentials produced a logged-in snapshot")
}

func clickLinuxDoAuthorizeButtons(ctx context.Context, port int, clientID string) {
	client := &http.Client{Timeout: time.Second}
	seen := map[string]struct{}{}
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		raw, err := fetchDevToolsTabs(client, port)
		if err != nil {
			continue
		}
		for _, tab := range devToolsTabs(raw) {
			if !shouldPrepareOAuthAuthorizeTab(clientID, tab) {
				continue
			}
			if _, ok := seen[tab.ID]; ok {
				continue
			}
			seen[tab.ID] = struct{}{}
			go clickLinuxDoAuthorizeButton(ctx, tab.WebSocketDebuggerURL)
		}
	}
}

func clickLinuxDoAuthorizeButton(ctx context.Context, websocketURL string) {
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	expr := `(() => {
		const words = ["允许", "同意", "授权", "确认", "Allow", "Authorize", "Approve", "Confirm"];
		const nodes = Array.from(document.querySelectorAll("button,a,input[type='submit'],input[type='button']"));
		for (const node of nodes) {
			const label = String(node.innerText || node.value || node.getAttribute("aria-label") || node.textContent || "").trim();
			if (!label) continue;
			if (words.some((word) => label.toLowerCase().includes(word.toLowerCase()))) {
				node.click();
				return JSON.stringify({ clicked: true });
			}
		}
		return JSON.stringify({ clicked: false });
	})()`
	_, _ = cdpRequest(ctx, conn, 1, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	}, 2*time.Second)
}
