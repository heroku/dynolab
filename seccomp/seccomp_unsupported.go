// +build !linux

package seccomp

// Load is unsupported on this platform.
func Load() error { return ErrUnsupportedPlatform }
