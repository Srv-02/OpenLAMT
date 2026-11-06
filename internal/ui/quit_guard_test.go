package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// interceptOf reads the close intercept the production code registered: the test window has
// no way to trigger one, which a real Cmd+Q or red button does.
func interceptOf(w fyne.Window) func() {
	f := reflect.ValueOf(w).Elem().FieldByName("onCloseIntercepted")
	if !f.IsValid() {
		panic("test window has no onCloseIntercepted")
	}
	fn := *(*func())(unsafe.Pointer(f.UnsafeAddr()))
	if fn == nil {
		panic("no close intercept registered")
	}
	return fn
}

// quitHarness gives the start window device windows with a chosen KVM state and collects the
// queued flush like the Fyne thread does: a "pass" delivers close requests, then runs the queue.
type quitHarness struct {
	h      *startHarness
	queue  []func()
	closed map[string]bool
	quits  int // how often the app was told to quit (the master window's Close)
}

func newQuitHarness(t *testing.T) *quitHarness {
	t.Helper()
	q := &quitHarness{h: newStart(t), closed: map[string]bool{}}
	q.h.s.later = func(f func()) { q.queue = append(q.queue, f) }
	// Closing the master window is recorded instead: the test driver panics on a second Close
	// and the cleanup of newStart closes it. Its SetOnClosed (closeDevices) is covered elsewhere.
	q.h.s.quitApp = func() { q.quits++ }
	t.Cleanup(func() { q.h.s.open = nil })
	return q
}

func (q *quitHarness) mainClosed() bool { return q.quits > 0 }

func (q *quitHarness) addDevice(name string, kvmBusy bool) *DeviceWindow {
	d := &DeviceWindow{win: q.h.s.app.NewWindow(name), kvmBusy: kvmBusy}
	d.win.SetOnClosed(func() { q.closed[name] = true })
	d.win.SetCloseIntercept(func() { q.h.s.requestClose(d.win) })
	q.h.s.open[name] = d
	return d
}

// pass delivers the close requests of one event and then runs what they queued.
func (q *quitHarness) pass(wins ...fyne.Window) {
	for _, w := range wins {
		interceptOf(w)()
	}
	for len(q.queue) > 0 {
		f := q.queue[0]
		q.queue = q.queue[1:]
		f()
	}
}

func (q *quitHarness) quit() {
	wins := []fyne.Window{}
	for _, d := range q.h.s.open {
		wins = append(wins, d.win)
	}
	q.pass(append(wins, q.h.s.win)...) // Cmd+Q: the main window is in the same pass
}

func (q *quitHarness) dialog() (cancel, action *escButton, shown bool) {
	top := q.h.s.win.Canvas().Overlays().Top()
	if top == nil {
		return nil, nil, false
	}
	if bs := panelButtons(top); len(bs) == 2 {
		cancel, action = bs[0], bs[1]
	}
	return cancel, action, true
}

func TestQuitWithoutKVMClosesEverythingSilently(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("a", false)
	q.quit()
	if _, _, shown := q.dialog(); shown {
		t.Fatal("asked although no KVM session is open")
	}
	if q.quits != 1 {
		t.Fatalf("not closed: %v", q.closed)
	}
}

func TestQuitWithKVMAsksAndCancelKeepsEverythingOpen(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("a", true)
	q.addDevice("b", false)
	q.quit()
	cancel, action, shown := q.dialog()
	if !shown || cancel == nil || action == nil {
		t.Fatalf("no confirmation: %v %v %v asking=%v n=%d", shown, cancel != nil, action != nil, q.h.s.quitAsking, q.h.s.kvmSessions())
	}
	if action.Text != lang.L("Quit") || q.h.s.win.Canvas().Focused() != cancel {
		t.Fatalf("button %q, Cancel focused: %v", action.Text, q.h.s.win.Canvas().Focused() == cancel)
	}
	if len(q.closed) != 0 || q.mainClosed() {
		t.Fatalf("closed before the answer: %v", q.closed)
	}
	q.quit() // a second Cmd+Q while the question is open adds nothing
	test.Tap(cancel)
	if _, _, shown := q.dialog(); shown || len(q.closed) != 0 || q.mainClosed() || q.h.s.quitAsking {
		t.Fatalf("Cancel: shown=%v closed=%v", shown, q.closed)
	}
	q.quit() // asks again afterwards
	if _, _, shown := q.dialog(); !shown {
		t.Fatal("no second question after Cancel")
	}
}

func TestQuitConfirmClosesEverything(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("a", true)
	q.addDevice("b", true)
	q.quit()
	_, action, _ := q.dialog()
	test.Tap(action)
	if q.quits != 1 {
		t.Fatalf("not closed: %v", q.closed)
	}
}

func TestQuitEscCancels(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("a", true)
	q.quit()
	q.h.s.win.Canvas().Unfocus()
	q.h.s.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if _, _, shown := q.dialog(); shown || len(q.closed) != 0 || q.mainClosed() {
		t.Fatalf("Esc: shown=%v closed=%v", shown, q.closed)
	}
}

func TestClosingMainWindowAloneAsksToo(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("a", true)
	q.pass(q.h.s.win)
	if _, _, shown := q.dialog(); !shown {
		t.Fatal("closing the main window ends the app and must ask")
	}
}

func TestSingleDeviceWindowClosesWithoutQuestion(t *testing.T) {
	q := newQuitHarness(t)
	a := q.addDevice("a", true) // KVM running, still no question: the red close button
	q.addDevice("b", true)
	q.pass(a.win)
	if _, _, shown := q.dialog(); shown {
		t.Fatal("asked for a single device window")
	}
	if !q.closed["a"] || q.closed["b"] || q.mainClosed() {
		t.Fatalf("closed: %v", q.closed)
	}
}

func TestKVMSessionsCountsConnectedConnectingAndReconnecting(t *testing.T) {
	q := newQuitHarness(t)
	q.addDevice("idle", false)
	q.addDevice("busy", true)
	r := q.addDevice("re", false)
	r.kvmReconnecting = true
	if n := q.h.s.kvmSessions(); n != 2 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestQuitTextSingularAndPlural(t *testing.T) {
	englishUI(t)
	_, one, _ := quitText(1)
	_, many, _ := quitText(3)
	if !strings.HasPrefix(one, "A KVM session") || !strings.HasPrefix(many, "3 KVM sessions") {
		t.Fatalf("%q / %q", one, many)
	}
}

func TestProductionWindowsRouteCloseRequestsThroughTheGuard(t *testing.T) {
	q := newQuitHarness(t)
	cfg := amt.Config{Host: "device.example.com", Username: "admin", Fingerprint: q.h.dev.FP}
	sess, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(q.h.dev))
	if err != nil {
		t.Fatal(err)
	}
	d := q.h.s.addDevice(sess, st, "Büro")
	d.win.SetOnClosed(func() { q.closed["dev"] = true })
	d.kvmBusy = true

	q.pass(d.win) // red button
	if _, _, shown := q.dialog(); shown || !q.closed["dev"] {
		t.Fatalf("device window: dialog=%v closed=%v", shown, q.closed)
	}

	d2 := q.h.s.addDevice(sess, st, "Büro")
	d2.kvmBusy = true
	q.pass(d2.win, q.h.s.win) // Cmd+Q: every window gets its request
	if _, _, shown := q.dialog(); !shown {
		t.Fatal("quit with a KVM session did not ask")
	}
}
