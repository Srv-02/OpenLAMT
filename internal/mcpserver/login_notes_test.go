package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

// loginKeys is a memory keychain whose password reads and writes can be switched off.
type loginKeys struct {
	*keychain.Memory
	failGet, failSet bool
}

var errLocked = errors.New("keychain locked")

func (k *loginKeys) Get(h, u string) (string, error) {
	if k.failGet {
		return "", errLocked
	}
	return k.Memory.Get(h, u)
}

func (k *loginKeys) Set(h, u, p string) error {
	if k.failSet {
		return errLocked
	}
	return k.Memory.Set(h, u, p)
}

const (
	nasHost = "nas.example.com"
	nasVar  = "OPENLAMT_PASSWORD_NAS_2EEXAMPLE_2ECOM"
)

// newLoginHarness is a harness with a real favorites manager over a memory keychain and the
// favorite "NAS" on nasHost, signed in as user (an account of deviceUsers or "admin"). The
// password is saved in the keychain only if savePassword.
func newLoginHarness(t *testing.T, user string, tls, savePassword bool) (*harness, *loginKeys) {
	t.Helper()
	h := newHarness(t)
	keys := &loginKeys{Memory: h.keys}
	h.store = keys
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	h.loginStore = h.mgr
	h.addDeviceAs("NAS", nasHost, user, tls)
	h.favs = nil // the manager is the source
	if err := h.mgr.Add(favorites.Favorite{Name: "NAS", Host: nasHost, Username: user, UseTLS: tls}); err != nil {
		t.Fatal(err)
	}
	if !savePassword {
		if err := h.keys.Delete(nasHost, user); err != nil {
			t.Fatal(err)
		}
	}
	h.devices[nasHost].Cfg.Users = deviceUsers()
	h.elicit = (&askAll{}).handler
	return h, keys
}

// notes runs update_user on the favorite's own account and returns its warnings joined and its
// result. It fails if a warning or the result holds the password.
func notes(t *testing.T, h *harness, args ...any) (warnings, result string) {
	t.Helper()
	out := mustOKNoPassword(t, h, "update_user", dev("NAS", append([]any{"user", "operator", "password", newPassword}, args...)...))
	w, _ := json.Marshal(out["warnings"])
	return string(w), out["result"].(string)
}

// setCommand sets passwords.command to argv and answers its runs with answer.
func setCommand(h *harness, argv []string, answer func(argv []string) ([]byte, error)) {
	h.cfg.Passwords.Command = argv
	h.startThen(func() { h.srv.s.pws.run = func(_ context.Context, a []string) ([]byte, error) { return answer(a) } })
}

// startThen starts the server and then runs f against it.
func (h *harness) startThen(f func()) {
	h.start()
	f()
}

func wantAll(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
}

func wantNone(t *testing.T, got string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(got, w) {
			t.Errorf("unexpected %q in %s", w, got)
		}
	}
}

// TestLoginNotesPasswordCommand (A): the password comes from the password manager, which still
// has the old one. The renamed favorite is not touched by the note.
func TestLoginNotesPasswordCommand(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, false)
	setCommand(h, []string{"pm", "{host}"}, func([]string) ([]byte, error) { return []byte("old-pw\n"), nil })
	w, res := notes(t, h, "new_name", "ops")
	wantAll(t, w, `NAS now accepts the user \"ops\" with the new password`, "from your password manager, and that still has the old one",
		"the entry for nas.example.com that `passwords.command` (the password setting in the openlamt-mcp config) reads. Until you do, OpenLAMT cannot connect to NAS.")
	wantNone(t, w, "openlamt-mcp password")
	wantNone(t, res, "from now on")
	if list, _ := h.mgr.List(); list[0].Username != "ops" {
		t.Errorf("favorite %+v", list)
	}
}

// TestLoginNotesEnvironment (B): the password comes from the environment variable; only it is named.
func TestLoginNotesEnvironment(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, false)
	h.cfg.Passwords.AllowPlaintext = true
	h.startThen(func() {
		h.srv.s.pws.getenv = func(k string) (string, bool) { return "old-pw", k == nasVar }
	})
	w, _ := notes(t, h, "new_name", "ops")
	wantAll(t, w, "from the environment variable `"+nasVar+"`, and that still holds the old one. Put the new password in that variable where you set it "+
		"(for example in the MCP settings of your AI app), then restart your AI app.")
	wantNone(t, w, EnvFileName, "the file")
}

