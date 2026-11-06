package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

const (
	DefaultPollInterval = 10 * time.Second
	MinPollInterval     = time.Second
	MaxPollInterval     = time.Hour
	prefPollInterval    = "poll_interval_seconds"
	prefTypeLayout      = "type_layout"
	prefKVMIdleMinutes  = "kvm_idle_minutes"
	prefKVMAutoConnect  = "kvm_auto_connect"
	prefKVMQuality      = "kvm_quality"
	prefLanguage        = "language"
	prefAppearance      = "appearance"
	prefUpdateCheck     = "update_check"
	prefColorBlind      = "color_blind"

	// DefaultKVMIdleTimeout ends an unattended KVM session after half an hour.
	DefaultKVMIdleTimeout = 30 * time.Minute
)

// KVMIdleChoices are the offered idle timeouts; 0 means never.
var KVMIdleChoices = []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 0}

var ErrInterval = errors.New("poll interval must be between 1 and 3600 seconds")

type Settings struct{ prefs fyne.Preferences }

func NewSettings(prefs fyne.Preferences) Settings { return Settings{prefs: prefs} }

func (s Settings) PollInterval() time.Duration {
	d := time.Duration(s.prefs.IntWithFallback(prefPollInterval, int(DefaultPollInterval/time.Second))) * time.Second
	if d < MinPollInterval || d > MaxPollInterval {
		return DefaultPollInterval
	}
	return d
}

func (s Settings) SetPollInterval(d time.Duration) error {
	if d < MinPollInterval || d > MaxPollInterval || d%time.Second != 0 {
		return ErrInterval
	}
	s.prefs.SetInt(prefPollInterval, int(d/time.Second))
	return nil
}

// TypeLayout is the global layout of "Type clipboard"; empty means the system language decides.
func (s Settings) TypeLayout() typing.Layout {
	l, _ := typing.ParseLayout(s.prefs.String(prefTypeLayout))
	return l
}

// SetTypeLayout stores the global layout; empty restores the system language.
func (s Settings) SetTypeLayout(l typing.Layout) {
	s.prefs.SetString(prefTypeLayout, string(l))
}

// KVMIdleTimeout is how long a KVM session may go without local input; 0 means never.
func (s Settings) KVMIdleTimeout() time.Duration {
	m := s.prefs.IntWithFallback(prefKVMIdleMinutes, int(DefaultKVMIdleTimeout/time.Minute))
	d := time.Duration(m) * time.Minute
	for _, c := range KVMIdleChoices {
		if c == d {
			return d
		}
	}
	return DefaultKVMIdleTimeout
}

// SetKVMIdleTimeout stores one of KVMIdleChoices; other values fall back to the default on read.
func (s Settings) SetKVMIdleTimeout(d time.Duration) {
	s.prefs.SetInt(prefKVMIdleMinutes, int(d/time.Minute))
}

// KVMAutoConnect is the global "connect KVM automatically" setting; off by default.
func (s Settings) KVMAutoConnect() bool { return s.prefs.BoolWithFallback(prefKVMAutoConnect, false) }

// SetKVMAutoConnect stores the global setting.
func (s Settings) SetKVMAutoConnect(on bool) { s.prefs.SetBool(prefKVMAutoConnect, on) }

// AutoConnectFor resolves a favorite's override (favorites.AutoConnectOn, AutoConnectOff or
// empty for the global setting) to whether KVM connects automatically.
func (s Settings) AutoConnectFor(override string) bool {
	switch override {
	case favorites.AutoConnectOn:
		return true
	case favorites.AutoConnectOff:
		return false
	}
	return s.KVMAutoConnect()
}

// KVMQuality is the global KVM quality, the Default of every favorite without its own choice;
// High unless Low is stored.
func (s Settings) KVMQuality() kvm.Quality {
	return kvmQualityOf(s.prefs.StringWithFallback(prefKVMQuality, favorites.QualityHigh))
}

// SetKVMQuality stores the global KVM quality.
func (s Settings) SetKVMQuality(q kvm.Quality) { s.prefs.SetString(prefKVMQuality, favoriteQuality(q)) }

// QualityFor resolves a favorite's quality override (favorites.QualityHigh, QualityLow or empty
// for the global setting) to the quality of the next connection.
func (s Settings) QualityFor(override string) kvm.Quality {
	switch override {
	case favorites.QualityHigh, favorites.QualityLow:
		return kvmQualityOf(override)
	}
	return s.KVMQuality()
}

// Language is the stored UI language: i18n.System (the default, the OS decides), i18n.German or
// i18n.English; unknown values count as System. It takes effect at the next start (i18n.Init).
func (s Settings) Language() string {
	if l := s.prefs.String(prefLanguage); i18n.Valid(l) {
		return l
	}
	return i18n.System
}

// SetLanguage stores the UI language for the next start.
func (s Settings) SetLanguage(l string) { s.prefs.SetString(prefLanguage, l) }

// Values of the setting "Appearance": System follows the OS (the default), Light and Dark pin
// the variant.
const (
	AppearanceSystem = ""
	AppearanceLight  = "light"
	AppearanceDark   = "dark"
)

// Appearance is the stored appearance; unknown values count as AppearanceSystem.
func (s Settings) Appearance() string {
	switch a := s.prefs.String(prefAppearance); a {
	case AppearanceLight, AppearanceDark:
		return a
	}
	return AppearanceSystem
}

// SetAppearance stores the appearance; applyAppearance makes the open windows follow it.
func (s Settings) SetAppearance(a string) { s.prefs.SetString(prefAppearance, a) }

// ThemeVariant is the variant the app draws in: the pinned one for Light and Dark, otherwise
// system, the variant the OS reports. The only place that decides it (FollowTheme).
func (s Settings) ThemeVariant(system fyne.ThemeVariant) fyne.ThemeVariant {
	switch s.Appearance() {
	case AppearanceLight:
		return theme.VariantLight
	case AppearanceDark:
		return theme.VariantDark
	}
	return system
}

// UpdateCheck is whether OpenLAMT asks GitHub for a newer release at start; on by default.
func (s Settings) UpdateCheck() bool { return s.prefs.BoolWithFallback(prefUpdateCheck, true) }

// SetUpdateCheck stores the setting; "Don't check for updates" in the update dialog turns it off.
func (s Settings) SetUpdateCheck(on bool) { s.prefs.SetBool(prefUpdateCheck, on) }

// ColorBlind is the setting "Color blind mode": status dots in the palettes that stay distinct
// with red-green color blindness (style.StatusColor); off by default.
func (s Settings) ColorBlind() bool { return s.prefs.BoolWithFallback(prefColorBlind, false) }

// SetColorBlind stores the setting; applyAppearance makes the open windows follow it.
func (s Settings) SetColorBlind(on bool) { s.prefs.SetBool(prefColorBlind, on) }
