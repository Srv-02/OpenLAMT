package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// infoButtons returns the titles of the info buttons below o.
func infoButtons(o fyne.CanvasObject) []string {
	var titles []string
	walk(o, func(c fyne.CanvasObject) {
		if b, ok := c.(*widgets.InfoButton); ok {
			titles = append(titles, b.Content.Title)
		}
	})
	return titles
}

// helpTranslations reads all texts of one language from the translation files.
func helpTranslations(t *testing.T, language string) map[string]string {
	t.Helper()
	files, err := filepath.Glob("i18n/translations/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no translation files: %v", err)
	}
	all := map[string]string{}
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".json")
		if base != language && !strings.HasSuffix(base, "."+language) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for k, v := range m {
			all[k] = v
		}
	}
	return all
}

// Every popover has What and When; a popover with a risk has both Wrong and Recover.
func TestHelpTopicsHaveTheirParts(t *testing.T) {
	englishUI(t)
	seen := map[string]bool{}
	for _, id := range allHelpIDs {
		c := helpContent(id)
		if c.Title == "" || c.What == "" || c.When == "" {
			t.Errorf("%s: incomplete %+v", id, c)
		}
		if (c.Wrong == "") != (c.Recover == "") {
			t.Errorf("%s: Wrong and Recover belong together", id)
		}
		if seen[c.Title] {
			t.Errorf("%s: title %q twice", id, c.Title)
		}
		seen[c.Title] = true
	}
}

var helpKeyRE = regexp.MustCompile(`lang\.L\("((?:[^"\\]|\\.)*)"\)`)

// Every text of a popover exists in English and in German, and the German text is not empty.
func TestHelpTextsExistInBothLanguages(t *testing.T) {
	src, err := os.ReadFile("help_topics.go")
	if err != nil {
		t.Fatal(err)
	}
	en, de := helpTranslations(t, "en"), helpTranslations(t, "de")
	keys := helpKeyRE.FindAllStringSubmatch(string(src), -1)
	if len(keys) < 3*len(allHelpIDs) {
		t.Fatalf("only %d texts found", len(keys))
	}
	for _, m := range keys {
		k := strings.ReplaceAll(m[1], `\"`, `"`)
		if en[k] == "" {
			t.Errorf("English text lacks %q", k)
		}
		if de[k] == "" {
			t.Errorf("German text lacks %q", k)
		}
	}
}

// Every info button of the Settings page: placed in its row, once per setting.
func TestSettingsPageHasItsInfoButtons(t *testing.T) {
	_, p := settingsHarness(t, nil)
	got := infoButtons(p.pane.body)
	for _, id := range []helpID{helpHostName, helpDomain, helpPing, helpDdns, helpEnvDomains, helpNetAddress,
		helpNetIP, helpKvm, helpIder, helpSol, helpConsent, helpClock, helpTls, helpTurnOff} {
		title := helpContent(id).Title
		if n := slices.Index(got, title); n < 0 {
			t.Errorf("no info button for %q", title)
		}
	}
	if len(got) != 14 {
		t.Errorf("%d info buttons, want 14: %v", len(got), got)
	}
}

// Access and Permissions of the user dialog carry an info button that opens a popover.
func TestUserDialogHasInfoButtons(t *testing.T) {
	h, p := usersHarness(t, exampleUsers())
	test.Tap(p.table.lines[2].action)
	top := h.m.parent.Canvas().Overlays().Top()
	got := infoButtons(top)
	want := []string{"Access", "Permissions"}
	if !slices.Equal(got, want) {
		t.Fatalf("info buttons %v, want %v", got, want)
	}
	var btn *widgets.InfoButton
	walk(top, func(o fyne.CanvasObject) {
		if b, ok := o.(*widgets.InfoButton); ok && b.Content.Title == "Permissions" {
			btn = b
		}
	})
	var shown *widgets.HelpPopover
	btn.OnShown = func(p *widgets.HelpPopover) { shown = p }
	btn.Tapped(nil)
	if shown == nil || !shown.Visible() {
		t.Error("the popover does not open")
	}
}

var helpBlockRE = regexp.MustCompile(`(?s)HelpContent\{(.*?)\n?\s*\}`)

// Every HelpContent in the sources has a title, What and When, and every text exists in both
// languages and is not empty. This covers the popovers outside help_topics.go (audit log, Remote
// Platform Erase, One Click Recovery, Power, Wi-Fi, System Defense).
func TestEveryPopoverTextExistsInBothLanguages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	en, de := helpTranslations(t, "en"), helpTranslations(t, "de")
	blocks := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "help_topics.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range helpBlockRE.FindAllStringSubmatch(string(src), -1) {
			if !strings.Contains(b[1], "lang.L(") {
				continue
			}
			blocks++
			for _, field := range []string{"Title:", "What:", "When:"} {
				if !strings.Contains(b[1], field) {
					t.Errorf("%s: popover without %s", f, field)
				}
			}
			for _, m := range helpKeyRE.FindAllStringSubmatch(b[1], -1) {
				k := strings.ReplaceAll(m[1], `\"`, `"`)
				if en[k] == "" {
					t.Errorf("%s: English text lacks %q", f, k)
				}
				if de[k] == "" {
					t.Errorf("%s: German text lacks %q", f, k)
				}
			}
		}
	}
	if blocks < 8 {
		t.Fatalf("only %d popovers found", blocks)
	}
}

// The audit log settings dialog carries the info buttons of the design (Record events, When the
// log is full), the switch rows of Remote features carry theirs.
func TestAuditAndRemoteFeatureRowsHaveInfoButtons(t *testing.T) {
	h, _ := openAuditDialog(t, exampleAuditSettings())
	got := infoButtons(h.m.parent.Canvas().Overlays().Top())
	want := []string{"Record events", "When the log is full"}
	if !slices.Equal(got, want) {
		t.Errorf("audit dialog info buttons %v, want %v", got, want)
	}
	for _, b := range []*widgets.InfoButton{rpeHelpButton(), ocrHelpButton()} {
		c := b.Content
		if c.Title == "" || c.What == "" || c.When == "" || c.Wrong == "" || c.Recover == "" {
			t.Errorf("incomplete popover %+v", c)
		}
	}
}
