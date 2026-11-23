//go:build linux

package localme

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// procNetTCP is /proc/net/tcp with: root on 127.0.0.1:16992 (LMS), uid 1000 on 0.0.0.0:16993,
// uid 1000 on 192.0.2.5:16992 (does not get 127.0.0.1), an established connection and uid 1000
// listening on 127.0.0.1:8080.
const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:4260 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0
   1: 00000000:4261 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1002 1 0000000000000000 100 0 0 10 0
   2: 050200C0:4260 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1003 1 0000000000000000 100 0 0 10 0
   3: 0100007F:4260 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 1004 1 0000000000000000 20 4 30 10 -1
   4: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1005 1 0000000000000000 100 0 0 10 0
`

// procNetTCP6 has uid 1000 on [::]:16992 and root on [::1]:16993 (does not get 127.0.0.1).
const procNetTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:4260 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2001 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:4261 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2002 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	ls, err := parseProcNetTCP(strings.NewReader(procNetTCP))
	want := []tcpListener{{Port: 16992, Owner: 0}, {Port: 16993, Owner: 1000}, {Port: 8080, Owner: 1000}}
	if err != nil || !slices.Equal(ls, want) {
		t.Fatalf("%v %v", ls, err)
	}
	ls, err = parseProcNetTCP(strings.NewReader(procNetTCP6))
	if err != nil || !slices.Equal(ls, []tcpListener{{Port: 16992, Owner: 1000}}) {
		t.Fatalf("tcp6: %v %v", ls, err)
	}
	if _, err := parseProcNetTCP(strings.NewReader("header\n 0: 0100007F:ZZZZ 00000000:0000 0A 0:0 0:0 0 0 0 1\n")); err == nil {
		t.Fatal("bad port accepted")
	}
}

// TestLinuxLMSRule: root's LMS passes, a user's listener on 127.0.0.1 or a wildcard does not.
func TestLinuxLMSRule(t *testing.T) {
	genuine := func(l tcpListener) bool { return l.Owner == 0 }
	only := func(text string) []tcpListener {
		ls, err := parseProcNetTCP(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return ls
	}
	lines := strings.Split(procNetTCP, "\n")
	if err := foreignLMS(only(lines[0]+"\n"+lines[1]), genuine); err != nil {
		t.Fatalf("root LMS refused: %v", err)
	}
	var foreign *ForeignLMSError
	if err := foreignLMS(only(procNetTCP), genuine); !errors.As(err, &foreign) || foreign.Port != 16993 {
		t.Fatalf("foreign wildcard listener: %v", err)
	}
	if err := foreignLMS(only(procNetTCP6), genuine); !errors.As(err, &foreign) || foreign.Port != 16992 {
		t.Fatalf("foreign IPv6 wildcard listener: %v", err)
	}
	// The real tables of this machine can be read (whatever listens there).
	if err := platformCheckLMS(); err != nil && !errors.As(err, &foreign) {
		t.Fatalf("reading /proc/net/tcp: %v", err)
	}
}
