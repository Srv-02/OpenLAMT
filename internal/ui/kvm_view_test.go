package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// redUpdate is one FramebufferUpdate covering w x h with RAW 64x64 tiles in RGB565 little
// endian (red = 0xf800); AMT sends at most 64x64 per rectangle.
func redUpdate(w, h int) []byte {
	var rects []kvmtest.Rect
	for y := 0; y < h; y += 64 {
		for x := 0; x < w; x += 64 {
			tw, th := min(64, w-x), min(64, h-y)
			rects = append(rects, kvmtest.RawRect(x, y, tw, th, bytes.Repeat([]byte{0x00, 0xf8}, tw*th)))
		}
	}
	return kvmtest.FramebufferUpdate(rects...)
}

// startKVM runs a fake server with one red 128x64 frame and dials it.
func startKVM(t *testing.T, updates ...[]byte) (*kvm.Client, *kvmtest.Server) {
	t.Helper()
	if updates == nil {
		updates = [][]byte{redUpdate(128, 64)}
	}
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "pw", Updates: updates})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := kvm.Dial(ctx, kvm.Config{Addr: srv.Addr, Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, srv
}

// inputMessages returns the key (4) and pointer (5) messages the server received so far.
func inputMessages(srv *kvmtest.Server) [][]byte {
	var out [][]byte
	for _, m := range srv.ClientMessages() {
		if m[0] == 4 || m[0] == 5 {
			out = append(out, m)
		}
	}
	return out
}

func waitInput(t *testing.T, srv *kvmtest.Server, n int) [][]byte {
	t.Helper()
	waitFor(t, func() bool { return len(inputMessages(srv)) >= n })
	return inputMessages(srv)
}

func keyMsg(sym uint32, down bool) []byte {
	m := []byte{4, 0, 0, 0, 0, 0, 0, 0}
	if down {
		m[1] = 1
	}
	binary.BigEndian.PutUint32(m[4:], sym)
	return m
}

func pointerMsg(x, y int, mask byte) []byte {
	m := []byte{5, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(m[2:], uint16(x))
	binary.BigEndian.PutUint16(m[4:], uint16(y))
	return m
}

type viewHarness struct {
	v      *kvmView
	w      fyne.Window
	frames chan struct{}
	ended  chan error
}

func newView(t *testing.T) *viewHarness {
	t.Helper()
	englishUI(t)
	test.NewTempApp(t)
	h := &viewHarness{v: newKVMView(newKeyMapper("linux", func() bool { return true })), frames: make(chan struct{}, 16), ended: make(chan error, 1)}
	h.w = test.NewTempWindow(t, h.v)
	h.w.Resize(fyne.NewSize(400, 300))
	h.v.onFrame = func() { h.frames <- struct{}{} }
	return h
}

func (h *viewHarness) attach(c *kvm.Client) { h.v.attach(c, func(err error) { h.ended <- err }) }

func TestFitRectAndToFramebuffer(t *testing.T) {
	pos, size := fitRect(fyne.NewSize(400, 300), 128, 64)
	if pos != fyne.NewPos(0, 50) || size != fyne.NewSize(400, 200) {
		t.Fatalf("wide: %v %v", pos, size)
	}
	pos, size = fitRect(fyne.NewSize(100, 300), 128, 64)
	if pos != fyne.NewPos(0, 125) || size != fyne.NewSize(100, 50) {
		t.Fatalf("narrow: %v %v", pos, size)
	}
	if _, size := fitRect(fyne.NewSize(400, 300), 0, 0); !size.IsZero() {
		t.Fatalf("no screen: %v", size)
	}
	area := fyne.NewSize(400, 300)
	for _, c := range []struct {
		p    fyne.Position
		x, y int
	}{
		{fyne.NewPos(0, 50), 0, 0}, {fyne.NewPos(200, 150), 64, 32}, {fyne.NewPos(399, 249), 127, 63},
		{fyne.NewPos(0, 0), 0, 0}, {fyne.NewPos(-10, 299), 0, 63}, {fyne.NewPos(1000, 1000), 127, 63},
	} {
		if x, y := toFramebuffer(c.p, area, 128, 64); x != c.x || y != c.y {
			t.Errorf("%v: got %d,%d want %d,%d", c.p, x, y, c.x, c.y)
		}
	}
	if x, y := toFramebuffer(fyne.NewPos(5, 5), area, 0, 0); x != 0 || y != 0 {
		t.Fatalf("no screen: %d,%d", x, y)
	}
}

func TestViewShowsFrameLetterboxed(t *testing.T) {
	h := newView(t)
	c, _ := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	if h.v.front == nil || h.v.front.Rect.Dx() != 128 || h.v.img.Image == nil {
		t.Fatal("frame not shown")
	}
	if p := h.v.front.RGBAAt(5, 5); p.R < 240 || p.G > 15 || p.B > 15 {
		t.Fatalf("pixel %v is not red", p)
	}
	wantPos, wantSize := fitRect(h.v.Size(), 128, 64)
	if h.v.img.Position() != wantPos || h.v.img.Size() != wantSize {
		t.Fatalf("image at %v %v, want %v %v (view %v)", h.v.img.Position(), h.v.img.Size(), wantPos, wantSize, h.v.Size())
	}
	c.Close()
	if err := <-h.ended; !errors.Is(err, kvm.ErrClosed) {
		t.Fatalf("ended with %v", err)
	}
}

func TestViewForwardsKeysMouseAndWheel(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	h.w.Canvas().Focus(h.v)
	h.v.KeyDown(ev(30, "")) // evdev KEY_A
	h.v.KeyUp(ev(30, ""))
	h.v.KeyDown(ev(1, fyne.KeyEscape)) // KEY_ESC; the name is ignored
	h.v.KeyUp(ev(1, fyne.KeyEscape))
	h.v.KeyDown(ev(86, ""))            // KEY_102ND: no kvm.Key, nothing sent
	h.v.KeyDown(ev(0, fyne.KeyEscape)) // unknown scan code: nothing sent, the name is no fallback
	centre := fyne.NewPos(h.v.Size().Width/2, h.v.Size().Height/2)
	x, y := toFramebuffer(centre, h.v.Size(), 128, 64)
	h.v.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}})
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}, Button: desktop.MouseButtonPrimary})
	h.v.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}, Button: desktop.MouseButtonPrimary})
	scroll := func(dy float32) {
		h.v.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: centre}, Scrolled: fyne.NewDelta(0, dy)})
	}
	scroll(25)  // one wheel click up (linux: 25 per click)
	scroll(-25) // one down
	scroll(10)  // trackpad: three small deltas add up to one click
	scroll(10)
	scroll(10)
	scroll(-5) // direction change drops the remainder of 5: nothing
	got := waitInput(t, srv, 13)
	want := [][]byte{keyMsg(0x61, true), keyMsg(0x61, false), keyMsg(0xff1b, true), keyMsg(0xff1b, false),
		pointerMsg(x, y, 0), pointerMsg(x, y, 1), pointerMsg(x, y, 0),
		pointerMsg(x, y, 8), pointerMsg(x, y, 0), pointerMsg(x, y, 16), pointerMsg(x, y, 0),
		pointerMsg(x, y, 8), pointerMsg(x, y, 0)}
	if len(got) != len(want) {
		t.Fatalf("%d messages: %x", len(got), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: got %x want %x", i, got[i], want[i])
		}
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(inputMessages(srv)); n != len(want) {
		t.Fatalf("%d messages, want %d", n, len(want))
	}
	c.Close()
	<-h.ended
}

