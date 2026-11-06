package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/update"
)

// fakeGitHub serves a release (or an error status) at /latest and the package at /pkg.
type fakeGitHub struct {
	srv     *httptest.Server
	status  int
	rel     update.Release
	pkg     []byte
	hits    int
	agent   string
	pkgHits int
}

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{status: 200, pkg: []byte("package of " + tag)}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			g.hits++
			g.agent = r.Header.Get("User-Agent")
			w.WriteHeader(g.status)
			_ = json.NewEncoder(w).Encode(g.rel)
		case "/pkg":
			g.pkgHits++
			_, _ = w.Write(g.pkg)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(g.srv.Close)
	sum := sha256.Sum256(g.pkg)
	g.rel = update.Release{Tag: tag, HTMLURL: "https://example.com/releases/" + tag,
		Body:   "## What's Changed\n* Language and appearance in the settings by @someone in https://example.com/pull/1\n* Windows icon in Explorer",
		Assets: []update.Asset{{Name: "OpenLAMT-" + tag + "-" + platformSuffix(), URL: g.srv.URL + "/pkg", Digest: "sha256:" + hex.EncodeToString(sum[:])}}}
	prevURL, prevDir, prevOS, prevArch := updateURL, downloadsDir, updateGOOS, updateGOARCH
	updateURL, updateGOOS, updateGOARCH = g.srv.URL+"/latest", "linux", "amd64"
	dir := t.TempDir()
	downloadsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { updateURL, downloadsDir, updateGOOS, updateGOARCH = prevURL, prevDir, prevOS, prevArch })
	return g
}

func platformSuffix() string { return "linux-x64.deb" }

// updateHarness is a start window of version 0.1.0 with the check enabled and no timer; the
// network work runs inline.
func updateHarness(t *testing.T, tag string) (*startHarness, *fakeGitHub) {
	t.Helper()
	withBuildVersion(t, "0.1.0")
	g := newFakeGitHub(t, tag)
	h := newStart(t)
	h.s.enableUpdate(-1)
	if h.s.updates != nil {
		h.s.updates.async = func(f func()) { f() }
	}
	return h, g
}

func runCheck(h *startHarness) {
	if h.s.updates != nil {
		h.s.runUpdateCheck(h.s.updates)
	}
}

func overlayTexts(w fyne.Window) string {
	var texts []string
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if l, ok := o.(*widget.Label); ok {
			texts = append(texts, l.Text)
		}
	})
	return strings.Join(texts, "|")
}

func TestUpdateNewerShowsDialog(t *testing.T) {
	h, g := updateHarness(t, "v0.2.0")
	runCheck(h)
	if g.hits != 1 || g.agent != "OpenLAMT/0.1.0" {
		t.Fatalf("hits %d, user agent %q", g.hits, g.agent)
	}
	all := overlayTexts(h.s.win)
	for _, want := range []string{"OpenLAMT 0.2.0 is available", "You are using 0.1.0.", "What's new:\n• Language and appearance in the settings\n• Windows icon in Explorer"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q missing: %q", want, all)
		}
	}
	overlayButton(t, h.s.win, "Later")
	overlayButton(t, h.s.win, "Download")
	if c := overlayCheck(t, h.s.win); c == nil || c.Text != "Don't check for updates" || c.Checked {
		t.Fatalf("check box %+v", c)
	}
}

func TestUpdateNotNewerOrFailingStaysSilent(t *testing.T) {
	for name, set := range map[string]func(g *fakeGitHub){
		"same version":  func(g *fakeGitHub) { g.rel.Tag = "v0.1.0" },
		"older rc":      func(g *fakeGitHub) { g.rel.Tag = "v0.1.0-rc16" },
		"not found":     func(g *fakeGitHub) { g.status = 404 },
		"rate limited":  func(g *fakeGitHub) { g.status = 403 },
		"server broken": func(g *fakeGitHub) { g.status = 500 },
	} {
		t.Run(name, func(t *testing.T) {
			h, g := updateHarness(t, "v0.2.0")
			set(g)
			runCheck(h)
			if g.hits != 1 || h.s.win.Canvas().Overlays().Top() != nil || len(h.errs) != 0 {
				t.Fatalf("hits %d, overlay %v, errors %d", g.hits, h.s.win.Canvas().Overlays().Top(), len(h.errs))
			}
		})
	}
}

