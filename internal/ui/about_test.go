package ui

import (
	"image/png"
	"io"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// dialogTexts collects the texts (canvas.Text, labels), the links (text -> URL, "" for a link
// with an action) and the primary buttons (text -> button) of the top overlay.
func dialogTexts(w fyne.Window) (texts []string, links map[string]*widgets.Link, buttons map[string]*widgets.PrimaryButton) {
	links = map[string]*widgets.Link{}
	buttons = map[string]*widgets.PrimaryButton{}
	walk(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *canvas.Text:
			texts = append(texts, v.Text)
		case *widget.Label:
			texts = append(texts, v.Text)
		case *widgets.Link:
			links[v.Text] = v
		case *widgets.PrimaryButton:
			buttons[v.Text] = v
		}
	})
	return
}

func linkURL(l *widgets.Link) string {
	if l == nil || l.URL == nil {
		return ""
	}
	return l.URL.String()
}

// urlsOpened replaces openURL for the test and returns what was opened.
func urlsOpened(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := openURL
	openURL = func(u string) { got = append(got, u) }
	t.Cleanup(func() { openURL = prev })
	return &got
}

func TestAboutDialogShowsVersionLinksAndCredits(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	showAboutDialog(h.s.win, h.s.app)
	texts, links, buttons := dialogTexts(h.s.win)
	all := strings.Join(texts, "|")
	for _, want := range []string{"OpenLAMT", "Desktop client for Intel AMT.", "Independent project, not affiliated with Intel.",
		"Support OpenLAMT", "Credits"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q missing: %q", want, all)
		}
	}
	if linkURL(links["github.com/Srv-02/OpenLAMT"]) != "https://github.com/Srv-02/OpenLAMT" || ProjectURL != "https://github.com/Srv-02/OpenLAMT" {
		t.Fatalf("project link: %v", links)
	}
	if linkURL(links["NOTICE"]) != ProjectURL+"/blob/main/NOTICE" {
		t.Fatalf("NOTICE link: %v", links["NOTICE"])
	}
	for _, c := range credits {
		if linkURL(links[c.Name]) != c.URL {
			t.Fatalf("credit %s missing: %v", c.Name, links)
		}
	}
	badges := 0
	walk(h.s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widgets.Badge); ok {
			badges++
			found := false
			for _, c := range credits {
				found = found || c.License == b.Text
			}
			if !found {
				t.Errorf("badge %q is no license of the credits", b.Text)
			}
		}
	})
	if badges != len(credits) {
		t.Errorf("%d badges for %d credits", badges, len(credits))
	}
	got := urlsOpened(t)
	test.Tap(buttons["Buy me a coffee"])
	test.Tap(buttons["Star on GitHub"])
	if strings.Join(*got, " ") != "https://buymeacoffee.com/michaelmayer "+ProjectURL {
		t.Fatalf("pills opened %v", *got)
	}
}

// The credits sit in a scroll container (HANDOFF.md 5.5), the dialog is 560 x 600.
func TestAboutDialogSizeAndScrollingCredits(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	h.s.app.Settings().SetTheme(style.Theme{})
	h.s.win.Resize(fyne.NewSize(1000, 700))
	showAboutDialog(h.s.win, h.s.app)
	var scroll *container.Scroll
	var frame fyne.CanvasObject
	walk(h.s.win.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *container.Scroll:
			scroll = v
		case *fyne.Container:
			if _, ok := v.Layout.(*fixedSizeLayout); ok && frame == nil {
				frame = v
			}
		}
	})
	if scroll == nil || frame == nil {
		t.Fatalf("scroll %v, frame %v", scroll, frame)
	}
	if s := frame.Size(); s.Width != 560 || s.Height != 600 {
		t.Errorf("dialog %v, want 560 x 600", s)
	}
	if scroll.Content.MinSize().Height > scroll.Size().Height {
		t.Errorf("credits %v do not fit the scroll area %v at the default font", scroll.Content.MinSize(), scroll.Size())
	}
}

func TestAboutDialogClosesWithButtonAndEsc(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	c := h.s.win.Canvas()
	showAboutDialog(h.s.win, h.s.app)
	if c.Overlays().Top() == nil {
		t.Fatal("not shown")
	}
	_, _, buttons := dialogTexts(h.s.win)
	test.Tap(buttons["Close"])
	if c.Overlays().Top() != nil {
		t.Fatal("Close did not close the dialog")
	}
	showAboutDialog(h.s.win, h.s.app)
	c.OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if c.Overlays().Top() != nil {
		t.Fatal("Esc did not close the dialog")
	}
	// A focused control closes it on Esc too.
	showAboutDialog(h.s.win, h.s.app)
	_, _, buttons = dialogTexts(h.s.win)
	buttons["Close"].TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if c.Overlays().Top() != nil {
		t.Fatal("Esc on the focused Close button did not close the dialog")
	}
}

// Every project in the credits must be named in NOTICE.
func TestCreditsAreListedInNotice(t *testing.T) {
	b, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range credits {
		if !strings.Contains(string(b), c.URL) {
			t.Errorf("%s (%s) not in NOTICE", c.Name, c.URL)
		}
	}
}

func TestSettingsDialogFooterSupportAndAbout(t *testing.T) {
	englishUI(t)
	h := newStart(t)
	showSettingsDialog(h.s.win, &h.s.settings)
	texts, links, _ := dialogTexts(h.s.win)
	all := strings.Join(texts, "|")
	if !strings.Contains(all, "OpenLAMT") {
		t.Fatalf("footer: %q", all)
	}
	got := urlsOpened(t)
	links["Support"].Tapped(nil)
	if strings.Join(*got, " ") != "https://buymeacoffee.com/michaelmayer" {
		t.Fatalf("Support opened %v", *got)
	}
	links["About & Credits"].Tapped(nil)
	texts, _, _ = dialogTexts(h.s.win)
	if !strings.Contains(strings.Join(texts, "|"), "Support OpenLAMT") {
		t.Fatalf("About not on top: %v", texts)
	}
	// Closing About shows the settings dialog again.
	_, _, buttons := dialogTexts(h.s.win)
	test.Tap(buttons["Close"])
	_, _, buttons = dialogTexts(h.s.win)
	if buttons[lang.L("Save")] == nil {
		t.Fatal("settings dialog not back")
	}
}

func TestFundingFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/FUNDING.yml")
	if err != nil || strings.TrimSpace(string(b)) != "buy_me_a_coffee: michaelmayer" {
		t.Fatalf("%q %v", b, err)
	}
}

func shotPNG(t *testing.T, out io.Writer, w fyne.Window) {
	t.Helper()
	c := software.NewCanvas()
	c.SetPadded(false)
	c.SetScale(shotScale())
	c.SetContent(w.Content())
	c.Resize(fyne.NewSize(1000, 700))
	if o := w.Canvas().Overlays().Top(); o != nil {
		c.Overlays().Add(o)
	}
	if err := png.Encode(out, c.Capture()); err != nil {
		t.Fatal(err)
	}
}

func TestAboutShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	_ = os.MkdirAll(dir, 0o755)
	englishUI(t)
	h := newStart(t)
	h.s.app.Settings().SetTheme(style.Theme{})
	save := func(name string) {
		f, err := os.Create(dir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		shotPNG(t, f, h.s.win)
	}
	showSettingsDialog(h.s.win, &h.s.settings)
	save("settings.png")
	showAboutDialog(h.s.win, h.s.app)
	save("about.png")
}
