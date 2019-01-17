//+build integration

// http://peter.bourgon.org/go-in-production/#testing-and-validation

package networking

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"net"
	"runtime"
	"strings"
	"testing"
)

func TestNetworkAddTUN(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	network := &Network{
		Subnet: &net.IPNet{
			IP:   net.IPv4(192, 168, 1, 0).To4(),
			Mask: net.CIDRMask(24, 32),
		},
		Gateway: net.IPv4(192, 168, 1, 1).To4(),
	}

	if err := network.Setup(); err != nil {
		t.Fatal(err)
	}

	if err := network.AddTUN("dyno0", net.IPv4(192, 168, 1, 42)); err != nil {
		t.Fatal(err)
	}

	routes, err := parseRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if want, got := 2, len(routes); want != got {
		t.Fatalf("want %d routes, got %d", want, got)
	}

	// test default route is up & gateway

	var (
		hexFlags   = "0003"     // RTF_UP (0x0001) | RTF_GATEWAY (0x0002)
		hexDST     = "00000000" // 0.0.0.0
		hexMask    = "00000000" // /0 (0.0.0.0)
		hexGateway = "0101A8C0" // 1.1.168.192
	)

	if want, got := "dyno0", routes[0]["Iface"]; want != got {
		t.Errorf("want route for interface %q, got %q", want, got)
	}
	if want, got := hexFlags, routes[0]["Flags"]; want != got {
		t.Errorf("want route with flags %q, got %q", want, got)
	}
	if want, got := hexDST, routes[0]["Destination"]; want != got {
		t.Errorf("want route with destination %q, got %q", want, got)
	}
	if want, got := hexMask, routes[0]["Mask"]; want != got {
		t.Errorf("want route with mask %q, got %q", want, got)
	}
	if want, got := hexGateway, routes[0]["Gateway"]; want != got {
		t.Errorf("want route with gateway %q, got %q", want, got)
	}

	// test local subnet route is up

	hexFlags = "0001"    // RTF_UP (0x0001)
	hexDST = "0001A8C0"  // 0.1.168.192
	hexMask = "00FFFFFF" // /24 (0.255.255.255)

	if want, got := "dyno0", routes[1]["Iface"]; want != got {
		t.Errorf("want route for interface %q, got %q", want, got)
	}
	if want, got := hexFlags, routes[1]["Flags"]; want != got {
		t.Errorf("want route with flags %q, got %q", want, got)
	}
	if want, got := hexDST, routes[1]["Destination"]; want != got {
		t.Errorf("want route with destination %q, got %q", want, got)
	}
	if want, got := hexMask, routes[1]["Mask"]; want != got {
		t.Errorf("want route with mask %q, got %q", want, got)
	}
}

func parseRoutes() ([]map[string]string, error) {
	data, err := ioutil.ReadFile("/proc/self/net/route")
	if err != nil {
		return nil, err
	}

	return parseFields(data, 1)
}

func parseFields(data []byte, hdrLine int) ([]map[string]string, error) {
	scanner := bufio.NewScanner(bytes.NewBuffer(data))

	for i := 0; i < hdrLine; i++ {
		if !scanner.Scan() {
			return nil, scanner.Err()
		}
	}

	header := scanner.Text()
	keys := strings.Fields(header)

	var ms []map[string]string
	for scanner.Scan() {
		m := make(map[string]string)
		for i, val := range strings.Fields(scanner.Text()) {
			m[keys[i]] = val
		}
		ms = append(ms, m)
	}
	return ms, scanner.Err()
}
