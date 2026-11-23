package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/scan"
)

const otherFP = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"

// newFavHarness is a harness with a real favorites manager (a file in a temp folder and the
// memory keychain), so the favorites tools are offered.
func newFavHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), h.keys)
	return h
}

// saved adds a favorite with password (and pin for TLS) to the manager.
func (h *harness) saved(name, host string, tls bool) favorites.Favorite {
	h.t.Helper()
	f := favorites.Favorite{Name: name, Host: host, Username: "admin", UseTLS: tls}
	if err := h.mgr.Add(f); err != nil {
		h.t.Fatal(err)
	}
	if err := h.keys.Set(host, "admin", testPassword); err != nil {
		h.t.Fatal(err)
	}
	if tls {
		if err := h.keys.SetFingerprint(host, "admin", testFP); err != nil {
			h.t.Fatal(err)
		}
	}
	return f
}

func (h *harness) managed() []favorites.Favorite {
	h.t.Helper()
	list, err := h.mgr.List()
	if err != nil {
		h.t.Fatal(err)
	}
	return list
}

// fakeScanner returns results for every scan and records the targets.
type fakeScanner struct {
	mu      sync.Mutex
	results []scan.Result
	targets []netip.Addr
}

func (f *fakeScanner) Scan(_ context.Context, targets []netip.Addr, onProgress func(done, total int), onResult func(scan.Result)) {
	f.mu.Lock()
	f.targets = targets
	f.mu.Unlock()
	for i := range targets {
		onProgress(i+1, len(targets))
	}
	for _, r := range f.results {
		onResult(r)
	}
}

// TestFavoritesToolsFollowGroupAndReadOnly: scan_network stays in read_only (it only reads);
// adding, changing and deleting favorites need the group and no read_only.
func TestFavoritesToolsFollowGroupAndReadOnly(t *testing.T) {
	writes := []string{"add_favorite", "delete_favorite", "update_favorite"}
	for _, c := range []struct {
		name      string
		setup     func(*Config)
		wantScan  bool
		wantWrite bool
	}{
		{"defaults", func(*Config) {}, true, true},
		{"read only", func(c *Config) { c.ReadOnly = true }, true, false},
		{"favorites off", func(c *Config) { c.Groups[GroupFavorites] = false }, false, false},
	} {
		h := newFavHarness(t)
		c.setup(&h.cfg)
		h.start()
		names := h.toolNames()
		if slices.Contains(names, "scan_network") != c.wantScan {
			t.Errorf("%s: scan_network offered = %v", c.name, !c.wantScan)
		}
		for _, w := range writes {
			if slices.Contains(names, w) != c.wantWrite {
				t.Errorf("%s: %s offered = %v", c.name, w, !c.wantWrite)
			}
		}
	}
}

// TestScanNetworkReportsDevices: the app's scan with its limits; results by address, known
// favorites marked, a device that only answers the ping gets a note; nothing needs a password.
func TestScanNetworkReportsDevices(t *testing.T) {
	h := newFavHarness(t)
	h.saved("NUC", "192.0.2.10", false)
	h.saved("Lab", "lab.example.com", true)
	sc := &fakeScanner{results: []scan.Result{
		{Addr: netip.MustParseAddr("192.0.2.30"), RMCP: true},
		{Addr: netip.MustParseAddr("192.0.2.20"), Name: "lab.example.com", Version: "16.1", TLS: true, HTTP: true},
		{Addr: netip.MustParseAddr("192.0.2.10"), Version: "12.0", HTTP: true},
		{Addr: netip.MustParseAddr("192.0.2.40"), Name: "new.example.com", Version: "18.0", TLS: true},
	}}
	h.scanner = sc
	h.cfg.ReadOnly = true
	h.start()
	out := h.mustOK("scan_network", map[string]any{"range": "192.0.2.0/24"})
	if len(sc.targets) != 254 || out["scanned"] != float64(254) {
		t.Fatalf("targets %d, out %v", len(sc.targets), out)
	}
	devs := out["devices"].([]any)
	var got []string
	for _, d := range devs {
		m := d.(map[string]any)
		got = append(got, m["address"].(string)+" "+strings.Join([]string{boolStr(m["already_favorite"]), boolStr(m["tls"]), boolStr(m["http"])}, ","))
	}
	want := []string{"192.0.2.10 true,false,true", "192.0.2.20 true,true,true", "192.0.2.30 false,false,false", "192.0.2.40 false,true,false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if note, _ := devs[2].(map[string]any)["note"].(string); !strings.Contains(note, "not activated") {
		t.Fatalf("note %q", note)
	}
	if v := devs[3].(map[string]any)["amt_version"]; v != "18.0" {
		t.Fatalf("version %v", v)
	}
	h.mustFail("scan_network", map[string]any{"range": "192.0.2.0/16"}, "up to 4096 addresses")
	h.mustFail("scan_network", map[string]any{"range": "lab.example.com"}, "must be an IPv4 network")
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("the scan connected %d times", n)
	}
	var actions []string
	for _, e := range h.logEntries() {
		actions = append(actions, e["tool"].(string)+" "+e["outcome"].(string)+" "+e["action"].(string))
	}
	if len(actions) != 3 || actions[0] != "scan_network ok scan 192.0.2.0/24 found 4" || !strings.HasPrefix(actions[1], "scan_network refused") {
		t.Fatalf("log %q", actions)
	}
}

