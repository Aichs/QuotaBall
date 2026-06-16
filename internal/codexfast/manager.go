package codexfast

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quotaball/internal/atomicfile"
)

const (
	DefaultHost         = "127.0.0.1"
	DefaultPort         = 48251
	DefaultTargetOrigin = "https://api.krill-ai.com"
	DefaultModels       = "gpt-5.5,gpt-5.4"
	AutoStartName       = "QuotaBallCodexFastProxy"
)

//go:embed assets/codex-fast-proxy.mjs assets/Start-CodexFastProxy.ps1
var bundled embed.FS

type Manager struct {
	CodexHome    string
	Host         string
	Port         int
	TargetOrigin string
	Models       []string
}

type stateFile struct {
	OriginalBaseURL string `json:"original_base_url,omitempty"`
}

type managerPaths struct {
	config      string
	proxyDir    string
	proxyScript string
	startScript string
	state       string
}

func Apply(ctx context.Context, enabled bool) error {
	manager, err := DefaultManager()
	if err != nil {
		return err
	}
	if enabled {
		return manager.Enable(ctx)
	}
	return manager.Disable(ctx)
}

func DetectEnabled() (bool, error) {
	manager, err := DefaultManager()
	if err != nil {
		return false, err
	}
	return manager.EnabledInCodexConfig()
}

func DefaultManager() (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Manager{}, err
	}
	return Manager{
		CodexHome:    filepath.Join(home, ".codex"),
		Host:         DefaultHost,
		Port:         DefaultPort,
		TargetOrigin: DefaultTargetOrigin,
		Models:       strings.Split(DefaultModels, ","),
	}, nil
}

func (m Manager) Enable(ctx context.Context) error {
	m = m.normalized()
	paths := m.paths()
	if err := os.MkdirAll(paths.proxyDir, 0o700); err != nil {
		return err
	}

	raw, err := readOptional(paths.config)
	if err != nil {
		return err
	}
	configured, currentBaseURL, err := ConfigureCodexConfig(string(raw), m.ProxyBaseURL())
	if err != nil {
		return err
	}
	state, _ := readState(paths.state)
	if currentBaseURL != "" && currentBaseURL != m.ProxyBaseURL() {
		state.OriginalBaseURL = currentBaseURL
	}
	if state.OriginalBaseURL == "" {
		state.OriginalBaseURL = strings.TrimRight(m.TargetOrigin, "/") + "/codex/v1"
	}

	if err := writeBundledFile(paths.proxyScript, "assets/codex-fast-proxy.mjs", nil); err != nil {
		return err
	}
	if err := writeBundledFile(paths.startScript, "assets/Start-CodexFastProxy.ps1", m.startScriptReplacements()); err != nil {
		return err
	}
	if err := startProxy(ctx, paths.startScript, m.Host, m.Port); err != nil {
		return err
	}
	if err := writeState(paths.state, state); err != nil {
		return err
	}
	if err := writeText(paths.config, configured); err != nil {
		return err
	}
	return enableAutoStart(paths.startScript)
}

