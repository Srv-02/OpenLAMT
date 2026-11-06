package ui

import (
	"context"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

// RFB pointer mask bits for the wheel (buttons 4 and 5); kvm.Button carries them unchanged.
const (
	wheelUp   kvm.Button = 1 << 3
	wheelDown kvm.Button = 1 << 4
)

// kvmView shows the remote screen letterboxed into its area and, while it has the focus,
// forwards every key (by physical scan code) and the mouse to the kvm.Client. All methods run
// on the Fyne thread; only the frame loop goroutine touches back and calls fyne.DoAndWait.
type kvmView struct {
	widget.BaseWidget
	img         *canvas.Image
	front, back *image.RGBA // front is shown, back is the loop's copy target; swapped per frame
	mapper      *keyMapper
	client      *kvm.Client // nil while disconnected
	focused     bool        // mouse and wheel are forwarded only while focused (keys arrive only then anyway)
	buttons     kvm.Button
	lastX       int
	lastY       int
	wheel       float32 // accumulated scroll delta, one wheel click per wheelStep
	onFrame     func()  // test hook, called after a frame was shown
	onInput     func()  // called for every key, button, pointer and wheel event sent
	// paused drops live key events and key releases while "Type clipboard" sends: the
	// sender holds Shift or AltGr of its current stroke, and a live key or a release (focus
	// moving to the "Stop typing" button) would change them in the middle of a character.
	paused bool
}

var (
	_ fyne.Focusable     = (*kvmView)(nil)
	_ fyne.Tabbable      = (*kvmView)(nil)
	_ fyne.Scrollable    = (*kvmView)(nil)
	_ desktop.Keyable    = (*kvmView)(nil)
	_ desktop.Mouseable  = (*kvmView)(nil)
	_ desktop.Hoverable  = (*kvmView)(nil)
	_ desktop.Cursorable = (*kvmView)(nil)
)

// wheelStep is the scroll delta of one wheel click as the glfw driver reports it: GLFW gives
// 1.0 per click on every platform, Fyne multiplies by scrollSpeed (scroll_speed_darwin.go: 10,
// scroll_speed_default.go: 25). Trackpads deliver many smaller deltas, which are summed up.
func wheelStep(goos string) float32 {
	if goos == "darwin" {
		return 10
	}
	return 25
}

func newKVMView(mapper *keyMapper) *kvmView {
	v := &kvmView{mapper: mapper}
	// ImageScaleFastest: the full framebuffer is uploaded as one texture and scaled on the GPU;
	// ImageScaleSmooth would rescale 8 MB in software on every paint (internal/painter/image.go:52).
	// The image is never hidden: Fyne only registers visible objects with the window canvas
	// (WalkVisibleObjectTree), and img.Refresh of an unregistered object does nothing, which left
	// the picture black until a click redrew the whole widget. Without a session Image is nil
	// and only the black background shows.
	v.img = &canvas.Image{ScaleMode: canvas.ImageScaleFastest}
	v.ExtendBaseWidget(v)
	return v
}

// attach shows the client's screen and starts the frame loop; ended runs on the Fyne thread
// once the session is over (Close or error), with the session error.
func (v *kvmView) attach(c *kvm.Client, ended func(error)) {
	v.bind(c, true)
	v.run(c, ended)
}

// bind makes c the view's client; with show its current image replaces the shown one,
// without (reconnect) the last image stays until c delivers its first frame, so the view
// does not flash black. run starts the frame loop afterwards.
func (v *kvmView) bind(c *kvm.Client, show bool) {
	v.client = c
	v.buttons = 0
	v.wheel = 0
	if show {
		v.front = c.SnapshotInto(v.front)
		v.img.Image = v.front
		v.Refresh()
		canvas.Refresh(v) // frees the cached textures of the whole widget, the image included
	}
}

// run starts the frame loop of c; ended runs on the Fyne thread once the session is over.
func (v *kvmView) run(c *kvm.Client, ended func(error)) { go v.loop(c, ended) }

// suspend drops the ended client but keeps its last image on screen while a reconnect runs.
func (v *kvmView) suspend() {
	v.client = nil
	v.buttons = 0
}

// detach releases held keys and hides the image; the loop reports ended on its own.
func (v *kvmView) detach() {
	if v.client == nil && v.img.Image == nil {
		return
	}
	if v.client != nil {
		v.releaseAll()
	}
	v.client = nil
	v.img.Image = nil
	v.Refresh()
	canvas.Refresh(v)
}

// free drops the client and the image buffers once the window is closed and no frame loop
// runs any more (the loop writes back). Fyne 2.8.2 keeps a closed window reachable: its text
// textures stay in cache.textTextures, keyed by the canvas, and are only evicted when that
// canvas paints again (common/canvas.go FreeDirtyTextures), which a closed one never does.
// With the canvas the whole widget tree stays, this view included; without free every closed
// device window kept two copies of the remote screen (about 10 MB at 1280x1024).
func (v *kvmView) free() {
	v.client = nil
	v.front, v.back = nil, nil
	v.img.Image = nil
}

// Client returns the attached client or nil.
func (v *kvmView) Client() *kvm.Client { return v.client }

func (v *kvmView) loop(c *kvm.Client, ended func(error)) {
	var seq uint64
	for {
		next, err := c.WaitFrame(context.Background(), seq)
		if err != nil {
			fyne.Do(func() { ended(err) })
			return
		}
		seq = next
		v.back = c.SnapshotInto(v.back)
		fyne.DoAndWait(func() { v.show(c, v.back) })
	}
}

// show makes img the visible frame; a frame of a detached client is dropped.
func (v *kvmView) show(c *kvm.Client, img *image.RGBA) {
	if v.client != c {
		return
	}
	resized := v.front == nil || v.front.Rect != img.Rect
	v.front, v.back = img, v.front
	v.img.Image = v.front
	if resized {
		v.Refresh()
		canvas.Refresh(v)
	} else {
		v.img.Refresh()
	}
	if v.onFrame != nil {
		v.onFrame()
	}
}

func (v *kvmView) screenSize() (w, h int) {
	if v.front == nil {
		return 0, 0
	}
	return v.front.Rect.Dx(), v.front.Rect.Dy()
}

// fitRect returns position and size of a w x h image scaled to fit area, centred (letterbox).
func fitRect(area fyne.Size, w, h int) (fyne.Position, fyne.Size) {
	if w <= 0 || h <= 0 || area.Width <= 0 || area.Height <= 0 {
		return fyne.Position{}, fyne.Size{}
	}
	scale := min(area.Width/float32(w), area.Height/float32(h))
	size := fyne.NewSize(float32(w)*scale, float32(h)*scale)
	return fyne.NewPos((area.Width-size.Width)/2, (area.Height-size.Height)/2), size
}

// toFramebuffer maps a point in the view to framebuffer pixels, clamped to the screen.
func toFramebuffer(p fyne.Position, area fyne.Size, w, h int) (x, y int) {
	pos, size := fitRect(area, w, h)
	if size.Width <= 0 || size.Height <= 0 {
		return 0, 0
	}
	x = int((p.X - pos.X) / size.Width * float32(w))
	y = int((p.Y - pos.Y) / size.Height * float32(h))
	return max(0, min(x, w-1)), max(0, min(y, h-1))
}

func (v *kvmView) CreateRenderer() fyne.WidgetRenderer {
	bg := style.BindRect(canvas.NewRectangle(nil), style.ColorNameLetterbox, "") // around the letterboxed image
	return &kvmRenderer{v: v, bg: bg}
}

type kvmRenderer struct {
	v  *kvmView
	bg *canvas.Rectangle
}

func (r *kvmRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	w, h := r.v.screenSize()
	pos, sz := fitRect(size, w, h)
	r.v.img.Move(pos)
	r.v.img.Resize(sz)
}
func (r *kvmRenderer) MinSize() fyne.Size           { return fyne.NewSize(320, 180) }
func (r *kvmRenderer) Refresh()                     { r.Layout(r.v.Size()); canvas.Refresh(r.bg); r.v.img.Refresh() }
func (r *kvmRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.v.img} }
func (r *kvmRenderer) Destroy()                     {}

