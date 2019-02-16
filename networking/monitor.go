package networking

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Monitor watches for changes to TCP sockets within the current network
// namespace. It polls the /proc/<pid>/task/<tid>/tcp{,6} files for changes,
// and presents the updated socket state as a SocketInfo event. Event consumers
// register and receive a channel of SocketInfo events by calling the
// SocketInfoChan method.
type Monitor struct {
	PollInterval time.Duration

	procTCP, procTCP6 *os.File

	doneo     sync.Once
	donec     chan struct{}
	sockChans []chan SocketInfo
}

// Run polls the socket state files from the procfs filesystem every
// interval, detects changes to socket states, and sends corresponding
// SocketInfo events to the registered channels.
func (m *Monitor) Run() error {
	var prevSockInfos socketInfoSet

	t := time.NewTicker(m.PollInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
		case <-m.donec:
			for _, ch := range m.sockChans {
				close(ch)
			}
			return nil
		}

		tcp4SockInfos, err := m.poll(m.procTCP, parseTCP)
		if err != nil {
			return err
		}

		tcp6SockInfos, err := m.poll(m.procTCP6, parseTCP)
		if err != nil {
			return err
		}

		sockInfos := tcp4SockInfos.union(tcp6SockInfos)

		// new sockets
		for _, si := range sockInfos.diff(prevSockInfos) {
			for _, ch := range m.sockChans {
				ch <- si
			}
		}

		// closed sockets
		for _, si := range prevSockInfos.diff(sockInfos) {
			si.State = TCPClosed
			for _, ch := range m.sockChans {
				ch <- si
			}
		}

		// TODO: updated sockets

		prevSockInfos = sockInfos
	}
}

// SocketInfoChan registers a new SocketInfo channel which receives
// events for every change in socket state.
func (m *Monitor) SocketInfoChan() <-chan SocketInfo {
	sockc := make(chan SocketInfo)
	m.sockChans = append(m.sockChans, sockc)
	return sockc
}

// Stop interrupts Run method's socket state polling.
func (m *Monitor) Stop(err error) {
	m.doneo.Do(func() { close(m.donec) })
}

type parseAddrFunc func(string) (net.Addr, error)

func (m *Monitor) poll(f *os.File, fn parseAddrFunc) (socketInfoSet, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	return parseProcNetSocket(data, fn)
}

func parseProcNetSocket(data []byte, fn parseAddrFunc) (socketInfoSet, error) {
	scanner := bufio.NewScanner(bytes.NewBuffer(data))

	if ok := scanner.Scan(); !ok {
		return nil, errors.New("empty /proc/net/tcp data")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var infos socketInfoSet
	for scanner.Scan() {
		vals := strings.Fields(scanner.Text())

		localAddr, err := fn(vals[1])
		if err != nil {
			return nil, err
		}

		remoteAddr, err := fn(vals[2])
		if err != nil {
			return nil, err
		}

		state, err := strconv.ParseUint(vals[3], 16, 16)
		if err != nil {
			return nil, err
		}

		info := SocketInfo{
			LocalAddr:  localAddr,
			RemoteAddr: remoteAddr,
			State:      SocketState(state),
		}

		infos = append(infos, info)
	}
	return infos, scanner.Err()
}

func parseTCP(addr string) (net.Addr, error) {
	ip, port, err := parseHexAddr(addr)
	if err != nil {
		return nil, err
	}

	return &net.TCPAddr{
		IP:   ip,
		Port: port,
	}, nil
}

func parseHexAddr(val string) (net.IP, int, error) {
	parts := strings.Split(val, ":")
	address, portnum := parts[0], parts[1]

	addr, err := hex.DecodeString(address)
	if err != nil {
		return nil, 0, err
	}

	ip := make(net.IP, len(addr))
	for i := len(addr) / 2; i >= 0; i-- {
		opp := len(addr) - 1 - i
		ip[i], ip[opp] = addr[opp], addr[i]
	}

	buf := make([]byte, 2)
	if _, err := hex.Decode(buf, []byte(portnum)); err != nil {
		return nil, 0, err
	}
	return ip, int(binary.BigEndian.Uint16(buf)), nil
}

// SocketState is the state of a network socket.
type SocketState int

// TCP socket states. See include/net/tcp_states.h in Linux.
const (
	TCPEstablished SocketState = iota + 1
	TCPSynSent
	TCPSynRecv
	TCPFinWait1
	TCPFinWait2
	TCPTimeWait
	TCPClose
	TCPCloseWait
	TCPLastAck
	TCPListen
	TCPClosing
	TCPNewSynRecv

	TCPClosed SocketState = -1
)

// SocketInfo is event information pertaining to a change in a network
// socket.
type SocketInfo struct {
	LocalAddr, RemoteAddr net.Addr
	State                 SocketState
}

func (s SocketInfo) id() [2]string {
	return [2]string{
		s.LocalAddr.String(),
		s.RemoteAddr.String(),
	}
}

type socketInfoSet []SocketInfo

func (s1 socketInfoSet) diff(s2 socketInfoSet) socketInfoSet {
	ids := map[[2]string]struct{}{}
	for _, v := range s2 {
		ids[v.id()] = struct{}{}
	}

	var s socketInfoSet
	for _, v := range s1 {
		if _, ok := ids[v.id()]; !ok {
			s = append(s, v)
		}
	}
	return s
}

func (s1 socketInfoSet) union(s2 socketInfoSet) socketInfoSet {
	ids := map[[2]string]struct{}{}

	var s socketInfoSet
	for _, v := range s2 {
		ids[v.id()] = struct{}{}
		s = append(s, v)
	}

	for _, v := range s1 {
		if _, ok := ids[v.id()]; !ok {
			s = append(s, v)
		}
	}
	return s
}
