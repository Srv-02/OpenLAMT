package scan

import (
	"errors"
	"net/netip"
	"testing"
)

func TestParseTargets(t *testing.T) {
	cases := []struct {
		in          string
		n           int
		first, last string
		err         error
	}{
		{"192.0.2.0/24", 254, "192.0.2.1", "192.0.2.254", nil},
		{"192.0.2.77/24", 254, "192.0.2.1", "192.0.2.254", nil},
		{"203.0.113.77/26", 62, "203.0.113.65", "203.0.113.126", nil},
		{"192.0.2.4/30", 2, "192.0.2.5", "192.0.2.6", nil},
		{"192.0.2.4/31", 2, "192.0.2.4", "192.0.2.5", nil},
		{"192.0.2.4/32", 1, "192.0.2.4", "192.0.2.4", nil},
		{"192.0.2.10-192.0.2.80", 71, "192.0.2.10", "192.0.2.80", nil},
		{" 192.0.2.10 - 80 ", 71, "192.0.2.10", "192.0.2.80", nil},
		{"192.0.2.250-192.0.2.255", 6, "192.0.2.250", "192.0.2.255", nil},
		{"192.0.2.9", 1, "192.0.2.9", "192.0.2.9", nil},
		{"192.0.2.0/16", 0, "", "", ErrTooMany},
		{"203.0.113.0/8", 0, "", "", ErrTooMany},
		{"192.0.2.0-203.0.113.255", 0, "", "", ErrTooMany},
		{"192.0.2.80-192.0.2.10", 0, "", "", ErrInvalid},
		{"192.0.2.10-300", 0, "", "", ErrInvalid},
		{"2001:db8::/64", 0, "", "", ErrInvalid},
		{"192.0.2.0/33", 0, "", "", ErrInvalid},
		{"example.com", 0, "", "", ErrInvalid},
		{"", 0, "", "", ErrInvalid},
	}
	for _, c := range cases {
		got, err := ParseTargets(c.in)
		if !errors.Is(err, c.err) {
			t.Errorf("%q: error %v, want %v", c.in, err, c.err)
			continue
		}
		if c.err != nil {
			continue
		}
		if len(got) != c.n || got[0] != netip.MustParseAddr(c.first) || got[len(got)-1] != netip.MustParseAddr(c.last) {
			t.Errorf("%q: %d addresses %v..%v, want %d %s..%s", c.in, len(got), got[0], got[len(got)-1], c.n, c.first, c.last)
		}
	}
}

// plus is the address n above from (a documentation address); the tests of the limit and of a
// carry into the next octet need addresses beyond the documentation ranges, which the test
// computes instead of spelling them out.
func plus(t *testing.T, from string, n int) string {
	t.Helper()
	a := netip.MustParseAddr(from).As4()
	v := (uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])) + uint32(n)
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
}

// A range across an octet boundary counts and lists every address.
func TestParseTargetsCarry(t *testing.T) {
	got, err := ParseTargets("192.0.2.250-" + plus(t, "192.0.2.250", 11))
	if err != nil || len(got) != 12 || got[0] != netip.MustParseAddr("192.0.2.250") || got[11] != netip.MustParseAddr(plus(t, "192.0.2.250", 11)) {
		t.Fatalf("%d addresses, err %v", len(got), err)
	}
}

// A whole /20 (MaxAddresses) is allowed, one address more is not.
func TestParseTargetsLimit(t *testing.T) {
	if _, err := ParseTargets("192.0.2.0-" + plus(t, "192.0.2.0", MaxAddresses-1)); err != nil {
		t.Fatalf("%d addresses: %v", MaxAddresses, err)
	}
	if _, err := ParseTargets("192.0.2.0-" + plus(t, "192.0.2.0", MaxAddresses)); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%d addresses: %v", MaxAddresses+1, err)
	}
}
