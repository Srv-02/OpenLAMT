package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/widgets"
	"github.com/Srv-02/openlamt/internal/update"
)

// The settings dialog is 600 x 760, has the sections of the design in order, each with its card,
// and only the sections scroll: title, footer and buttons stay in place (gesamtdesign V8).
func TestSettingsDialogSections(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	s := h.s
	showSettingsDialog(s.win, &s.settings, s.checkUpdateNow)
	top := s.win.Canvas().Overlays().Top()

	var frame *fixedSizeLayout
	walk(top, func(o fyne.CanvasObject) {
		if c, ok := o.(*fyne.Container); ok && frame == nil {
			frame, _ = c.Layout.(*fixedSizeLayout)
		}
	})
	if frame == nil || frame.size != fyne.NewSize(600, 760) {
		t.Fatalf("dialog frame %+v, want 600x760", frame)
	}
	var scroll *container.Scroll
	walk(top, func(o fyne.CanvasObject) {
		if sc, ok := o.(*container.Scroll); ok && scroll == nil {
			scroll = sc
		}
	})
	if scroll == nil {
		t.Fatal("no scroll area")
	}

	var rowCounts []int
	walk(scroll.Content, func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *widgets.FormCard:
			rowCounts = append(rowCounts, len(v.Rows()))
		}
	})
	want := []string{"General", "Updates", "Devices", "Remote screen", "Data"}
	var titles []string
	for _, x := range textsBelow(scroll.Content) {
		if slices.Contains(want, x) {
			titles = append(titles, x)
		}
	}
	if !slices.Equal(titles, want) {
		t.Fatalf("sections %q, want %q", titles, want)
	}
	if wantRows := []int{3, 1, 1, 5, 1}; !slices.Equal(rowCounts, wantRows) {
		t.Fatalf("rows per section %v, want %v", rowCounts, wantRows)
	}

	// The fixed parts lie outside the scroll area.
	for _, text := range []string{"Settings", "OpenLAMT", "Support", "About & Credits", "Save", "Cancel"} {
		if inside(scroll.Content, text) {
			t.Errorf("%q scrolls with the sections", text)
		}
	}
	if !inside(top, "Save") || !inside(top, "About & Credits") {
		t.Error("footer or buttons missing")
	}
	// Each section is cut off at the scroll area, so it is shorter than the content.
	if scroll.Size().Height >= scroll.Content.MinSize().Height+1 && scroll.Content.MinSize().Height > 700 {
		t.Errorf("scroll area %v, content %v", scroll.Size(), scroll.Content.MinSize())
	}
}

// textsBelow lists the texts below o, in tree order.
func textsBelow(o fyne.CanvasObject) []string {
	var out []string
	walk(o, func(c fyne.CanvasObject) {
		switch v := c.(type) {
		case *widget.Label:
			out = append(out, v.Text)
		case *canvas.Text:
			out = append(out, v.Text)
		}
	})
	return out
}

// inside reports whether the texts or buttons below root include text.
func inside(root fyne.CanvasObject, text string) bool {
	found := hasText(root, text)
	walk(root, func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *widgets.PrimaryButton:
			found = found || v.Text == text
		case *widgets.Link:
			found = found || v.Text == text
		}
	})
	return found
}

// logged collects what the update check writes to the log.
func logged(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	prev := updateLog
	updateLog = func(reason string, err error) {
		if err != nil {
			reason += ": " + err.Error()
		}
		lines = append(lines, reason)
	}
	t.Cleanup(func() { updateLog = prev })
	return &lines
}

func hasLine(lines []string, part string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, part) })
}

// #59 (a): every automatic check that shows nothing leaves a log line with the reason.
func TestUpdateSilentExitsAreLogged(t *testing.T) {
	t.Run("setting off", func(t *testing.T) {
		log := logged(t)
		withBuildVersion(t, "0.1.0")
		newFakeGitHub(t, "v0.2.0")
		h := newStart(t)
		h.s.settings.SetUpdateCheck(false)
		h.s.enableUpdate(-1)
		if !hasLine(*log, "the setting is off") {
			t.Fatalf("log %q", *log)
		}
	})
	t.Run("no version", func(t *testing.T) {
		log := logged(t)
		withBuildVersion(t, "dev")
		newFakeGitHub(t, "v0.2.0")
		h := newStart(t)
		h.s.enableUpdate(-1)
		if !hasLine(*log, "no release version") {
			t.Fatalf("log %q", *log)
		}
	})
	t.Run("API error with status", func(t *testing.T) {
		log := logged(t)
		h, g := updateHarness(t, "v0.2.0")
		g.status = 403
		runCheck(h)
		if !hasLine(*log, "update check failed") || !hasLine(*log, "403 Forbidden") {
			t.Fatalf("log %q", *log)
		}
	})
	t.Run("up to date", func(t *testing.T) {
		log := logged(t)
		h, _ := updateHarness(t, "v0.1.0")
		runCheck(h)
		if !hasLine(*log, "no newer release") {
			t.Fatalf("log %q", *log)
		}
	})
	t.Run("window busy", func(t *testing.T) {
		log := logged(t)
		h, _ := updateHarness(t, "v0.2.0")
		h.s.busy["admin@host.example.com"] = "x"
		runCheck(h)
		if !hasLine(*log, "shown once the window is quiet") {
			t.Fatalf("log %q", *log)
		}
	})
}

