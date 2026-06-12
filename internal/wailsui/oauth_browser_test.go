package wailsui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"krill_monitor/internal/newapi"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "quotaball-oauth-test-*")
	if err != nil {
		os.Exit(m.Run())
	}
	_ = os.Setenv(oauthLogFileEnv, filepath.Join(dir, "oauth.log"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestOAuthCallbackFromDevToolsTabsFindsMatchingNewAPICallback(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	raw := testDevToolsTabsJSON(t, []devToolsTab{
		{ID: "ignore", URL: testOAuthProviderURL(t, state)},
		{ID: "match", URL: callbackURL},
	})

	gotURL, tabID, ok := oauthCallbackFromDevToolsTabs(baseURL, raw)
	if !ok {
		t.Fatal("expected callback URL to be detected")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
	if tabID != "match" {
		t.Fatalf("tabID = %q, want match", tabID)
	}
}

func TestOAuthCallbackFromDevToolsTabsFindsNewAPIBackendCallback(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)
	raw := testDevToolsTabsJSON(t, []devToolsTab{
		{ID: "ignore", URL: testOAuthProviderURL(t, state)},
		{ID: "match", URL: callbackURL},
	})

	gotURL, tabID, ok := oauthCallbackFromDevToolsTabs(baseURL, raw)
	if !ok {
		t.Fatal("expected NewAPI backend callback URL to be detected")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
	if tabID != "match" {
		t.Fatalf("tabID = %q, want match", tabID)
	}
}

func TestOAuthCallbackFromDevToolsTabsIgnoresOtherSites(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	otherBaseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	raw := testDevToolsTabsJSON(t, []devToolsTab{
		{ID: "other", URL: testOAuthCallbackURL(t, otherBaseURL, "/oauth/linuxdo", code, state)},
	})

	if callbackURL, tabID, ok := oauthCallbackFromDevToolsTabs(baseURL, raw); ok {
		t.Fatalf("unexpected callbackURL=%q tabID=%q", callbackURL, tabID)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsTransientCallbackRequest(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	raw := testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL)

	gotURL, ok := oauthCallbackFromDevToolsEvent(baseURL, raw)
	if !ok {
		t.Fatal("expected callback URL to be detected from network event")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsNewAPIBackendCallbackRequest(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)
	raw := testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL)

	gotURL, ok := oauthCallbackFromDevToolsEvent(baseURL, raw)
	if !ok {
		t.Fatal("expected NewAPI backend callback URL to be detected from network event")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsTransientNavigation(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	raw := testDevToolsEventJSON(t, "Page.frameNavigated", callbackURL)

	gotURL, ok := oauthCallbackFromDevToolsEvent(baseURL, raw)
	if !ok {
		t.Fatal("expected callback URL to be detected from navigation event")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
}

func TestOAuthCallbackFromDevToolsEventIgnoresUnexpectedState(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, expectedState := testOAuthCodeState(t)
	staleURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, "stale-"+expectedState)
	raw := testDevToolsEventJSON(t, "Network.requestWillBeSent", staleURL)

	if callbackURL, ok := oauthCallbackFromDevToolsEventForState(baseURL, raw, expectedState); ok {
		t.Fatalf("unexpected stale callback accepted: %q", callbackURL)
	}

	currentURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, expectedState)
	raw = testDevToolsEventJSON(t, "Network.requestWillBeSent", currentURL)
	gotURL, ok := oauthCallbackFromDevToolsEventForState(baseURL, raw, expectedState)
	if !ok {
		t.Fatal("expected current-state callback to be accepted")
	}
	if gotURL != currentURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
}

func TestOAuthCallbackFromDevToolsEventFindsPausedFetchCallback(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	raw := testDevToolsPausedEventJSON(t, "request-1", callbackURL)

	gotURL, ok := oauthCallbackFromDevToolsEvent(baseURL, raw)
	if !ok {
		t.Fatal("expected callback URL to be detected from paused fetch event")
	}
	if gotURL != callbackURL {
		t.Fatalf("callbackURL = %q", gotURL)
	}
}

func TestPausedOAuthCallbackFromDevToolsEventIgnoresUnexpectedState(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	code, expectedState := testOAuthCodeState(t)
	staleURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, "stale-"+expectedState)
	raw := testDevToolsPausedEventJSON(t, "request-1", staleURL)

	if callbackURL, requestID, ok := pausedOAuthCallbackFromDevToolsEventForState(baseURL, raw, expectedState); ok {
		t.Fatalf("unexpected stale paused callback accepted: callbackURL=%q requestID=%q", callbackURL, requestID)
	}

	currentURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, expectedState)
	raw = testDevToolsPausedEventJSON(t, "request-2", currentURL)
	gotURL, requestID, ok := pausedOAuthCallbackFromDevToolsEventForState(baseURL, raw, expectedState)
	if !ok {
		t.Fatal("expected current-state paused callback to be accepted")
	}
	if gotURL != currentURL || requestID != "request-2" {
		t.Fatalf("callbackURL=%q requestID=%q", gotURL, requestID)
	}
}

func TestPrepareAndWatchOAuthDevToolsTabNavigatesAfterObjectStateResponse(t *testing.T) {
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
	baseURL := testHTTPBaseURL(t)
	_, state := testOAuthCodeState(t)
	clientID := "client-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
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
	go prepareAndWatchOAuthDevToolsTab(ctx, wsURL, baseURL, clientID, nil, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools preparer did not connect")
	}
	defer conn.Close()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "Network.enable", "Page.enable", "Fetch.enable":
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
		case "Runtime.evaluate":
			response := map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"result": map[string]any{
						"type":  "object",
						"value": map[string]any{"success": true, "data": state},
					},
				},
			}
			raw, _ := json.Marshal(response)
			_ = conn.WriteMessage(websocket.TextMessage, raw)
		case "Page.navigate":
			navigateURL, _ := req.Params["url"].(string)
			parsed, err := url.Parse(navigateURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Host != "connect.linux.do" || parsed.Query().Get("client_id") != clientID || parsed.Query().Get("state") != state {
				t.Fatalf("navigate URL = %q", navigateURL)
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
		case "Page.bringToFront":
			cancel()
			return
		default:
			t.Fatalf("unexpected devtools method %q", req.Method)
		}
	}
}

func TestPrepareAndWatchOAuthDevToolsTabKeepsCallbackObservedDuringNavigate(t *testing.T) {
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
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	clientID := "client-" + testNameSlug(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)
	accessToken := "token-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	continued := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}
	resolved := make(chan struct{})
	resolve := func(context.Context) oauthCallbackResult {
		select {
		case <-continued:
			close(resolved)
			return oauthCallbackResult{AccessToken: accessToken, UserID: "42"}
		default:
			return oauthCallbackResult{Error: "browser session not ready"}
		}
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	go prepareAndWatchOAuthDevToolsTab(ctx, wsURL, baseURL, clientID, resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools preparer did not connect")
	}
	defer conn.Close()

	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Runtime.evaluate":
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{
							"type":  "object",
							"value": map[string]any{"success": true, "data": state},
						},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.navigate":
				_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-race", callbackURL))
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Page.bringToFront":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Fetch.continueRequest":
				if req.Params["requestId"] != "request-race" {
					t.Errorf("requestId = %v", req.Params["requestId"])
				}
				close(continued)
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.AccessToken != accessToken {
			t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback observed during navigate was swallowed")
	}
	select {
	case <-continued:
	case <-time.After(2 * time.Second):
		t.Fatal("callback request was not continued")
	}
	select {
	case <-resolved:
	case <-time.After(2 * time.Second):
		t.Fatal("browser-session resolver was not called")
	}
}

func TestImportModeDoesNotBlockPausedCallbackAfterNetworkObserved(t *testing.T) {
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
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	clientID := "client-" + testNameSlug(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)
	accessToken := "token-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}
	continued := make(chan struct{})
	resolve := func(context.Context) oauthCallbackResult {
		select {
		case <-continued:
			return oauthCallbackResult{AccessToken: accessToken, UserID: "42"}
		case <-time.After(2 * time.Second):
			return oauthCallbackResult{Error: "callback request was not continued"}
		}
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	go prepareAndWatchOAuthDevToolsTab(ctx, wsURL, baseURL, clientID, resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools preparer did not connect")
	}
	defer conn.Close()

	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Runtime.evaluate":
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{
							"type":  "object",
							"value": map[string]any{"success": true, "data": state},
						},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.navigate":
				_ = conn.WriteMessage(websocket.TextMessage, testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL))
				time.Sleep(50 * time.Millisecond)
				_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-after-network", callbackURL))
			case "Page.bringToFront":
			case "Fetch.continueRequest":
				if req.Params["requestId"] != "request-after-network" {
					t.Errorf("requestId = %v", req.Params["requestId"])
				}
				close(continued)
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.AccessToken != accessToken {
			t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("callback request was blocked after Network.requestWillBeSent")
	}
}

func TestWatchOAuthDevToolsTabEmitsPausedCallbackCookiesAndAbortsBrowserRequest(t *testing.T) {
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
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	cookieDomain := testHost(t, baseURL)
	cookieValue := "state-cookie-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
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
	go watchOAuthDevToolsTab(ctx, wsURL, baseURL, callbacks, done, stop)

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
	aborted := make(chan struct{})
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.getAllCookies":
				raw, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{"cookies": []devToolsCookie{{
						Name:   "session",
						Value:  cookieValue,
						Domain: cookieDomain,
					}}},
				})
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Fetch.failRequest":
				if req.Params["requestId"] != "request-1" {
					t.Errorf("requestId = %v", req.Params["requestId"])
				}
				close(aborted)
			case "Page.close":
				return
			case "Runtime.evaluate":
				t.Errorf("paused callback path should not wait for page-context auth")
				return
			}
		}
	}()
	if err := conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-1", callbackURL)); err != nil {
		t.Fatal(err)
	}

	select {
	case callback := <-callbacks:
		if callback.CallbackURL != callbackURL {
			t.Fatalf("callbackURL = %q", callback.CallbackURL)
		}
		if !strings.Contains(callback.SessionCookies, cookieValue) {
			t.Fatalf("SessionCookies = %q, want browser state cookie", callback.SessionCookies)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not emit paused callback")
	}
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("paused browser callback request was not aborted")
	}
}