func boolStr(v any) string {
	if b, _ := v.(bool); b {
		return "true"
	}
	return "false"
}

// TestAddFavoriteWithoutQuestion: without a client that can ask, a favorite is saved without a
// pin and the answer says what a person still has to do; no certificate is read. Duplicates,
// names outside allowed_devices and unsafe values are refused.
func TestAddFavoriteWithoutQuestion(t *testing.T) {
	h := newFavHarness(t)
	h.saved("NUC", "192.0.2.10", false)
	h.fetchFP = func(context.Context, string) (string, error) {
		t.Error("the certificate was read")
		return testFP, nil
	}
	h.cfg.AllowedDevices = []string{"NUC", "Lab Server", "lab2.example.com", "Bad"}
	h.start()
	out := h.mustOK("add_favorite", map[string]any{"name": "Lab Server", "host": " LAB.example.com ", "tls": true})
	if out["host"] != "lab.example.com" || out["username"] != "admin" || out["certificate_pinned"] != false || out["password_found"] != false {
		t.Fatalf("%v", out)
	}
	steps, _ := json.Marshal(out["next_steps"])
	if !strings.Contains(string(steps), "openlamt-mcp password \\\"Lab Server\\\"") || !strings.Contains(string(steps), "openlamt-mcp trust \\\"Lab Server\\\"") {
		t.Fatalf("next steps %s", steps)
	}
	if list := h.managed(); len(list) != 2 || list[1] != (favorites.Favorite{Name: "Lab Server", Host: "lab.example.com", Username: "admin", UseTLS: true}) {
		t.Fatalf("%+v", list)
	}
	// Without a name the host is the name.
	if out := h.mustOK("add_favorite", map[string]any{"host": "lab2.example.com", "username": "ops", "tls": false}); out["name"] != "lab2.example.com" {
		t.Fatalf("%v", out)
	}
	h.mustFail("add_favorite", map[string]any{"name": "NUC", "host": "192.0.2.11", "tls": false}, "names must be unique")
	h.mustFail("add_favorite", map[string]any{"name": "Bad", "host": "192.0.2.10", "tls": false}, "exists already")
	h.mustFail("add_favorite", map[string]any{"name": "Elsewhere", "host": "192.0.2.12", "tls": false}, "not in allowed_devices")
	h.mustFail("add_favorite", map[string]any{"name": `Bad`, "host": "192.0.2.12:16992", "tls": false}, "port")
	h.mustFail("add_favorite", map[string]any{"name": `Bad`, "host": "a/b.example.com", "tls": false}, "not a host name")
	h.mustFail("add_favorite", map[string]any{"name": "x\"; rm -rf ~", "host": "192.0.2.12", "tls": false}, "not allowed")
	h.mustFail("add_favorite", map[string]any{"name": "Bad", "host": "192.0.2.12", "username": "../root", "tls": false}, "not allowed")
	if n := len(h.managed()); n != 3 {
		t.Fatalf("%d favorites", n)
	}
}

