//go:build linux

package localme

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// exitError runs a shell that exits with code.
func exitError(t *testing.T, code string) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+code).Run()
	if err == nil {
		t.Fatal("no exit error")
	}
	return err
}

// TestPkexecDeclined: a dismissed or refused authentication is ErrDeclined.
func TestPkexecDeclined(t *testing.T) {
	for _, code := range []string{"126", "127"} {
		if _, err := pkexecAnswer(exitError(t, code), &bytes.Buffer{}, ""); !errors.Is(err, ErrDeclined) {
			t.Errorf("exit %s: %v", code, err)
		}
	}
}

func TestPkexecAnswer(t *testing.T) {
	r, err := pkexecAnswer(nil, bytes.NewBufferString(`{"schema":1,"found":true,"version":"16.1.25"}`), "")
	if err != nil || !r.Found || r.Version != "16.1.25" {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = pkexecAnswer(exitError(t, "2"), &bytes.Buffer{}, "usage\n")
	if err == nil || errors.Is(err, ErrDeclined) || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("helper failure: %v", err)
	}
	if _, err := pkexecAnswer(nil, bytes.NewBufferString("not json"), ""); err == nil {
		t.Fatal("broken answer accepted")
	}
}

func TestHelperProgramPrefersAppImage(t *testing.T) {
	t.Setenv("APPIMAGE", "/opt/example/OpenLAMT.AppImage")
	if p, err := helperProgram(); err != nil || p != "/opt/example/OpenLAMT.AppImage" {
		t.Fatalf("%q %v", p, err)
	}
	t.Setenv("APPIMAGE", "")
	if p, err := helperProgram(); err != nil || !strings.HasPrefix(p, "/") {
		t.Fatalf("%q %v", p, err)
	}
}
