package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Geometry of the device window (SessionA.html, SessionA2.html).
const (
	toolbarPadLeft   float32 = 18
	toolbarPadRight  float32 = 12
	toolbarGap       float32 = 6
	dividerMargin    float32 = 4
	dividerHeight    float32 = 30
	menuOffsetX      float32 = -8 // menu left edge relative to its toolbar button
	menuOffsetY      float32 = 58 // menu top relative to the top of the 52 px item
	statusPad        float32 = 14
	statusGap        float32 = 16
	hostTextSize     float32 = 11
	emptyTileSize    float32 = 72
	emptyTileRadius  float32 = 20
	emptyIconSize    float32 = 34
	emptyTextWidth   float32 = 360
	emptyGap         float32 = 14
	emptyButtonGap   float32 = 6
	bannerIconSize   float32 = 16
	statusLockSize   float32 = 12
	statusLockGap    float32 = 5
	statusDotTextGap float32 = 6
	dotNameGap       float32 = 10 // toolbar dot to name and host, as the sidebar row gap
)

// buildContent lays out toolbar, banners, the stages with the sidebar and the status bar.
func (d *DeviceWindow) buildContent() fyne.CanvasObject {
	top := container.New(&columnLayout{fill: -1}, d.buildToolbar(), hline(style.ColorNameSeparatorDark), d.buildWarning(), d.buildMessage())
	d.body = container.New(manageSideLayout{}, d.buildStage(), d.buildManagePane())
	content := container.New(&columnLayout{fill: 1}, top, d.body, hline(style.ColorNameSeparatorDark), d.buildStatusBar())
	d.setKVMControls()
	d.showPower()
	return content
}

// buildToolbar returns the 64 px toolbar: device name and host on the left, the button groups
// on the right.
func (d *DeviceWindow) buildToolbar() fyne.CanvasObject {
	cfg := d.sess.Config()
	d.title = newEllipsisText(d.name, style.Size(style.SizeNameButtonText), theme.ColorNameForeground, true)
	d.host = newEllipsisText(cfg.Host, hostTextSize, style.ColorNameTextSecondary, false)
	d.title.shrink, d.host.shrink = true, true
	names := container.New(&vCenterLayout{gap: 1}, d.title, d.host)
	d.headDot = widgets.NewStatusDot(widgets.DotUnknown, style.StatusDotSize)
	head := container.New(&dotLeadLayout{gap: dotNameGap}, d.headDot, names)

	d.powerBtn = widgets.NewToolbarMenuButton(icons.Power, lang.L("Power"), d.showPowerMenu)
	d.buildPowerMenu()
	d.buildKeyboardMenu()
	d.toolbarItems = container.New(layout.NewCustomPaddedHBoxLayout(toolbarGap),
		d.powerBtn, divider(),
		d.viewSwitch, d.mediaBtn, d.quality, divider(),
		d.cadBtn, d.clipBtn, d.keyboard, divider(),
		d.manageBtn, d.fullBtn)
	row := container.New(&leadingFillLayout{gap: toolbarGap}, head, d.toolbarItems)
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameToolbar, "")
	bg.SetMinSize(fyne.NewSize(1, style.ToolbarHeight))
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(0, 0, toolbarPadLeft, toolbarPadRight), row))
}

// divider is the thin vertical line between toolbar groups.
func divider() fyne.CanvasObject {
	r := style.BindRect(canvas.NewRectangle(nil), style.ColorNameGroupDivider, "")
	r.SetMinSize(fyne.NewSize(1, dividerHeight))
	return container.New(layout.NewCustomPaddedLayout(0, 0, dividerMargin, dividerMargin), container.NewCenter(r))
}

// hline is a 1 px horizontal line.
func hline(c fyne.ThemeColorName) fyne.CanvasObject {
	r := style.BindRect(canvas.NewRectangle(nil), c, "")
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

// newBanner is a strip below the toolbar on a red tint: TLS warning, failed status read.
func newBanner(icon fyne.Resource, text *widget.Label) fyne.CanvasObject {
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameDangerTile, "")
	var lead fyne.CanvasObject
	if icon != nil {
		img := widgets.NewSymbol(icon, bannerIconSize)
		lead = container.NewCenter(img)
	}
	body := container.NewBorder(nil, nil, lead, nil, text)
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(0, 0, toolbarPadLeft-theme.Padding(), toolbarPadRight), body))
}