// TestLoginNotesEnvFile (B): the password comes from the .env file; only the file is named.
func TestLoginNotesEnvFile(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, false)
	h.cfg.Passwords.AllowPlaintext = true
	if err := os.WriteFile(h.cfg.Passwords.EnvFile, []byte(nasVar+"=old-pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.startThen(func() { h.srv.s.pws.getenv = func(string) (string, bool) { return "", false } })
	w, _ := notes(t, h, "new_name", "ops")
	wantAll(t, w, "from the file `"+h.cfg.Passwords.EnvFile+"`, and that still holds the old one. Open that file and replace the old password after `"+nasVar+"=` with the new one.")
	wantNone(t, w, "environment variable")
}

// TestLoginNotesKeychainLocked (C): the keychain exists but cannot store the password.
func TestLoginNotesKeychainLocked(t *testing.T) {
	h, keys := newLoginHarness(t, "operator", true, true)
	h.start()
	keys.failSet = true
	w, res := notes(t, h)
	wantAll(t, w, "could not save that password on this computer (the system keychain is locked). Unlock the keychain, then save the new password for NAS in the OpenLAMT app or with `openlamt-mcp password \\\"NAS\\\"` in a terminal. "+
		"Until you do, OpenLAMT tries the old password and cannot connect to NAS.")
	wantNone(t, res, "from now on")
}

// TestLoginNotesNoKeychain (C): certificates.pin_file over a keychain that cannot be read, as on
// a headless Linux. The rename lands in the favorites and the pin file; the note names the
// password manager and the .env file, not openlamt-mcp password.
func TestLoginNotesNoKeychain(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	inner := &loginKeys{Memory: h.keys}
	h.pinPath = filepath.Join(dir, PinFileName)
	pins, err := OpenPinFile(h.pinPath, inner)
	if err != nil {
		t.Fatal(err)
	}
	h.store = pins
	h.mgr = favorites.NewManager(favorites.NewStore(filepath.Join(dir, "favorites.json")), pins)
	h.loginStore = h.mgr
	h.cfg.Certificates.PinFile = true
	h.addDeviceAs("NAS", nasHost, "operator", true)
	h.favs = nil
	if err := h.mgr.Add(favorites.Favorite{Name: "NAS", Host: nasHost, Username: "operator", UseTLS: true}); err != nil {
		t.Fatal(err)
	}
	if err := pins.SetFingerprint(nasHost, "operator", testFP); err != nil {
		t.Fatal(err)
	}
	inner.failGet, inner.failSet = true, true
	h.devices[nasHost].Cfg.Users = deviceUsers()
	h.elicit = (&askAll{}).handler
	h.cfg.Passwords.Command = []string{"pm", "{host}"}
	h.start()
	// The password manager serves the connection once; afterwards no source is configured.
	h.srv.s.pws.run = func(context.Context, []string) ([]byte, error) {
		h.srv.s.pws.cfg.Command = nil
		return []byte("old-pw"), nil
	}
	w, res := notes(t, h, "new_name", "ops")
	wantAll(t, w, "because this computer has no system keychain (the safe password storage of the operating system). "+
		"Store the new password in your password manager and set `passwords.command` in the openlamt-mcp config so OpenLAMT can read it there. "+
		"Or, less safe and meant for test labs, add the line `"+nasVar+"=\\u003cnew password\\u003e` to the file `"+h.cfg.Passwords.EnvFile+
		"` and set `allow_plaintext_passwords` to true in the config; after either change, restart your AI app. Until you do, OpenLAMT cannot connect to NAS.")
	wantNone(t, w, "openlamt-mcp password")
	wantNone(t, res, "from now on")
	if list, _ := h.mgr.List(); list[0].Username != "ops" {
		t.Errorf("favorite %+v", list)
	}
	if fp, err := pins.GetFingerprint(nasHost, "ops"); err != nil || fp != testFP {
		t.Errorf("pin of ops: %q %v", fp, err)
	}
	if _, err := pins.GetFingerprint(nasHost, "operator"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("pin of operator left behind: %v", err)
	}
}

// TestLoginNotesUserPlaceholder (D): only the name changed and passwords.command looks the
// password up by {user}.
func TestLoginNotesUserPlaceholder(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, false)
	setCommand(h, []string{"pm", "{host}", "{user}"}, func(a []string) ([]byte, error) {
		if a[len(a)-1] == "operator" {
			return []byte(newPassword), nil
		}
		return nil, errors.New("exited with status 1")
	})
	w, _ := notes(t, h, "new_name", "ops")
	wantAll(t, w, `NAS now uses the user name \"ops\". Your password manager looks up the NAS password under the user name, so copy the entry for \"operator\" to \"ops\". Until you do, OpenLAMT cannot connect to NAS.`)
}

