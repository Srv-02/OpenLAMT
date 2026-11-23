package ui

import (
	"image/color"
	"strconv"
	"strings"
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

// Geometry of the settings dialog (gesamtdesign V8): 600 x 760, padding 20/22/18, 16 between the
// blocks, a number field of 56 x 26. The title, the footer and the buttons stay in place, the
// sections between them scroll.
const (
	settingsWidth  float32 = 600
	settingsHeight float32 = 760

	settingsSectionGap float32 = 16 // between two sections

	numberFieldWidth  float32 = 56
	numberFieldHeight float32 = 26
	rowPadH           float32 = 14
)

// settingsForm holds the fields of the global settings: poll interval (also the interval of
// the sidebar's status dots), the layout of "Type clipboard", the KVM idle timeout, the KVM quality that favorites without a choice use, whether KVM
// connects automatically, the folder for screenshots and recordings, the UI language, the
// appearance and the update check at start.
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
	// captureFolder is the folder for screenshots and recordings as chosen in the dialog; empty
	// asks for a file name each time.
	captureFolder string
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
	f.captureFolder = s.CaptureFolder()
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
	s.SetCaptureFolder(f.captureFolder)
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

// hiddenMessagesRow is the row "Hidden messages" (Extras.dc.html): a label with a secondary
// explanation and the button "Reset Warnings", which shows every message hidden with "Don’t
// show again" again at once (not on Save). The button is disabled while nothing is hidden.
func hiddenMessagesRow(s *Settings, onEscape func()) (fyne.CanvasObject, *widgets.PrimaryButton) {
	b := dialogButton(lang.L("Reset Warnings"), false, 14, nil)
	b.OnTapped = func() {
		s.ResetHiddenMessages()
		b.Disable()
	}
	b.OnEscape = onEscape
	if !s.HiddenMessages() {
		b.Disable()
	}
	// The dialog has a fixed width: the explanation wraps at the width left of the button.
	width := settingsWidth - 2*22 - 2*rowPadH - b.MinSize().Width - 16 - 2
	size := style.Size(style.SizeNameSecondaryText)
	lines := []fyne.CanvasObject{designText(lang.L("Hidden messages"), style.Size("text"), "foreground", false)}
	for _, l := range wrapWords(lang.L("Show all messages again that were hidden with “Don’t show again”."), size, width) {
		lines = append(lines, designText(l, size, style.ColorNameTextSecondary, false))
	}
	label := container.New(layout.NewCustomPaddedVBoxLayout(1), lines...)
	return container.New(layout.NewCustomPaddedLayout(colorBlindPadV, colorBlindPadV, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 16, fill: 0}, label, b)), b
}

// wrapWords breaks s into lines no wider than width at size, at spaces.
func wrapWords(s string, size, width float32) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		next := w
		if line != "" {
			next = line + " " + w
		}
		if line != "" && fyne.MeasureText(next, size, fyne.TextStyle{}).Width > width {
			lines = append(lines, line)
			next = w
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// pickCaptureFolder opens the folder dialog of the row "Screenshots and recordings"; tests
// replace it.
var pickCaptureFolder = func(win fyne.Window, chosen func(path string), failed func(error)) {
	defaultPickers().folder(win, chosen, failed)
}

// folderRow is the row "Screenshots and recordings" (gesamtdesign V9, issues #4 and #26): the
// label with the folder in monospace below it, or "Not set: …" while empty, the button
// "Choose…" and, while a folder is set, "×" to clear it again. The value is stored on Save.
type folderRow struct {
	obj    fyne.CanvasObject
	desc   *ellipsisText
	choose *widgets.PrimaryButton
	clear  *widgets.PrimaryButton
	f      *settingsForm
}

func newFolderRow(f *settingsForm, pick func(chosen func(path string)), onEscape func()) *folderRow {
	r := &folderRow{f: f}
	r.choose = dialogButton(lang.L("Choose…"), false, 12, func() { pick(r.set) })
	r.choose.SetIcon(icons.Get(icons.Folder, icons.Foreground))
	r.clear = dialogButton("", false, 8, func() { r.set("") })
	r.clear.SetIcon(icons.Get(icons.Close, icons.Foreground))
	r.choose.OnEscape, r.clear.OnEscape = onEscape, onEscape
	r.desc = newEllipsisText("", style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false)
	r.desc.shrink = true
	label := container.New(layout.NewCustomPaddedVBoxLayout(2),
		designText(lang.L("Screenshots and recordings"), style.Size("text"), "foreground", false), r.desc)
	r.obj = container.New(layout.NewCustomPaddedLayout(colorBlindPadV, colorBlindPadV, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 8, fill: 0}, label, r.choose, r.clear))
	r.set(f.captureFolder)
	return r
}

