package ui

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// walk calls fn for o and everything below it (containers and widget renderers).
func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	switch v := o.(type) {
	case *fyne.Container:
		for _, c := range v.Objects {
			walk(c, fn)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			walk(c, fn)
		}
	}
}

// overlayButton returns the button labelled text in the top overlay (a dialog).
func overlayButton(t *testing.T, w fyne.Window, text string) *widget.Button {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("no dialog shown")
	}
	var found *widget.Button
	walk(top, func(o fyne.CanvasObject) {
		switch b := o.(type) {
		case *widget.Button:
			if b.Text == text {
				found = b
			}
		case *escButton: // every button of a panel dialog
			if b.Text == text {
				found = &b.Button
			}
		}
	})
	if found == nil {
		t.Fatalf("no button %q in the dialog", text)
	}
	return found
}

func overlayCheck(t *testing.T, w fyne.Window) *widget.Check {
	t.Helper()
	var found *widget.Check
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch c := o.(type) {
		case *widget.Check:
			found = c
		case *dialogCheck:
			found = &c.Check
		}
	})
	if found == nil {
		t.Fatal("no check in the dialog")
	}
	return found
}

func addFavorites(t *testing.T, h *startHarness, favs ...favorites.Favorite) {
	t.Helper()
	for _, f := range favs {
		if err := h.s.mgr.Upsert(f); err != nil {
			t.Fatal(err)
		}
	}
	h.reload(t)
}

func rowNames(s *StartWindow) []string {
	var out []string
	for _, o := range s.list.Objects {
		if r, ok := o.(*widgets.SidebarRow); ok {
			out = append(out, r.Name())
		}
	}
	return out
}

func TestSearchFiltersByNameAndHostAndShowsEmptyStates(t *testing.T) {
	h := newStart(t)
	s := h.s
	if len(rowNames(s)) != 0 || len(s.list.Objects) != 1 || !hasText(s.list.Objects[0], "No favorites yet") {
		t.Fatal("no empty state without favorites")
	}
	addFavorites(t, h,
		favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"},
		favorites.Favorite{Name: "Keller", Host: "192.0.2.10", Username: "admin"},
		favorites.Favorite{Host: "lab.example.com", Username: "admin"})
	test.Type(s.search, "BÜ")
	if got := rowNames(s); len(got) != 1 || got[0] != "Büro" {
		t.Fatalf("name search: %v", got)
	}
	s.search.SetText("192.0.2")
	if got := rowNames(s); len(got) != 1 || got[0] != "Keller" {
		t.Fatalf("host search: %v", got)
	}
	s.search.SetText("example.com")
	if got := rowNames(s); len(got) != 2 {
		t.Fatalf("host search: %v", got)
	}
	s.search.SetText("nothing")
	if len(rowNames(s)) != 0 || !hasText(s.list.Objects[0], "No matches") {
		t.Fatal("no empty state for the search")
	}
	s.search.SetText("")
	if len(rowNames(s)) != 3 {
		t.Fatal("clearing the search did not show all")
	}
}

func hasText(o fyne.CanvasObject, text string) bool {
	found := false
	walk(o, func(c fyne.CanvasObject) {
		switch v := c.(type) {
		case *widget.Label:
			found = found || v.Text == text
		case *canvas.Text:
			found = found || v.Text == text
		}
	})
	return found
}

func TestDuplicateCreatesANewFavoriteOnly(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true, Layout: "us", Quality: favorites.QualityLow})
	h.keys.Set("device.example.com", "admin", "secret-1")
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.dupBtn)
	if s.mode != modeNew || s.host.Text != "device.example.com" || s.name.Text != "Büro copy" || s.user.Text != "admin" ||
		!s.useTLS.Checked() || s.password.Text != "" || s.selKey != "" {
		t.Fatalf("form %q %q %q", s.host.Text, s.name.Text, s.password.Text)
	}
	// A copy offers Cancel and Save only; the title mirrors the name.
	if s.connectBtn.Visible() || s.saveBtn.Visible() || !s.editSaveBtn.Visible() || !s.editCancelBtn.Visible() || s.title.Text != "Büro copy" {
		t.Fatalf("buttons connect=%v save=%v title=%q", s.connectBtn.Visible(), s.saveBtn.Visible(), s.title.Text)
	}
	// Same host and user: the original is not replaced.
	test.Tap(s.editSaveBtn)
	if err := <-h.errs; !errors.Is(err, favorites.ErrExists) {
		t.Fatalf("err = %v", err)
	}
	h.waitReload(t)
	if len(s.items) != 1 || s.items[0].Name != "Büro" || s.mode != modeNew {
		t.Fatalf("%+v", s.items)
	}
	s.user.SetText("operator")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if len(s.items) != 2 {
		t.Fatalf("%+v", s.items)
	}
	dup := s.items[1]
	if dup.Name != "Büro copy" || dup.Username != "operator" || dup.Layout != "us" || dup.Quality != favorites.QualityLow || !dup.UseTLS {
		t.Fatalf("%+v", dup)
	}
	if s.selKey != deviceKey("device.example.com", "operator") {
		t.Fatal("duplicate not selected")
	}
	if _, err := h.keys.Get("device.example.com", "operator"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("password copied")
	}
}

