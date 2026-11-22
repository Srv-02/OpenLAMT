package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// withManagePages adds test pages to the registry until the test ends. Call it before
// newManageHarness.
func withManagePages(t *testing.T, defs ...managePageDef) {
	t.Helper()
	old := managePageDefs
	t.Cleanup(func() { managePageDefs = old })
	managePageDefs = slices.Clone(old)
	for _, d := range defs {
		registerManagePage(d)
	}
}

// testPageDef is a page with a label as body and the name as tab and title.
func testPageDef(section manageSection, order int, name string, icon icons.Name) managePageDef {
	return managePageDef{section: section, order: order, icon: icon,
		names: func() (string, string) { return name, name + " page" },
		build: func(m *manager, compact bool) *managePane {
			return &managePane{body: widget.NewLabel(name)}
		}}
}

// withExtraPages registers Alpha, Beta (More) and Gamma (Diagnostics), Gamma first.
func withExtraPages(t *testing.T) {
	t.Helper()
	withManagePages(t,
		testPageDef(sectionDiagnostics, 10, "Gamma", icons.Key),
		testPageDef(sectionMore, 20, "Beta", icons.Server),
		testPageDef(sectionMore, 10, "Alpha", icons.Settings))
}

func pageTitles(m *manager) []string {
	var out []string
	for _, p := range m.pages {
		out = append(out, p.title)
	}
	return out
}

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	f()
}

func TestManagePageRegistryOrder(t *testing.T) {
	englishUI(t)
	five := []string{"Hardware", "Event log", "Audit log", "Settings", "Users"}
	m := &manager{}
	m.pages = managePages(m)
	if got := pageTitles(m); !slices.Equal(got, five) {
		t.Fatalf("production pages %v", got)
	}
	for i, p := range m.pages {
		if p.section != sectionMain || p.build == nil {
			t.Errorf("page %d: section %v", i, p.section)
		}
	}

	// registration order does not matter: section first, then order
	withManagePages(t,
		testPageDef(sectionDiagnostics, 10, "Gamma", icons.Key),
		testPageDef(sectionMore, 20, "Beta", icons.Server),
		testPageDef(sectionMore, 5, "Alpha", icons.Settings))
	m.pages = managePages(m)
	want := append(slices.Clone(five), "Alpha page", "Beta page", "Gamma page")
	if got := pageTitles(m); !slices.Equal(got, want) {
		t.Fatalf("pages %v, want %v", got, want)
	}
	if m.pages[5].tab != "Alpha" || m.pages[5].section != sectionMore || m.pages[7].section != sectionDiagnostics {
		t.Errorf("tab %q sections %v %v", m.pages[5].tab, m.pages[5].section, m.pages[7].section)
	}

	mustPanic(t, "a duplicate (section, order)", func() { registerManagePage(testPageDef(sectionMore, 20, "Dup", icons.Key)) })
	mustPanic(t, "a page without build", func() {
		d := testPageDef(sectionMore, 99, "X", icons.Key)
		d.build = nil
		registerManagePage(d)
	})
	mustPanic(t, "a page without names", func() {
		d := testPageDef(sectionMore, 98, "X", icons.Key)
		d.names = nil
		registerManagePage(d)
	})
	// the same order in another section is fine
	registerManagePage(testPageDef(sectionMore, 10, "Other", icons.Key))
}

