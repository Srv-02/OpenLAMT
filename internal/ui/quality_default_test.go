package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
)

// A favorite without a quality of its own (Default, also every file written before the field)
// follows the global setting; an explicit High or Low does not; a device that is not a
// favorite follows it as well.
func TestDeviceQualityDefaultFollowsGlobalSetting(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	for _, c := range []struct {
		name   string
		global kvm.Quality
		fav    *favorites.Favorite
		want   kvm.Quality
	}{
		{"default, global low", kvm.QualityLow, &favorites.Favorite{}, kvm.QualityLow},
		{"default, global high", kvm.QualityHigh, &favorites.Favorite{}, kvm.QualityHigh},
		{"explicit high, global low", kvm.QualityLow, &favorites.Favorite{Quality: favorites.QualityHigh}, kvm.QualityHigh},
		{"explicit low, global high", kvm.QualityHigh, &favorites.Favorite{Quality: favorites.QualityLow}, kvm.QualityLow},
		{"no favorite, global low", kvm.QualityLow, nil, kvm.QualityLow},
	} {
		NewSettings(a.Preferences()).SetKVMQuality(c.global)
		h := newDevice(t, a, &session.Fake{FP: fp}, true)
		if h.d.kvmQuality != c.global {
			t.Errorf("%s: initial quality %v", c.name, h.d.kvmQuality)
		}
		loaded := make(chan struct{}, 1)
		h.d.onQualityLoaded = func() { loaded <- struct{}{} }
		h.d.layouts = &fakeLayouts{fav: c.fav}
		h.d.loadQuality()
		recv(t, loaded)
		if h.d.kvmQuality != c.want || !h.d.qualityItems[c.want].Checked {
			t.Errorf("%s: quality %v", c.name, h.d.kvmQuality)
		}
	}
}

// Choosing a quality in the device window stores it explicitly, whatever the global setting is.
func TestDeviceQualityMenuStoresExplicitChoice(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	NewSettings(a.Preferences()).SetKVMQuality(kvm.QualityLow)
	h := newDevice(t, a, &session.Fake{FP: fp}, true)
	store := &fakeLayouts{fav: &favorites.Favorite{}}
	h.d.layouts = store
	saved := make(chan struct{}, 1)
	h.d.onQualitySaved = func() { saved <- struct{}{} }
	h.d.setQuality(kvm.QualityLow) // equals the global setting, still stored
	recv(t, saved)
	if f := store.stored(); f.Quality != favorites.QualityLow {
		t.Fatalf("stored %+v", f)
	}
}

// The form offers Default, High and Low. Default removes the override; saving something else
// keeps an explicit choice and a Default alike.
func TestFormQualityDefaultOverrideAndKeep(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true, Quality: favorites.QualityLow})
	editFavorite(t, h, "device.example.com", "admin")
	if got := s.qualitySel.Options; len(got) != 3 || got[0] != "Default" || got[1] != "High" || got[2] != "Low" || s.qualitySel.Selected != "Low" {
		t.Fatalf("options %v selected %q", got, s.qualitySel.Selected)
	}
	s.name.SetText("Lab") // only the name changes: the explicit choice stays
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].Quality != favorites.QualityLow || s.values["quality"].Text != "Low" {
		t.Fatalf("explicit choice lost: %+v", s.items[0])
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelected("Default")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].Quality != "" || s.values["quality"].Text != "Default" {
		t.Fatalf("Default did not remove the override: %+v", s.items[0])
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelected("High") // explicit High differs from Default even with global High
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].Quality != favorites.QualityHigh {
		t.Fatalf("explicit High not stored: %+v", s.items[0])
	}
}

// A form change from Default to an explicit quality reaches the open window; with the global
// setting as Default the window follows it.
func TestFormQualityDefaultReachesOpenWindow(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.settings.SetKVMQuality(kvm.QualityLow)
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true, Quality: favorites.QualityHigh})
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})
	d.applyQuality(kvm.QualityHigh)
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelected("Default")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if d.kvmQuality != kvm.QualityLow {
		t.Fatalf("window quality %v, want the global Low", d.kvmQuality)
	}
}