func (d *DeviceWindow) buildWarning() fyne.CanvasObject {
	text := widget.NewLabel(lang.L("Connection without TLS: password and data are sent unencrypted."))
	text.Wrapping = fyne.TextWrapWord
	d.warning = newBanner(icons.Get(icons.Lock, icons.Danger), text)
	if d.sess.Config().UseTLS {
		d.warning.Hide()
	}
	return d.warning
}

func (d *DeviceWindow) buildMessage() fyne.CanvasObject {
	d.message = widget.NewLabel("")
	d.message.Wrapping = fyne.TextWrapWord
	d.messageBanner = newBanner(nil, d.message)
	d.messageBanner.Hide()
	return d.messageBanner
}

// setMessage shows text (failed status read, certificate mismatch) in the banner; empty hides it.
func (d *DeviceWindow) setMessage(text string) {
	d.message.SetText(text)
	if (text != "") == d.messageBanner.Visible() {
		return
	}
	if text == "" {
		d.messageBanner.Hide()
	} else {
		d.messageBanner.Show()
	}
	if d.content != nil {
		d.content.Refresh() // the banner changes the height of the top block
	}
}

// buildStatusBar returns the 28 px status bar: power, AMT version and control mode on the left;
// on the right the media status (only while set), TLS, the KVM state or message and the console
// status (only while set).
func (d *DeviceWindow) buildStatusBar() fyne.CanvasObject {
	text := func() *ellipsisText {
		return newEllipsisText("", style.Size(style.SizeNameSecondaryText), style.ColorNameTextSecondary, false)
	}
	d.powerDot = widgets.NewStatusDot(widgets.DotUnknown, style.StatusDotSizeSmall)
	d.power, d.version, d.mode, d.tls, d.kvmLine, d.consoleLine = text(), text(), text(), text(), text(), text()
	d.mediaLine = newEllipsisText("", style.Size(style.SizeNameSecondaryText), style.ColorNameAccentText, false)
	d.version.Hide()
	d.mode.Hide()
	d.mediaLine.Hide()
	d.consoleLine.Hide()
	d.kvmLine.shrink = true
	d.kvmLine.align = fyne.TextAlignTrailing
	power := container.New(layout.NewCustomPaddedHBoxLayout(statusDotTextGap), container.NewCenter(d.powerDot), d.power)
	left := container.New(layout.NewCustomPaddedHBoxLayout(statusGap), power, d.version, d.mode)

	lock := widgets.NewSymbol(icons.Get(icons.Lock, icons.Secondary), statusLockSize)
	if d.sess.Config().UseTLS {
		d.tls.SetText("TLS")
	} else {
		lock.Hide()
		d.tls.SetText(lang.L("No TLS"))
	}
	tls := container.New(layout.NewCustomPaddedHBoxLayout(statusLockGap), container.NewCenter(lock), d.tls)
	d.refreshKVMLine()

	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameStatusBar, "")
	bg.SetMinSize(fyne.NewSize(1, style.StatusBarHeight))
	row := container.New(&statusLayout{gap: statusGap, shrink: statusShrink}, left, d.mediaLine, tls, d.kvmLine, d.consoleLine)
	d.statusRow = row
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(0, 0, statusPad, statusPad), row))
}

// setVersion shows "AMT <version>" and "Control mode <mode>"; empty values hide their part.
func (d *DeviceWindow) setVersion(version, mode string) {
	set := func(t *ellipsisText, s string) {
		t.SetText(s)
		if s == "" {
			t.Hide()
		} else {
			t.Show()
		}
	}
	if version != "" {
		version = "AMT " + version
	}
	if mode != "" {
		mode = lang.L("Control mode {{.Mode}}", map[string]any{"Mode": mode})
	}
	set(d.version, version)
	set(d.mode, mode)
	d.relayoutStatus()
}

// relayoutStatus places the status bar texts again after one of them changed its width.
func (d *DeviceWindow) relayoutStatus() {
	if d.statusRow != nil {
		d.statusRow.Refresh()
	}
}

