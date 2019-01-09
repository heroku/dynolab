package logging

import (
	"io"
	"log"
	"os"

	shuttle "github.com/heroku/log-shuttle"
)

// Forwarder sends logline data to a remote logging service. Only
// Logplex/log-shuttle style logdrain services are currently supported.
type Forwarder struct {
	LogdrainURL string
	// TODO: OtherServiceURL string

	AppName, AppID string
	ProcessID      string

	rcs   []io.ReadCloser
	donec chan struct{}
}

// Forward sends loglines read from rc to a logging service.
func (f *Forwarder) Forward(rc io.ReadCloser) {
	if f.donec == nil {
		f.donec = make(chan struct{})
	}

	f.rcs = append(f.rcs, rc)
}

// Run forwards logs to a logging service.
func (f *Forwarder) Run() error {
	cfg := shuttle.NewConfig()
	cfg.LogsURL = f.LogdrainURL
	cfg.Appname = f.AppName
	cfg.Hostname = f.AppID
	cfg.Procid = f.ProcessID
	cfg.ComputeHeader()

	ls := shuttle.NewShuttle(cfg)
	for _, rc := range f.rcs {
		ls.LoadReader(rc)
	}

	// TODO: wire up ls.ErrLogger, parse & return log-shuttle error
	ls.ErrLogger = log.New(os.Stdout, "log-shuttle", 0)

	ls.Launch()
	<-f.donec
	ls.Land()

	return nil
}

// Stop interrupts f.
func (f *Forwarder) Stop(err error) { close(f.donec) }
