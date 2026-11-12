package amt

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var digestPair = regexp.MustCompile(`(\w+)="([^"]*)"`)

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newAMTServer serves WS-Man fixtures behind Digest auth (MD5, qop=auth) on 127.0.0.1.
func newAMTServer(t *testing.T, useTLS bool, user, pass string, routes map[string]string) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			// Unquoted algorithm must stay last: the client splits the challenge on `",`.
			w.Header().Set("WWW-Authenticate", `Digest realm="openlamt-test", nonce="0123456789abcdef", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := map[string]string{}
		for _, m := range digestPair.FindAllStringSubmatch(auth, -1) {
			p[m[1]] = m[2]
		}
		if nc := regexp.MustCompile(`nc=([0-9a-f]+)`).FindStringSubmatch(auth); p["nc"] == "" && nc != nil {
			p["nc"] = nc[1]
		}
		ha1 := md5hex(p["username"] + ":" + p["realm"] + ":" + pass)
		ha2 := md5hex("POST:" + p["uri"])
		expected := md5hex(ha1 + ":" + p["nonce"] + ":" + p["nc"] + ":" + p["cnonce"] + ":" + p["qop"] + ":" + ha2)
		if p["username"] != user || p["response"] != expected {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		name, ok := routes[routeKey(string(body))]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// A "400:" prefix sends the file as an HTTP 400 SOAP fault, like AMT does for a refused
		// request of an authenticated sender.
		status := http.StatusOK
		if rest, ok := strings.CutPrefix(name, "400:"); ok {
			name, status = rest, http.StatusBadRequest
		}
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(data)
	})
	var ts *httptest.Server
	if useTLS {
		ts = httptest.NewTLSServer(h)
	} else {
		ts = httptest.NewServer(h)
	}
	t.Cleanup(ts.Close)
	return ts
}

// dialTo redirects every dial to the test server, whatever port the library picks.
func dialTo(ts *httptest.Server) dialFunc {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ts.Listener.Addr().String())
	}
}
