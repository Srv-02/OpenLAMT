package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

var errNoKeychain = errors.New("the name org.freedesktop.secrets was not provided")

// newPinHarness is a harness as on a computer without a keychain: certificates.pin_file is on, the
// server and a real favorites manager use a pin file over the memory keychain, and passwords come
// from passwords.command ("pw-for-<host>").
func newPinHarness(t *testing.T) *harness {
	h := newHarness(t)
	dir := t.TempDir()
	h.pinPath = filepath.Join(dir, PinFileName)
	pins, err := OpenPinFile(h.pinPath, h.keys)
	if err != nil {
		t.Fatal(err)
	}
	h.store = pins
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(dir, "favorites.json")), pins)
	h.cfg.Certificates.PinFile = true
	h.cfg.Passwords.Command = helperCommand(t, "print", "{host}")
	return h
}

// tlsFavorite saves a TLS favorite with its fake device; pinned trusts testFP in the pin file.
func (h *harness) tlsFavorite(name, host string, pinned bool) {
	h.t.Helper()
	if err := h.mgr.Add(favorites.Favorite{Name: name, Host: host, Username: "admin", UseTLS: true}); err != nil {
		h.t.Fatal(err)
	}
	if pinned {
		if err := h.store.SetFingerprint(host, "admin", testFP); err != nil {
			h.t.Fatal(err)
		}
	}
	h.devices[host] = &session.Fake{FP: testFP, St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}}
}

// TestPinFileServesTLSWithoutKeychain: the case of #191. The keychain cannot be read, the pin
// comes from the file and the password from passwords.command.
func TestPinFileServesTLSWithoutKeychain(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Lab Server", "lab.example.com", true)
	h.keys.Err = errNoKeychain
	h.start()
	h.mustOK("device_status", dev("Lab Server"))
	if c := h.connectCalls(); len(c) != 1 || c[0].Fingerprint != testFP || c[0].Password != "pw-for-lab.example.com" {
		t.Fatalf("connect %+v", c)
	}
}

