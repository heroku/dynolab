package networking

import (
	"io"
	"net"
)

// NAT proxies egress connections from an internal network to an external
// network. Internal connections arrive over EgressListener and the
// corresponding external connection is created via EgressDial.
type NAT struct {
	EgressListener net.Listener
	EgressDial     func(net.Addr) (net.Conn, error)
}

// Run proxies connections from an internal to an external network.
func (n *NAT) Run() error {
	for {
		conn, err := n.EgressListener.Accept()
		if err != nil {
			return err
		}

		go n.forward(conn)
	}
}

// Stop interrupts n.
func (n *NAT) Stop(err error) {}

func (n *NAT) forward(client net.Conn) {
	server, err := n.EgressDial(client.LocalAddr())
	if err != nil {
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			// drop the client connection, which will propegate the time out
			// without establishing the connection (finishing the 3-way handshake).
			return
		}

		client.Close() // send RST during handshake to the dyno
		return
	}

	if err := proxy(client, server); err != nil {
		panic("TODO: figure out how to handle: " + err.Error())
	}
}

func proxy(c1, c2 net.Conn) error {
	copyFn := func(w io.WriteCloser, r io.Reader) error {
		defer w.Close()

		if _, err := io.Copy(w, r); err != nil && !isReadOnClosingConn(err) {
			// if the other side closes the writer paired to this reader, a
			// Read may return a poll.ErrNetClosing error.
			return err
		}
		return nil
	}

	errc := make(chan error)
	go func() { errc <- copyFn(c1, c2) }()
	go func() { errc <- copyFn(c2, c1) }()

	if err := <-errc; err != nil {
		// TODO: drain errc?
		return err
	}
	return <-errc
}

func isReadOnClosingConn(err error) bool {
	nerr, ok := err.(*net.OpError)
	return ok && nerr.Op == "read" && nerr.Err.Error() == "use of closed network connection"
}