func TestPrepareAndWatchOAuthDevToolsTabContinuesCallbackAndImportsBrowserSession(t *testing.T) {
	oldTokenTimeout := oauthAccessTokenCaptureTimeout
	oauthAccessTokenCaptureTimeout = 150 * time.Millisecond
	t.Cleanup(func() { oauthAccessTokenCaptureTimeout = oldTokenTimeout })

	cookieValue := testCookieValue(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if cookie, err := r.Cookie("session"); err == nil && cookie.Value == cookieValue && r.Header.Get("New-Api-User") == "1" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"tester","quota":100,"used_quota":25,"request_count":2}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"请登录 NewAPI"}`))
	}))
	defer apiServer.Close()

	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	devToolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer devToolsServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, state := testOAuthCodeState(t)
	code := "code-" + testNameSlug(t)
	callbackURL := testOAuthCallbackURL(t, apiServer.URL, "/oauth/linuxdo", code, state)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(devToolsServer.URL, "http")
	continued := make(chan struct{})
	resolve := func(context.Context) oauthCallbackResult {
		select {
		case <-continued:
			return oauthCallbackResult{SessionCookies: testStoredCookieJSON(t, cookieValue), UserID: "1"}
		default:
			return oauthCallbackResult{Error: "browser session not ready"}
		}
	}
	go prepareAndWatchOAuthDevToolsTab(ctx, wsURL, apiServer.URL, "client-id", resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools preparer did not connect")
	}
	defer conn.Close()

	failed := make(chan struct{})
	runtimeEvaluateCount := 0
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Runtime.evaluate":
				runtimeEvaluateCount++
				if runtimeEvaluateCount == 1 {
					response := map[string]any{
						"id": req.ID,
						"result": map[string]any{
							"result": map[string]any{
								"type":  "object",
								"value": map[string]any{"success": true, "data": state},
							},
						},
					}
					raw, _ := json.Marshal(response)
					_ = conn.WriteMessage(websocket.TextMessage, raw)
					continue
				}
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"value": `{"success":false,"message":"localStorage user missing"}`},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.navigate":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
			case "Page.bringToFront":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
				_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-browser", callbackURL))
			case "Fetch.continueRequest":
				if req.Params["requestId"] != "request-browser" {
					t.Errorf("requestId = %v", req.Params["requestId"])
				}
				close(continued)
			case "Fetch.failRequest":
				close(failed)
			case "Network.getAllCookies":
				cookies := []devToolsCookie{{Name: "session", Value: cookieValue, Domain: testHost(t, apiServer.URL)}}
				raw := testDevToolsCookiesJSON(t, cookies)
				var response map[string]any
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Error(err)
					return
				}
				response["id"] = req.ID
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.close":
				return
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.CallbackURL != "" {
			t.Fatalf("browser-session strategy should import cookies without app-owned callback, got %q", callback.CallbackURL)
		}
		if !strings.Contains(callback.SessionCookies, cookieValue) {
			t.Fatalf("SessionCookies = %q, want browser session", callback.SessionCookies)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser-session watcher did not emit imported session")
	}
	select {
	case <-continued:
	case <-time.After(2 * time.Second):
		t.Fatal("browser callback request was not continued")
	}
	select {
	case <-failed:
		t.Fatal("browser-session strategy must not abort the browser callback request")
	default:
	}
}

func TestWatchOAuthImportDevToolsTabContinuesAuthorizeTabCallbackAndImportsSession(t *testing.T) {
	oldTokenTimeout := oauthAccessTokenCaptureTimeout
	oauthAccessTokenCaptureTimeout = 150 * time.Millisecond
	t.Cleanup(func() { oauthAccessTokenCaptureTimeout = oldTokenTimeout })

	cookieValue := testCookieValue(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if cookie, err := r.Cookie("session"); err == nil && cookie.Value == cookieValue && r.Header.Get("New-Api-User") == "1" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"tester","quota":100,"used_quota":25,"request_count":2}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"请登录 NewAPI"}`))
	}))
	defer apiServer.Close()

	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	devToolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer devToolsServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, apiServer.URL, "/oauth/linuxdo", code, state)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(devToolsServer.URL, "http")
	resolve := func(context.Context) oauthCallbackResult {
		return oauthCallbackResult{SessionCookies: testStoredCookieJSON(t, cookieValue), UserID: "1"}
	}
	go watchOAuthImportDevToolsTab(ctx, wsURL, apiServer.URL, state, resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools import watcher did not connect")
	}
	defer conn.Close()

	continued := make(chan struct{})
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
				if req.Method == "Fetch.enable" {
					_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-authorize-tab", callbackURL))
				}
			case "Fetch.continueRequest":
				if req.Params["requestId"] != "request-authorize-tab" {
					t.Errorf("requestId = %v", req.Params["requestId"])
				}
				close(continued)
			case "Fetch.failRequest":
				t.Errorf("authorize-tab import watcher must not abort callback")
				return
			case "Runtime.evaluate":
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"value": `{"success":false,"message":"localStorage user missing"}`},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Network.getAllCookies":
				cookies := []devToolsCookie{{Name: "session", Value: cookieValue, Domain: testHost(t, apiServer.URL)}}
				raw := testDevToolsCookiesJSON(t, cookies)
				var response map[string]any
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Error(err)
					return
				}
				response["id"] = req.ID
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.close":
				return
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.CallbackURL != "" {
			t.Fatalf("authorize-tab strategy should import cookies without app-owned callback, got %q", callback.CallbackURL)
		}
		if !strings.Contains(callback.SessionCookies, cookieValue) {
			t.Fatalf("SessionCookies = %q, want browser session", callback.SessionCookies)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorize-tab watcher did not emit imported session")
	}
	select {
	case <-continued:
	case <-time.After(2 * time.Second):
		t.Fatal("authorize-tab callback request was not continued")
	}
}

