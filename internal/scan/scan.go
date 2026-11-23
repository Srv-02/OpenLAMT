package scan

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The ports of Intel AMT.
const (
	PortRMCP  = 623
	PortHTTP  = 16992
	PortHTTPS = 16993
)

// Result is one device that answered.
type Result struct {
	Addr    netip.Addr
	Name    string // reverse DNS name; empty when there is none
	Version string // "16.1", from the Server header; empty when unknown
	RMCP    bool   // UDP 623 answered the presence ping
	HTTP    bool   // TCP 16992 is open
	TLS     bool   // TCP 16993 is open
}

// PortsClosed reports that only the RMCP ping answered: AMT is probably not activated.
func (r Result) PortsClosed() bool { return !r.HTTP && !r.TLS }

// Scanner probes addresses. The zero value scans the real ports; tests set the ports of fake
// listeners.
type Scanner struct {
	RMCPPort  int
	HTTPPort  int
	HTTPSPort int
	Timeout   time.Duration // per probe; 1.5 s when zero
	Workers   int           // addresses probed at the same time; 128 when zero
	// Lookup resolves an address to a name; nil uses reverse DNS.
	Lookup func(ctx context.Context, addr netip.Addr) string
}

func (s *Scanner) rmcpPort() int  { return orDefault(s.RMCPPort, PortRMCP) }
func (s *Scanner) httpPort() int  { return orDefault(s.HTTPPort, PortHTTP) }
func (s *Scanner) httpsPort() int { return orDefault(s.HTTPSPort, PortHTTPS) }

func orDefault(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}

func (s *Scanner) timeout() time.Duration {
	if s.Timeout <= 0 {
		return 1500 * time.Millisecond
	}
	return s.Timeout
}

// Scan probes targets until all are done or ctx ends. onResult gets every device found and
// onProgress the number of addresses done, both one at a time (never concurrently); either may
// be nil. It returns after the last callback.
func (s *Scanner) Scan(ctx context.Context, targets []netip.Addr, onProgress func(done, total int), onResult func(Result)) {
	workers := s.Workers
	if workers <= 0 {
		workers = 128
	}
	var mu sync.Mutex
	done := 0
	jobs := make(chan netip.Addr)
	var wg sync.WaitGroup
	for range min(workers, max(1, len(targets))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				res, ok := s.probe(ctx, a)
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				done++
				if ok && onResult != nil {
					onResult(res)
				}
				if onProgress != nil {
					onProgress(done, len(targets))
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for _, a := range targets {
		select {
		case jobs <- a:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

// probe asks one address; ok is false when it is not an AMT device.
func (s *Scanner) probe(ctx context.Context, a netip.Addr) (Result, bool) {
	r := Result{Addr: a}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); r.RMCP = s.ping(ctx, a) }()
	go func() { defer wg.Done(); r.HTTP = s.open(ctx, a, s.httpPort()) }()
	go func() { defer wg.Done(); r.TLS = s.open(ctx, a, s.httpsPort()) }()
	wg.Wait()
	if ctx.Err() != nil || !(r.RMCP || r.HTTP || r.TLS) {
		return r, false
	}
	server := ""
	if r.TLS {
		server = s.serverHeader(ctx, a, s.httpsPort(), true)
	}
	if server == "" && r.HTTP {
		server = s.serverHeader(ctx, a, s.httpPort(), false)
	}
	if server != "" && !strings.Contains(strings.ToLower(server), "intel") && !r.RMCP {
		return r, false // an open port of something else
	}
	r.Version = ParseServerVersion(server)
	r.Name = s.name(ctx, a)
	return r, true
}

func (s *Scanner) name(ctx context.Context, a netip.Addr) string {
	ctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if s.Lookup != nil {
		return s.Lookup(ctx, a)
	}
	names, err := net.DefaultResolver.LookupAddr(ctx, a.String())
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(names[0], "."))
}

func (s *Scanner) open(ctx context.Context, a netip.Addr, port int) bool {
	d := net.Dialer{Timeout: s.timeout()}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.String(), strconv.Itoa(port)))
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// presencePing is the RMCP/ASF presence ping: RMCP header (version 6, class ASF) and an ASF
// message of type 0x80 with the IANA enterprise number 4542.
var presencePing = []byte{0x06, 0x00, 0xff, 0x06, 0x00, 0x00, 0x11, 0xbe, 0x80, 0x00, 0x00, 0x00}

// isPong reports whether b is an ASF presence pong (message type 0x40).
func isPong(b []byte) bool {
	return len(b) >= 12 && b[0] == 0x06 && b[3] == 0x06 &&
		b[4] == 0x00 && b[5] == 0x00 && b[6] == 0x11 && b[7] == 0xbe && b[8] == 0x40
}

func (s *Scanner) ping(ctx context.Context, a netip.Addr) bool {
	d := net.Dialer{Timeout: s.timeout()}
	c, err := d.DialContext(ctx, "udp", net.JoinHostPort(a.String(), strconv.Itoa(s.rmcpPort())))
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(s.timeout()))
	if _, err := c.Write(presencePing); err != nil {
		return false
	}
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	return err == nil && isPong(buf[:n])
}

