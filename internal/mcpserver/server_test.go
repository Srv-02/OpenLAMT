package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

const (
	testFP       = "abababababababababababababababababababababababababababababababab"
	testPassword = "Secret-Pass-1!"
)

type favList []favorites.Favorite

func (l favList) List() ([]favorites.Favorite, error) { return slices.Clone(l), nil }

// harness runs the server against an in-memory client, favorites, a memory keychain and one
// session.Fake per host.
type harness struct {
	t       *testing.T
	cfg     Config
	favs    favList
	keys    *keychain.Memory
	devices map[string]*session.Fake
	elicit  func(*mcp.ElicitRequest) (*mcp.ElicitResult, error) // nil: the client has no elicitation

	mu       sync.Mutex
	connects []amt.Config
	logPath  string
	cs       *mcp.ClientSession
	srv      *Server
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{t: t, cfg: Defaults(dir), keys: keychain.NewMemory(), devices: map[string]*session.Fake{}}
	h.cfg.ActionLogPath = filepath.Join(dir, ActionLogFileName)
	return h
}

// addDevice saves a favorite with password (and pin for TLS) and returns its fake device.
func (h *harness) addDevice(name, host string, tls bool) *session.Fake {
	h.favs = append(h.favs, favorites.Favorite{Name: name, Host: host, Username: "admin", UseTLS: tls})
	if err := h.keys.Set(host, "admin", testPassword); err != nil {
		h.t.Fatal(err)
	}
	if tls {
		if err := h.keys.SetFingerprint(host, "admin", testFP); err != nil {
			h.t.Fatal(err)
		}
	}
	f := &session.Fake{FP: testFP, St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}}
	h.devices[host] = f
	return f
}

func (h *harness) connect(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
	h.mu.Lock()
	h.connects = append(h.connects, cfg)
	h.mu.Unlock()
	if prompt != nil {
		h.t.Error("the MCP server passed a trust prompt: it must never pin")
	}
	f, ok := h.devices[cfg.Host]
	if !ok {
		return nil, errors.New("dial tcp: no route to host")
	}
	return session.ConnectFake(f)(ctx, cfg, prompt)
}

func (h *harness) connectCalls() []amt.Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.connects)
}

// start connects a client; call after setting cfg, favs and elicit.
func (h *harness) start() {
	t := h.t
	var log *ActionLog
	if h.cfg.ActionLog {
		var err error
		if log, err = OpenActionLog(h.cfg.ActionLogPath); err != nil {
			t.Fatal(err)
		}
		h.logPath = h.cfg.ActionLogPath
	}
	srv := New(Options{Config: h.cfg, Favorites: h.favs, Keys: h.keys, Connect: h.connect, Log: log, Version: "test"})
	h.srv = srv
	t.Cleanup(srv.Close)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	var opts *mcp.ClientOptions
	if h.elicit != nil {
		opts = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, r *mcp.ElicitRequest) (*mcp.ElicitResult, error) { return h.elicit(r) }}
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, opts)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	h.cs = cs
}

// call runs a tool and returns its text and structured result.
func (h *harness) call(name string, args map[string]any) (text string, out map[string]any, isError bool) {
	h.t.Helper()
	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: protocol error %v", name, err)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	text = strings.Join(parts, "\n")
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	if strings.Contains(text, testPassword) {
		h.t.Fatalf("%s: result contains the password: %s", name, text)
	}
	return text, out, res.IsError
}

func (h *harness) mustOK(name string, args map[string]any) map[string]any {
	h.t.Helper()
	text, out, isErr := h.call(name, args)
	if isErr {
		h.t.Fatalf("%s %v: error %s", name, args, text)
	}
	return out
}

func (h *harness) mustFail(name string, args map[string]any, want string) string {
	h.t.Helper()
	text, _, isErr := h.call(name, args)
	if !isErr || !strings.Contains(text, want) {
		h.t.Fatalf("%s %v: isError=%v, want %q in %q", name, args, isErr, want, text)
	}
	return text
}

