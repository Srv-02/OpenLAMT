package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/configfile"
	"github.com/Srv-02/openlamt/internal/favorites"
)

const (
	pinOne = "abababababababababababababababababababababababababababababababab"
	pinTwo = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
)

// configHarness is the start window with two favorites (one with a password and a pin), the
// settings dialog open with Export and Import on, and file dialogs that answer with a path.
type configHarness struct {
	*startHarness
	dir    string
	save   string // what the save dialog answers; "" cancels
	open   string
	opened *[]string // URLs the app opened
}

func newConfigHarness(t *testing.T) *configHarness {
	t.Helper()
	h := &configHarness{startHarness: newStart(t), dir: t.TempDir()}
	h.save = filepath.Join(h.dir, "out.json")
	addFavorites(t, h.startHarness,
		favorites.Favorite{Name: "Lab", Host: "lab.example.com", Username: "admin", UseTLS: true, Quality: favorites.QualityLow},
		favorites.Favorite{Host: "192.0.2.25", Username: "admin"})
	h.keys.Set("lab.example.com", "admin", "hunter2-secret")
	h.keys.SetFingerprint("lab.example.com", "admin", pinOne)
	prevSave, prevOpen := pickSaveFile, pickOpenFile
	pickSaveFile = func(_ fyne.Window, _, name string, chosen func(string)) {
		if !strings.HasPrefix(name, "openlamt-config-") || !strings.HasSuffix(name, ".json") {
			t.Errorf("suggested name %q", name)
		}
		if h.save != "" {
			chosen(h.save)
		}
	}
	pickOpenFile = func(_ fyne.Window, _ string, chosen func(string)) {
		if h.open != "" {
			chosen(h.open)
		}
	}
	t.Cleanup(func() { pickSaveFile, pickOpenFile = prevSave, prevOpen })
	h.opened = urlsOpened(t)
	return h
}

// openSettings shows the settings dialog with Export and Import turned on.
func (h *configHarness) openSettings(t *testing.T) *configTransfer {
	t.Helper()
	tr := newConfigTransfer(h.s.win, &h.s.settings, h.s.mgr, h.s.keys, h.s.showImported)
	tr.run = func(f func()) { f() }
	tr.now = func() time.Time { return time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC) }
	showSettingsDialog(h.s.win, &h.s.settings, nil, withConfig(tr))
	return tr
}

func (h *configHarness) top() fyne.CanvasObject { return h.s.win.Canvas().Overlays().Top() }

func TestSettingsDataSectionHasConfigurationRow(t *testing.T) {
	h := newStart(t)
	showSettingsDialog(h.s.win, &h.s.settings, nil)
	top := h.s.win.Canvas().Overlays().Top()
	for _, text := range []string{"Configuration", "Favorites and settings as a file. Passwords stay in the keychain.", "Export…", "Import…"} {
		if !inside(top, text) && !hasTextPrefix(top, text[:min(len(text), 20)]) {
			t.Errorf("%q missing in the settings dialog", text)
		}
	}
	// Without a transfer the buttons stay off.
	if b := overlayPrimary(t, h.s.win, "Export…"); !b.Disabled() {
		t.Error("Export is on without a transfer")
	}
}

