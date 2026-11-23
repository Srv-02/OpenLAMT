package ui

import (
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// "This computer" (#11, design V15): a sidebar section above "Favorites" that shows only when
// this computer has an Intel ME device. The detection runs once at start, without
// administrator rights (internal/localme). Checking, turning on, activating and deactivating
// Intel AMT through the device are not built yet: the detail shows the unchecked state only.

// buildLocalSection returns the hidden section: heading and the "This computer" row.
func (s *StartWindow) buildLocalSection() fyne.CanvasObject {
	s.localRow = widgets.NewSidebarRow(lang.L("This computer"), lang.L("Intel ME found, not checked"))
	s.localRow.SetIcon(icons.Hardware)
	s.localRow.SetDot(widgets.DotUnknown)
	s.localRow.OnTapped = s.selectLocal
	heading := container.New(layout.NewCustomPaddedLayout(8, 6, 20, 20), sectionLabel(lang.L("This computer")))
	row := container.New(layout.NewCustomPaddedLayout(0, 4, 10, 10), s.localRow)
	s.localSection = container.NewVBox(heading, row)
	s.localSection.Hide()
	return s.localSection
}

// detectLocal runs the detection and shows the section when an Intel ME device is present.
func (s *StartWindow) detectLocal() {
	s.meFound = s.localME != nil && s.localME()
	setShown(s.localSection, s.meFound)
}

// selectLocal shows the detail of "This computer"; an edit in progress is discarded.
func (s *StartWindow) selectLocal() {
	s.selKey, s.dupOf = "", nil
	s.markSelection()
	s.setMode(modeLocal)
}

// localMeta is the metadata line of the "This computer" detail: "<hostname> · Intel ME found".
func localMeta() string {
	found := lang.L("Intel ME found")
	if h, err := os.Hostname(); err == nil && h != "" {
		return h + " · " + found
	}
	return found
}

// newLocalCard is the card of the unchecked state.
func newLocalCard() fyne.CanvasObject {
	return newEmptyCard(lang.L("Not checked yet"),
		lang.L("Check reads Intel AMT through the Intel ME driver of this computer. Nothing is changed."))
}