func moreHarness(t *testing.T) *manageHarness {
	t.Helper()
	englishUI(t)
	withExtraPages(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.Sidebar()
	return h
}

func TestManageSidebarMoreSegment(t *testing.T) {
	h := moreHarness(t)
	side, tabs := h.m.side, h.m.side.tabs
	if tabs.Len() != 6 || tabs.Label(5) != "More" || side.more != 5 {
		t.Fatalf("segments %d, last %q, more %d", tabs.Len(), tabs.Label(5), side.more)
	}

	// tapping More opens the menu and keeps the selection
	tabs.Tap(4)
	tabs.Tap(5)
	if side.morePopup == nil || !side.morePopup.Visible() || tabs.Selected() != 4 || side.view.current != 4 {
		t.Fatalf("popup %v selected %d current %d", side.morePopup, tabs.Selected(), side.view.current)
	}
	var labels []string
	var seps []int
	for i, it := range side.moreMenu.Items {
		labels = append(labels, it.Label)
		if it.IsSeparator {
			seps = append(seps, i)
		}
	}
	if !slices.Equal(labels, []string{"Alpha page", "Beta page", "", "Gamma page"}) || !slices.Equal(seps, []int{2}) {
		t.Fatalf("menu %q separators %v", labels, seps)
	}
	if side.moreMenu.Items[0].Icon != icons.Get(icons.Settings, icons.Foreground) {
		t.Error("page symbol missing")
	}

	// choosing Beta: its pane, caption and selection
	side.morePopup.Item(1).Tapped(nil)
	if side.morePopup != nil || side.view.current != 6 || tabs.Label(5) != "Beta" || tabs.Selected() != 5 {
		t.Fatalf("after Beta: current %d caption %q selected %d", side.view.current, tabs.Label(5), tabs.Selected())
	}
	if !side.view.slots[6].Visible() || side.view.slots[4].Visible() {
		t.Error("Beta's pane is not the visible one")
	}

	// tapping the selected More opens the menu again; choosing Alpha changes the caption
	tabs.Tap(5)
	if side.morePopup == nil || tabs.Selected() != 5 {
		t.Fatal("reselecting More must open the menu")
	}
	side.morePopup.Item(0).Tapped(nil)
	if tabs.Label(5) != "Alpha" || side.view.current != 5 {
		t.Fatalf("caption %q current %d", tabs.Label(5), side.view.current)
	}

	// a main segment restores the caption
	tabs.Tap(0)
	if tabs.Label(5) != "More" || tabs.Selected() != 0 || side.view.current != 0 {
		t.Fatalf("caption %q selected %d current %d", tabs.Label(5), tabs.Selected(), side.view.current)
	}
}

func TestManageSidebarMoreMenuKeepsSelectionWhenDismissed(t *testing.T) {
	h := moreHarness(t)
	side, tabs := h.m.side, h.m.side.tabs
	tabs.Tap(3)
	tabs.Tap(5)
	side.morePopup.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if side.morePopup != nil || tabs.Selected() != 3 || side.view.current != 3 || tabs.Label(5) != "More" {
		t.Fatalf("Escape: popup %v selected %d current %d", side.morePopup, tabs.Selected(), side.view.current)
	}

	tabs.Tap(5)
	side.morePopup.Tapped(&fyne.PointEvent{Position: fyne.NewPos(1, 1)}) // outside the panel
	if side.morePopup != nil || tabs.Selected() != 3 || side.view.current != 3 {
		t.Fatalf("tap outside: popup %v selected %d", side.morePopup, tabs.Selected())
	}

	// with a More page active, dismissing keeps it
	tabs.Tap(5)
	side.morePopup.Item(0).Tapped(nil)
	tabs.Tap(5)
	side.morePopup.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if tabs.Selected() != 5 || tabs.Label(5) != "Alpha" || side.view.current != 5 {
		t.Fatalf("selected %d caption %q current %d", tabs.Selected(), tabs.Label(5), side.view.current)
	}
}

func TestManageSidebarRightArrowOpensMore(t *testing.T) {
	h := moreHarness(t)
	side, tabs := h.m.side, h.m.side.tabs
	tabs.Tap(4)
	tabs.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if side.morePopup == nil || tabs.Selected() != 4 {
		t.Fatalf("popup %v selected %d", side.morePopup, tabs.Selected())
	}
}

func TestManageSidebarDiagnosticsItemOpensWindow(t *testing.T) {
	h := moreHarness(t)
	side, tabs := h.m.side, h.m.side.tabs
	popped := 0
	h.m.SetOnPopOut(func() { popped++ })
	tabs.Tap(5)
	side.morePopup.Item(3).Tapped(nil)
	if popped != 1 || h.m.win == nil || h.m.win.view.current != 7 || side.view.current != 0 || tabs.Selected() != 0 {
		t.Fatalf("popped %d win %v sidebar page %d selected %d", popped, h.m.win, side.view.current, tabs.Selected())
	}
	w := h.m.win
	if len(w.nav) != len(h.m.pages) || !w.nav[7].Active() || w.title.Text != "Gamma page" {
		t.Errorf("nav %d active %v title %q", len(w.nav), w.nav[7].Active(), w.title.Text)
	}
	// separator before the More pages, heading before the diagnostics pages
	at := func(o fyne.CanvasObject) int {
		if t, ok := o.(*canvas.Text); ok { // the heading sits in a padded container
			return slices.IndexFunc(w.navBox.Objects, func(x fyne.CanvasObject) bool {
				c, ok := x.(*fyne.Container)
				return ok && len(c.Objects) == 1 && c.Objects[0] == t
			})
		}
		return slices.Index(w.navBox.Objects, o)
	}
	if w.navSep == nil || w.navHeading == nil || w.navHeading.Text != "Diagnostics" {
		t.Fatal("window nav lacks separator or heading")
	}
	if !(at(w.nav[4]) < at(w.navSep) && at(w.navSep) < at(w.nav[5]) && at(w.nav[6]) < at(w.navHeading) && at(w.navHeading) < at(w.nav[7])) {
		t.Errorf("order: users %d sep %d alpha %d beta %d heading %d gamma %d", at(w.nav[4]), at(w.navSep), at(w.nav[5]),
			at(w.nav[6]), at(w.navHeading), at(w.nav[7]))
	}

	// an open window switches the page
	h.m.openWindowAt(6)
	if h.m.win != w || w.view.current != 6 || !w.nav[6].Active() || w.nav[7].Active() || w.title.Text != "Beta page" {
		t.Errorf("open window: current %d title %q", w.view.current, w.title.Text)
	}
	// OpenWindow keeps its behavior: it only brings the open window forward
	h.m.OpenWindow()
	if w.view.current != 6 {
		t.Errorf("OpenWindow switched to page %d", w.view.current)
	}
}

func TestManageOpenWindowStartsOnSidebarMorePage(t *testing.T) {
	h := moreHarness(t)
	h.m.side.tabs.Tap(5)
	h.m.side.morePopup.Item(1).Tapped(nil)
	h.m.OpenWindow()
	if h.m.win == nil || h.m.win.view.current != 6 {
		t.Fatalf("window on page %v", h.m.win)
	}
}

func TestManageCloseDismissesMoreMenu(t *testing.T) {
	h := moreHarness(t)
	h.m.side.tabs.Tap(5)
	popup := h.m.side.morePopup
	if popup == nil {
		t.Fatal("no menu")
	}
	h.m.Close()
	if popup.Visible() || h.m.side.morePopup != nil {
		t.Error("Close left the More menu open")
	}
}

func TestManageNoMoreWithoutExtraPages(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.Sidebar()
	tabs := h.m.side.tabs
	if tabs.Len() != 5 || h.m.side.more != -1 {
		t.Fatalf("segments %d more %d", tabs.Len(), h.m.side.more)
	}
	tabs.Tap(4)
	tabs.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if h.m.side.morePopup != nil || tabs.Selected() != 4 {
		t.Error("Right arrow past the last tab opened a menu")
	}
	h.m.OpenWindow()
	w := h.m.win
	if w.navSep != nil || w.navHeading != nil || len(w.navBox.Objects) != 1+5 {
		t.Errorf("sep %v heading %v objects %d", w.navSep, w.navHeading, len(w.navBox.Objects))
	}
}

// With only a diagnostics page the menu has no separator; the window nav no separator line.
func TestManageOnlyDiagnosticsPage(t *testing.T) {
	englishUI(t)
	withManagePages(t, testPageDef(sectionDiagnostics, 10, "Gamma", icons.Key))
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW()})
	h.m.Sidebar()
	h.m.side.tabs.Tap(5)
	items := h.m.side.moreMenu.Items
	if len(items) != 1 || items[0].IsSeparator {
		t.Fatalf("menu items %d", len(items))
	}
	h.m.side.morePopup.Dismiss()
	h.m.OpenWindow()
	if h.m.win.navSep != nil || h.m.win.navHeading == nil {
		t.Errorf("sep %v heading %v", h.m.win.navSep, h.m.win.navHeading)
	}
}

// The sidebar never shows a diagnostics pane: the segment of a diagnostics page is not
// selectable, the page only opens in the window.
func TestManageWindowOnlyPageNotInSidebar(t *testing.T) {
	h := moreHarness(t)
	side := h.m.side
	for _, i := range []int{0, 1, 2, 3, 4} {
		side.tabs.Tap(i)
	}
	for _, item := range []int{0, 1} {
		side.tabs.Tap(5)
		side.morePopup.Item(item).Tapped(nil)
	}
	if side.view.panes[7] != nil || side.view.current >= 7 {
		t.Errorf("the sidebar built the diagnostics page: current %d", side.view.current)
	}
	for page, want := range map[int]int{0: 0, 4: 4, 5: 5, 6: 5} {
		if got := side.tabIndex(page); got != want {
			t.Errorf("page %d is on segment %d, want %d", page, got, want)
		}
	}
}
