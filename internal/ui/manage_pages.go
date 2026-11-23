package ui

import (
	"fmt"
	"slices"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// Registry of the device management pages. Every page registers itself in an init() of its own
// file, so a new page needs no change in manage.go.
//
// Order keys, unique per section (leave gaps; parallel changes must not take the same key):
//
//	main:        Hardware 10, Event log 20, Audit log 30, Settings 40, Users 50
//	more:        Power 10, Wi-Fi 20, System Defense 30, Certificates 40
//	diagnostics: WSMAN Browser 10

// manageSection is where a page sits in the navigation.
type manageSection int

const (
	sectionMain        manageSection = iota // a sidebar segment of its own, top of the window navigation
	sectionMore                             // sidebar: the "More" menu; window: below a separator
	sectionDiagnostics                      // window: group "Diagnostics"; sidebar: the "More" menu opens the window
)

// managePageDef is one registered page.
type managePageDef struct {
	section manageSection
	order   int                        // place inside the section, ascending; unique per section
	names   func() (tab, title string) // translated when a manager is built (lang.L literals, so the i18n scan finds them)
	icon    icons.Name
	build   func(m *manager, compact bool) *managePane
}

var managePageDefs []managePageDef

// registerManagePage adds a page. It panics on a missing names or build and on a (section,
// order) another page already has.
func registerManagePage(d managePageDef) {
	if d.names == nil || d.build == nil {
		panic("ui: management page without names or build")
	}
	for _, o := range managePageDefs {
		if o.section == d.section && o.order == d.order {
			panic(fmt.Sprintf("ui: management pages share section %d, order %d", d.section, d.order))
		}
	}
	managePageDefs = append(managePageDefs, d)
}

// managePages builds the pages of m: sorted by section, then order, names translated.
func managePages(m *manager) []managePage {
	defs := slices.Clone(managePageDefs)
	slices.SortFunc(defs, func(a, b managePageDef) int {
		if a.section != b.section {
			return int(a.section) - int(b.section)
		}
		return a.order - b.order
	})
	pages := make([]managePage, len(defs))
	for i, d := range defs {
		tab, title := d.names()
		pages[i] = managePage{section: d.section, tab: tab, title: title, icon: d.icon,
			build: func(compact bool) *managePane { return d.build(m, compact) }}
	}
	return pages
}