// showPower updates everything that follows the power state: status bar, toolbar dot, Power
// menu items, the KVM controls and the empty state.
func (d *DeviceWindow) showPower() {
	text := lang.L("Unknown")
	if d.powerKnown {
		text = powerStateText(d.powerState)
	}
	d.power.SetText(text)
	d.relayoutStatus()
	dot := powerDot(d.powerKnown, d.powerState)
	d.powerDot.SetState(dot)
	d.headDot.SetState(dot)
	if d.aborted || d.powerBusy {
		d.powerBtn.Disable()
	} else {
		d.powerBtn.Enable()
	}
	d.updatePowerItems()
	d.setKVMControls() // KVM is locked while the device is off; also refreshes the empty state
}

// poweredOff reports whether the device was read as off: then there is no screen to connect
// to, and "Connect KVM" and the KVM toggle are locked (issue #120).
func (d *DeviceWindow) poweredOff() bool {
	return d.powerKnown && !d.aborted && isOff(d.powerState)
}

// refreshKVMLine shows the transient KVM message or the plain KVM state in the status bar.
func (d *DeviceWindow) refreshKVMLine() {
	if d.kvmLine == nil {
		return
	}
	text := d.kvmStatus.Text
	if text == "" {
		text = lang.L("KVM not connected")
		if d.kvmClient != nil || d.kvmReconnecting {
			text = lang.L("KVM connected")
		}
	}
	d.kvmLine.SetText(text)
	d.relayoutStatus()
}

// buildEmptyState returns the "Remote screen not connected" panel shown over the letterbox
// while no KVM session runs.
func (d *DeviceWindow) buildEmptyState() fyne.CanvasObject {
	tileBg := style.BindRect(&canvas.Rectangle{StrokeWidth: 1, CornerRadius: emptyTileRadius},
		style.ColorNameEmptyStateTile, style.ColorNameTileBorder)
	tileBg.SetMinSize(fyne.NewSquareSize(emptyTileSize))
	img := widgets.NewSymbol(icons.GetStroke(icons.KVM, icons.Secondary, 1.4), emptyIconSize)
	tile := container.NewCenter(container.NewStack(tileBg, container.NewCenter(img)))

	title := style.BindText(canvas.NewText(lang.L("Remote screen not connected"), nil), theme.ColorNameForeground)
	title.TextSize = style.Size(style.SizeNameEmptyTitle)
	title.TextStyle.Bold = true
	title.Alignment = fyne.TextAlignCenter

	d.emptyText = widget.NewRichText(&widget.TextSegment{Style: widget.RichTextStyle{
		Alignment: fyne.TextAlignCenter, ColorName: style.ColorNameTextSecondary, SizeName: theme.SizeNameText}})
	d.emptyText.Wrapping = fyne.TextWrapWord
	d.emptyBtn = widgets.NewPrimaryButton(lang.L("Connect KVM"), icons.GetStroke(icons.KVM, icons.White, 2), d.toggleKVM)

	d.emptyBtnBox = container.New(layout.NewCustomPaddedLayout(emptyButtonGap, 0, 0, 0), container.NewCenter(d.emptyBtn))
	d.emptyBox = container.New(layout.NewCustomPaddedVBoxLayout(emptyGap),
		tile, title, container.New(&fixedWidthLayout{width: emptyTextWidth}, d.emptyText), d.emptyBtnBox)
	// The override pins the design theme for the custom text color (test apps use Fyne's).
	d.emptyCenter = container.NewCenter(d.emptyBox)
	d.emptyState = container.NewThemeOverride(d.emptyCenter, overrideTheme)
	return d.emptyState
}

// refreshEmptyState shows the panel without a session and adapts text and button. While the
// device is off the button is hidden (the text says why), unless a connect can be cancelled.
func (d *DeviceWindow) refreshEmptyState() {
	if d.emptyState == nil {
		return
	}
	if d.kvmClient != nil || d.kvmReconnecting {
		d.emptyState.Hide()
		return
	}
	d.emptyState.Show()
	seg := d.emptyText.Segments[0].(*widget.TextSegment)
	seg.Text = d.emptyMessage()
	d.emptyText.Refresh()
	if d.poweredOff() && !d.kvmBusy {
		d.emptyBtnBox.Hide()
	} else {
		d.emptyBtnBox.Show()
	}
	if d.kvmBusy {
		d.emptyBtn.SetText(lang.L("Cancel"))
		d.emptyBtn.SetIcon(nil)
		d.emptyBtn.Importance = widget.MediumImportance
	} else {
		d.emptyBtn.SetText(lang.L("Connect KVM"))
		d.emptyBtn.SetIcon(icons.GetStroke(icons.KVM, icons.White, 2))
		d.emptyBtn.Importance = widget.HighImportance
	}
	if d.aborted {
		d.emptyBtn.Disable()
	} else {
		d.emptyBtn.Enable()
	}
	d.emptyBtn.Refresh()
	d.emptyBox.Refresh() // text and button change their size: lay out and centre again
	d.emptyCenter.Refresh()
}

