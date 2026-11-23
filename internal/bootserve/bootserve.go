// Package bootserve is the local HTTPS server of One Click Recovery (issue #21): it serves one
// image file (.efi or .iso) to one device for the duration of one boot. It listens only on the
// address the user chose, with a fresh certificate signed by a fresh temporary root, and stops
// after the device has loaded the file completely, after a timeout, or on Close. It has no Fyne
// and no AMT dependency: the caller puts the root on the device and removes it afterwards.
package bootserve

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultPort is the port the Boot Options dialog suggests.
const DefaultPort = 8443

// DefaultTimeout is how long the server waits for the device to load the image (design: at most
// 10 minutes).
const DefaultTimeout = 10 * time.Minute

// RootName is the common name of the temporary root certificate; the device's certificate
// list shows it (design: "OpenLAMT recovery (temporary)").
const RootName = "OpenLAMT recovery (temporary)"

// ErrNotDownloaded: the timeout passed before the device had loaded the whole image.
var ErrNotDownloaded = errors.New("the device did not download the image in time")

// ErrClosed: Close ended the server before the download was complete.
var ErrClosed = errors.New("the boot server was closed")

// PortInUseError: another program listens on the port already.
type PortInUseError struct {
	Port int
	Err  error
}

func (e *PortInUseError) Error() string { return fmt.Sprintf("port %d is in use: %v", e.Port, e.Err) }
func (e *PortInUseError) Unwrap() error { return e.Err }

// Config describes one server.
type Config struct {
	Path    string     // the image file
	Addr    netip.Addr // the local address the device downloads from; the server listens only there
	Port    int        // 0: DefaultPort
	Timeout time.Duration
	// Progress is called after each written chunk with the bytes of the current download and
	// the file size; it runs on the server's goroutine. May be nil.
	Progress func(sent, total int64)
}

// Server serves one image. Its methods are safe for concurrent use.
type Server struct {
	url     string
	root    []byte
	name    string
	size    int64
	srv     *http.Server
	ln      net.Listener
	done    chan struct{}
	once    sync.Once
	err     error // set before done is closed
	sent    atomic.Int64
	timerMu sync.Mutex // guards timer: the timeout can fire before Start has stored it
	timer   *time.Timer
	cfg     Config
	path    string // the URL path of the image
	modTime time.Time
}

// Start checks the file, creates the certificates, listens on cfg.Addr and serves the file in
// the background. The port being in use is a *PortInUseError.
func Start(cfg Config) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if !cfg.Addr.IsValid() {
		return nil, errors.New("bootserve: no local address")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("bootserve: invalid port %d", cfg.Port)
	}
	st, err := os.Stat(cfg.Path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("bootserve: %s is not a file", cfg.Path)
	}
	leaf, root, err := certificates(cfg.Addr)
	if err != nil {
		return nil, err
	}
	hostPort := net.JoinHostPort(cfg.Addr.String(), strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", hostPort)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.Errno(10048)) { // 10048: WSAEADDRINUSE
			return nil, &PortInUseError{Port: cfg.Port, Err: err}
		}
		return nil, err
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		ln.Close()
		return nil, err
	}
	name := filepath.Base(cfg.Path)
	s := &Server{cfg: cfg, root: root, name: name, size: st.Size(), ln: ln, done: make(chan struct{}),
		path: "/" + hex.EncodeToString(token) + "/" + url.PathEscape(name), modTime: st.ModTime()}
	s.url = "https://" + hostPort + s.path
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.serve),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 30 * time.Second,
	}
	s.timerMu.Lock()
	s.timer = time.AfterFunc(cfg.Timeout, func() { s.finish(ErrNotDownloaded) })
	s.timerMu.Unlock()
	go func() {
		err := s.srv.ServeTLS(ln, "", "")
		if !errors.Is(err, http.ErrServerClosed) {
			s.finish(err)
		}
	}()
	return s, nil
}

// URL is the address of the image for the device: https://<addr>:<port>/<token>/<name>.
func (s *Server) URL() string { return s.url }

