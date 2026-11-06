package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/typing"
)

// fakeLayouts is a layoutStore in memory; a nil fav means "no favorite". A non-nil block makes
// Find wait, so the run stays pending.
type fakeLayouts struct {
	mu      sync.Mutex
	fav     *favorites.Favorite
	findErr error
	setErr  error
	block   chan struct{}
	saved   []string // SetQuality calls
}

func (f *fakeLayouts) Find(host, user string) (favorites.Favorite, bool, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil || f.fav == nil {
		return favorites.Favorite{}, false, f.findErr
	}
	return *f.fav, true, nil
}

// SetQuality records the stored quality on the favorite; without one it reports ErrNotFound
// like favorites.Manager.
func (f *fakeLayouts) SetQuality(host, user, quality string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, quality)
	if f.fav == nil {
		return favorites.ErrNotFound
	}
	f.fav.Quality = quality
	return f.setErr
}

// SetLayout records the stored layout name on the favorite; without one it reports ErrNotFound.
func (f *fakeLayouts) SetLayout(host, user, layout string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fav == nil {
		return favorites.ErrNotFound
	}
	f.fav.Layout = layout
	return f.setErr
}

// stored returns a copy of the favorite (nil without one), safe against the save goroutines.
func (f *fakeLayouts) stored() *favorites.Favorite {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fav == nil {
		return nil
	}
	c := *f.fav
	return &c
}

type clipHarness struct {
	*kvmHarness
	layouts *fakeLayouts
	done    chan struct{}
}

// newClipDevice connects a KVM device whose clipboard holds clip and whose local layout is DE.
func newClipDevice(t *testing.T, clip string, fav *favorites.Favorite) *clipHarness {
	t.Helper()
	h := &clipHarness{kvmHarness: newKVMDevice(t, &sessionFake{}, kvmtest.Options{}), layouts: &fakeLayouts{fav: fav},
		done: make(chan struct{}, 4)}
	h.d.readClipboard = func() string { return clip }
	h.d.detectLayout = func() typing.Layout { return typing.LayoutDE }
	h.d.layouts = h.layouts
	h.d.typeDelay = 0
	h.d.onTypeDone = func() { h.done <- struct{}{} }
	h.connect(t, true)
	return h
}

// run taps "Type Clipboard" and waits until the run ended (typed, stopped or refused).
func (h *clipHarness) run(t *testing.T) {
	t.Helper()
	test.Tap(h.d.clipBtn)
	recv(t, h.done)
}

func (h *clipHarness) count(sym uint32, down bool) (n int) {
	for _, m := range inputMessages(h.srv) {
		if bytes.Equal(m, keyMsg(sym, down)) {
			n++
		}
	}
	return n
}

// idle checks that no run is left and the controls are back to normal.
func (h *clipHarness) idle(t *testing.T) {
	t.Helper()
	if h.d.typing != nil || h.d.view.paused || h.d.clipBtn.Caption() != "Type Clipboard" || h.d.barClip.Caption() != "Type Clipboard" || h.d.kvmStatus.Text != "" {
		t.Fatalf("typing=%v paused=%v button=%q status=%q", h.d.typing, h.d.view.paused, h.d.clipBtn.Caption(), h.d.kvmStatus.Text)
	}
}

func TestTypeClipboardReleasesHeldKeysFirst(t *testing.T) {
	h := newClipDevice(t, "x", &favorites.Favorite{Layout: "us"})
	h.d.win.Canvas().Focus(h.d.view)
	h.d.view.KeyDown(&fyne.KeyEvent{Physical: fyne.HardwareKey{ScanCode: 30}}) // KEY_A
	waitInput(t, h.srv, 1)
	h.run(t)
	got := waitInput(t, h.srv, 4)
	if !bytes.Equal(got[1], keyMsg(0x61, false)) || !bytes.Equal(got[2], keyMsg('x', true)) {
		t.Fatalf("messages %x", got)
	}
	h.disconnect(t)
}

func TestTypeClipboardSendsStrokesWithoutDialog(t *testing.T) {
	h := newClipDevice(t, "Ab", &favorites.Favorite{Layout: "us"})
	var during []string
	h.d.onTypeProgress = func(done, total int) {
		if done == 1 {
			during = []string{h.d.clipBtn.Caption(), h.d.barClip.Caption(), h.d.kvmStatus.Text}
			if !h.d.view.paused {
				t.Error("live input not paused while typing")
			}
		}
	}
	h.run(t)
	got := waitInput(t, h.srv, 6) // Shift down, a down/up, Shift up, b down/up
	want := [][]byte{keyMsg(0xffe1, true), keyMsg(0x41, true), keyMsg(0x41, false), keyMsg(0xffe1, false), keyMsg(0x62, true), keyMsg(0x62, false)}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("message %d: %x", i, got[i])
		}
	}
	if strings.Join(during, "|") != "Stop typing|Stop typing|Typing clipboard: 1 of 2 characters" {
		t.Fatalf("during: %q", during)
	}
	if len(got) != 6 || h.d.win.Canvas().Overlays().Top() != nil || h.d.win.Canvas().Focused() != h.d.view || len(h.errs) != 0 {
		t.Fatalf("messages %d focus %v errs %d", len(got), h.d.win.Canvas().Focused(), len(h.errs))
	}
	h.idle(t)
}

