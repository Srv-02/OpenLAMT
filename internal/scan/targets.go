// Package scan finds Intel AMT devices in an IPv4 network: an RMCP presence ping on UDP 623
// and a check of TCP 16992 (HTTP) and 16993 (HTTPS). The version is read from the HTTP
// `Server:` header, because the RMCP pong carries the major version in four bits only (AMT 16
// and newer do not fit). The scan sends no credentials and changes nothing on a device.
package scan

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// MaxAddresses is the most addresses one scan covers (a /20): a typo such as /8 must not
// run for hours. Larger networks are scanned in parts.
const MaxAddresses = 4096

var (
	// ErrInvalid means the input is neither a network, a range nor an address.
	ErrInvalid = errors.New("not an IPv4 network or range")
	// ErrTooMany means the input covers more than MaxAddresses addresses.
	ErrTooMany = errors.New("too many addresses")
)

// ParseTargets turns "192.0.2.0/24", "192.0.2.10-192.0.2.80", "192.0.2.10-80" or a single
// address into the addresses to scan, in ascending order. A network of /30 or larger leaves out
// its network and broadcast address. Only IPv4 is scanned.
func ParseTargets(s string) ([]netip.Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrInvalid
	}
	switch {
	case strings.Contains(s, "/"):
		return parseCIDR(s)
	case strings.Contains(s, "-"):
		return parseRange(s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return nil, ErrInvalid
	}
	return []netip.Addr{a}, nil
}

func parseCIDR(s string) ([]netip.Addr, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return nil, ErrInvalid
	}
	p = p.Masked()
	size := 1 << (32 - p.Bits())
	if size > MaxAddresses {
		return nil, ErrTooMany
	}
	first := p.Addr()
	out := make([]netip.Addr, 0, size)
	for a, i := first, 0; i < size; a, i = a.Next(), i+1 {
		out = append(out, a)
	}
	if p.Bits() <= 30 {
		out = out[1 : len(out)-1]
	}
	return out, nil
}

func parseRange(s string) ([]netip.Addr, error) {
	lo, hi, _ := strings.Cut(s, "-")
	lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)
	from, err := netip.ParseAddr(lo)
	if err != nil || !from.Is4() {
		return nil, ErrInvalid
	}
	var to netip.Addr
	if n, err := strconv.Atoi(hi); err == nil { // "192.0.2.10-80": the last octet only
		if n < 0 || n > 255 {
			return nil, ErrInvalid
		}
		b := from.As4()
		b[3] = byte(n)
		to = netip.AddrFrom4(b)
	} else if to, err = netip.ParseAddr(hi); err != nil || !to.Is4() {
		return nil, ErrInvalid
	}
	if to.Less(from) {
		return nil, ErrInvalid
	}
	if count(from, to) > MaxAddresses {
		return nil, ErrTooMany
	}
	var out []netip.Addr
	for a := from; ; a = a.Next() {
		out = append(out, a)
		if a == to {
			break
		}
	}
	return out, nil
}

func count(from, to netip.Addr) int {
	f, t := from.As4(), to.As4()
	n := func(b [4]byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	return n(t) - n(f) + 1
}