func (h *harness) toolNames() []string {
	h.t.Helper()
	res, err := h.cs.ListTools(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func (h *harness) logEntries() []map[string]any {
	h.t.Helper()
	f, err := os.Open(h.logPath)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e map[string]any
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			h.t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func dev(name string, extra ...any) map[string]any {
	m := map[string]any{"device": name}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// TestToolsFollowGroups: disabled groups are not offered; list_devices always is.
func TestToolsFollowGroups(t *testing.T) {
	all := []string{"audit_log", "boot_to_bios", "device_status", "event_log", "hardware_info", "kvm_connect", "kvm_disconnect",
		"kvm_mouse", "kvm_screenshot", "kvm_send_keys", "kvm_type_text", "list_devices", "power_action",
		"sol_connect", "sol_disconnect", "sol_read", "sol_send_keys", "sol_type_text"}
	h := newHarness(t)
	h.start()
	if got := h.toolNames(); !reflect.DeepEqual(got, all) {
		t.Fatalf("defaults: %v", got)
	}

	h = newHarness(t)
	h.cfg.ReadOnly = true
	h.start()
	if got := h.toolNames(); !reflect.DeepEqual(got, []string{"audit_log", "device_status", "event_log", "hardware_info",
		"kvm_connect", "kvm_disconnect", "kvm_screenshot", "list_devices", "sol_connect", "sol_disconnect", "sol_read"}) {
		t.Fatalf("read only: %v", got)
	}

	h = newHarness(t)
	h.cfg.Groups[GroupRead] = false
	h.start()
	if got := h.toolNames(); !reflect.DeepEqual(got, []string{"boot_to_bios", "kvm_connect", "kvm_disconnect", "kvm_mouse",
		"kvm_send_keys", "kvm_type_text", "list_devices", "power_action", "sol_connect", "sol_disconnect", "sol_send_keys", "sol_type_text"}) {
		t.Fatalf("read off: %v", got)
	}

	h = newHarness(t)
	h.cfg.Groups[GroupRead] = false
	h.cfg.Groups[GroupKVMInput] = false
	h.start()
	if got := h.toolNames(); !reflect.DeepEqual(got, []string{"boot_to_bios", "list_devices", "power_action"}) {
		t.Fatalf("read and kvm_input off: %v", got)
	}
}

// TestListDevicesShowsAllowedFavorites: only allowed favorites, with TLS, pin and password
// state, never the password; no device is contacted.
func TestListDevicesShowsAllowedFavorites(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	h.addDevice("NUC", "192.0.2.10", false)
	h.addDevice("Hidden", "hidden.example.com", true)
	h.favs = append(h.favs, favorites.Favorite{Name: "New TLS", Host: "new.example.com", Username: "admin", UseTLS: true})
	h.cfg.AllowedDevices = []string{"Lab Server", "NUC", "New TLS"}
	h.start()
	out := h.mustOK("list_devices", nil)
	devs, _ := out["devices"].([]any)
	if len(devs) != 3 {
		t.Fatalf("%v", out)
	}
	want := []map[string]any{
		{"name": "Lab Server", "host": "lab.example.com", "username": "admin", "tls": true, "certificate_pinned": true, "password_stored": true},
		{"name": "NUC", "host": "192.0.2.10", "username": "admin", "tls": false, "certificate_pinned": false, "password_stored": true},
		{"name": "New TLS", "host": "new.example.com", "username": "admin", "tls": true, "certificate_pinned": false, "password_stored": false},
	}
	for i, w := range want {
		d := devs[i].(map[string]any)
		for k, v := range w {
			if d[k] != v {
				t.Errorf("device %d %s = %v, want %v", i, k, d[k], v)
			}
		}
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("list_devices connected %d times", n)
	}
}

// TestDeviceStatusReportsPowerVersionModeTLS uses the pinned fingerprint and the keychain
// password, and reports the graceful shutdown support.
func TestDeviceStatusReportsPowerVersionModeTLS(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("Lab Server", "lab.example.com", true)
	f.States = []power.PowerState{power.PowerOn, power.PowerOffHard, power.PowerOffSoftGraceful}
	h.start()
	out := h.mustOK("device_status", dev("Lab Server"))
	for k, v := range map[string]any{"device": "Lab Server", "power_state": "on", "power_state_code": float64(2), "amt_version": "16.1.25", "control_mode": "ACM", "tls": true, "graceful_shutdown_supported": true} {
		if out[k] != v {
			t.Errorf("%s = %v, want %v", k, out[k], v)
		}
	}
	if _, ok := out["warnings"]; ok {
		t.Errorf("warnings on a TLS device: %v", out["warnings"])
	}
	c := h.connectCalls()
	if len(c) != 1 || c[0].Fingerprint != testFP || c[0].Password != testPassword || !c[0].UseTLS || c[0].Host != "lab.example.com" || c[0].Username != "admin" {
		t.Fatalf("connect %+v", c)
	}
	if f.IdleClosed() == 0 {
		t.Fatal("session not closed after the call")
	}
}

// TestDeviceStatusReportsDeniedParts: a limited account still gets a status.
func TestDeviceStatusReportsDeniedParts(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.St = amt.Status{Denied: amt.StatusPower | amt.StatusVersion, ControlMode: amt.ControlModeClient}
	f.StatesErr = amt.ErrAccessDenied
	h.start()
	out := h.mustOK("device_status", dev("NUC"))
	if out["power_state"] != "unknown" || out["control_mode"] != "CCM" || !reflect.DeepEqual(out["not_permitted"], []any{"power_state", "amt_version", "power_capabilities"}) {
		t.Fatalf("%v", out)
	}
}

// TestTLSFavoriteWithoutPinIsRefused: the MCP server never pins and does not even connect.
func TestTLSFavoriteWithoutPinIsRefused(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	if err := h.keys.DeleteFingerprint("lab.example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.mustFail("device_status", dev("Lab Server"), "Connect once with the OpenLAMT app to pin the certificate")
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}

// TestPlainFavoriteCarriesWarning: without TLS the tool works and warns.
func TestPlainFavoriteCarriesWarning(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	out := h.mustOK("device_status", dev("NUC"))
	w, _ := out["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(w[0].(string), "not encrypted") || out["tls"] != false {
		t.Fatalf("%v", out)
	}
	if c := h.connectCalls(); len(c) != 1 || c[0].UseTLS || c[0].Fingerprint != "" {
		t.Fatalf("connect %+v", c)
	}
}

// TestDeviceLookupErrors: not allowed, unknown, ambiguous, missing password, keychain down.
func TestDeviceLookupErrors(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	h.addDevice("Twin", "a.example.com", false)
	h.addDevice("Twin", "b.example.com", false)
	h.addDevice("NoPass", "nopass.example.com", false)
	if err := h.keys.Delete("nopass.example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	h.cfg.AllowedDevices = []string{"Twin", "NoPass", "Ghost"}
	h.start()
	h.mustFail("device_status", dev("Lab Server"), "not in allowed_devices")
	h.mustFail("device_status", dev("Ghost"), "No favorite named \"Ghost\"")
	h.mustFail("device_status", dev("Twin"), "2 favorites are named \"Twin\"")
	h.mustFail("device_status", dev("NoPass"), "No password is stored")
	h.mustFail("device_status", dev(""), "device is required")
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}

// TestDeviceErrorsAreExplained: the server's own texts for login, permission and certificate.
func TestDeviceErrorsAreExplained(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.addDevice("Gone", "gone.example.com", false)
	delete(h.devices, "gone.example.com")
	h.start()
	f.SetStatusErr(session.ErrAuth)
	h.mustFail("device_status", dev("NUC"), "rejected the user name or password")
	f.SetStatusErr(nil)
	f.HWErr = amt.ErrAccessDenied
	h.mustFail("hardware_info", dev("NUC"), "lacks the permission")
	h.mustFail("device_status", dev("Gone"), "Cannot reach")
}

func TestFingerprintMismatchIsExplained(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	h.start()
	h.devices["lab.example.com"].StatusErr = &amt.FingerprintMismatchError{Host: "lab.example.com", Expected: testFP, Actual: strings.Repeat("cd", 32)}
	h.mustFail("device_status", dev("Lab Server"), "TLS certificate of the device changed")
}

// TestPowerActionFollowsTheAppRules: on when on, anything but on when off, and shutdown
// without Off - Soft Graceful are refused before a command is sent.
func TestPowerActionFollowsTheAppRules(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	h.mustFail("power_action", dev("NUC", "action", "on"), "already on")
	h.mustFail("power_action", dev("NUC", "action", "shutdown"), "does not offer a graceful shutdown")
	out := h.mustOK("power_action", dev("NUC", "action", "off"))
	if out["power_state_before"] != "on" || out["action"] != "off" {
		t.Fatalf("%v", out)
	}
	f.SetStatus(amt.Status{PowerState: models.PowerStateOffSoft})
	h.mustFail("power_action", dev("NUC", "action", "reset"), "device is off")
	h.mustOK("power_action", dev("NUC", "action", "on"))
	f.SetStatus(amt.Status{PowerState: models.PowerStateOn})
	f.SetPowerStates([]power.PowerState{power.PowerOffSoftGraceful}, nil)
	h.mustOK("power_action", dev("NUC", "action", "shutdown"))
	if got := f.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.PowerOff, amt.PowerOn, amt.PowerShutdown}) {
		t.Fatalf("powered %v", got)
	}
	h.mustFail("power_action", dev("NUC", "action", "hibernate"), "action")
}

// TestPowerActionUnknownStateAllowsAll: a state the account may not read locks nothing but
// shutdown (as in the app).
func TestPowerActionUnknownStateAllowsAll(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.St = amt.Status{Denied: amt.StatusPower}
	h.start()
	h.mustOK("power_action", dev("NUC", "action", "on"))
	h.mustOK("power_action", dev("NUC", "action", "reset"))
	if got := f.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.PowerOn, amt.PowerReset}) {
		t.Fatalf("powered %v", got)
	}
}

// TestPowerActionDestructiveGroupOff: only power on remains.
func TestPowerActionDestructiveGroupOff(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.St.PowerState = models.PowerStateOffHard
	h.cfg.Groups[GroupDestructive] = false
	h.start()
	h.mustFail("power_action", dev("NUC", "action", "reset"), `tool group "destructive" is off`)
	h.mustOK("power_action", dev("NUC", "action", "on"))
	if n := len(h.connectCalls()); n != 1 {
		t.Fatalf("connects %d", n)
	}
}

// TestPowerErrorShowsReturnValue: the AMT ReturnValue and its meaning reach the agent.
func TestPowerErrorShowsReturnValue(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.PowerErr = &amt.PowerError{Action: amt.PowerOff, ReturnValue: 2}
	h.start()
	h.mustFail("power_action", dev("NUC", "action", "off"), "ReturnValue 2")
	f.PowerErr = &amt.PowerError{Action: amt.PowerReset, ReturnValue: 4097}
	h.mustFail("power_action", dev("NUC", "action", "reset"), "invalid state transition")
}

// TestBootToBIOSConsentFlow: without a code the agent is told to ask the person at the device;
// with the right code the boot runs. A wrong or malformed code sends no power command.
func TestBootToBIOSConsentFlow(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Consent, f.ConsentCode = true, "123456"
	f.St.ControlMode = amt.ControlModeClient
	h.start()
	h.mustFail("boot_to_bios", dev("NUC"), "call boot_to_bios again with consent_code")
	h.mustFail("boot_to_bios", dev("NUC", "consent_code", "654321"), "did not accept the consent code")
	h.mustFail("boot_to_bios", dev("NUC", "consent_code", "12345"), "six digits")
	if len(f.Powered()) != 0 {
		t.Fatalf("powered %v", f.Powered())
	}
	out := h.mustOK("boot_to_bios", dev("NUC", "consent_code", " 123456 "))
	if got := f.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.BootToBIOS}) || out["power_state_before"] != "on" {
		t.Fatalf("powered %v out %v", got, out)
	}
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "123456") || strings.Contains(string(b), "654321") {
			t.Fatalf("action log holds a consent code: %s", b)
		}
	}
}