// The glfw driver never focuses on a click (window.go:536-549): the view does it in MouseDown.
// Without the focus the mouse and the wheel are not forwarded (spec: while the image has the focus).
func TestViewClickFocusesAndFocusGatesMouse(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	h.w.Canvas().Unfocus()
	centre := fyne.NewPos(h.v.Size().Width/2, h.v.Size().Height/2)
	h.v.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}})
	h.v.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: centre}, Scrolled: fyne.NewDelta(0, 25)})
	time.Sleep(20 * time.Millisecond)
	if n := len(inputMessages(srv)); n != 0 {
		t.Fatalf("%d messages without focus", n)
	}
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}, Button: desktop.MouseButtonPrimary})
	if h.w.Canvas().Focused() != h.v || !h.v.focused {
		t.Fatal("click did not focus the view")
	}
	h.v.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: centre}, Button: desktop.MouseButtonPrimary})
	x, y := toFramebuffer(centre, h.v.Size(), 128, 64)
	got := waitInput(t, srv, 2)
	if !bytes.Equal(got[0], pointerMsg(x, y, 1)) || !bytes.Equal(got[1], pointerMsg(x, y, 0)) {
		t.Fatalf("messages %x", got)
	}
	c.Close()
	<-h.ended
}

// A button released outside the view goes to the object under the cursor (window.go:613-630):
// leaving the view releases the held buttons on the remote.
func TestViewMouseOutReleasesButtons(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	h.w.Canvas().Focus(h.v)
	pos := fyne.NewPos(10, 60)
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: pos}, Button: desktop.MouseButtonSecondary})
	h.v.MouseOut()
	h.v.MouseOut() // nothing held any more: no second release
	x, y := toFramebuffer(pos, h.v.Size(), 128, 64)
	got := waitInput(t, srv, 2)
	if !bytes.Equal(got[0], pointerMsg(x, y, byte(kvm.ButtonRight))) || !bytes.Equal(got[1], pointerMsg(x, y, 0)) {
		t.Fatalf("messages %x", got)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(inputMessages(srv)); n != 2 || h.v.buttons != 0 {
		t.Fatalf("%d messages, buttons %v", n, h.v.buttons)
	}
	c.Close()
	<-h.ended
}