// #59 (c): a newer release found while the window is busy is shown once, as soon as it is quiet.
func TestUpdateShownOnceTheWindowIsQuiet(t *testing.T) {
	const key = "admin@host.example.com"
	h, _ := updateHarness(t, "v0.2.0")
	h.s.busy[key] = "x"
	runCheck(h)
	n := h.s.updates
	if h.s.win.Canvas().Overlays().Top() != nil || !n.waiting || n.retry == nil {
		t.Fatal("a busy window must keep the release and wait")
	}
	h.s.updateRetry(n, update.Release{Tag: "v0.2.0"})
	if h.s.win.Canvas().Overlays().Top() != nil || !n.waiting {
		t.Fatal("still busy: nothing yet, still waiting")
	}
	delete(h.s.busy, key)
	rel := update.Release{Tag: "v0.2.0", Body: "* New thing"}
	h.s.updateRetry(n, rel)
	if !strings.Contains(overlayTexts(h.s.win), "OpenLAMT 0.2.0 is available") || n.waiting || !n.shown {
		t.Fatalf("the dialog must appear once quiet: %q", overlayTexts(h.s.win))
	}
	test.Tap(overlayButton(t, h.s.win, "Later"))
	h.s.updateRetry(n, rel)
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("shown only once")
	}
}

func TestUpdateWaitingStopsWhenTheSettingTurnsOff(t *testing.T) {
	log := logged(t)
	h, _ := updateHarness(t, "v0.2.0")
	h.s.busy["admin@host.example.com"] = "x"
	runCheck(h)
	delete(h.s.busy, "admin@host.example.com")
	h.s.settings.SetUpdateCheck(false)
	h.s.updateRetry(h.s.updates, update.Release{Tag: "v0.2.0"})
	if h.s.win.Canvas().Overlays().Top() != nil || h.s.updates.waiting || !hasLine(*log, "turned off meanwhile") {
		t.Fatalf("log %q", *log)
	}
}

func TestUpdateCoffeeWaitsWhileTheUpdateWaits(t *testing.T) {
	withBuildVersion(t, "0.1.0")
	newFakeGitHub(t, "v0.2.0")
	h, _ := nudgeHarness(t, 3)
	h.s.enableUpdate(-1)
	h.s.busy["admin@host.example.com"] = "x"
	runCheck(h)
	delete(h.s.busy, "admin@host.example.com")
	h.s.coffeeFire()
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("coffee must wait for the update dialog")
	}
	h.s.updateRetry(h.s.updates, update.Release{Tag: "v0.2.0"})
	if !strings.Contains(overlayTexts(h.s.win), "is available") {
		t.Fatal("update dialog missing")
	}
}

// checkNowResult runs the manual check and returns what it reported.
func checkNowResult(h *startHarness) []updateCheckResult {
	var got []updateCheckResult
	h.s.checkUpdateNow(func(r updateCheckResult) { got = append(got, r) })
	return got
}