// set shows dir (empty: not set) and keeps it for Save.
func (r *folderRow) set(dir string) {
	r.f.captureFolder = dir
	r.desc.mono = dir != ""
	if dir == "" {
		r.desc.SetText(lang.L("Not set: OpenLAMT asks for a file name each time."))
		r.clear.Hide()
	} else {
		r.desc.SetText(dir)
		r.clear.Show()
	}
	r.desc.Refresh()
	if c, ok := r.obj.(*fyne.Container); ok {
		c.Refresh()
	}
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

// updateStatusSize is the text size of the second line of the row "Check for updates".
var updateStatusSize = style.Size(style.SizeNameSecondaryText)

// updateRow is the row "Check for updates": a label with a status line, the button "Check Now"
// and the switch. The status line shows what the switch does, then "Checking…", then the result
// of the check (green or red) in the row itself.
type updateRow struct {
	obj      fyne.CanvasObject
	label    *fyne.Container
	button   *widgets.PrimaryButton
	width    float32 // the room of the status line
	relayout func()  // makes the dialog fit the new height of the row
	checkNow func(report func(updateCheckResult))
}

func newUpdateRow(sw *widgets.Switch, checkNow func(report func(updateCheckResult)), onEscape func(), relayout func()) *updateRow {
	r := &updateRow{checkNow: checkNow, relayout: relayout}
	r.button = dialogButton(lang.L("Check Now"), false, 14, r.start)
	r.button.OnEscape = onEscape
	if checkNow == nil {
		r.button.Disable()
	}
	r.label = container.New(layout.NewCustomPaddedVBoxLayout(1))
	// The button grows to "Checking…"; the status line wraps at the width left beside it.
	btn := fyne.MeasureText(lang.L("Checking…"), 13, fyne.TextStyle{Bold: true}).Width + 2*14
	r.width = settingsWidth - 2*22 - 2*rowPadH - max(btn, r.button.MinSize().Width) - sw.MinSize().Width - 2*16 - 2
	r.set(lang.L("Checks GitHub for a new version at start."), style.ColorNameTextSecondary)
	r.obj = container.New(layout.NewCustomPaddedLayout(colorBlindPadV, colorBlindPadV, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 16, fill: 0}, r.label, r.button, sw))
	return r
}

// set shows text in the status line below the label.
func (r *updateRow) set(text string, c fyne.ThemeColorName) {
	lines := []fyne.CanvasObject{designText(lang.L("Check for updates"), style.Size("text"), "foreground", false)}
	for _, l := range wrapWords(text, updateStatusSize, r.width) {
		lines = append(lines, designText(l, updateStatusSize, c, false))
	}
	r.label.Objects = lines
	r.label.Refresh()
	if r.relayout != nil {
		r.relayout()
	}
}

// start is a tap on "Check Now": the button is locked while GitHub is asked.
func (r *updateRow) start() {
	r.button.SetText(lang.L("Checking…"))
	r.button.Disable()
	r.set(lang.L("Checking…"), style.ColorNameTextSecondary)
	r.checkNow(func(res updateCheckResult) {
		r.button.SetText(lang.L("Check Now"))
		r.button.Enable()
		switch res.state {
		case updateCheckLatest:
			r.set(res.text, "success")
		case updateCheckFailed:
			r.set(res.text, style.ColorNameErrorText)
		default: // the update dialog opens
			r.set(lang.L("Checks GitHub for a new version at start."), style.ColorNameTextSecondary)
		}
	})
}

// settingsDialog is what the sections of the settings dialog build from. Each section is one
// method that returns its heading and card; showSettingsDialog lists them in display order, so
// a new feature adds its section there and one method here.
type settingsDialog struct {
	parent   fyne.Window
	app      fyne.App
	s        *Settings
	f        *settingsForm
	d        *designDialog
	checkNow func(report func(updateCheckResult))
	relayout func()
	update   *updateRow
	folder   *folderRow
	config   *configTransfer // nil: Export and Import stay disabled
}

// settingsOption adds something to the settings dialog that the caller owns.
type settingsOption func(*settingsDialog)

// withConfig enables Export Config and Import Config of the row "Configuration".
func withConfig(t *configTransfer) settingsOption { return func(c *settingsDialog) { c.config = t } }

// section is a heading (12 px semibold, secondary) above a card of rows, like the pages of the
// device management.
func (c *settingsDialog) section(title string, rows ...fyne.CanvasObject) fyne.CanvasObject {
	card := widgets.NewFormCard(rows...)
	card.Background = style.ColorNameDialogCard
	return settingsGroup(title, style.ColorNameTextSecondary, card)
}

func (c *settingsDialog) generalSection() fyne.CanvasObject {
	return c.section(lang.L("General"),
		settingsRow(lang.L("Language"), c.f.language),
		settingsRow(lang.L("Appearance"), c.f.appearance),
		colorBlindRow(c.f.colorBlind))
}

func (c *settingsDialog) updatesSection() fyne.CanvasObject {
	c.update = newUpdateRow(c.f.updates, c.checkNow, c.d.Close, func() {
		if c.relayout != nil {
			c.relayout()
		}
	})
	return c.section(lang.L("Updates"), c.update.obj)
}

