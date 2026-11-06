package ui

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/typing"
)

// keyboardDevice is a device window whose favorite (nil: quick connect) is loaded.
func keyboardDevice(t *testing.T, fav *favorites.Favorite) (*deviceHarness, *fakeLayouts, chan struct{}) {
	t.Helper()
	englishUI(t)
	h := newDevice(t, test.NewTempApp(t), &session.Fake{FP: fp}, true)
	store := &fakeLayouts{fav: fav}
	h.d.layouts = store
	loaded := make(chan struct{}, 1)
	h.d.onQualityLoaded = func() { loaded <- struct{}{} }
	h.d.loadQuality()
	recv(t, loaded)
	saved := make(chan struct{}, 8)
	h.d.onKeyboardSaved = func() { saved <- struct{}{} }
	return h, store, saved
}

func pickKeyboard(t *testing.T, h *deviceHarness, label string) {
	t.Helper()
	test.Tap(h.d.keyboard)
	if h.d.keyboardPopup == nil {
		t.Fatal("keyboard menu not shown")
	}
	for _, it := range h.d.keyboardMenu.Items {
		if it.Label == label {
			tapMenuItem(t, h.d.keyboardPopup, h.d.keyboardMenu, it)
			return
		}
	}
	t.Fatalf("no item %q", label)
}

func TestKeyboardMenuReplacesSettingsButton(t *testing.T) {
	h, _, _ := keyboardDevice(t, nil)
	if h.d.keyboard.Caption() != "Keyboard" {
		t.Fatalf("caption %q", h.d.keyboard.Caption())
	}
	test.Tap(h.d.keyboard)
	var labels []string
	for _, it := range h.d.keyboardMenu.Items {
		labels = append(labels, it.Label)
	}
	want := []string{"Default", "Danish", "English (UK)", "English (US)", "French", "French (Belgium)",
		"French (Switzerland)", "German", "German (Switzerland)", "Italian", "Japanese", "Norwegian",
		"Portuguese", "Portuguese (Brazil)", "Russian", "Spanish", "Swedish/Finnish"}
	if len(labels) != len(want) {
		t.Fatalf("items %q", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("items %q", labels)
		}
	}
	if h.d.win.Canvas().Overlays().Top() == nil {
		t.Fatal("no menu popup")
	}
}

func TestKeyboardMenuSavesLayoutOnFavorite(t *testing.T) {
	h, store, saved := keyboardDevice(t, &favorites.Favorite{Layout: "de", AutoConnect: favorites.AutoConnectOn})
	if !h.d.layoutItems["de"].Checked || h.d.layoutItems["us"].Checked || h.d.layoutItems[""].Checked {
		t.Fatal("stored layout not checked")
	}
	changed := 0
	h.d.onFavoriteChanged = func() { changed++ }
	pickKeyboard(t, h, "English (US)")
	recv(t, saved)
	if f := store.stored(); f.Layout != "us" || f.AutoConnect != favorites.AutoConnectOn || !h.d.layoutItems["us"].Checked || h.d.layoutItems["de"].Checked || changed != 1 {
		t.Fatalf("stored %+v changed %d", f, changed)
	}
	pickKeyboard(t, h, "Default")
	recv(t, saved)
	if f := store.stored(); f.Layout != "" || !h.d.layoutItems[""].Checked {
		t.Fatalf("stored %+v", f)
	}
	if len(h.errs) != 0 {
		t.Fatal("unexpected error")
	}
}

func TestKeyboardMenuQuickConnectKeepsLayoutForTheSession(t *testing.T) {
	h, store, saved := keyboardDevice(t, nil)
	pickKeyboard(t, h, "English (US)")
	recv(t, saved)
	if store.stored() != nil || len(h.errs) != 0 || !h.d.layoutItems["us"].Checked {
		t.Fatalf("stored %+v errs %d", store.stored(), len(h.errs))
	}
	if got := h.d.resolveLayout(h.d.layoutChoice, typing.LayoutDE); got != typing.LayoutUS {
		t.Fatalf("session layout %s", got)
	}
}

func TestKeyboardLayoutChoiceIsUsedWhenTyping(t *testing.T) {
	h := newClipDevice(t, "<", nil) // '<' maps in US only; local layout DE
	h.d.layoutChoice, h.d.layoutChosen = "us", true
	h.run(t)
	if len(h.errs) != 0 {
		t.Fatalf("err %v", describe(<-h.errs))
	}
	// US types '<' as Shift+Comma: Shift down, Comma down, Comma up, Shift up. The test server
	// records asynchronously, so wait for all four before DE runs (issue #118).
	if typed := len(waitInput(t, h.srv, 4)); typed != 4 {
		t.Fatalf("US: %d messages, want 4", typed)
	}
	h.d.layoutChoice = ""
	h.run(t)
	if n := len(inputMessages(h.srv)); n != 4 || len(h.errs) != 0 { // DE skips '<' silently
		t.Fatalf("DE: %d messages after 4, errs %d", n, len(h.errs))
	}
}

func TestKeyboardMenuReportsWriteError(t *testing.T) {
	h, store, saved := keyboardDevice(t, &favorites.Favorite{})
	store.setErr = errors.New("disk full")
	pickKeyboard(t, h, "German")
	recv(t, saved)
	if err := <-h.errs; err == nil || err.Error() != "disk full" {
		t.Fatalf("err %v", err)
	}
}

func TestAdoptFavoriteUpdatesKeyboardMenu(t *testing.T) {
	h, _, _ := keyboardDevice(t, &favorites.Favorite{Layout: "de"})
	h.d.adoptFavorite("us", favorites.AutoConnectOn)
	if !h.d.layoutItems["us"].Checked || h.d.layoutItems["de"].Checked || h.d.autoOverride != favorites.AutoConnectOn || h.d.layoutChoice != "us" {
		t.Fatal("form edit not adopted")
	}
}

// A change in the Keyboard menu shows in the favorites form: in the read-only view, and in an
// open edit mode for the fields the user has not touched; a touched field survives the reload.
func TestKeyboardMenuChangeShowsInForm(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})
	d.isFavorite = true
	editFavorite(t, h, "device.example.com", "admin")
	s.autoSel.SetSelected("Off") // touched: must survive
	d.setLayout("us")
	h.waitReload(t)
	if s.layoutSel.Selected != "English (US)" || s.autoSel.Selected != "Off" {
		t.Fatalf("edit mode shows layout %q, auto-connect %q", s.layoutSel.Selected, s.autoSel.Selected)
	}
	test.Tap(s.editCancelBtn)
	if s.mode != modeView || s.values["layout"].Text != "English (US)" {
		t.Fatalf("view shows %q", s.values["layout"].Text)
	}
	editFavorite(t, h, "device.example.com", "admin")
	if s.layoutSel.Selected != "English (US)" {
		t.Fatalf("edit shows %q", s.layoutSel.Selected)
	}
}
