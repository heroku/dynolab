package networking

import (
	"context"
	"io/ioutil"
	"net"
	"testing"
	"time"

	"github.com/pkg/errors"
)

func TestForwarder(t *testing.T) {
	t.Parallel()

	network := &Network{
		Subnet: &net.IPNet{
			IP:   net.IPv4(192, 168, 1, 0).To4(),
			Mask: net.CIDRMask(24, 32),
		},
		Gateway: net.IPv4(192, 168, 1, 1).To4(),

		skipNetNS: true,
	}

	if err := network.Setup(); err != nil {
		t.Fatal(err)
	}
	if err := network.AddLoopback(); err != nil {
		t.Fatal(err)
	}

	bridge := &Bridge{
		Network: network,
	}

	t.Run("UDP", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ln, err := bridge.Listen("udp", "0.0.0.0/0:512")
		if err != nil {
			t.Fatal(err)
		}

		errc := make(chan error)
		go func() {
			defer close(errc)

			conn, err := ln.Accept()
			if err != nil {
				errc <- err
				return
			}

			buf := make([]byte, 1024)
			n, err := conn.Read(buf)
			if err != nil {
				errc <- err
				return
			}
			data := buf[:n]

			if want, got := "hello", string(data); want != got {
				errc <- errors.Errorf("want data %q, got %q", want, got)
				return
			}
			if want, got := "4.3.2.1:", conn.RemoteAddr().String()[:8]; want != got {
				errc <- errors.Errorf("want remote addr %q, got %q", want, got)
				return
			}
			if not, got := ":8765", conn.RemoteAddr().String()[7:]; not == got {
				errc <- errors.Errorf("want remote addr port not %q, got %q", not, got)
			}
			if want, got := "udp", conn.RemoteAddr().Network(); want != got {
				errc <- errors.Errorf("want remote addr %q, got %q", want, got)
				return
			}
		}()

		forwarder := &Forwarder{
			Bridge: bridge,
			RemoteAddr: &net.UDPAddr{
				IP:   net.IPv4(10, 41, 42, 43),
				Port: 512,
			},
		}

		go func() {
			conn, err := forwarder.Forward(ctx, "udp", "4.3.2.1:8765")
			if err != nil {
				errc <- err
				return
			}

			if _, err := conn.Write([]byte("hello")); err != nil {
				errc <- err
				return
			}
			if err := conn.Close(); err != nil {
				errc <- err
				return
			}
		}()

		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("TCP", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ln, err := bridge.Listen("tcp", "0.0.0.0/0:256")
		if err != nil {
			t.Fatal(err)
		}

		errc := make(chan error)
		go func() {
			defer close(errc)

			conn, err := ln.Accept()
			if err != nil {
				errc <- err
				return
			}

			data, err := ioutil.ReadAll(conn)
			if err != nil {
				errc <- err
				return
			}

			if want, got := "hello", string(data); want != got {
				errc <- errors.Errorf("want data %q, got %q", want, got)
				return
			}
			if want, got := "1.2.3.4:", conn.RemoteAddr().String()[:8]; want != got {
				errc <- errors.Errorf("want remote addr %q, got %q", want, got)
				return
			}
			if not, got := ":5678", conn.RemoteAddr().String()[7:]; not == got {
				errc <- errors.Errorf("want remote addr port not %q, got %q", not, got)
			}
			if want, got := "tcp", conn.RemoteAddr().Network(); want != got {
				errc <- errors.Errorf("want remote addr %q, got %q", want, got)
				return
			}
		}()

		forwarder := &Forwarder{
			Bridge: bridge,
			RemoteAddr: &net.TCPAddr{
				IP:   net.IPv4(10, 1, 2, 42),
				Port: 256,
			},
		}

		go func() {
			conn, err := forwarder.Forward(ctx, "tcp", "1.2.3.4:5678")
			if err != nil {
				errc <- err
				return
			}

			if _, err := conn.Write([]byte("hello")); err != nil {
				errc <- err
				return
			}
			if err := conn.Close(); err != nil {
				errc <- err
				return
			}
		}()

		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		forwarder := &Forwarder{
			Bridge: bridge,
			RemoteAddr: &net.TCPAddr{
				IP:   net.IPv4(10, 11, 12, 13),
				Port: 1415,
			},
			Timeout: 1 * time.Nanosecond,
		}

		_, err := forwarder.Forward(ctx, "tcp", "172.128.16.2:80")
		if want, got := context.DeadlineExceeded, err; want != got {
			t.Fatalf("want forward error %v, got %v", want, got)
		}
	})
}