// TestLoginNotesRenameOnlyHostCommand: only the name changed and the command does not use
// {user}, so the password manager already serves the new login: no note.
func TestLoginNotesRenameOnlyHostCommand(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, false)
	setCommand(h, []string{"pm", "{host}"}, func([]string) ([]byte, error) { return []byte(newPassword), nil })
	w, res := notes(t, h, "new_name", "ops")
	if w != "null" {
		t.Errorf("warnings %s", w)
	}
	wantAll(t, res, "from now on")
}

// TestLoginNotesFavoriteNotRenamed (E): another favorite already uses the new name.
func TestLoginNotesFavoriteNotRenamed(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, true)
	if err := h.mgr.Add(favorites.Favorite{Name: "Other", Host: nasHost, Username: "ops"}); err != nil {
		t.Fatal(err)
	}
	h.start()
	w, res := notes(t, h, "new_name", "ops")
	wantAll(t, w, `NAS now accepts the user \"ops\", but OpenLAMT still signs in to NAS as \"operator\", because another favorite (saved device entry) already uses \"ops\" on nas.example.com. `+
		"Delete that other favorite, then change the user of NAS to \\\"ops\\\", in the OpenLAMT app or through your AI agent. "+
		"After that, save the password for NAS again with `openlamt-mcp password \\\"NAS\\\"` and, if NAS uses TLS, trust its certificate with `openlamt-mcp trust \\\"NAS\\\"`; until then, OpenLAMT cannot connect to NAS.")
	wantNone(t, res, "from now on")
	if list, _ := h.mgr.List(); list[0].Username != "operator" {
		t.Errorf("favorite %+v", list)
	}
}

// TestLoginNotesFavoriteFileNotWritten (E): the favorite could not be changed for another reason.
func TestLoginNotesFavoriteFileNotWritten(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", false, true)
	h.loginStore = &fakeLogins{keys: h.keys, err: errors.New("disk full")}
	h.start()
	w, _ := notes(t, h, "new_name", "ops")
	wantAll(t, w, `NAS now accepts the user \"ops\", but OpenLAMT could not save that in its favorite NAS (your saved entry for this device), so it still signs in as \"operator\". `+
		"Change the user of NAS to \\\"ops\\\" in the OpenLAMT app or let your AI agent do it, then save the password for NAS again in the app or with `openlamt-mcp password \\\"NAS\\\"` in a terminal. Until then, OpenLAMT cannot connect to NAS.")
	wantNone(t, w, "uses TLS", "disk full")
}

// TestLoginNotesOtherAccountWithoutFavorite: an account no favorite signs in with gets no note,
// even if the keychain failed.
func TestLoginNotesOtherAccountWithoutFavorite(t *testing.T) {
	h, _ := newLoginHarness(t, "operator", true, true)
	h.loginStore = &fakeLogins{keys: h.keys, err: &favorites.KeychainError{Err: errLocked}}
	h.start()
	out := mustOKNoPassword(t, h, "update_user", dev("NAS", "user", "bob", "new_name", "robert", "password", newPassword))
	w, _ := json.Marshal(out["warnings"])
	wantNone(t, string(w), "cannot connect", "saving", "keychain")
}

// TestLoginNotesKeychainSaved: everything saved, no note, and the result says so.
func TestLoginNotesKeychainSaved(t *testing.T) {
	h, keys := newLoginHarness(t, "operator", true, true)
	h.start()
	w, res := notes(t, h, "new_name", "ops")
	if w != "null" {
		t.Errorf("warnings %s", w)
	}
	wantAll(t, res, "from now on")
	if pw, _ := keys.Memory.Get(nasHost, "ops"); pw != newPassword {
		t.Error("password not saved")
	}
}

// TestLoginNotesAdminAccount: set_admin_account of the favorite's own admin login, with the
// password from the password manager (A).
func TestLoginNotesAdminAccount(t *testing.T) {
	h, _ := newLoginHarness(t, "admin", true, false)
	setCommand(h, []string{"pm", "{host}"}, func([]string) ([]byte, error) { return []byte("old-pw"), nil })
	out := mustOKNoPassword(t, h, "set_admin_account", dev("NAS", "new_name", "boss", "password", newPassword, "confirm", "NAS"))
	w, _ := json.Marshal(out["warnings"])
	wantAll(t, string(w), `NAS now accepts the user \"boss\" with the new password, but OpenLAMT gets the NAS password from your password manager`)
	wantNone(t, out["result"].(string), "from now on")
}

var _ session.LoginStore = (*favorites.Manager)(nil)
