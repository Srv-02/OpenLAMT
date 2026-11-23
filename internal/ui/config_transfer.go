package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/configfile"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Export Config and Import Config of the settings dialog (gesamtdesign V11, V12, issue #7).
// Both dialogs open above the settings dialog. The file is readable JSON (internal/configfile);
// passwords are never in it, the pinned fingerprints are by default.

// configTransfer is what the row "Configuration" needs. The settings dialog creates one per
// opening and sets reloadForm.
type configTransfer struct {
	parent   fyne.Window
	settings *Settings
	mgr      *favorites.Manager
	keys     keychain.Store
	// onImported runs on the Fyne thread after favorites were added (the manager window shows
	// them); reloadForm shows imported settings in the open settings dialog.
	onImported func(added []favorites.Favorite)
	reloadForm func()

	now func() time.Time
	// run does work that may wait for the keychain off the Fyne thread; tests run it inline.
	run func(func())
}

func newConfigTransfer(parent fyne.Window, s *Settings, mgr *favorites.Manager, keys keychain.Store, onImported func([]favorites.Favorite)) *configTransfer {
	return &configTransfer{parent: parent, settings: s, mgr: mgr, keys: keys, onImported: onImported,
		now: time.Now, run: func(f func()) { go f() }}
}

// exportName is the suggested file name: openlamt-config-YYYY-MM-DD.json.
func (c *configTransfer) exportName() string {
	return "openlamt-config-" + c.now().Format("2006-01-02") + ".json"
}

// favoritesText is "1 favorite" or "3 favorites".
func favoritesText(n int) string {
	if n == 1 {
		return lang.L("{{.Count}} favorite", map[string]any{"Count": n})
	}
	return lang.L("{{.Count}} favorites", map[string]any{"Count": n})
}

// export is "Export…": the choices, then the save dialog, then the file.
func (c *configTransfer) export() {
	favs, err := c.mgr.List()
	if err != nil {
		showError(c.parent, err)
		return
	}
	var favCheck, setCheck, pinCheck *dialogCheck
	var p *panel
	sync := func() {
		if favCheck.Checked {
			pinCheck.Enable()
		} else {
			pinCheck.Disable()
		}
		if p != nil && p.action != nil {
			if favCheck.Checked || setCheck.Checked {
				p.action.Enable()
			} else {
				p.action.Disable()
			}
		}
	}
	p = showPanel(c.parent, panelSpec{title: lang.L("Export Config"), action: lang.L("Export…"), cancel: lang.L("Cancel"),
		onAction: func() {
			o := configfile.Options{Favorites: favCheck.Checked, Fingerprints: favCheck.Checked && pinCheck.Checked, App: appVersion(fyne.CurrentApp())}
			if setCheck.Checked {
				snap := c.settings.Snapshot()
				o.Settings = &snap
			}
			pickSaveFile(c.parent, lang.L("Export Config"), c.exportName(), func(path string) { c.save(path, favs, o) })
		},
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(lang.L("Saves favorites and settings to a JSON file. Passwords are never exported; they stay in the keychain."))
		msg.Wrapping = fyne.TextWrapWord
		favCheck = p.newCheck(lang.L("Favorites ({{.Count}})", map[string]any{"Count": len(favs)}), len(favs) > 0)
		if len(favs) == 0 {
			favCheck.Disable()
		}
		setCheck = p.newCheck(lang.L("Settings"), true)
		pinCheck = p.newCheck(lang.L("Pinned certificate fingerprints"), true)
		favCheck.OnChanged = func(bool) { sync() }
		setCheck.OnChanged = func(bool) { sync() }
		hint := secondaryNote(lang.L("The other computer then trusts these devices without asking. Turn off to confirm each certificate again on first connect."))
		return []fyne.CanvasObject{msg, favCheck, setCheck, pinCheck, hint}
	})
	sync()
}

// secondaryNote is a wrapped hint in the secondary color and the small text size.
func secondaryNote(text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.SizeName = theme.SizeNameCaptionText
	l.Importance = widget.LowImportance
	return l
}

// save writes the file on a goroutine (the pins come from the keychain) and then shows the
// result.
func (c *configTransfer) save(path string, favs []favorites.Favorite, o configfile.Options) {
	o.Now = c.now()
	c.run(func() {
		f, err := configfile.Build(favs, c.keys, o)
		if err == nil {
			err = configfile.Write(path, f)
		}
		fyne.Do(func() {
			if err != nil {
				showError(c.parent, err)
				return
			}
			c.exported(path, len(f.Favorites), f.Settings != nil)
		})
	})
}

