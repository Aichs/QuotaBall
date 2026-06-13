//go:build !windows

package ui

import "errors"

func Run() error {
	return errors.New("QuotaBall UI currently targets Windows")
}