func TestExportWritesFileWithoutPasswordsAndShowsResult(t *testing.T) {
	h := newConfigHarness(t)
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Export…"))
	top := h.top()
	for _, want := range []string{"Export Config", "Favorites (2)", "Settings", "Pinned certificate fingerprints"} {
		if !hasText(top, want) && !inside(top, want) {
			t.Fatalf("export dialog lacks %q", want)
		}
	}
	for _, c := range []string{"Favorites (2)", "Settings", "Pinned certificate fingerprints"} {
		if !findCheck(t, top, c).Checked {
			t.Errorf("%q is not ticked by default", c)
		}
	}
	test.Tap(overlayButton(t, h.s.win, "Export…"))

	b, err := os.ReadFile(h.save)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "hunter2-secret") || strings.Contains(text, "password") {
		t.Errorf("a password is in the file:\n%s", text)
	}
	for _, want := range []string{`"host": "lab.example.com"`, `"fingerprint": "` + pinOne + `"`, `"kvm_quality": "low"`, `"poll_interval_seconds": 10`, `"exported": "2026-11-23T09:00:00Z"`} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %s:\n%s", want, text)
		}
	}
	top = h.top()
	if !hasText(top, "Config exported") || !hasTextPrefix(top, "2 favorites and the settings were saved to out.json") {
		t.Fatalf("result dialog: %v", textsBelow(top))
	}
	test.Tap(overlayButton(t, h.s.win, "Show in Folder"))
	if got := *h.opened; len(got) != 1 || !strings.HasPrefix(got[0], "file://") || !strings.Contains(got[0], filepath.ToSlash(h.dir)) {
		t.Errorf("Show in Folder opened %v, want the folder %s", got, h.dir)
	}
}

func TestExportWithoutPinsAndOnlyFavorites(t *testing.T) {
	h := newConfigHarness(t)
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Export…"))
	test.Tap(findCheck(t, h.top(), "Pinned certificate fingerprints"))
	test.Tap(findCheck(t, h.top(), "Settings"))
	test.Tap(overlayButton(t, h.s.win, "Export…"))
	b, _ := os.ReadFile(h.save)
	if strings.Contains(string(b), "fingerprint") || strings.Contains(string(b), `"settings"`) || !strings.Contains(string(b), "lab.example.com") {
		t.Errorf("file:\n%s", b)
	}
	if !hasTextPrefix(h.top(), "Saved to out.json: 2 favorites.") {
		t.Errorf("result: %v", textsBelow(h.top()))
	}
}

func TestExportNeedsAChoiceAndCancelWritesNothing(t *testing.T) {
	h := newConfigHarness(t)
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Export…"))
	test.Tap(findCheck(t, h.top(), "Favorites (2)"))
	if !findCheck(t, h.top(), "Pinned certificate fingerprints").Disabled() {
		t.Error("pins stay on without favorites")
	}
	test.Tap(findCheck(t, h.top(), "Settings"))
	if !overlayButton(t, h.s.win, "Export…").Disabled() {
		t.Error("Export is on with nothing chosen")
	}
	test.Tap(findCheck(t, h.top(), "Settings"))
	h.save = "" // the user cancels the save dialog
	test.Tap(overlayButton(t, h.s.win, "Export…"))
	if _, err := os.Stat(filepath.Join(h.dir, "out.json")); err == nil {
		t.Error("a file was written although the save dialog was cancelled")
	}
}

// madeFile writes a configuration file made by the export of another computer.
func (h *configHarness) madeFile(t *testing.T, f configfile.File) string {
	t.Helper()
	path := filepath.Join(h.dir, "in.json")
	if err := configfile.Write(path, f); err != nil {
		t.Fatal(err)
	}
	h.open = path
	return path
}

func incoming(t *testing.T) configfile.File {
	t.Helper()
	poll, look, quality := 45, AppearanceLight, favorites.QualityLow
	return configfile.File{Format: configfile.Format, Version: configfile.Version, App: "1.2.0",
		Favorites: []configfile.Favorite{
			{Favorite: favorites.Favorite{Name: "Other name", Host: "lab.example.com", Username: "admin"}, Fingerprint: pinTwo},
			{Favorite: favorites.Favorite{Name: "NAS", Host: "nas.example.com", Username: "admin", UseTLS: true}, Fingerprint: pinTwo},
			{Favorite: favorites.Favorite{Host: "192.0.2.77", Username: "admin"}},
		},
		Settings: &configfile.Settings{PollIntervalSeconds: &poll, Appearance: &look, KVMQuality: &quality}}
}

