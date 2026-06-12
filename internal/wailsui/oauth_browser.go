package wailsui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"krill_monitor/internal/newapi"
)

var startOAuthBrowserCapture = startDefaultOAuthBrowserCapture

const oauthProfileDirEnv = "QUOTABALL_OAUTH_PROFILE_DIR"
const oauthDebugPortEnv = "QUOTABALL_OAUTH_DEBUG_PORT"
const oauthLogFileEnv = "QUOTABALL_OAUTH_LOG_FILE"
const defaultOAuthDebugPort = 27183
const oauthDevToolsStartupTimeout = 12 * time.Second
const oauthDevToolsDisconnectTimeout = 4 * time.Second

var oauthAccessTokenCaptureTimeout = 12 * time.Second
var oauthSessionCookieCaptureTimeout = 90 * time.Second
var oauthBrowserAuthCaptureTimeout = 90 * time.Second

var debugPortPattern = regexp.MustCompile(`--remote-debugging-port=(\d+)`)
var oauthSensitiveQueryPattern = regexp.MustCompile(`(?i)(code|state|client_id|access_token|token)=([^&\s]+)`)
var oauthDirectLaunchMarkerPattern = regexp.MustCompile(`quotaball-oauth-[a-z0-9]+`)

type oauthCapture struct {
	Callbacks <-chan oauthCallbackResult
	Done      <-chan struct{}
	close     func()
}

type oauthCallbackResult struct {
	CallbackURL    string
	SessionCookies string
	AccessToken    string
	UserID         string
	Error          string
}

type browserLocalAuth struct {
	AccessToken string
	UserID      string
}

type oauthCompletionMode int

const (
	oauthCompletionIntercept oauthCompletionMode = iota
	oauthCompletionImportBrowserSession
)

type oauthBrowserAuthResolver func(context.Context) oauthCallbackResult

func (c *oauthCapture) Close() {
	if c != nil && c.close != nil {
		c.close()
	}
}

type devToolsTab struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func startDefaultOAuthBrowserCapture(ctx context.Context, authorizeURL, baseURL, clientID string) (capture *oauthCapture, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			oauthLogf("oauth browser start panic: %v", recovered)
			capture = nil
			err = errors.New("自动登录浏览器启动异常，请重试")
		}
	}()
	browser, err := findOAuthBrowser()
	if err != nil {
		return nil, err
	}
	profileDir, err := oauthBrowserProfileDir()
	if err != nil {
		return nil, err
	}
	launchURL, watchClientID, expectedState, err := oauthBrowserLaunchPlan(authorizeURL, baseURL, clientID)
	if err != nil {
		return nil, err
	}
	oauthLogf("oauth browser capture starting mode=%s base=%s", oauthBrowserCaptureMode(watchClientID), baseURL)
	port := runningOAuthBrowserDebugPort(profileDir)
	if port == 0 {
		port, err = oauthBrowserDebugPort()
		if err != nil {
			return nil, err
		}
	}

	callbacks := make(chan oauthCallbackResult, 1)
	done := make(chan struct{})
	var cmd *exec.Cmd
	args := []string{
		"--new-window",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--remote-allow-origins=*",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		launchURL,
	}
	cmd = exec.CommandContext(ctx, browser, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("无法启动自动登录浏览器：%w", err)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}
	go func() {
		defer recoverOAuthPanic("oauth browser wait")
		_ = cmd.Wait()
	}()
	go func() {
		defer recoverOAuthPanic("oauth browser poll")
		pollOAuthBrowser(ctx, port, baseURL, launchURL, authorizeURL, watchClientID, expectedState, callbacks, done, stop)
	}()

	return &oauthCapture{
		Callbacks: callbacks,
		Done:      done,
		close:     stop,
	}, nil
}

func recoverOAuthPanic(scope string) {
	if recovered := recover(); recovered != nil {
		oauthLogf("%s panic: %v", scope, recovered)
	}
}

func oauthLogf(format string, args ...any) {
	message := sanitizeOAuthLogMessage(fmt.Sprintf(format, args...))
	path := strings.TrimSpace(os.Getenv(oauthLogFileEnv))
	if path == "" {
		root, err := os.UserCacheDir()
		if err != nil || strings.TrimSpace(root) == "" {
			root = os.TempDir()
		}
		path = filepath.Join(root, "QuotaBall", "oauth.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), message)
}

func sanitizeOAuthLogMessage(message string) string {
	return oauthSensitiveQueryPattern.ReplaceAllString(message, `$1=<redacted>`)
}

func oauthBrowserDebugPort() (int, error) {
	if override := strings.TrimSpace(os.Getenv(oauthDebugPortEnv)); override != "" {
		port, err := strconv.Atoi(override)
		if err != nil || port <= 0 || port > 65535 {
			return 0, errors.New("自动登录浏览器调试端口无效")
		}
		return port, nil
	}
	if loopbackPortAvailable(defaultOAuthDebugPort) || devToolsPortActive(defaultOAuthDebugPort) {
		return defaultOAuthDebugPort, nil
	}
	return freeLoopbackPort()
}

func oauthBrowserProfileDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(oauthProfileDirEnv)); override != "" {
		dir := filepath.Clean(override)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("无法创建自动登录浏览器资料目录：%w", err)
		}
		return dir, nil
	}

	root, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(root) == "" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "QuotaBall", "OAuthBrowser")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("无法创建自动登录浏览器资料目录：%w", err)
	}
	return dir, nil
}