func TestTypeClipboardMultilineIsTypedAtOnce(t *testing.T) {
	h := newClipDevice(t, "a\r\nb\r\n", &favorites.Favorite{Layout: "us"})
	h.run(t)
	waitInput(t, h.srv, 6)
	if h.count(0xff0d, true) != 1 { // one Enter: the trailing line break is dropped
		t.Fatalf("%d Enter", h.count(0xff0d, true))
	}
	h.idle(t)
}

func TestTypeClipboardRefusesWithoutShowingTheText(t *testing.T) {
	cases := []struct{ clip, want string }{
		{"\r\n", "The clipboard contains no text."},
		{strings.Repeat("a", typing.MaxChars+1), "The text has 2001 characters; at most 2000 can be typed."},
	}
	for _, c := range cases {
		h := newClipDevice(t, c.clip, nil)
		h.run(t)
		err := <-h.errs
		if describe(err) != c.want {
			t.Fatalf("%q: %q", c.clip, describe(err))
		}
		if n := len(inputMessages(h.srv)); n != 0 {
			t.Fatalf("%q: %d key messages", c.clip, n)
		}
		h.idle(t)
	}
}

// Characters the layout cannot type are skipped without a question, a message or a status text;
// everything else is typed.
func TestTypeClipboardSkipsUntypeableCharactersSilently(t *testing.T) {
	h := newClipDevice(t, "pa<ss>word", nil) // DE: < and > are not on the layout
	var totals []int
	h.d.onTypeProgress = func(done, total int) { totals = append(totals, total) }
	h.run(t)
	got := waitInput(t, h.srv, 16) // p a s s w o r d: down and up each
	if len(got) != 16 || h.d.win.Canvas().Overlays().Top() != nil || len(h.errs) != 0 {
		t.Fatalf("messages %d overlay %v errs %d", len(got), h.d.win.Canvas().Overlays().Top(), len(h.errs))
	}
	h.idle(t)
	if len(totals) == 0 || totals[0] != 8 {
		t.Fatalf("progress totals %v, want 8 typeable characters", totals)
	}
}

func TestTypeClipboardOnlyUntypeableCharactersDoNothingAndSayNothing(t *testing.T) {
	h := newClipDevice(t, "<|>", nil)
	h.run(t)
	if n := len(inputMessages(h.srv)); n != 0 || h.d.win.Canvas().Overlays().Top() != nil || len(h.errs) != 0 {
		t.Fatalf("messages %d overlay %v errs %d", n, h.d.win.Canvas().Overlays().Top(), len(h.errs))
	}
	h.idle(t)
}

func TestTypeClipboardLayoutOrder(t *testing.T) {
	h := newClipDevice(t, "", nil) // detectLayout: DE
	cases := []struct {
		override string
		global   typing.Layout
		want     typing.Layout
	}{
		{"us", typing.LayoutDE, typing.LayoutUS},      // the favorite override wins
		{"", typing.LayoutUS, typing.LayoutUS},        // then the global setting
		{"", "", typing.LayoutDE},                     // then the system language
		{"xx", typing.LayoutUS, typing.LayoutUS},      // an unknown override is ignored
		{"ch-de", typing.LayoutUS, typing.LayoutCHDE}, // every layout code is valid
	}
	for _, c := range cases {
		if got := h.d.resolveLayout(c.override, c.global); got != c.want {
			t.Errorf("override %q global %q: %s, want %s", c.override, c.global, got, c.want)
		}
	}
}

func TestTypeClipboardUsesGlobalSetting(t *testing.T) {
	h := newClipDevice(t, "<", &favorites.Favorite{}) // '<' maps in US only (the DE key next to Shift is not in the table)
	h.d.settings.SetTypeLayout(typing.LayoutUS)
	h.run(t)
	if len(h.errs) != 0 {
		t.Fatalf("err %v", describe(<-h.errs))
	}
	// US types '<' as Shift+Comma: Shift down, Comma down, Comma up, Shift up. The test server
	// records asynchronously, so wait for all four before DE runs (issue #118).
	if typed := len(waitInput(t, h.srv, 4)); typed != 4 {
		t.Fatalf("US: %d messages, want 4", typed)
	}
	h.d.settings.SetTypeLayout(typing.LayoutDE)
	h.run(t)
	if n := len(inputMessages(h.srv)); n != 4 || len(h.errs) != 0 { // DE skips '<' silently
		t.Fatalf("DE: %d messages after 4, errs %d", n, len(h.errs))
	}
}

