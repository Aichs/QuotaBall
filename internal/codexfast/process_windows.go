//go:build windows

package codexfast

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

func enableAutoStart(scriptPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	value := fmt.Sprintf(`powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%s"`, scriptPath)
	return key.SetStringValue(AutoStartName, value)
}

func disableAutoStart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	err = key.DeleteValue(AutoStartName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func startProxy(ctx context.Context, scriptPath, host string, port int) error {
	if err := waitForProxy(ctx, host, port, 250*time.Millisecond); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", scriptPath)
	cmd.SysProcAttr = hiddenWindowSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitForProxy(ctx, host, port, 5*time.Second)
}

func stopProxy(ctx context.Context, host string, port int) error {
	script := fmt.Sprintf(`$ErrorActionPreference = "SilentlyContinue"
Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*codex-fast-proxy.mjs*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
Get-NetTCPConnection -LocalAddress '%s' -LocalPort %d -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }`, host, port)
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script)
	cmd.SysProcAttr = hiddenWindowSysProcAttr()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stop codex fast proxy: %w: %s", err, string(output))
	}
	return nil
}

func hiddenWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
