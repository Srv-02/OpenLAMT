package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

// Issue #153: renaming the signed-in account in device management renames the favorite; the
// start window follows: the open window, the probe skip and the selection move to the new key,
// so a double click focuses the window and Edit saves the renamed favorite.
func TestRenameOfOwnAccountMovesStartWindowToNewKey(t *testing.T) {
	h := newStart(t)
	s := h.s
	const host = "device.example.com"
	s.mgr.Upsert(favorites.Favorite{Name: "Lab", Host: host, Username: "admin", UseTLS: true})
	if err := h.keys.Set(host, "admin", "old-secret"); err != nil {
		t.Fatal(err)
	}
	h.reload(t)
	dev := &session.Fake{FP: fp}
	dev.Cfg.Users.Admin = "admin"
	d := openFakeDevice(t, h, host, dev)
	test.Tap(s.rows[deviceKey(host, "admin")])

	conf, err := amt.Confirm(amt.OpAdminAccount, host, amt.ConfirmPhrase(amt.OpAdminAccount, host))
	if err != nil {
		t.Fatal(err)
	}
	// Device management runs the write on a goroutine; the test driver runs fyne.Do inline.
	if err := d.sess.SetAdminAccount(amt.AccountChange{Name: "admin2", Password: newPassword}, conf, d.loginStore); err != nil {
		t.Fatal(err)
	}
	h.waitReload(t)
	oldK, newK := deviceKey(host, "admin"), deviceKey(host, "admin2")
	if len(s.items) != 1 || s.items[0].Username != "admin2" {
		t.Fatalf("favorite not renamed: %+v", s.items)
	}
	if _, still := s.open[oldK]; still || s.open[newK] != d {
		t.Fatalf("open window not moved: %v", s.open)
	}
	if s.prober.skipped(oldK) || !s.prober.skipped(newK) {
		t.Fatal("probe skip not moved")
	}
	if s.selKey != newK || !s.rows[newK].Selected() || s.values["user"].Text != "admin2" {
		t.Fatalf("selection not moved: %q", s.selKey)
	}
	if !s.saved[newK] || s.values["password"].Text != "Saved in keychain" {
		t.Fatalf("keychain state of the new login: %v %q", s.saved, s.values["password"].Text)
	}

	// A double click focuses the open window instead of a second session.
	s.connectFavorite(s.items[0])
	if len(s.busy) != 0 || len(h.dev.Configs()) != 0 {
		t.Fatal("second session to the same device started")
	}

	// Edit keeps host and user locked and saves the renamed favorite.
	editFavorite(t, h, host, "admin2")
	if !s.user.Disabled() || s.user.Text != "admin2" {
		t.Fatalf("user field: disabled %v, %q", s.user.Disabled(), s.user.Text)
	}
	s.name.SetText("Lab 2")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	select {
	case err := <-h.errs:
		t.Fatalf("save failed: %v", err)
	default:
	}
	if f := s.items[0]; f.Name != "Lab 2" || f.Username != "admin2" {
		t.Fatalf("%+v", f)
	}

	// Closing the window frees the new key.
	d.poller.Stop()
	d.win.Close()
	if _, still := s.open[newK]; still || s.prober.skipped(newK) {
		t.Fatal("closed window still registered under the new key")
	}
}