func TestTypeClipboardStoreErrorIsReportedButDoesNotBlock(t *testing.T) {
	h := newClipDevice(t, "ab", nil)
	h.layouts.findErr = errors.New("read failed")
	h.run(t)
	if err := <-h.errs; err.Error() != "read failed" {
		t.Fatalf("err %v", err)
	}
	waitInput(t, h.srv, 4)
	h.idle(t)
}

func TestTypeClipboardWithoutStoreUsesDetection(t *testing.T) {
	h := newClipDevice(t, "ab", nil)
	h.d.layouts = nil
	h.d.detectLayout = func() typing.Layout { return typing.LayoutUS }
	h.run(t)
	waitInput(t, h.srv, 4)
}

func TestTypeClipboardStopStopsBetweenCharacters(t *testing.T) {
	const total = 300
	h := newClipDevice(t, strings.Repeat("A", total), &favorites.Favorite{Layout: "us"})
	h.d.typeDelay = 20 * time.Millisecond
	// In the test driver the sender goroutine runs the Fyne-thread work, so "Stop typing" is
	// pressed from the progress hook and the test thread touches no widget while sending.
	h.d.onTypeProgress = func(done, _ int) {
		if done == 8 {
			h.d.typeClipboard()
		}
	}
	h.run(t)
	waitFor(t, func() bool { return h.count(0xffe1, true) == h.count(0xffe1, false) })
	if n := h.count(0xffe1, true); n == 0 || n >= total {
		t.Fatalf("%d of %d characters sent", n, total)
	}
	if len(h.errs) != 0 || h.d.win.Canvas().Focused() != h.d.view {
		t.Fatalf("errs %d", len(h.errs))
	}
	h.idle(t)
}

func TestTypeClipboardStopWhilePendingSendsNothing(t *testing.T) {
	h := newClipDevice(t, "abc", nil)
	h.layouts.block = make(chan struct{})
	test.Tap(h.d.clipBtn)
	if h.d.typing == nil || h.d.clipBtn.Caption() != "Stop typing" {
		t.Fatal("not pending")
	}
	test.Tap(h.d.barClip) // stop
	h.idle(t)
	close(h.layouts.block)
	time.Sleep(50 * time.Millisecond)
	if n := len(inputMessages(h.srv)); n != 0 {
		t.Fatalf("%d key messages", n)
	}
}

func TestTypeClipboardPendingRunEndsWhenKVMEnds(t *testing.T) {
	h := newClipDevice(t, "abc", nil)
	h.layouts.block = make(chan struct{})
	test.Tap(h.d.clipBtn)
	job := h.d.typing
	h.disconnect(t)
	if h.d.typing != nil || !job.closed || h.d.view.paused {
		t.Fatalf("still running: typing=%v", h.d.typing)
	}
	select {
	case <-job.ctx.Done():
	default:
		t.Fatal("context not cancelled")
	}
	close(h.layouts.block)
}

func TestTypeClipboardSendingStopsWhenKVMEnds(t *testing.T) {
	h := newClipDevice(t, strings.Repeat("a", 300), nil)
	h.d.typeDelay = 20 * time.Millisecond
	h.d.onTypeProgress = func(done, _ int) {
		if done == 4 {
			h.d.disconnectKVM() // the session ends while characters are being typed
		}
	}
	h.run(t)
	recv(t, h.changed) // kvmEnded
	if h.d.typing != nil || len(h.errs) != 0 {
		t.Fatalf("typing %v errs %d", h.d.typing, len(h.errs))
	}
}

func TestStartWindowGivesDevicesTheFavoriteStore(t *testing.T) {
	h := newStart(t)
	d := openFakeDevice(t, h, "one.example.com", h.dev)
	if d.layouts == nil || d.readClipboard == nil || d.detectLayout == nil || d.typeDelay != typing.DefaultDelay {
		t.Fatal("device window not wired")
	}
}