func runningOAuthBrowserDebugPort(profileDir string) int {
	if runtime.GOOS != "windows" {
		return 0
	}
	out, err := exec.Command("wmic", "process", "where", "name='msedge.exe' or name='chrome.exe'", "get", "CommandLine", "/value").Output()
	if err == nil {
		if port := debugPortFromProcessList(profileDir, string(out)); port != 0 {
			return port
		}
	}
	ps := "Get-CimInstance Win32_Process -Filter \"name='msedge.exe' or name='chrome.exe'\" | ForEach-Object { $_.CommandLine }"
	out, err = exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return 0
	}
	return debugPortFromProcessList(profileDir, string(out))
}

func debugPortFromProcessList(profileDir, raw string) int {
	profile := normalizeProfilePath(profileDir)
	if profile == "" {
		return 0
	}
	for _, line := range strings.Split(raw, "\n") {
		normalized := normalizeCommandLine(line)
		if !strings.Contains(normalized, profile) {
			continue
		}
		match := debugPortPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		port, err := strconv.Atoi(match[1])
		if err == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	return 0
}

func normalizeProfilePath(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, `/`, `\`)
	value = filepath.Clean(value)
	if value == "." {
		return ""
	}
	return value
}

func normalizeCommandLine(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, `/`, `\`)
	return value
}

func oauthBrowserLaunchPlan(authorizeURL, baseURL, clientID string) (launchURL, watchClientID, expectedState string, err error) {
	watchClientID = strings.TrimSpace(clientID)
	authorizeURL = strings.TrimSpace(authorizeURL)
	baseURL = strings.TrimSpace(baseURL)
	expectedState = stateFromAuthorizeURL(authorizeURL)
	if watchClientID != "" {
		launchURL = baseURL
	} else {
		if authorizeURL == "" {
			return "", "", "", errors.New("自动登录浏览器缺少授权地址")
		}
		launchURL = oauthDirectLaunchURL()
	}
	if strings.TrimSpace(launchURL) == "" {
		return "", "", "", errors.New("自动登录浏览器缺少启动地址")
	}
	return launchURL, watchClientID, expectedState, nil
}

func oauthDirectLaunchURL() string {
	marker := "quotaball-oauth-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	html := `<!doctype html><meta charset="utf-8"><title>QuotaBall OAuth</title><meta name="quotaball-oauth" content="` + marker + `">`
	return "data:text/html;charset=utf-8," + url.PathEscape(html)
}

func oauthBrowserCaptureMode(clientID string) string {
	if strings.TrimSpace(clientID) != "" {
		return "browser-state"
	}
	return "direct-authorize"
}

func stateFromAuthorizeURL(authorizeURL string) string {
	u, err := url.Parse(strings.TrimSpace(authorizeURL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("state"))
}

func pollOAuthBrowser(ctx context.Context, port int, baseURL string, launchURL string, authorizeURL string, clientID string, expectedState string, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth poll")
	client := &http.Client{Timeout: 2 * time.Second}
	watcherCtx, cancelWatchers := context.WithCancel(ctx)
	defer cancelWatchers()
	resolveBrowserAuth := func(resolveCtx context.Context) oauthCallbackResult {
		return captureBrowserAuthFromAnyNewAPITab(resolveCtx, port, baseURL)
	}
	watchedTabs := map[string]struct{}{}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	startedAt := time.Now()
	var lastDevToolsSeen time.Time
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-done:
			return
		case <-ticker.C:
			raw, err := fetchDevToolsTabs(client, port)
			if err != nil {
				if lastDevToolsSeen.IsZero() && time.Since(startedAt) > oauthDevToolsStartupTimeout {
					oauthLogf("oauth browser devtools did not start on port %d: %v", port, err)
					stop()
					return
				}
				if !lastDevToolsSeen.IsZero() && time.Since(lastDevToolsSeen) > oauthDevToolsDisconnectTimeout {
					oauthLogf("oauth browser devtools disconnected on port %d: %v", port, err)
					stop()
					return
				}
				continue
			}
			lastDevToolsSeen = time.Now()
			callbackURL, tabID, ok := oauthCallbackFromDevToolsTabsForState(baseURL, raw, expectedState)
			if !ok {
				for _, tab := range devToolsTabs(raw) {
					if strings.TrimSpace(tab.ID) == "" || strings.TrimSpace(tab.WebSocketDebuggerURL) == "" {
						continue
					}
					watchKind := ""
					watchState := ""
					switch {
					case clientID != "" && shouldPrepareOAuthTab(baseURL, tab):
						watchKind = "newapi"
					case clientID != "" && shouldPrepareOAuthAuthorizeTab(clientID, tab):
						watchKind = "authorize"
						watchState = stateFromAuthorizeURL(tab.URL)
					case clientID == "" && shouldPrepareDirectAuthorizeTab(tab, launchURL):
						watchKind = "direct"
					default:
						continue
					}
					if _, exists := watchedTabs[tab.ID]; exists {
						continue
					}
					watchedTabs[tab.ID] = struct{}{}
					oauthLogf("oauth browser watching %s tab id=%s url=%s", watchKind, tab.ID, tab.URL)
					go func(websocketURL string, kind string, state string) {
						defer recoverOAuthPanic("oauth devtools watcher")
						switch kind {
						case "newapi":
							prepareAndWatchOAuthDevToolsTab(watcherCtx, websocketURL, baseURL, clientID, resolveBrowserAuth, callbacks, done, stop)
							return
						case "authorize":
							watchOAuthImportDevToolsTab(watcherCtx, websocketURL, baseURL, state, resolveBrowserAuth, callbacks, done, stop)
							return
						case "direct":
							prepareAndWatchDirectAuthorizeTab(watcherCtx, websocketURL, baseURL, authorizeURL, expectedState, callbacks, done, stop)
							return
						}
					}(tab.WebSocketDebuggerURL, watchKind, watchState)
				}
				continue
			}
			if clientID != "" {
				continue
			}
			closeDevToolsTab(client, port, tabID)
			emitOAuthCallback(oauthCallbackResult{CallbackURL: callbackURL}, callbacks, done, stop)
			return
		}
	}
}

func prepareAndWatchDirectAuthorizeTab(ctx context.Context, websocketURL, baseURL, authorizeURL, expectedState string, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth devtools direct")
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := cdpRequest(ctx, conn, 1, "Network.enable", nil, 2*time.Second); err != nil {
		return
	}
	if _, err := cdpRequest(ctx, conn, 2, "Page.enable", nil, 2*time.Second); err != nil {
		return
	}
	if _, err := cdpRequest(ctx, conn, 6, "Fetch.enable", map[string]any{
		"patterns": oauthCallbackFetchPatterns(baseURL),
	}, 2*time.Second); err != nil {
		return
	}
	_ = conn.WriteJSON(map[string]any{
		"id":     4,
		"method": "Page.navigate",
		"params": map[string]any{"url": authorizeURL},
	})
	_ = conn.WriteJSON(map[string]any{"id": 5, "method": "Page.bringToFront"})
	watchOAuthDevToolsConn(ctx, conn, baseURL, expectedState, oauthCompletionIntercept, nil, callbacks, done, stop)
}

func watchOAuthDevToolsTab(ctx context.Context, websocketURL, baseURL string, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	watchOAuthDevToolsTabForState(ctx, websocketURL, baseURL, "", callbacks, done, stop)
}

func watchOAuthDevToolsTabForState(ctx context.Context, websocketURL, baseURL, expectedState string, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth devtools")
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"id": 1, "method": "Network.enable"})
	_ = conn.WriteJSON(map[string]any{"id": 2, "method": "Page.enable"})
	_ = conn.WriteJSON(map[string]any{
		"id":     6,
		"method": "Fetch.enable",
		"params": map[string]any{
			"patterns": oauthCallbackFetchPatterns(baseURL),
		},
	})
	watchOAuthDevToolsConn(ctx, conn, baseURL, expectedState, oauthCompletionIntercept, nil, callbacks, done, stop)
}

func watchOAuthImportDevToolsTab(ctx context.Context, websocketURL, baseURL, expectedState string, resolveBrowserAuth oauthBrowserAuthResolver, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth devtools import")
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		oauthLogf("oauth import watcher dial failed: %v", err)
		return
	}
	defer conn.Close()
	if _, err := cdpRequest(ctx, conn, 1, "Network.enable", nil, 2*time.Second); err != nil {
		oauthLogf("oauth import watcher network enable failed: %v", err)
		return
	}
	if _, err := cdpRequest(ctx, conn, 2, "Page.enable", nil, 2*time.Second); err != nil {
		oauthLogf("oauth import watcher page enable failed: %v", err)
		return
	}
	if _, err := cdpRequest(ctx, conn, 6, "Fetch.enable", map[string]any{
		"patterns": oauthCallbackFetchPatterns(baseURL),
	}, 2*time.Second); err != nil {
		oauthLogf("oauth import watcher fetch enable failed: %v", err)
		return
	}
	oauthLogf("oauth import watcher ready state=%s", expectedState)
	watchOAuthDevToolsConn(ctx, conn, baseURL, expectedState, oauthCompletionImportBrowserSession, resolveBrowserAuth, callbacks, done, stop)
}

func prepareAndWatchOAuthDevToolsTab(ctx context.Context, websocketURL, baseURL, clientID string, resolveBrowserAuth oauthBrowserAuthResolver, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth devtools prepare")
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = cdpRequest(ctx, conn, 1, "Network.enable", nil, 2*time.Second)
	_, _ = cdpRequest(ctx, conn, 2, "Page.enable", nil, 2*time.Second)
	_, _ = cdpRequest(ctx, conn, 6, "Fetch.enable", map[string]any{
		"patterns": oauthCallbackFetchPatterns(baseURL),
	}, 2*time.Second)
	if resolveBrowserAuth != nil {
		quickCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		callback := resolveBrowserAuth(quickCtx)
		cancel()
		if oauthCallbackHasCredential(callback) {
			oauthLogf("browser auth captured before starting OAuth")
			emitOAuthCallback(callback, callbacks, done, stop)
			return
		}
	}
	state, err := browserOAuthState(ctx, conn, baseURL)
	if err != nil || strings.TrimSpace(state) == "" {
		oauthLogf("browser oauth state failed: %v", err)
		return
	}
	oauthLogf("browser oauth state ready state=%s", state)
	_ = conn.WriteJSON(map[string]any{
		"id":     4,
		"method": "Page.navigate",
		"params": map[string]any{"url": newapi.LinuxDoAuthorizeURL(clientID, state)},
	})
	_ = conn.WriteJSON(map[string]any{"id": 5, "method": "Page.bringToFront"})
	watchOAuthDevToolsConn(ctx, conn, baseURL, state, oauthCompletionImportBrowserSession, resolveBrowserAuth, callbacks, done, stop)
}

func watchOAuthDevToolsConn(ctx context.Context, conn *websocket.Conn, baseURL string, expectedState string, mode oauthCompletionMode, resolveBrowserAuth oauthBrowserAuthResolver, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	stopReader := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
			_ = conn.Close()
		case <-stopReader:
		}
	}()
	defer close(stopReader)
	var resolveOnce sync.Once
	startResolvedOAuthBrowserSession := func(delay time.Duration) {
		resolveOnce.Do(func() {
			go func() {
				defer recoverOAuthPanic("oauth browser session resolver")
				if delay > 0 {
					select {
					case <-ctx.Done():
						return
					case <-done:
						return
					case <-time.After(delay):
					}
				}
				emitResolvedOAuthBrowserSession(ctx, resolveBrowserAuth, callbacks, done, stop)
			}()
		})
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		default:
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if callbackURL, requestID, ok := pausedOAuthCallbackFromDevToolsEventForState(baseURL, raw, expectedState); ok {
			if mode == oauthCompletionImportBrowserSession {
				oauthLogf("oauth browser callback paused; continuing browser completion")
				continuePausedOAuthRequest(conn, requestID)
				startResolvedOAuthBrowserSession(700 * time.Millisecond)
				return
			}
			sessionCookies := captureDevToolsCallbackCookies(ctx, conn, baseURL)
			abortPausedOAuthRequest(conn, requestID)
			_ = conn.WriteJSON(map[string]any{"id": 3, "method": "Page.close"})
			emitOAuthCallback(oauthCallbackResult{CallbackURL: callbackURL, SessionCookies: sessionCookies}, callbacks, done, stop)
			return
		}
		callbackURL, ok := oauthCallbackFromDevToolsEventForState(baseURL, raw, expectedState)
		if !ok {
			continue
		}
		if mode == oauthCompletionImportBrowserSession {
			oauthLogf("oauth browser callback observed; waiting for callback request to complete")
			startResolvedOAuthBrowserSession(1200 * time.Millisecond)
			continue
		}
		sessionCookies := captureDevToolsSessionCookies(ctx, conn, baseURL)
		_ = conn.WriteJSON(map[string]any{"id": 3, "method": "Page.close"})
		emitOAuthCallback(oauthCallbackResult{CallbackURL: callbackURL, SessionCookies: sessionCookies}, callbacks, done, stop)
		return
	}
}

func emitResolvedOAuthBrowserSession(ctx context.Context, resolveBrowserAuth oauthBrowserAuthResolver, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	if resolveBrowserAuth == nil {
		emitOAuthFailure("NewAPI 已完成授权，但自动登录缺少浏览器会话读取器，请重试", callbacks, done)
		return
	}
	callback := resolveBrowserAuth(ctx)
	if oauthCallbackHasCredential(callback) {
		emitOAuthCallback(callback, callbacks, done, stop)
		return
	}
	message := strings.TrimSpace(callback.Error)
	if message == "" {
		message = "NewAPI 已完成授权，但未捕获到登录态；请确认授权页跳回 NewAPI 后已登录，再重新打开 LinuxDo 登录页"
	}
	oauthLogf("oauth browser callback observed but session import failed: %s", message)
	emitOAuthFailure(message, callbacks, done)
}

func oauthCallbackHasCredential(callback oauthCallbackResult) bool {
	return strings.TrimSpace(callback.CallbackURL) != "" ||
		(strings.TrimSpace(callback.SessionCookies) != "" && strings.TrimSpace(callback.UserID) != "") ||
		(strings.TrimSpace(callback.AccessToken) != "" && strings.TrimSpace(callback.UserID) != "")
}

func browserOAuthState(ctx context.Context, conn *websocket.Conn, baseURL string) (string, error) {
	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for {
		state, err := browserOAuthStateOnce(ctx, conn, baseURL)
		if err == nil {
			return state, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(350 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("NewAPI 未返回 OAuth state")
}

func browserOAuthStateOnce(ctx context.Context, conn *websocket.Conn, baseURL string) (string, error) {
	logoutURL := strings.TrimRight(baseURL, "/") + "/api/user/logout"
	stateURL := strings.TrimRight(baseURL, "/") + "/api/oauth/state"
	expr := fmt.Sprintf(`(async () => {
		try { await fetch(%q, { credentials: "include", headers: { "Accept": "application/json" } }); } catch (_) {}
		const res = await fetch(%q, { credentials: "include", headers: { "Accept": "application/json" } });
		const text = await res.text();
		try {
			return JSON.stringify(JSON.parse(text));
		} catch (_) {
			return JSON.stringify({ success: false, message: "OAuth state response invalid, HTTP " + res.status });
		}
	})()`, logoutURL, stateURL)
	raw, err := cdpRequest(ctx, conn, 3, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"awaitPromise":  true,
		"returnByValue": true,
	}, 5*time.Second)
	if err != nil {
		return "", err
	}
	value, err := cdpJSONValue(raw)
	if err != nil {
		return "", err
	}
	var api struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(value, &api); err != nil {
		return "", err
	}
	if !api.Success || strings.TrimSpace(api.Data) == "" {
		msg := strings.TrimSpace(api.Message)
		if msg == "" {
			msg = "NewAPI 未返回 OAuth state"
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(api.Data), nil
}

func captureDevToolsAccessToken(ctx context.Context, conn *websocket.Conn, baseURL string) string {
	deadline := time.Now().Add(oauthAccessTokenCaptureTimeout)
	var lastErr error
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		auth, err := captureDevToolsLocalAuthOnce(ctx, conn, baseURL, 80+attempt)
		if err == nil && strings.TrimSpace(auth.AccessToken) != "" {
			oauthLogf("browser access token captured from local storage")
			return auth.AccessToken
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(350 * time.Millisecond):
		}
	}
	if lastErr != nil {
		oauthLogf("browser access token capture timed out: %v", lastErr)
	}
	return ""
}

func captureDevToolsAccessTokenOnce(ctx context.Context, conn *websocket.Conn, baseURL string, requestID int) (string, error) {
	auth, err := captureDevToolsLocalAuthOnce(ctx, conn, baseURL, requestID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(auth.AccessToken) == "" {
		return "", errors.New("NewAPI localStorage token missing")
	}
	return auth.AccessToken, nil
}

func captureDevToolsLocalAuthOnce(ctx context.Context, conn *websocket.Conn, baseURL string, requestID int) (browserLocalAuth, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return browserLocalAuth{}, err
	}
	origin := base.Scheme + "://" + base.Host
	expr := fmt.Sprintf(`(() => {
		if (location.origin !== %q) {
			return JSON.stringify({ success: false, message: "waiting for NewAPI origin: " + location.origin });
		}
		const out = { token: "", userId: "" };
		const uid = localStorage.getItem("uid");
		if (uid && String(uid).trim()) {
			out.userId = String(uid).trim();
		}
		const raw = localStorage.getItem("user");
		try {
			const user = raw ? JSON.parse(raw) : null;
			if (!out.userId && user && user.id != null) {
				out.userId = String(user.id).trim();
			}
			if (user && typeof user.token === "string" && user.token.trim()) {
				out.token = user.token.trim();
			}
		} catch (_) {
			return JSON.stringify({ success: false, message: "localStorage user invalid" });
		}
		if (!/^[1-9][0-9]*$/.test(out.userId)) {
			return JSON.stringify({ success: false, message: "localStorage user id missing" });
		}
		return JSON.stringify({ success: true, data: out });
	})()`, origin)
	raw, err := cdpRequest(ctx, conn, requestID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	}, 2*time.Second)
	if err != nil {
		return browserLocalAuth{}, err
	}
	value, err := cdpJSONValue(raw)
	if err != nil {
		return browserLocalAuth{}, err
	}
	var api struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Token  string `json:"token"`
			UserID string `json:"userId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(value, &api); err != nil {
		return browserLocalAuth{}, err
	}
	userID := cleanBrowserUserID(api.Data.UserID)
	if !api.Success || userID == "" {
		msg := strings.TrimSpace(api.Message)
		if msg == "" {
			msg = "NewAPI localStorage user id missing"
		}
		return browserLocalAuth{}, errors.New(msg)
	}
	return browserLocalAuth{AccessToken: strings.TrimSpace(api.Data.Token), UserID: userID}, nil
}

