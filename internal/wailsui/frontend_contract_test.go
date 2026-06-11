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
