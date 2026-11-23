//go:build linux

package localme

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// platformCheckLMS reads the listening sockets of /proc/net/tcp and /proc/net/tcp6. Intel's LMS
// runs as root: a listener of any other uid is foreign.
func platformCheckLMS() error {
	var all []tcpListener
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(name)
		if os.IsNotExist(err) && strings.HasSuffix(name, "6") {
			continue // no IPv6
		}
		if err != nil {
			return fmt.Errorf("checking the LMS ports: %w", err)
		}
		ls, err := parseProcNetTCP(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("checking the LMS ports: %w", err)
		}
		all = append(all, ls...)
	}
	return foreignLMS(all, func(l tcpListener) bool { return l.Owner == 0 })
}

// Local addresses (as /proc/net/tcp writes them) of a listener that receives connections to
// 127.0.0.1: 127.0.0.1 and the wildcard, in IPv4 and IPv6 (the IPv6 wildcard and ::ffff:127.0.0.1
// take IPv4 connections unless IPV6_V6ONLY is set; counting them is the safe side).
var loopbackListen = map[string]bool{
	"0100007F":                         true,
	"00000000":                         true,
	"00000000000000000000000000000000": true,
	"0000000000000000FFFF00000100007F": true,
}

// tcpListen is the state LISTEN in /proc/net/tcp.
const tcpListen = "0A"

// parseProcNetTCP reads the listeners that receive connections to 127.0.0.1 from the text of
// /proc/net/tcp or /proc/net/tcp6: "sl local_address rem_address st … uid …".
func parseProcNetTCP(r io.Reader) ([]tcpListener, error) {
	var out []tcpListener
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // the header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 8 || f[3] != tcpListen {
			continue
		}
		addr, port, ok := strings.Cut(f[1], ":")
		if !ok || !loopbackListen[strings.ToUpper(addr)] {
			continue
		}
		p, err := strconv.ParseUint(port, 16, 16)
		if err != nil {
			return nil, fmt.Errorf("bad local address %q", f[1])
		}
		uid, err := strconv.ParseUint(f[7], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("bad uid %q", f[7])
		}
		out = append(out, tcpListener{Port: int(p), Owner: uint32(uid)})
	}
	return out, sc.Err()
}
