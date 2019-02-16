package networking

import (
	"context"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/pkg/errors"
)

func TestBridge(t *testing.T) {
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

	lnUDP, err := bridge.Listen("udp", "192.168.1.40/29:128")
	if err != nil {
		t.Fatal(err)
	}

	lnTCP, err := bridge.Listen("tcp", "192.168.1.40/29:128")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("UDP", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		client, err := bridge.Dial(ctx, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 2)}, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 42), Port: 128})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := client.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}

		server, err := lnUDP.Accept()
		if err != nil {
			t.Fatal(err)
		}

		buf, n := make([]byte, 1024), 0
		if n, err = server.Read(buf); err != nil {
			t.Fatal(err)
		}

		if want, got := "ping", string(buf[:n]); want != got {
			t.Errorf("want msg %q, got %q", want, got)
		}

		if _, err := server.Write([]byte("pong")); err != nil {
			t.Fatal(err)
		}

		if n, err = client.Read(buf); err != nil {
			t.Fatal(err)
		}

		if want, got := "pong", string(buf[:n]); want != got {
			t.Errorf("want msg %q, got %q", want, got)
		}

		if err := lnUDP.Close(); err != nil {
			t.Fatal(err)
		}

		if _, err := lnUDP.Accept(); err != syscall.EINVAL {
			t.Errorf("want syscall.EINVAL err on closed accept, got %q", err)
		}
	})

	t.Run("TCP", func(t *testing.T) {
		t.Parallel()

		errc := make(chan error, 1)
		go func() {
			server, err := lnTCP.Accept()
			if err != nil {
				errc <- err
				return
			}
			defer server.Close()

			buf, n := make([]byte, 1024), 0
			if n, err = server.Read(buf); err != nil {
				errc <- err
				return
			}

			if want, got := "ping", string(buf[:n]); want != got {
				errc <- errors.Errorf("want msg %q, got %q", want, got)
				return
			}

			if _, err := server.Write([]byte("pong")); err != nil {
				errc <- err
				return
			}

			if err := server.Close(); err != nil {
				errc <- err
				return
			}
		}()

		go func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			client, err := bridge.Dial(ctx, &net.TCPAddr{IP: net.IPv4(192, 168, 1, 2)}, &net.TCPAddr{IP: net.IPv4(192, 168, 1, 42), Port: 128})
			if err != nil {
				errc <- err
				return
			}
			defer client.Close()

			if _, err := client.Write([]byte("ping")); err != nil {
				errc <- err
				return
			}

			buf, n := make([]byte, 1024), 0
			if n, err = client.Read(buf); err != nil {
				errc <- err
				return
			}

			if want, got := "pong", string(buf[:n]); want != got {
				errc <- errors.Errorf("want msg %q, got %q", want, got)
			}

			if _, err := client.Read(buf); err != io.EOF {
				errc <- errors.Errorf("want err %q, got %q", io.EOF, err)
			}

			if err := client.Close(); err != nil {
				errc <- err
				return
			}

			close(errc)
		}()

		if err := <-errc; err != nil {
			t.Fatal(err)
		}

		if err := lnTCP.Close(); err != nil {
			t.Fatal(err)
		}

		if _, err := lnTCP.Accept(); err != syscall.EINVAL {
			t.Errorf("want syscall.EINVAL err on closed accept, got %q", err)
		}
	})
}