// TestBootToBIOSOnOffDeviceIsNotDestructive: like in the app it only powers the device on.
func TestBootToBIOSOnOffDeviceIsNotDestructive(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.Groups[GroupDestructive] = false
	h.start()
	h.mustFail("boot_to_bios", dev("NUC"), `tool group "destructive" is off`)
	f.SetStatus(amt.Status{PowerState: models.PowerStateOffSoft})
	h.mustOK("boot_to_bios", dev("NUC"))
	if got := f.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.BootToBIOS}) {
		t.Fatalf("powered %v", got)
	}
}

func testEvents() []amt.EventLogEntry {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	return []amt.EventLogEntry{
		{Time: base.Add(3 * time.Hour), Severity: amt.SeverityCritical, Source: "BIOS", Description: "Memory error"},
		{Time: base.Add(2 * time.Hour), Severity: amt.SeverityInfo, Source: "BIOS", Description: "Starting operating system boot process"},
		{Time: base.Add(time.Hour), Severity: amt.SeverityWarning, Source: "Intel(r) ME", Description: "Fan speed low"},
		{Time: base, Severity: amt.SeverityInfo, Source: "BIOS", Description: "Performing PCI configuration"},
	}
}

func TestEventLogFilters(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Events = testEvents()
	h.start()
	out := h.mustOK("event_log", dev("NUC"))
	if out["total"] != float64(4) || out["returned"] != float64(4) {
		t.Fatalf("%v", out)
	}
	out = h.mustOK("event_log", dev("NUC", "min_severity", "warning"))
	if out["matched"] != float64(2) {
		t.Fatalf("severity: %v", out)
	}
	out = h.mustOK("event_log", dev("NUC", "since", "2026-11-20T11:30:00Z", "contains", "boot", "limit", 1))
	e, _ := out["entries"].([]any)
	if len(e) != 1 || e[0].(map[string]any)["description"] != "Starting operating system boot process" || e[0].(map[string]any)["time"] != "2026-11-20T12:00:00Z" {
		t.Fatalf("filtered: %v", out)
	}
	out = h.mustOK("event_log", dev("NUC", "limit", 2))
	if out["returned"] != float64(2) || out["matched"] != float64(4) {
		t.Fatalf("limit: %v", out)
	}
	h.mustFail("event_log", dev("NUC", "since", "yesterday"), "RFC 3339")
	h.mustFail("event_log", dev("NUC", "limit", 1000), "limit")
	h.mustFail("event_log", dev("NUC", "min_severity", "fatal"), "min_severity")
}

