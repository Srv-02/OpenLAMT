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
		{"192.0.2.0/20", 4094, "192.0.0.1", "192.0.15.254", nil},
		{"192.0.2.4/30", 2, "192.0.2.5", "192.0.2.6", nil},
		{"192.0.2.4/31", 2, "192.0.2.4", "192.0.2.5", nil},
		{"192.0.2.4/32", 1, "192.0.2.4", "192.0.2.4", nil},
		{"192.0.2.10-192.0.2.80", 71, "192.0.2.10", "192.0.2.80", nil},
		{" 192.0.2.10 - 80 ", 71, "192.0.2.10", "192.0.2.80", nil},
		{"192.0.2.250-192.0.3.5", 12, "192.0.2.250", "192.0.3.5", nil},
		{"192.0.2.9", 1, "192.0.2.9", "192.0.2.9", nil},
		{"192.0.2.0/16", 0, "", "", ErrTooMany},
		{"203.0.0.0/8", 0, "", "", ErrTooMany},
		{"192.0.0.0-192.0.16.0", 0, "", "", ErrTooMany},
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

// A whole /20 is allowed, one address more is not.
func TestParseTargetsLimit(t *testing.T) {
	if _, err := ParseTargets("192.0.0.0-192.0.15.255"); err != nil {
		t.Fatalf("4096 addresses: %v", err)
	}
	if _, err := ParseTargets("192.0.0.0-192.0.16.0"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("4097 addresses: %v", err)
	}
}
