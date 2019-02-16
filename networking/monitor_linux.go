package networking

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Setup performs the thread-local initialization for monitoring the current
// network namespace.
func (m *Monitor) Setup() error {
	pid, tid := strconv.Itoa(syscall.Getpid()), strconv.Itoa(syscall.Gettid())
	procNetDir := filepath.Join("/proc", pid, "task", tid, "net")

	var err error
	if m.procTCP, err = os.Open(filepath.Join(procNetDir, "tcp")); err != nil {
		return err
	}
	if m.procTCP6, err = os.Open(filepath.Join(procNetDir, "tcp6")); err != nil {
		return err
	}

	m.donec = make(chan struct{})
	return nil
}
