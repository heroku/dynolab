package networking

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/netstack/tcpip"
	"github.com/google/netstack/tcpip/adapters/gonet"
	"github.com/google/netstack/tcpip/network/ipv4"
	"github.com/google/netstack/tcpip/transport/tcp"
	"github.com/google/netstack/tcpip/transport/udp"
	"github.com/google/netstack/waiter"
)

// Bridge connects a Network to the current process' default networking stack.
// Egress connections (created by the dyno) are forwarded to a net.Listener
// registered through the Listen method. Ingress connections are created
// with the Dial method.
type Bridge struct {
	Network *Network

	DialTimeout time.Duration
	MaxInFlight int

	routemu   sync.RWMutex
	routes    []route
	listeners []*listenerChan

	inito sync.Once
}

// Dial establish an ingress TCP or UDP connection from laddr to raddr. An
// existing endpoint should exist for raddr, otherwise:
//   * for UDP connections, all packets will be silently dropped
//   * for TCP connections, the handshake will timeout causing the Dial return an error
func (b *Bridge) Dial(ctx context.Context, laddr, raddr net.Addr) (net.Conn, error) {
	b.inito.Do(b.init)

	if laddr.Network() != raddr.Network() {
		return nil, errors.New("dial: network mismatch")
	}

	switch laddr.Network() {
	case "udp", "udp4":
		return b.dialUDP(laddr.(*net.UDPAddr), raddr.(*net.UDPAddr))
	case "tcp", "tcp4":
		return b.dialTCP(ctx, laddr.(*net.TCPAddr), raddr.(*net.TCPAddr))
	default:
		return nil, errors.New("dial: unknown network")
	}
}

func (b *Bridge) dialUDP(laddr, raddr *net.UDPAddr) (net.Conn, error) {
	srcAddr := tcpip.FullAddress{
		Addr: tcpip.Address(laddr.IP.To4()),
		Port: uint16(laddr.Port),
	}

	dstAddr := tcpip.FullAddress{
		Addr: tcpip.Address(raddr.IP.To4()),
		Port: uint16(raddr.Port),
	}

	var wq waiter.Queue
	ep, terr := b.Network.stack.NewEndpoint(udp.ProtocolNumber, ipv4.ProtocolNumber, &wq)
	if terr != nil {
		return nil, errors.New(terr.String())
	}

	if terr = ep.Bind(srcAddr, nil); terr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "bind",
			Net:  laddr.Network(),
			Addr: laddr,
			Err:  errors.New(terr.String()),
		}
	}
	if terr = ep.Connect(dstAddr); terr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "connect",
			Net:  raddr.Network(),
			Addr: raddr,
			Err:  errors.New(terr.String()),
		}
	}

	return &udpConn{
		Conn:       gonet.NewConn(&wq, ep),
		localAddr:  laddr,
		remoteAddr: raddr,
	}, nil
}

func (b *Bridge) dialTCP(ctx context.Context, laddr, raddr *net.TCPAddr) (net.Conn, error) {
	srcAddr := tcpip.FullAddress{
		Addr: tcpip.Address(laddr.IP.To4()),
		Port: uint16(laddr.Port),
	}

	dstAddr := tcpip.FullAddress{
		Addr: tcpip.Address(raddr.IP.To4()),
		Port: uint16(raddr.Port),
	}

	var wq waiter.Queue
	ep, terr := b.Network.stack.NewEndpoint(tcp.ProtocolNumber, ipv4.ProtocolNumber, &wq)
	if terr != nil {
		return nil, errors.New(terr.String())
	}

	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	wq.EventRegister(&waitEntry, waiter.EventOut)
	defer wq.EventUnregister(&waitEntry)

	if terr = ep.Bind(srcAddr, nil); terr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "bind",
			Net:  laddr.Network(),
			Addr: laddr,
			Err:  errors.New(terr.String()),
		}
	}

	if terr = ep.Connect(dstAddr); terr == tcpip.ErrConnectStarted {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notifyCh:
			terr = ep.GetSockOpt(tcpip.ErrorOption{})
		}
	}
	if terr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "connect",
			Net:  raddr.Network(),
			Addr: raddr,
			Err:  errors.New(terr.String()),
		}
	}

	return &tcpConn{
		Conn:       gonet.NewConn(&wq, ep),
		localAddr:  laddr,
		remoteAddr: raddr,
	}, nil
}

