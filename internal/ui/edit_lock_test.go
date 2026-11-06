package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

const lockHint = "Close the device window to change host and username."

// Issue #78: an open device window is registered under host and user, so edit mode locks
// both while it is open and releases them when it closes.
func TestEditLocksHostAndUserWhileDeviceWindowIsOpen(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})

	editFavorite(t, h, "device.example.com", "admin")
	if !s.host.Disabled() || !s.user.Disabled() {
		t.Fatal("host and user must be locked while the device window is open")
	}
	if s.name.Disabled() || s.password.Disabled() {
		t.Fatal("name and password stay editable")
	}
	if !hasText(s.card1, lockHint) {
		t.Fatal("hint missing")
	}

	// A save with the lock on leaves host and user alone.
	s.name.SetText("Keller")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if f := s.items[0]; f.Name != "Keller" || f.Host != "device.example.com" || f.Username != "admin" {
		t.Fatalf("%+v", f)
	}

	// Closing the window during edit mode frees the fields and drops the hint.
	editFavorite(t, h, "device.example.com", "admin")
	d.win.Close()
	if s.host.Disabled() || s.user.Disabled() {
		t.Fatal("host and user must be free after the window closed")
	}
	if hasText(s.card1, lockHint) {
		t.Fatal("hint must disappear")
	}

	// Editing without an open window is free from the start.
	test.Tap(s.editCancelBtn)
	editFavorite(t, h, "device.example.com", "admin")
	if s.host.Disabled() || s.user.Disabled() || hasText(s.card1, lockHint) {
		t.Fatal("no window open: no lock")
	}
	// Opening the window during edit mode locks the fields again.
	d2 := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})
	if !s.host.Disabled() || !s.user.Disabled() || !hasText(s.card1, lockHint) {
		t.Fatal("opening the window must lock")
	}
	d2.poller.Stop()
	d2.win.Close()
}
