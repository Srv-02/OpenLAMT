package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/typing"
)

// Every layout is offered by all three choice fields and its code survives a save and reload.
func TestEveryLayoutIsOfferedAndStoredByCode(t *testing.T) {
	if len(layoutNames()) != 16 {
		t.Fatalf("%d layouts", len(layoutNames()))
	}
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.editBtn)
	if got := len(s.layoutSel.Options); got != 1+len(typing.Layouts()) {
		t.Fatalf("form offers %d options", got)
	}
	for _, l := range typing.Layouts() {
		s.layoutSel.SetSelected(layoutLabel(l))
		if got := selectedLayout(s.layoutSel); got != string(l) {
			t.Fatalf("%s selects code %q", layoutLabel(l), got)
		}
	}
	s.layoutSel.SetSelected("French (Switzerland)")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if f := s.items[0]; f.Layout != "ch-fr" || s.values["layout"].Text != "French (Switzerland)" {
		t.Fatalf("%+v %q", f, s.values["layout"].Text)
	}
	// the same field offers the layout again when editing
	test.Tap(s.editBtn)
	if s.layoutSel.Selected != "French (Switzerland)" {
		t.Fatalf("selected %q", s.layoutSel.Selected)
	}
}

func TestSettingsStoreNewLayoutCodes(t *testing.T) {
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)
	_, drops, _ := settingsParts(t, s.win)
	layout := drops[4] // the last row, "Type clipboard layout"
	if got := len(layout.Options); got != 1+len(typing.Layouts()) {
		t.Fatalf("settings offer %d options", got)
	}
	layout.SetSelected("Swedish/Finnish")
	if got := dropdownLayout(layout); got != "se-fi" {
		t.Fatalf("code %q", got)
	}
}

func TestKeyboardMenuStoresNewLayoutCode(t *testing.T) {
	h, store, saved := keyboardDevice(t, &favorites.Favorite{Layout: "de"})
	pickKeyboard(t, h, "Portuguese (Brazil)")
	recv(t, saved)
	if f := store.stored(); f.Layout != "br" || !h.d.layoutItems["br"].Checked || h.d.layoutItems["de"].Checked {
		t.Fatalf("stored %+v", f)
	}
	// an old stored value still resolves
	if got := layoutChoiceOf("de"); got != "de" {
		t.Fatalf("old value %q", got)
	}
	if got := layoutChoiceOf("nonsense"); got != "" {
		t.Fatalf("unknown value %q", got)
	}
}