func TestDeleteAsksAndCanKeepThePassword(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h,
		favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"},
		favorites.Favorite{Name: "Keller", Host: "other.example.com", Username: "admin"})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.keys.Set("other.example.com", "admin", "secret-2")

	// Cancel keeps everything.
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.delBtn)
	if !hasText(s.win.Canvas().Overlays().Top(), "Delete Büro?") {
		t.Fatal("no question")
	}
	test.Tap(overlayButton(t, s.win, lang.L("Cancel")))
	if len(s.items) != 2 || len(h.reloaded) != 0 {
		t.Fatal("cancel deleted")
	}
	// Without the check the password stays in the keychain.
	test.Tap(s.delBtn)
	check := overlayCheck(t, s.win)
	if !check.Checked || check.Text != "Also delete the saved password from the keychain" {
		t.Fatal("check not on by default")
	}
	test.Tap(check)
	test.Tap(overlayButton(t, s.win, "Delete"))
	h.waitReload(t)
	if len(s.items) != 1 || s.mode != modeNew {
		t.Fatalf("%+v", s.items)
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Fatal("password deleted")
	}
	// With the check it goes too.
	test.Tap(s.rows[deviceKey("other.example.com", "admin")])
	test.Tap(s.delBtn)
	test.Tap(overlayButton(t, s.win, "Delete"))
	h.waitReload(t)
	if _, err := h.keys.Get("other.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) || len(s.items) != 0 {
		t.Fatal("password kept")
	}
}

func TestEditAdvancedAndPassword(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if s.values["layout"].Text != "Default" || s.values["quality"].Text != "Default" || !s.advanced.Visible() {
		t.Fatal("advanced not shown read only")
	}
	test.Tap(s.editBtn)
	// The password is never loaded into the field.
	if s.password.Text != "" || s.password.PlaceHolder != "Saved in keychain" || !s.savePassword.Checked() || s.resetFP.Checked() {
		t.Fatalf("password field %q placeholder %q", s.password.Text, s.password.PlaceHolder)
	}
	s.layoutSel.SetSelected("German")
	s.qualitySel.SetSelectedIndex(2)
	test.Tap(s.resetFP)
	s.password.SetText("secret-new")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	f := s.items[0]
	if f.Layout != "de" || f.QualityChoice() != favorites.QualityLow || s.values["layout"].Text != "German" || s.values["quality"].Text != "Low" {
		t.Fatalf("%+v", f)
	}
	if _, err := h.keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("fingerprint not reset")
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "secret-new" {
		t.Fatalf("password %q", pw)
	}
	// An empty field keeps the password; turning "Save password" off deletes it.
	test.Tap(s.editBtn)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "secret-new" {
		t.Fatalf("password %q", pw)
	}
	test.Tap(s.editBtn)
	s.layoutSel.SetSelectedIndex(0)
	test.Tap(s.savePassword)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if _, err := h.keys.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("password kept")
	}
	if s.items[0].Layout != "" || s.values["password"].Text != "Not saved" || s.items[0].QualityChoice() != favorites.QualityLow {
		t.Fatalf("%+v", s.items[0])
	}
	// Edit toggles: a second tap leaves edit mode without saving.
	test.Tap(s.editBtn)
	test.Tap(s.editBtn)
	if s.mode != modeView || len(h.reloaded) != 0 {
		t.Fatal("edit toggle")
	}
}

