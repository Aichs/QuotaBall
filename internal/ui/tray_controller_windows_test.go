//go:build windows

package ui

import (
	"os"
	"strings"
	"testing"

	"quotaball/internal/config"
	"quotaball/internal/krill"
)

func TestTrayControllerCreatesNotifyIconAndBackgroundMenu(t *testing.T) {
	raw, err := os.ReadFile("tray_controller_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	for _, want := range []string{
		"walk.NewNotifyIcon",
		"notify.SetVisible(true)",
		"notify.MouseDown().Attach",
		"显示/隐藏面板",
		"立即刷新",
		"退出登录",
		"退出",
		"SetToolTip",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("tray controller is missing %q", want)
		}
	}
}

func TestTrayTooltipUsesNewAPIBalanceInsteadOfWeeklyQuota(t *testing.T) {
	got := trayTooltip(krill.Snapshot{
		Provider: "newapi",
		OK:       true,
		Wallet:   1.8,
		Spend:    0.2,
	})
	want := "QuotaBall - 余额 $1.80 / 消耗 $0.20"
	if got != want {
		t.Fatalf("trayTooltip = %q, want %q", got, want)
	}
}

func TestTrayTooltipUsesSub2BalanceAndMonthlyRemaining(t *testing.T) {
	got := trayTooltip(krill.Snapshot{
		Provider: config.ProviderSub2,
		OK:       true,
		Wallet:   8.5,
		Summary: krill.Summary{
			TotalMonthlyRemainingUSD: 1500,
		},
	})
	want := "QuotaBall - 余额 $8.50 / 本月剩余 $1500.00"
	if got != want {
		t.Fatalf("trayTooltip = %q, want %q", got, want)
	}
}