func TestWatchOAuthImportDevToolsTabImportsLocalStorageToken(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	devToolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer devToolsServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	accessToken := "token-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(devToolsServer.URL, "http")
	resolve := func(context.Context) oauthCallbackResult {
		return oauthCallbackResult{AccessToken: accessToken, UserID: "42"}
	}
	go watchOAuthImportDevToolsTab(ctx, wsURL, baseURL, state, resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools import watcher did not connect")
	}
	defer conn.Close()

	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
				if req.Method == "Fetch.enable" {
					_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-token", callbackURL))
				}
			case "Fetch.continueRequest":
			case "Runtime.evaluate":
				payload, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"token": accessToken, "userId": "42"}})
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"value": string(payload)},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Network.getAllCookies":
				t.Errorf("token path should not require cookie fallback")
				return
			case "Page.close":
				return
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.AccessToken != accessToken {
			t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
		}
		if callback.SessionCookies != "" {
			t.Fatalf("SessionCookies = %q, want empty", callback.SessionCookies)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorize-tab watcher did not emit token callback")
	}
}

func TestWatchOAuthImportDevToolsTabResolvesSessionAwayFromCallbackConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	devToolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer devToolsServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)
	accessToken := "token-" + testNameSlug(t)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}
	resolved := make(chan struct{})
	resolve := func(context.Context) oauthCallbackResult {
		close(resolved)
		return oauthCallbackResult{AccessToken: accessToken, UserID: "42"}
	}

	wsURL := "ws" + strings.TrimPrefix(devToolsServer.URL, "http")
	go watchOAuthImportDevToolsTab(ctx, wsURL, baseURL, state, resolve, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("devtools import watcher did not connect")
	}
	defer conn.Close()

	continued := make(chan struct{})
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.enable", "Page.enable", "Fetch.enable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
				if req.Method == "Fetch.enable" {
					_ = conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-token", callbackURL))
				}
			case "Fetch.continueRequest":
				close(continued)
			case "Runtime.evaluate", "Network.getAllCookies":
				t.Errorf("callback DevTools connection must not be used for browser-session import after continue; got %s", req.Method)
				return
			}
		}
	}()

	select {
	case callback := <-callbacks:
		if callback.AccessToken != accessToken {
			t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorize-tab watcher did not emit token callback")
	}
	select {
	case <-continued:
	case <-time.After(2 * time.Second):
		t.Fatal("callback request was not continued")
	}
	select {
	case <-resolved:
	case <-time.After(2 * time.Second):
		t.Fatal("browser-session resolver was not called")
	}
}

