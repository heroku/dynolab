// +build linux

package seccomp

import (
	"runtime"
	"syscall"
	"testing"
)

func TestLoad(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		t.Skip("invalid test permissions: " + err.Error())
	}

	if err := Load(); err != nil {
		t.Fatal(err)
	}

	if want, got := syscall.EPERM, syscall.Unshare(syscall.CLONE_NEWNS); want != got {
		t.Fatalf("want error %q, got %q", want, got)
	}
}
