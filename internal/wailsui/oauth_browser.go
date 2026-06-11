package wailsui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

var debugPortPattern = regexp.MustCompile(`--remote-debugging-port=(\d+)`)
var oauthSensitiveQueryPattern = regexp.MustCompile(`(?i)(code|state|client_id)=([^&\s]+)`)

type oauthCapture struct {
	Callbacks <-chan string
	Done      <-chan struct{}
	close     func()
}

func (c *oauthCapture) Close() {
	if c != nil && c.close != nil {
		c.close()
	}
}

type devToolsTab struct {
	ID                   string `json:"id"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func startDefaultOAuthBrowserCapture(ctx context.Context, authorizeURL, baseURL string) (capture *oauthCapture, err error) {
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
	port := runningOAuthBrowserDebugPort(profileDir)
	if port == 0 {
		port, err = oauthBrowserDebugPort()
		if err != nil {
			return nil, err
		}
	}

	callbacks := make(chan string, 1)
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
		authorizeURL,
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
		pollOAuthBrowser(ctx, port, baseURL, callbacks, done, stop)
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

func pollOAuthBrowser(ctx context.Context, port int, baseURL string, callbacks chan<- string, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth poll")
	client := &http.Client{Timeout: 2 * time.Second}
	watcherCtx, cancelWatchers := context.WithCancel(ctx)
	defer cancelWatchers()
	watchedTabs := map[string]struct{}{}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
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
				continue
			}
			callbackURL, _, ok := oauthCallbackFromDevToolsTabs(baseURL, raw)
			if !ok {
				for _, tab := range devToolsTabs(raw) {
					if strings.TrimSpace(tab.ID) == "" || strings.TrimSpace(tab.WebSocketDebuggerURL) == "" {
						continue
					}
					if _, exists := watchedTabs[tab.ID]; exists {
						continue
					}
					watchedTabs[tab.ID] = struct{}{}
					go func(websocketURL string) {
						defer recoverOAuthPanic("oauth devtools watcher")
						watchOAuthDevToolsTab(watcherCtx, websocketURL, baseURL, callbacks, done, stop)
					}(tab.WebSocketDebuggerURL)
				}
				continue
			}
			emitOAuthCallback(callbackURL, callbacks, done, stop)
			return
		}
	}
}

func watchOAuthDevToolsTab(ctx context.Context, websocketURL, baseURL string, callbacks chan<- string, done <-chan struct{}, stop func()) {
	defer recoverOAuthPanic("oauth devtools")
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"id": 1, "method": "Network.enable"})
	_ = conn.WriteJSON(map[string]any{"id": 2, "method": "Page.enable"})

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}
		callbackURL, ok := oauthCallbackFromDevToolsEvent(baseURL, raw)
		if !ok {
			continue
		}
		emitOAuthCallback(callbackURL, callbacks, done, stop)
		return
	}
}

func emitOAuthCallback(callbackURL string, callbacks chan<- string, done <-chan struct{}, stop func()) {
	select {
	case callbacks <- callbackURL:
	case <-done:
	default:
	}
	stop()
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

func devToolsTabs(raw []byte) []devToolsTab {
	var tabs []devToolsTab
	if err := json.Unmarshal(raw, &tabs); err != nil {
		return nil
	}
	return tabs
}

func oauthCallbackFromDevToolsTabs(baseURL string, raw []byte) (string, string, bool) {
	for _, tab := range devToolsTabs(raw) {
		if strings.TrimSpace(tab.URL) == "" {
			continue
		}
		if _, err := newapi.ExtractLinuxDoCallback(baseURL, tab.URL); err == nil {
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
	var event devToolsEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return "", false
	}
	if !strings.HasPrefix(event.Method, "Network.") && !strings.HasPrefix(event.Method, "Page.") {
		return "", false
	}
	for _, candidate := range devToolsEventURLs(event.Params) {
		if _, err := newapi.ExtractLinuxDoCallback(baseURL, candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
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
