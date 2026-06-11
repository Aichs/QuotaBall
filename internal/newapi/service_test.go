package newapi

import (
	"testing"

	"krill_monitor/internal/config"
)

func TestServiceConfigureClearsMemoryTokenWhenBaseURLChanges(t *testing.T) {
	svc := &Service{}
	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: "https://a.example",
		RememberLogin: true,
	})

	svc.mu.Lock()
	svc.memToken = "token-for-a"
	svc.email = "a@example.com"
	svc.pending = &pendingOAuth{baseURL: "https://a.example", state: "state-a"}
	svc.mu.Unlock()

	svc.Configure(config.Config{
		Provider:      config.ProviderNewAPI,
		NewAPIBaseURL: "https://b.example",
		RememberLogin: true,
	})

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.memToken != "" || svc.email != "" || svc.pending != nil {
		t.Fatalf("Configure must clear in-memory auth when base URL changes, token=%q email=%q pending=%v", svc.memToken, svc.email, svc.pending != nil)
	}
}
