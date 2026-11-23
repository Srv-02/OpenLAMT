package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	logins  *fakeLogins                                         // the LoginStore the server gets
	// loginStore, if set, is the LoginStore the server gets instead of logins.
	loginStore session.LoginStore
	logger     *slog.Logger // the server's stderr log; nil discards
	// mgr, if set, replaces favs: a real favorites manager (file in a temp folder, the memory
	// keychain), so the favorites tools are offered.
	mgr     *favorites.Manager
	scanner Scanner
	fetchFP func(ctx context.Context, host string) (string, error)
	// store, if set, is what the server reads passwords and fingerprints from instead of keys: a
	// pin file over keys (certificates.pin_file). pinPath is that file.
	store   keychain.Store
	pinPath string

	mu       sync.Mutex
	connects []amt.Config
	logPath  string
	cs       *mcp.ClientSession
	srv      *Server
}

func newHarness(t *testing.T) *harness {
	dir := t.TempDir()
	h := &harness{t: t, cfg: Defaults(dir), keys: keychain.NewMemory(), devices: map[string]*session.Fake{}}
	h.logins = &fakeLogins{keys: h.keys}
	h.cfg.ActionLogPath = filepath.Join(dir, ActionLogFileName)
	return h
}

// addDevice saves a favorite with password (and pin for TLS) and returns its fake device.
func (h *harness) addDevice(name, host string, tls bool) *session.Fake {
	return h.addDeviceAs(name, host, "admin", tls)
}

