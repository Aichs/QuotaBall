package wailsui

import (
	"os"
	"strings"
	"testing"

	"quotaball/internal/krill"
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

func TestFrontendKrillPanelUsesWeeklyAndMonthlyQuotaLabels(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	for _, want := range []string{"周额度", "月总额度"} {
		if !strings.Contains(js, want) {
			t.Fatalf("Krill quota panel must expose %q", want)
		}
	}
	for _, removed := range []string{"转结", "当日", "日额度"} {
		if strings.Contains(js, removed) {
			t.Fatalf("Krill quota panel must not use old quota label %q", removed)
		}
	}
	if !strings.Contains(js, "weeklyLimit") || !strings.Contains(js, "monthlyLimit") {
		t.Fatalf("frontend must read explicit weekly and monthly quota fields")
	}
}

func TestFrontendNewAPIPanelUsesBalanceSpendAndRequestCards(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, "function isNewAPIProvider") {
		t.Fatalf("frontend must detect NewAPI snapshots separately from Krill")
	}
	for _, want := range []string{"当前余额", "历史消耗", "请求次数"} {
		if !strings.Contains(js, want) {
			t.Fatalf("NewAPI panel must expose %q", want)
		}
	}

	start := strings.Index(js, "function newAPIStatCards")
	if start < 0 {
		t.Fatalf("frontend must keep NewAPI stat cards separate from Krill quota cards")
	}
	end := strings.Index(js[start:], "function krillStatCards")
	if end < 0 {
		t.Fatalf("newAPIStatCards test could not find function boundary")
	}
	newAPIStats := js[start : start+end]
	for _, want := range []string{"snapshot.wallet", "snapshot.spend", "snapshot.req"} {
		if !strings.Contains(newAPIStats, want) {
			t.Fatalf("NewAPI stat cards must read %q", want)
		}
	}
	for _, removed := range []string{"周额度", "月总额度", "本周剩余"} {
		if strings.Contains(newAPIStats, removed) {
			t.Fatalf("NewAPI stat cards must not expose Krill quota label %q", removed)
		}
	}
}

func TestFrontendNewAPISettingsHideGlassToggle(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, "function showGlassSetting") ||
		!strings.Contains(js, "!isNewAPIProvider(state.snapshot)") {
		t.Fatalf("frontend settings must hide the glass toggle for NewAPI snapshots")
	}
	start := strings.Index(js, "async function onSettings")
	if start < 0 {
		t.Fatalf("frontend must define onSettings")
	}
	end := strings.Index(js[start:], "async function boot")
	if end < 0 {
		t.Fatalf("onSettings test could not find function boundary")
	}
	onSettings := js[start : start+end]
	if !strings.Contains(onSettings, `glassEnabled: showGlassSetting() ? form.get("glassEnabled") === "on" : Boolean(state.config.glassEnabled)`) {
		t.Fatalf("NewAPI settings save must preserve the existing glass preference instead of posting an unchecked hidden toggle")
	}
}

func TestFrontendSettingsExposeCodexFastProxySwitch(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	for _, want := range []string{
		`codexFastProxyEnabled: false`,
		`name="codexFastProxyEnabled"`,
		`Codex Fast 代理`,
		`codexFastProxyEnabled: form.get("codexFastProxyEnabled") === "on"`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("frontend settings must include %q", want)
		}
	}
}

func TestFrontendHeaderExposesAboutButtonNextToSettings(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	start := strings.Index(js, "function renderHeader")
	if start < 0 {
		t.Fatalf("frontend must define renderHeader")
	}
	end := strings.Index(js[start:], "function renderStats")
	if end < 0 {
		t.Fatalf("renderHeader test could not find function boundary")
	}
	header := js[start : start+end]
	settingsIndex := strings.Index(header, `data-action="settings"`)
	aboutIndex := strings.Index(header, `data-action="about"`)
	if settingsIndex < 0 || aboutIndex < 0 {
		t.Fatalf("header must expose settings and about icon buttons")
	}
	if aboutIndex < settingsIndex {
		t.Fatalf("about button should be placed next to the settings button, after settings")
	}
	if !strings.Contains(header, `title="关于"`) {
		t.Fatalf("about button must have an accessible title")
	}
}