func captureDevToolsSessionCookies(ctx context.Context, conn *websocket.Conn, baseURL string) string {
	deadline := time.Now().Add(oauthSessionCookieCaptureTimeout)
	var lastErr error
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		raw, err := cdpRequest(ctx, conn, 30+attempt, "Network.getAllCookies", nil, 2*time.Second)
		if err == nil {
			cookies, cookieErr := sessionCookiesFromDevTools(raw, baseURL)
			if cookieErr == nil && strings.TrimSpace(cookies) != "" {
				return cookies
			}
			err = cookieErr
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(350 * time.Millisecond):
		}
	}
	if lastErr != nil {
		oauthLogf("browser session cookie capture timed out: %v", lastErr)
	}
	return ""
}

func captureBrowserAuthFromAnyNewAPITab(ctx context.Context, port int, baseURL string) oauthCallbackResult {
	if port <= 0 {
		return oauthCallbackResult{Error: "NewAPI 已完成授权，但浏览器调试端口无效，请重试"}
	}
	deadline := time.Now().Add(oauthBrowserAuthCaptureTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	var lastTabs string
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		callback, tabs, err := captureBrowserAuthFromAnyNewAPITabOnce(ctx, client, port, baseURL, attempt)
		if strings.TrimSpace(tabs) != "" {
			lastTabs = tabs
		}
		if oauthCallbackHasCredential(callback) {
			return callback
		}
		if err != nil {
			lastErr = err
		} else if strings.TrimSpace(callback.Error) != "" {
			lastErr = errors.New(callback.Error)
		}
		select {
		case <-ctx.Done():
			return oauthCallbackResult{Error: "NewAPI 自动登录已取消"}
		case <-time.After(350 * time.Millisecond):
		}
	}
	if lastErr != nil {
		oauthLogf("browser auth capture from stable tab timed out: %v", lastErr)
	}
	if strings.TrimSpace(lastTabs) != "" {
		oauthLogf("browser auth capture tabs snapshot: %s", lastTabs)
	}
	return oauthCallbackResult{Error: "NewAPI 已完成授权，但未捕获到登录态；请确认授权页跳回 NewAPI 后已登录，再重新打开 LinuxDo 登录页"}
}