// An error dialog opening while the clipboard is typed must not release the modifier the sender
// goroutine holds: the next character would go out without Shift.
func TestTypeClipboardKeepsHeldModifierWhenErrorDialogOpens(t *testing.T) {
	h := newClipDevice(t, "abc", nil)
	h.layouts.block = make(chan struct{}) // the run stays pending; the test plays the sender
	h.d.onError = h.d.showErrorDialog     // the real path; the harness replaces it with a recorder
	test.Tap(h.d.clipBtn)
	c := h.d.kvmClient
	if err := c.SendKey(typing.KeyShiftL, true); err != nil { // as the sender goroutine does mid-stroke
		t.Fatal(err)
	}
	waitInput(t, h.srv, 1)
	h.d.onError(errors.New("x"))
	h.d.view.FocusLost()                         // focus moving to "Stop typing" must not release either
	if err := c.SendKey('x', true); err != nil { // marker: the next message, 'X' while Shift is held
		t.Fatal(err)
	}
	got := waitInput(t, h.srv, 2)
	if !bytes.Equal(got[1], keyMsg('X', true)) { // still shifted
		t.Fatalf("Shift was released: messages %x", got)
	}
	h.d.closeTyping()
	close(h.layouts.block)
	_ = c.ReleaseAll()
	h.disconnect(t)
}

// Live keys are dropped while a run exists, so they cannot mix with the typed strokes.
func TestTypeClipboardDropsLiveKeysWhileRunning(t *testing.T) {
	h := newClipDevice(t, "abc", nil)
	h.layouts.block = make(chan struct{})
	test.Tap(h.d.clipBtn)
	h.d.view.KeyDown(&fyne.KeyEvent{Physical: fyne.HardwareKey{ScanCode: 30}})
	h.d.closeTyping()
	close(h.layouts.block)
	time.Sleep(50 * time.Millisecond)
	if n := len(inputMessages(h.srv)); n != 0 {
		t.Fatalf("%d key messages", n)
	}
}

// Disconnecting while characters are typed closes the session only after the sender goroutine
// has left typing.Type, so its last key-up is not sent into a closed socket.
func TestTypeClipboardDisconnectWaitsForSenderBeforeClosing(t *testing.T) {
	h := newClipDevice(t, strings.Repeat("A", 300), &favorites.Favorite{Layout: "us"})
	h.d.typeDelay = time.Millisecond
	inHook, release := make(chan struct{}), make(chan struct{})
	h.d.onTypeProgress = func(done, _ int) {
		if done == 4 {
			close(inHook)
			<-release // the sender goroutine is still inside typing.Type
		}
	}
	test.Tap(h.d.clipBtn)
	recv(t, inHook)
	h.d.disconnectKVM()
	select {
	case <-h.changed:
		t.Fatal("session closed while the sender goroutine was still typing")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	recv(t, h.done)
	recv(t, h.changed) // kvmEnded
	for _, sym := range []uint32{typing.KeyShiftL, 'A'} {
		if d, u := h.count(sym, true), h.count(sym, false); d != u || d == 0 {
			t.Fatalf("keysym %#x: %d down, %d up", sym, d, u)
		}
	}
}

// A stop while a stroke is out keeps the run (and the paused view) until the sender has left
// typing.Type, so a new run cannot start on top of its last stroke.
func TestTypeClipboardStopKeepsStateUntilSenderLeft(t *testing.T) {
	h := newClipDevice(t, strings.Repeat("A", 300), &favorites.Favorite{Layout: "us"})
	h.d.typeDelay = time.Millisecond
	var afterStop []any
	h.d.onTypeProgress = func(done, _ int) {
		if done == 4 {
			h.d.typeClipboard() // stop
			afterStop = []any{h.d.typing != nil, h.d.view.paused, h.d.clipBtn.Caption()}
			h.d.typeClipboard() // a second click while stopping starts nothing
			afterStop = append(afterStop, h.d.typing != nil)
		}
	}
	h.run(t)
	if fmt.Sprint(afterStop) != "[true true Stop typing true]" {
		t.Fatalf("after stop: %v", afterStop)
	}
	h.idle(t)
	if len(h.errs) != 0 {
		t.Fatalf("error after stop: %v", describe(<-h.errs))
	}
}

// A failed send is reported without the error text: it names the keysym of the current character.
func TestTypeClipboardSendErrorHidesCharacter(t *testing.T) {
	h := newClipDevice(t, "s", nil)
	h.layouts.block = make(chan struct{})
	test.Tap(h.d.clipBtn)
	job := h.d.typing
	job.finish(errors.New("typing: key down 0x73: broken pipe")) // as the sender reports a failed write
	err := <-h.errs
	var plain *plainError
	if strings.Contains(err.Error(), "0x73") || !strings.Contains(describe(err), "interrupted") || !errors.As(err, &plain) {
		t.Fatalf("%q / %q", err.Error(), describe(err))
	}
	h.idle(t)
	close(h.layouts.block)
}
