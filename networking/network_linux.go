package networking

import (
	"encoding/binary"
	"errors"
	"net"

	"github.com/google/netstack/tcpip"
	"github.com/google/netstack/tcpip/link/fdbased"
	"github.com/google/netstack/tcpip/link/sniffer"
	"github.com/google/netstack/tcpip/link/tun"
	"github.com/google/netstack/tcpip/network/ipv4"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func (n *Network) setup() error {
	if n.skipNetNS {
		return nil
	}

	var err error
	if n.netns, err = netns.New(); err != nil {
		return err
	}
	if err := netns.Set(n.netns); err != nil {
		return err
	}
	return n.netns.Close()
}

// AddTUN attaches a tun interface device to the network and registers the FD
// side into n's tcpip stack.
func (n *Network) AddTUN(iface string, ip net.IP) error {
	if !n.Subnet.Contains(ip) {
		return errors.New("ip address is not part of subnet")
	}

	tuntap := &netlink.Tuntap{
		LinkAttrs: netlink.LinkAttrs{
			Name:   iface,
			MTU:    n.MTU,
			TxQLen: n.TxQueueLen,
		},
		Mode:  netlink.TUNTAP_MODE_TUN,
		Flags: netlink.TUNTAP_DEFAULTS,
	}

	if err := netlink.LinkAdd(tuntap); err != nil {
		return err
	}

	bcast := make(net.IP, 4)
	binary.BigEndian.PutUint32(bcast, binary.BigEndian.Uint32(ip)|^binary.BigEndian.Uint32(n.Subnet.Mask))

	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: n.Subnet.Mask,
		},
		Peer: &net.IPNet{
			IP:   n.Gateway,
			Mask: n.Subnet.Mask,
		},
		Broadcast: bcast,
	}

	if err := netlink.AddrAdd(tuntap, addr); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(tuntap); err != nil {
		return err
	}

	route := &netlink.Route{
		LinkIndex: tuntap.Index,
		Src:       ip,
		Gw:        n.Gateway,
	}

	if err := netlink.RouteAdd(route); err != nil {
		return err
	}

	tunFD, err := tun.Open(iface)
	if err != nil {
		return err
	}

	linkID := fdbased.New(&fdbased.Options{
		FD:  tunFD,
		MTU: uint32(n.MTU),
	})
	if n.Debug {
		linkID = sniffer.New(linkID)
	}

	n.nicID++
	if err := n.stack.CreateNIC(n.nicID, linkID); err != nil {
		return errors.New(err.String())
	}

	tunSubnet, err := tcpip.NewSubnet(tcpip.Address(unspecifiedIPv4.IP), tcpip.AddressMask(unspecifiedIPv4.Mask))
	if err != nil {
		panic("impossible")
	}
	if err := n.stack.AddSubnet(n.nicID, ipv4.ProtocolNumber, tunSubnet); err != nil {
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
