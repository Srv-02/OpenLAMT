package localme

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeME is a reader with fixed answers; it counts the calls.
type fakeME struct {
	openErr, versionErr error
	version             string
	opSupported, opOn   bool
	opAllowed           bool
	mode, prov          int
	wired               *Link
	calls               []string
}

func (f *fakeME) call(n string) { f.calls = append(f.calls, n) }

func (f *fakeME) Open() error { f.call("Open"); return f.openErr }
func (f *fakeME) Version() (string, error) {
	f.call("Version")
	return f.version, f.versionErr
}
func (f *fakeME) OperationalState() (bool, bool, bool, error) {
	f.call("OperationalState")
	return f.opSupported, f.opOn, f.opAllowed, nil
}
func (f *fakeME) ControlMode() (int, error)       { f.call("ControlMode"); return f.mode, nil }
func (f *fakeME) ProvisioningState() (int, error) { f.call("ProvisioningState"); return f.prov, nil }
func (f *fakeME) UUID() (string, error) {
	f.call("UUID")
	return "12345678-1234-1234-1234-123456789abc", nil
}
func (f *fakeME) DNSSuffix() (string, error) { f.call("DNSSuffix"); return "example.com", nil }
func (f *fakeME) LAN(wireless bool) (Link, error) {
	f.call("LAN")
	if wireless || f.wired == nil {
		return Link{}, errors.New("no such interface")
	}
	return *f.wired, nil
}

func TestReadActivatedAMT(t *testing.T) {
	me := &fakeME{version: "16.1.25", opSupported: true, opOn: true, mode: 1, prov: 2,
		wired: &Link{Enabled: true, LinkUp: true, DHCP: true, IP: "192.0.2.50"}}
	r := read(me)
	if !r.Found || r.Version != "16.1.25" || r.TurnedOff || r.ControlMode != ModeCCM || r.Provisioning != "post" ||
		r.DNSSuffix != "example.com" || r.UUID == "" || r.Wired == nil || r.Wired.IP != "192.0.2.50" || r.Wireless != nil {
		t.Fatalf("%+v", r)
	}
	if r.ShortVersion() != "16.1" {
		t.Fatalf("short version %q", r.ShortVersion())
	}
}

func TestReadTurnedOffAMT(t *testing.T) {
	r := read(&fakeME{version: "16.1.25", opSupported: true, opOn: false})
	if !r.Found || !r.TurnedOff || r.ControlMode != ModeNone || !r.TurnOnSupported || r.TurnOnAllowed {
		t.Fatalf("%+v", r)
	}
	// Bit 0 of IsChangeToAMTEnabled: the firmware allows turning it on now.
	if r := read(&fakeME{version: "16.1.25", opSupported: true, opAllowed: true}); !r.TurnedOff || !r.TurnOnSupported || !r.TurnOnAllowed {
		t.Fatalf("allowed: %+v", r)
	}
	// Without the new interface the state is unknown: AMT answered, so it counts as on.
	if r := read(&fakeME{version: "15.0.10", opSupported: false, opAllowed: true}); !r.Found || r.TurnedOff || r.TurnOnSupported || r.TurnOnAllowed {
		t.Fatalf("old interface: %+v", r)
	}
}

// AMT 11 and older have no operational state: it is not asked for.
func TestReadSkipsOperationalStateBeforeAMT12(t *testing.T) {
	me := &fakeME{version: "11.8.50", opSupported: true}
	if r := read(me); !r.Found || r.TurnedOff {
		t.Fatalf("%+v", r)
	}
	for _, c := range me.calls {
		if c == "OperationalState" {
			t.Fatal("operational state asked on AMT 11")
		}
	}
}

