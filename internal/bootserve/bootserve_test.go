package bootserve

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var loopback = netip.MustParseAddr("127.0.0.1")

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func image(t *testing.T, name string, size int) (string, []byte) {
	t.Helper()
	data := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// client trusts only the temporary root of s, as the device does after the root was added.
func client(t *testing.T, s *Server) *http.Client {
	t.Helper()
	root, err := x509.ParseCertificate(s.RootDER())
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func waitDone(t *testing.T, s *Server) error {
	t.Helper()
	select {
	case <-s.Done():
		return s.Err()
	case <-time.After(5 * time.Second):
		t.Fatal("server not done")
		return nil
	}
}

// TestServerServesOnceAndStops: the device loads the image over TLS with the temporary root;
// the server reports progress, completes and stops listening.
func TestServerServesOnceAndStops(t *testing.T) {
	path, data := image(t, "recovery.efi", 64*1024)
	var last atomic.Int64
	s, err := Start(Config{Path: path, Addr: loopback, Port: freePort(t), Progress: func(sent, total int64) {
		if total != int64(len(data)) {
			t.Errorf("total %d", total)
		}
		last.Store(sent)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.URL(), "https://127.0.0.1:") || !strings.HasSuffix(s.URL(), "/recovery.efi") || s.Size() != int64(len(data)) {
		t.Fatalf("url %s size %d", s.URL(), s.Size())
	}
	c := client(t, s)
	head, err := c.Head(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	select {
	case <-s.Done():
		t.Fatal("HEAD completed the download")
	default:
	}
	resp, err := c.Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(body, data) || resp.Header.Get("Content-Type") != "application/efi" {
		t.Fatalf("got %d bytes, type %q", len(body), resp.Header.Get("Content-Type"))
	}
	if err := waitDone(t, s); err != nil {
		t.Fatalf("err %v", err)
	}
	if last.Load() != int64(len(data)) {
		t.Fatalf("progress %d", last.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if conn, err := net.DialTimeout("tcp", strings.SplitN(s.URL(), "/", 4)[2], time.Second); err == nil {
		conn.Close()
		t.Fatal("still listening after the download")
	}
}

// The certificate names the chosen address; the root is a CA with the design's name.
func TestServerCertificates(t *testing.T) {
	path, _ := image(t, "recovery.iso", 16)
	s, err := Start(Config{Path: path, Addr: loopback, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, _ := x509.ParseCertificate(s.RootDER())
	if !root.IsCA || root.Subject.CommonName != RootName {
		t.Fatalf("root %v ca %v", root.Subject, root.IsCA)
	}
	conn, err := tls.Dial("tcp", strings.SplitN(s.URL(), "/", 4)[2], &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	leaf := conn.ConnectionState().PeerCertificates[0]
	conn.Close()
	if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) || leaf.IsCA {
		t.Fatalf("leaf %v", leaf.IPAddresses)
	}
	if err := leaf.CheckSignatureFrom(root); err != nil {
		t.Fatal(err)
	}
	if contentType("x.ISO") != "application/vnd.efi-iso" || contentType("x.img") != "application/octet-stream" {
		t.Fatal("content types")
	}
}

func TestServerOtherPathsAndRangesDoNotComplete(t *testing.T) {
	path, _ := image(t, "recovery.efi", 1024)
	s, err := Start(Config{Path: path, Addr: loopback, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := client(t, s)
	base := s.URL()[:strings.LastIndex(s.URL(), "/")]
	host := strings.SplitN(s.URL(), "/", 4)[2]
	for _, u := range []string{base + "/other.efi", "https://" + host + "/recovery.efi"} {
		r, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", u, r.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, s.URL(), nil)
	req.Header.Set("Range", "bytes=0-99")
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	select {
	case <-s.Done():
		t.Fatal("a range request completed the download")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestServerTimeoutAndClose(t *testing.T) {
	path, _ := image(t, "recovery.efi", 16)
	s, err := Start(Config{Path: path, Addr: loopback, Port: freePort(t), Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, s); !errors.Is(err, ErrNotDownloaded) {
		t.Fatalf("timeout: %v", err)
	}
	s, err = Start(Config{Path: path, Addr: loopback, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.Close()
	if err := waitDone(t, s); !errors.Is(err, ErrClosed) {
		t.Fatalf("close: %v", err)
	}
}

func TestServerPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	path, _ := image(t, "recovery.efi", 16)
	_, err = Start(Config{Path: path, Addr: loopback, Port: ln.Addr().(*net.TCPAddr).Port})
	var pe *PortInUseError
	if !errors.As(err, &pe) || pe.Port != ln.Addr().(*net.TCPAddr).Port {
		t.Fatalf("err %v", err)
	}
}

func TestStartRefusesBadInput(t *testing.T) {
	path, _ := image(t, "recovery.efi", 16)
	for name, c := range map[string]Config{
		"no file":    {Path: filepath.Join(t.TempDir(), "missing.efi"), Addr: loopback, Port: freePort(t)},
		"directory":  {Path: t.TempDir(), Addr: loopback, Port: freePort(t)},
		"no address": {Path: path, Port: freePort(t)},
		"bad port":   {Path: path, Addr: loopback, Port: 70000},
	} {
		if s, err := Start(c); err == nil {
			s.Close()
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRouteToLoopback(t *testing.T) {
	a, err := RouteTo("127.0.0.1")
	if err != nil || a != loopback {
		t.Fatalf("%v %v", a, err)
	}
	for _, a := range Addresses() {
		if !a.IP.Is4() || a.IP.IsLoopback() || a.Interface == "" {
			t.Fatalf("address %+v", a)
		}
	}
}
