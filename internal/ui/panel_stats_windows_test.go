//go:build windows

package ui

import (
	"testing"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

func TestPanelStatCardsUseNewAPIBalanceSpendRequests(t *testing.T) {
	cards := panelStatCards(krill.Snapshot{
		Provider: config.ProviderNewAPI,
		Wallet:   1.8,
		Spend:    0.2,
		Req:      "618",
	})

	if len(cards) != 3 {
		t.Fatalf("NewAPI panel cards = %d, want 3", len(cards))
	}
	want := []struct {
		title string
		value string
	}{
		{"当前余额", "$1.80"},
		{"历史消耗", "$0.20"},
		{"请求次数", "618"},
	}
	for i, expected := range want {
		if cards[i].title != expected.title || cards[i].value != expected.value {
			t.Fatalf("card %d = (%q, %q), want (%q, %q)", i, cards[i].title, cards[i].value, expected.title, expected.value)
		}
	}
}

func TestPanelStatCardsKeepKrillWeeklyMonthlyQuota(t *testing.T) {
	cards := panelStatCards(krill.Snapshot{
		Spend:  300,
		Wallet: 12,
		Summary: krill.Summary{
			TotalWeeklyQuotaUSD:      600,
			TotalWeeklyRemainingUSD:  300,
			TotalMonthlyQuotaUSD:     2400,
			TotalMonthlyUsedUSD:      600,
			TotalMonthlyRemainingUSD: 1800,
		},
	})

	if len(cards) != 4 {
		t.Fatalf("Krill panel cards = %d, want 4", len(cards))
	}
	if cards[0].title != "本周剩余" || cards[2].title != "周额度" || cards[3].title != "月总额度" {
		t.Fatalf("Krill panel card labels changed: %+v", cards)
	}
}
