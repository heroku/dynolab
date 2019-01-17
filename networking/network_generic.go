//+build !linux

package networking

import (
	"errors"
	"net"
)

func (n *Network) setup() error {
	return nil
}

// AddTUN is unsupported on this platform.
func (n *Network) AddTUN(iface string, ip net.IP) error {
	return errors.New("networking: unsupported platform for tun")
}