// emptyMessage is the text of the empty state for the current power state.
func (d *DeviceWindow) emptyMessage() string {
	args := map[string]any{"Name": d.name}
	switch {
	case !d.powerKnown || d.aborted:
		return lang.L("Connect KVM to see the screen of {{.Name}} and control keyboard and mouse.", args)
	case d.powerState == models.PowerStateOn:
		return lang.L("{{.Name}} is powered on. Connect KVM to see the screen and control keyboard and mouse.", args)
	case isOff(d.powerState):
		return lang.L("{{.Name}} is powered off. Power it on to connect KVM.", args)
	case isAsleep(d.powerState):
		return lang.L("{{.Name}} is asleep. Connect KVM to see the screen and control keyboard and mouse.", args)
	}
	return lang.L("Connect KVM to see the screen of {{.Name}} and control keyboard and mouse.", args)
}

// powerIcon is the menu symbol of action.
func powerIcon(a amt.PowerAction) icons.Name {
	switch a {
	case amt.PowerReset:
		return icons.Reset
	case amt.PowerCycle:
		return icons.PowerCycle
	case amt.BootToBIOS:
		return icons.BIOS
	case amt.BootToISO:
		return icons.Media
	}
	return icons.Power
}

// powerMenuLabel is the menu text of action; "…" marks the ones that ask first.
func powerMenuLabel(a amt.PowerAction) string {
	switch a {
	case amt.PowerOn:
		return lang.L("Power On")
	case amt.PowerReset:
		return lang.L("Reset…")
	case amt.PowerCycle:
		return lang.L("Power Cycle…")
	case amt.PowerShutdown:
		return lang.L("Shutdown…")
	case amt.PowerOff:
		return lang.L("Power Off…")
	case amt.BootToBIOS:
		return lang.L("Reboot to BIOS…")
	case amt.BootToISO:
		return lang.L("Boot from ISO…")
	}
	return a.String()
}

// buildPowerMenu creates the items of the Power menu: Power On, Power Off | Reset, Power Cycle,
// Reboot to BIOS, Boot from ISO | Shutdown.
func (d *DeviceWindow) buildPowerMenu() {
	d.powerItems = map[amt.PowerAction]*fyne.MenuItem{}
	var items []*fyne.MenuItem
	for _, act := range powerOrder {
		it := fyne.NewMenuItem(powerMenuLabel(act), func() { d.run(act) })
		d.powerItems[act] = it
		items = append(items, it)
		if act == amt.PowerOff || act == amt.BootToISO {
			items = append(items, fyne.NewMenuItemSeparator())
		}
	}
	d.powerMenu = fyne.NewMenu("", items...)
}

// updatePowerItems locks the items that do not apply to the power state.
func (d *DeviceWindow) updatePowerItems() {
	for act, it := range d.powerItems {
		it.Disabled = !d.powerAllowed(act)
		v := icons.Foreground
		if it.Disabled {
			v = icons.Disabled
		}
		it.Icon = icons.Get(powerIcon(act), v)
	}
	if d.powerPopup != nil {
		d.powerPopup.Refresh()
	}
	d.media.powerChanged() // "Restart from ISO…" of the Media popover
}

// showPowerMenu opens the Power menu. The state is read once more right away, followed by the
// supported power states (for Shutdown); the items follow the last known state until that read
// arrives (apply refreshes the open menu).
func (d *DeviceWindow) showPowerMenu() {
	if d.powerPopup != nil || d.aborted || d.powerBusy {
		return
	}
	d.updatePowerItems()
	d.powerPopup = d.showMenu(d.powerBtn, d.powerMenu, func() { d.powerPopup = nil })
	d.poller.RefreshPowerStates() // last: apply may update the open menu
}