// Import only merges: new hosts are added with their pin, the existing host is skipped, the
// settings stay as they are unless the box is ticked, and the first new favorite is selected.
func TestImportMergesFavoritesAndSelectsTheFirstNew(t *testing.T) {
	h := newConfigHarness(t)
	h.madeFile(t, incoming(t))
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Import…"))

	top := h.top()
	for _, want := range []string{"Import Config", "in.json · OpenLAMT 1.2.0", "2 new · 1 skipped (Lab is already here)", "Pinned for 1 favorites",
		"Not included. OpenLAMT asks on first connect."} {
		if !hasText(top, want) {
			t.Errorf("preview lacks %q: %v", want, textsBelow(top))
		}
	}
	if findCheck(t, top, "Also apply settings").Checked {
		t.Error("settings are applied by default")
	}
	test.Tap(overlayButton(t, h.s.win, "Import"))

	list, _ := h.s.mgr.List()
	if len(list) != 4 || list[0].Name != "Lab" || list[2].Host != "nas.example.com" || !list[2].UseTLS || list[3].Host != "192.0.2.77" {
		t.Fatalf("favorites: %+v", list)
	}
	if fp, _ := h.keys.GetFingerprint("nas.example.com", "admin"); fp != pinTwo {
		t.Errorf("pin of the new favorite %q", fp)
	}
	if fp, _ := h.keys.GetFingerprint("lab.example.com", "admin"); fp != pinOne {
		t.Errorf("pin of the existing favorite changed to %q", fp)
	}
	if _, err := h.keys.Get("nas.example.com", "admin"); err == nil {
		t.Error("an imported favorite has a password")
	}
	if h.s.selKey != deviceKey("nas.example.com", "admin") {
		t.Errorf("selected %q", h.s.selKey)
	}
	if h.s.settings.PollInterval() != DefaultPollInterval || h.s.settings.Appearance() != AppearanceSystem {
		t.Error("settings changed without the check box")
	}
}

func TestImportAppliesSettingsOnRequestAndTheFormFollows(t *testing.T) {
	h := newConfigHarness(t)
	h.madeFile(t, incoming(t))
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Import…"))
	test.Tap(findCheck(t, h.top(), "Also apply settings"))
	test.Tap(overlayButton(t, h.s.win, "Import"))

	s := h.s.settings
	if s.PollInterval() != 45*time.Second || s.Appearance() != AppearanceLight {
		t.Errorf("settings: poll %v look %q", s.PollInterval(), s.Appearance())
	}
	if got := s.KVMQuality(); favoriteQuality(got) != favorites.QualityLow {
		t.Errorf("quality %v", got)
	}
	// The open dialog shows the imported values, so Save does not write the old ones back.
	var shown bool
	walk(h.s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if e, ok := o.(*escEntry); ok && e.Text == "45" {
			shown = true
		}
	})
	if !shown {
		t.Errorf("the settings dialog still shows the old poll interval")
	}
}

func TestImportOfOnlyKnownHostsChangesNothing(t *testing.T) {
	h := newConfigHarness(t)
	f := incoming(t)
	f.Favorites, f.Settings = f.Favorites[:1], nil
	h.madeFile(t, f)
	h.openSettings(t)
	test.Tap(overlayPrimary(t, h.s.win, "Import…"))
	if !overlayButton(t, h.s.win, "Import").Disabled() || !hasText(h.top(), "0 new · 1 skipped (Lab is already here)") {
		t.Fatalf("preview: %v", textsBelow(h.top()))
	}
}

