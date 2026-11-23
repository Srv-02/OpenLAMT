//go:build windows

package localme

import (
	"errors"
	"testing"
)

// fakeTables is tcpTables with fixed answers.
type fakeTables struct {
	ls  []tcpListener
	pid uint32
	err error
}

func (f fakeTables) listeners() ([]tcpListener, error) { return f.ls, f.err }
func (f fakeTables) lmsPID() (uint32, error)           { return f.pid, nil }

func TestWindowsLMSRule(t *testing.T) {
	var foreign *ForeignLMSError
	if err := checkLMSWith(fakeTables{}); err != nil {
		t.Fatalf("no listener: %v", err)
	}
	if err := checkLMSWith(fakeTables{ls: []tcpListener{{Port: 16992, Owner: 4242}, {Port: 16993, Owner: 4242}}, pid: 4242}); err != nil {
		t.Fatalf("LMS refused: %v", err)
	}
	if err := checkLMSWith(fakeTables{ls: []tcpListener{{Port: 16992, Owner: 4242}, {Port: 16993, Owner: 777}}, pid: 4242}); !errors.As(err, &foreign) || foreign.Port != 16993 {
		t.Fatalf("foreign beside LMS: %v", err)
	}
	// LMS not running: any listener is foreign.
	if err := checkLMSWith(fakeTables{ls: []tcpListener{{Port: 16992, Owner: 4242}}}); !errors.As(err, &foreign) {
		t.Fatalf("no LMS: %v", err)
	}
	if err := checkLMSWith(fakeTables{err: errors.New("access denied")}); err == nil || errors.As(err, &foreign) {
		t.Fatalf("table error: %v", err)
	}
	// The real tables of this machine can be read.
	if err := platformCheckLMS(); err != nil && !errors.As(err, &foreign) {
		t.Fatalf("reading the TCP table: %v", err)
	}
}
