package wailsui

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendHasLoggedOutRenderPathWithoutPanelShell(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	start := strings.Index(js, "function renderLoggedOut")
	if start < 0 {
		t.Fatalf("frontend must define a logged-out-only render path")
	}
	end := strings.Index(js[start:], "function renderMainPanel")
	if end < 0 {
		t.Fatalf("frontend must keep main panel rendering separate from logged-out rendering")
	}
	loggedOut := js[start : start+end]
	if strings.Contains(loggedOut, `class="shell"`) {
		t.Fatalf("logged-out render path must not include the main panel shell")
	}
}

func TestFrontendLoginSupportsProviderSelectionWithoutChangingKrillDefault(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, `provider: "krill"`) {
		t.Fatalf("frontend config state must default to the existing Krill AI provider")
	}
	for _, want := range []string{"NewAPI", "Sub2", "Krill AI"} {
		if !strings.Contains(js, want) {
			t.Fatalf("login UI must expose provider option %q", want)
		}
	}
	if !strings.Contains(js, `data-action="newapi-start-oauth"`) ||
		!strings.Contains(js, `data-form="newapi-complete"`) {
		t.Fatalf("NewAPI login must provide OAuth start and callback completion controls")
	}
}

func TestFrontendNewAPIRememberCheckboxCanSendFalse(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	start := strings.Index(js, "function syncLoginFormState")
	if start < 0 {
		t.Fatalf("frontend must define syncLoginFormState")
	}
	end := strings.Index(js[start:], "async function startNewAPIOAuth")
	if end < 0 {
		t.Fatalf("syncLoginFormState test could not find function boundary")
	}
	syncLoginFormState := js[start : start+end]
	if strings.Contains(syncLoginFormState, `data.has("remember")`) {
		t.Fatalf("remember checkbox state must be read even when unchecked; unchecked checkboxes are absent from FormData")
	}
	if !strings.Contains(syncLoginFormState, `state.config.rememberLogin = data.get("remember") === "on";`) {
		t.Fatalf("syncLoginFormState must be able to set rememberLogin to false")
	}
}

func TestFrontendNewAPIOAuthKeepsAuthorizeURLForManualRetry(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, "oauthAuthorizeUrl") {
		t.Fatalf("frontend must keep the NewAPI authorize URL for manual retry/copy")
	}
	if !strings.Contains(js, `data-action="copy-oauth-url"`) {
		t.Fatalf("NewAPI login should expose a copy-authorize-url fallback when LinuxDo is unreachable")
	}
	if !strings.Contains(js, `state.oauthAuthorizeUrl = started.authorizeUrl || "";`) {
		t.Fatalf("StartNewAPIOAuth result must store authorizeUrl")
	}
}

func TestFrontendNewAPIOAuthCommunicatesAutomaticCompletion(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, "autoCapture") {
		t.Fatalf("frontend must read the backend automatic OAuth capture flag")
	}
	if !strings.Contains(js, "授权完成后会自动登录") {
		t.Fatalf("NewAPI login should tell the user that browser approval completes login automatically")
	}
}

func TestFrontendSnapshotUpdateKeepsLoginModalInSyncWithAuthState(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, `if (snap.loggedIn && state.modal === "login")`) {
		t.Fatalf("snapshot update must clear stale login modal after successful saved-session refresh")
	}
	if !strings.Contains(js, `if (!snap.loggedIn)`) || !strings.Contains(js, `state.modal = "login";`) {
		t.Fatalf("snapshot update must force the login modal when the user is logged out")
	}
}

func TestBackendGlassSyncRequiresLoggedInSnapshot(t *testing.T) {
	raw, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(raw)

	start := strings.Index(goSrc, "func (a *App) syncGlass")
	if start < 0 {
		t.Fatalf("backend must define syncGlass")
	}
	end := strings.Index(goSrc[start:], "func (a *App) setRefreshing")
	if end < 0 {
		t.Fatalf("syncGlass test could not find function boundary")
	}
	syncGlass := goSrc[start : start+end]
	if !strings.Contains(syncGlass, "snap.LoggedIn") {
		t.Fatalf("glass ball visibility must depend on the snapshot logged-in state")
	}
	if !strings.Contains(syncGlass, "show := enabled && snap.LoggedIn") {
		t.Fatalf("glass ball should only show when enabled and logged in")
	}
}

func TestBackendShowPanelForcesLoginOnlyStateWhenLoggedOut(t *testing.T) {
	raw, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(raw)

	start := strings.Index(goSrc, "func (a *App) ShowPanel")
	if start < 0 {
		t.Fatalf("backend must define ShowPanel")
	}
	end := strings.Index(goSrc[start:], "func (a *App) TogglePanel")
	if end < 0 {
		t.Fatalf("ShowPanel test could not find function boundary")
	}
	showPanel := goSrc[start : start+end]
	if !strings.Contains(showPanel, "!a.hasLoginState()") || !strings.Contains(showPanel, "loginRequiredMessage()") {
		t.Fatalf("ShowPanel must force a logged-out snapshot before revealing the panel")
	}
}

func TestBackendStartsAndSyncsTrayController(t *testing.T) {
	raw, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(raw)

	if !strings.Contains(goSrc, "tray") || !strings.Contains(goSrc, "*ui.TrayController") {
		t.Fatalf("app must keep a tray controller so it can run in the background notification area")
	}
	if !strings.Contains(goSrc, "_ = a.ensureTrayController()") {
		t.Fatalf("startup must create the tray controller")
	}
	if !strings.Contains(goSrc, "func (a *App) syncTray") || !strings.Contains(goSrc, "a.syncTray(snap)") {
		t.Fatalf("snapshot changes must update the tray tooltip/status")
	}
	if !strings.Contains(goSrc, "tray.Close()") {
		t.Fatalf("shutdown must dispose the tray icon")
	}
}

func TestBackendHidesMainWindowFromTaskbarWhenTrayIsAvailable(t *testing.T) {
	raw, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(raw)
	if strings.Count(goSrc, "hideMainWindowFromTaskbar()") < 3 {
		t.Fatalf("startup, explicit show, and reveal flows must keep the Wails panel out of the taskbar")
	}

	taskbar, err := os.ReadFile("taskbar_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	taskbarSrc := string(taskbar)
	if !strings.Contains(taskbarSrc, "WS_EX_APPWINDOW") || !strings.Contains(taskbarSrc, "WS_EX_TOOLWINDOW") {
		t.Fatalf("Windows taskbar hiding must switch app-window style to tool-window style")
	}
	if !strings.Contains(taskbarSrc, "WindowBelongs") && !strings.Contains(taskbarSrc, "windowBelongsToProcess") {
		t.Fatalf("taskbar hiding must verify it is changing this process's Wails window")
	}
}