func TestPrepareAndWatchDirectAuthorizeTabEnablesFetchBeforeNavigate(t *testing.T) {
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
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	authorizeURL := testOAuthProviderURL(t, state)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	callbacks := make(chan oauthCallbackResult, 1)
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
	go prepareAndWatchDirectAuthorizeTab(ctx, wsURL, baseURL, authorizeURL, state, callbacks, done, stop)

	var conn *websocket.Conn
	select {
	case conn = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("direct authorize watcher did not connect")
	}
	defer conn.Close()

	var sawFetchBeforeNavigate bool
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "Network.enable", "Page.enable":
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
		case "Fetch.enable":
			sawFetchBeforeNavigate = true
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{}}`))
		case "Page.navigate":
			if !sawFetchBeforeNavigate {
				t.Fatal("Page.navigate happened before Fetch.enable completed")
			}
			if got, _ := req.Params["url"].(string); got != authorizeURL {
				t.Fatalf("navigate URL = %q, want %q", got, authorizeURL)
			}
			goto navigated
		default:
			t.Fatalf("unexpected devtools method before navigation %q", req.Method)
		}
	}

navigated:
	if _, raw, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	} else {
		var req struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(raw, &req) != nil || req.Method != "Page.bringToFront" {
			t.Fatalf("expected Page.bringToFront after navigate, got %s", raw)
		}
	}
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Network.getAllCookies":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":`+strconv.Itoa(req.ID)+`,"result":{"cookies":[]}}`))
			case "Fetch.failRequest", "Page.close":
				return
			}
		}
	}()
	if err := conn.WriteMessage(websocket.TextMessage, testDevToolsPausedEventJSON(t, "request-direct", callbackURL)); err != nil {
		t.Fatal(err)
	}

	select {
	case callback := <-callbacks:
		if callback.CallbackURL != callbackURL {
			t.Fatalf("callbackURL = %q, want %q", callback.CallbackURL, callbackURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct authorize watcher did not emit callback URL")
	}
}

func TestStartDefaultOAuthBrowserCaptureDoesNotStopWhenLauncherProcessExits(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json", "/json/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case "/json/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Browser":"Fake"}`))
		default:
			http.NotFound(w, r)
		}
	})}
	defer server.Close()
	go func() {
		_ = server.Serve(listener)
	}()

	fakeBrowser := filepath.Join(t.TempDir(), "fake-browser.cmd")
	if err := os.WriteFile(fakeBrowser, []byte("@echo off\r\nexit /b 0\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUOTABALL_OAUTH_BROWSER", fakeBrowser)
	t.Setenv(oauthDebugPortEnv, strconv.Itoa(port))
	t.Setenv(oauthProfileDirEnv, filepath.Join(t.TempDir(), "OAuthBrowser"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture, err := startDefaultOAuthBrowserCapture(ctx, "", testHTTPBaseURL(t), "client-id")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()

	select {
	case <-capture.Done:
		t.Fatal("launcher process exit must not be treated as OAuth browser close")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestOAuthBrowserLaunchPlanUsesIntermediatePageForBackendOwnedState(t *testing.T) {
	authorizeURL := "https://connect.linux.do/oauth2/authorize?client_id=client-direct&response_type=code&state=state-direct"
	baseURL := "https://x666.me"

	launchURL, watchClientID, expectedState, err := oauthBrowserLaunchPlan(authorizeURL, baseURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if launchURL == authorizeURL {
		t.Fatalf("direct-authorize mode must not launch the authorize URL before DevTools Fetch interception is enabled")
	}
	if !strings.HasPrefix(launchURL, "data:text/html") || !strings.Contains(launchURL, "quotaball-oauth") {
		t.Fatalf("launchURL = %q, want marked intermediate page", launchURL)
	}
	if watchClientID != "" {
		t.Fatalf("watchClientID = %q, want empty direct-authorize mode", watchClientID)
	}
	if expectedState != "state-direct" {
		t.Fatalf("expectedState = %q, want state-direct", expectedState)
	}
}

func TestOAuthBrowserLaunchPlanUsesBaseURLOnlyForExplicitBrowserStateMode(t *testing.T) {
	authorizeURL := "https://connect.linux.do/oauth2/authorize?client_id=client-direct&response_type=code&state=state-direct"
	baseURL := "https://x666.me"

	launchURL, watchClientID, expectedState, err := oauthBrowserLaunchPlan(authorizeURL, baseURL, "client-explicit")
	if err != nil {
		t.Fatal(err)
	}
	if launchURL != baseURL {
		t.Fatalf("launchURL = %q, want base URL %q", launchURL, baseURL)
	}
	if watchClientID != "client-explicit" {
		t.Fatalf("watchClientID = %q, want explicit client ID", watchClientID)
	}
	if expectedState != "state-direct" {
		t.Fatalf("expectedState = %q, want state-direct", expectedState)
	}
}

func TestBrowserOAuthStateRetriesTransientFailures(t *testing.T) {
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
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serverConn := <-connected
	defer serverConn.Close()

	_, state := testOAuthCodeState(t)
	responses := []map[string]any{
		{"success": false, "message": "temporary empty state response"},
		{"success": true, "data": state},
	}
	go func() {
		for _, payload := range responses {
			_, raw, err := serverConn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID int `json:"id"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			response := map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"result": map[string]any{
						"type":  "object",
						"value": payload,
					},
				},
			}
			raw, _ = json.Marshal(response)
			_ = serverConn.WriteMessage(websocket.TextMessage, raw)
		}
	}()

	got, err := browserOAuthState(ctx, conn, testHTTPBaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	if got != state {
		t.Fatalf("state = %q, want %q", got, state)
	}
}