func TestUpdateNoCheckWhenOffOrDevBuild(t *testing.T) {
	t.Run("setting off", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		g := newFakeGitHub(t, "v0.2.0")
		h := newStart(t)
		h.s.settings.SetUpdateCheck(false)
		h.s.enableUpdate(-1)
		runCheck(h)
		if h.s.updates != nil || g.hits != 0 {
			t.Fatalf("checked with the setting off: %d", g.hits)
		}
	})
	for _, v := range []string{"", "dev"} {
		t.Run("version "+v, func(t *testing.T) {
			withBuildVersion(t, v)
			g := newFakeGitHub(t, "v0.2.0")
			h := newStart(t)
			h.s.enableUpdate(-1)
			runCheck(h)
			if h.s.updates != nil || g.hits != 0 {
				t.Fatalf("checked a development build: %d", g.hits)
			}
		})
	}
}

func TestUpdateSettingDefaultsOn(t *testing.T) {
	h := newStart(t)
	if !h.s.settings.UpdateCheck() {
		t.Fatal("the update check must be on by default")
	}
}

func TestUpdateDontCheckTurnsOff(t *testing.T) {
	for _, button := range []string{"Later", "Download"} {
		t.Run(button, func(t *testing.T) {
			h, _ := updateHarness(t, "v0.2.0")
			runCheck(h)
			test.Tap(overlayCheck(t, h.s.win))
			test.Tap(overlayButton(t, h.s.win, button))
			if h.s.settings.UpdateCheck() {
				t.Fatal("the setting must be off")
			}
		})
	}
	t.Run("unchecked keeps it", func(t *testing.T) {
		h, _ := updateHarness(t, "v0.2.0")
		runCheck(h)
		test.Tap(overlayButton(t, h.s.win, "Later"))
		if !h.s.settings.UpdateCheck() || h.s.win.Canvas().Overlays().Top() != nil {
			t.Fatal("Later alone closes and keeps the setting")
		}
	})
}

func TestUpdateDownloadSavesAndShowsFolder(t *testing.T) {
	opened := urlsOpened(t)
	h, g := updateHarness(t, "v0.2.0")
	dir, _ := downloadsDir()
	runCheck(h)
	test.Tap(overlayButton(t, h.s.win, "Download"))
	if g.pkgHits != 1 {
		t.Fatalf("package fetched %d times", g.pkgHits)
	}
	saved := filepath.Join(dir, "OpenLAMT-v0.2.0-linux-x64.deb")
	if b, err := os.ReadFile(saved); err != nil || string(b) != string(g.pkg) {
		t.Fatalf("saved file: %q %v", b, err)
	}
	all := overlayTexts(h.s.win)
	want := "Saved as " + filepath.Join(filepath.Base(dir), "OpenLAMT-v0.2.0-linux-x64.deb") + ". Quit OpenLAMT"
	if !strings.Contains(all, want) {
		t.Fatalf("%q missing: %q", want, all)
	}
	overlayButton(t, h.s.win, "Close")
	test.Tap(overlayButton(t, h.s.win, "Show in folder"))
	if len(*opened) != 1 || (*opened)[0] != fileURL(dir) {
		t.Fatalf("opened %v", *opened)
	}
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("dialog still open")
	}
}

func TestUpdateDownloadButtonLockedWhileLoading(t *testing.T) {
	h, _ := updateHarness(t, "v0.2.0")
	var pending func()
	h.s.updates.async = func(f func()) { pending = f }
	runCheck(h)
	b := overlayButton(t, h.s.win, "Download")
	test.Tap(b)
	if !b.Disabled() || b.Text != "Downloading…" {
		t.Fatalf("button %q disabled %v", b.Text, b.Disabled())
	}
	pending()
	overlayButton(t, h.s.win, "Show in folder")
}

