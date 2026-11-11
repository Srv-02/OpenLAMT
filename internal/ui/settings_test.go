package ui

import (
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

func TestSettingsDefaultAndRoundTrip(t *testing.T) {
	a := test.NewTempApp(t)
	s := NewSettings(a.Preferences())
	if s.PollInterval() != 10*time.Second {
		t.Fatalf("default %v", s.PollInterval())
	}
	if err := s.SetPollInterval(30 * time.Second); err != nil || s.PollInterval() != 30*time.Second {
		t.Fatalf("%v %v", err, s.PollInterval())
	}
	for _, bad := range []time.Duration{0, 500 * time.Millisecond, 3601 * time.Second, 1500 * time.Millisecond} {
		if err := s.SetPollInterval(bad); !errors.Is(err, ErrInterval) {
			t.Fatalf("%v accepted: %v", bad, err)
		}
	}
	if s.PollInterval() != 30*time.Second {
		t.Fatal("invalid value changed the setting")
	}
	a.Preferences().SetInt("poll_interval_seconds", 99999)
	if s.PollInterval() != 10*time.Second {
		t.Fatalf("garbage not clamped: %v", s.PollInterval())
	}
}

func TestTypeLayoutSetting(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.TypeLayout() != "" {
		t.Fatal("default must be empty (system language)")
	}
	s.SetTypeLayout(typing.LayoutUS)
	if s.TypeLayout() != typing.LayoutUS {
		t.Fatalf("%q", s.TypeLayout())
	}
	s.SetTypeLayout("")
	if s.TypeLayout() != "" {
		t.Fatalf("%q", s.TypeLayout())
	}
}

func TestKVMIdleTimeoutSetting(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.KVMIdleTimeout() != DefaultKVMIdleTimeout {
		t.Fatalf("default %v", s.KVMIdleTimeout())
	}
	s.SetKVMIdleTimeout(0)
	if s.KVMIdleTimeout() != 0 {
		t.Fatal("never not stored")
	}
	s.SetKVMIdleTimeout(7 * time.Minute) // not offered: default
	if s.KVMIdleTimeout() != DefaultKVMIdleTimeout {
		t.Fatalf("%v", s.KVMIdleTimeout())
	}
}

func TestKVMAutoConnectSetting(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.KVMAutoConnect() {
		t.Fatal("default must be off")
	}
	// The favorite's override wins; empty (or unknown) means the global setting.
	for _, c := range []struct {
		global   bool
		override string
		want     bool
	}{
		{false, "", false}, {true, "", true}, {false, "on", true}, {true, "off", false},
		{true, "on", true}, {false, "off", false}, {true, "bogus", true},
	} {
		s.SetKVMAutoConnect(c.global)
		if got := s.AutoConnectFor(c.override); got != c.want {
			t.Errorf("global %v override %q: %v", c.global, c.override, got)
		}
	}
}

func TestSettingsFormStoresKVMAutoConnect(t *testing.T) {
	englishUI(t)
	s := NewSettings(test.NewTempApp(t).Preferences())
	f := newSettingsForm(&s)
	if f.autoKVM.Checked() {
		t.Fatal("form must start off")
	}
	f.autoKVM.SetChecked(true)
	if err := f.apply(&s); err != nil || !s.KVMAutoConnect() {
		t.Fatalf("%v %v", err, s.KVMAutoConnect())
	}
	if !newSettingsForm(&s).autoKVM.Checked() {
		t.Fatal("form must show the stored value")
	}
}

func TestKVMQualitySetting(t *testing.T) {
	a := test.NewTempApp(t)
	s := NewSettings(a.Preferences())
	if s.KVMQuality() != kvm.QualityHigh {
		t.Fatalf("default %v", s.KVMQuality())
	}
	s.SetKVMQuality(kvm.QualityLow)
	if s.KVMQuality() != kvm.QualityLow {
		t.Fatalf("%v", s.KVMQuality())
	}
	// Default follows the global setting, an explicit choice does not.
	for _, c := range []struct {
		override string
		want     kvm.Quality
	}{{"", kvm.QualityLow}, {favorites.QualityHigh, kvm.QualityHigh}, {favorites.QualityLow, kvm.QualityLow}, {"bogus", kvm.QualityLow}} {
		if got := s.QualityFor(c.override); got != c.want {
			t.Errorf("override %q: %v, want %v", c.override, got, c.want)
		}
	}
	a.Preferences().SetString("kvm_quality", "garbage")
	if s.KVMQuality() != kvm.QualityHigh {
		t.Fatalf("garbage must read as High, got %v", s.KVMQuality())
	}
}

func TestLanguageSetting(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.Language() != i18n.System {
		t.Fatalf("default %q, want System", s.Language())
	}
	for _, l := range []string{i18n.German, i18n.English, i18n.System} {
		s.SetLanguage(l)
		if s.Language() != l {
			t.Fatalf("stored %q, read %q", l, s.Language())
		}
	}
	s.SetLanguage("fr") // not shipped: System
	if s.Language() != i18n.System {
		t.Fatalf("unknown value: %q", s.Language())
	}
}

// The dialog shows the stored language and Save stores the choice.
func TestSettingsFormLanguage(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	f := newSettingsForm(&s)
	if f.language.Selected != "System" {
		t.Fatalf("default shown %q", f.language.Selected)
	}
	f.language.SetSelected("Deutsch")
	if err := f.apply(&s); err != nil {
		t.Fatal(err)
	}
	if s.Language() != i18n.German {
		t.Fatalf("stored %q", s.Language())
	}
	if got := newSettingsForm(&s).language.Selected; got != "Deutsch" {
		t.Fatalf("reopened shows %q", got)
	}
	f.language.SetSelected("English")
	if err := f.apply(&s); err != nil || s.Language() != i18n.English {
		t.Fatalf("%v %q", err, s.Language())
	}
}

func TestAppearanceSetting(t *testing.T) {
	s := NewSettings(test.NewTempApp(t).Preferences())
	if s.Appearance() != AppearanceSystem {
		t.Fatalf("default %q, want System", s.Appearance())
	}
	for _, a := range []string{AppearanceLight, AppearanceDark, AppearanceSystem} {
		s.SetAppearance(a)
		if s.Appearance() != a {
			t.Fatalf("stored %q, read %q", a, s.Appearance())
		}
	}
	s.SetAppearance("sepia") // unknown: System
	if s.Appearance() != AppearanceSystem {
		t.Fatalf("unknown value: %q", s.Appearance())
	}
	for _, c := range []struct {
		appearance   string
		system, want fyne.ThemeVariant
	}{
		{AppearanceSystem, theme.VariantLight, theme.VariantLight},
		{AppearanceSystem, theme.VariantDark, theme.VariantDark},
		{AppearanceLight, theme.VariantDark, theme.VariantLight},
		{AppearanceDark, theme.VariantLight, theme.VariantDark},
	} {
		s.SetAppearance(c.appearance)
		if got := s.ThemeVariant(c.system); got != c.want {
			t.Errorf("%q on system %d: %d, want %d", c.appearance, c.system, got, c.want)
		}
	}
}

// The dialog shows the stored appearance and Save stores the choice.
func TestSettingsFormAppearance(t *testing.T) {
	englishUI(t)
	s := NewSettings(test.NewTempApp(t).Preferences())
	f := newSettingsForm(&s)
	if f.appearance.Selected != "System" || len(f.appearance.Options) != 3 {
		t.Fatalf("default shown %q of %v", f.appearance.Selected, f.appearance.Options)
	}
	f.appearance.SetSelected("Dark")
	if err := f.apply(&s); err != nil || s.Appearance() != AppearanceDark {
		t.Fatalf("stored %q (%v)", s.Appearance(), err)
	}
	if got := newSettingsForm(&s).appearance.Selected; got != "Dark" {
		t.Fatalf("reopened shows %q", got)
	}
}

// "Don’t show again" of the hint "Mount a folder" is stored and ResetHiddenMessages, the
// button "Reset Warnings", shows every hidden message again.
func TestHiddenMessagesReset(t *testing.T) {
	a := test.NewTempApp(t)
	s := NewSettings(a.Preferences())
	if s.HideFolderMountHint() || s.HiddenMessages() {
		t.Fatal("a message is hidden by default")
	}
	s.SetHideFolderMountHint(true)
	if !NewSettings(a.Preferences()).HideFolderMountHint() || !s.HiddenMessages() {
		t.Fatal("not stored")
	}
	s.ResetHiddenMessages()
	if s.HideFolderMountHint() || s.HiddenMessages() {
		t.Fatal("not reset")
	}
	for _, k := range hiddenMessagePrefs {
		a.Preferences().SetBool(k, true)
		if !s.HiddenMessages() {
			t.Fatalf("%s not counted", k)
		}
		s.ResetHiddenMessages()
		if a.Preferences().Bool(k) {
			t.Fatalf("%s not reset", k)
		}
	}
}