// Listen registers a network+CIDR+port combination for egress TCP or UDP
// connections. Accepted TCP connections are in the active-open (SYN_SENT)
// state, and will finish the handshake on first read or write. Closing the
// connection prior to a read/write will abort the handshake with a RST. A nop
// on the connection will result in a connection timeout in the dyno.
func (b *Bridge) Listen(network, address string) (net.Listener, error) {
	b.inito.Do(b.init)

	networks, cidr, port, err := parseNetworkAddress(network, address)
	if err != nil {
		return nil, err
	}

	b.routemu.Lock()
	defer b.routemu.Unlock()

	ln := newListenerChan(b.MaxInFlight)
	for _, network := range networks {
		b.routes = append(b.routes, route{network, cidr, port})
		b.listeners = append(b.listeners, ln)
	}
	return ln, nil
}

func (b *Bridge) init() {
	if b.MaxInFlight == 0 {
		b.MaxInFlight = 1 << 12
	}

	tcpForwarder := tcp.NewForwarder(b.Network.stack, b.Network.RxWindowLen, b.Network.MaxEgressConnCount, b.forwardTCP)
	b.Network.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := udp.NewForwarder(b.Network.stack, b.forwardUDP)
	b.Network.stack.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)
}

func (b *Bridge) forwardTCP(req *tcp.ForwarderRequest) {
	reqID := req.ID()

	dstAddr := &net.TCPAddr{
		IP:   net.IP(reqID.LocalAddress).To4(),
		Port: int(reqID.LocalPort),
	}

	srcAddr := &net.TCPAddr{
		IP:   net.IP(reqID.RemoteAddress).To4(),
		Port: int(reqID.RemotePort),
	}

	if ln, match := b.matchRoute(dstAddr.Network(), dstAddr.IP, dstAddr.Port); match {
		conn := &tcpConn{
			localAddr:  dstAddr,
			remoteAddr: srcAddr,
			req:        req,
		}

		ln.send(conn)
	}
}

func (b *Bridge) forwardUDP(req *udp.ForwarderRequest) {
	reqID := req.ID()

	dstAddr := &net.UDPAddr{
		IP:   net.IP(reqID.LocalAddress).To4(),
		Port: int(reqID.LocalPort),
	}

	srcAddr := &net.UDPAddr{
		IP:   net.IP(reqID.RemoteAddress).To4(),
		Port: int(reqID.RemotePort),
	}

	if ln, match := b.matchRoute(dstAddr.Network(), dstAddr.IP, dstAddr.Port); match {
		var wq waiter.Queue
		ep, terr := req.CreateEndpoint(&wq)
		if terr != nil {
			panic("TODO: figure out how to handle: " + terr.String())
		}

		conn := &udpConn{
			Conn:       gonet.NewConn(&wq, ep),
			localAddr:  dstAddr,
			remoteAddr: srcAddr,
		}

		ln.send(conn)
	}
}

func (b *Bridge) matchRoute(network string, ip net.IP, port int) (*listenerChan, bool) {
	b.routemu.RLock()
	defer b.routemu.RUnlock()

	for i, route := range b.routes {
		if route.network != network {
			continue
		}
		if !route.cidr.Contains(ip) {
			continue
		}
		if route.port != 0 && int(route.port) != port {
			continue
		}

		return b.listeners[i], true
	}
	return nil, false
}

type route struct {
	network string
	cidr    *net.IPNet
	port    uint16
}

type listenerChan struct {
	sync.Mutex

	ch chan net.Conn
}

