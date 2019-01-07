package seccomp

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	// BlockedSyscalls are syscalls blocked from use by dyno applications.
	BlockedSyscalls = []int32{
		syscall.SYS_UNSHARE,
	}

	prog *syscall.SockFprog
)

const seccompSetModeFilter = 0x1

// Load sets the seccomp syscall-blocking program for the local system thread.
func Load() error {
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, seccompSetModeFilter, 0, uintptr(unsafe.Pointer(prog)))
	if errno == syscall.Errno(0) {
		return nil
	}
	return errno
}
