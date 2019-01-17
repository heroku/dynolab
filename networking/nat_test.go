package networking

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/pkg/errors"
)

func TestNAT(t *testing.T) {
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

	lnNAT, err := bridge.Listen("tcp+udp", "0.0.0.0/0:0")
	if err != nil {
		t.Fatal(err)
	}

	nat := &NAT{
		EgressListener: lnNAT,
		EgressDial: func(addr net.Addr) (net.Conn, error) {
			return net.Dial(addr.Network(), addr.String())
		},
	}

	go nat.Run()
	defer nat.Stop(nil)

	t.Run("UDP", func(t *testing.T) {
		t.Parallel()

		server, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		srvAddr := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: server.LocalAddr().(*net.UDPAddr).Port,
		}

		client, err := bridge.Dial(ctx, &net.UDPAddr{IP: net.IPv4(192, 168, 1, 2)}, srvAddr)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := client.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 1024)
		n, addr, err := server.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if want, got := "ping", string(buf[:n]); want != got {
			t.Errorf("want msg %q, got %q", want, got)
		}

		if _, err := server.WriteTo([]byte("pong"), addr); err != nil {
			t.Fatal(err)
		}

		if n, err = client.Read(buf); err != nil {
			t.Fatal(err)
		}
		if want, got := "pong", string(buf[:n]); want != got {
			t.Errorf("want msg %q, got %q", want, got)
		}
	})

	t.Run("TCP", func(t *testing.T) {
		t.Parallel()

		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}

		errc := make(chan error, 1)
		go func() {
			server, err := ln.Accept()
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

			lnAddr := &net.TCPAddr{
				IP:   net.IPv4(127, 0, 0, 1),
				Port: ln.Addr().(*net.TCPAddr).Port,
			}

			client, err := bridge.Dial(ctx, &net.TCPAddr{IP: net.IPv4(192, 168, 1, 2)}, lnAddr)
			if err != nil {
				t.Fatal(err)
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

			close(errc)
		}()

		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	})
}