// Focus: Tab is consumed (AcceptsTab) so it reaches the remote; losing the focus releases
// every held key and button so nothing stays pressed on the remote.
func (v *kvmView) FocusGained()             { v.focused = true }
func (v *kvmView) FocusLost()               { v.focused = false; v.releaseAll() }
func (v *kvmView) TypedRune(rune)           {}
func (v *kvmView) TypedKey(*fyne.KeyEvent)  {}
func (v *kvmView) AcceptsTab() bool         { return v.client != nil }
func (v *kvmView) Cursor() desktop.Cursor   { return desktop.CrosshairCursor }
func (v *kvmView) KeyDown(e *fyne.KeyEvent) { v.key(e, true) }
func (v *kvmView) KeyUp(e *fyne.KeyEvent)   { v.key(e, false) }

func (v *kvmView) key(e *fyne.KeyEvent, down bool) {
	if v.client == nil || v.paused {
		return
	}
	k := v.mapper.Key(e)
	if k == kvm.KeyUnknown {
		return
	}
	if k == kvm.CapsLock && v.mapper.goos == "darwin" {
		// macOS reports Caps Lock only when its state changes (cocoa_window.m flagsChanged:
		// press when it engages, release when it disengages); the remote toggles on every
		// press, so each event becomes one full press there.
		v.input()
		_ = v.client.KeyEvent(k, true)
		_ = v.client.KeyEvent(k, false)
		return
	}
	v.input()
	_ = v.client.KeyEvent(k, down) // a failed send ends the session; the loop reports it
}

