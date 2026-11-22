package ui

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// testList is a test-only list page of names over a fake read.
type testList struct {
	names []string
	err   error
	src   *manageSource[[]string]
	page  *listPage
	tap   int // taps of the row buttons
}

// testListDef registers a More page "Things" built on listPage; tl is filled when it is built.
func testListDef(tl *testList, refresh, footer, action bool, tools ...fyne.CanvasObject) managePageDef {
	return managePageDef{section: sectionMore, order: 10, icon: icons.Settings,
		names: func() (string, string) { return "Things", "Things" },
		build: func(m *manager, compact bool) *managePane {
			tl.src = newManageSource(m, func() ([]string, error) { return tl.names, tl.err })
			spec := listSpec{columns: []listColumn{{title: "Name", width: 160, compactWidth: 72}, {title: "Note"}},
				tools: tools, src: tl.src, emptyTitle: "No things", emptyText: "Add a thing to see it here.", footer: footer}
			if action {
				spec.action = &listAction{width: 90, compactWidth: 54}
			}
			if refresh {
				spec.refresh = tl.src.reload
			}
			p := m.newListPage(compact, spec)
			tl.page = p
			render := func() {
				var rows []listRow
				for i, n := range tl.src.val {
					r := listRow{cells: [][]widget.RichTextSegment{
						{cellSeg(n, "foreground", "text", false)}, {cellSeg("note "+n, "foreground", "text", false)}}}
					if action && i != 1 {
						r.action = newUserEditButton(compact, false, func() { tl.tap++ })
					}
					rows = append(rows, r)
				}
				p.update(rows, tl.src.loaded(), tl.src.loading, tl.src.err)
				p.setFooter("Times in UTC.")
			}
			p.pane.shown = tl.src.ensure
			p.pane.close = tl.src.subscribe(render)
			render()
			return p.pane
		}}
}

func newListHarness(t *testing.T, tl *testList, refresh, footer, action bool, tools ...fyne.CanvasObject) *manageHarness {
	t.Helper()
	englishUI(t)
	withManagePages(t, testListDef(tl, refresh, footer, action, tools...))
	return newManageHarness(t, &session.Fake{FP: certFP, HW: exampleHW()})
}

// showWindowPage opens the management window on the test page and returns its pane.
func showWindowPage(h *manageHarness) *managePane {
	h.m.OpenWindow()
	test.Tap(h.m.win.nav[5])
	return h.m.win.view.panes[5]
}

func TestListPageEmptyStateAndFooter(t *testing.T) {
	tl := &testList{}
	h := newListHarness(t, tl, true, true, true)
	showWindowPage(h)
	p := tl.page
	if p.empty.Visible() || !p.table.Visible() {
		t.Fatal("empty card shown before the first read")
	}
	h.wait(t)
	if !p.empty.Visible() || p.table.Visible() {
		t.Error("empty card hidden after a read without rows")
	}
	if !p.footer.Visible() || p.footer.Text != "Times in UTC." {
		t.Errorf("footer = %q visible %v", p.footer.Text, p.footer.Visible())
	}
	p.setFooter("")
	if p.footer.Visible() {
		t.Error("footer stays visible without text")
	}
	tl.names = []string{"alpha", "beta", "gamma"}
	tl.src.reload()
	h.wait(t)
	if p.empty.Visible() || !p.table.Visible() || len(p.table.lines) != 3 {
		t.Errorf("with rows: empty %v table %v lines %d", p.empty.Visible(), p.table.Visible(), len(p.table.lines))
	}
}

func TestListPageNoEmptyCardWithoutTitle(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: certFP, HW: exampleHW()})
	p := h.m.newListPage(false, listSpec{columns: []listColumn{{title: "Name"}}})
	p.update(nil, true, false, nil)
	if p.empty != nil || p.footer != nil || p.refresh != nil || !p.table.Visible() {
		t.Error("a page without empty state, footer and refresh builds them or hides the table")
	}
	p.setFooter("ignored") // no footer: no effect
	if p.pane.tools != nil {
		t.Error("a page without tools has a tools object")
	}
}

func TestListPageRefreshDisabledWhileLoading(t *testing.T) {
	tl := &testList{names: []string{"alpha"}}
	h := newListHarness(t, tl, true, false, false)
	showWindowPage(h)
	p := tl.page
	if !p.refresh.Disabled() {
		t.Error("Refresh enabled during the first read")
	}
	h.wait(t)
	if p.refresh.Disabled() {
		t.Error("Refresh disabled after the read")
	}
	test.Tap(p.refresh)
	if !p.refresh.Disabled() || tl.src.reads != 2 {
		t.Errorf("after Refresh: disabled %v reads %d", p.refresh.Disabled(), tl.src.reads)
	}
	h.wait(t)
	if p.refresh.Disabled() {
		t.Error("Refresh stays disabled")
	}
	if len(p.table.lines) != 1 || p.pane.tools != p.refresh {
		t.Errorf("lines %d, tools is the Refresh button: %v", len(p.table.lines), p.pane.tools == p.refresh)
	}
}

