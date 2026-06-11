//go:build !windows

package ui

import "errors"

func Run() error {
	return errors.New("Krill Monitor Go UI currently targets Windows")
}