func captureBrowserAuthFromAnyNewAPITabOnce(ctx context.Context, client *http.Client, port int, baseURL string, attempt int) (oauthCallbackResult, string, error) {
	raw, err := fetchDevToolsTabs(client, port)
	if err != nil {
		return oauthCallbackResult{}, "", err
	}
	tabs := devToolsTabs(raw)
	summary := devToolsTabsSummary(tabs)
	var lastErr error
	sawReadableTab := false
	for _, tab := range tabs {
		if !shouldReadBrowserAuthFromTab(tab) {
			continue
		}
		sawReadableTab = true
		if shouldCaptureBrowserAuthFromTab(baseURL, tab) {
			callback, err := captureBrowserAuthFromTab(ctx, tab.WebSocketDebuggerURL, baseURL, attempt)
			if oauthCallbackHasCredential(callback) {
				return callback, summary, nil
			}
			if err != nil {
				lastErr = err
			}
			continue
		}
		callback, err := captureBrowserSessionCookiesFromTab(ctx, tab.WebSocketDebuggerURL, baseURL, attempt)
		if oauthCallbackHasCredential(callback) {
			return callback, summary, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return oauthCallbackResult{}, summary, lastErr
	}
	if !sawReadableTab {
		return oauthCallbackResult{}, summary, errors.New("未找到可读取浏览器 Cookie 的授权标签页")
	}
	return oauthCallbackResult{}, summary, errors.New("未捕获到 NewAPI 登录态")
}

func shouldCaptureBrowserAuthFromTab(baseURL string, tab devToolsTab) bool {
	if !shouldReadBrowserAuthFromTab(tab) || !shouldPrepareOAuthTab(baseURL, tab) {
		return false
	}
	return true
}

func shouldReadBrowserAuthFromTab(tab devToolsTab) bool {
	if strings.TrimSpace(tab.WebSocketDebuggerURL) == "" {
		return false
	}
	return strings.TrimSpace(tab.Type) == "" || strings.EqualFold(tab.Type, "page")
}

func captureBrowserAuthFromTab(ctx context.Context, websocketURL, baseURL string, attempt int) (oauthCallbackResult, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return oauthCallbackResult{}, err
	}
	defer conn.Close()
	auth, err := captureDevToolsLocalAuthOnce(ctx, conn, baseURL, 1000+attempt*2)
	if err == nil && strings.TrimSpace(auth.AccessToken) != "" && strings.TrimSpace(auth.UserID) != "" {
		oauthLogf("browser access token captured from stable NewAPI tab")
		return oauthCallbackResult{AccessToken: auth.AccessToken, UserID: auth.UserID}, nil
	}
	lastErr := err
	cookies, err := captureDevToolsSessionCookiesOnce(ctx, conn, baseURL, auth.UserID, 1001+attempt*2)
	if err == nil && strings.TrimSpace(cookies) != "" && strings.TrimSpace(auth.UserID) != "" {
		oauthLogf("browser session cookies captured from stable NewAPI tab")
		return oauthCallbackResult{SessionCookies: cookies, UserID: auth.UserID}, nil
	}
	if err != nil {
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("未捕获到 NewAPI 登录态")
	}
	return oauthCallbackResult{}, lastErr
}

