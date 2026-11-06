package ui

import (
	"testing"

	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
)

func mouse(b desktop.MouseButton) *desktop.MouseEvent { return &desktop.MouseEvent{Button: b} }

// Fyne's GLFW driver holds back a single click until the double-click interval has passed
// (Tapped). The selection must therefore happen on MouseDown.
func TestSidebarSelectsOnMouseDownWithoutTapped(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	s.mgr.Upsert(favorites.Favorite{Host: "other.example.com", Username: "operator"})
	h.reload(t)
	a, b := s.rows[deviceKey("device.example.com", "admin")], s.rows[deviceKey("other.example.com", "operator")]

	b.MouseDown(mouse(desktop.MouseButtonSecondary))
	if s.selKey != "" {
		t.Fatal("right click selected")
	}
	a.MouseDown(mouse(desktop.MouseButtonPrimary))
	if s.selKey != deviceKey("device.example.com", "admin") || !a.Selected() || s.mode != modeView {
		t.Fatalf("MouseDown did not select: %q", s.selKey)
	}
	b.MouseDown(mouse(desktop.MouseButtonPrimary))
	if !b.Selected() || a.Selected() {
		t.Fatal("second MouseDown did not move the selection")
	}
}

func TestMouseDownsThenDoubleTapConnectsOnce(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.reload(t)
	r := s.rows[deviceKey("device.example.com", "admin")]
	r.MouseDown(mouse(desktop.MouseButtonPrimary))
	r.MouseUp(mouse(desktop.MouseButtonPrimary))
	r.MouseDown(mouse(desktop.MouseButtonPrimary))
	r.MouseUp(mouse(desktop.MouseButtonPrimary))
	test.DoubleTap(r)
	h.waitCfg(t)
	h.waitEnded(t)
	if n := len(h.dev.Configs()); n != 1 {
		t.Fatalf("%d connects", n)
	}
}

func TestPasswordStateCachedAcrossVisits(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	s.mgr.Upsert(favorites.Favorite{Host: "other.example.com", Username: "operator"})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.reload(t)
	calls := 0
	s.lookupPassword = func(host, user string, answer func(bool)) {
		calls++
		pw, err := h.keys.Get(host, user)
		answer(err == nil && pw != "")
	}
	a, b := s.rows[deviceKey("device.example.com", "admin")], s.rows[deviceKey("other.example.com", "operator")]
	test.Tap(a)
	test.Tap(b)
	first := calls
	test.Tap(a)
	if calls != first || s.values["password"].Text != "Saved in keychain" {
		t.Fatalf("calls %d -> %d, password %q", first, calls, s.values["password"].Text)
	}
	test.Tap(b)
	if calls != first || s.values["password"].Text != "Not saved" {
		t.Fatalf("calls %d -> %d, password %q", first, calls, s.values["password"].Text)
	}
}

func TestPasswordCacheFollowsSaveAndDelete(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	h.reload(t)
	a := s.rows[deviceKey("device.example.com", "admin")]
	test.Tap(a)
	if s.values["password"].Text != "Not saved" {
		t.Fatal("precondition")
	}
	// Save a password in edit mode: view and later visits show it without a stale cache.
	editFavorite(t, h, "device.example.com", "admin")
	s.savePassword.SetChecked(true)
	s.password.SetText("new-secret")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if s.values["password"].Text != "Saved in keychain" {
		t.Fatalf("after save: %q", s.values["password"].Text)
	}
	// Delete the favorite with its password, add it again without one.
	s.remove(s.items[0], true)
	h.waitReload(t)
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	h.reload(t)
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if s.values["password"].Text != "Not saved" || s.savePassword.Checked() {
		t.Fatalf("after delete: %q", s.values["password"].Text)
	}
}