func TestShouldPrepareOAuthTabOnlyMatchesBaseURLPages(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	otherBaseURL := testHTTPBaseURL(t)

	if !shouldPrepareOAuthTab(baseURL, devToolsTab{Type: "page", URL: baseURL + "/dashboard"}) {
		t.Fatal("expected base URL page to be prepared")
	}
	if shouldPrepareOAuthTab(baseURL, devToolsTab{Type: "iframe", URL: baseURL + "/frame"}) {
		t.Fatal("iframe targets should not be prepared")
	}
	if shouldPrepareOAuthTab(baseURL, devToolsTab{Type: "page", URL: otherBaseURL}) {
		t.Fatal("other hosts should not be prepared")
	}
	if shouldPrepareOAuthTab(baseURL, devToolsTab{Type: "page", URL: "devtools://devtools/bundled/devtools_app.html"}) {
		t.Fatal("DevTools pages should not be prepared")
	}
}

func TestShouldPrepareOAuthAuthorizeTabMatchesLinuxDoAuthorizePage(t *testing.T) {
	_, state := testOAuthCodeState(t)
	clientID := "client-" + testNameSlug(t)
	authorizeURL := newapi.LinuxDoAuthorizeURL(clientID, state)

	if !shouldPrepareOAuthAuthorizeTab(clientID, devToolsTab{Type: "page", URL: authorizeURL}) {
		t.Fatal("expected matching LinuxDo authorize tab to be watched")
	}
	if shouldPrepareOAuthAuthorizeTab("other-client", devToolsTab{Type: "page", URL: authorizeURL}) {
		t.Fatal("other client_id should not match")
	}
	if shouldPrepareOAuthAuthorizeTab(clientID, devToolsTab{Type: "worker", URL: authorizeURL}) {
		t.Fatal("non-page authorize targets should not match")
	}
	if shouldPrepareOAuthAuthorizeTab(clientID, devToolsTab{Type: "page", URL: "https://connect.linux.do/"}) {
		t.Fatal("non-authorize LinuxDo page should not match")
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
	cookieValue := testCookieValue(t)
	baseURL := testAuthenticatedNewAPIBaseURL(t, cookieValue)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	cookieDomain := testHost(t, baseURL)
	callbacks := make(chan oauthCallbackResult, 1)
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
	go watchOAuthDevToolsTab(ctx, wsURL, baseURL, callbacks, done, stop)

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
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Runtime.evaluate":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"id":20,"result":{"result":{"value":"{\"success\":true,\"data\":{\"id\":42}}"}}}`))
			case "Network.getAllCookies":
				response := map[string]any{
					"id": 30,
					"result": map[string]any{
						"cookies": []map[string]string{{
							"name":   "session",
							"value":  cookieValue,
							"domain": cookieDomain,
							"path":   "/",
						}},
					},
				}
				raw, _ := json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.close":
				return
			}
		}
	}()
	err := conn.WriteMessage(websocket.TextMessage, testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case callback := <-callbacks:
		if callback.CallbackURL != callbackURL {
			t.Fatalf("callbackURL = %q", callback.CallbackURL)
		}
		wantCookies := testStoredCookieJSON(t, cookieValue)
		if callback.SessionCookies != wantCookies {
			t.Fatalf("SessionCookies = %q", callback.SessionCookies)
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

func TestWatchOAuthDevToolsTabWaitsForSessionCookieAfterCallback(t *testing.T) {
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
	cookieValue := testCookieValue(t)
	baseURL := testAuthenticatedNewAPIBaseURL(t, cookieValue)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	cookieDomain := testHost(t, baseURL)
	callbacks := make(chan oauthCallbackResult, 1)
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
	go watchOAuthDevToolsTab(ctx, wsURL, baseURL, callbacks, done, stop)

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
	getCookiesCount := 0
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Runtime.evaluate":
				t.Errorf("session capture should not depend on page-context Runtime.evaluate after callback")
				return
			case "Network.getAllCookies":
				getCookiesCount++
				cookies := []map[string]string{{"name": "other", "value": "ignored", "domain": "other.test", "path": "/"}}
				if getCookiesCount >= 2 {
					cookies = append(cookies, map[string]string{
						"name":   "session",
						"value":  cookieValue,
						"domain": cookieDomain,
						"path":   "/",
					})
				}
				raw, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{"cookies": cookies}})
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.close":
				return
			}
		}
	}()
	if err := conn.WriteMessage(websocket.TextMessage, testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL)); err != nil {
		t.Fatal(err)
	}

	select {
	case callback := <-callbacks:
		if callback.SessionCookies != testStoredCookieJSON(t, cookieValue) {
			t.Fatalf("SessionCookies = %q", callback.SessionCookies)
		}
		if getCookiesCount < 2 {
			t.Fatalf("expected cookie polling, got %d Network.getAllCookies call(s)", getCookiesCount)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not wait for session cookie")
	}
}

func TestWatchOAuthDevToolsTabWaitsForAuthenticatedSessionAfterCallback(t *testing.T) {
	cookieValue := testCookieValue(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if cookie, err := r.Cookie("session"); err == nil && cookie.Value == cookieValue && r.Header.Get("New-Api-User") == "1" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"tester","quota":100,"used_quota":25,"request_count":2}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"请登录 NewAPI"}`))
	}))
	defer apiServer.Close()

	upgrader := websocket.Upgrader{}
	connected := make(chan *websocket.Conn, 1)
	devToolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connected <- conn
	}))
	defer devToolsServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseURL := apiServer.URL
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/oauth/linuxdo", code, state)
	cookieDomain := testHost(t, baseURL)
	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	stop := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}
	wsURL := "ws" + strings.TrimPrefix(devToolsServer.URL, "http")
	go watchOAuthDevToolsTab(ctx, wsURL, baseURL, callbacks, done, stop)

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
	getCookiesCount := 0
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Runtime.evaluate":
				t.Errorf("session capture should not depend on page-context Runtime.evaluate after callback")
				return
			case "Network.getAllCookies":
				getCookiesCount++
				cookies := []devToolsCookie{{Name: "theme", Value: "light", Domain: cookieDomain}}
				if getCookiesCount >= 2 {
					cookies = append(cookies, devToolsCookie{Name: "session", Value: cookieValue, Domain: cookieDomain})
				}
				raw := testDevToolsCookiesJSON(t, cookies)
				var response map[string]any
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Error(err)
					return
				}
				response["id"] = req.ID
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Page.close":
				return
			}
		}
	}()
	if err := conn.WriteMessage(websocket.TextMessage, testDevToolsEventJSON(t, "Network.requestWillBeSent", callbackURL)); err != nil {
		t.Fatal(err)
	}

	select {
	case callback := <-callbacks:
		if !strings.Contains(callback.SessionCookies, cookieValue) {
			t.Fatalf("SessionCookies = %q, want authenticated session cookie", callback.SessionCookies)
		}
		if getCookiesCount < 2 {
			t.Fatalf("expected auth validation to reject first cookie set, got %d Network.getAllCookies call(s)", getCookiesCount)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not wait for authenticated session cookie")
	}
}

