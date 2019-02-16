//+build integration

// http://peter.bourgon.org/go-in-production/#testing-and-validation

package networking

import (
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestMonitor(t *testing.T) {
	tests := []struct {
		name string

		network, address string

		localAddr, remoteAddr net.Addr
	}{
		{
			name: "tcp4 INADDR_ANY",

			network: "tcp4", address: ":0",

			localAddr: &net.TCPAddr{
				IP: net.IPv4(0, 0, 0, 0).To4(),
			},
			remoteAddr: &net.TCPAddr{
				IP: net.IPv4(0, 0, 0, 0).To4(),
			},
		},
		{
			name: "tcp6 INADDR_ANY",

			network: "tcp6", address: ":0",

			localAddr: &net.TCPAddr{
				IP: net.ParseIP("::"),
			},
			remoteAddr: &net.TCPAddr{
				IP: net.ParseIP("::"),
			},
		},
		{
			name: "dual tcp INADDR_ANY",

			network: "tcp", address: ":0",

			localAddr: &net.TCPAddr{
				IP: net.ParseIP("::"),
			},
			remoteAddr: &net.TCPAddr{
				IP: net.ParseIP("::"),
			},
		},
		{
			name: "tcp4 INADDR_LOOPBACK",

			network: "tcp4", address: "127.0.0.1:0",

			localAddr: &net.TCPAddr{
				IP: net.IPv4(127, 0, 0, 1).To4(),
			},
			remoteAddr: &net.TCPAddr{
				IP: net.IPv4(0, 0, 0, 0).To4(),
			},
		},
		{
			name: "tcp6 INADDR_LOOPBACK",

			network: "tcp6", address: "[::1]:0",

			localAddr: &net.TCPAddr{
				IP: net.ParseIP("::1"),
			},
			remoteAddr: &net.TCPAddr{
				IP: net.ParseIP("::"),
			},
		},
	}

	mon := &Monitor{
		PollInterval: 1 * time.Millisecond,
	}

	if err := mon.Setup(); err != nil {
		t.Fatal(err)
	}
	sockc := mon.SocketInfoChan()

	errc := make(chan error)
	go func() { errc <- mon.Run() }()
	defer mon.Stop(nil)

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			ln, err := net.Listen(test.network, test.address)
			if err != nil {
				if isErrAddressNotAvailable(err) {
					t.Skip("EADDRNOTAVAIL")
				}
				t.Fatal(err)
			}

			var info SocketInfo
			select {
			case info = <-sockc:
			case err := <-errc:
				t.Fatal(err)
			}

			if want, got := TCPListen, info.State; want != got {
				t.Errorf("want socket info state %d, got %d", want, got)
			}

			wantHost, _, err := net.SplitHostPort(test.localAddr.String())
			if err != nil {
				t.Fatal(err)
			}

			gotHost, _, err := net.SplitHostPort(info.LocalAddr.String())
			if err != nil {
				t.Fatal(err)
			}

			if want, got := wantHost, gotHost; want != got {
				t.Errorf("want socket info local host %q, got %q", want, got)
			}
			if want, got := test.remoteAddr.String(), info.RemoteAddr.String(); want != got {
				t.Errorf("want socket info remote addr %q, got %q", want, got)
			}

			if err := ln.Close(); err != nil {
				t.Fatal(err)
			}

			select {
			case info = <-sockc:
			case err := <-errc:
				t.Fatal(err)
			}

			if want, got := TCPClosed, info.State; want != got {
				t.Errorf("want socket info state %d, got %d", want, got)
			}

			if gotHost, _, err = net.SplitHostPort(info.LocalAddr.String()); err != nil {
				t.Fatal(err)
			}

			if want, got := wantHost, gotHost; want != got {
				t.Errorf("want socket info local host %q, got %q", want, got)
			}
			if want, got := test.remoteAddr.String(), info.RemoteAddr.String(); want != got {
				t.Errorf("want socket info remote addr %q, got %q", want, got)
			}
		})
	}
}

func isErrAddressNotAvailable(err error) bool {
	if nerr, ok := err.(*net.OpError); ok {
		if oerr, ok := nerr.Err.(*os.SyscallError); ok {
			return oerr.Err == syscall.EADDRNOTAVAIL
		}
	}
	return false
}
