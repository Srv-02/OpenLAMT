package ui

import (
	"image/color"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/i18n"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Geometry of the settings dialog (Settings.html, one 45 px row added for the language, one for
// the appearance and one for the update check, 45 for the two-line row "Color blind mode"): 600 x 620, padding 20/22/18, 16 between the blocks, a number field of 56 x 26.
const (
	settingsWidth  float32 = 600
	settingsHeight float32 = 620

	numberFieldWidth  float32 = 56
	numberFieldHeight float32 = 26
	rowPadH           float32 = 14
)

// settingsForm holds the fields of the global settings: poll interval (also the interval of
// the sidebar's status dots), the layout of "Type clipboard", the KVM idle timeout, the KVM quality that favorites without a choice use, whether KVM
// connects automatically, the UI language, the appearance and the update check at start.
type settingsForm struct {
	interval   *escEntry
	layout     *widgets.Dropdown
	idle       *widgets.Dropdown
	quality    *widgets.Dropdown
	autoKVM    *widgets.Switch
	language   *widgets.Dropdown
	appearance *widgets.Dropdown
	colorBlind *widgets.Switch
	updates    *widgets.Switch
}

func newSettingsForm(s *Settings) *settingsForm {
	f := &settingsForm{interval: newEscEntry()}
	f.interval.SetText(strconv.Itoa(int(s.PollInterval() / time.Second)))
	f.interval.Validator = validateInterval
	f.layout = newLayoutDropdown(lang.L("Automatic (system language)"), string(s.TypeLayout()))
	f.idle = newIdleDropdown(s.KVMIdleTimeout())
	f.quality = newQualityDropdown(s.KVMQuality())
	f.autoKVM = widgets.NewSwitch(nil)
	f.autoKVM.SetChecked(s.KVMAutoConnect())
	f.language = newLanguageDropdown(s.Language())
	f.appearance = newAppearanceDropdown(s.Appearance())
	f.colorBlind = widgets.NewSwitch(nil)
	f.colorBlind.SetChecked(s.ColorBlind())
	f.updates = widgets.NewSwitch(nil)
	f.updates.SetChecked(s.UpdateCheck())
	return f
}

// appearanceChoices are the options of the setting "Appearance", System first.
var appearanceChoices = []string{AppearanceSystem, AppearanceLight, AppearanceDark}

func appearanceLabel(a string) string {
	switch a {
	case AppearanceLight:
		return lang.L("Light")
	case AppearanceDark:
		return lang.L("Dark")
	}
	return lang.L("System")
}

func newAppearanceDropdown(current string) *widgets.Dropdown {
	var options []string
	for _, a := range appearanceChoices {
		options = append(options, appearanceLabel(a))
	}
	d := widgets.NewDropdown(options)
	d.SetSelected(appearanceLabel(current))
	return d
}

// languageChoices are the options of the setting "Language", System first.
var languageChoices = []string{i18n.System, i18n.German, i18n.English}

// languageLabel names a language in itself, so it can be found in either UI language; only
// System is translated.
func languageLabel(l string) string {
	switch l {
	case i18n.German:
		return "Deutsch"
	case i18n.English:
		return "English"
	}
	return lang.L("System")
}

func newLanguageDropdown(current string) *widgets.Dropdown {
	var options []string
	for _, l := range languageChoices {
		options = append(options, languageLabel(l))
	}
	d := widgets.NewDropdown(options)
	d.SetSelected(languageLabel(current))
	return d
}

// newLayoutDropdown is newLayoutSelect in the design's choice field.
func newLayoutDropdown(none, current string) *widgets.Dropdown {
	sel := newLayoutSelect(none, current)
	d := widgets.NewDropdown(sel.Options)
	d.SetSelected(sel.Selected)
	return d
}

// dropdownLayout returns the layout name of a newLayoutDropdown, empty for "none".
func dropdownLayout(d *widgets.Dropdown) string {
	if l, ok := layoutOfLabel(d.Selected); ok {
		return string(l)
	}
	return ""
}

// qualityOptions are the choices of the global KVM quality, High first.
func qualityOptions() []kvm.Quality { return []kvm.Quality{kvm.QualityHigh, kvm.QualityLow} }

func newQualityDropdown(current kvm.Quality) *widgets.Dropdown {
	var options []string
	for _, q := range qualityOptions() {
		options = append(options, qualityText(favoriteQuality(q)))
	}
	d := widgets.NewDropdown(options)
	d.SetSelected(qualityText(favoriteQuality(current)))
	return d
}

func newIdleDropdown(current time.Duration) *widgets.Dropdown {
	var options []string
	for _, c := range KVMIdleChoices {
		options = append(options, idleLabel(c))
	}
	d := widgets.NewDropdown(options)
	d.SetSelected(idleLabel(current))
	return d
}

// valid reports whether the form can be saved.
func (f *settingsForm) valid() bool { return validateInterval(f.interval.Text) == nil }

// apply stores the fields. Open device windows and the status dots follow the interval
// through the preferences listener of the manager window; the language follows at the next start,
// the appearance and color blind mode once the caller runs applyAppearance.
func (f *settingsForm) apply(s *Settings) error {
	s.SetLanguage(languageChoices[max(0, f.language.SelectedIndex())])
	s.SetAppearance(appearanceChoices[max(0, f.appearance.SelectedIndex())])
	s.SetColorBlind(f.colorBlind.Checked())
	s.SetTypeLayout(typing.Layout(dropdownLayout(f.layout)))
	s.SetKVMAutoConnect(f.autoKVM.Checked())
	s.SetUpdateCheck(f.updates.Checked())
	s.SetKVMQuality(qualityOptions()[max(0, f.quality.SelectedIndex())])
	s.SetKVMIdleTimeout(KVMIdleChoices[max(0, f.idle.SelectedIndex())])
	sec, _ := strconv.Atoi(f.interval.Text)
	return s.SetPollInterval(time.Duration(sec) * time.Second)
}

// escEntry is an entry that also closes the dialog on Esc.
type escEntry struct {
	widget.Entry
	onEscape func()
}

func newEscEntry() *escEntry {
	e := &escEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *escEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && e.onEscape != nil {
		e.onEscape()
		return
	}
	e.Entry.TypedKey(k)
}

// fieldTheme is the theme of the number field: its own fill and border inside the card (whose
// theme hides entry borders), and a padding that makes the entry 26 high.
type fieldTheme struct{ style.Theme }

func (fieldTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameInputBackground:
		return style.Color(style.ColorNameField)
	case theme.ColorNameInputBorder:
		return style.Color(style.ColorNameDialogBorder)
	}
	return style.Color(name)
}

