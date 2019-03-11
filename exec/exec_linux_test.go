//+build integration

// http://peter.bourgon.org/go-in-production/#testing-and-validation

package exec

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pkg/errors"
)

func TestDynoInit(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			`{ { cat /proc/self/status & } & } &`,
		},

		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		defer close(errc)

		if want, got := ExitCode(0), dyno.Run(); want != got {
			errc <- errors.Errorf("want dyno to exit %q, got %q", want, got)
		}
	}()

	data, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}

	var ppid int

	scanner := bufio.NewScanner(bytes.NewBuffer(data))
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "PPid:\t") {
			if ppid, err = strconv.Atoi(line[6:]); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if ppid == 0 {
		t.Fatal("unexpected output from /proc/self/status")
	}
	if want, got := os.Getpid(), ppid; want != got {
		t.Errorf("want ppid of double-fork %d, got %d", want, got)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDynoLoadSeccomp(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			`unshare -U whoami`,
		},

		LoadSeccomp: true,

		Stdout: pw,
		Stderr: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		defer close(errc)
		errc <- dyno.Run()
	}()

	data, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "nobody\n" {
		t.Error("unshare syscall was not blocked by seccomp")
	}

	if want, got := ExitCode(0x100), <-errc; want != got {
		t.Fatalf("want exit code %q, got %q", want, got)
	}
}

func TestDynoCapabilities(t *testing.T) {
	pr, pw := io.Pipe()

	capMask := int64(1<<capKill) | int64(1<<capNetBindService)

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			`cat /proc/self/status`,
		},

		Capabilities: []string{
			"CAP_KILL",
			"CAP_NET_BIND_SERVICE",
		},

		Stdout: pw,
		Stderr: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		defer close(errc)
		errc <- dyno.Run()
	}()

	data, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(data))
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "Cap") {
			mask, err := strconv.ParseInt(line[8:], 16, 64)
			if err != nil {
				t.Fatal(err)
			}

			if want, got := capMask, mask; want != got {
				t.Errorf("want capability mask %x, got %x", want, got)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if want, got := ExitCode(0), <-errc; want != got {
		t.Fatalf("want exit code %q, got %q", want, got)
	}
}

func TestDynoUIDGID(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			`id`,
		},

		UID: 65534, // nobody
		GID: 65534, // nogroup

		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		defer close(errc)

		if want, got := ExitCode(0), dyno.Run(); want != got {
			errc <- errors.Errorf("want dyno to exit %q, got %q", want, got)
		}
	}()

	data, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}

	id := "uid=65534(nobody) gid=65534(nogroup) groups=65534(nogroup)"
	if want, got := id, string(data[:len(data)-1]); want != got {
		t.Errorf("want uid/gid %q, got %q", want, got)
	}
}

func TestDynoProcHidepidFlag(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			`ls /proc/[1-9]*/status`,
		},

		UID: 65534,
		GID: 65534,

		AddProcHidepidFlag: true,

		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		defer close(errc)

		if want, got := ExitCode(0), dyno.Run(); want != got {
			errc <- errors.Errorf("want dyno to exit %q, got %q", want, got)
		}
	}()

	data, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}

	if want, got := 1, strings.Count(string(data), "\n"); want != got {
		t.Errorf("want %d visible processes, got %d", want, got)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}