func TestProbeResultsShowInSidebarAndHeader(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h,
		favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true},
		favorites.Favorite{Name: "Keller", Host: "other.example.com", Username: "admin"})
	k1, k2 := deviceKey("device.example.com", "admin"), deviceKey("other.example.com", "admin")
	test.Tap(s.rows[k1])
	if hasText(s.meta, "AMT 16.1.25") || !hasText(s.meta, "TLS") {
		t.Fatal("meta before the probe")
	}
	s.applyProbe(k1, readProbe(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}))
	s.applyProbe(k2, readProbe(amt.Status{PowerState: models.PowerStateOffSoft}))
	s.applyProbe(deviceKey("gone.example.com", "admin"), readProbe(amt.Status{PowerState: models.PowerStateOn}))
	if s.rows[k1].Dot() != widgets.DotOn || s.rows[k2].Dot() != widgets.DotOff || len(s.probes) != 2 {
		t.Fatal("dots not shown")
	}
	if !hasText(s.meta, "AMT 16.1.25") || !hasText(s.meta, "ACM") || !hasText(s.meta, "TLS") {
		t.Fatal("meta not filled")
	}
	test.Tap(s.rows[k2])
	if !hasText(s.meta, "Without TLS") || hasText(s.meta, "ACM") {
		t.Fatal("meta of the second favorite")
	}
	// The search rebuilds the rows with the known dots.
	s.search.SetText("keller")
	if s.rows[k2].Dot() != widgets.DotOff {
		t.Fatal("dot lost by the search")
	}
	s.applyProbe(k2, probeResult{dot: widgets.DotUnknown})
	if s.rows[k2].Dot() != widgets.DotUnknown {
		t.Fatal("neutral state not shown")
	}
}

func TestProberFollowsTheFavoritesOfTheWindow(t *testing.T) {
	h := newStart(t)
	s := h.s
	// A fresh prober wired like NewStartWindow's, reporting into a channel (the window applies
	// results on the Fyne thread; the test applies them itself).
	got := make(chan string, 8)
	s.prober = newStatusProber(h.keys, s.conn.connect, func() time.Duration { return time.Hour }, func(key string, r probeResult) { got <- key })
	t.Cleanup(func() { s.prober.Stop(); <-s.prober.Done() })
	s.prober.Start()
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.keys.SetFingerprint("device.example.com", "admin", fp)
	addFavorites(t, h, favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	select {
	case k := <-got:
		if k != deviceKey("device.example.com", "admin") {
			t.Fatalf("key %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not start a probe")
	}
}

func TestSettingsButtonOpensGlobalSettings(t *testing.T) {
	h := newStart(t)
	s := h.s
	opened := 0
	s.showSettings = func() { opened++ }
	test.Tap(s.settingsBtn)
	if opened != 1 {
		t.Fatal("settings not opened")
	}
}

func TestSettingsDialogStoresGlobalSettings(t *testing.T) {
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings)
	var entry *escEntry
	var drops []*widgets.Dropdown
	var switches []*widgets.Switch
	walk(s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *escEntry:
			entry = v
		case *widgets.Dropdown:
			drops = append(drops, v)
		case *widgets.Switch:
			switches = append(switches, v)
		}
	})
	// Rows: language, appearance, color blind, updates, interval, quality, auto-connect, idle, layout.
	if len(switches) != 3 || switches[0].Checked() || !switches[1].Checked() {
		t.Fatalf("want the color blind (off), the update (on) and the KVM auto-connect switch, got %d", len(switches))
	}
	sw := switches[2]
	if entry == nil || len(drops) != 5 || drops[0].Selected != "System" || drops[1].Selected != "System" || drops[2].Selected != "High" || entry.Text != "10" || drops[3].Selected != "30 minutes" || sw.Checked() {
		t.Fatalf("form: entry=%v dropdowns=%d switch=%v", entry, len(drops), sw)
	}
	entry.SetText("25")
	drops[4].SetSelected("English (US)")
	drops[3].SetSelected("Never")
	drops[2].SetSelected("Low")
	sw.SetChecked(true)
	_, _, buttons := dialogTexts(s.win)
	test.Tap(buttons[lang.L("Save")])
	if s.settings.PollInterval() != 25*time.Second || s.settings.TypeLayout() != "us" || s.settings.KVMIdleTimeout() != 0 || !s.settings.KVMAutoConnect() || s.settings.KVMQuality() != kvm.QualityLow {
		t.Fatalf("%v %q %v %v", s.settings.PollInterval(), s.settings.TypeLayout(), s.settings.KVMIdleTimeout(), s.settings.KVMAutoConnect())
	}
	if s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("Save did not close the dialog")
	}
	// An invalid interval is refused.
	f := newSettingsForm(&s.settings)
	f.interval.SetText("0")
	if err := f.apply(&s.settings); !errors.Is(err, ErrInterval) || s.settings.PollInterval() != 25*time.Second {
		t.Fatalf("err = %v", err)
	}
}

