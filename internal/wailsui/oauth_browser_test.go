package wailsui

import (
	"path/filepath"
	"testing"
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
