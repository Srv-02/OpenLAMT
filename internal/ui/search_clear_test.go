package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// findClear returns the clear button of the search field built by newSearchField.
func findClear(o fyne.CanvasObject) *managerButton {
	switch v := o.(type) {
	case *managerButton:
		if v.icon == icons.Clear {
			return v
		}
	case *fyne.Container:
		for _, c := range v.Objects {
			if b := findClear(c); b != nil {
				return b
			}
		}
	}
	return nil
}

func TestSearchClearButtonFollowsText(t *testing.T) {
	e := widget.NewEntry()
	field := newSearchField(e)
	btn := findClear(field)
	if btn == nil {
		t.Fatal("no clear button in the search field")
	}
	if btn.Visible() {
		t.Fatal("clear button visible with an empty field")
	}
	e.SetText("abc")
	if !btn.Visible() {
		t.Fatal("clear button hidden with text")
	}
	e.SetText("")
	if btn.Visible() {
		t.Fatal("clear button still visible after the text was removed")
	}
}

func TestSearchClearButtonEmptiesFieldAndShowsAllFavorites(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h,
		favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"},
		favorites.Favorite{Name: "Keller", Host: "192.0.2.10", Username: "admin"})
	all := len(rowNames(s))
	if all != 2 {
		t.Fatalf("rows: %v", rowNames(s))
	}
	test.Type(s.search, "kel")
	if len(rowNames(s)) != 1 {
		t.Fatalf("filter: %v", rowNames(s))
	}
	btn := findClear(s.win.Content())
	if btn == nil || !btn.Visible() {
		t.Fatal("clear button not visible with text")
	}
	test.Tap(btn)
	if s.search.Text != "" {
		t.Fatalf("field not cleared: %q", s.search.Text)
	}
	if got := rowNames(s); len(got) != all {
		t.Fatalf("filter not reset: %v", got)
	}
	if btn.Visible() {
		t.Fatal("clear button visible after clearing")
	}
}