// exported is the info dialog "Config exported".
func (c *configTransfer) exported(path string, favs int, settings bool) {
	file := filepath.Base(path)
	var text string
	switch {
	case favs > 0 && settings:
		text = lang.L("{{.Favorites}} and the settings were saved to {{.File}}.", map[string]any{"Favorites": favoritesText(favs), "File": file})
	case favs > 0:
		text = lang.L("Saved to {{.File}}: {{.Favorites}}.", map[string]any{"Favorites": favoritesText(favs), "File": file})
	default:
		text = lang.L("The settings were saved to {{.File}}.", map[string]any{"File": file})
	}
	showPanel(c.parent, panelSpec{icon: icons.Check, tone: toneInfo, tileAbove: true,
		title: lang.L("Config exported"), action: lang.L("OK")},
		func(p *panel) []fyne.CanvasObject {
			msg := widget.NewLabel(text)
			msg.Wrapping = fyne.TextWrapWord
			p.extras = []fyne.CanvasObject{p.newButton(lang.L("Show in Folder"), func() { showInFolder(path) })}
			return []fyne.CanvasObject{msg}
		})
}

// importConfig is "Import…": the open dialog, the checks, then the preview.
func (c *configTransfer) importConfig() {
	pickOpenFile(c.parent, lang.L("Import Config"), func(path string) {
		f, err := configfile.Read(path)
		if err == nil && f.Settings != nil {
			err = validateSettings(*f.Settings)
		}
		if err != nil {
			c.cannotImport(path, err)
			return
		}
		existing, err := c.mgr.List()
		if err != nil {
			showError(c.parent, err)
			return
		}
		c.preview(path, f, configfile.Plan(f, existing))
	})
}

// importErrorText is the text of "Cannot import this file" for a file that is no configuration;
// ok is false for an error of the file system, which showError reports.
func importErrorText(path string, err error) (text string, ok bool) {
	var dmg *configfile.DamagedError
	var newer *configfile.NewerError
	switch {
	case errors.Is(err, configfile.ErrNotConfig):
		return lang.L("“{{.File}}” is not an OpenLAMT configuration. Choose a file made with Export Config.",
			map[string]any{"File": filepath.Base(path)}), true
	case errors.As(err, &newer):
		if newer.App == "" {
			return lang.L("This file was made by a newer OpenLAMT. Update OpenLAMT first."), true
		}
		return lang.L("This file was made by OpenLAMT {{.Version}}. Update OpenLAMT first.", map[string]any{"Version": newer.App}), true
	case errors.As(err, &dmg):
		return damagedText(dmg), true
	}
	return "", false
}

func damagedText(e *configfile.DamagedError) string {
	args := map[string]any{"Line": e.Line, "N": e.Item, "Setting": e.Setting}
	switch e.Reason {
	case configfile.ReasonSyntax, configfile.ReasonType:
		return lang.L("The file is damaged: line {{.Line}}.", args)
	case configfile.ReasonNoVersion:
		return lang.L("The file is damaged: it has no format version.")
	case configfile.ReasonNoHost:
		return lang.L("The file is damaged: favorite {{.N}} has no host.", args)
	case configfile.ReasonBadHost:
		return lang.L("The file is damaged: favorite {{.N}} has an invalid host.", args)
	case configfile.ReasonNoUser:
		return lang.L("The file is damaged: favorite {{.N}} has no user name.", args)
	case configfile.ReasonBadPin:
		return lang.L("The file is damaged: favorite {{.N}} has an invalid fingerprint.", args)
	}
	return lang.L("The file is damaged: the setting {{.Setting}} has an invalid value.", args)
}

func (c *configTransfer) cannotImport(path string, err error) {
	text, ok := importErrorText(path, err)
	if !ok {
		showError(c.parent, err) // the file could not be read at all
		return
	}
	showPanel(c.parent, panelSpec{icon: icons.Warning, tone: toneDanger, tileAbove: true,
		title: lang.L("Cannot import this file"), action: lang.L("OK")},
		func(p *panel) []fyne.CanvasObject {
			msg := widget.NewLabel(text)
			msg.Wrapping = fyne.TextWrapWord
			return []fyne.CanvasObject{msg}
		})
}

// skippedText is the value of the row "Favorites" of the preview, such as
// "2 new · 1 skipped (Lab-Server is already here)".
func skippedText(plan configfile.Preview) string {
	args := map[string]any{"New": len(plan.New), "Skipped": len(plan.Skipped)}
	switch n := len(plan.Skipped); {
	case n == 0:
		return lang.L("{{.New}} new", args)
	case n == 1:
		args["Name"] = plan.Skipped[0].DisplayName()
		return lang.L("{{.New}} new · 1 skipped ({{.Name}} is already here)", args)
	default:
		var names []string
		for _, f := range plan.Skipped[:min(n, 3)] {
			names = append(names, f.DisplayName())
		}
		list := strings.Join(names, ", ")
		if n > 3 {
			list += "…"
		}
		args["Names"] = list
		return lang.L("{{.New}} new · {{.Skipped}} skipped (already here: {{.Names}})", args)
	}
}