func captureBrowserSessionCookiesFromTab(ctx context.Context, websocketURL, baseURL string, attempt int) (oauthCallbackResult, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return oauthCallbackResult{}, err
	}
	defer conn.Close()
	auth, err := captureDevToolsLocalAuthOnce(ctx, conn, baseURL, 2000+attempt*2)
	if err != nil {
		return oauthCallbackResult{}, err
	}
	cookies, err := captureDevToolsSessionCookiesOnce(ctx, conn, baseURL, auth.UserID, 2001+attempt*2)
	if err != nil {
		return oauthCallbackResult{}, err
	}
	if strings.TrimSpace(cookies) == "" {
		return oauthCallbackResult{}, errors.New("未捕获到 NewAPI session cookie")
	}
	oauthLogf("browser session cookies captured from oauth browser tab")
	return oauthCallbackResult{SessionCookies: cookies, UserID: auth.UserID}, nil
}

func captureDevToolsSessionCookiesOnce(ctx context.Context, conn *websocket.Conn, baseURL, userID string, requestID int) (string, error) {
	userID = cleanBrowserUserID(userID)
	if userID == "" {
		return "", errors.New("NewAPI localStorage user id missing")
	}
	raw, err := cdpRequest(ctx, conn, requestID, "Network.getAllCookies", nil, 2*time.Second)
	if err != nil {
		return "", err
	}
	cookies, err := sessionCookiesFromDevTools(raw, baseURL)
	if err != nil {
		return "", err
	}
	if !devToolsSessionCookiesAuthenticated(ctx, baseURL, cookies, userID) {
		return "", newapi.ErrAuthRequired
	}
	return cookies, nil
}