func TestAuditLogFilters(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	f.Audit = []amt.AuditLogEntry{
		{Time: base.Add(time.Hour), App: "Remote Control", Event: "Performed a power-up", InitiatorType: 0, Initiator: "admin", Address: "192.0.2.50"},
		{Time: base, App: "Security Admin", Event: "Provisioning Started", InitiatorType: 2},
	}
	h.start()
	out := h.mustOK("audit_log", dev("NUC"))
	e, _ := out["entries"].([]any)
	if len(e) != 2 || e[0].(map[string]any)["initiator"] != "admin" || e[1].(map[string]any)["initiator"] != "local" {
		t.Fatalf("%v", out)
	}
	out = h.mustOK("audit_log", dev("NUC", "contains", "192.0.2.50"))
	if out["matched"] != float64(1) {
		t.Fatalf("contains: %v", out)
	}
	f.AuditErr = amt.ErrAccessDenied
	h.mustFail("audit_log", dev("NUC"), "lacks the permission")
}

func TestHardwareInfo(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.HW = amt.HardwareInfo{
		System: amt.SystemInfo{Manufacturer: "ACME Corp GmbH", Model: "NUC-1"},
		BIOS:   amt.BIOSInfo{Version: "1.2", ReleaseDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		CPUs:   []amt.CPUInfo{{Socket: "CPU 0", Name: "Example CPU", MaxClockMHz: 4000}},
		Memory: []amt.MemoryModule{{Bank: "BANK 0", CapacityBytes: 16 << 30, Type: "DDR5"}},
		AMT:    amt.AMTInfo{Version: "16.1.25", Build: "2000"},
	}
	h.start()
	out := h.mustOK("hardware_info", dev("NUC"))
	sys, _ := out["system"].(map[string]any)
	bios, _ := out["bios"].(map[string]any)
	mem, _ := out["memory"].([]any)
	if sys["manufacturer"] != "ACME Corp GmbH" || bios["release_date"] != "2026-01-02" || len(mem) != 1 || mem[0].(map[string]any)["capacity_bytes"] != float64(16<<30) {
		t.Fatalf("%v", out)
	}
}

// TestClientConfirmation: with require_client_confirmation a write tool runs only after the
// person in the MCP client accepted; nothing reaches the device before.
func TestClientConfirmation(t *testing.T) {
	var asked []string
	answer := &mcp.ElicitResult{Action: "decline"}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return answer, nil
	}
	h.start()
	h.mustFail("power_action", dev("NUC", "action", "off"), "not confirmed")
	if len(asked) != 1 || !strings.Contains(asked[0], "NUC") || !strings.Contains(asked[0], "off") || len(h.connectCalls()) != 0 {
		t.Fatalf("asked %v connects %d", asked, len(h.connectCalls()))
	}
	answer = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": false}}
	h.mustFail("power_action", dev("NUC", "action", "off"), "not confirmed")
	answer = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	h.mustOK("power_action", dev("NUC", "action", "off"))
	if got := f.Powered(); !reflect.DeepEqual(got, []amt.PowerAction{amt.PowerOff}) {
		t.Fatalf("powered %v", got)
	}
	h.mustOK("device_status", dev("NUC")) // read tools ask nothing
	if len(asked) != 3 {
		t.Fatalf("asked %d times", len(asked))
	}
}