// TestAddFavoriteTrustsCertificateOnlyAfterAccept: with a client that can ask, the person sees
// the fingerprint and the pin is stored only when they tick it; a certificate that changes
// between the question and the answer saves nothing.
func TestAddFavoriteTrustsCertificateOnlyAfterAccept(t *testing.T) {
	var asked []string
	answer := map[string]any{trustField: true}
	action := "accept"
	h := newFavHarness(t)
	fps := []string{testFP}
	var mu sync.Mutex
	h.fetchFP = func(_ context.Context, host string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		fp := fps[0]
		if len(fps) > 1 {
			fps = fps[1:]
		}
		return fp, nil
	}
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: action, Content: answer}, nil
	}
	h.start()
	out := h.mustOK("add_favorite", map[string]any{"name": "Lab", "host": "lab.example.com", "tls": true})
	if pin, _ := h.keys.GetFingerprint("lab.example.com", "admin"); pin != testFP || out["certificate_pinned"] != true {
		t.Fatalf("pin %q, out %v", pin, out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], fingerprintLines(testFP)) || !strings.Contains(asked[0], "AB:AB:AB:AB:AB:AB:AB:AB\n") {
		t.Fatalf("asked %q", asked)
	}
	if out["certificate_sha256"] != amt.FormatFingerprint(testFP) {
		t.Fatalf("%v", out)
	}

	// Declined: saved without a pin.
	action = "decline"
	out = h.mustOK("add_favorite", map[string]any{"name": "Lab 2", "host": "lab2.example.com", "tls": true})
	if _, err := h.keys.GetFingerprint("lab2.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) || out["certificate_pinned"] != false {
		t.Fatalf("declined: %v %v", err, out)
	}
	// Accepted, but the box not ticked: no pin either.
	action, answer = "accept", map[string]any{trustField: false}
	h.mustOK("add_favorite", map[string]any{"name": "Lab 3", "host": "lab3.example.com", "tls": true})
	if _, err := h.keys.GetFingerprint("lab3.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("unticked: %v", err)
	}
	// The certificate changed between question and answer: refused, nothing saved.
	answer = map[string]any{trustField: true}
	mu.Lock()
	fps = []string{testFP, otherFP}
	mu.Unlock()
	h.mustFail("add_favorite", map[string]any{"name": "Lab 4", "host": "lab4.example.com", "tls": true}, "changed while the question was open")
	if n := len(h.managed()); n != 3 {
		t.Fatalf("%d favorites", n)
	}
	// Without TLS there is no question.
	n := len(asked)
	h.mustOK("add_favorite", map[string]any{"name": "Plain", "host": "192.0.2.10", "tls": false})
	if len(asked) != n {
		t.Fatal("asked for a favorite without TLS")
	}
}

// TestAddFavoriteRequireConfirmation: with require_client_confirmation the person confirms the
// new favorite; a client that cannot ask gets a refusal.
func TestAddFavoriteRequireConfirmation(t *testing.T) {
	h := newFavHarness(t)
	h.cfg.RequireClientConfirmation = true
	h.start()
	h.mustFail("add_favorite", map[string]any{"name": "NUC", "host": "192.0.2.10", "tls": false}, "cannot ask for confirmation")
	if n := len(h.managed()); n != 0 {
		t.Fatalf("%d favorites", n)
	}

	h = newFavHarness(t)
	h.cfg.RequireClientConfirmation = true
	confirm := false
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{confirmField: confirm}}, nil
	}
	h.start()
	h.mustFail("add_favorite", map[string]any{"name": "NUC", "host": "192.0.2.10", "tls": false}, "not confirmed")
	confirm = true
	h.mustOK("add_favorite", map[string]any{"name": "NUC", "host": "192.0.2.10", "tls": false})
	if n := len(h.managed()); n != 1 {
		t.Fatalf("%d favorites", n)
	}
}