func TestReadNotFound(t *testing.T) {
	cases := map[string]struct {
		me     reader
		reason string
	}{
		"no reader":  {nil, ReasonNoDriver},
		"no driver":  {&fakeME{openErr: errors.New("HECI driver not detected")}, ReasonNoDriver},
		"no AMT":     {&fakeME{versionErr: errors.New("AMT Not Found")}, ReasonNoAMT},
		"no version": {&fakeME{version: " "}, ReasonNoAMT},
	}
	for name, c := range cases {
		r := read(c.me)
		if r.Found || r.Reason != c.reason || r.Version != "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

// TestResultContract fixes the JSON between the helper and the app: field names, the schema,
// and that an unknown schema or a broken answer is refused.
func TestResultContract(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 14, 0, 0, time.UTC)
	b, err := Encode(Result{Found: true, Version: "16.1.25", TurnedOff: true, TurnOnSupported: true, TurnOnAllowed: true, ControlMode: ModeNone, Provisioning: "pre",
		Wired: &Link{Enabled: true, LinkUp: true, DHCP: true, IP: "192.0.2.50", MAC: "00:00:5e:00:53:01"}, Checked: at})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":2,"found":true,"version":"16.1.25","turnedOff":true,"turnOnSupported":true,"turnOnAllowed":true,"controlMode":"none","provisioning":"pre",` +
		`"wired":{"enabled":true,"linkUp":true,"dhcp":true,"ip":"192.0.2.50","mac":"00:00:5e:00:53:01"},"checked":"2026-11-23T10:14:00Z"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	r, err := Decode(b)
	if err != nil || !r.Found || !r.Checked.Equal(at) || r.Wired.MAC != "00:00:5e:00:53:01" {
		t.Fatalf("%+v %v", r, err)
	}
	// The helper leaves Checked empty: it is not in its answer.
	if b, _ := Encode(Result{Reason: ReasonNoDriver}); string(b) != `{"schema":2,"found":false,"reason":"no-driver"}` {
		t.Fatalf("not found: %s", b)
	}
	for _, bad := range []string{``, `{`, `{"schema":3,"found":true}`, `{"found":true}`, `{"schema":2}` + strings.Repeat(" ", maxResultSize)} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("accepted %.40q", bad)
		}
	}
}

// TestDecodeSchema1: a result stored before the setup actions is still read. Schema 1 set
// TurnedOff only with the 16.1 interface, so it means TurnOnSupported; TurnOnAllowed is unknown.
func TestDecodeSchema1(t *testing.T) {
	r, err := Decode([]byte(`{"schema":1,"found":true,"version":"16.1.25","turnedOff":true,"controlMode":"none"}`))
	if err != nil || r.Schema != Schema || !r.TurnedOff || !r.TurnOnSupported || r.TurnOnAllowed {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Setup() != SetupTurnOnBIOS {
		t.Fatalf("setup %v", r.Setup())
	}
	r, err = Decode([]byte(`{"schema":1,"found":true,"version":"16.1.25","controlMode":"ccm"}`))
	if err != nil || r.TurnOnSupported || r.Setup() != SetupDeactivate {
		t.Fatalf("%+v %v", r, err)
	}
}

func useFakeME(t *testing.T, me reader) {
	t.Helper()
	old := openReader
	openReader = func() reader { return me }
	t.Cleanup(func() { openReader = old })
}

func TestIsHelper(t *testing.T) {
	if IsHelper(nil) || IsHelper([]string{"-v"}) || IsHelper([]string{"--local-activate"}) || !IsHelper([]string{"--local-check"}) ||
		!IsHelper([]string{"--local-enable"}) || !IsHelper([]string{"--local-activate-ccm"}) || !IsHelper([]string{"--local-deactivate"}) {
		t.Fatal("IsHelper")
	}
}

func TestHelperPrintsAnswer(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25", mode: 2})
	var out bytes.Buffer
	if code := RunHelper([]string{HelperFlag}, nil, &out); code != helperOK {
		t.Fatalf("exit %d", code)
	}
	r, err := Decode(out.Bytes())
	if err != nil || !r.Found || r.ControlMode != ModeACM {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHelperWritesNewFileOnly(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25"})
	path := filepath.Join(t.TempDir(), resultFile)
	if code := RunHelper([]string{HelperFlag, "--out", path}, nil, nil); code != helperOK {
		t.Fatalf("exit %d", code)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Contains(b, []byte(`"version":"16.1.25"`)) {
		t.Fatalf("%s %v", b, err)
	}
	// An existing file is never replaced.
	if code := RunHelper([]string{HelperFlag, "--out", path}, nil, nil); code != helperWrite {
		t.Fatalf("existing file: exit %d", code)
	}
}

// TestHelperDispatch: every other argument list is refused before the Intel ME is opened.
func TestHelperDispatch(t *testing.T) {
	opened := false
	oldR, oldC := openReader, openChanger
	openReader = func() reader { opened = true; return nil }
	openChanger = func() changer { opened = true; return nil }
	t.Cleanup(func() { openReader, openChanger = oldR, oldC })
	abs := filepath.Join(t.TempDir(), resultFile)
	pipe := `\\.\pipe\openlamt-0123456789abcdef0123456789abcdef`
	for _, args := range [][]string{
		{},
		{"--activate"},
		{HelperFlag, "--activate"},
		{HelperFlag, "--enable"},
		{HelperFlag, "--out"},
		{HelperFlag, "--out", "check.json"}, // relative
		{HelperFlag, "--out", filepath.Join(filepath.Dir(abs), "other.json")}, // another name
		{HelperFlag, "--out", abs, "--activate"},
		{"--out", abs, HelperFlag},
		{HelperFlag, string(ActionTurnOn)},
		{string(ActionTurnOn), string(ActionDeactivate)},
		{string(ActionTurnOn), "--password", "Secr3t!pw"},
		{string(ActionDeactivate), "--out"},
		{string(ActionDeactivate), "--out", "check.json"},
		{string(ActionTurnOn), "--pipe", pipe, "--out", abs},                  // only the activation reads a password
		{string(ActionActivateCCM), "--pipe", pipe},                           // the pipe needs the answer file
		{string(ActionActivateCCM), "--pipe", `\\.\pipe\other`, "--out", abs}, // not the app's pipe name
		{string(ActionActivateCCM), "--pipe", pipe + "0", "--out", abs},
		{string(ActionActivateCCM), "--out", abs, "--pipe", pipe},
		{string(ActionActivateCCM), "Secr3t!pw"},
	} {
		var out bytes.Buffer
		if code := RunHelper(args, strings.NewReader("Secr3t!pw"), &out); code != helperUsage || out.Len() != 0 {
			t.Errorf("%q: exit %d, output %q", args, code, out.String())
		}
	}
	if opened {
		t.Fatal("the Intel ME was opened for a refused argument list")
	}
	for _, args := range [][]string{
		{HelperFlag}, {HelperFlag, "--out", abs},
		{string(ActionTurnOn)}, {string(ActionDeactivate), "--out", abs}, {string(ActionActivateCCM)},
		{string(ActionActivateCCM), "--out", abs}, {string(ActionActivateCCM), "--pipe", pipe, "--out", abs},
	} {
		if _, ok := parseHelper(args); !ok {
			t.Errorf("%q refused", args)
		}
	}
}

// TestHelperNeverWrites: the reader of Check has read methods only, and no file of this
// package but mei_change.go (the setup actions) names a method of rpc-go that changes Intel AMT
// or reads a secret, or imports more of rpc-go than pkg/amt.
func TestHelperNeverWrites(t *testing.T) {
	var methods []string
	rt := reflect.TypeFor[reader]()
	for i := range rt.NumMethod() {
		methods = append(methods, rt.Method(i).Name)
	}
	sort.Strings(methods)
	want := "ControlMode DNSSuffix LAN Open OperationalState ProvisioningState UUID Version"
	if got := strings.Join(methods, " "); got != want {
		t.Fatalf("reader methods %q, want %q", got, want)
	}
	forbidden := map[string]bool{
		"EnableAMT": true, "DisableAMT": true, "SetAmtOperationalState": true, "Unprovision": true,
		"StartConfigurationHBased": true, "StopConfiguration": true, "GetLocalSystemAccount": true,
		"TEPSetTimeAndCerts": true, "TEPRequestOwnershipVoucher": true, "TEPSetOwnershipVoucher": true,
		"TEPRemoveOwnershipVoucher": true, "Local": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "mei_change.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && forbidden[sel.Sel.Name] {
				t.Errorf("%s: %s uses %s", name, fset.Position(sel.Pos()), sel.Sel.Name)
			}
			if imp, ok := n.(*ast.ImportSpec); ok && strings.Contains(imp.Path.Value, "rpc-go") &&
				imp.Path.Value != `"github.com/device-management-toolkit/rpc-go/v2/pkg/amt"` {
				t.Errorf("%s imports %s", name, imp.Path.Value)
			}
			return true
		})
	}
}

// TestCheckNeverOpensChanger: Check reads only; the changer of the setup actions stays closed.
func TestCheckNeverOpensChanger(t *testing.T) {
	useFakeME(t, &fakeME{version: "16.1.25"})
	old := openChanger
	openChanger = func() changer { t.Error("Check opened the changer"); return nil }
	t.Cleanup(func() { openChanger = old })
	var out bytes.Buffer
	if code := RunHelper([]string{HelperFlag}, strings.NewReader("Secr3t!pw"), &out); code != helperOK {
		t.Fatalf("exit %d", code)
	}
}