func TestFrontendAboutModalIncludesAuthorAndLinks(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, `state.modal === "about"`) {
		t.Fatalf("frontend modal router must support the about page")
	}
	start := strings.Index(js, "function renderAbout")
	if start < 0 {
		t.Fatalf("frontend must define renderAbout")
	}
	end := strings.Index(js[start:], "function showGlassSetting")
	if end < 0 {
		t.Fatalf("renderAbout test could not find function boundary")
	}
	about := js[start : start+end]
	for _, want := range []string{
		"作者",
		`alt="晏琳"`,
		`<div class="about-value">晏琳</div>`,
		"assets/about-avatar.png",
		"https://github.com/Aichs/QuotaBall/tree/feature/newapi-integration",
		"https://linux.do/u/aichs/summary",
		"LinuxDo 社区",
		"新的理想型社区",
		"真诚、友善、团结、专业",
		`class="about-avatar"`,
		`class="about-community"`,
		`class="dialog about"`,
		`target="_blank"`,
		`rel="noreferrer"`,
	} {
		if !strings.Contains(about, want) {
			t.Fatalf("about page must include %q", want)
		}
	}
}

func TestFrontendAboutAvatarAssetIsBundled(t *testing.T) {
	info, err := os.Stat("frontend/src/assets/about-avatar.png")
	if err != nil {
		t.Fatalf("about page avatar asset must be bundled: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("about page avatar asset must not be empty")
	}
}

func TestFrontendAboutModalStylesAvatarAndCommunitySection(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)

	for _, want := range []string{
		".about-avatar",
		"border-radius: 50%",
		"object-fit: cover",
		".about-community",
		".linuxdo-logo",
		".community-copy",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("about page styles must include %q", want)
		}
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
	if !strings.Contains(js, `started.autoCapture ? "" : (started.authorizeUrl || "")`) {
		t.Fatalf("automatic NewAPI login should hide manual authorize URL while preserving manual fallback")
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
	if !strings.Contains(js, "首次可能需要登录") || !strings.Contains(js, "当前浏览器登录态") {
		t.Fatalf("NewAPI login should explain persistent automatic mode and current-browser manual mode")
	}
}

func TestFrontendNewAPIAutomaticModeIsDefaultAndHidesManualCallbackControls(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, `newapiAutoCallback: true`) {
		t.Fatalf("NewAPI automatic callback mode should be the default")
	}
	if !strings.Contains(js, `${!auto && state.oauthAuthorizeUrl ?`) ||
		!strings.Contains(js, "!auto ? `<input class=\"field\" name=\"callbackUrl\"") {
		t.Fatalf("manual copy/callback controls should only render when automatic mode is disabled")
	}
	if !strings.Contains(js, `EventsOn("oauth:callback"`) ||
		!strings.Contains(js, "completeNewAPIOAuthFromCallback") {
		t.Fatalf("frontend should complete captured OAuth callbacks through the normal Wails binding")
	}
}

func TestFrontendNewAPIOAuthForwardsCapturedCallbackAndOptionalCookies(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	start := strings.Index(js, "async function completeNewAPIOAuthFromCallback")
	if start < 0 {
		t.Fatalf("frontend must define completeNewAPIOAuthFromCallback")
	}
	end := strings.Index(js[start:], "async function onSettings")
	if end < 0 {
		t.Fatalf("completeNewAPIOAuthFromCallback test could not find function boundary")
	}
	complete := js[start : start+end]
	if !strings.Contains(complete, "sessionCookies") || !strings.Contains(complete, "accessToken") || !strings.Contains(complete, "userId") {
		t.Fatalf("automatic NewAPI completion must forward browser cookies, access token, and user id to the backend")
	}
	if strings.Contains(complete, `if (!baseUrl || !callbackUrl)`) ||
		strings.Contains(complete, `!callbackUrl && !sessionCookies)`) {
		t.Fatalf("automatic NewAPI completion must allow callback, session-cookie, and token completion")
	}
}