// TestUpdateFavoriteAlwaysAsks: a change is orange: it always asks, a client that cannot ask
// gets a refusal. A new host drops password and pin; names stay unique.
func TestUpdateFavoriteAlwaysAsks(t *testing.T) {
	h := newFavHarness(t)
	h.saved("Lab", "lab.example.com", true)
	h.saved("NUC", "192.0.2.10", false)
	h.start()
	h.mustFail("update_favorite", dev("Lab", "host", "lab2.example.com"), "risk warning")
	if list := h.managed(); list[0].Host != "lab.example.com" {
		t.Fatalf("%+v", list)
	}

	h2 := newFavHarness(t)
	h2.saved("Lab", "lab.example.com", true)
	h2.saved("NUC", "192.0.2.10", false)
	var asked []string
	h2.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h2.start()
	h2.mustFail("update_favorite", dev("Lab", "name", "NUC"), "names must be unique")
	h2.mustFail("update_favorite", dev("Lab", "name", "Lab"), "Nothing to change")
	out := h2.mustOK("update_favorite", dev("Lab", "name", "Lab Server", "host", "lab2.example.com"))
	if out["name"] != "Lab Server" || out["host"] != "lab2.example.com" || out["password_found"] != false || out["certificate_pinned"] != false {
		t.Fatalf("%v", out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "host: lab.example.com → lab2.example.com") ||
		!strings.Contains(asked[0], "stay with the old host") {
		t.Fatalf("asked %q", asked)
	}
	for _, host := range []string{"lab.example.com", "lab2.example.com"} {
		if _, err := h2.keys.Get(host, "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("password of %s: %v", host, err)
		}
		if _, err := h2.keys.GetFingerprint(host, "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("pin of %s: %v", host, err)
		}
	}
	// A rename keeps password and pin.
	h2.mustOK("update_favorite", dev("NUC", "name", "NUC 2"))
	if pw, _ := h2.keys.Get("192.0.2.10", "admin"); pw != testPassword {
		t.Fatal("a rename dropped the password")
	}
	var logged []string
	for _, e := range h2.logEntries() {
		if e["tool"] == "update_favorite" && e["outcome"] == "ok" {
			logged = append(logged, e["action"].(string))
		}
	}
	if len(logged) != 2 || logged[0] != "update name: Lab → Lab Server, host: lab.example.com → lab2.example.com" {
		t.Fatalf("logged %q", logged)
	}
}

// TestDeleteFavoriteAlwaysAsks: deleting is orange; it removes the favorite, its password and its pin.
func TestDeleteFavoriteAlwaysAsks(t *testing.T) {
	h := newFavHarness(t)
	h.saved("Lab", "lab.example.com", true)
	h.start()
	h.mustFail("delete_favorite", dev("Lab"), "risk warning")

	h2 := newFavHarness(t)
	h2.saved("Lab", "lab.example.com", true)
	action := "decline"
	h2.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: action, Content: map[string]any{"confirm": true}}, nil
	}
	h2.cfg.AllowedDevices = []string{"Lab"}
	h2.start()
	h2.mustFail("delete_favorite", dev("Lab"), "not confirmed")
	if n := len(h2.managed()); n != 1 {
		t.Fatalf("%d favorites after a decline", n)
	}
	h2.mustFail("delete_favorite", dev("Other"), "not in allowed_devices")
	action = "accept"
	if out := h2.mustOK("delete_favorite", dev("Lab")); out["deleted"] != "Lab" {
		t.Fatalf("%v", out)
	}
	if n := len(h2.managed()); n != 0 {
		t.Fatalf("%d favorites", n)
	}
	if _, err := h2.keys.Get("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("password: %v", err)
	}
	if _, err := h2.keys.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("pin: %v", err)
	}
}

// TestListDevicesReportsPasswordSource: password_found and password_source per favorite, from
// the keychain or passwords.command, without contacting a device; device tools use the
// command's password.
func TestListDevicesReportsPasswordSource(t *testing.T) {
	h := newHarness(t)
	h.addDevice("Lab Server", "lab.example.com", true)
	f := h.addDevice("NUC", "192.0.2.10", false)
	if err := h.keys.Delete("192.0.2.10", "admin"); err != nil {
		t.Fatal(err)
	}
	h.cfg.Passwords.Command = helperCommand(t, "print", "{host}")
	h.start()
	out := h.mustOK("list_devices", nil)
	devs := out["devices"].([]any)
	lab, nuc := devs[0].(map[string]any), devs[1].(map[string]any)
	if lab["password_found"] != true || lab["password_source"] != "keychain" || lab["password_stored"] != true || lab["certificate_pinned"] != true {
		t.Fatalf("lab %v", lab)
	}
	if nuc["password_found"] != true || nuc["password_source"] != "command" || nuc["password_stored"] != false {
		t.Fatalf("nuc %v", nuc)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("list_devices connected %d times", n)
	}
	h.mustOK("device_status", dev("NUC"))
	if c := h.connectCalls(); len(c) != 1 || c[0].Password != "pw-for-192.0.2.10" {
		t.Fatalf("connect %+v", c)
	}
	_ = f
}