func TestListPageRowWithoutAction(t *testing.T) {
	tl := &testList{names: []string{"alpha", "beta", "gamma"}}
	h := newListHarness(t, tl, false, false, true)
	showWindowPage(h)
	h.wait(t)
	lines := tl.page.table.lines
	if len(lines) != 3 || lines[0].action == nil || lines[1].action != nil || lines[2].action == nil {
		t.Fatalf("actions: %+v", lines)
	}
	for _, o := range tl.page.table.CreateRenderer().Objects() {
		if o == nil {
			t.Fatal("nil object in the renderer")
		}
	}
	test.Tap(lines[2].action)
	if tl.tap != 1 {
		t.Errorf("taps = %d", tl.tap)
	}
	// the row height of a row without action comes from its text only
	cols := tl.page.table.columns(700)
	if h1, h0 := tl.page.table.rowHeight(lines[1], cols), tl.page.table.rowHeight(lines[0], cols); h1 >= h0 {
		t.Errorf("row height without action %v, with %v", h1, h0)
	}
}

func TestListPageWriteReloadsAndShowsError(t *testing.T) {
	tl := &testList{names: []string{"alpha"}}
	h := newListHarness(t, tl, false, false, false)
	showWindowPage(h)
	h.wait(t)
	win := h.m.win.win
	if tl.src.reads != 1 || win.Canvas().Overlays().Top() != nil {
		t.Fatalf("reads %d, overlay %v", tl.src.reads, win.Canvas().Overlays().Top())
	}
	tl.page.write(func() error { return nil })
	h.wait(t) // the write ended: the source reads again
	if tl.src.reads != 2 || win.Canvas().Overlays().Top() != nil {
		t.Errorf("after a write: reads %d, overlay %v", tl.src.reads, win.Canvas().Overlays().Top())
	}
	h.wait(t) // the new read
	tl.page.write(func() error { return errors.New("write failed") })
	h.wait(t)
	if tl.src.reads != 3 || win.Canvas().Overlays().Top() == nil {
		t.Errorf("after a failed write: reads %d, overlay %v", tl.src.reads, win.Canvas().Overlays().Top())
	}
	h.wait(t)
}

func TestManageWriteLate(t *testing.T) {
	englishUI(t)
	h := newManageHarness(t, &session.Fake{FP: certFP, HW: exampleHW()})
	var got []string
	h.m.Close()
	manageWrite(h.m, nil, func() string { return "r" }, func(string) { got = append(got, "done") }, func(r string) { got = append(got, "late "+r) })
	h.wait(t)
	manageWrite(h.m, nil, func() string { return "r" }, nil, nil) // nil callbacks drop the result
	h.wait(t)
	if len(got) != 1 || got[0] != "late r" {
		t.Errorf("callbacks = %v", got)
	}
}

func TestListPageTools(t *testing.T) {
	t.Run("window with a single tool", func(t *testing.T) {
		tl := &testList{}
		add := newManageButton("+ Add thing", func() {})
		pane := showWindowPage(newListHarness(t, tl, false, false, false, add))
		if pane.tools != fyne.CanvasObject(add) {
			t.Errorf("pane.tools = %T", pane.tools)
		}
	})
	t.Run("window with two tools", func(t *testing.T) {
		tl := &testList{}
		add := newManageButton("+ Add thing", func() {})
		pane := showWindowPage(newListHarness(t, tl, true, false, false, add))
		if pane.tools == fyne.CanvasObject(add) || pane.tools == nil {
			t.Errorf("pane.tools = %T", pane.tools)
		}
	})
	t.Run("sidebar", func(t *testing.T) {
		tl := &testList{}
		add := newManageButton("+ Add thing", func() {})
		h := newListHarness(t, tl, true, false, false, add)
		h.m.Sidebar()
		sp := h.m.side.view.show(5)
		if sp.tools != nil {
			t.Error("the sidebar page has window tools")
		}
		row, ok := sp.body.(*fyne.Container).Objects[0].(*fyne.Container)
		if !ok || len(row.Objects) != 3 || row.Objects[1] != fyne.CanvasObject(add) || row.Objects[2] != fyne.CanvasObject(tl.page.refresh) {
			t.Fatalf("sidebar tools row: %+v", sp.body.(*fyne.Container).Objects[0])
		}
	})
}
