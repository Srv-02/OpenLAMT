package ui

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/widgets"
	"github.com/Srv-02/openlamt/internal/update"
)

// One-click update (issue #61): the dialog, each platform's handover with fakes, every
// fallback, and the quit through the quit path of the main window.

// oneClick is a start window of 0.1.0 that found v0.2.0, with the quit recorded instead of
// done, and an installer whose system calls are fakes that record into events.
type oneClick struct {
	q        *quitHarness
	g        *fakeGitHub
	dir      string // Downloads
	events   []string
	openErr  error
	elevErr  error
	writable bool
	app, exe string   // the "running" Windows exe
	private  []string // private folders the installer made
	opened   []string // full paths handed to the opener
}

// before runs ahead of the check (a different package).
func newOneClick(t *testing.T, goos string, before ...func(o *oneClick)) *oneClick {
	t.Helper()
	withBuildVersion(t, "0.1.0")
	o := &oneClick{g: newFakeGitHub(t, "v0.2.0"), writable: true}
	o.dir, _ = downloadsDir()
	o.q = newQuitHarness(t)
	o.q.h.s.quitApp = func() { o.q.quits++; o.events = append(o.events, "quit") }
	updateGOOS = goos
	switch goos {
	case "darwin":
		updateGOARCH = "arm64"
		o.g.setPackage("macos-arm64.dmg", []byte("dmg"))
	case "windows":
		o.g.setPackage("windows-x64.zip", exeZip(t, []byte("new exe")))
		o.app = t.TempDir()
		o.exe = filepath.Join(o.app, "OpenLAMT.exe")
		if err := os.WriteFile(o.exe, []byte("old exe"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		o.g.setPackage("linux-x64.deb", []byte("deb"))
	}
	updateInstaller = update.Installer{
		Open: func(p string) error {
			o.events = append(o.events, "open "+filepath.Base(p))
			o.opened = append(o.opened, p)
			return o.openErr
		},
		Executable: func() (string, error) { return o.exe, nil },
		Writable:   func(string) bool { return o.writable },
		PrivateDir: func() (string, error) {
			d, err := os.MkdirTemp(t.TempDir(), "openlamt-update-")
			o.private = append(o.private, d)
			return d, err
		},
		Rename: os.Rename,
		Remove: os.Remove,
		Elevate: func(newExe, exe, _ string) error {
			o.events = append(o.events, "elevate")
			if o.elevErr != nil {
				return o.elevErr
			}
			b, err := os.ReadFile(newExe)
			if err != nil {
				return err
			}
			return os.WriteFile(exe, b, 0o600)
		},
		Start: func(p string) error {
			o.events = append(o.events, "start "+filepath.Base(p))
			return nil
		},
	}
	for _, f := range before {
		f(o)
	}
	o.q.h.s.enableUpdate(-1)
	o.q.h.s.updates.async = func(f func()) { f() }
	o.q.h.s.runUpdateCheck(o.q.h.s.updates)
	if !strings.Contains(overlayTexts(o.q.h.s.win), "OpenLAMT 0.2.0 is available") {
		t.Fatal("no update dialog")
	}
	return o
}

func exeZip(t *testing.T, exe []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("OpenLAMT.exe")
	if err == nil {
		_, err = w.Write(exe)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (o *oneClick) win() fyne.Window { return o.q.h.s.win }

// update taps Update and runs what the quit queued.
func (o *oneClick) update(t *testing.T) {
	t.Helper()
	test.Tap(overlayButton(t, o.win(), "Update"))
	o.q.pass()
}

func (o *oneClick) events2() string { return strings.Join(o.events, ", ") }

// dialogAll is every text of the top dialog, labels and canvas texts.
func dialogAll(w fyne.Window) string {
	texts, _, _ := dialogTexts(w)
	return strings.Join(texts, "|")
}

func TestUpdateDialogHasNoStopCheckAndAnUpdateButton(t *testing.T) {
	for goos, intro := range map[string]string{
		"darwin":  "You are using 0.1.0. Update downloads the new version, opens it in the Finder and quits OpenLAMT. Drag the new app onto the old one.",
		"windows": "You are using 0.1.0. Update downloads the new version, replaces OpenLAMT and starts it again.",
		"linux":   "You are using 0.1.0. Update downloads the new version, opens it in your software installer and quits OpenLAMT.",
	} {
		t.Run(goos, func(t *testing.T) {
			o := newOneClick(t, goos)
			walk(o.win().Canvas().Overlays().Top(), func(c fyne.CanvasObject) {
				switch c.(type) {
				case *widget.Check, *dialogCheck:
					t.Error("the update dialog has no check box; the settings switch stays")
				}
			})
			if got := texts(panelButtons(o.win().Canvas().Overlays().Top())); got != "Later|Update" {
				t.Errorf("buttons %q", got)
			}
			if all := overlayTexts(o.win()); !strings.Contains(all, intro) {
				t.Errorf("intro missing: %q", all)
			}
			if !o.q.h.s.settings.UpdateCheck() {
				t.Error("the setting must stay on")
			}
		})
	}
}

func TestUpdateLaterKeepsTheSetting(t *testing.T) {
	o := newOneClick(t, "linux")
	test.Tap(overlayButton(t, o.win(), "Later"))
	if !o.q.h.s.settings.UpdateCheck() || o.win().Canvas().Overlays().Top() != nil || len(o.events) != 0 {
		t.Fatalf("Later closes and keeps the setting; events %s", o.events2())
	}
}

func progressOf(t *testing.T, w fyne.Window) (*widgets.ProgressBar, string) {
	t.Helper()
	var bar *widgets.ProgressBar
	var hint string
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *widgets.ProgressBar:
			if v.Visible() {
				bar = v
			}
		case *canvas.Text:
			if strings.HasPrefix(v.Text, "Downloading") && v.Text != "Downloading…" { // not the button
				hint = v.Text
			}
		}
	})
	return bar, hint
}

func visibleIn(w fyne.Window, target fyne.CanvasObject) bool {
	visible := false
	var find func(o fyne.CanvasObject, shown bool)
	find = func(o fyne.CanvasObject, shown bool) {
		shown = shown && o.Visible()
		if o == target {
			visible = shown
			return
		}
		switch v := o.(type) {
		case *fyne.Container:
			for _, c := range v.Objects {
				find(c, shown)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				find(c, shown)
			}
		}
	}
	find(w.Canvas().Overlays().Top(), true)
	return visible
}

func TestUpdateShowsProgressWhileDownloading(t *testing.T) {
	o := newOneClick(t, "linux", func(o *oneClick) { o.g.setPackage("linux-x64.deb", bytes.Repeat([]byte("x"), 1_550_000)) })
	var pending func()
	o.q.h.s.updates.async = func(f func()) { pending = f }
	b := overlayButton(t, o.win(), "Update")
	bar, _ := progressOf(t, o.win())
	if bar != nil && visibleIn(o.win(), bar) {
		t.Fatal("the bar shows only while downloading")
	}
	test.Tap(b)
	if !b.Disabled() || b.Text != "Downloading…" {
		t.Fatalf("button %q disabled %v", b.Text, b.Disabled())
	}
	bar, hint := progressOf(t, o.win())
	if bar == nil || !visibleIn(o.win(), bar) || bar.Value() != 0 || hint != "Downloading… 0.0 of 1.6 MB" {
		t.Fatalf("bar %v, hint %q", bar, hint)
	}
	// The download reports into the same bar; the last report fills it.
	o.openErr = errors.New("exit status 4")
	pending()
	if bar.Value() != 1 {
		t.Errorf("bar at %v after the download", bar.Value())
	}
}

func TestUpdateLaterDuringDownloadCancels(t *testing.T) {
	o := newOneClick(t, "linux")
	var pending func()
	o.q.h.s.updates.async = func(f func()) { pending = f }
	test.Tap(overlayButton(t, o.win(), "Update"))
	test.Tap(overlayButton(t, o.win(), "Later"))
	pending()
	o.q.pass()
	if o.win().Canvas().Overlays().Top() != nil || len(o.q.h.errs) != 0 || len(o.events) != 0 || o.q.quits != 0 {
		t.Fatalf("a cancelled update stays quiet: events %s", o.events2())
	}
	if es, _ := os.ReadDir(o.dir); len(es) != 0 {
		t.Fatalf("files left: %v", es)
	}
}

func TestUpdateMacOpensTheDMGAndQuits(t *testing.T) {
	o := newOneClick(t, "darwin")
	o.update(t)
	if got := o.events2(); got != "open OpenLAMT-v0.2.0-macos-arm64.dmg, quit" {
		t.Fatalf("events %s", got)
	}
	o.handedOverPrivately(t, "dmg")
}

// handedOverPrivately checks that the opener got the verified bytes from the installer's
// private folder and that nothing landed in Downloads.
func (o *oneClick) handedOverPrivately(t *testing.T, content string) {
	t.Helper()
	if len(o.opened) != 1 || len(o.private) != 1 || filepath.Dir(o.opened[0]) != o.private[0] {
		t.Fatalf("opened %v, private folders %v", o.opened, o.private)
	}
	if b, err := os.ReadFile(o.opened[0]); err != nil || string(b) != content {
		t.Fatalf("handed over %q %v", b, err)
	}
	if es, _ := os.ReadDir(o.dir); len(es) != 0 {
		t.Fatalf("Downloads holds %v; only the fallback saves there", es)
	}
}

func TestUpdateLinuxOpensTheDebAndQuits(t *testing.T) {
	o := newOneClick(t, "linux")
	o.update(t)
	if got := o.events2(); got != "open OpenLAMT-v0.2.0-linux-x64.deb, quit" {
		t.Fatalf("events %s", got)
	}
	o.handedOverPrivately(t, "deb")
}

func TestUpdateHandoverFailureShowsTheFallback(t *testing.T) {
	for name, c := range map[string]struct {
		goos   string
		set    func(o *oneClick)
		file   string
		reason string
	}{
		"linux without handler": {"linux", func(o *oneClick) { o.openErr = errors.New("exit status 4") }, "OpenLAMT-v0.2.0-linux-x64.deb",
			"OpenLAMT could not open the installer: no app is set up for .deb files. Install the file yourself:"},
		"mac open failed": {"darwin", func(o *oneClick) { o.openErr = errors.New("exit status 1") }, "OpenLAMT-v0.2.0-macos-arm64.dmg",
			"OpenLAMT could not open the disk image. Install the file yourself:"},
		"windows UAC declined": {"windows", func(o *oneClick) { o.writable, o.elevErr = false, update.ErrDeclined }, "OpenLAMT-v0.2.0-windows-x64.zip",
			"OpenLAMT could not replace itself: the administrator permission was declined. Install the file yourself:"},
		"windows elevation failed": {"windows", func(o *oneClick) { o.writable, o.elevErr = false, errors.New("exit 5") }, "OpenLAMT-v0.2.0-windows-x64.zip",
			"OpenLAMT could not replace itself. Install the file yourself:"},
		"windows rename failed": {"windows", func(o *oneClick) {
			updateInstaller.Rename = func(string, string) error { return os.ErrPermission }
		}, "OpenLAMT-v0.2.0-windows-x64.zip", "OpenLAMT could not replace itself. Install the file yourself:"},
	} {
		t.Run(name, func(t *testing.T) {
			opened := urlsOpened(t)
			o := newOneClick(t, c.goos)
			c.set(o)
			o.update(t)
			if o.q.quits != 0 || strings.Contains(o.events2(), "start") {
				t.Fatalf("a failed handover neither quits nor restarts: %s", o.events2())
			}
			all := dialogAll(o.win())
			for _, want := range []string{"OpenLAMT 0.2.0 downloaded", c.reason, homePath(filepath.Join(o.dir, c.file))} {
				if !strings.Contains(all, want) {
					t.Errorf("%q missing: %q", want, all)
				}
			}
			if got := texts(panelButtons(o.win().Canvas().Overlays().Top())); got != "Close|Show in Folder" {
				t.Errorf("buttons %q", got)
			}
			if _, err := os.Stat(filepath.Join(o.dir, c.file)); err != nil {
				t.Errorf("the package must stay in Downloads: %v", err)
			}
			if c.goos == "windows" {
				if b, _ := os.ReadFile(o.exe); string(b) != "old exe" {
					t.Errorf("the running exe must stay: %q", b)
				}
			}
			test.Tap(overlayButton(t, o.win(), "Show in Folder"))
			if len(*opened) != 1 || (*opened)[0] != fileURL(o.dir) {
				t.Fatalf("opened %v", *opened)
			}
		})
	}
}

func TestUpdateWindowsReplacesTheExeAndRestarts(t *testing.T) {
	o := newOneClick(t, "windows")
	o.update(t)
	if got := o.events2(); got != "start OpenLAMT.exe, quit" {
		t.Fatalf("events %s: the new exe starts right before the quit", got)
	}
	if b, _ := os.ReadFile(o.exe); string(b) != "new exe" {
		t.Fatalf("exe holds %q", b)
	}
	if b, _ := os.ReadFile(o.exe + ".old"); string(b) != "old exe" {
		t.Fatalf(".old holds %q", b)
	}
	if es, _ := os.ReadDir(o.dir); len(es) != 0 {
		t.Fatalf("Downloads holds %v; the zip is never written", es)
	}
}

func TestUpdateWindowsProtectedFolderAsksForElevation(t *testing.T) {
	o := newOneClick(t, "windows")
	o.writable = false
	o.update(t)
	if got := o.events2(); got != "elevate, start OpenLAMT.exe, quit" {
		t.Fatalf("events %s", got)
	}
	if b, _ := os.ReadFile(o.exe); string(b) != "new exe" {
		t.Fatalf("exe holds %q", b)
	}
}

func TestUpdateQuitsThroughTheQuitPath(t *testing.T) {
	o := newOneClick(t, "windows")
	o.q.addDevice("a", true) // a KVM session is open: the known question comes
	o.update(t)
	cancel, action, shown := o.q.dialog()
	if !shown || action == nil || action.Text != "Quit" || !strings.Contains(dialogAll(o.win()), "Quit OpenLAMT?") {
		t.Fatalf("no quit question: %q", dialogAll(o.win()))
	}
	test.Tap(cancel)
	if o.q.quits != 0 || len(o.events) != 0 || o.q.h.s.onQuit != nil {
		t.Fatalf("Cancel keeps OpenLAMT open and starts nothing: %s", o.events2())
	}
	// A later quit of the user's own is a plain quit: no restart left over.
	o.q.quit()
	_, action, _ = o.q.dialog()
	test.Tap(action)
	if got := o.events2(); got != "quit" {
		t.Fatalf("events %s", got)
	}

	o2 := newOneClick(t, "windows")
	o2.q.addDevice("b", true)
	o2.update(t)
	_, action, _ = o2.q.dialog()
	test.Tap(action)
	if got := o2.events2(); got != "start OpenLAMT.exe, quit" {
		t.Fatalf("Quit: events %s", got)
	}
}

func TestUpdateDownloadFailureShowsUpdateFailed(t *testing.T) {
	for name, c := range map[string]struct {
		set, undo func(o *oneClick)
		text      string
	}{
		"checksum": {func(o *oneClick) { o.g.pkg = []byte("tampered") }, func(o *oneClick) { o.g.pkg = []byte("deb") },
			"The download does not match its checksum. Nothing was installed. Try again later."},
		"broken off": {func(o *oneClick) { o.g.pkgStatus = 503 }, func(o *oneClick) { o.g.pkgStatus = 0 },
			"The download did not finish. Nothing was installed. Try again later."},
	} {
		t.Run(name, func(t *testing.T) {
			o := newOneClick(t, "linux")
			c.set(o)
			o.update(t)
			all := dialogAll(o.win())
			if !strings.Contains(all, "Update failed") || !strings.Contains(all, c.text) {
				t.Fatalf("dialog %q", all)
			}
			if got := texts(panelButtons(o.win().Canvas().Overlays().Top())); got != "Close|Try Again" {
				t.Errorf("buttons %q", got)
			}
			var symbol bool
			walk(o.win().Canvas().Overlays().Top(), func(c fyne.CanvasObject) { _, ok := c.(*widgets.Symbol); symbol = symbol || ok })
			if !symbol {
				t.Error("the red warning tile is missing")
			}
			if len(o.events) != 0 || o.q.quits != 0 || len(o.q.h.errs) != 0 {
				t.Fatalf("nothing installed: %s", o.events2())
			}
			if es, _ := os.ReadDir(o.dir); len(es) != 0 {
				t.Fatalf("files left: %v", es)
			}
			// Try Again starts over and works once the package is right.
			c.undo(o)
			test.Tap(overlayButton(t, o.win(), "Try Again"))
			o.q.pass()
			if got := o.events2(); got != "open OpenLAMT-v0.2.0-linux-x64.deb, quit" {
				t.Fatalf("Try Again: events %s", got)
			}
		})
	}
}

func TestUpdateAppImageKeepsDownloadAndShowInFolder(t *testing.T) {
	opened := urlsOpened(t)
	h, g := updateHarness(t, "v0.2.0")
	updateAppImage = true
	g.setPackage("linux-x64.AppImage", []byte("appimage"))
	dir, _ := downloadsDir()
	runCheck(h)
	if all := overlayTexts(h.s.win); !strings.Contains(all, "The new version is saved to your Downloads folder; you install it yourself.") {
		t.Fatalf("intro %q", all)
	}
	test.Tap(overlayButton(t, h.s.win, "Download"))
	if b, err := os.ReadFile(filepath.Join(dir, "OpenLAMT-v0.2.0-linux-x64.AppImage")); err != nil || string(b) != "appimage" {
		t.Fatalf("saved file: %q %v", b, err)
	}
	want := "Saved as " + filepath.Join(filepath.Base(dir), "OpenLAMT-v0.2.0-linux-x64.AppImage") + ". Quit OpenLAMT"
	if all := overlayTexts(h.s.win); !strings.Contains(all, want) {
		t.Fatalf("%q missing: %q", want, all)
	}
	test.Tap(overlayButton(t, h.s.win, "Show in Folder"))
	if len(*opened) != 1 || (*opened)[0] != fileURL(dir) {
		t.Fatalf("opened %v", *opened)
	}
}

func TestHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	prev := updateGOOS
	t.Cleanup(func() { updateGOOS = prev })
	updateGOOS = "linux"
	if got := homePath(filepath.Join(home, "Downloads", "a.deb")); got != "~/Downloads/a.deb" {
		t.Errorf("got %q", got)
	}
	other := filepath.Join(string(filepath.Separator)+"srv", "a.deb")
	if got := homePath(other); got != other {
		t.Errorf("got %q", got)
	}
	updateGOOS = "windows"
	if got := homePath(filepath.Join(home, "Downloads", "a.zip")); got != filepath.Join(home, "Downloads", "a.zip") {
		t.Errorf("Windows keeps the full path: %q", got)
	}
}