// TestListDevicesNamesMissingSteps: a favorite without password or pin says which subcommand a
// person runs.
func TestListDevicesNamesMissingSteps(t *testing.T) {
	h := newHarness(t)
	h.favs = append(h.favs, favorites.Favorite{Name: "New TLS", Host: "new.example.com", Username: "admin", UseTLS: true})
	h.start()
	d := h.mustOK("list_devices", nil)["devices"].([]any)[0].(map[string]any)
	w, _ := json.Marshal(d["warnings"])
	if d["password_found"] != false || !strings.Contains(string(w), "openlamt-mcp password \\\"New TLS\\\"") ||
		!strings.Contains(string(w), "openlamt-mcp trust \\\"New TLS\\\"") {
		t.Fatalf("%v", d)
	}
}

// TestPlaintextWarningInEveryAnswer: with allow_plaintext_passwords every answer, a refusal
// too, carries the warning; the structured result stays intact.
func TestPlaintextWarningInEveryAnswer(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.Passwords = PasswordConfig{AllowPlaintext: true, EnvFile: filepath.Join(t.TempDir(), EnvFileName)}
	h.start()
	text, out, isErr := h.call("list_devices", nil)
	if isErr || !strings.HasPrefix(text, PlaintextWarning) || !strings.Contains(text, `"name":"NUC"`) || len(out["devices"].([]any)) != 1 {
		t.Fatalf("%v %q %v", isErr, text, out)
	}
	if text := h.mustFail("device_status", dev("Ghost"), "No favorite named"); !strings.HasPrefix(text, PlaintextWarning) {
		t.Fatalf("refusal %q", text)
	}
}

// TestCommandLineQuotesOrOmitsName: the subcommand the agent shows a person is safe to copy.
func TestCommandLineQuotesOrOmitsName(t *testing.T) {
	for name, want := range map[string]string{
		"NUC":           "`openlamt-mcp trust NUC`",
		"Lab Server":    "`openlamt-mcp trust \"Lab Server\"`",
		"x\"; rm -rf ~": "`openlamt-mcp trust <favorite name>`",
		"a$(id)":        "`openlamt-mcp trust <favorite name>`",
		"-rf":           "`openlamt-mcp trust <favorite name>`",
	} {
		if got := commandLine("trust", name); got != want {
			t.Errorf("%q: %s", name, got)
		}
	}
}

// TestUpdateHostNeverCarriesThePassword: after update_favorite points a favorite at another
// host, no source hands out the old host's password: the keychain entry is gone,
// passwords.command is asked for the new host, and the environment variable is the new host's.
func TestUpdateHostNeverCarriesThePassword(t *testing.T) {
	accept := func(*mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	t.Setenv(PasswordEnvVar("192.0.2.10"), "env-old-host")

	// Keychain and command: the command gets the new host and returns its own password.
	h := newFavHarness(t)
	h.saved("NUC", "192.0.2.10", false)
	h.cfg.Passwords = PasswordConfig{Command: helperCommand(t, "print", "{host}"), AllowPlaintext: true, EnvFile: filepath.Join(t.TempDir(), EnvFileName)}
	var asked []string
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return accept(r)
	}
	h.start()
	h.mustOK("update_favorite", dev("NUC", "host", "192.0.2.11"))
	if len(asked) != 1 || !strings.Contains(asked[0], "stay with the old host") || !strings.Contains(asked[0], "openlamt-mcp password NUC") {
		t.Fatalf("asked %q", asked)
	}
	h.mustFail("device_status", dev("NUC"), "no route to host") // no fake device at the new host: the attempt is recorded
	c := h.connectCalls()
	if len(c) != 1 || c[0].Host != "192.0.2.11" || c[0].Password != "pw-for-192.0.2.11" {
		t.Fatalf("connect %+v", c)
	}

	// Keychain and environment only: the old host's variable is not the new host's.
	h2 := newFavHarness(t)
	h2.saved("NUC", "192.0.2.10", false)
	h2.cfg.Passwords = PasswordConfig{AllowPlaintext: true, EnvFile: filepath.Join(t.TempDir(), EnvFileName)}
	h2.elicit = accept
	h2.start()
	h2.mustOK("update_favorite", dev("NUC", "host", "192.0.2.11"))
	h2.mustFail("device_status", dev("NUC"), `No password found for "NUC"`)
	if n := len(h2.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
	d := h2.mustOK("list_devices", nil)["devices"].([]any)[0].(map[string]any)
	if d["password_found"] != false || d["password_variable"] != PasswordEnvVar("192.0.2.11") {
		t.Fatalf("%v", d)
	}
}