func TestCloseDevToolsTabRequestsMatchingTab(t *testing.T) {
	closed := make(chan string, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/json/close/") {
			t.Fatalf("path = %q, want /json/close/<id>", r.URL.Path)
		}
		closed <- strings.TrimPrefix(r.URL.Path, "/json/close/")
		_, _ = w.Write([]byte("Target is closing"))
	})}
	defer server.Close()
	go func() {
		_ = server.Serve(listener)
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	closeDevToolsTab(&http.Client{Timeout: time.Second}, port, "tab-123")

	select {
	case got := <-closed:
		if got != "tab-123" {
			t.Fatalf("closed tab = %q, want tab-123", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closeDevToolsTab did not request tab close")
	}
}

func TestCaptureBrowserAuthFromAnyNewAPITabReadsLocalStorageToken(t *testing.T) {
	accessToken := "token-" + testNameSlug(t)
	baseURL := testHTTPBaseURL(t)

	upgrader := websocket.Upgrader{}
	wsConnected := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		wsConnected <- conn
	}))
	defer wsServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	devToolsServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" && r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		raw := testDevToolsTabsJSON(t, []devToolsTab{
			{ID: "callback", Type: "page", URL: baseURL + "/api/oauth/linuxdo?code=busy&state=busy", WebSocketDebuggerURL: wsURL + "/busy"},
			{ID: "main", Type: "page", URL: baseURL + "/", WebSocketDebuggerURL: wsURL + "/main"},
		})
		_, _ = w.Write(raw)
	})}
	defer devToolsServer.Close()
	go func() {
		_ = devToolsServer.Serve(listener)
	}()

	go func() {
		conn := <-wsConnected
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Runtime.evaluate":
				payload, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"token": accessToken, "userId": "42"}})
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"value": string(payload)},
					},
				}
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			default:
				t.Errorf("unexpected devtools method %q", req.Method)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	port := listener.Addr().(*net.TCPAddr).Port
	callback := captureBrowserAuthFromAnyNewAPITab(ctx, port, baseURL)
	if callback.AccessToken != accessToken {
		t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
	}
	if callback.SessionCookies != "" {
		t.Fatalf("SessionCookies = %q, want empty", callback.SessionCookies)
	}
}

