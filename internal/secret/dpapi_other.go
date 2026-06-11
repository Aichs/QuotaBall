//go:build !windows

package secret

import "errors"

func protect(_ []byte) ([]byte, error) {
	return nil, errors.New("DPAPI secret storage is only supported on Windows")
}

func unprotect(_ []byte) ([]byte, error) {
	return nil, errors.New("DPAPI secret storage is only supported on Windows")
}
