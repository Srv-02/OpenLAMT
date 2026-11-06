package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func TestSidebarKeepsItsWidthWithLongHost(t *testing.T) {
	h := newStart(t)
	h.s.mgr.Upsert(favorites.Favorite{Host: strings.Repeat("a", 60) + ".example.com", Username: "admin"})
	h.reload(t)
	if w := h.s.list.MinSize().Width; w > style.SidebarWidth {
		t.Fatalf("list MinSize width = %v, want <= %v", w, style.SidebarWidth)
	}
}

func TestManagerButton(t *testing.T) {
	test.NewTempApp(t)
	n := 0
	b := newManagerIconButton(icons.Edit, func() { n++ })
	if s := b.MinSize(); s.Width != style.IconButtonWidth || s.Height != style.IconButtonHeight {
		t.Fatalf("size %v", s)
	}
	test.Tap(b)
	b.TypedRune(' ')
	b.Disable()
	test.Tap(b)
	if n != 2 {
		t.Fatalf("%d taps", n)
	}
	tb := newManagerTextButton(icons.Add, "Quick Connect", nil)
	if s := tb.MinSize(); s.Height != style.NewConnectionRowHeight || s.Width < 100 {
		t.Fatalf("size %v", s)
	}
	test.Tap(tb) // no callback: no panic
}
