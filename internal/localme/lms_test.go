package localme

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestForeignLMS: only listeners on 16992 and 16993 count, and only those genuine refuses.
func TestForeignLMS(t *testing.T) {
	root := func(l tcpListener) bool { return l.Owner == 0 }
	var foreign *ForeignLMSError
	cases := []struct {
		name string
		ls   []tcpListener
		port int // 0: none foreign
	}{
		{"no listener", nil, 0},
		{"other ports", []tcpListener{{Port: 22, Owner: 1000}, {Port: 8080, Owner: 1000}}, 0},
		{"genuine LMS", []tcpListener{{Port: 16992, Owner: 0}, {Port: 16993, Owner: 0}}, 0},
		{"foreign on 16992", []tcpListener{{Port: 16992, Owner: 1000}}, 16992},
		{"foreign on 16993 beside LMS", []tcpListener{{Port: 16992, Owner: 0}, {Port: 16993, Owner: 1000}}, 16993},
	}
	for _, c := range cases {
		err := foreignLMS(c.ls, root)
		if c.port == 0 && err != nil || c.port != 0 && (!errors.As(err, &foreign) || foreign.Port != c.port) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestHelperForeignLMSAnswer: a foreign listener is answered as ForeignPort, not as a refusal
// of the firmware, and the answer holds no password.
func TestHelperForeignLMSAnswer(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25"})
	useFakeChanger(t, &fakeChanger{err: &ForeignLMSError{Port: 16992}})
	path := filepath.Join(t.TempDir(), resultFile)
	if code := RunHelper([]string{string(ActionActivateCCM), "--out", path}, strings.NewReader("Secr3t!pw"), nil); code != helperOK {
		t.Fatalf("exit %d", code)
	}
	a, err := readActionFile(t, path)
	var foreign *ForeignLMSError
	if err != nil || a.Refused != "" || a.ForeignPort != 16992 || !errors.As(a.Err(), &foreign) || foreign.Port != 16992 {
		t.Fatalf("%+v %v", a, err)
	}
}