func (m Manager) Disable(ctx context.Context) error {
	m = m.normalized()
	paths := m.paths()
	var errs []error
	state, _ := readState(paths.state)
	originalBaseURL := state.OriginalBaseURL
	if originalBaseURL == "" {
		originalBaseURL = strings.TrimRight(m.TargetOrigin, "/") + "/codex/v1"
	}
	raw, err := readOptional(paths.config)
	if err != nil {
		errs = append(errs, err)
	} else {
		restored, err := RestoreCodexConfig(string(raw), m.ProxyBaseURL(), originalBaseURL)
		if err != nil {
			errs = append(errs, err)
		} else if restored != string(raw) {
			if err := writeText(paths.config, restored); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := disableAutoStart(); err != nil {
		errs = append(errs, err)
	}
	if err := stopProxy(ctx, m.Host, m.Port); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (m Manager) EnabledInCodexConfig() (bool, error) {
	m = m.normalized()
	raw, err := readOptional(m.paths().config)
	if err != nil {
		return false, err
	}
	provider := activeProvider(string(raw))
	if provider == "" {
		provider = "custom"
	}
	return findTableString(string(raw), providerTable(provider), "base_url") == m.ProxyBaseURL(), nil
}

func (m Manager) ProxyBaseURL() string {
	m = m.normalized()
	return fmt.Sprintf("http://%s:%d/codex/v1", m.Host, m.Port)
}

func (m Manager) normalized() Manager {
	if strings.TrimSpace(m.Host) == "" {
		m.Host = DefaultHost
	}
	if m.Port == 0 {
		m.Port = DefaultPort
	}
	if strings.TrimSpace(m.TargetOrigin) == "" {
		m.TargetOrigin = DefaultTargetOrigin
	}
	m.TargetOrigin = strings.TrimRight(strings.TrimSpace(m.TargetOrigin), "/")
	if len(m.Models) == 0 {
		m.Models = strings.Split(DefaultModels, ",")
	}
	for i := range m.Models {
		m.Models[i] = strings.TrimSpace(m.Models[i])
	}
	return m
}

func (m Manager) paths() managerPaths {
	proxyDir := filepath.Join(m.CodexHome, "fast-proxy")
	return managerPaths{
		config:      filepath.Join(m.CodexHome, "config.toml"),
		proxyDir:    proxyDir,
		proxyScript: filepath.Join(proxyDir, "codex-fast-proxy.mjs"),
		startScript: filepath.Join(proxyDir, "Start-CodexFastProxy.ps1"),
		state:       filepath.Join(proxyDir, "fast-proxy-state.json"),
	}
}

func (m Manager) startScriptReplacements() map[string]string {
	return map[string]string{
		"{{HOST}}":          m.Host,
		"{{PORT}}":          strconv.Itoa(m.Port),
		"{{TARGET_ORIGIN}}": m.TargetOrigin,
		"{{MODELS}}":        strings.Join(m.Models, ","),
	}
}

func ConfigureCodexConfig(raw, proxyBaseURL string) (string, string, error) {
	provider := activeProvider(raw)
	if provider == "" {
		provider = "custom"
	}
	currentBaseURL := findTableString(raw, providerTable(provider), "base_url")
	out := raw
	if strings.TrimSpace(out) == "" {
		out = "model_provider = \"custom\"\n"
	}
	out = updateTopLevelString(out, "model_provider", provider)
	out = updateTopLevelString(out, "service_tier", "priority")
	out = updateTableBool(out, "features", "fast_mode", true)
	out = updateTableBool(out, "notice", "fast_default_opt_out", false)
	out = updateModelProviderBaseURL(out, provider, proxyBaseURL)
	return out, currentBaseURL, nil
}

func RestoreCodexConfig(raw, proxyBaseURL, originalBaseURL string) (string, error) {
	if strings.TrimSpace(originalBaseURL) == "" {
		return raw, nil
	}
	provider := activeProvider(raw)
	if provider == "" {
		provider = "custom"
	}
	current := findTableString(raw, providerTable(provider), "base_url")
	if current != proxyBaseURL {
		return raw, nil
	}
	return updateModelProviderBaseURL(raw, provider, originalBaseURL), nil
}

func activeProvider(raw string) string {
	head := raw[:firstTableIndex(raw)]
	return findAssignmentString(head, "model_provider")
}

func providerTable(provider string) string {
	return "model_providers." + provider
}

func updateModelProviderBaseURL(raw, provider, value string) string {
	table := providerTable(provider)
	if _, _, _, found := tableRange(raw, table); !found {
		return withTrailingNewline(raw) + "\n[" + table + "]\nname = " + tomlQuote(provider) + "\nbase_url = " + tomlQuote(value) + "\n"
	}
	return updateTableString(raw, table, "base_url", value)
}

func updateTopLevelString(raw, key, value string) string {
	idx := firstTableIndex(raw)
	head := raw[:idx]
	tail := raw[idx:]
	updated, found := updateAssignmentInBlock(head, key, tomlQuote(value))
	if found {
		return updated + tail
	}
	return withTrailingNewline(head) + key + " = " + tomlQuote(value) + "\n" + tail
}

func updateTableString(raw, table, key, value string) string {
	return updateTableAssignment(raw, table, key, tomlQuote(value))
}

func updateTableBool(raw, table, key string, value bool) string {
	return updateTableAssignment(raw, table, key, strconv.FormatBool(value))
}

func updateTableAssignment(raw, table, key, renderedValue string) string {
	_, headerEnd, end, found := tableRange(raw, table)
	if !found {
		return withTrailingNewline(raw) + "\n[" + table + "]\n" + key + " = " + renderedValue + "\n"
	}
	section := raw[headerEnd:end]
	updated, ok := updateAssignmentInBlock(section, key, renderedValue)
	if !ok {
		updated = key + " = " + renderedValue + "\n" + section
	}
	return raw[:headerEnd] + updated + raw[end:]
}

func findTableString(raw, table, key string) string {
	_, headerEnd, end, found := tableRange(raw, table)
	if !found {
		return ""
	}
	return findAssignmentString(raw[headerEnd:end], key)
}

func findAssignmentString(block, key string) string {
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		beforeComment := strings.SplitN(trimmed, "#", 2)[0]
		parts := strings.SplitN(beforeComment, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		value, err := strconv.Unquote(strings.TrimSpace(parts[1]))
		if err == nil {
			return value
		}
	}
	return ""
}

func updateAssignmentInBlock(block, key, renderedValue string) (string, bool) {
	lines := splitLines(block)
	for i, line := range lines {
		withoutNewline := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(withoutNewline)
		if strings.HasPrefix(trimmed, key) {
			beforeComment := strings.SplitN(trimmed, "#", 2)[0]
			parts := strings.SplitN(beforeComment, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
				prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				newline := line[len(withoutNewline):]
				lines[i] = prefix + key + " = " + renderedValue + newline
				return strings.Join(lines, ""), true
			}
		}
	}
	return block, false
}

func tableRange(raw, table string) (start, headerEnd, end int, found bool) {
	re := regexp.MustCompile(`(?m)^\s*\[` + regexp.QuoteMeta(table) + `\]\s*(?:#.*)?$`)
	loc := re.FindStringIndex(raw)
	if loc == nil {
		return 0, 0, 0, false
	}
	headerEnd = loc[1]
	if next := strings.IndexByte(raw[headerEnd:], '\n'); next >= 0 {
		headerEnd += next + 1
	}
	end = len(raw)
	nextTable := regexp.MustCompile(`(?m)^\s*\[`)
	if next := nextTable.FindStringIndex(raw[headerEnd:]); next != nil {
		end = headerEnd + next[0]
	}
	return loc[0], headerEnd, end, true
}

func firstTableIndex(raw string) int {
	re := regexp.MustCompile(`(?m)^\s*\[`)
	loc := re.FindStringIndex(raw)
	if loc == nil {
		return len(raw)
	}
	return loc[0]
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	matches := regexp.MustCompile(`.*(?:\r?\n|$)`).FindAllString(s, -1)
	if len(matches) > 0 && matches[len(matches)-1] == "" {
		matches = matches[:len(matches)-1]
	}
	return matches
}

func tomlQuote(value string) string {
	return strconv.Quote(value)
}

func withTrailingNewline(raw string) string {
	if raw == "" || strings.HasSuffix(raw, "\n") {
		return raw
	}
	return raw + "\n"
}

func writeBundledFile(path, name string, replacements map[string]string) error {
	raw, err := fs.ReadFile(bundled, name)
	if err != nil {
		return err
	}
	for old, newValue := range replacements {
		raw = bytes.ReplaceAll(raw, []byte(old), []byte(newValue))
	}
	return writeBytes(path, raw)
}

func readOptional(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return raw, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, err
}

func readState(path string) (stateFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return stateFile{}, err
	}
	var state stateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return stateFile{}, err
	}
	return state, nil
}

func writeState(path string, state stateFile) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return writeBytes(path, raw)
}

func writeText(path, text string) error {
	return writeBytes(path, []byte(text))
}

func writeBytes(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, raw, 0o600)
}

func waitForProxy(ctx context.Context, host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 300 * time.Millisecond}
	url := fmt.Sprintf("http://%s:%d/_codex_fast_proxy/health", host, port)
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("proxy health check timed out")
			}
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
	}
}