// buildQualityMenu creates the Quality menu button and its two items.
func (d *DeviceWindow) buildQualityMenu() {
	d.quality = widgets.NewToolbarMenuButton(icons.KVM, lang.L("Quality"), d.showQualityMenu)
	high := fyne.NewMenuItem(lang.L("High quality (65,536 colors)"), func() { d.setQuality(kvm.QualityHigh) })
	low := fyne.NewMenuItem(lang.L("Low quality (256 colors)"), func() { d.setQuality(kvm.QualityLow) })
	d.qualityItems = map[kvm.Quality]*fyne.MenuItem{kvm.QualityHigh: high, kvm.QualityLow: low}
	d.qualityMenu = fyne.NewMenu("", high, low)
	d.applyQuality(d.settings.KVMQuality())
}

func (d *DeviceWindow) showQualityMenu() {
	if d.qualityPopup != nil || d.kvmBusy || d.kvmReconnecting || d.aborted || d.consoleView {
		return
	}
	d.qualityPopup = d.showMenu(d.quality, d.qualityMenu, func() { d.qualityPopup = nil })
}

// showMenu opens m below btn (SessionA2.html: 6 px below the item, 8 px to the left) and marks
// btn open until the menu closes. Held keys are released first: the menu takes the focus.
func (d *DeviceWindow) showMenu(btn *widgets.ToolbarButton, m *fyne.Menu, closed func()) *widgets.MenuPopup {
	d.releaseKeys()
	p := widgets.NewMenuPopup(m, d.win.Canvas())
	p.OnDismiss = func() {
		btn.SetOpen(false)
		closed()
		if !d.fullScreen {
			d.focusView()
		}
	}
	btn.SetOpen(true)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(btn)
	top := (btn.Size().Height - style.ToolbarItemHeight) / 2
	p.ShowAtPosition(pos.Add(fyne.NewPos(menuOffsetX, top+menuOffsetY)))
	return p
}

// closeMenus dismisses an open Power, Quality or Keyboard menu and the Media popover (power
// action started, abort, KVM connect).
func (d *DeviceWindow) closeMenus() {
	d.media.dismiss()
	if d.powerPopup != nil {
		d.powerPopup.Dismiss()
	}
	if d.qualityPopup != nil {
		d.qualityPopup.Dismiss()
	}
	if d.keyboardPopup != nil {
		d.keyboardPopup.Dismiss()
	}
}

// qualityBadge is the short form on the Quality button.
func qualityBadge(q kvm.Quality) string {
	if q == kvm.QualityLow {
		return "LQ"
	}
	return "HQ"
}

func favoriteQuality(q kvm.Quality) string {
	if q == kvm.QualityLow {
		return favorites.QualityLow
	}
	return favorites.QualityHigh
}

func kvmQualityOf(s string) kvm.Quality {
	if s == favorites.QualityLow {
		return kvm.QualityLow
	}
	return kvm.QualityHigh
}

// applyQuality shows q as the quality of the next connection.
func (d *DeviceWindow) applyQuality(q kvm.Quality) {
	d.kvmQuality = q
	d.quality.SetBadge(qualityBadge(q))
	for k, it := range d.qualityItems {
		it.Checked = k == q
	}
}

// setQuality is the user's choice in the Quality menu: it applies to the next connection, a
// running session reconnects with it (switchQuality), and it is stored with the favorite
// (nothing is stored for a device that is not a favorite).
func (d *DeviceWindow) setQuality(q kvm.Quality) {
	if d.kvmBusy || d.kvmReconnecting || d.aborted {
		return
	}
	d.qualityChosen = true
	d.applyQuality(q)
	d.switchQuality()
	store := d.layouts
	if store == nil {
		return
	}
	cfg := d.sess.Config()
	want := favoriteQuality(q)
	go func() {
		// One write at a time, each with the latest choice: quick changes cannot overtake.
		d.qualitySave.Lock()
		d.qualityWant = want
		d.qualitySave.Unlock()
		d.saveQuality(store, cfg.Host, cfg.Username)
	}()
}

func (d *DeviceWindow) saveQuality(store favoriteStore, host, user string) {
	d.qualitySave.Lock()
	err := store.SetQuality(host, user, d.qualityWant)
	d.qualitySave.Unlock()
	fyne.Do(func() {
		if err != nil && !errors.Is(err, favorites.ErrNotFound) && !d.closed {
			d.onError(err)
		}
		if err == nil && d.onFavoriteChanged != nil {
			d.onFavoriteChanged()
		}
		if d.onQualitySaved != nil {
			d.onQualitySaved()
		}
	})
}

