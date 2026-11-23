package scan

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The tests scan 127.0.0.x only, with fake listeners on ports of their own. Nothing leaves the
// computer.

func freeTCP(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func freeUDP(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// fakePong answers every presence ping on ip:port with a pong; it returns a function that reads the number of pings.
func fakePong(t *testing.T, ip string, port int) func() int {
	t.Helper()
	return fakePongWith(t, ip, port, 0x20)
}

// fakePongWith is fakePong with the given Supported Interactions byte: 0x20 is an AMT device
// (DASH), 0x00 an IPMI BMC.
func fakePongWith(t *testing.T, ip string, port int, interactions byte) func() int {
	t.Helper()
	c, err := net.ListenPacket("udp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		t.Skip("cannot bind", ip, err)
	}
	t.Cleanup(func() { c.Close() })
	var pings int
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			if n != 12 || buf[8] != 0x80 {
				continue
			}
			mu.Lock()
			pings++
			mu.Unlock()
			pong := []byte{0x06, 0x00, 0xff, 0x06, 0x00, 0x00, 0x11, 0xbe, 0x40, buf[9], 0x00, 0x10,
				0x00, 0x00, 0x11, 0xbe, 0, 0, 0, 0, 0x81, interactions, 0, 0, 0, 0, 0, 0}
			c.WriteTo(pong, from)
		}
	}()
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return pings
	}
}

// fakeWeb serves "GET /" with the Server header on ip:port, with TLS when useTLS.
func fakeWeb(t *testing.T, ip string, port int, server string, useTLS bool) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		t.Skip("cannot bind", ip, err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", server)
		w.WriteHeader(http.StatusSeeOther)
	}))
	srv.Listener.Close()
	srv.Listener = l
	if useTLS {
		srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
}

