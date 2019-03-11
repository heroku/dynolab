package exec

import (
	"io"
	"io/ioutil"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCleanedEnv(t *testing.T) {
	config := struct {
		SomeConfig string `env:"PRIVATE_CONFIG"`
	}{}

	env := []string{
		"FOO=BAR",
		"PRIVATE_CONFIG=XXXX",
	}

	cleanedEnv, err := CleanedEnv(&config, env)
	if err != nil {
		t.Fatal(err)
	}
	if want, got := []string{"FOO=BAR"}, cleanedEnv; !reflect.DeepEqual(want, got) {
		t.Errorf("want cleaned env %q, got %q", want, got)
	}
}

func TestDynoStop(t *testing.T) {
	dyno := &Dyno{
		CommandLine: []string{
			"/bin/sh", "-c",
			"sleep 10",
		},
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	dyno.Stop(nil)
	if want, got := ExitCode(syscall.SIGTERM), dyno.Run(); want != got {
		t.Fatalf("want stopped dyno to exit %q, got %q", want, got)
	}
}

func TestDynoOutput(t *testing.T) {
	pr, pw := io.Pipe()

	outc := make(chan string)
	errc := make(chan error)
	go func() {
		buf, err := ioutil.ReadAll(pr)
		outc <- string(buf)
		errc <- err
	}()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/sh", "-c",
			"echo hello",
		},
		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}
	if want, got := ExitCode(0), dyno.Run(); want != got {
		t.Errorf("want exit code %d, got %d", want, got)
	}

	if want, got := "hello\n", <-outc; want != got {
		t.Errorf("want output %q, got %q", want, got)
	}

	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDynoEnv(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/usr/bin/env",
		},
		Env: []string{
			"FOO=BAR",
			"BAR=BAZ",
			"BAZ=FOO",
		},

		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		errc <- dyno.Run()
	}()

	buf, err := ioutil.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	env := string(buf)

	if err := <-errc; err != ExitCode(0) {
		t.Fatal(err)
	}

	for _, envVar := range dyno.Env {
		if want := envVar; !strings.Contains(env, want) {
			t.Errorf("environ did not contain %q", want)
		}
	}
}

func TestDynoSignalForwarding(t *testing.T) {
	_, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/sh", "-c",
			"echo hello",
		},
		Stdout: pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	if want, got := ExitCode(syscall.SIGINT), dyno.Run(); want != got {
		t.Fatalf("want signal forwarded dyno to exit %q, got %q", want, got)
	}
}

func TestDynoGracefulShutdown(t *testing.T) {
	pr, pw := io.Pipe()

	dyno := &Dyno{
		CommandLine: []string{
			"/bin/bash", "-c",
			"trap '' SIGTERM ; echo 'trap initialized' ; sleep 10",
		},

		ShutdownPeriod: 100 * time.Microsecond,
		Stdout:         pw,
	}

	if err := dyno.Start(); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 128)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if want, got := "trap initialized\n", string(buf[:n]); want != got {
		t.Fatalf("want message %q, got %q", want, got)
	}

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	if want, got := ExitCode(syscall.SIGKILL), dyno.Run(); want != got {
		t.Fatalf("want graceful shutdown dyno to exit %q, got %q", want, got)
	}
}