// TestClientConfirmationWithoutElicitationIsRefused: a client that cannot ask never runs it.
func TestClientConfirmationWithoutElicitationIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.RequireClientConfirmation = true
	h.start()
	h.mustFail("power_action", dev("NUC", "action", "reset"), "cannot ask for confirmation")
	if len(f.Powered()) != 0 || len(h.connectCalls()) != 0 {
		t.Fatalf("powered %v", f.Powered())
	}
}

// TestActionLogRecordsCalls: one JSON line per call with tool, device, action and outcome.
func TestActionLogRecordsCalls(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.PowerErr = &amt.PowerError{Action: amt.PowerReset, ReturnValue: 4}
	h.cfg.AllowedDevices = []string{"NUC"}
	h.start()
	h.mustOK("device_status", dev("NUC"))
	h.mustFail("power_action", dev("NUC", "action", "reset"), "ReturnValue 4")
	h.mustFail("device_status", dev("Other"), "allowed_devices")
	got := h.logEntries()
	want := []map[string]any{
		{"tool": "device_status", "device": "NUC", "outcome": "ok"},
		{"tool": "power_action", "device": "NUC", "action": "reset", "outcome": "error"},
		{"tool": "device_status", "device": "Other", "outcome": "refused"},
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("entry %d %s = %v, want %v", i, k, got[i][k], v)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, got[i]["time"].(string)); err != nil {
			t.Errorf("entry %d time: %v", i, err)
		}
	}
	if fi, err := os.Stat(h.logPath); err != nil || (fi.Mode().Perm()&0o077 != 0 && !isWindows()) {
		t.Fatalf("log mode %v %v", fi.Mode(), err)
	}
}

// TestActionLogOff writes nothing.
func TestActionLogOff(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.ActionLog = false
	h.start()
	h.mustOK("device_status", dev("NUC"))
	if _, err := os.Stat(h.cfg.ActionLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("log written: %v", err)
	}
}

func isWindows() bool { return runtime.GOOS == "windows" }

// TestKeychainUnavailable: a keychain that cannot be read is named as such, once per device.
func TestKeychainUnavailable(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	h.keys.Err = errors.New("the name org.freedesktop.secrets was not provided")
	h.start()
	h.mustFail("device_status", dev("Lab Server"), "Cannot read the system keychain")
	out := h.mustOK("list_devices", nil)
	d := out["devices"].([]any)[0].(map[string]any)
	if w, _ := d["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "Secret Service") || d["password_stored"] != false {
		t.Fatalf("%v", d)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}
