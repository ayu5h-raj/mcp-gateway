//go:build !darwin

package service

// Install returns ErrUnsupported on non-macOS platforms.
func Install(_ string) error { return ErrUnsupported }

// Uninstall returns ErrUnsupported on non-macOS platforms.
func Uninstall() error { return ErrUnsupported }
