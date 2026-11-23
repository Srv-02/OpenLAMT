package ui

import (
	"slices"
	"strconv"
	"time"

	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/configfile"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
)

// Snapshot is the global settings in the form of the configuration file (Export Config). It
// holds the stored values, not what an open settings dialog shows. Hidden messages are
// left out: they belong to the person at this computer.
func (s Settings) Snapshot() configfile.Settings {
	poll := int(s.PollInterval() / time.Second)
	layout := string(s.TypeLayout())
	idle := int(s.KVMIdleTimeout() / time.Minute)
	auto, quality := s.KVMAutoConnect(), favoriteQuality(s.KVMQuality())
	language, look := s.Language(), s.Appearance()
	updates, blind := s.UpdateCheck(), s.ColorBlind()
	return configfile.Settings{PollIntervalSeconds: &poll, TypeLayout: &layout, KVMIdleMinutes: &idle,
		KVMAutoConnect: &auto, KVMQuality: &quality, Language: &language, Appearance: &look,
		UpdateCheck: &updates, ColorBlind: &blind}
}

// validateSettings checks every value of c that the file holds; a value out of range makes the
// whole file damaged, so an import never applies half of it.
func validateSettings(c configfile.Settings) error {
	bad := func(name string) error {
		return &configfile.DamagedError{Reason: configfile.ReasonBadSetting, Setting: name}
	}
	if v := c.PollIntervalSeconds; v != nil {
		if d := time.Duration(*v) * time.Second; d < MinPollInterval || d > MaxPollInterval {
			return bad("poll_interval_seconds")
		}
	}
	if v := c.TypeLayout; v != nil && *v != "" {
		if _, ok := typing.ParseLayout(*v); !ok {
			return bad("type_layout")
		}
	}
	if v := c.KVMIdleMinutes; v != nil && !slices.Contains(KVMIdleChoices, time.Duration(*v)*time.Minute) {
		return bad("kvm_idle_minutes")
	}
	if v := c.KVMQuality; v != nil && *v != favorites.QualityHigh && *v != favorites.QualityLow {
		return bad("kvm_quality")
	}
	if v := c.Language; v != nil && !i18n.Valid(*v) {
		return bad("language")
	}
	if v := c.Appearance; v != nil && *v != AppearanceSystem && *v != AppearanceLight && *v != AppearanceDark {
		return bad("appearance")
	}
	return nil
}

// ApplySnapshot stores the values of c that the file holds (after validateSettings).
func (s Settings) ApplySnapshot(c configfile.Settings) {
	if v := c.PollIntervalSeconds; v != nil {
		_ = s.SetPollInterval(time.Duration(*v) * time.Second)
	}
	if v := c.TypeLayout; v != nil {
		l, _ := typing.ParseLayout(*v)
		s.SetTypeLayout(l)
	}
	if v := c.KVMIdleMinutes; v != nil {
		s.SetKVMIdleTimeout(time.Duration(*v) * time.Minute)
	}
	if v := c.KVMAutoConnect; v != nil {
		s.SetKVMAutoConnect(*v)
	}
	if v := c.KVMQuality; v != nil {
		s.SetKVMQuality(kvmQualityOf(*v))
	}
	if v := c.Language; v != nil {
		s.SetLanguage(*v)
	}
	if v := c.Appearance; v != nil {
		s.SetAppearance(*v)
	}
	if v := c.UpdateCheck; v != nil {
		s.SetUpdateCheck(*v)
	}
	if v := c.ColorBlind; v != nil {
		s.SetColorBlind(*v)
	}
}

// reload shows the stored settings in the fields, after an import changed them behind the
// open dialog; otherwise Save would write the old values back.
func (f *settingsForm) reload(s *Settings) {
	f.interval.SetText(strconv.Itoa(int(s.PollInterval() / time.Second)))
	f.layout.SetSelected(newLayoutSelect(lang.L("Automatic (system language)"), string(s.TypeLayout())).Selected)
	f.idle.SetSelected(idleLabel(s.KVMIdleTimeout()))
	f.quality.SetSelected(qualityText(favoriteQuality(s.KVMQuality())))
	f.autoKVM.SetChecked(s.KVMAutoConnect())
	f.language.SetSelected(languageLabel(s.Language()))
	f.appearance.SetSelected(appearanceLabel(s.Appearance()))
	f.colorBlind.SetChecked(s.ColorBlind())
	f.updates.SetChecked(s.UpdateCheck())
}
