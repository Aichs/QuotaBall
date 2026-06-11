package wailsui

import "testing"

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