func TestUpdateLaterDuringDownloadShowsNothing(t *testing.T) {
	h, _ := updateHarness(t, "v0.2.0")
	var pending func()
	h.s.updates.async = func(f func()) { pending = f }
	runCheck(h)
	test.Tap(overlayButton(t, h.s.win, "Download"))
	test.Tap(overlayButton(t, h.s.win, "Later"))
	pending()
	if h.s.win.Canvas().Overlays().Top() != nil || len(h.errs) != 0 {
		t.Fatal("a cancelled download must stay quiet")
	}
}

func TestUpdateDownloadDigestMismatchShowsError(t *testing.T) {
	h, g := updateHarness(t, "v0.2.0")
	dir, _ := downloadsDir()
	g.pkg = []byte("tampered")
	runCheck(h)
	test.Tap(overlayButton(t, h.s.win, "Download"))
	if len(h.errs) != 1 {
		t.Fatal("no error")
	}
	if err := <-h.errs; !strings.Contains(describe(err), "checksum") {
		t.Fatalf("error %q", describe(err))
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("files left: %v", es)
	}
}

func TestUpdateWithoutPackageOpensReleasePage(t *testing.T) {
	opened := urlsOpened(t)
	h, g := updateHarness(t, "v0.2.0")
	updateGOOS, updateGOARCH = "linux", "riscv64"
	runCheck(h)
	test.Tap(overlayButton(t, h.s.win, "Download"))
	if len(*opened) != 1 || (*opened)[0] != g.rel.HTMLURL || g.pkgHits != 0 {
		t.Fatalf("opened %v, package hits %d", *opened, g.pkgHits)
	}
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("dialog still open")
	}
}

func TestUpdateQuietRules(t *testing.T) {
	const key = "admin@host.example.com"
	for name, block := range map[string]func(h *startHarness){
		"device window open":  func(h *startHarness) { h.s.open[key] = &DeviceWindow{} },
		"connect in progress": func(h *startHarness) { h.s.busy[key] = "x" },
		"another dialog open": func(h *startHarness) { showAboutDialog(h.s.win, h.s.app) },
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := updateHarness(t, "v0.2.0")
			block(h)
			before := h.s.win.Canvas().Overlays().Top()
			runCheck(h)
			if h.s.win.Canvas().Overlays().Top() != before {
				t.Fatal("must not show")
			}
			delete(h.s.open, key)
		})
	}
}

func TestUpdateGoesBeforeCoffee(t *testing.T) {
	t.Run("update shown, no coffee", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		newFakeGitHub(t, "v0.2.0")
		h, _ := nudgeHarness(t, 3)
		h.s.enableUpdate(-1)
		h.s.coffeeFire() // the nudge falls due while the check is pending
		if h.s.win.Canvas().Overlays().Top() != nil {
			t.Fatal("coffee must wait for the update answer")
		}
		runCheck(h)
		if !strings.Contains(overlayTexts(h.s.win), "OpenLAMT 0.2.0 is available") {
			t.Fatal("update dialog missing")
		}
		test.Tap(overlayButton(t, h.s.win, "Later"))
		h.s.coffeeFire()
		if h.s.win.Canvas().Overlays().Top() != nil {
			t.Fatal("no coffee in a start that showed the update")
		}
	})
	t.Run("nothing new, coffee follows", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		g := newFakeGitHub(t, "v0.2.0")
		g.status = 404
		h, _ := nudgeHarness(t, 3)
		h.s.enableUpdate(-1)
		h.s.coffeeFire()
		runCheck(h)
		if _, _, buttons := dialogTexts(h.s.win); buttons["Buy me a coffee"] == nil {
			t.Fatal("the coffee nudge must follow")
		}
	})
}

func TestSettingsDialogUpdateSwitch(t *testing.T) {
	h := newStart(t)
	f := newSettingsForm(&h.s.settings)
	if !f.updates.Checked() {
		t.Fatal("switch must start on")
	}
	f.updates.SetChecked(false)
	if err := f.apply(&h.s.settings); err != nil {
		t.Fatal(err)
	}
	if h.s.settings.UpdateCheck() {
		t.Fatal("Save must turn the check off")
	}
}

func TestFileURL(t *testing.T) {
	if got := fileURL("/home/user/Downloads"); got != "file:///home/user/Downloads" {
		t.Fatal(got)
	}
}