// TestPinFileIsTheOnlyPinSource: with certificates.pin_file a certificate trusted in the keychain
// (by the app) does not count; the refusal names "trust" and does not send the person to the app.
func TestPinFileIsTheOnlyPinSource(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Lab Server", "lab.example.com", false)
	if err := h.keys.SetFingerprint("lab.example.com", "admin", testFP); err != nil {
		t.Fatal(err)
	}
	h.start()
	text := h.mustFail("device_status", dev("Lab Server"), "runs `openlamt-mcp trust \"Lab Server\"`")
	if strings.Contains(text, "connects once with the OpenLAMT app") || !strings.Contains(text, "certificates.pin_file") {
		t.Fatalf("%s", text)
	}
	d := h.mustOK("list_devices", nil)["devices"].([]any)[0].(map[string]any)
	if d["certificate_pinned"] != false {
		t.Fatalf("%v", d)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}

// TestPinFileListDevices: with certificates.pin_file the pin state comes from the file whatever
// the keychain does. A keychain that cannot be read is named only when no password was found, and
// then the answer points to passwords.command, not to the option that is on already.
func TestPinFileListDevices(t *testing.T) {
	for _, withCommand := range []bool{true, false} {
		h := newPinHarness(t)
		h.tlsFavorite("Pinned", "lab.example.com", true)
		h.tlsFavorite("New", "new.example.com", false)
		h.keys.Err = errNoKeychain
		if !withCommand {
			h.cfg.Passwords.Command = nil
		}
		h.start()
		devs := h.mustOK("list_devices", nil)["devices"].([]any)
		pinned, fresh := devs[0].(map[string]any), devs[1].(map[string]any)
		pw, _ := pinned["warnings"].([]any)
		fw, _ := fresh["warnings"].([]any)
		if pinned["certificate_pinned"] != true || fresh["certificate_pinned"] != false || pinned["password_found"] != withCommand {
			t.Fatalf("command=%v: %v %v", withCommand, pinned, fresh)
		}
		if withCommand {
			if len(pw) != 0 || len(fw) != 1 || !strings.Contains(fw[0].(string), "`openlamt-mcp trust New`") {
				t.Fatalf("with a password command: %v %v", pw, fw)
			}
			continue
		}
		if len(pw) != 1 || !strings.Contains(pw[0].(string), "system keychain") || !strings.Contains(pw[0].(string), "passwords.command") ||
			strings.Contains(pw[0].(string), "Secret Service") {
			t.Fatalf("pinned, no password: %v", pw)
		}
		if len(fw) != 2 || !strings.Contains(fw[0].(string), "system keychain") || !strings.Contains(fw[1].(string), "`openlamt-mcp trust New`") {
			t.Fatalf("new, no password: %v", fw)
		}
	}
}

// TestPinFileListDevicesCommandFindsNoPassword: the command finds no password and the keychain
// cannot be read. The headless text speaks of saving and removing, so list_devices shows the
// password hint instead; the command's own reason stays.
func TestPinFileListDevicesCommandFindsNoPassword(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Pinned", "lab.example.com", true)
	h.cfg.Passwords.Command = helperCommand(t, "fail", "{host}")
	h.keys.Err = errNoKeychain
	h.start()
	d := h.mustOK("list_devices", nil)["devices"].([]any)[0].(map[string]any)
	w, _ := json.Marshal(d["warnings"])
	if d["password_found"] != false || strings.Contains(string(w), "nothing else is needed") ||
		!strings.Contains(string(w), "No password found: the person stores it in the password manager that passwords.command reads.") {
		t.Fatalf("%v", d)
	}
}

// TestPinFileDamagedAtRuntimeIsNamed: a pin file that breaks while the server runs refuses the
// TLS device with the file's name, not with a keychain text, and nothing connects.
func TestPinFileDamagedAtRuntimeIsNamed(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Lab Server", "lab.example.com", true)
	h.start()
	if err := os.WriteFile(h.pinPath, []byte(`{"admin@lab.example.com": "s3cret-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	text := h.mustFail("device_status", dev("Lab Server"), PinFileName)
	if !strings.Contains(text, "certificates.pin_file") || strings.Contains(text, "system keychain") || strings.Contains(text, "s3cret") {
		t.Fatalf("%s", text)
	}
	d := h.mustOK("list_devices", nil)["devices"].([]any)[0].(map[string]any)
	w, _ := json.Marshal(d["warnings"])
	if d["certificate_pinned"] != false || !strings.Contains(string(w), PinFileName) || strings.Contains(string(w), "not trusted yet") {
		t.Fatalf("%v", d)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("connected %d times", n)
	}
}

// TestPinFileAddFavoritePinsIntoFile: the certificate the person trusts in the question of
// add_favorite goes into the pin file, not into the keychain; without a question the next step
// names "trust" only.
func TestPinFileAddFavoritePinsIntoFile(t *testing.T) {
	h := newPinHarness(t)
	h.fetchFP = func(context.Context, string) (string, error) { return testFP, nil }
	asked := ""
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = r.Params.Message
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{trustField: true}}, nil
	}
	h.start()
	out := h.mustOK("add_favorite", map[string]any{"name": "Lab", "host": "lab.example.com", "tls": true})
	if !strings.Contains(asked, "`openlamt-mcp trust Lab`") || strings.Contains(asked, "OpenLAMT app") {
		t.Fatalf("question %q", asked)
	}
	if pin, _ := h.store.GetFingerprint("lab.example.com", "admin"); pin != testFP || out["certificate_pinned"] != true {
		t.Fatalf("pin %q, out %v", pin, out)
	}
	if _, err := h.keys.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("the pin reached the keychain: %v", err)
	}

	h2 := newPinHarness(t) // a client that cannot ask
	h2.start()
	out = h2.mustOK("add_favorite", map[string]any{"name": "Lab", "host": "lab.example.com", "tls": true})
	steps, _ := json.Marshal(out["next_steps"])
	if !strings.Contains(string(steps), "`openlamt-mcp trust Lab`") || strings.Contains(string(steps), "OpenLAMT app") {
		t.Fatalf("next steps %s", steps)
	}
}

// TestPinFileFavoriteEditsWithoutKeychain: with the keychain unreadable, a new host and a delete
// still save the favorite and remove its pin from the file; the answer says that the keychain
// step (the old password) failed.
func TestPinFileFavoriteEditsWithoutKeychain(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Lab", "lab.example.com", true)
	h.tlsFavorite("Other", "other.example.com", true)
	h.keys.Err = errNoKeychain
	h.elicit = (&askAll{}).handler
	h.start()
	out := h.mustOK("update_favorite", dev("Lab", "host", "lab2.example.com"))
	w, _ := json.Marshal(out["warnings"])
	if out["host"] != "lab2.example.com" || out["certificate_pinned"] != false ||
		!strings.Contains(string(w), "no password was saved or removed there") || strings.Contains(string(w), "set passwords.command") {
		t.Fatalf("%v", out)
	}
	if _, err := h.store.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("the pin of the old host stayed: %v", err)
	}
	if out := h.mustOK("delete_favorite", dev("Other")); out["deleted"] != "Other" {
		t.Fatalf("%v", out)
	}
	if _, err := h.store.GetFingerprint("other.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("the pin of the deleted favorite stayed: %v", err)
	}
}

// TestPinFileFollowsCertificateChange: set_tls_certificate hands the new fingerprint to the
// favorites manager; over a pin file it replaces the pin in the file and needs no keychain.
func TestPinFileFollowsCertificateChange(t *testing.T) {
	h := newPinHarness(t)
	h.tlsFavorite("Lab", "lab.example.com", true)
	h.keys.Err = errNoKeychain
	if err := h.mgr.ChangeFingerprint("lab.example.com", "admin", testFP, otherFP); err != nil {
		t.Fatal(err)
	}
	if fp, err := h.store.GetFingerprint("lab.example.com", "admin"); err != nil || fp != otherFP {
		t.Fatalf("pin %q %v", fp, err)
	}
}