// RootDER is the temporary root certificate (DER) that signed the server's certificate. The
// device has to trust it for the boot.
func (s *Server) RootDER() []byte { return s.root }

// Name is the file name of the image.
func (s *Server) Name() string { return s.name }

// Size is the size of the image in bytes.
func (s *Server) Size() int64 { return s.size }

// Sent is the number of bytes of the current download.
func (s *Server) Sent() int64 { return s.sent.Load() }

// Done is closed when the server has stopped: download complete, timeout or Close.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err is nil after a complete download, ErrNotDownloaded after the timeout, ErrClosed after
// Close, or the error of the listener. Valid once Done is closed.
func (s *Server) Err() error {
	<-s.done
	return s.err
}

// Close stops the server; a download in progress is cut off. Safe to call twice.
func (s *Server) Close() { s.finish(ErrClosed) }

// finish stops the server once with err.
func (s *Server) finish(err error) {
	s.once.Do(func() {
		s.err = err
		s.timerMu.Lock()
		if s.timer != nil {
			s.timer.Stop()
		}
		s.timerMu.Unlock()
		go func() {
			// A finished download has written everything: the idle connection closes at once.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.srv.Shutdown(ctx); err != nil {
				s.srv.Close()
			}
		}()
		s.ln.Close()
		close(s.done)
	})
}

// contentType names the image for the UEFI HTTP boot client, which decides by it (or by the
// extension) whether it loads an EFI program or a RAM disk.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".efi":
		return "application/efi"
	case ".iso":
		return "application/vnd.efi-iso"
	}
	return "application/octet-stream"
}

// serve answers GET and HEAD for the image and 404 for everything else. A GET that wrote the
// whole file completes the download.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.cfg.Path)
	if err != nil {
		http.Error(w, "image not readable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", contentType(s.name))
	full := r.Method == http.MethodGet && r.Header.Get("Range") == ""
	s.sent.Store(0)
	cw := &countingWriter{ResponseWriter: w, add: func(n int) {
		v := s.sent.Add(int64(n))
		if s.cfg.Progress != nil {
			s.cfg.Progress(v, s.size)
		}
	}}
	http.ServeContent(cw, r, s.name, s.modTime, f)
	if full && cw.written == s.size && cw.status == http.StatusOK {
		s.finish(nil)
	}
}

// countingWriter counts the body bytes of one response.
type countingWriter struct {
	http.ResponseWriter
	written int64
	status  int
	add     func(int)
}

func (c *countingWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(p)
	c.written += int64(n)
	c.add(n)
	return n, err
}

// certificates creates a temporary root (RSA 2048, valid for two days around now) and the
// server certificate for addr signed by it. The root's key is dropped: nothing else can be
// signed with it.
func certificates(addr netip.Addr) (tls.Certificate, []byte, error) {
	now := time.Now()
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: RootName, Organization: []string{"OpenLAMT"}},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: addr.String(), Organization: []string{"OpenLAMT"}},
		NotBefore:    rootTmpl.NotBefore,
		NotAfter:     rootTmpl.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{addr.AsSlice()},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, root, &key.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: key}, rootDER, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// Address is a local address the device can download from.
type Address struct {
	IP        netip.Addr
	Interface string // the name of the network interface, e.g. "eth0" or "Ethernet"
}

// Addresses lists the IPv4 addresses of the interfaces that are up, without loopback.
func Addresses() []Address {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Address
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			if ip := pfx.Addr().Unmap(); ip.Is4() && !ip.IsLoopback() {
				out = append(out, Address{IP: ip, Interface: ifc.Name})
			}
		}
	}
	return out
}

// RouteTo returns the local address the system would use to reach host (a name or an IP; the
// port is that of AMT), without sending anything. It resolves names, so call it off the UI
// thread.
func RouteTo(host string) (netip.Addr, error) {
	c, err := net.Dial("udp", net.JoinHostPort(host, "16992"))
	if err != nil {
		return netip.Addr{}, err
	}
	defer c.Close()
	ap, err := netip.ParseAddrPort(c.LocalAddr().String())
	if err != nil {
		return netip.Addr{}, err
	}
	return ap.Addr().Unmap(), nil
}
