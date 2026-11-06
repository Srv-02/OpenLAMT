package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// A keychain lookup that started before the password was saved and answers afterwards must
// not overwrite the fresh state (it would stay wrong until the next restart).
func TestStaleKeychainLookupDoesNotOverwriteFreshState(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	h.reload(t)
	var late func()
	calls := 0
	s.lookupPassword = func(host, user string, answer func(bool)) {
		calls++
		pw, err := h.keys.Get(host, user)
		saved := err == nil && pw != ""
		if calls == 1 { // the first lookup reads before the save and answers after everything
			late = func() { answer(saved) }
			return
		}
		answer(saved)
	}
	test.Tap(s.rows[deviceKey("device.example.com", "admin")]) // lookup 1 starts, stays pending
	if late == nil {
		t.Fatal("precondition: first lookup should be pending")
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.savePassword.SetChecked(true)
	s.password.SetText("new-secret")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if !s.saved[deviceKey("device.example.com", "admin")] {
		t.Fatal("precondition: fresh state should be saved")
	}
	late() // the stale answer "not saved" arrives now
	if !s.saved[deviceKey("device.example.com", "admin")] || s.values["password"].Text != "Saved in keychain" {
		t.Fatalf("stale answer overwrote the cache: %v, %q", s.saved, s.values["password"].Text)
	}
}

// Connecting with "Save password" through the real start window path updates the detail view.
func TestConnectWithSavePasswordUpdatesDetail(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	h.reload(t)
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if s.values["password"].Text != "Not saved" {
		t.Fatal("precondition")
	}
	s.conn.askPassword = func(_, _ string, offerSave bool, answer func(string, bool, bool)) {
		if !offerSave {
			t.Error("save not offered")
		}
		answer("secret-1", true, true)
	}
	test.Tap(s.connectBtn)
	h.waitCfg(t)
	h.waitEnded(t)
	if s.values["password"].Text != "Saved in keychain" || !s.saved[deviceKey("device.example.com", "admin")] {
		t.Fatalf("detail not updated: %q", s.values["password"].Text)
	}
}

// cancelAfterSet cancels the running attempts once the password is in the keychain, like a
// click on Cancel between persist and the end of the attempt.
type cancelAfterSet struct {
	keychain.Store
	cancel func()
}

func (c cancelAfterSet) Set(host, user, pw string) error {
	err := c.Store.Set(host, user, pw)
	c.cancel()
	return err
}

// An attempt cancelled after persist skips onOpened, but the password is saved: the cache
// must not stay on "Not saved".
func TestCancelAfterPersistRefreshesSavedState(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	h.reload(t)
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	s.conn.keys = cancelAfterSet{Store: h.keys, cancel: s.conn.cancelAll}
	looked := make(chan struct{}, 4)
	s.lookupPassword = func(host, user string, answer func(bool)) {
		pw, err := h.keys.Get(host, user)
		answer(err == nil && pw != "")
		looked <- struct{}{}
	}
	s.conn.askPassword = func(_, _ string, _ bool, answer func(string, bool, bool)) { answer("secret-1", true, true) }
	test.Tap(s.connectBtn)
	select { // the refresh after the cancelled attempt (the channel orders the reads below)
	case <-looked:
	case <-time.After(2 * time.Second):
		t.Fatal("no keychain refresh after the cancelled attempt")
	}
	if s.values["password"].Text != "Saved in keychain" || !s.saved[deviceKey("device.example.com", "admin")] {
		t.Fatalf("cache stayed stale: %q", s.values["password"].Text)
	}
}
