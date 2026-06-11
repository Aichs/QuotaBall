//go:build windows

package ui

import "syscall"

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procUpdateLayeredWindow   = user32.NewProc("UpdateLayeredWindow")
)
