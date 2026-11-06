package ui

import (
	"errors"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/typing"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// latestWriter keeps quick successive writes of one favorite field in order: every change takes
// a ticket on the Fyne thread, and a write whose ticket is older than one already written is
// dropped, so the last choice is the one that ends up in the file.
type latestWriter struct {
	next uint64 // Fyne thread
	mu   sync.Mutex
	done uint64
}

func (w *latestWriter) ticket() uint64 { w.next++; return w.next }

func (w *latestWriter) write(ticket uint64, fn func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ticket < w.done {
		return nil
	}
	w.done = ticket
	return fn()
}

// layoutChoices are the entries of the layout part of the Keyboard menu: Default (empty, the
// global setting decides) followed by every layout of layoutNames.
func layoutChoices() []string {
	choices := []string{""}
	for _, l := range layoutNames() {
		choices = append(choices, string(l))
	}
	return choices
}

func layoutChoiceLabel(c string) string {
	if c == "" {
		return lang.L("Default")
	}
	return layoutLabel(typing.Layout(c))
}

// layoutChoiceOf is a stored layout name as a menu choice; unknown names count as Default.
func layoutChoiceOf(stored string) string {
	if l, ok := typing.ParseLayout(stored); ok {
		return string(l)
	}
	return ""
}

// buildKeyboardMenu creates the Keyboard menu button: the layout of "Type clipboard" (Default,
// then every layout by name).
func (d *DeviceWindow) buildKeyboardMenu() {
	d.keyboard = widgets.NewToolbarMenuButton(icons.Keyboard, lang.L("Keyboard"), d.showKeyboardMenu)
	d.layoutItems = map[string]*fyne.MenuItem{}
	var items []*fyne.MenuItem
	for _, c := range layoutChoices() {
		it := fyne.NewMenuItem(layoutChoiceLabel(c), func() { d.setLayout(c) })
		d.layoutItems[c] = it
		items = append(items, it)
	}
	d.keyboardMenu = fyne.NewMenu("", items...)
	d.refreshKeyboardItems()
}

// refreshKeyboardItems sets the check mark of the chosen layout.
func (d *DeviceWindow) refreshKeyboardItems() {
	for c, it := range d.layoutItems {
		it.Checked = c == d.layoutChoice
	}
	if d.keyboardPopup != nil {
		d.keyboardPopup.Refresh()
	}
}

func (d *DeviceWindow) showKeyboardMenu() {
	if d.keyboardPopup != nil || d.aborted {
		return
	}
	d.refreshKeyboardItems()
	d.keyboardPopup = d.showMenu(d.keyboard, d.keyboardMenu, func() { d.keyboardPopup = nil })
}

// setLayout is the user's choice of the layout for "Type clipboard": it applies at once (every
// run reads it again) and is stored with the favorite; a device that is not a favorite keeps
// it for this window only.
func (d *DeviceWindow) setLayout(choice string) {
	d.layoutChoice, d.layoutChosen = choice, true
	d.refreshKeyboardItems()
	ticket := d.layoutWrite.ticket()
	d.saveFavorite(func(s favoriteStore, host, user string) error {
		return d.layoutWrite.write(ticket, func() error { return s.SetLayout(host, user, choice) })
	})
}

// adoptFavorite takes over the layout and the auto-connect override the favorites form stored
// (the file is already written).
func (d *DeviceWindow) adoptFavorite(layout, autoConnect string) {
	d.layoutChoice, d.layoutChosen = layoutChoiceOf(layout), true
	d.autoOverride = autoConnect
	d.refreshKeyboardItems()
}

// saveFavorite runs write on a goroutine. ErrNotFound (not a favorite) is not an error; other
// failures are reported, and a stored change tells the start window to reload.
func (d *DeviceWindow) saveFavorite(write func(s favoriteStore, host, user string) error) {
	store := d.layouts
	if store == nil {
		return
	}
	cfg := d.sess.Config()
	go func() {
		err := write(store, cfg.Host, cfg.Username)
		fyne.Do(func() {
			if err != nil && !errors.Is(err, favorites.ErrNotFound) && !d.closed {
				d.onError(err)
			}
			if err == nil && d.onFavoriteChanged != nil {
				d.onFavoriteChanged()
			}
			if d.onKeyboardSaved != nil {
				d.onKeyboardSaved()
			}
		})
	}()
}
