package exec

import (
	"os"
	"sort"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/heroku/dynolab/seccomp"
)

const (
	capKill           = 5
	capSetuid         = 7
	capSetpcap        = 8
	capNetBindService = 10
	capNetAdmin       = 12
	capSysAdmin       = 21
	capAuditWrite     = 29

	prCapAmbient      = 47
	prCapAmbientRaise = 2
	prCapAmbientLower = 3

	prSetChildSubreaper = 36

	pAll = 0
)

var capTable = map[string]int{
	"CAP_AUDIT_WRITE":      capAuditWrite,
	"CAP_KILL":             capKill,
	"CAP_NET_ADMIN":        capNetAdmin,
	"CAP_NET_BIND_SERVICE": capNetBindService,
	"CAP_SETUID":           capSetuid,
	"CAP_SYS_ADMIN":        capSysAdmin,
}

func (d *Dyno) start() error {
	// setup init functionality

	if err := unix.Prctl(prSetChildSubreaper, 1, 0, 0, 0); err != nil {
		return err
	}

	// drop unshare syscall via seccomp

	if d.LoadSeccomp {
		if err := seccomp.Load(); err != nil {
			return err
		}
	}

	// remount /proc with hidepid=2

	if d.AddProcHidepidFlag {
		if err := syscall.Mount("", "/proc", "proc", syscall.MS_REMOUNT, "hidepid=2"); err != nil {
			return err
		}
	}

	// drop all capabilities before re-adding

	if d.Capabilities != nil {
		var caps []int
		for _, name := range d.Capabilities {
			cap, ok := capTable[name]
			if !ok {
				panic("unknown capability: " + name)
			}
			caps = append(caps, cap)
		}
		sort.Ints(caps)

		c := struct {
			hdr struct {
				_version uint32 // nolint: unused
				pid      int
			}
			data [2]struct {
				_effective  uint32 // nolint: unused
				_permitted  uint32 // nolint: unused
				inheritable uint32
			}
		}{}

		if _, _, err := syscall.Syscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&c.hdr)), uintptr(unsafe.Pointer(nil)), 0); err != 0 {
			return err
		}
		if _, _, err := syscall.Syscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&c.hdr)), uintptr(unsafe.Pointer(&c.data[0])), 0); err != 0 {
			return err
		}

		c.data[0].inheritable = 0x0
		c.data[1].inheritable = 0x0

		for cap := 0; true; cap++ {
			if idx := sort.SearchInts(caps, cap); idx < len(caps) && caps[idx] == cap {
				if err := unix.Prctl(prCapAmbient, prCapAmbientRaise, uintptr(cap), 0, 0); err != nil {
					return err
				}

				if idx = 0; cap >= 32 {
					idx = 1
				}
				c.data[idx].inheritable |= uint32(1 << uint(cap))

				continue
			}

			if err := unix.Prctl(prCapAmbient, prCapAmbientLower, uintptr(cap), 0, 0); err != nil {
				if err == syscall.EINVAL {
					break
				}
				return err
			}

			if cap != capSetpcap {
				if err := unix.Prctl(syscall.PR_CAPBSET_DROP, uintptr(cap), 0, 0, 0); err != nil {
					return err
				}
			}
		}

		c.hdr.pid = syscall.Gettid()
		if _, _, err := syscall.Syscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&c.hdr)), uintptr(unsafe.Pointer(&c.data[0])), 0); err != 0 {
			return err
		}

		if idx := sort.SearchInts(caps, capSetpcap); idx == len(caps) || caps[idx] != capSetpcap {
			if err := unix.Prctl(syscall.PR_CAPBSET_DROP, capSetpcap, 0, 0, 0); err != nil {
				return err
			}
		}
	}

	// switch UID/GID

	if uid, gid := d.UID, d.GID; uid != 0 || gid != 0 {
		if uid == 0 {
			uid = os.Geteuid()
		}
		if gid == 0 {
			gid = os.Getegid()
		}

		if err := unix.Setresgid(gid, -1, -1); err != nil {
			return err
		}
		if err := unix.Setresuid(uid, -1, -1); err != nil {
			return err
		}
	}

	return d.cmd.Start()
}

func (d *Dyno) reap() error {
	// reap all zombied child processes but the entrypoint, which
	// is reaped by d.cmd.Wait()

	// see src/os/wait_waitid.go

	var (
		siginfo [16]uint64
		psig    = &siginfo[0]
	)

	_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, pAll, 0, uintptr(unsafe.Pointer(psig)), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
	if errno != 0 && errno != syscall.ECHILD {
		return errno
	}

	pid := int(siginfo[2])
	if pid == 0 || pid == d.cmd.Process.Pid {
		return nil
	}

	proc := &os.Process{
		Pid: pid,
	}

	if _, err := proc.Wait(); err != nil && err != syscall.ECHILD {
		return err
	}

	return nil
}