// A file that is not ours, damaged or newer is explained and changes nothing.
func TestImportRejectsBadFilesWithAClearError(t *testing.T) {
	cases := map[string]struct{ content, want string }{
		"other json":  {`{"hello": "world"}`, "“in.json” is not an OpenLAMT configuration. Choose a file made with Export Config."},
		"not json":    {"just some notes", "“in.json” is not an OpenLAMT configuration. Choose a file made with Export Config."},
		"damaged":     {"{\n  \"format\": \"openlamt-config\",\n  \"version\": 1,\n  \"favorites\": [\n    {\"host\": \"a.example.com\" \"username\": \"admin\"}\n  ]\n}", "The file is damaged: line 5."},
		"no host":     {`{"format": "openlamt-config", "version": 1, "favorites": [{"username": "admin"}]}`, "The file is damaged: favorite 1 has no host."},
		"newer":       {`{"format": "openlamt-config", "version": 2, "app": "2.0.0"}`, "This file was made by OpenLAMT 2.0.0. Update OpenLAMT first."},
		"bad setting": {`{"format": "openlamt-config", "version": 1, "settings": {"poll_interval_seconds": 0}}`, "The file is damaged: the setting poll_interval_seconds has an invalid value."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newConfigHarness(t)
			h.open = filepath.Join(h.dir, "in.json")
			if err := os.WriteFile(h.open, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			h.openSettings(t)
			test.Tap(overlayPrimary(t, h.s.win, "Import…"))
			top := h.top()
			if !hasText(top, "Cannot import this file") || !hasTextPrefix(top, c.want) {
				t.Fatalf("dialog: %v", textsBelow(top))
			}
			list, _ := h.s.mgr.List()
			if len(list) != 2 {
				t.Errorf("favorites changed: %+v", list)
			}
			test.Tap(overlayButton(t, h.s.win, "OK"))
		})
	}
}

func TestSkippedText(t *testing.T) {
	fav := func(n string) configfile.Favorite {
		return configfile.Favorite{Favorite: favorites.Favorite{Name: n, Host: n}}
	}
	for _, c := range []struct {
		skipped []string
		want    string
	}{
		{nil, "2 new"},
		{[]string{"a"}, "2 new · 1 skipped (a is already here)"},
		{[]string{"a", "b"}, "2 new · 2 skipped (already here: a, b)"},
		{[]string{"a", "b", "c", "d"}, "2 new · 4 skipped (already here: a, b, c…)"},
	} {
		p := configfile.Preview{New: []configfile.Favorite{fav("x"), fav("y")}}
		for _, s := range c.skipped {
			p.Skipped = append(p.Skipped, fav(s).Favorite)
		}
		if got := skippedText(p); got != c.want {
			t.Errorf("%v: %q, want %q", c.skipped, got, c.want)
		}
	}
}

// Snapshot and ApplySnapshot round trip; a value out of range is refused before anything is applied.
func TestSettingsSnapshotRoundTripAndValidation(t *testing.T) {
	h := newStart(t)
	s := h.s.settings
	s.SetPollInterval(30 * time.Second)
	s.SetKVMIdleTimeout(time.Hour)
	s.SetKVMAutoConnect(true)
	s.SetAppearance(AppearanceDark)
	s.SetColorBlind(true)
	s.SetUpdateCheck(false)
	snap := s.Snapshot()
	if err := validateSettings(snap); err != nil {
		t.Fatal(err)
	}
	h2 := newStart(t)
	h2.s.settings.ApplySnapshot(snap)
	o := h2.s.settings
	if o.PollInterval() != 30*time.Second || o.KVMIdleTimeout() != time.Hour || !o.KVMAutoConnect() || o.Appearance() != AppearanceDark || !o.ColorBlind() || o.UpdateCheck() {
		t.Errorf("round trip lost a value: %+v", o.Snapshot())
	}
	zero, big, odd, lang2, look, q := 0, 9999, 7, "xx", "neon", "ultra"
	for name, bad := range map[string]configfile.Settings{
		"poll small": {PollIntervalSeconds: &zero}, "poll big": {PollIntervalSeconds: &big}, "idle": {KVMIdleMinutes: &odd},
		"language": {Language: &lang2}, "appearance": {Appearance: &look}, "quality": {KVMQuality: &q}, "layout": {TypeLayout: &lang2},
	} {
		if validateSettings(bad) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