// #59 (b): the manual check works with the setting off and always reports.
func TestCheckNowReportsEveryOutcome(t *testing.T) {
	t.Run("newer opens the update dialog, with the setting off", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		g := newFakeGitHub(t, "v0.2.0")
		h := newStart(t)
		h.s.settings.SetUpdateCheck(false)
		got := checkNowResult(h)
		if g.hits != 1 || len(got) != 1 || got[0].state != updateCheckFound {
			t.Fatalf("hits %d, result %+v", g.hits, got)
		}
		if !strings.Contains(overlayTexts(h.s.win), "OpenLAMT 0.2.0 is available") {
			t.Fatal("update dialog missing")
		}
	})
	t.Run("latest", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		newFakeGitHub(t, "v0.1.0")
		h := newStart(t)
		got := checkNowResult(h)
		if len(got) != 1 || got[0].state != updateCheckLatest || got[0].text != "You are using the latest version (0.1.0)." {
			t.Fatalf("result %+v", got)
		}
		if h.s.win.Canvas().Overlays().Top() != nil {
			t.Fatal("no dialog for the latest version")
		}
	})
	for name, c := range map[string]struct {
		status int
		text   string
	}{
		"rate limit":   {403, "Could not check for updates: GitHub's rate limit is reached. Try again later."},
		"too many":     {429, "Could not check for updates: GitHub's rate limit is reached. Try again later."},
		"server error": {500, "Could not check for updates: GitHub answered with HTTP 500. Try again later."},
	} {
		t.Run(name, func(t *testing.T) {
			withBuildVersion(t, "0.1.0")
			g := newFakeGitHub(t, "v0.2.0")
			g.status = c.status
			h := newStart(t)
			got := checkNowResult(h)
			if len(got) != 1 || got[0].state != updateCheckFailed || got[0].text != c.text {
				t.Fatalf("result %+v, want %q", got, c.text)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		withBuildVersion(t, "0.1.0")
		g := newFakeGitHub(t, "v0.2.0")
		g.srv.Close()
		h := newStart(t)
		got := checkNowResult(h)
		if len(got) != 1 || got[0].state != updateCheckFailed || !strings.Contains(got[0].text, "GitHub could not be reached") {
			t.Fatalf("result %+v", got)
		}
	})
	t.Run("development build", func(t *testing.T) {
		withBuildVersion(t, "dev")
		newFakeGitHub(t, "v0.2.0")
		h := newStart(t)
		got := checkNowResult(h)
		if len(got) != 1 || got[0].state != updateCheckFailed || !strings.Contains(got[0].text, "no release version") {
			t.Fatalf("result %+v", got)
		}
	})
}

func TestUpdateFailReasonTimeout(t *testing.T) {
	if got := updateFailReason(context.DeadlineExceeded); got != "GitHub did not answer. Try again later." {
		t.Fatal(got)
	}
	if got := updateFailReason(errors.New("boom")); !strings.Contains(got, "could not be reached") {
		t.Fatal(got)
	}
}

// A manual answer replaces an automatic one that waits for a quiet window.
func TestCheckNowTakesOverAWaitingNotice(t *testing.T) {
	h, _ := updateHarness(t, "v0.2.0")
	h.s.busy["admin@host.example.com"] = "x"
	runCheck(h)
	delete(h.s.busy, "admin@host.example.com")
	checkNowResult(h)
	n := h.s.updates
	test.Tap(overlayButton(t, h.s.win, "Later"))
	h.s.updateRetry(n, update.Release{Tag: "v0.2.0"})
	if h.s.win.Canvas().Overlays().Top() != nil || n.waiting {
		t.Fatal("the dialog appeared twice")
	}
}

// The row in the dialog: the button locks while checking, the result stands in the row, green for
// the latest version and red for an error, and a newer version opens the update dialog.
func TestSettingsCheckNowRow(t *testing.T) {
	englishUI(t)
	withBuildVersion(t, "0.1.0")
	g := newFakeGitHub(t, "v0.1.0")
	h := newStart(t)
	s := h.s
	var pending func()
	updateAsync = func(f func()) { pending = f }
	showSettingsDialog(s.win, &s.settings, s.checkUpdateNow)
	if !hasText(s.win.Canvas().Overlays().Top(), "Checks GitHub for a new version at start.") {
		t.Fatal("idle text missing")
	}
	btn := overlayPrimary(t, s.win, "Check Now")
	test.Tap(btn)
	if !btn.Disabled() || btn.Text != "Checking…" || !hasText(s.win.Canvas().Overlays().Top(), "Checking…") {
		t.Fatalf("button %q disabled %v", btn.Text, btn.Disabled())
	}
	pending()
	top := s.win.Canvas().Overlays().Top()
	if btn.Disabled() || btn.Text != "Check Now" || !hasTextPrefix(top, "You are using the latest version (0.1.0).") {
		t.Fatalf("after the answer: button %q disabled %v", btn.Text, btn.Disabled())
	}

	g.status = 500
	test.Tap(btn)
	pending()
	if !hasTextPrefix(s.win.Canvas().Overlays().Top(), "Could not check for updates") {
		t.Fatal("error missing in the row")
	}

	g.status, g.rel.Tag = 200, "v0.2.0"
	test.Tap(btn)
	pending()
	if !strings.Contains(overlayTexts(s.win), "OpenLAMT 0.2.0 is available") {
		t.Fatal("update dialog missing")
	}
}

func overlayPrimary(t *testing.T, w fyne.Window, text string) *widgets.PrimaryButton {
	t.Helper()
	_, _, buttons := dialogTexts(w)
	b := buttons[text]
	if b == nil {
		t.Fatalf("button %q missing", text)
	}
	return b
}

// hasTextPrefix is true if a text below o starts with prefix: long texts wrap into lines.
func hasTextPrefix(o fyne.CanvasObject, prefix string) bool {
	return slices.ContainsFunc(textsBelow(o), func(x string) bool { return strings.HasPrefix(x, prefix) })
}