// loadQuality reads the favorite's KVM quality, keyboard layout and auto-connect override on a goroutine (Find reads a file). A device that
// is not a favorite, or a file that cannot be read, keeps the global quality: the start window reports a
// broken favorites file. A choice the user made meanwhile wins.
func (d *DeviceWindow) loadQuality() {
	store := d.layouts
	if store == nil {
		return
	}
	cfg := d.sess.Config()
	go func() {
		f, found, err := store.Find(cfg.Host, cfg.Username)
		fyne.Do(func() {
			if err == nil && found && !d.qualityChosen && d.kvmClient == nil && !d.kvmBusy && !d.kvmReconnecting {
				d.applyQuality(d.settings.QualityFor(f.QualityChoice()))
			}
			if err == nil && found {
				d.autoOverride = f.AutoConnectChoice()
				d.isFavorite = true
				if !d.layoutChosen {
					d.layoutChoice = layoutChoiceOf(f.Layout)
				}
				d.refreshKeyboardItems()
			}
			d.autoLoaded = true
			d.autoConnect()
			if d.onQualityLoaded != nil {
				d.onQualityLoaded()
			}
		})
	}()
}

// ellipsisText is one line of text in a design size and color. With shrink it may get less
// width than the text needs and then ends in "…" (device name, KVM message).
type ellipsisText struct {
	widget.BaseWidget
	Text   string
	size   float32
	color  fyne.ThemeColorName
	bold   bool
	align  fyne.TextAlign
	shrink bool
}

func newEllipsisText(text string, size float32, c fyne.ThemeColorName, bold bool) *ellipsisText {
	t := &ellipsisText{Text: text, size: size, color: c, bold: bold}
	t.ExtendBaseWidget(t)
	return t
}

// SetText changes the text.
func (t *ellipsisText) SetText(s string) {
	if t.Text == s {
		return
	}
	t.Text = s
	t.Refresh()
}

func (t *ellipsisText) textStyle() fyne.TextStyle { return fyne.TextStyle{Bold: t.bold} }

// natural is the width of the full text.
func (t *ellipsisText) natural() float32 {
	return fyne.MeasureText(t.Text, t.size, t.textStyle()).Width
}

func (t *ellipsisText) CreateRenderer() fyne.WidgetRenderer {
	txt := canvas.NewText("", style.Color(t.color))
	txt.TextSize = t.size
	txt.TextStyle = t.textStyle()
	return &ellipsisRenderer{t: t, txt: txt}
}

type ellipsisRenderer struct {
	t   *ellipsisText
	txt *canvas.Text
}

func (r *ellipsisRenderer) MinSize() fyne.Size {
	h := fyne.MeasureText("Mg", r.t.size, r.t.textStyle()).Height
	if r.t.shrink {
		return fyne.NewSize(0, h)
	}
	return fyne.NewSize(r.t.natural(), h)
}

func (r *ellipsisRenderer) Layout(size fyne.Size) {
	r.txt.Text = ellipsize(r.t.Text, r.t.size, r.t.textStyle(), size.Width)
	ts := fyne.MeasureText(r.txt.Text, r.t.size, r.t.textStyle())
	x := float32(0)
	switch r.t.align {
	case fyne.TextAlignCenter:
		x = (size.Width - ts.Width) / 2
	case fyne.TextAlignTrailing:
		x = size.Width - ts.Width
	}
	r.txt.Move(fyne.NewPos(x, (size.Height-ts.Height)/2))
	r.txt.Resize(ts)
}

func (r *ellipsisRenderer) Refresh() {
	r.txt.Color = style.Color(r.t.color)
	r.Layout(r.t.Size())
	r.txt.Refresh()
}

func (r *ellipsisRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.txt} }
func (r *ellipsisRenderer) Destroy()                     {}

// ellipsize shortens s with "…" so that it fits width at the given size and style.
func ellipsize(s string, size float32, st fyne.TextStyle, width float32) string {
	if fyne.MeasureText(s, size, st).Width <= width {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r) // largest n with r[:n]+"…" fitting
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(string(r[:mid])+"…", size, st).Width <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ""
	}
	return string(r[:lo]) + "…"
}

