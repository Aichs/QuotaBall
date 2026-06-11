//go:build windows

package ui

import (
	"os"
	"strings"
	"testing"
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