func TestCaptureBrowserAuthFromAnyNewAPITabReadsCallbackNewAPITab(t *testing.T) {
	accessToken := "token-" + testNameSlug(t)
	baseURL := testHTTPBaseURL(t)
	code, state := testOAuthCodeState(t)
	callbackURL := testOAuthCallbackURL(t, baseURL, "/api/oauth/linuxdo", code, state)

	upgrader := websocket.Upgrader{}
	wsConnected := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		wsConnected <- conn
	}))
	defer wsServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	devToolsServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" && r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		raw := testDevToolsTabsJSON(t, []devToolsTab{
			{ID: "callback", Type: "page", URL: callbackURL, WebSocketDebuggerURL: wsURL + "/callback"},
		})
		_, _ = w.Write(raw)
	})}
	defer devToolsServer.Close()
	go func() {
		_ = devToolsServer.Serve(listener)
	}()

	go func() {
		conn := <-wsConnected
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			if req.Method != "Runtime.evaluate" {
				t.Errorf("unexpected devtools method %q", req.Method)
				return
			}
			payload, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"token": accessToken, "userId": "42"}})
			response := map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"result": map[string]any{"value": string(payload)},
				},
			}
			raw, _ = json.Marshal(response)
			_ = conn.WriteMessage(websocket.TextMessage, raw)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	port := listener.Addr().(*net.TCPAddr).Port
	callback := captureBrowserAuthFromAnyNewAPITab(ctx, port, baseURL)
	if callback.AccessToken != accessToken {
		t.Fatalf("AccessToken = %q, want %q", callback.AccessToken, accessToken)
	}
}

func TestCaptureBrowserAuthFromAnyNewAPITabReadsSessionCookiesWithUserID(t *testing.T) {
	cookieValue := testCookieValue(t)
	baseURL := testAuthenticatedNewAPIBaseURL(t, cookieValue)

	upgrader := websocket.Upgrader{}
	wsConnected := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		wsConnected <- conn
	}))
	defer wsServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	devToolsServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" && r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		raw := testDevToolsTabsJSON(t, []devToolsTab{
			{ID: "token", Type: "page", URL: baseURL + "/console/token", WebSocketDebuggerURL: wsURL + "/token"},
		})
		_, _ = w.Write(raw)
	})}
	defer devToolsServer.Close()
	go func() {
		_ = devToolsServer.Serve(listener)
	}()

	go func() {
		conn := <-wsConnected
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			switch req.Method {
			case "Runtime.evaluate":
				payload, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"userId": "1"}})
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{"value": string(payload)},
					},
				}
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			case "Network.getAllCookies":
				response := map[string]any{
					"id": req.ID,
					"result": map[string]any{"cookies": []devToolsCookie{
						{Name: "session", Value: cookieValue, Domain: testHost(t, baseURL)},
					}},
				}
				raw, _ = json.Marshal(response)
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			default:
				t.Errorf("unexpected devtools method %q", req.Method)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	port := listener.Addr().(*net.TCPAddr).Port
	callback := captureBrowserAuthFromAnyNewAPITab(ctx, port, baseURL)
	if callback.SessionCookies == "" {
		t.Fatalf("SessionCookies is empty, callback=%#v", callback)
	}
	if callback.AccessToken != "" {
		t.Fatalf("AccessToken = %q, want empty", callback.AccessToken)
	}
	if callback.UserID != "1" {
		t.Fatalf("UserID = %q, want 1", callback.UserID)
	}
}

