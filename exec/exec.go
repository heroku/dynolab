package exec

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joeshaw/envdecode"
)

var forwardedSignals = []os.Signal{
	syscall.SIGHUP,
	syscall.SIGINT,
	syscall.SIGQUIT,
	syscall.SIGKILL,
	syscall.SIGTERM,
	syscall.SIGSTOP,
	syscall.SIGTSTP,
	syscall.SIGCHLD,
	syscall.SIGCONT,
}

// CleanedEnv returns a subset of the environment env without the
// environment variables marked as configuration variables via
// envdecode style struct field tags.
func CleanedEnv(target interface{}, env []string) ([]string, error) {
	configInfos, err := envdecode.Export(target)
	if err != nil {
		return nil, err
	}

	var banned sort.StringSlice
	for _, ci := range configInfos {
		banned = append(banned, ci.EnvVar)
	}
	banned.Sort()

	var cleaned []string
	for _, envVar := range env {
		key := strings.Split(envVar, "=")[0]
		if idx := banned.Search(key); idx == len(banned) || banned[idx] != key {
			cleaned = append(cleaned, envVar)
		}
	}
	return cleaned, nil
}

// Dyno manages a dyno container process group.
type Dyno struct {
	CommandLine []string

	Dir string
	Env []string

	ShutdownPeriod time.Duration

	UID, GID     int
	Capabilities []string
	LoadSeccomp  bool

	AddProcHidepidFlag bool

	Stdin          io.Reader
	Stdout, Stderr io.WriteCloser

	cmd  *exec.Cmd
	sigc chan os.Signal
}

// Start launches a dyno process group.
func (d *Dyno) Start() error {
	dir := d.Dir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}

	env := d.Env
	if env == nil {
		env = os.Environ()
	}

	d.cmd = exec.Command(d.CommandLine[0], d.CommandLine[1:]...)
	d.cmd.Dir, d.cmd.Env = dir, env
	d.cmd.Stdin, d.cmd.Stdout, d.cmd.Stderr = d.Stdin, d.Stdout, d.Stderr
	d.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	d.sigc = make(chan os.Signal, 32)
	signal.Notify(d.sigc, forwardedSignals...)

	if err := d.start(); err != nil {
		signal.Stop(d.sigc)
		return err
	}
	return nil
}

// Run blocks until the dyno process group has exited and returns
// the exit code as an ExitCode error.
func (d *Dyno) Run() error {
	if d.Stderr != nil {
		defer d.Stderr.Close()
	}
	if d.Stdout != nil {
		defer d.Stdout.Close()
	}

	err := d.wait()
	if _, ok := err.(*exec.ExitError); err == nil || ok {
		return ExitCode(d.ExitCode())
	}
	return err
}

func (d *Dyno) wait() error {
	defer signal.Stop(d.sigc)

	errc := make(chan error)
	go func() { errc <- d.cmd.Wait() }()

	var shutdownc <-chan time.Time
	for {
		select {
		case err := <-errc:
			return err
		case sig := <-d.sigc:
			if sig == syscall.SIGCHLD {
				if err := d.reap(); err != nil {
					return err
				}
				continue
			}

			if !d.kill(sig.(syscall.Signal)) {
				return <-errc
			}

			if d.ShutdownPeriod > 0 && sig == os.Signal(syscall.SIGTERM) {
				shutdownc = time.After(d.ShutdownPeriod)
			}
		case <-shutdownc:
			if !d.kill(syscall.SIGKILL) {
				return <-errc
			}
			shutdownc = nil
		}
	}
}

func (d *Dyno) kill(sig syscall.Signal) bool {
	if err := syscall.Kill(-d.cmd.Process.Pid, sig); err != nil {
		switch {
		case err == syscall.ESRCH:
		case err.Error() != "os: process already finished":
		default:
			panic("impossible")
		}
		return false
	}
	return true
}

// Stop sends a kill signal to the dyno process group. Dyno
// processes may still be running after Stop returns.
func (d *Dyno) Stop(error) {
	d.sigc <- syscall.SIGTERM
}

// ExitCode is the exit code of the parent process in the dyno
// process group.
func (d *Dyno) ExitCode() ExitCode {
	return ExitCode(d.cmd.ProcessState.Sys().(syscall.WaitStatus))
}

// ExitCode is an error exit code.
type ExitCode int

func (c ExitCode) Error() string { return "exit " + strconv.Itoa(int(c)) }
