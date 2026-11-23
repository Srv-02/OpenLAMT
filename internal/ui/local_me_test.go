package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/localme"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// newLocalStart builds a start window whose Intel ME detection answers found, with one favorite.
func newLocalStart(t *testing.T, found bool) *StartWindow {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(style.Theme{}) // the empty card uses the app's sizes
	keys := keychain.NewMemory()
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	mgr.Upsert(favorites.Favorite{Name: "Lab-Server", Host: "device.example.com", Username: "admin", UseTLS: true})
	s := newStartWindow(a, mgr, keys, session.ConnectFake(&session.Fake{FP: fp}), func(s *StartWindow) {
		s.prober.Stop()
		s.lookupPassword = func(_, _ string, answer func(bool)) { answer(false) }
		s.localME = localme.Fake(found)
	})
	t.Cleanup(func() { s.win.Close() })
	return s
}

// richTexts returns the text of every RichText below o.
func richTexts(o fyne.CanvasObject) []string {
	var out []string
	walk(o, func(c fyne.CanvasObject) {
		if r, ok := c.(*widget.RichText); ok {
			out = append(out, r.String())
		}
	})
	return out
}

func TestLocalSectionOnlyWithIntelME(t *testing.T) {
	if s := newLocalStart(t, false); s.localSection.Visible() {
		t.Fatal("section shown without an Intel ME device")
	}
	s := newLocalStart(t, true)
	if !s.localSection.Visible() || !hasText(s.localSection, "This computer") {
		t.Fatal("section hidden with an Intel ME device")
	}
	r := s.localRow
	if r.Name() != "This computer" || r.Host() != "Intel ME found, not checked" || r.Dot() != widgets.DotUnknown || r.Icon() != icons.Hardware {
		t.Fatalf("row %q %q dot=%v icon=%v", r.Name(), r.Host(), r.Dot(), r.Icon())
	}
	if s.mode != modeView || r.Selected() { // the start still selects the first favorite
		t.Fatalf("mode=%v local selected=%v", s.mode, r.Selected())
	}
}

func TestLocalDetailShowsNotChecked(t *testing.T) {
	s := newLocalStart(t, true)
	test.Tap(s.localRow)
	if s.mode != modeLocal || !s.localRow.Selected() || s.selKey != "" {
		t.Fatalf("mode=%v selected=%v sel=%q", s.mode, s.localRow.Selected(), s.selKey)
	}
	if s.title.Text != "This computer" {
		t.Fatalf("title %q", s.title.Text)
	}
	if !s.meta.Visible() || !hasTextSuffix(s.meta, "Intel ME found") {
		t.Fatal("meta line lacks \"Intel ME found\"")
	}
	if !s.localCard.Visible() || s.card1.Visible() || s.card2.Visible() || s.advanced.Visible() {
		t.Fatal("detail shows the favorite cards")
	}
	if !hasText(s.localCard, "Not checked yet") ||
		strings.Join(richTexts(s.localCard), "") != "Check reads Intel AMT through the Intel ME driver of this computer. Nothing is changed." {
		t.Fatalf("card texts %q", richTexts(s.localCard))
	}
	if len(s.actions.Objects) != 1 || s.actions.Objects[0] != s.localActions || !s.localActions.Visible() || s.checkBtn.Text != "Check" {
		t.Fatalf("header %d objects, button %q", len(s.actions.Objects), s.checkBtn.Text)
	}
	if !s.editBtn.Disabled() || !s.dupBtn.Disabled() || !s.delBtn.Disabled() {
		t.Fatal("toolbar enabled without a favorite")
	}
	if s.tileIcon.Resource() != icons.GetStroke(icons.Hardware, icons.Accent, 1.5) {
		t.Fatal("tile does not show the chip")
	}

	// Back to the favorite: its cards return and the local row is deselected.
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if s.mode != modeView || s.localRow.Selected() || s.localCard.Visible() || !s.card1.Visible() || s.title.Text != "Lab-Server" {
		t.Fatalf("mode=%v local selected=%v", s.mode, s.localRow.Selected())
	}
	if s.tileIcon.Resource() != icons.GetStroke(icons.Server, icons.Accent, 1.5) {
		t.Fatal("tile kept the chip")
	}
}

func TestSearchKeepsLocalSection(t *testing.T) {
	s := newLocalStart(t, true)
	s.search.SetText("no-such-device")
	if len(s.rows) != 0 {
		t.Fatalf("%d favorites match", len(s.rows))
	}
	if !s.localSection.Visible() {
		t.Fatal("search hid \"This computer\"")
	}
}

// TestLocalDetailSurvivesReload: a reload of the favorites file keeps "This computer" selected.
func TestLocalDetailSurvivesReload(t *testing.T) {
	s := newLocalStart(t, true)
	test.Tap(s.localRow)
	s.reload()
	if s.mode != modeLocal || !s.localRow.Selected() || !s.localCard.Visible() {
		t.Fatalf("mode=%v after reload", s.mode)
	}
}

func hasTextSuffix(o fyne.CanvasObject, suffix string) bool {
	found := false
	walk(o, func(c fyne.CanvasObject) {
		if t, ok := c.(*canvas.Text); ok && strings.HasSuffix(t.Text, suffix) {
			found = true
		}
	})
	return found
}
