package supervisor

import (
	"errors"
	"testing"
	"time"
)

func TestGroupZero(t *testing.T) {
	var g Group
	res := make(chan error)
	go func() { res <- g.Run() }()
	select {
	case err := <-res:
		if err != nil {
			t.Errorf("%v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout")
	}
}

func TestGroupOne(t *testing.T) {
	myError := errors.New("foobar")
	var g Group
	if err := g.Start(func() error { return myError }, func(error) {}); err != nil {
		t.Fatal(err)
	}

	res := make(chan error)
	go func() { res <- g.Run() }()
	select {
	case err := <-res:
		if want, have := myError, err; want != have {
			t.Errorf("want %v, have %v", want, have)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout")
	}
}

func TestGroupMany(t *testing.T) {
	interrupt := errors.New("interrupt")
	donec := make(chan struct{})
	var g Group
	if err := g.Start(func() error { <-donec; return interrupt }, func(error) {}); err != nil {
		t.Fatal(err)
	}

	cancel := make(chan struct{})
	if err := g.Start(func() error { <-cancel; return nil }, func(error) { close(cancel) }); err != nil {
		t.Fatal(err)
	}

	res := make(chan error)
	go func() { res <- g.Run() }()

	close(donec)

	select {
	case err := <-res:
		if want, have := interrupt, err; want != have {
			t.Errorf("want %v, have %v", want, have)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout")
	}
}

func TestGroupStartError(t *testing.T) {
	interrupt := errors.New("interrupt")
	var g Group
	if err := g.Start(func() error { return interrupt }, func(error) {}); err != nil {
		t.Fatal(err)
	}

	// ensure g.errc has interrupt err buffered before next call to g.Start
	if err := <-g.errc; err != interrupt {
		t.Fatalf("want err %v, got %v", interrupt, err)
	}
	g.errc <- interrupt

	cancel := make(chan struct{})
	if err := g.Start(func() error { <-cancel; return nil }, func(error) { close(cancel) }); err != interrupt {
		t.Fatalf("want err %v, got %v", interrupt, err)
	}
}

func TestGroupInterruptOrder(t *testing.T) {
	interrupt := errors.New("interrupt")
	doneA, doneB, doneC := make(chan struct{}), make(chan struct{}), make(chan struct{})

	var g Group
	if err := g.Start(func() error { <-doneA; return nil }, func(error) { <-doneB; <-doneC; close(doneA) }); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(func() error { <-doneB; return nil }, func(error) { <-doneC; close(doneB) }); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(func() error { <-doneC; return nil }, func(error) { close(doneC) }); err != nil {
		t.Fatal(err)
	}

	// ensure finished, non-erroring service does not trigger interrupt

	if err := g.Start(func() error { return nil }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(func() error { return interrupt }, func(error) {}); err != nil {
		t.Fatal(err)
	}

	res := make(chan error)
	go func() { res <- g.Run() }()

	select {
	case err := <-res:
		if want, have := interrupt, err; want != have {
			t.Errorf("want %v, have %v", want, have)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout")
	}
}
