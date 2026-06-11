package wailsui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOAuthCallbackFromDevToolsTabsFindsMatchingNewAPICallback(t *testing.T) {
	raw := []byte(`[
		{"id":"ignore","url":"https://connect.linux.do/oauth2/authorize?state=state-123"},
		{"id":"match","url":"https://x666.me/oauth/linuxdo?code=code-123&state=state-123"}
	]`)

	callbackURL, tabID, ok := oauthCallbackFromDevToolsTabs("https://x666.me", raw)
	if !ok {
		t.Fatal("expected callback URL to be detected")
	}
	if callbackURL != "https://x666.me/oauth/linuxdo?code=code-123&state=state-123" {
		t.Fatalf("callbackURL = %q", callbackURL)
	}
	if tabID != "match" {
		t.Fatalf("tabID = %q, want match", tabID)
	}
}

func TestOAuthCallbackFromDevToolsTabsIgnoresOtherSites(t *testing.T) {
	raw := []byte(`[
		{"id":"other","url":"https://other.example/oauth/linuxdo?code=code-123&state=state-123"}
	]`)

	if callbackURL, tabID, ok := oauthCallbackFromDevToolsTabs("https://x666.me", raw); ok {
		t.Fatalf("unexpected callbackURL=%q tabID=%q", callbackURL, tabID)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsTransientCallbackRequest(t *testing.T) {
	raw := []byte(`{
		"method":"Network.requestWillBeSent",
		"params":{
			"request":{"url":"https://x666.me/oauth/linuxdo?code=code-123&state=state-123"}
		}
	}`)

	callbackURL, ok := oauthCallbackFromDevToolsEvent("https://x666.me", raw)
	if !ok {
		t.Fatal("expected callback URL to be detected from network event")
	}
	if callbackURL != "https://x666.me/oauth/linuxdo?code=code-123&state=state-123" {
		t.Fatalf("callbackURL = %q", callbackURL)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsTransientNavigation(t *testing.T) {
	raw := []byte(`{
		"method":"Page.frameNavigated",
		"params":{
			"frame":{"url":"https://x666.me/oauth/linuxdo?code=code-456&state=state-456"}
		}
	}`)

	callbackURL, ok := oauthCallbackFromDevToolsEvent("https://x666.me", raw)
	if !ok {
		t.Fatal("expected callback URL to be detected from navigation event")
	}
	if callbackURL != "https://x666.me/oauth/linuxdo?code=code-456&state=state-456" {
		t.Fatalf("callbackURL = %q", callbackURL)
	}
}

func TestWatchOAuthDevToolsTabEmitsTransientCallback(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbacks := make(chan string, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	go watchOAuthDevToolsTab(ctx, wsURL, "https://x666.me", callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools watcher did not connect")
	}
	defer conn.Close()
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("read enable message %d: %v", i+1, err)
		}
	}
	err := conn.WriteMessage(websocket.TextMessage, []byte(`{
		"method":"Network.requestWillBeSent",
		"params":{"request":{"url":"https://x666.me/oauth/linuxdo?code=code-789&state=state-789"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case callbackURL := <-callbacks:
		if callbackURL != "https://x666.me/oauth/linuxdo?code=code-789&state=state-789" {
			t.Fatalf("callbackURL = %q", callbackURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not emit callback URL")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop browser after callback")
	}
}

func TestDebugPortFromProcessListFindsOAuthProfilePort(t *testing.T) {
	raw := `
CommandLine="C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --new-window --remote-debugging-address=127.0.0.1 --remote-debugging-port=60037 --user-data-dir="C:\Users\kites\AppData\Local\QuotaBall\OAuthBrowser" https://connect.linux.do/oauth2/authorize?state=abc
CommandLine="C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222 --user-data-dir="C:\Users\kites\AppData\Local\OtherProfile"
`

	port := debugPortFromProcessList(`C:\Users\kites\AppData\Local\QuotaBall\OAuthBrowser`, raw)
	if port != 60037 {
		t.Fatalf("port = %d, want 60037", port)
	}
}

func TestOAuthBrowserProfileDirIsPersistent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QUOTABALL_OAUTH_PROFILE_DIR", root)

	first, err := oauthBrowserProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	second, err := oauthBrowserProfileDir()
	if err != nil {
		t.Fatal(err)
	}

	if first != second {
		t.Fatalf("profile dir must be stable, got %q then %q", first, second)
	}
	if first != filepath.Clean(root) {
		t.Fatalf("profile dir = %q, want override root %q", first, filepath.Clean(root))
	}
}