// columnLayout stacks its objects without gaps; the object at index fill takes the height left
// over (-1: none). Hidden objects take no space.
type columnLayout struct{ fill int }

func (l *columnLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	rest := size.Height
	for i, o := range objs {
		if i != l.fill && o.Visible() {
			rest -= o.MinSize().Height
		}
	}
	y := float32(0)
	for i, o := range objs {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		if i == l.fill {
			h = max(0, rest)
		}
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (l *columnLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		if o.Visible() {
			m := o.MinSize()
			s = fyne.NewSize(max(s.Width, m.Width), s.Height+m.Height)
		}
	}
	return s
}

// vCenterLayout stacks its objects with gap and centres the block vertically; every object gets
// the full width.
type vCenterLayout struct{ gap float32 }

func (l *vCenterLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for i, o := range objs {
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height
		if i > 0 {
			s.Height += l.gap
		}
	}
	return s
}

func (l *vCenterLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := (size.Height - l.MinSize(objs).Height) / 2
	for _, o := range objs {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h + l.gap
	}
}

// dotLeadLayout puts objs[0] (a dot) at its size at the left, vertically centred, and gives
// objs[1] the rest after gap.
type dotLeadLayout struct{ gap float32 }

func (l *dotLeadLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b := objs[0].MinSize(), objs[1].MinSize()
	return fyne.NewSize(a.Width+l.gap+b.Width, max(a.Height, b.Height))
}

func (l *dotLeadLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	ds := objs[0].MinSize()
	objs[0].Move(fyne.NewPos(0, (size.Height-ds.Height)/2))
	objs[0].Resize(ds)
	objs[1].Move(fyne.NewPos(ds.Width+l.gap, 0))
	objs[1].Resize(fyne.NewSize(max(0, size.Width-ds.Width-l.gap), size.Height))
}

// leadingFillLayout puts objs[1] at its minimum width on the right and gives objs[0] the rest.
type leadingFillLayout struct{ gap float32 }

func (l *leadingFillLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	a, b := objs[0].MinSize(), objs[1].MinSize()
	return fyne.NewSize(a.Width+l.gap+b.Width, max(a.Height, b.Height))
}

func (l *leadingFillLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	bw := objs[1].MinSize().Width
	objs[1].Move(fyne.NewPos(size.Width-bw, 0))
	objs[1].Resize(fyne.NewSize(bw, size.Height))
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(max(0, size.Width-bw-l.gap), size.Height))
}

// statusLayout places objs[0] at the start and the other visible objects at the end, in their
// order; objs[shrink], an *ellipsisText (the KVM text), is shortened when the bar is too narrow.
type statusLayout struct {
	gap    float32
	shrink int
}

// statusShrink is the index of the KVM text in the status bar row.
const statusShrink = 3

func (l *statusLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	n := 0
	for i, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		if i != l.shrink {
			s.Width += m.Width
		}
		s.Height = max(s.Height, m.Height)
		n++
	}
	s.Width += float32(max(0, n-1)) * l.gap
	return s
}

func (l *statusLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	left := objs[0]
	lw := left.MinSize().Width
	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(lw, size.Height))
	var fixed float32
	n := 0
	for i, o := range objs[1:] {
		if o.Visible() {
			n++
			if i+1 != l.shrink {
				fixed += o.MinSize().Width
			}
		}
	}
	var sw float32
	if t, ok := objs[l.shrink].(*ellipsisText); ok && t.Visible() {
		sw = min(t.natural(), max(0, size.Width-lw-fixed-float32(n)*l.gap))
	}
	x := size.Width
	for i := len(objs) - 1; i >= 1; i-- {
		o := objs[i]
		if !o.Visible() {
			continue
		}
		w := o.MinSize().Width
		if i == l.shrink {
			w = sw
		}
		x -= w
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, size.Height))
		x -= l.gap
	}
}

// fixedWidthLayout gives its single object a fixed width (or less if the space is narrower)
// and the height a wrapped text needs at that width.
type fixedWidthLayout struct{ width float32 }

func (l *fixedWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	o := objs[0]
	if o.Size().Width != l.width {
		o.Resize(fyne.NewSize(l.width, o.MinSize().Height))
	}
	return fyne.NewSize(l.width, o.MinSize().Height)
}

func (l *fixedWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(min(l.width, size.Width), size.Height))
}
