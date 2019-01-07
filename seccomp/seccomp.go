package seccomp

//go:generate go run github.com/heroku/dynolab/seccomp/_generate -o prog_linux.go

import "errors"

// ErrUnsupportedPlatform indicates the operating system does not support seccomp.
var ErrUnsupportedPlatform = errors.New("seccomp: unsupported platform")
