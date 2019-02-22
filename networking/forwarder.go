package networking

import (
	"context"
	"errors"
	"net"
	"time"
)

// Forwarder establishes connections to a forward address. It is simmilar in
// function to a net.Dialer; both create network connections. However,
// Forwarder always establishes connections to RemoteAddr with a configurable
// local address.
type Forwarder struct {
	Bridge *Bridge

	RemoteAddr net.Addr

	Timeout   time.Duration
	ReusePort bool
}

// Forward connects to RemoteAddr from the address on the named network.
func (f *Forwarder) Forward(ctx context.Context, network, address string) (net.Conn, error) {
	if f.Timeout != 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}

	localAddr, err := f.resolveAddr(network, address)
	if err != nil {
		return nil, err
	}

	return f.Bridge.Dial(ctx, localAddr, f.RemoteAddr)
}

func (f *Forwarder) resolveAddr(network, address string) (net.Addr, error) {
	switch network {
	case "udp", "udp4":
		udpAddr, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			return nil, err
		}
		if !f.ReusePort {
			udpAddr.Port = 0
		}
		return udpAddr, nil
	case "tcp", "tcp4":
		tcpAddr, err := net.ResolveTCPAddr(network, address)
		if err != nil {
			return nil, err
		}
		if !f.ReusePort {
			tcpAddr.Port = 0
		}
		return tcpAddr, nil
	default:
		return nil, errors.New("forward: unknown network")
	}
}
