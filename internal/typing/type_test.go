package typing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type ev struct {
	Keysym uint32
	Down   bool
}

type fakeSender struct {
	events  []ev
	failAt  int // 1-based event number that fails; 0 = never
	failErr error
}

func (f *fakeSender) SendKey(k uint32, down bool) error {
	if f.failAt > 0 && len(f.events)+1 == f.failAt {
		f.events = append(f.events, ev{k, down}) // recorded as attempted
		return f.failErr
	}
	f.events = append(f.events, ev{k, down})
	return nil
}

// pressed returns keysyms still held down at the end of events.
func pressed(evs []ev) map[uint32]bool {
	m := map[uint32]bool{}
	for _, e := range evs {
		if e.Down {
			m[e.Keysym] = true
		} else {
			delete(m, e.Keysym)
		}
	}
	return m
}

func TestTypeSequence(t *testing.T) {
	p := mustPrepare(t, "A@a", LayoutDE)
	f := &fakeSender{}
	var prog [][2]int
	err := Type(context.Background(), f, p, 0, func(d, n int) { prog = append(prog, [2]int{d, n}) })
	if err != nil {
		t.Fatal(err)
	}
	want := []ev{
		{KeyShiftL, true}, {'a', true}, {'a', false}, {KeyShiftL, false},
		{KeyAltR, true}, {'q', true}, {'q', false}, {KeyAltR, false},
		{'a', true}, {'a', false},
	}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("got %v\nwant %v", f.events, want)
	}
	if !reflect.DeepEqual(prog, [][2]int{{1, 3}, {2, 3}, {3, 3}}) {
		t.Fatalf("progress %v", prog)
	}
}

func TestTypeShiftAltGrTogether(t *testing.T) {
	f := &fakeSender{}
	p := Plan{Layout: LayoutDE, Chars: 1, Strokes: []Stroke{{Keysym: 'x', Shift: true, AltGr: true}}}
	if err := Type(context.Background(), f, p, 0, nil); err != nil {
		t.Fatal(err)
	}
	want := []ev{{KeyShiftL, true}, {KeyAltR, true}, {'x', true}, {'x', false}, {KeyAltR, false}, {KeyShiftL, false}}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("got %v", f.events)
	}
}

func TestTypeNotSendable(t *testing.T) {
	f := &fakeSender{}
	p := mustPrepare(t, strings.Repeat("a", MaxChars+1), LayoutDE) // too long
	if err := Type(context.Background(), f, p, 0, nil); !errors.Is(err, ErrNotSendable) {
		t.Fatalf("got %v", err)
	}
	if len(f.events) != 0 {
		t.Fatal("no events expected")
	}
}

func TestTypeCancelMidway(t *testing.T) {
	p := mustPrepare(t, "AB@c", LayoutDE)
	f := &fakeSender{}
	ctx, cancel := context.WithCancel(context.Background())
	err := Type(ctx, f, p, 0, func(d, n int) {
		if d == 2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(f.events) != 8 { // two shifted strokes, 4 events each
		t.Fatalf("events %v", f.events)
	}
	if len(pressed(f.events)) != 0 {
		t.Fatalf("keys left down: %v", pressed(f.events))
	}
}

func TestTypeCancelDuringDelay(t *testing.T) {
	p := mustPrepare(t, "ab", LayoutUS)
	f := &fakeSender{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := Type(ctx, f, p, time.Hour, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if len(f.events) != 2 {
		t.Fatalf("events %v", f.events)
	}
}

func TestTypeSendErrorReleasesModifiers(t *testing.T) {
	boom := errors.New("boom")
	p := mustPrepare(t, "@", LayoutDE) // AltGr+q
	// events: 1 AltGr down, 2 q down (fails)
	f := &fakeSender{failAt: 2, failErr: boom}
	err := Type(context.Background(), f, p, 0, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if len(pressed(f.events)) != 0 {
		t.Fatalf("keys left down: %v (%v)", pressed(f.events), f.events)
	}
	// failure on modifier press itself: shift down fails, nothing else pressed
	p = mustPrepare(t, "A", LayoutUS)
	f = &fakeSender{failAt: 1, failErr: boom}
	if err := Type(context.Background(), f, p, 0, nil); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if want := []ev{{KeyShiftL, true}, {KeyShiftL, false}}; !reflect.DeepEqual(f.events, want) {
		t.Fatalf("got %v\nwant %v", f.events, want)
	}
}

func TestTypeDelay(t *testing.T) {
	p := mustPrepare(t, "abc", LayoutUS)
	f := &fakeSender{}
	start := time.Now()
	if err := Type(context.Background(), f, p, 20*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	el := time.Since(start)
	if el < 38*time.Millisecond { // two pauses, none before first or after last
		t.Fatalf("too fast: %v", el)
	}
	if el > 500*time.Millisecond {
		t.Fatalf("too slow: %v", el)
	}
}

func TestTypeDeadKeySequence(t *testing.T) {
	p := mustPrepare(t, "^", LayoutDE)
	f := &fakeSender{}
	var prog [][2]int
	if err := Type(context.Background(), f, p, 0, func(d, n int) { prog = append(prog, [2]int{d, n}) }); err != nil {
		t.Fatal(err)
	}
	want := []ev{{'`', true}, {'`', false}, {KeySpace, true}, {KeySpace, false}}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("got %v\nwant %v", f.events, want)
	}
	if !reflect.DeepEqual(prog, [][2]int{{1, 1}}) {
		t.Fatalf("progress %v", prog)
	}
}

func TestTypeCancelAfterDeadKey(t *testing.T) {
	p := mustPrepare(t, "a^b", LayoutDE)
	f := &fakeSender{}
	ctx, cancel := context.WithCancel(context.Background())
	err := Type(ctx, f, p, 5*time.Millisecond, func(d, n int) {
		if d == 2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	want := []ev{
		{'a', true}, {'a', false},
		{'`', true}, {'`', false}, {KeySpace, true}, {KeySpace, false},
	}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("got %v\nwant %v", f.events, want)
	}
}