func newListenerChan(size int) *listenerChan {
	return &listenerChan{
		ch: make(chan net.Conn, size),
	}
}

func (l *listenerChan) Accept() (net.Conn, error) {
	l.Lock()
	ch := l.ch
	l.Unlock()

	if ch == nil {
		return nil, syscall.EINVAL
	}

	conn, ok := <-ch
	if !ok {
		return nil, syscall.EINVAL
	}
	return conn, nil
}

func (l *listenerChan) Close() error {
	l.Lock()
	defer l.Unlock()

	close(l.ch)
	l.ch = nil
	return nil
}

func (l *listenerChan) Addr() net.Addr { return nil }

func (l *listenerChan) send(conn net.Conn) {
	l.Lock()
	defer l.Unlock()

	if l.ch != nil {
		l.ch <- conn
	}
}

type tcpConn struct {
	net.Conn

	localAddr, remoteAddr net.Addr

	req interface {
		Complete(bool)
		CreateEndpoint(*waiter.Queue) (tcpip.Endpoint, *tcpip.Error)
	}

	connecto sync.Once
}

func (c *tcpConn) Read(b []byte) (n int, err error) {
	c.connecto.Do(c.connect)
	return c.Conn.Read(b)
}

func (c *tcpConn) Write(b []byte) (n int, err error) {
	c.connecto.Do(c.connect)
	return c.Conn.Write(b)
}

func (c *tcpConn) Close() error {
	if c.Conn != nil {
		return c.Conn.Close()
	}

	c.connecto.Do(c.reset)
	return nil
}

// CloseRead shuts down the reading side of the TCP connection. Most callers
// should just use Close.
func (c *tcpConn) CloseRead() error {
	cwc, ok := c.Conn.(interface {
		CloseRead() error
	})
	if !ok {
		panic("impossible")
	}
	return cwc.CloseRead()
}

// CloseWrite shuts down the writing side of the TCP connection. Most callers
// should just use Close.
func (c *tcpConn) CloseWrite() error {
	cwc, ok := c.Conn.(interface {
		CloseWrite() error
	})
	if !ok {
		panic("impossible")
	}
	return cwc.CloseWrite()
}

func (c *tcpConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *tcpConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *tcpConn) SetDeadline(t time.Time) error {
	c.connecto.Do(c.connect)
	return c.Conn.SetDeadline(t)
}

func (c *tcpConn) SetReadDeadline(t time.Time) error {
	c.connecto.Do(c.connect)
	return c.Conn.SetReadDeadline(t)
}

func (c *tcpConn) SetWriteDeadline(t time.Time) error {
	c.connecto.Do(c.connect)
	return c.Conn.SetWriteDeadline(t)
}

func (c *tcpConn) connect() {
	if c.Conn != nil {
		return
	}

	var wq waiter.Queue
	ep, terr := c.req.CreateEndpoint(&wq)
	if terr != nil {
		panic("TODO: figure out how to handle: " + terr.String())
	}
	c.req.Complete(false)

	c.Conn = gonet.NewConn(&wq, ep)
}

func (c *tcpConn) reset() {
	// TODO: check if CreateEndpoint needs to be called for handshake RST to be sent
	c.req.Complete(true)
}

type udpConn struct {
	net.Conn

	localAddr, remoteAddr net.Addr
}

func (c *udpConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *udpConn) RemoteAddr() net.Addr { return c.remoteAddr }

func parseNetworkAddress(network, address string) ([]string, *net.IPNet, uint16, error) {
	networks := strings.Split(network, "+")

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, 0, err
	}

	_, cidr, err := net.ParseCIDR(host)
	if err != nil {
		return nil, nil, 0, err
	}

	portnum, err := strconv.Atoi(port)
	if err != nil {
		return nil, nil, 0, err
	}
	if int(uint16(portnum)) != portnum {
		return nil, nil, 0, errors.New("invalid port number")
	}

	return networks, cidr, uint16(portnum), nil
}
