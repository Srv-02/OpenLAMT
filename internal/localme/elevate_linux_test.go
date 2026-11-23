//go:build linux

package localme

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	out, err := pkexecAnswer(nil, bytes.NewBufferString(`{"schema":2,"found":true,"version":"16.1.25"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := readAnswer(out)
	if err != nil || !r.Found || r.Version != "16.1.25" {
		t.Fatalf("%+v %v", r, err)
	}
	_, err = pkexecAnswer(exitError(t, "2"), &bytes.Buffer{}, "usage\n")
	if err == nil || errors.Is(err, ErrDeclined) || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("helper failure: %v", err)
	}
	if _, err := readAnswer(bytes.NewBufferString("not json")); err == nil {
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

// fakeElevator replaces pkexec by a script that starts the program like pkexec does (same
// stdin, stdout and stderr) and the helper by script; it returns the file where the helper
// logs its arguments.
func fakeElevator(t *testing.T, helper string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pk := write("pkexec", `exec "$@"`+"\n")
	prog := write("openlamt", helper)
	oldL, oldP := lookPkexec, helperProgram
	lookPkexec = func() (string, error) { return pk, nil }
	helperProgram = func() (string, error) { return prog, nil }
	t.Cleanup(func() { lookPkexec, helperProgram = oldL, oldP })
	return filepath.Join(dir, "args")
}

// TestActivationPasswordOnStdin: on Linux the password reaches the helper on stdin through the
// elevator, never in its arguments, and Run wipes the caller's bytes afterwards.
func TestActivationPasswordOnStdin(t *testing.T) {
	args := fakeElevator(t, `echo "$@" > "$(dirname "$0")/args"
pw=$(cat)
[ "$pw" = 'Secr3t!pw' ] || exit 4
printf '%s' '{"schema":2,"state":{"schema":2,"found":true,"version":"16.1.25","controlMode":"ccm"}}'
`)
	pw := []byte("Secr3t!pw")
	r, err := Run(ActionActivateCCM, pw)
	if err != nil || r.Err() != nil || r.State.ControlMode != ModeCCM {
		t.Fatalf("%+v %v", r, err)
	}
	got, err := os.ReadFile(args)
	if err != nil || strings.TrimSpace(string(got)) != string(ActionActivateCCM) {
		t.Fatalf("helper arguments %q %v", got, err)
	}
	if !bytes.Equal(pw, make([]byte, len(pw))) {
		t.Fatalf("password not wiped: %q", pw)
	}
}

// TestActionWithoutPassword: Turn On and Deactivate give the helper an empty stdin and their
// flag only; a refusal comes back with the fresh state.
func TestActionWithoutPassword(t *testing.T) {
	args := fakeElevator(t, `echo "$@" > "$(dirname "$0")/args"
[ -z "$(cat)" ] || exit 4
printf '%s' '{"schema":2,"refused":"unprovision status 1","state":{"schema":2,"found":true,"version":"16.1.25","controlMode":"ccm"}}'
`)
	r, err := Run(ActionDeactivate, nil)
	var refused *RefusedError
	if err != nil || !errors.As(r.Err(), &refused) || refused.Reason != "unprovision status 1" || r.State.ControlMode != ModeCCM {
		t.Fatalf("%+v %v", r, err)
	}
	if got, _ := os.ReadFile(args); strings.TrimSpace(string(got)) != string(ActionDeactivate) {
		t.Fatalf("helper arguments %q", got)
	}
}

// TestActionDeclinedAndInvalid: refused rights are ErrDeclined; an invalid password never
// starts the elevator.
func TestActionDeclinedAndInvalid(t *testing.T) {
	args := fakeElevator(t, "echo started > \"$(dirname \"$0\")/args\"\nexit 126\n")
	if _, err := Run(ActionTurnOn, nil); !errors.Is(err, ErrDeclined) {
		t.Fatalf("declined: %v", err)
	}
	os.Remove(args)
	for _, pw := range []string{"", "short1!", "nouppercase1!", "Secr3t:pw", "Secr3t,pw", `Secr3t"pw`} {
		if _, err := Run(ActionActivateCCM, []byte(pw)); !errors.Is(err, ErrPassword) {
			t.Errorf("%q: %v", pw, err)
		}
	}
	if _, err := os.Stat(args); err == nil {
		t.Fatal("the elevator ran for an invalid password")
	}
}