func TestBackendOAuthCallbackWaiterCompletesCapturedLoginInBackend(t *testing.T) {
	raw, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(raw)

	start := strings.Index(goSrc, "func (a *App) waitNewAPIOAuthCallback")
	if start < 0 {
		t.Fatalf("backend must define waitNewAPIOAuthCallback")
	}
	end := strings.Index(goSrc[start:], "func nextOAuthCallback")
	if end < 0 {
		t.Fatalf("waitNewAPIOAuthCallback test could not find function boundary")
	}
	waiter := goSrc[start : start+end]
	if !strings.Contains(waiter, "completeCapturedNewAPIOAuth") {
		t.Fatalf("OAuth callback waiter should complete captured browser login in the backend")
	}
	if strings.Contains(waiter, "NewAPI 自动登录未捕获到 session") {
		t.Fatalf("automatic OAuth waiter must allow callback-only completion for backend-owned OAuth state")
	}
	if !strings.Contains(waiter, "emitOAuthError") {
		t.Fatalf("automatic OAuth waiter must still emit errors when capture closes without callback")
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

func TestFrontendNewAPIOAuthErrorAndTimeoutResetWaitingState(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	for _, want := range []string{
		"scheduleOAuthWaitTimeout",
		"clearOAuthWaitTimer",
		"NewAPI 自动登录超时",
		`state.busy = false;`,
		`state.oauthMessage = "";`,
		`state.formError = "";`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("NewAPI OAuth waiting/error state must include %q", want)
		}
	}
}

func TestFrontendAuthRequiredRefreshForcesLoggedOutLoginModal(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	if !strings.Contains(js, "isAuthRequiredMessage") ||
		!strings.Contains(js, `loggedIn: false`) ||
		!strings.Contains(js, `state.modal = "login";`) {
		t.Fatalf("refresh auth failures must force a logged-out login state")
	}
	if !strings.Contains(js, "loggedOutSnapshotError") {
		t.Fatalf("logged-out login view should surface backend auth errors")
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
	end := strings.Index(goSrc[start:], "func (a *App) beginRefreshOperation")
	if end < 0 {
		t.Fatalf("syncGlass test could not find function boundary")
	}
	syncGlass := goSrc[start : start+end]
	if !strings.Contains(syncGlass, "snap.LoggedIn") {
		t.Fatalf("glass ball visibility must depend on the snapshot logged-in state")
	}
	if !strings.Contains(syncGlass, "show := snap.LoggedIn && (snap.Provider == config.ProviderNewAPI || enabled)") {
		t.Fatalf("glass ball should always show for logged-in NewAPI and otherwise follow the glass setting")
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

func TestBackendWindowSizeMatchesAuthState(t *testing.T) {
	w, h := windowSizeForSnapshot(krill.Snapshot{LoggedIn: false})
	if w != loginWindowWidth || h != loginWindowHeight {
		t.Fatalf("logged-out window size = %dx%d, want %dx%d", w, h, loginWindowWidth, loginWindowHeight)
	}

	w, h = windowSizeForSnapshot(krill.Snapshot{LoggedIn: true})
	if w != panelWidth || h != panelHeight {
		t.Fatalf("logged-in window size = %dx%d, want %dx%d", w, h, panelWidth, panelHeight)
	}
}

func TestSnapshotDTOIncludesProviderForFrontendBranching(t *testing.T) {
	dto := snapshotDTO(krill.Snapshot{Provider: "newapi", LoggedIn: true, OK: true})
	if dto.Provider != "newapi" {
		t.Fatalf("SnapshotDTO.Provider = %q, want newapi", dto.Provider)
	}
}

func TestFrontendLoggedOutRootUsesLoginDimensions(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/main.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	if !strings.Contains(css, ".login-root") ||
		!strings.Contains(css, "width: 446px") ||
		!strings.Contains(css, "height: 486px") {
		t.Fatalf("logged-out root must match compact login window dimensions")
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
