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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"krill_monitor/internal/newapi"
)

var startOAuthBrowserCapture = startDefaultOAuthBrowserCapture

const oauthProfileDirEnv = "QUOTABALL_OAUTH_PROFILE_DIR"

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
	ID  string `json:"id"`
	URL string `json:"url"`
}

func startDefaultOAuthBrowserCapture(ctx context.Context, authorizeURL, baseURL string) (*oauthCapture, error) {
	browser, err := findOAuthBrowser()
	if err != nil {
		return nil, err
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	profileDir, err := oauthBrowserProfileDir()
	if err != nil {
		return nil, err
	}

	callbacks := make(chan string, 1)
	done := make(chan struct{})
	args := []string{
		"--new-window",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		authorizeURL,
	}
	cmd := exec.CommandContext(ctx, browser, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("无法启动自动登录浏览器：%w", err)
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			close(done)
		})
	}
	go func() {
		_ = cmd.Wait()
		cleanup()
	}()
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	go pollOAuthBrowser(ctx, port, baseURL, callbacks, done, stop)

	return &oauthCapture{
		Callbacks: callbacks,
		Done:      done,
		close:     stop,
	}, nil
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

func pollOAuthBrowser(ctx context.Context, port int, baseURL string, callbacks chan<- string, done <-chan struct{}, stop func()) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(350 * time.Millisecond)
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
				continue
			}
			select {
			case callbacks <- callbackURL:
			case <-done:
			default:
			}
			stop()
			return
		}
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

func oauthCallbackFromDevToolsTabs(baseURL string, raw []byte) (string, string, bool) {
	var tabs []devToolsTab
	if err := json.Unmarshal(raw, &tabs); err != nil {
		return "", "", false
	}
	for _, tab := range tabs {
		if strings.TrimSpace(tab.URL) == "" {
			continue
		}
		if _, err := newapi.ExtractLinuxDoCallback(baseURL, tab.URL); err == nil {
			return tab.URL, tab.ID, true
		}
	}
	return "", "", false
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