// infoRow is a row of the preview card: a small label above its value.
func infoRow(label, value string) fyne.CanvasObject {
	l := designText(label, style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false)
	v := widget.NewLabel(value)
	v.Wrapping = fyne.TextWrapWord
	return container.New(layout.NewCustomPaddedLayout(6, 2, rowPadH, rowPadH),
		container.New(layout.NewCustomPaddedVBoxLayout(0), container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), l), v))
}

// preview is the question "Import Config": what the file holds and what would happen.
func (c *configTransfer) preview(path string, f configfile.File, plan configfile.Preview) {
	var applyCheck *dialogCheck
	var p *panel
	sync := func() {
		if p == nil || p.action == nil {
			return
		}
		if len(plan.New) > 0 || (applyCheck != nil && applyCheck.Checked) {
			p.action.Enable()
		} else {
			p.action.Disable()
		}
	}
	p = showPanel(c.parent, panelSpec{icon: icons.Download, tone: toneInfo,
		title: lang.L("Import Config"), action: lang.L("Import"), cancel: lang.L("Cancel"),
		onAction: func() { c.apply(f, plan, applyCheck != nil && applyCheck.Checked) },
	}, func(p *panel) []fyne.CanvasObject {
		name := filepath.Base(path)
		if f.App != "" {
			name += " · OpenLAMT " + f.App
		}
		file := designText(name, style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false)
		rows := []fyne.CanvasObject{infoRow(lang.L("Favorites"), skippedText(plan))}
		if plan.Pins > 0 {
			rows = append(rows, infoRow(lang.L("Fingerprints"), lang.L("Pinned for {{.Count}} favorites", map[string]any{"Count": plan.Pins})))
		}
		rows = append(rows, infoRow(lang.L("Passwords"), lang.L("Not included. OpenLAMT asks on first connect.")))
		card := widgets.NewFormCard(rows...)
		card.Background = style.ColorNameDialogCard
		body := []fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), file), card}
		if f.Settings != nil {
			applyCheck = p.newCheck(lang.L("Also apply settings"), false)
			applyCheck.OnChanged = func(bool) { sync() }
			body = append(body, applyCheck, secondaryNote(lang.L("Language, appearance, KVM and updates.")))
		}
		return body
	})
	sync()
}

// apply merges the favorites (and, if asked, the settings) of f. Nothing is replaced.
func (c *configTransfer) apply(f configfile.File, plan configfile.Preview, withSettings bool) {
	c.run(func() {
		added, _, conflicts, err := configfile.Import(c.mgr, f)
		fyne.Do(func() {
			if withSettings && f.Settings != nil {
				c.applySettings(*f.Settings)
			}
			if len(added) > 0 && c.onImported != nil {
				c.onImported(added)
			}
			if len(conflicts) > 0 {
				c.pinConflicts(conflicts)
			}
			if err != nil {
				showError(c.parent, err)
			}
		})
	})
}

// pinConflicts tells that imported pins were not stored because the keychain already trusts
// another certificate for the device.
func (c *configTransfer) pinConflicts(conflicts []favorites.Favorite) {
	var names []string
	for _, f := range conflicts[:min(len(conflicts), 5)] {
		names = append(names, f.DisplayName())
	}
	list := strings.Join(names, ", ")
	if len(conflicts) > 5 {
		list += "…"
	}
	showMessage(c.parent, lang.L("Import Config"),
		lang.L("OpenLAMT kept the trusted certificate for: {{.Names}}. The file holds a different one.", map[string]any{"Names": list}), "")
}

// applySettings stores the imported settings and lets the app and the open dialog follow.
func (c *configTransfer) applySettings(in configfile.Settings) {
	s := c.settings
	language, look, blind := s.Language(), s.Appearance(), s.ColorBlind()
	s.ApplySnapshot(in)
	a := fyne.CurrentApp()
	if s.Appearance() != look || s.ColorBlind() != blind {
		applyAppearance(a)
	}
	if c.reloadForm != nil {
		c.reloadForm()
	}
	if s.Language() != language {
		showMessage(c.parent, lang.L("Language"), lang.L("The language changes the next time OpenLAMT starts."), "")
	}
}