// macOS reports Caps Lock only on a state change (cocoa_window.m flagsChanged): each event is
// sent as a full press, because the remote toggles on every press.
func TestViewMacCapsLockIsOneFullPressPerEvent(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	h.w.Canvas().Focus(h.v)
	h.v.mapper = newKeyMapper("darwin", nil)
	h.v.KeyDown(ev(0x39, "")) // kVK_CapsLock engaged
	h.v.KeyUp(ev(0x39, ""))   // disengaged (reported as release)
	h.v.mapper = newKeyMapper("linux", func() bool { return true })
	h.v.KeyDown(ev(58, "")) // KEY_CAPSLOCK: a normal key elsewhere
	got := waitInput(t, srv, 5)
	want := [][]byte{keyMsg(0xffe5, true), keyMsg(0xffe5, false), keyMsg(0xffe5, true), keyMsg(0xffe5, false), keyMsg(0xffe5, true)}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: got %x want %x", i, got[i], want[i])
		}
	}
	c.Close()
	<-h.ended
}

func TestViewFocusLostReleasesHeldKeysAndButtons(t *testing.T) {
	h := newView(t)
	c, srv := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	h.w.Canvas().Focus(h.v)
	h.v.KeyDown(ev(42, "")) // KEY_LEFTSHIFT held
	pos := fyne.NewPos(10, 60)
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: pos}, Button: desktop.MouseButtonSecondary})
	waitInput(t, srv, 2)
	h.w.Canvas().Unfocus()
	got := waitInput(t, srv, 4)
	x, y := toFramebuffer(pos, h.v.Size(), 128, 64)
	if !bytes.Equal(got[2], keyMsg(0xffe1, false)) || !bytes.Equal(got[3], pointerMsg(x, y, 0)) {
		t.Fatalf("release messages %x", got[2:])
	}
	h.v.detach() // nothing held any more: no further messages
	h.v.KeyDown(ev(30, ""))
	h.v.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: pos}})
	time.Sleep(20 * time.Millisecond)
	if n := len(inputMessages(srv)); n != 4 {
		t.Fatalf("%d messages after detach", n)
	}
	if h.v.img.Image != nil || h.v.Client() != nil {
		t.Fatal("detach did not hide the image")
	}
	c.Close()
	<-h.ended
}