// maxHeaderBytes caps what the scan reads from a peer it does not trust: the status line and the
// headers; the body is never read.
const maxHeaderBytes = 16 << 10

// serverHeader reads the Server header of "GET /" without signing in. Nothing secret is sent,
// so the certificate of the device is not checked: the scan has no pin yet and only reads a
// banner.
func (s *Scanner) serverHeader(ctx context.Context, a netip.Addr, port int, useTLS bool) string {
	ctx, cancel := context.WithTimeout(ctx, 2*s.timeout())
	defer cancel()
	d := net.Dialer{Timeout: s.timeout()}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.String(), strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * s.timeout()))
	if useTLS {
		t := tls.Client(c, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // banner only
		if err := t.HandshakeContext(ctx); err != nil {
			return ""
		}
		c = t
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Host = a.String()
	req.Header.Set("User-Agent", "OpenLAMT")
	req.Header.Set("Connection", "close")
	if err := req.Write(c); err != nil {
		return ""
	}
	resp, err := http.ReadResponse(bufio.NewReader(io.LimitReader(c, maxHeaderBytes)), req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	return resp.Header.Get("Server")
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.\d+)*`)

// ParseServerVersion returns "major.minor" from a Server header such as "Intel(R) Active
// Management Technology 16.1.25"; empty when there is no version. Older firmware only says
// "Intel(R) AMT" with a version, newer ones may add a build number, which is dropped.
func ParseServerVersion(server string) string {
	if !strings.Contains(strings.ToLower(server), "intel") {
		return ""
	}
	m := versionRE.FindStringSubmatch(server)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// LocalNetwork returns the network of this computer to prefill the scan: the network of the
// interface that routes to the internet, as a CIDR; a network larger than MaxAddresses becomes
// the /24 around the address. ok is false when there is none. No packet is sent.
func LocalNetwork() (cidr string, ok bool) {
	c, err := net.Dial("udp4", "192.0.2.1:9") // a UDP "connect" only picks the route
	if err != nil {
		return "", false
	}
	defer c.Close()
	local, _ := c.LocalAddr().(*net.UDPAddr)
	if local == nil {
		return "", false
	}
	addr, ok := netip.AddrFromSlice(local.IP.To4())
	if !ok {
		return "", false
	}
	ifaces, _ := net.InterfaceAddrs()
	var nets []*net.IPNet
	for _, i := range ifaces {
		if n, ok := i.(*net.IPNet); ok {
			nets = append(nets, n)
		}
	}
	return networkOf(addr, nets)
}

// networkOf finds the network of addr among nets, limited to a /24 when it is larger than a /20.
func networkOf(addr netip.Addr, nets []*net.IPNet) (string, bool) {
	for _, n := range nets {
		if !n.Contains(addr.AsSlice()) {
			continue
		}
		ones, bits := n.Mask.Size()
		if bits != 32 {
			continue
		}
		if ones < 20 {
			ones = 24
		}
		return netip.PrefixFrom(addr, ones).Masked().String(), true
	}
	return "", false
}