func (v *kvmView) input() {
	if v.onInput != nil {
		v.onInput()
	}
}

func (v *kvmView) releaseAll() {
	if v.client == nil || v.paused {
		return
	}
	_ = v.client.ReleaseAll()
	v.releaseButtons()
}

// releaseButtons sends a pointer event without buttons if any is held.
func (v *kvmView) releaseButtons() {
	if v.buttons != 0 && v.client != nil {
		v.buttons = 0
		_ = v.client.PointerEvent(v.lastX, v.lastY, 0)
	}
}

func toButton(b desktop.MouseButton) kvm.Button {
	var out kvm.Button
	if b&desktop.MouseButtonPrimary != 0 {
		out |= kvm.ButtonLeft
	}
	if b&desktop.MouseButtonSecondary != 0 {
		out |= kvm.ButtonRight
	}
	if b&desktop.MouseButtonTertiary != 0 {
		out |= kvm.ButtonMiddle
	}
	return out
}

// MouseDown takes the focus: the glfw driver only unfocuses on a click (window.go:536-549),
// widgets request the focus themselves (widget/button.go:201-204).
func (v *kvmView) MouseDown(e *desktop.MouseEvent) {
	if v.client == nil {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(v); c != nil && c.Focused() != v {
		c.Focus(v)
	}
	v.buttons |= toButton(e.Button)
	v.pointer(e.Position)
}
func (v *kvmView) MouseUp(e *desktop.MouseEvent) {
	v.buttons &^= toButton(e.Button)
	v.pointer(e.Position)
}
func (v *kvmView) MouseIn(e *desktop.MouseEvent) { v.pointer(e.Position) }
func (v *kvmView) MouseMoved(e *desktop.MouseEvent) {
	v.pointer(e.Position)
}

// MouseOut releases held buttons: a release outside the view goes to the object under the
// cursor (window.go:613-630), so the remote would keep the button pressed.
func (v *kvmView) MouseOut() { v.releaseButtons() }

// Scrolled sums the scroll delta and sends one wheel click (RFB buttons 4 up, 5 down, pressed
// and released) per wheelStep, so a trackpad does not scroll one click per tiny delta.
func (v *kvmView) Scrolled(e *fyne.ScrollEvent) {
	if v.client == nil || !v.focused || e.Scrolled.DY == 0 {
		return
	}
	step := wheelStep(v.mapper.goos)
	if (v.wheel > 0) != (e.Scrolled.DY > 0) {
		v.wheel = 0 // direction changed: drop the remainder
	}
	v.wheel += e.Scrolled.DY
	v.input()
	x, y := v.toScreen(e.Position)
	for ; v.wheel >= step; v.wheel -= step {
		_ = v.client.PointerEvent(x, y, v.buttons|wheelUp)
		_ = v.client.PointerEvent(x, y, v.buttons)
	}
	for ; v.wheel <= -step; v.wheel += step {
		_ = v.client.PointerEvent(x, y, v.buttons|wheelDown)
		_ = v.client.PointerEvent(x, y, v.buttons)
	}
}

func (v *kvmView) toScreen(p fyne.Position) (int, int) {
	w, h := v.screenSize()
	v.lastX, v.lastY = toFramebuffer(p, v.Size(), w, h)
	return v.lastX, v.lastY
}

func (v *kvmView) pointer(p fyne.Position) {
	if v.client == nil || !v.focused {
		return
	}
	x, y := v.toScreen(p)
	v.input()
	_ = v.client.PointerEvent(x, y, v.buttons)
}