func captureDevToolsCallbackCookies(ctx context.Context, conn *websocket.Conn, baseURL string) string {
	raw, err := cdpRequest(ctx, conn, 29, "Network.getAllCookies", nil, 2*time.Second)
	if err != nil {
		return ""
	}
	cookies, err := sessionCookiesFromDevTools(raw, baseURL)
	if err != nil {
		return ""
	}
	return cookies
}

func devToolsSessionCookiesAuthenticated(ctx context.Context, baseURL, sessionCookies, userID string) bool {
	client, err := newapi.NewClient(baseURL, &http.Client{Timeout: 3 * time.Second})
	if err != nil {
		return false
	}
	client.UserID = cleanBrowserUserID(userID)
	if client.UserID == "" {
		return false
	}
	if err := client.ImportSessionCookies(sessionCookies); err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = client.UserSelf(checkCtx, "")
	return err == nil
}

func abortPausedOAuthRequest(conn *websocket.Conn, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if conn == nil || requestID == "" {
		return
	}
	_ = conn.WriteJSON(map[string]any{
		"id":     7,
		"method": "Fetch.failRequest",
		"params": map[string]any{
			"requestId":   requestID,
			"errorReason": "Aborted",
		},
	})
}

func continuePausedOAuthRequest(conn *websocket.Conn, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if conn == nil || requestID == "" {
		return
	}
	_ = conn.WriteJSON(map[string]any{
		"id":     8,
		"method": "Fetch.continueRequest",
		"params": map[string]any{
			"requestId": requestID,
		},
	})
}