func (c *settingsDialog) devicesSection() fyne.CanvasObject {
	unit := designText(lang.L("seconds"), style.Size("text"), style.ColorNameTextSecondary, false)
	interval := container.New(&rowLayout{gap: 8, fill: -1}, numberField(c.f.interval), unit)
	return c.section(lang.L("Devices"), settingsRow(lang.L("Poll interval"), interval))
}

func (c *settingsDialog) remoteScreenSection() fyne.CanvasObject {
	c.folder = newFolderRow(c.f, func(chosen func(path string)) {
		pickCaptureFolder(c.parent, chosen, func(err error) { showError(c.parent, err) })
	}, c.d.Close)
	return c.section(lang.L("Remote screen"),
		settingsRow(lang.L("KVM quality"), c.f.quality),
		settingsRow(lang.L("KVM auto-connect"), c.f.autoKVM),
		settingsRow(lang.L("Disconnect KVM without input after"), c.f.idle),
		settingsRow(lang.L("Type clipboard layout"), c.f.layout),
		c.folder.obj)
}

func (c *settingsDialog) dataSection() fyne.CanvasObject {
	hidden, _ := hiddenMessagesRow(c.s, c.d.Close)
	return c.section(lang.L("Data"), c.configRow(), hidden)
}

// configRow is the row "Configuration": a label with its explanation and the buttons Export…
// and Import…, which open their dialogs above the settings dialog (#7).
func (c *settingsDialog) configRow() fyne.CanvasObject {
	export := settingsIconButton(lang.L("Export…"), icons.Upload, nil)
	imp := settingsIconButton(lang.L("Import…"), icons.Download, nil)
	export.OnEscape, imp.OnEscape = c.d.Close, c.d.Close
	if c.config == nil {
		export.Disable()
		imp.Disable()
	} else {
		c.config.reloadForm = func() { c.f.reload(c.s) }
		export.OnTapped, imp.OnTapped = c.config.export, c.config.importConfig
	}
	buttons := container.New(&rowLayout{gap: 8, fill: -1}, export, imp)
	// The dialog has a fixed width: the explanation wraps at the width left of the buttons.
	width := settingsWidth - 2*22 - 2*rowPadH - buttons.MinSize().Width - 16 - 2
	size := style.Size(style.SizeNameSecondaryText)
	lines := []fyne.CanvasObject{designText(lang.L("Configuration"), style.Size("text"), "foreground", false)}
	for _, l := range wrapWords(lang.L("Favorites and settings as a file. Passwords stay in the keychain."), size, width) {
		lines = append(lines, designText(l, size, style.ColorNameTextSecondary, false))
	}
	label := container.New(layout.NewCustomPaddedVBoxLayout(1), lines...)
	return container.New(layout.NewCustomPaddedLayout(colorBlindPadV, colorBlindPadV, rowPadH, rowPadH),
		container.New(&rowLayout{gap: 16, fill: 0}, label, buttons))
}

// settingsIconButton is the bordered 26 px button of a settings row with a symbol before the text.
func settingsIconButton(text string, icon icons.Name, tapped func()) *widgets.PrimaryButton {
	b := settingsButton(text, tapped)
	b.SetIcon(icons.Get(icon, icons.Secondary))
	return b
}

// showSettingsDialog shows the global settings on parent; Save stays disabled while the
// interval is invalid. checkNow runs the manual update check of the row "Check for updates";
// withConfig turns on Export and Import.
func showSettingsDialog(parent fyne.Window, s *Settings, checkNow func(report func(updateCheckResult)), opts ...settingsOption) {
	a := fyne.CurrentApp()
	f := newSettingsForm(s)
	showDesignDialog(parent, dialogSpec{width: settingsWidth, height: settingsHeight, padTop: 20, padRight: 22 - scrollInset,
		padBottom: 18, padL: 22 - scrollInset, gap: 16, flex: 1},
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

			// The sections run top to bottom in this order; Tab follows the same order,
			// because Fyne moves the focus through the widget tree.
			c := &settingsDialog{parent: parent, app: a, s: s, f: f, d: d, checkNow: checkNow}
			for _, o := range opts {
				o(c)
			}
			var sections []fyne.CanvasObject
			for _, build := range []func() fyne.CanvasObject{
				c.generalSection, c.updatesSection, c.devicesSection, c.remoteScreenSection, c.dataSection,
			} {
				sections = append(sections, build())
			}
			title := designText(lang.L("Settings"), style.Size("subHeadingText"), "foreground", true)
			buttons := container.New(&rowLayout{gap: 8, fill: 0}, spacer(), cancel, save)
			// The sections scroll; title, footer and buttons stay in place.
			body := scrollBody(dialogSpec{gap: settingsSectionGap, flex: -1}, sections...)
			c.relayout = func() { body.Content.Refresh(); body.Refresh() }
			// The scroll area keeps scrollInset free at its sides; the fixed parts align with it.
			pad := func(o fyne.CanvasObject) fyne.CanvasObject {
				return container.New(layout.NewCustomPaddedLayout(0, 0, scrollInset, scrollInset), o)
			}
			return []fyne.CanvasObject{pad(title), body, pad(settingsFooter(parent, a)), pad(buttons)}
		})
}
