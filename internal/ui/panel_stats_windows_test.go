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

func TestPanelStatCardsUseSub2BalanceDailyWeeklyMonthlyQuota(t *testing.T) {
	cards := panelStatCards(krill.Snapshot{
		Provider: config.ProviderSub2,
		Wallet:   8.5,
		Summary: krill.Summary{
			TotalDailyQuotaUSD:       100,
			TotalDailyUsedUSD:        25,
			TotalDailyRemainingUSD:   75,
			TotalWeeklyQuotaUSD:      700,
			TotalWeeklyUsedUSD:       140,
			TotalWeeklyRemainingUSD:  560,
			TotalMonthlyQuotaUSD:     3000,
			TotalMonthlyUsedUSD:      1500,
			TotalMonthlyRemainingUSD: 1500,
		},
	})

	if len(cards) != 4 {
		t.Fatalf("Sub2 panel cards = %d, want 4", len(cards))
	}
	want := []string{"账户余额", "今日剩余", "本周剩余", "本月剩余"}
	for i, title := range want {
		if cards[i].title != title {
			t.Fatalf("card %d title = %q, want %q", i, cards[i].title, title)
		}
	}
	if cards[1].value != "$75.00" || cards[2].value != "$560.00" || cards[3].value != "$1,500.00" {
		t.Fatalf("Sub2 remaining values = %#v", cards)
	}
}
