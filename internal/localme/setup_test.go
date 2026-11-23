package localme

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupOffer: each check result offers exactly one setup action or hint (design "Ablauf").
func TestSetupOffer(t *testing.T) {
	on := func(mode string) Result { return Result{Found: true, Version: "16.1.25", ControlMode: mode} }
	off := func(supported, allowed bool) Result {
		return Result{Found: true, Version: "16.1.25", TurnedOff: true, TurnOnSupported: supported, TurnOnAllowed: allowed, ControlMode: ModeNone}
	}
	cases := []struct {
		name string
		r    Result
		want Setup
	}{
		{"not found", Result{Reason: ReasonNoAMT}, SetupNone},
		{"off, 16.1, allowed", off(true, true), SetupTurnOn},
		{"off, 16.1, not allowed", off(true, false), SetupTurnOnBIOS},
		{"off, before 16.1", off(false, true), SetupTurnOnLegacy},
		{"on, not activated", on(ModeNone), SetupActivate},
		{"CCM", on(ModeCCM), SetupDeactivate},
		{"ACM", on(ModeACM), SetupACM},
		{"unknown mode", on(""), SetupNone},
	}
	for _, c := range cases {
		if got := c.r.Setup(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for _, ok := range []string{"Secr3t!pw", "Aa1!aaaa", "Aa1!" + strings.Repeat("x", 28), "Pass word 1!", "Pa$$w0rd<>&"} {
		if err := ValidatePassword([]byte(ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Aa1!aaa", "Aa1!" + strings.Repeat("x", 29), "secr3t!pw", "SECR3T!PW", "Secret!pw", "Secr3tpw1",
		"Secr3t:pw", "Secr3t,pw", `Secr3t"pw`, "Secr3t!pw\n", "Secr3t!pw\t", "Secr3t!pwä"} {
		if err := ValidatePassword([]byte(bad)); !errors.Is(err, ErrPassword) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestActionContract(t *testing.T) {
	b, err := encodeAction(ActionResult{Refused: "control mode 2 is not Client Control Mode", State: Result{Found: true, Version: "16.1.25", ControlMode: ModeACM}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":2,"refused":"control mode 2 is not Client Control Mode","state":{"schema":2,"found":true,"version":"16.1.25","controlMode":"acm"}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	a, err := decodeAction(b)
	var refused *RefusedError
	if err != nil || !errors.As(a.Err(), &refused) || a.State.ControlMode != ModeACM {
		t.Fatalf("%+v %v", a, err)
	}
	if (ActionResult{}).Err() != nil {
		t.Fatal("a made change is an error")
	}
	for _, bad := range []string{``, `{`, `{"schema":1,"state":{"schema":2}}`, `{"schema":2,"state":{"schema":1}}`, `{"schema":2}` + strings.Repeat(" ", maxResultSize)} {
		if _, err := decodeAction([]byte(bad)); err == nil {
			t.Errorf("accepted %.40q", bad)
		}
	}
}

func TestRefusalIsShortAndSecretFree(t *testing.T) {
	secret := []byte("Secr3t!pw")
	if got := refusal(errors.New("setup failed for Secr3t!pw\nstack trace"), secret); got != "setup failed for ***" {
		t.Fatalf("%q", got)
	}
	if got := refusal(errors.New(strings.Repeat("x", 300)), nil); len(got) > maxReason+len("…") {
		t.Fatalf("%d bytes", len(got))
	}
	if got := refusal(errors.New(" "), nil); got != "unknown error" {
		t.Fatalf("%q", got)
	}
}

// fakeChanger records the setup actions; its methods print and prompt like rpc-go may.
type fakeChanger struct {
	calls    []string
	password string
	err      error
	stdin    string // what a prompt read from os.Stdin
}

func (f *fakeChanger) do(name string) error {
	f.calls = append(f.calls, name)
	fmt.Println("rpc-go chatter") // must not reach the answer on stdout
	b, _ := io.ReadAll(bufio.NewReader(os.Stdin))
	f.stdin = string(b)
	return f.err
}
func (f *fakeChanger) TurnOn() error { return f.do("TurnOn") }
func (f *fakeChanger) ActivateCCM(pw string) error {
	f.password = pw
	return f.do("ActivateCCM")
}
func (f *fakeChanger) Deactivate() error { return f.do("Deactivate") }

func useFakeChanger(t *testing.T, ch changer) {
	t.Helper()
	old := openChanger
	openChanger = func() changer { return ch }
	t.Cleanup(func() { openChanger = old })
}

// runOnStdout runs the helper the way main does: its answer writer is the process's stdout.
func runOnStdout(t *testing.T, args []string, stdin io.Reader) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := RunHelper(args, stdin, w)
	os.Stdout = old
	w.Close()
	b, _ := io.ReadAll(r)
	r.Close()
	return code, string(b)
}

// TestHelperRunsActions: each flag runs its action, reads the state again and answers it; what
// rpc-go prints stays out of the answer and a prompt reads nothing.
func TestHelperRunsActions(t *testing.T) {
	for _, c := range []struct {
		action Action
		call   string
	}{{ActionTurnOn, "TurnOn"}, {ActionDeactivate, "Deactivate"}, {ActionActivateCCM, "ActivateCCM"}} {
		me := &fakeME{version: "16.1.25", opSupported: true, opOn: true, mode: 1}
		useFakeME(t, me)
		ch := &fakeChanger{}
		useFakeChanger(t, ch)
		code, out := runOnStdout(t, []string{string(c.action)}, strings.NewReader("Secr3t!pw"))
		if code != helperOK || len(ch.calls) != 1 || ch.calls[0] != c.call || ch.stdin != "" {
			t.Fatalf("%s: exit %d, calls %v, prompt read %q", c.action, code, ch.calls, ch.stdin)
		}
		a, err := decodeAction([]byte(out))
		if err != nil || a.Refused != "" || !a.State.Found || a.State.ControlMode != ModeCCM {
			t.Fatalf("%s: answer %q: %v", c.action, out, err)
		}
		if c.action == ActionActivateCCM && ch.password != "Secr3t!pw" {
			t.Fatalf("password %q", ch.password)
		}
		if c.action != ActionActivateCCM && ch.password != "" {
			t.Fatal("a password reached Turn On or Deactivate")
		}
		if strings.Contains(out, "Secr3t") {
			t.Fatalf("answer holds the password: %s", out)
		}
	}
}

// TestHelperRefusalAnswer: a failed change is answered with its reason and the state; the exit
// code stays 0 and the answer file holds no password.
func TestHelperRefusalAnswer(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25", mode: 0})
	useFakeChanger(t, &fakeChanger{err: errors.New("activation failed with Secr3t!pw")})
	path := filepath.Join(t.TempDir(), resultFile)
	if code := RunHelper([]string{string(ActionActivateCCM), "--out", path}, strings.NewReader("Secr3t!pw"), nil); code != helperOK {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(path)
	if err != nil || bytes.Contains(b, []byte("Secr3t")) {
		t.Fatalf("%s %v", b, err)
	}
	a, err := decodeAction(b)
	if err != nil || a.Refused != "activation failed with ***" || a.State.ControlMode != ModeNone {
		t.Fatalf("%+v %v", a, err)
	}
}

// TestHelperNeedsAValidPassword: an activation without a valid password changes nothing.
func TestHelperNeedsAValidPassword(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25"})
	opened := false
	old := openChanger
	openChanger = func() changer { opened = true; return &fakeChanger{} }
	t.Cleanup(func() { openChanger = old })
	for _, in := range []io.Reader{nil, strings.NewReader(""), strings.NewReader("weak"), strings.NewReader("Secr3t!pw" + strings.Repeat("x", 40))} {
		var out bytes.Buffer
		if code := RunHelper([]string{string(ActionActivateCCM)}, in, &out); code != helperSecret || out.Len() != 0 {
			t.Errorf("exit %d, output %q", code, out.String())
		}
	}
	if opened {
		t.Fatal("the changer was opened without a valid password")
	}
}

// readActionFile decodes the action answer in the file at path.
func readActionFile(t *testing.T, path string) (ActionResult, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ActionResult{}, err
	}
	if bytes.Contains(b, []byte("Secr3t")) {
		t.Fatalf("answer holds the password: %s", b)
	}
	return decodeAction(b)
}