func cdpRequest(ctx context.Context, conn *websocket.Conn, id int, method string, params any, timeout time.Duration) ([]byte, error) {
	if conn == nil {
		return nil, errors.New("devtools connection is nil")
	}
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var response struct {
			ID    int             `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &response) != nil || response.ID != id {
			continue
		}
		if len(response.Error) > 0 && string(response.Error) != "null" {
			return nil, fmt.Errorf("devtools %s failed", method)
		}
		return raw, nil
	}
}

func cdpJSONValue(raw []byte) ([]byte, error) {
	var response struct {
		Result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	value := bytes.TrimSpace(response.Result.Result.Value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil, errors.New("devtools response value is empty")
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.New("devtools response value is empty")
		}
		if !json.Valid([]byte(text)) {
			return nil, errors.New("devtools response value is not JSON")
		}
		return []byte(text), nil
	}
	if !json.Valid(value) {
		return nil, errors.New("devtools response value is not JSON")
	}
	return value, nil
}

func emitOAuthCallback(callback oauthCallbackResult, callbacks chan<- oauthCallbackResult, done <-chan struct{}, stop func()) {
	select {
	case callbacks <- callback:
	case <-done:
	default:
	}
	stop()
}

func emitOAuthFailure(message string, callbacks chan<- oauthCallbackResult, done <-chan struct{}) {
	select {
	case callbacks <- oauthCallbackResult{Error: strings.TrimSpace(message)}:
	case <-done:
	default:
	}
}

func fetchDevToolsTabs(client *http.Client, port int) ([]byte, error) {
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("devtools status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func closeDevToolsTab(client *http.Client, port int, tabID string) {
	tabID = strings.TrimSpace(tabID)
	if client == nil || port <= 0 || tabID == "" {
		return
	}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/close/" + url.PathEscape(tabID))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

type devToolsCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

func sessionCookiesFromDevTools(raw []byte, baseURL string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	host := strings.ToLower(strings.TrimSpace(base.Hostname()))
	if host == "" {
		return "", errors.New("NewAPI 网站地址无效")
	}
	var response struct {
		Result struct {
			Cookies []devToolsCookie `json:"cookies"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	stored := make([]storedBrowserCookie, 0, len(response.Result.Cookies))
	for _, cookie := range response.Result.Cookies {
		if !isNewAPISessionCookie(cookie.Name) || !cookieDomainMatches(host, cookie.Domain) {
			continue
		}
		stored = append(stored, storedBrowserCookie{Name: cookie.Name, Value: cookie.Value})
	}
	if len(stored) == 0 {
		return "", errors.New("未捕获到 NewAPI session cookie")
	}
	out, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func isNewAPISessionCookie(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "session")
}

type storedBrowserCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func cookieDomainMatches(host, domain string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), ".")
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if host == "" || domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func cleanBrowserUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	id, err := strconv.Atoi(userID)
	if err != nil || id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func devToolsTabs(raw []byte) []devToolsTab {
	var tabs []devToolsTab
	if err := json.Unmarshal(raw, &tabs); err != nil {
		return nil
	}
	return tabs
}

func devToolsTabsSummary(tabs []devToolsTab) string {
	if len(tabs) == 0 {
		return "count=0"
	}
	items := make([]string, 0, len(tabs))
	for i, tab := range tabs {
		if i >= 6 {
			items = append(items, fmt.Sprintf("...(+%d)", len(tabs)-i))
			break
		}
		tabType := strings.TrimSpace(tab.Type)
		if tabType == "" {
			tabType = "unknown"
		}
		rawURL := sanitizeOAuthLogMessage(strings.TrimSpace(tab.URL))
		if len(rawURL) > 180 {
			rawURL = rawURL[:180] + "..."
		}
		if rawURL == "" {
			rawURL = "(empty)"
		}
		items = append(items, tabType+" "+rawURL)
	}
	return fmt.Sprintf("count=%d [%s]", len(tabs), strings.Join(items, " | "))
}

