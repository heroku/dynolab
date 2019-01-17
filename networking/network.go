package networking

import (
	"errors"
	"net"

	"github.com/google/netstack/tcpip"
	"github.com/google/netstack/tcpip/link/loopback"
	"github.com/google/netstack/tcpip/link/sniffer"
	"github.com/google/netstack/tcpip/network/ipv4"
	"github.com/google/netstack/tcpip/stack"
	"github.com/google/netstack/tcpip/transport/tcp"
	"github.com/google/netstack/tcpip/transport/udp"
	"github.com/vishvananda/netns"
)

var (
	networks   = []string{ipv4.ProtocolName}
	transports = []string{
		tcp.ProtocolName,
		udp.ProtocolName,
	}

	unspecifiedIPv4 = &net.IPNet{
		IP:   net.IPv4(0, 0, 0, 0).To4(),
		Mask: net.IPv4Mask(0, 0, 0, 0),
	}
)

// Network is the networking configuration and TCP/IP stack for a dyno. It
// initializes the system configuration (e.g. the namespace, interface(s), and
// route(s)) and manages the networking service.
type Network struct {
	Subnet  *net.IPNet
	Gateway net.IP
	Debug   bool

	MTU int

	TxQueueLen  int
	RxWindowLen int

	MaxEgressConnCount int

	netns netns.NsHandle
	stack *stack.Stack
	nicID tcpip.NICID

	skipNetNS bool
}

// Setup constructs the basic networking layout for dynos.
func (n *Network) Setup() error {
	if !n.Subnet.Contains(n.Gateway) {
		return errors.New("gateway is not part of subnet")
	}
	if int(uint32(n.MTU)) != n.MTU {
		return errors.New("invalid MTU")
	}

	if n.MaxEgressConnCount == 0 {
		n.MaxEgressConnCount = 1 << 20
	}

	if err := n.setup(); err != nil {
		return err
	}

	n.stack = stack.New(networks, transports, stack.Options{})

	return nil
}

// AddLoopback attaches a loopback interface to the network for spoofing
// ingress connections and forwarding egress connections.
func (n *Network) AddLoopback() error {
	linkID := loopback.New()
	if n.Debug {
		linkID = sniffer.New(linkID)
	}

	n.nicID++
	if err := n.stack.CreateNIC(n.nicID, linkID); err != nil {
		return errors.New(err.String())
	}

	loSubnet, err := tcpip.NewSubnet(tcpip.Address(unspecifiedIPv4.IP), tcpip.AddressMask(unspecifiedIPv4.Mask))
	if err != nil {
		panic("impossible")
	}
	if err := n.stack.AddSubnet(n.nicID, ipv4.ProtocolNumber, loSubnet); err != nil {
		return errors.New(err.String())
	}

	if err := n.stack.SetSpoofing(n.nicID, true); err != nil {
		return errors.New(err.String())
	}

	n.stack.SetRouteTable([]tcpip.Route{
		{
			Destination: tcpip.Address(unspecifiedIPv4.IP),
			Mask:        tcpip.AddressMask(unspecifiedIPv4.Mask),
			NIC:         n.nicID,
		},
	})

	return nil
}