func collect(t *testing.T, s *Scanner, targets ...string) (results []Result, progress [][2]int) {
	t.Helper()
	var addrs []netip.Addr
	for _, a := range targets {
		addrs = append(addrs, netip.MustParseAddr(a))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.Scan(ctx, addrs, func(done, total int) { progress = append(progress, [2]int{done, total}) },
		func(r Result) { results = append(results, r) })
	slices.SortFunc(results, func(a, b Result) int { return a.Addr.Compare(b.Addr) })
	return results, progress
}

func TestScanFindsDevices(t *testing.T) {
	udp, http1, https := freeUDP(t), freeTCP(t), freeTCP(t)
	for https == http1 {
		https = freeTCP(t)
	}
	// .1 is a full device (RMCP, HTTP and TLS), .2 only answers the ping (not activated),
	// .3 has an HTTP port of another product, .4 is silent, .5 has TLS only.
	pings := fakePong(t, "127.0.0.1", udp)
	fakePong(t, "127.0.0.2", udp)
	fakeWeb(t, "127.0.0.1", http1, "Intel(R) Active Management Technology 16.1.25", false)
	fakeWeb(t, "127.0.0.1", https, "Intel(R) Active Management Technology 16.1.25", true)
	fakeWeb(t, "127.0.0.3", http1, "nginx/1.25", false)
	fakeWeb(t, "127.0.0.5", https, "Intel(R) AMT 9.5.61", true)

	s := &Scanner{RMCPPort: udp, HTTPPort: http1, HTTPSPort: https, Timeout: 500 * time.Millisecond,
		Lookup: func(_ context.Context, a netip.Addr) string {
			if a.String() == "127.0.0.1" {
				return "lab.example.com"
			}
			return ""
		}}
	got, progress := collect(t, s, "127.0.0.1", "127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5")

	want := []Result{
		{Addr: netip.MustParseAddr("127.0.0.1"), Name: "lab.example.com", Version: "16.1", RMCP: true, HTTP: true, TLS: true},
		{Addr: netip.MustParseAddr("127.0.0.2"), RMCP: true},
		{Addr: netip.MustParseAddr("127.0.0.5"), Version: "9.5", TLS: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("results\n got %+v\nwant %+v", got, want)
	}
	if !got[1].PortsClosed() || got[0].PortsClosed() || got[2].PortsClosed() {
		t.Error("PortsClosed is wrong")
	}
	if n := pings(); n != 1 {
		t.Errorf("%d pings to the first device, want 1", n)
	}
	if len(progress) != 5 || progress[len(progress)-1] != [2]int{5, 5} {
		t.Errorf("progress %v, want 5 steps ending at 5 of 5", progress)
	}
}

func TestScanStops(t *testing.T) {
	s := &Scanner{RMCPPort: freeUDP(t), HTTPPort: freeTCP(t), HTTPSPort: freeTCP(t), Timeout: 200 * time.Millisecond, Workers: 1}
	ctx, cancel := context.WithCancel(context.Background())
	var targets []netip.Addr
	for i := range 200 {
		targets = append(targets, netip.AddrFrom4([4]byte{127, 0, 1, byte(i)}))
	}
	calls := 0
	s.Scan(ctx, targets, func(done, _ int) {
		calls++
		if done == 3 {
			cancel()
		}
	}, nil)
	if calls < 3 || calls > 6 {
		t.Fatalf("%d steps after Stop at step 3", calls)
	}
}

// Results and progress never run at the same time, so the caller needs no lock.
func TestScanCallbacksAreSerialized(t *testing.T) {
	udp := freeUDP(t)
	fakePong(t, "127.0.0.1", udp)
	s := &Scanner{RMCPPort: udp, HTTPPort: freeTCP(t), HTTPSPort: freeTCP(t), Timeout: 300 * time.Millisecond, Workers: 8}
	var targets []netip.Addr
	for i := range 40 {
		targets = append(targets, netip.AddrFrom4([4]byte{127, 0, 0, byte(i + 1)}))
	}
	inside := 0
	check := func() {
		inside++
		if inside != 1 {
			t.Error("callbacks overlap")
		}
		time.Sleep(time.Millisecond)
		inside--
	}
	s.Scan(context.Background(), targets, func(int, int) { check() }, func(Result) { check() })
}

func TestParseServerVersion(t *testing.T) {
	for in, want := range map[string]string{
		"Intel(R) Active Management Technology 16.1.25": "16.1",
		"Intel(R) Active Management Technology 11.8.92": "11.8",
		"Intel(R) AMT 9.5.61":                           "9.5",
		"Intel(R) Active Management Technology":         "",
		"nginx/1.25.3":                                  "",
		"":                                              "",
	} {
		if got := ParseServerVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestIsPong(t *testing.T) {
	pong := []byte{0x06, 0x00, 0xff, 0x06, 0x00, 0x00, 0x11, 0xbe, 0x40, 0, 0, 0x10,
		0x00, 0x00, 0x11, 0xbe, 0, 0, 0, 0, 0x81, 0x20, 0, 0, 0, 0, 0, 0}
	if !isPong(pong) {
		t.Fatal("a DASH pong is not recognized")
	}
	ping := slices.Clone(pong)
	ping[8] = 0x80
	ipmi := slices.Clone(pong)
	ipmi[21] = 0x00
	for name, b := range map[string][]byte{"ping": ping, "short": pong[:8], "no DASH bit (IPMI)": ipmi,
		"cut before the interactions byte": pong[:21], "empty": nil} {
		if isPong(b) {
			t.Errorf("%s counts as a pong", name)
		}
	}
}

func TestNetworkOf(t *testing.T) {
	_, small, _ := net.ParseCIDR("192.0.2.0/24")
	_, mid, _ := net.ParseCIDR("198.51.100.0/22")
	_, big, _ := net.ParseCIDR("203.0.0.0/8")
	nets := []*net.IPNet{small, mid, big}
	for in, want := range map[string]string{"192.0.2.77": "192.0.2.0/24", "198.51.101.9": "198.51.100.0/22", "203.0.113.7": "203.0.113.0/24"} {
		if got, ok := networkOf(netip.MustParseAddr(in), nets); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := networkOf(netip.MustParseAddr("198.51.99.1"), nets); ok {
		t.Error("an address outside every network found one")
	}
}

// A peer that sends endless headers is cut off at maxHeaderBytes instead of being read forever.
func TestServerHeaderReadIsBounded(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer l.Close()
	sent := make(chan int, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.Write([]byte("HTTP/1.1 200 OK\r\n"))
		n := 0
		line := []byte("X-Fill: " + string(make([]byte, 1000)) + "\r\n")
		c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		for n < 1<<20 {
			w, err := c.Write(line)
			n += w
			if err != nil {
				break
			}
		}
		sent <- n
	}()
	s := &Scanner{Timeout: time.Second}
	port := l.Addr().(*net.TCPAddr).Port
	if got := s.serverHeader(context.Background(), netip.MustParseAddr("127.0.0.1"), port, false); got != "" {
		t.Errorf("Server header %q from an endless response", got)
	}
}

// IPMI BMCs answer the presence ping with IANA 4542, entities 0x81 and interactions 0x00. They are
// not listed; a DASH pong without open ports is; any pong with an open port is.
func TestScanFiltersIPMIPongs(t *testing.T) {
	udp, http1, https := freeUDP(t), freeTCP(t), freeTCP(t)
	for https == http1 {
		https = freeTCP(t)
	}
	fakePongWith(t, "127.0.0.1", udp, 0x00) // IPMI BMC, no ports
	fakePongWith(t, "127.0.0.2", udp, 0x20) // DASH, no ports
	fakePongWith(t, "127.0.0.3", udp, 0x00) // IPMI-style pong with an AMT port
	fakeWeb(t, "127.0.0.3", http1, "Intel(R) Active Management Technology 16.1.25", false)

	s := &Scanner{RMCPPort: udp, HTTPPort: http1, HTTPSPort: https, Timeout: 500 * time.Millisecond,
		Lookup: func(context.Context, netip.Addr) string { return "" }}
	got, _ := collect(t, s, "127.0.0.1", "127.0.0.2", "127.0.0.3")
	want := []Result{
		{Addr: netip.MustParseAddr("127.0.0.2"), RMCP: true},
		{Addr: netip.MustParseAddr("127.0.0.3"), Version: "16.1", HTTP: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("results\n got %+v\nwant %+v", got, want)
	}
	if !got[0].PortsClosed() || got[1].PortsClosed() {
		t.Error("PortsClosed is wrong")
	}
}