func TestViewReusesBuffersAndFollowsResize(t *testing.T) {
	h := newView(t)
	second := kvmtest.FramebufferUpdate(kvmtest.DesktopSizeRect(64, 32))
	c, _ := startKVM(t, redUpdate(128, 64), redUpdate(128, 64), second)
	widths := make(chan int, 16) // width of the shown frame; updates may coalesce in WaitFrame, even before attach
	h.v.onFrame = func() { widths <- h.v.front.Rect.Dx() }
	h.attach(c)
	for w := 0; w != 64; {
		select {
		case w = <-widths:
		case <-time.After(2 * time.Second):
			t.Fatal("resized frame not shown")
		}
	}
	if h.v.img.Size() != fyne.NewSize(h.v.Size().Width, h.v.Size().Width/2) {
		t.Fatalf("resize not applied: %v %v", h.v.front.Rect, h.v.img.Size())
	}
	if h.v.back == nil {
		t.Fatal("the previous frame must survive as back buffer")
	}
	c.Close()
	<-h.ended
}

func TestDragBarStaysInsideWidth(t *testing.T) {
	englishUI(t)
	test.NewTempApp(t)
	b := newDragBar(widget.NewButton("A", nil), widget.NewButton("B", nil))
	layer := b.layer()
	layer.Resize(fyne.NewSize(500, 400)) // the layout centres the bar once the final width is known
	bw := b.Size().Width
	if bw <= 0 || b.Position() != fyne.NewPos((500-bw)/2, 0) {
		t.Fatalf("placed at %v, width %v", b.Position(), bw)
	}
	b.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-1000, 40)})
	if b.Position() != fyne.NewPos(0, 0) {
		t.Fatalf("left clamp: %v", b.Position())
	}
	b.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(2000, 0)})
	if b.Position() != fyne.NewPos(500-bw, 0) {
		t.Fatalf("right clamp: %v", b.Position())
	}
	b.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-10, 0)})
	if b.Position() != fyne.NewPos(500-bw-10, 0) {
		t.Fatalf("move: %v", b.Position())
	}
	b.DragEnd()
	layer.Resize(fyne.NewSize(300, 400)) // narrower: the dragged position is kept, clamped
	if b.Position() != fyne.NewPos(300-bw, 0) {
		t.Fatalf("clamp after resize: %v", b.Position())
	}
	b.recentre()
	if b.Position() != fyne.NewPos((300-bw)/2, 0) {
		t.Fatalf("recentre: %v", b.Position())
	}
}

func TestViewWithoutSessionTakesNoFocusOrTab(t *testing.T) {
	h := newView(t)
	h.w.Canvas().Unfocus()
	h.v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(5, 5)}, Button: desktop.MouseButtonPrimary})
	if h.w.Canvas().Focused() == h.v || h.v.AcceptsTab() {
		t.Fatal("view without client took focus or Tab")
	}
	c, _ := startKVM(t)
	h.attach(c)
	recv(t, h.frames)
	if !h.v.AcceptsTab() {
		t.Fatal("view with client refuses Tab")
	}
	c.Close()
	<-h.ended
}

// The image must stay a visible, canvas-registered object, otherwise img.Refresh does nothing
// in the GL driver and the picture stays black until a click redraws the widget.
func TestKVMImageIsNeverHidden(t *testing.T) {
	v := newKVMView(newKeyMapper("linux", func() bool { return true }))
	if !v.img.Visible() {
		t.Fatal("image hidden before a session")
	}
}