func shouldPrepareOAuthTab(baseURL string, tab devToolsTab) bool {
	if strings.TrimSpace(tab.Type) != "" && !strings.EqualFold(tab.Type, "page") {
		return false
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	tabURL, err := url.Parse(strings.TrimSpace(tab.URL))
	if err != nil {
		return false
	}
	baseHost := strings.TrimSpace(base.Host)
	tabHost := strings.TrimSpace(tabURL.Host)
	return baseHost != "" && strings.EqualFold(baseHost, tabHost)
}

func shouldPrepareOAuthAuthorizeTab(clientID string, tab devToolsTab) bool {
	if strings.TrimSpace(tab.Type) != "" && !strings.EqualFold(tab.Type, "page") {
		return false
	}
	tabURL, err := url.Parse(strings.TrimSpace(tab.URL))
	if err != nil {
		return false
	}
	if !strings.EqualFold(tabURL.Host, "connect.linux.do") || tabURL.Path != "/oauth2/authorize" {
		return false
	}
	q := tabURL.Query()
	return strings.TrimSpace(clientID) != "" &&
		q.Get("client_id") == strings.TrimSpace(clientID) &&
		strings.TrimSpace(q.Get("state")) != ""
}

func shouldPrepareDirectAuthorizeTab(tab devToolsTab, launchURL string) bool {
	if strings.TrimSpace(tab.Type) != "" && !strings.EqualFold(tab.Type, "page") {
		return false
	}
	marker := oauthDirectLaunchMarker(launchURL)
	if marker == "" {
		return false
	}
	return strings.Contains(tab.URL, marker)
}

func oauthDirectLaunchMarker(rawURL string) string {
	return oauthDirectLaunchMarkerPattern.FindString(strings.TrimSpace(rawURL))
}

func oauthCallbackFetchPatterns(baseURL string) []map[string]string {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || strings.TrimSpace(base.Scheme) == "" || strings.TrimSpace(base.Host) == "" {
		return nil
	}
	prefix := base.Scheme + "://" + base.Host
	return []map[string]string{
		{"urlPattern": prefix + "/oauth/linuxdo*", "requestStage": "Request"},
		{"urlPattern": prefix + "/api/oauth/linuxdo*", "requestStage": "Request"},
	}
}

func oauthCallbackFromDevToolsTabs(baseURL string, raw []byte) (string, string, bool) {
	return oauthCallbackFromDevToolsTabsForState(baseURL, raw, "")
}

func oauthCallbackFromDevToolsTabsForState(baseURL string, raw []byte, expectedState string) (string, string, bool) {
	for _, tab := range devToolsTabs(raw) {
		if strings.TrimSpace(tab.URL) == "" {
			continue
		}
		if callbackURLMatchesExpectedState(baseURL, tab.URL, expectedState) {
			return tab.URL, tab.ID, true
		}
	}
	return "", "", false
}

type devToolsEvent struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func oauthCallbackFromDevToolsEvent(baseURL string, raw []byte) (string, bool) {
	return oauthCallbackFromDevToolsEventForState(baseURL, raw, "")
}

func oauthCallbackFromDevToolsEventForState(baseURL string, raw []byte, expectedState string) (string, bool) {
	var event devToolsEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return "", false
	}
	if !strings.HasPrefix(event.Method, "Network.") && !strings.HasPrefix(event.Method, "Page.") && event.Method != "Fetch.requestPaused" {
		return "", false
	}
	for _, candidate := range devToolsEventURLs(event.Params) {
		if callbackURLMatchesExpectedState(baseURL, candidate, expectedState) {
			return candidate, true
		}
	}
	return "", false
}

func pausedOAuthCallbackFromDevToolsEvent(baseURL string, raw []byte) (string, string, bool) {
	return pausedOAuthCallbackFromDevToolsEventForState(baseURL, raw, "")
}

func pausedOAuthCallbackFromDevToolsEventForState(baseURL string, raw []byte, expectedState string) (string, string, bool) {
	var event struct {
		Method string `json:"method"`
		Params struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Method != "Fetch.requestPaused" {
		return "", "", false
	}
	callbackURL := strings.TrimSpace(event.Params.Request.URL)
	if !callbackURLMatchesExpectedState(baseURL, callbackURL, expectedState) {
		return "", "", false
	}
	requestID := strings.TrimSpace(event.Params.RequestID)
	if requestID == "" {
		return "", "", false
	}
	return callbackURL, requestID, true
}

func callbackURLMatchesExpectedState(baseURL, callbackURL, expectedState string) bool {
	cb, err := newapi.ExtractLinuxDoCallback(baseURL, callbackURL)
	if err != nil {
		return false
	}
	expectedState = strings.TrimSpace(expectedState)
	return expectedState == "" || cb.State == expectedState
}

func devToolsEventURLs(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var urls []string
	collectDevToolsURLs(value, &urls)
	return urls
}

func collectDevToolsURLs(value any, urls *[]string) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.EqualFold(key, "url") {
				if s, ok := child.(string); ok && strings.TrimSpace(s) != "" {
					*urls = append(*urls, s)
				}
				continue
			}
			collectDevToolsURLs(child, urls)
		}
	case []any:
		for _, child := range v {
			collectDevToolsURLs(child, urls)
		}
	}
}

func findOAuthBrowser() (string, error) {
	for _, path := range oauthBrowserCandidates() {
		if path == "" {
			continue
		}
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return path, nil
		}
		if resolved, err := exec.LookPath(path); err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("未找到 Edge 或 Chrome，取消勾选“自动完成登录”后可使用手动回调 URL 登录")
}

func oauthBrowserCandidates() []string {
	if runtime.GOOS != "windows" {
		return []string{"msedge", "google-chrome", "chromium", "chrome"}
	}
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	localAppData := os.Getenv("LOCALAPPDATA")
	return []string{
		os.Getenv("QUOTABALL_OAUTH_BROWSER"),
		filepath.Join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(localAppData, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
		"msedge.exe",
		"chrome.exe",
	}
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("无法分配本地浏览器调试端口")
	}
	return addr.Port, nil
}

func loopbackPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func devToolsPortActive(port int) bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