// blank is the empty symbol that replaces the entry's validation marks: the field is too
// small for them, the red border and the disabled Save say it already.
var blank = fyne.NewStaticResource("blank.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"></svg>`))

func (fieldTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	if name == theme.IconNameConfirm || name == theme.IconNameError {
		return blank
	}
	return style.Theme{}.Icon(name)
}

func (fieldTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameInnerPadding {
		return 5
	}
	return style.Size(name)
}

// numberField puts the entry at the design's 56 x 26.
func numberField(e *escEntry) fyne.CanvasObject {
	return container.New(&fixedSizeLayout{size: fyne.NewSize(numberFieldWidth, numberFieldHeight)},
		container.NewThemeOverride(e, fieldTheme{}))
}

// settingsRow is one 44 px row of the card: the label takes the width that is left, the control
// sits at the right.
func settingsRow(label string, control fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 16, fill: 0}, designText(label, style.Size("text"), "foreground", false), control))
}

// colorBlindDotGap is the space between the preview dots of "Color blind mode", colorBlindPadV the
// padding above and below its two-line label.
const (
	colorBlindDotGap float32 = 6
	colorBlindPadV   float32 = 10
)

// colorBlindRow is the row "Color blind mode": a two-line label (secondary line 11.5), four
// preview dots (on, standby, off, unknown) in the color-blind palette of the current appearance,
// and the switch.
func colorBlindRow(sw *widgets.Switch) fyne.CanvasObject {
	label := container.New(layout.NewCustomPaddedVBoxLayout(2),
		designText(lang.L("Color blind mode"), style.Size("text"), "foreground", false),
		designText(lang.L("Status colors that stay distinct with red-green color blindness."),
			style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false))
	var dots []fyne.CanvasObject
	for _, s := range []widgets.DotState{widgets.DotOn, widgets.DotAsleep, widgets.DotOff, widgets.DotUnknown} {
		dots = append(dots, widgets.NewColorBlindPreviewDot(s, style.StatusDotSize))
	}
	preview := container.New(&rowLayout{gap: colorBlindDotGap, fill: -1}, dots...)
	return container.New(layout.NewCustomPaddedLayout(colorBlindPadV, colorBlindPadV, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 16, fill: 0}, label, preview, sw))
}

// footerLink is a flat 28 px link button of the footer.
func footerLink(text string, icon fyne.Resource, iconSize float32, tapped func()) *widgets.Link {
	l := widgets.NewLink(text, nil)
	l.OnTapped = tapped
	l.Icon, l.IconSize, l.IconLeading, l.IconGap = icon, iconSize, true, 6
	l.Height, l.PadH, l.TextSize = 28, 10, style.Size("text")
	return l
}

func settingsFooter(parent fyne.Window, a fyne.App) fyne.CanvasObject {
	line := hairline(style.ColorNameTile)
	name := designText("OpenLAMT", style.Size("text"), "foreground", true)
	info := container.New(layout.NewCustomPaddedVBoxLayout(1), name)
	if v := versionText(a, style.Size(style.SizeNameSecondaryText)); v != nil {
		info.Add(v)
	}
	support := footerLink(lang.L("Support"), icons.Get(icons.Heart, icons.HeartPink), 14, func() { openURL(supportURL) })
	about := footerLink(lang.L("About & Credits"), icons.Get(icons.Info, icons.Secondary), 15, func() { showAboutDialog(parent, a) })
	row := container.New(&rowLayout{gap: 12, fill: 2}, widgets.NewAppIcon(icons.AppSimple, 36), info, spacer(), support, about)
	return container.New(layout.NewCustomPaddedVBoxLayout(14), line, row)
}

// showSettingsDialog shows the global settings on parent; Save stays disabled while the
// interval is invalid.
func showSettingsDialog(parent fyne.Window, s *Settings) {
	a := fyne.CurrentApp()
	f := newSettingsForm(s)
	showDesignDialog(parent, dialogSpec{width: settingsWidth, height: settingsHeight, padTop: 20, padRight: 22 - scrollInset,
		padBottom: 18, padL: 22 - scrollInset, gap: 16, flex: 0},
		func(d *designDialog) []fyne.CanvasObject {
			save := dialogButton(lang.L("Save"), true, 18, nil)
			cancel := dialogButton(lang.L("Cancel"), false, 16, d.Close)
			doSave := func() {
				if !f.valid() {
					return
				}
				d.Close()
				before, look, colorBlind := s.Language(), s.Appearance(), s.ColorBlind()
				err := f.apply(s)
				if s.Appearance() != look || s.ColorBlind() != colorBlind {
					applyAppearance(a)
				}
				if err != nil {
					showError(parent, err)
				} else if s.Language() != before {
					showMessage(parent, lang.L("Language"), lang.L("The language changes the next time OpenLAMT starts."), "")
				}
			}
			save.OnTapped = doSave
			sync := func() {
				if f.valid() {
					save.Enable()
				} else {
					save.Disable()
				}
			}
			f.interval.OnChanged = func(string) { sync() }
			f.interval.OnSubmitted = func(string) { doSave() }
			f.interval.onEscape = d.Close
			f.layout.OnEscape, f.idle.OnEscape, f.quality.OnEscape, f.autoKVM.OnEscape, f.language.OnEscape, f.appearance.OnEscape, f.colorBlind.OnEscape, f.updates.OnEscape = d.Close, d.Close, d.Close, d.Close, d.Close, d.Close, d.Close, d.Close
			save.OnEscape, cancel.OnEscape = d.Close, d.Close
			sync()

			// The rows run top to bottom in the order of the spec; Tab follows the same order,
			// because Fyne moves the focus through the widget tree.
			unit := designText(lang.L("seconds"), style.Size("text"), style.ColorNameTextSecondary, false)
			interval := container.New(&rowLayout{gap: 8, fill: -1}, numberField(f.interval), unit)
			card := widgets.NewFormCard(
				settingsRow(lang.L("Language"), f.language),
				settingsRow(lang.L("Appearance"), f.appearance),
				colorBlindRow(f.colorBlind),
				settingsRow(lang.L("Check for updates"), f.updates),
				settingsRow(lang.L("Poll interval"), interval),
				settingsRow(lang.L("KVM quality"), f.quality),
				settingsRow(lang.L("KVM auto-connect"), f.autoKVM),
				settingsRow(lang.L("Disconnect KVM without input after"), f.idle),
				settingsRow(lang.L("Type clipboard layout"), f.layout))
			card.Background = style.ColorNameDialogCard
			title := designText(lang.L("Settings"), style.Size("subHeadingText"), "foreground", true)
			buttons := container.New(&rowLayout{gap: 8, fill: 0}, spacer(), cancel, save)
			// Title, card and footer scroll, so the buttons stay reachable in a small window.
			body := scrollBody(dialogSpec{gap: 16, flex: 2}, title, card, spacer(), settingsFooter(parent, a))
			return []fyne.CanvasObject{body, buttons}
		})
}
