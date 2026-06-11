//go:build windows

package wailsui

import (
	"os"
	"syscall"
	"time"

	"github.com/lxn/win"
)

func hideMainWindowFromTaskbar() {
	title, err := syscall.UTF16PtrFromString("Krill AI 额度监控")
	if err != nil {
		return
	}
	pid := uint32(os.Getpid())
	for i := 0; i < 20; i++ {
		hwnd := win.FindWindow(nil, title)
		if hwnd != 0 && windowBelongsToProcess(hwnd, pid) {
			hideWindowFromTaskbar(hwnd)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func windowBelongsToProcess(hwnd win.HWND, pid uint32) bool {
	var owner uint32
	win.GetWindowThreadProcessId(hwnd, &owner)
	return owner == pid
}

func hideWindowFromTaskbar(hwnd win.HWND) {
	wasVisible := win.IsWindowVisible(hwnd)
	if wasVisible {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
	ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	ex &^= int32(win.WS_EX_APPWINDOW)
	ex |= int32(win.WS_EX_TOOLWINDOW)
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex)
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
	if wasVisible {
		win.ShowWindow(hwnd, win.SW_SHOWNA)
	}
}