// TestManagerShots renders the manager with the software painter into $OPENLAMT_SHOTS for
// review against the design. Skipped without the variable. Only made-up data.
func TestManagerShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newStart(t)
	s := h.s
	s.app.Settings().SetTheme(style.Theme{})
	shot := func(name string) {
		t.Helper()
		c := software.NewCanvas()
		c.SetPadded(false)
		c.SetScale(shotScale())
		c.SetContent(s.win.Content())
		c.Resize(fyne.NewSize(1000, 660))
		if o := s.win.Canvas().Overlays().Top(); o != nil {
			c.Overlays().Add(o)
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, c.Capture()); err != nil {
			t.Fatal(err)
		}
	}
	shot("manager-empty.png")
	addFavorites(t, h,
		favorites.Favorite{Name: "Lab-Server", Host: "192.0.2.10", Username: "admin", UseTLS: true},
		favorites.Favorite{Name: "Build-Host", Host: "build.example.com", Username: "operator"},
		favorites.Favorite{Host: "192.0.2.25", Username: "admin", UseTLS: true})
	h.keys.Set("192.0.2.10", "admin", "secret-1")
	k := deviceKey("192.0.2.10", "admin")
	s.applyProbe(k, readProbe(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}))
	s.applyProbe(deviceKey("build.example.com", "operator"), readProbe(amt.Status{PowerState: models.PowerStateOffSoft}))
	test.Tap(s.rows[k])
	shot("manager-view.png")
	test.Tap(s.editBtn)
	shot("manager-edit.png")
	test.Tap(s.newConnBtn)
	s.host.SetText("192.0.2.30")
	shot("manager-new.png")
	s.busy["x"] = "192.0.2.30"
	s.refreshActions()
	shot("manager-connecting.png")
	delete(s.busy, "x")
	s.refreshActions()
	s.search.SetText("nothing")
	shot("manager-no-match.png")
	s.search.SetText("")
	test.Tap(s.rows[k])
	test.Tap(s.delBtn)
	shot("manager-delete.png")
	test.Tap(overlayButton(t, s.win, lang.L("Cancel")))
	showSettingsDialog(s.win, &s.settings)
	shot("manager-settings.png")
}

// At the default size (1000×660) the detail pane shows header and every card without
// scrolling, also in edit mode with the four-row Advanced card; the spacing shrinks for that
// (detailLayout). A lower window scrolls. Name and metadata line are 4 px apart (ManagerA.html).
func TestDetailFitsDefaultWindow(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.app.Settings().SetTheme(style.Theme{})
	addFavorites(t, h, favorites.Favorite{Name: "Lab-Server", Host: "192.0.2.10", Username: "admin", UseTLS: true})
	test.Tap(s.rows[deviceKey("192.0.2.10", "admin")])
	s.win.Resize(fyne.NewSize(1000, 660))
	fits := func(what string) {
		t.Helper()
		bottom := s.advanced.Position().Y + s.advanced.Size().Height
		if s.win.Padded() || s.scroller.Size().Height != 660-style.FormRowHeight || s.cards.MinSize().Height > s.scroller.Size().Height ||
			bottom > s.scroller.Size().Height || !s.card3.Visible() {
			t.Fatalf("%s: content %v, Advanced ends at %v, viewport %v", what, s.cards.MinSize(), bottom, s.scroller.Size())
		}
	}
	fits("view")
	test.Tap(s.editBtn)
	fits("edit")
	heading := s.cards.Objects[0].(*fyne.Container).Objects[1].(*fyne.Container)
	if line := heading.Objects[1]; s.title.Position().Y+s.title.Size().Height+headingGap != line.Position().Y {
		t.Fatalf("title %v+%v, metadata line %v", s.title.Position(), s.title.Size(), line.Position())
	}
	s.win.Resize(fyne.NewSize(1000, 450))
	if s.cards.MinSize().Height <= s.scroller.Size().Height {
		t.Fatal("a low window does not scroll")
	}
	s.win.Resize(fyne.NewSize(1000, 660))
	// Quick Connect (no Advanced card) has the design spacing.
	test.Tap(s.newConnBtn)
	if s.card1.Position().Y != detailPad+s.cards.Objects[0].Size().Height+detailGap {
		t.Fatalf("card at %v", s.card1.Position())
	}
}
