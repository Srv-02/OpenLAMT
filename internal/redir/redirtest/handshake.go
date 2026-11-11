// Package redirtest provides the server side of the AMT redirection handshake for the fake
// servers in kvmtest and idertest. It deliberately does not import internal/redir (import cycle
// with that package's tests) and is only imported by tests.
package redirtest

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
)

// Start magics a client sends first.
var (
	MagicKVM  = []byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}
	MagicIDER = []byte{0x10, 0x00, 0x00, 0x00, 'I', 'D', 'E', 'R'}
)

// Options configures the handshake.
type Options struct {
	Username, Password string
	Realm              string // digest realm; "" = "redirtest"
	AuthTypes          []byte // offered in the query reply; nil = {4}
	StartStatus        byte   // StartRedirectionSessionReply status; 0 = success
	AuthDataLen        int    // pad the query reply's authData to this length (0 = natural)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func challenge(status byte, realm, nonce string) []byte {
	var data []byte
	for _, f := range []string{realm, nonce, "auth"} {
		data = append(data, byte(len(f)))
		data = append(data, f...)
	}
	return authReply(status, 4, data)
}

func authReply(status, authType byte, data []byte) []byte {
	b := []byte{0x14, status, 0, 0, authType, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[5:], uint32(len(data)))
	return append(b, data...)
}

// readAuth reads one authentication message: 9-byte header plus LE32 length bytes.
func readAuth(c net.Conn) (hdr [9]byte, body []byte, ok bool) {
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	n := binary.LittleEndian.Uint32(hdr[5:])
	if n > 4096 {
		return
	}
	body = make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return
	}
	return hdr, body, true
}

// Handshake runs the redirection protocol on c up to and including the authentication success
// reply: start magic (must equal magic), StartRedirectionSessionReply, authentication query,
// digest challenge and verification. ok reports success. authType is the authType byte of the
// digest message if one was received (0 otherwise), also when verification failed. Afterwards c
// carries the service's own protocol.
func Handshake(c net.Conn, magic []byte, o Options) (authType byte, ok bool) {
	realm := o.Realm
	if realm == "" {
		realm = "redirtest"
	}
	start := make([]byte, 8)
	if _, err := io.ReadFull(c, start); err != nil || !bytes.Equal(start, magic) {
		return 0, false
	}
	reply := make([]byte, 13)
	reply[0], reply[1] = 0x11, o.StartStatus
	if _, err := c.Write(reply); err != nil || o.StartStatus != 0 {
		return 0, false
	}

	hdr, _, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 || hdr[4] != 0 {
		return 0, false
	}
	types := o.AuthTypes
	if types == nil {
		types = []byte{4}
	}
	data := append([]byte(nil), types...)
	if o.AuthDataLen > len(data) {
		data = append(data, make([]byte, o.AuthDataLen-len(data))...)
	}
	if _, err := c.Write(authReply(0, 0, data)); err != nil {
		return 0, false
	}

	// User message, then challenge.
	hdr, ubody, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 {
		return 0, false
	}
	wantUser := append([]byte{byte(len(o.Username))}, o.Username...)
	wantUser = append(wantUser, 0, 0, byte(len("/RedirectionService")))
	wantUser = append(wantUser, "/RedirectionService"...)
	wantUser = append(wantUser, 0, 0, 0, 0)
	userOK := hdr[4] == 4 && bytes.Equal(ubody, wantUser)
	const nonce = "0123456789abcdef"
	if _, err := c.Write(challenge(1, realm, nonce)); err != nil {
		return 0, false
	}

	// Digest message: user, realm, nonce, uri, cnonce, nc, response, qop.
	hdr, body, ok := readAuth(c)
	if !ok || hdr[0] != 0x13 {
		return 0, false
	}
	authType = hdr[4]
	var f [8]string
	for i := range f {
		if len(body) < 1 || len(body) < 1+int(body[0]) {
			return authType, false
		}
		f[i] = string(body[1 : 1+int(body[0])])
		body = body[1+int(body[0]):]
	}
	ha1 := md5hex(o.Username + ":" + realm + ":" + o.Password)
	ha2 := md5hex("POST:" + f[3])
	want := md5hex(ha1 + ":" + nonce + ":" + f[5] + ":" + f[4] + ":" + f[7] + ":" + ha2)
	strict := userOK && f[3] == "/RedirectionService" && f[5] == "00000001" && f[7] == "auth" && len(f[4]) == 32
	if !strict || f[0] != o.Username || f[1] != realm || f[2] != nonce || f[6] != want {
		// Like AMT: a fresh challenge with status 1, then the connection closes.
		_, _ = c.Write(challenge(1, realm, "fedcba9876543210"))
		return authType, false
	}
	if _, err := c.Write(authReply(0, 4, nil)); err != nil {
		return authType, false
	}
	return authType, true
}