// addDeviceAs is addDevice for a favorite that signs in as user.
func (h *harness) addDeviceAs(name, host, user string, tls bool) *session.Fake {
	h.favs = append(h.favs, favorites.Favorite{Name: name, Host: host, Username: user, UseTLS: tls})
	if err := h.keys.Set(host, user, testPassword); err != nil {
		h.t.Fatal(err)
	}
	if tls {
		if err := h.keys.SetFingerprint(host, user, testFP); err != nil {
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

// key is the favKey of the favorite named name, which the managers keep its sessions by.
func (h *harness) key(name string) string {
	h.t.Helper()
	for _, f := range h.favs {
		if f.DisplayName() == name {
			return favKey(f)
		}
	}
	h.t.Fatalf("no favorite %q", name)
	return ""
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
	var favs FavoriteSource = h.favs
	if h.mgr != nil {
		favs = h.mgr
	}
	var keys keychain.Store = h.keys
	if h.store != nil {
		keys = h.store
	}
	var logins session.LoginStore = h.logins
	if h.loginStore != nil {
		logins = h.loginStore
	}
	srv := New(Options{Config: h.cfg, Favorites: favs, Keys: keys, Connect: h.connect, Logins: logins, Log: log, Logger: h.logger,
		Scanner: h.scanner, FetchFingerprint: h.fetchFP, Version: "test"})
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

// fakeLogins is the LoginStore of the tests: it records the calls and follows them in the
// memory keychain, as *favorites.Manager does. err, if set, fails every call.
type fakeLogins struct {
	mu    sync.Mutex
	keys  *keychain.Memory
	calls []string // "login host old new" or "pin host user old new"; never the password
	pws   []string // the passwords of the login calls
	err   error
}

func (l *fakeLogins) ChangeLogin(host, oldUser, newUser, newPassword string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "login "+host+" "+oldUser+" "+newUser)
	l.pws = append(l.pws, newPassword)
	if l.err != nil {
		return l.err
	}
	return l.keys.Set(host, newUser, newPassword)
}

func (l *fakeLogins) ChangeFingerprint(host, user, oldFP, newFP string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "pin "+host+" "+user+" "+oldFP+" "+newFP)
	if l.err != nil {
		return l.err
	}
	return l.keys.SetFingerprint(host, user, newFP)
}

func (l *fakeLogins) recorded() ([]string, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls), slices.Clone(l.pws)
}

func dev(name string, extra ...any) map[string]any {
	m := map[string]any{"device": name}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// TestToolsFollowGroups: disabled groups are not offered; list_devices always is. Reading
// settings and users stays in read_only; red changes need their group and destructive.
func TestToolsFollowGroups(t *testing.T) {
	settingsWrites := []string{"set_general", "set_remote_features", "set_tls_certificate", "sync_clock"}
	settingsRed := []string{"disable_amt_network", "set_environment_detection", "set_network"}
	usersWrites := []string{"add_user", "delete_user", "set_user_enabled", "update_user"}
	usersRed := []string{"set_admin_account"}
	tools := func(groups ...[]string) []string {
		var out []string
		for _, g := range groups {
			out = append(out, g...)
		}
		slices.Sort(out)
		return out
	}
	readTools := []string{"audit_log", "device_status", "event_log", "get_settings", "hardware_info", "list_users"}
	// Sessions open with read or kvm_input; the screen and the console output are read tools.
	sessions := []string{"kvm_connect", "kvm_disconnect", "sol_connect", "sol_disconnect"}
	kvmRead := append([]string{"kvm_screenshot", "sol_read"}, sessions...)
	input := []string{"kvm_mouse", "kvm_send_keys", "kvm_type_text", "sol_send_keys", "sol_type_text"}
	media := []string{"media_boot_next", "media_mount", "media_status", "media_unmount"}
	power := []string{"boot_from_iso", "boot_to_bios", "power_action"}
	// scan_network of the group favorites only reads: it stays in read_only (the favorites tools
	// that write need a favorites manager, which this harness does not have; see
	// TestFavoritesToolsFollowGroupAndReadOnly).
	always := []string{"list_devices", "scan_network"}
	for _, c := range []struct {
		name  string
		setup func(*Config)
		want  []string
	}{
		{"defaults", func(*Config) {},
			tools(always, readTools, kvmRead, input, media, power, settingsWrites, settingsRed, usersWrites, usersRed)},
		{"read only", func(c *Config) { c.ReadOnly = true }, tools(always, readTools, kvmRead)},
		{"read off", func(c *Config) { c.Groups[GroupRead] = false },
			tools(always, sessions, input, media, power, settingsWrites, settingsRed, usersWrites, usersRed)},
		{"read and kvm_input off", func(c *Config) { c.Groups[GroupRead], c.Groups[GroupKVMInput] = false, false },
			tools(always, media, power, settingsWrites, settingsRed, usersWrites, usersRed)},
		{"destructive off", func(c *Config) { c.Groups[GroupDestructive] = false },
			tools(always, readTools, kvmRead, input, media, power, settingsWrites, usersWrites)},
		{"settings and users off", func(c *Config) { c.Groups[GroupSettings], c.Groups[GroupUsers] = false, false },
			tools(always, []string{"audit_log", "device_status", "event_log", "hardware_info"}, kvmRead, input, media, power)},
	} {
		h := newHarness(t)
		c.setup(&h.cfg)
		h.start()
		if got := h.toolNames(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
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
	h.mustFail("device_status", dev("Lab Server"), "runs `openlamt-mcp trust \"Lab Server\"`")
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
	h.mustFail("device_status", dev("NoPass"), `No password found for "NoPass"`)
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
	text := h.mustFail("device_status", dev("Lab Server"), "TLS certificate of the device changed")
	if !strings.Contains(text, "`openlamt-mcp trust ") || !strings.Contains(text, "certificates.pin_file") {
		t.Fatalf("%s", text)
	}
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

// TestBootToBIOSRefusesSleepingDeviceFirst (issue #142): a device in S3 is refused before the
// consent starts, so the agent is not asked for a code that the dark screen never shows.
func TestBootToBIOSRefusesSleepingDeviceFirst(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Consent, f.ConsentCode = true, "123456"
	f.St = amt.Status{PowerState: models.PowerStateSleepDeep, ControlMode: amt.ControlModeClient}
	h.start()
	h.mustFail("boot_to_bios", dev("NUC"), "The device is asleep (S3)")
	if f.BootConsents() != 0 || len(f.Powered()) != 0 {
		t.Fatalf("consents %d powered %v", f.BootConsents(), f.Powered())
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

// TestLogsShortenAgentStrings (#118): a huge device name or path from the agent is shortened in
// the action log, on stderr and in the error text, with its length.
func TestLogsShortenAgentStrings(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.PowerErr = &amt.PowerError{Action: amt.PowerReset, ReturnValue: 4}
	var stderr bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&stderr, nil))
	h.start()
	const n = 100000
	long := strings.Repeat("x", n)
	text := h.mustFail("device_status", dev(long), "No favorite named")
	padded := "NUC" + strings.Repeat(" ", n) // found: the lookup trims it
	h.mustFail("power_action", dev(padded, "action", "reset"), "ReturnValue 4")
	path, _, isErr := h.call("media_mount", dev("NUC", "path", filepath.Join(t.TempDir(), long+".iso")))
	if !isErr || !strings.Contains(path, ".iso (") { // the end of the path, the file name, is kept
		t.Fatalf("media_mount of a missing path: %v %.200s", isErr, path)
	}
	for _, msg := range []string{text, path} {
		if len(msg) > 1000 || !strings.Contains(msg, "characters)") {
			t.Errorf("error text of %d bytes: %.200s", len(msg), msg)
		}
	}
	entries := h.logEntries()
	if len(entries) != 3 {
		t.Fatalf("%d entries", len(entries))
	}
	for i, e := range entries {
		b, _ := json.Marshal(e)
		if len(b) > 2000 {
			t.Errorf("entry %d has %d bytes: %.300s", i, len(b), b)
		}
	}
	if d := entries[0]["device"].(string); !strings.HasPrefix(d, strings.Repeat("x", maxLogged)+"…") || !strings.HasSuffix(d, fmt.Sprintf("(%d characters)", n)) {
		t.Errorf("device %q", d)
	}
	if stderr.Len() == 0 || stderr.Len() > 2000 {
		t.Errorf("stderr has %d bytes: %.300s", stderr.Len(), stderr.String())
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
	text := h.mustFail("device_status", dev("Lab Server"), "Cannot read the system keychain")
	if !strings.Contains(text, "certificates.pin_file") {
		t.Fatalf("%s", text)
	}
	out := h.mustOK("list_devices", nil)
	d := out["devices"].([]any)[0].(map[string]any)
	if w, _ := d["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "Secret Service") || !strings.Contains(w[0].(string), "certificates.pin_file") || d["password_stored"] != false {
		t.Fatalf("%v", d)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}