func TestOAuthCallbackHasCredentialRequiresUserIDForBrowserAuth(t *testing.T) {
	if oauthCallbackHasCredential(oauthCallbackResult{AccessToken: "token"}) {
		t.Fatal("access token callback without user id must not be treated as completed")
	}
	if !oauthCallbackHasCredential(oauthCallbackResult{AccessToken: "token", UserID: "42"}) {
		t.Fatal("access token plus user id must be treated as a completed OAuth callback")
	}
	if !oauthCallbackHasCredential(oauthCallbackResult{SessionCookies: "cookies", UserID: "42"}) {
		t.Fatal("session cookies plus user id must be treated as a completed OAuth callback")
	}
	if !oauthCallbackHasCredential(oauthCallbackResult{CallbackURL: "https://example.test/oauth/linuxdo?code=a&state=b"}) {
		t.Fatal("callback URL must still be treated as completed for app-owned OAuth state")
	}
	if oauthCallbackHasCredential(oauthCallbackResult{}) {
		t.Fatal("empty callback must not be treated as completed")
	}
}

func TestSessionCookiesFromDevToolsFiltersBaseDomain(t *testing.T) {
	baseURL := testHTTPBaseURL(t)
	cookieValue := testCookieValue(t)
	raw := testDevToolsCookiesJSON(t, []devToolsCookie{
		{Name: "session", Value: cookieValue, Domain: testHost(t, baseURL)},
		{Name: "other", Value: "ignored", Domain: "other.test"},
	})

	cookies, err := sessionCookiesFromDevTools(raw, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if cookies != testStoredCookieJSON(t, cookieValue) {
		t.Fatalf("cookies = %s", cookies)
	}
}

func TestDebugPortFromProcessListFindsOAuthProfilePort(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "OAuthBrowser")
	_, state := testOAuthCodeState(t)
	raw := `
CommandLine="C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --new-window --remote-debugging-address=127.0.0.1 --remote-debugging-port=60037 --user-data-dir="` + profileDir + `" https://connect.linux.do/oauth2/authorize?state=` + state + `
CommandLine="C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222 --user-data-dir="C:\Users\kites\AppData\Local\OtherProfile"
`

	port := debugPortFromProcessList(profileDir, raw)
	if port != 60037 {
		t.Fatalf("port = %d, want 60037", port)
	}
}

func TestRecoverOAuthPanicDoesNotPropagate(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("recoverOAuthPanic should recover panic, got %v", recovered)
		}
	}()
	func() {
		defer recoverOAuthPanic("test")
		panic("boom")
	}()
}

func TestSanitizeOAuthLogMessageRedactsSensitiveQueryValues(t *testing.T) {
	code, state := testOAuthCodeState(t)
	clientID := "client-" + testNameSlug(t)
	got := sanitizeOAuthLogMessage("https://connect.linux.do/oauth2/authorize?client_id=" + clientID + "&state=" + state + "&code=" + code)
	for _, secret := range []string{clientID, state, code} {
		if strings.Contains(got, secret) {
			t.Fatalf("log message leaked %q in %q", secret, got)
		}
	}
	for _, marker := range []string{"client_id=<redacted>", "state=<redacted>", "code=<redacted>"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("log message missing redaction marker %q in %q", marker, got)
		}
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

func testHTTPBaseURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	return server.URL
}

func testAuthenticatedNewAPIBaseURL(t *testing.T, cookieValue string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if cookie, err := r.Cookie("session"); err == nil && cookie.Value == cookieValue {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"tester","quota":100,"used_quota":25,"request_count":2}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"请登录 NewAPI"}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func testOAuthCodeState(t *testing.T) (string, string) {
	t.Helper()
	suffix := testNameSlug(t)
	return "code-" + suffix, "state-" + suffix
}

func testCookieValue(t *testing.T) string {
	t.Helper()
	return "session-" + testNameSlug(t)
}

func testOAuthCallbackURL(t *testing.T, baseURL, path, code, state string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = path
	q := u.Query()
	q.Set("code", code)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

func testOAuthProviderURL(t *testing.T, state string) string {
	t.Helper()
	u, err := url.Parse(testHTTPBaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/oauth2/authorize"
	q := u.Query()
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

func testHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func testDevToolsTabsJSON(t *testing.T, tabs []devToolsTab) []byte {
	t.Helper()
	raw, err := json.Marshal(tabs)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testDevToolsEventJSON(t *testing.T, method, rawURL string) []byte {
	t.Helper()
	payload := map[string]any{"method": method}
	if method == "Page.frameNavigated" {
		payload["params"] = map[string]any{"frame": map[string]string{"url": rawURL}}
	} else {
		payload["params"] = map[string]any{"request": map[string]string{"url": rawURL}}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testDevToolsPausedEventJSON(t *testing.T, requestID, rawURL string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"method": "Fetch.requestPaused",
		"params": map[string]any{
			"requestId": requestID,
			"request":   map[string]string{"url": rawURL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testDevToolsCookiesJSON(t *testing.T, cookies []devToolsCookie) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id":     9,
		"result": map[string]any{"cookies": cookies},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testStoredCookieJSON(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal([]storedBrowserCookie{{Name: "session", Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func testNameSlug(t *testing.T) string {
	t.Helper()
	return strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
}
