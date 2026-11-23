package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
)

// Issue #151: Edit clicked before the keychain lookup answered shows "Save password" off. The
// late answer "saved" must reach the form as well, so Save after a name change keeps the
// stored password instead of deleting it.
func TestEditLateKeychainAnswerKeepsSavedPassword(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Name: "Lab", Host: "device.example.com", Username: "admin"})
	if err := h.keys.Set("device.example.com", "admin", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	h.reload(t)
	var pending []func()
	s.lookupPassword = func(host, user string, answer func(bool)) {
		pw, err := h.keys.Get(host, user)
		saved := err == nil && pw != ""
		pending = append(pending, func() { answer(saved) }) // a slow keychain (unlock prompt)
	}
	k := deviceKey("device.example.com", "admin")
	s.forgetSaved("device.example.com", "admin")
	editFavorite(t, h, "device.example.com", "admin") // the click starts the lookup, Edit comes first
	if len(pending) != 1 || s.savePassword.Checked() {
		t.Fatalf("precondition: lookup pending (%d), switch off (%v)", len(pending), s.savePassword.Checked())
	}
	pending[0]() // the answer "saved" arrives while edit mode is open
	if !s.saved[k] || !s.savePassword.Checked() || s.password.PlaceHolder != "Saved in keychain" {
		t.Fatalf("edit mode did not follow the late answer: switch=%v placeholder=%q", s.savePassword.Checked(), s.password.PlaceHolder)
	}
	s.name.SetText("Lab 2")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if pw, err := h.keys.Get("device.example.com", "admin"); err != nil || pw != "stored-secret" {
		t.Fatalf("saved password lost: %q, %v", pw, err)
	}
	if s.items[0].Name != "Lab 2" {
		t.Fatalf("name not saved: %+v", s.items[0])
	}
}

// Issue #151: a switch the user touched before the late answer keeps the user's choice, and a
// switch the user turned off after it showed "saved" still deletes the password.
func TestEditLateKeychainAnswerKeepsTouchedSwitch(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	if err := h.keys.Set("device.example.com", "admin", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	h.reload(t)
	var pending []func()
	s.lookupPassword = func(host, user string, answer func(bool)) {
		pw, err := h.keys.Get(host, user)
		saved := err == nil && pw != ""
		pending = append(pending, func() { answer(saved) })
	}
	s.forgetSaved("device.example.com", "admin")
	editFavorite(t, h, "device.example.com", "admin")
	s.savePassword.SetChecked(true) // the user turns it on and types a new password
	s.password.SetText("new-secret")
	pending[0]()
	if !s.savePassword.Checked() || s.editSaved {
		t.Fatalf("touched switch overwritten: switch=%v editSaved=%v", s.savePassword.Checked(), s.editSaved)
	}
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "new-secret" {
		t.Fatalf("typed password not saved: %q", pw)
	}

	// Explicitly off after the form showed "saved": the password goes.
	for _, f := range pending[1:] {
		f()
	}
	pending = nil
	editFavorite(t, h, "device.example.com", "admin")
	if !s.savePassword.Checked() {
		t.Fatal("precondition: the form shows the saved password")
	}
	s.savePassword.SetChecked(false)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if _, err := h.keys.Get("device.example.com", "admin"); err == nil {
		t.Fatal("password switched off by the user was not deleted")
	}
}

// Issue #151: a connect of another device that succeeds while edit mode is open must not empty
// the password the user is typing there; Save stores it.
func TestConnectOfOtherDeviceKeepsPasswordTypedInEdit(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Name: "B", Host: "b.example.com", Username: "admin"})
	if err := h.keys.Set("b.example.com", "admin", "old-secret"); err != nil {
		t.Fatal(err)
	}
	h.reload(t)
	editFavorite(t, h, "b.example.com", "admin")
	s.password.SetText("new-secret")
	s.begin(connectRequest{cfg: amt.Config{Host: "a.example.com", Username: "admin", Password: "x", UseTLS: true}})
	h.waitCfg(t)
	h.waitEnded(t)
	if s.password.Text != "new-secret" {
		t.Fatalf("typed password emptied by the other connect: %q", s.password.Text)
	}
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if pw, _ := h.keys.Get("b.example.com", "admin"); pw != "new-secret" {
		t.Fatalf("typed password not saved: %q", pw)
	}

	// A Quick Connect form for another device keeps its password too.
	s.newConnection()
	s.host.SetText("c.example.com")
	s.password.SetText("typed")
	s.begin(connectRequest{cfg: amt.Config{Host: "a.example.com", Username: "admin", Password: "x", UseTLS: true}})
	h.waitCfg(t)
	h.waitEnded(t)
	if s.password.Text != "typed" {
		t.Fatalf("Quick Connect form for another device emptied: %q", s.password.Text)
	}
}
