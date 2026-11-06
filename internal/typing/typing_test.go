package typing

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func st(k uint32) Stroke    { return Stroke{Keysym: k} }
func shift(k uint32) Stroke { return Stroke{Keysym: k, Shift: true} }
func altgr(k uint32) Stroke { return Stroke{Keysym: k, AltGr: true} }
func mustPrepare(t *testing.T, text string, l Layout) Plan {
	t.Helper()
	p, err := Prepare(text, l)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return p
}

func TestAcceptanceCriterion5DE(t *testing.T) {
	p := mustPrepare(t, "Test-123 äöü yz!@#", LayoutDE)
	if !p.Sendable() || len(p.Unmappable) != 0 {
		t.Fatalf("not sendable: %+v", p)
	}
	want := []Stroke{
		shift('t'), st('e'), st('s'), st('t'), st('/'), // "Test-"
		st('1'), st('2'), st('3'), st(KeySpace),
		st('\''), st(';'), st('['), st(KeySpace), // äöü
		st('z'), st('y'), // yz on DE
		shift('1'), altgr('q'), st('\\'), // !@#
	}
	if !reflect.DeepEqual(p.Strokes, want) {
		t.Fatalf("strokes\n got %v\nwant %v", p.Strokes, want)
	}
}

func TestAcceptanceCriterion5US(t *testing.T) {
	p := mustPrepare(t, "Test-123 yz!@#", LayoutUS)
	if !p.Sendable() || len(p.Unmappable) != 0 {
		t.Fatalf("not sendable: %+v", p)
	}
	want := []Stroke{
		shift('t'), st('e'), st('s'), st('t'), st('-'),
		st('1'), st('2'), st('3'), st(KeySpace),
		st('y'), st('z'),
		shift('1'), shift('2'), shift('3'),
	}
	if !reflect.DeepEqual(p.Strokes, want) {
		t.Fatalf("strokes\n got %v\nwant %v", p.Strokes, want)
	}
}

func TestDeadKeysDE(t *testing.T) {
	cases := map[string][]Stroke{
		"^": {{Keysym: '`', Dead: true}},
		"´": {{Keysym: '=', Dead: true}},
		"`": {{Keysym: '=', Shift: true, Dead: true}},
	}
	for in, want := range cases {
		p := mustPrepare(t, in, LayoutDE)
		if !reflect.DeepEqual(p.Strokes, want) {
			t.Errorf("%q: got %v want %v", in, p.Strokes, want)
		}
		if len(p.Strokes) != p.Chars {
			t.Errorf("%q: %d strokes for %d chars", in, len(p.Strokes), p.Chars)
		}
	}
}

func TestDeadKeysUS(t *testing.T) {
	p := mustPrepare(t, "^`", LayoutUS)
	want := []Stroke{shift('6'), st('`')}
	if !reflect.DeepEqual(p.Strokes, want) {
		t.Fatalf("got %v want %v", p.Strokes, want)
	}
	p = mustPrepare(t, "a´", LayoutUS)
	if len(p.Unmappable) != 1 || p.Unmappable[0].Char != '´' {
		t.Fatalf("´ must be unmappable on US: %+v", p)
	}
	if !p.Sendable() || len(p.Strokes) != 1 {
		t.Fatalf("the a is still typed: %+v", p)
	}
}

func TestUnmappableISOKeyDE(t *testing.T) {
	p := mustPrepare(t, "a<\nb>|", LayoutDE)
	want := []Unmappable{
		{Char: '<', Index: 1, Line: 1, Column: 2},
		{Char: '>', Index: 4, Line: 2, Column: 2},
		{Char: '|', Index: 5, Line: 2, Column: 3},
	}
	if !reflect.DeepEqual(p.Unmappable, want) {
		t.Fatalf("got %v want %v", p.Unmappable, want)
	}
	// untypeable characters are skipped, everything else is typed
	if !p.Sendable() || len(p.Strokes) != 3 || p.Typeable() != 3 || p.Chars != 6 {
		t.Fatalf("a, Enter and b must be typed: %+v", p)
	}
	if p.Strokes[0] != st('a') || p.Strokes[1] != st(KeyReturn) || p.Strokes[2] != st('b') {
		t.Fatalf("strokes %v", p.Strokes)
	}
}

// A line break that only ends the text after the untypeable characters are skipped must not
// be typed: the command would run on the target.
func TestSkippedCharactersDoNotLeaveATrailingEnter(t *testing.T) {
	for _, in := range []string{"cmd\n<", "cmd\n<\n<", "cmd\n<\n", "cmd\r\n|\r\n>", "cmd\n<\n<\n\n"} {
		p := mustPrepare(t, in, LayoutDE)
		want := []Stroke{st('c'), st('m'), st('d')}
		if !p.Sendable() || !reflect.DeepEqual(p.Strokes, want) || p.Typeable() != 3 {
			t.Errorf("%q: strokes %v typeable %d", in, p.Strokes, p.Typeable())
		}
	}
	// a line break between typeable characters stays
	p := mustPrepare(t, "a\n<b", LayoutDE)
	if want := []Stroke{st('a'), st(KeyReturn), st('b')}; !reflect.DeepEqual(p.Strokes, want) {
		t.Errorf("strokes %v", p.Strokes)
	}
	// only line breaks and untypeable characters: nothing at all, not even Enter
	if p := mustPrepare(t, "\n<", LayoutDE); len(p.Strokes) != 0 || p.Typeable() != 0 {
		t.Errorf("strokes %v", p.Strokes)
	}
}

func TestOnlyUnmappableCharactersTypeNothing(t *testing.T) {
	p := mustPrepare(t, "<|>", LayoutDE)
	if !p.Sendable() || len(p.Strokes) != 0 || p.Typeable() != 0 {
		t.Fatalf("%+v", p)
	}
	if err := Type(context.Background(), &fakeSender{}, p, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUnmappableOthers(t *testing.T) {
	p := mustPrepare(t, "a€b\x01ñ", LayoutUS)
	if len(p.Unmappable) != 3 {
		t.Fatalf("got %v", p.Unmappable)
	}
	if p.Unmappable[0].Char != '€' || p.Unmappable[1].Char != 0x01 || p.Unmappable[2].Char != 'ñ' {
		t.Fatalf("got %v", p.Unmappable)
	}
}

func TestLineBreaks(t *testing.T) {
	for _, in := range []string{"a\r\nb", "a\rb", "a\nb"} {
		p := mustPrepare(t, in, LayoutUS)
		want := []Stroke{st('a'), st(KeyReturn), st('b')}
		if !reflect.DeepEqual(p.Strokes, want) || !p.Multiline || p.Text != "a\nb" || p.Chars != 3 {
			t.Errorf("%q: %+v", in, p)
		}
	}
}

func TestTrailingLineBreaksRemoved(t *testing.T) {
	for _, in := range []string{"ab\n", "ab\r\n", "ab\n\n\r\n\r", "ab\r\n\r\n"} {
		p := mustPrepare(t, in, LayoutUS)
		if p.Text != "ab" || p.Multiline || p.Chars != 2 {
			t.Errorf("%q: %+v", in, p)
		}
	}
	p := mustPrepare(t, "ab \t\n", LayoutUS)
	if p.Text != "ab \t" {
		t.Errorf("spaces/tabs must stay: %q", p.Text)
	}
}

func TestTab(t *testing.T) {
	p := mustPrepare(t, "a\tb", LayoutDE)
	if p.Strokes[1] != st(KeyTab) {
		t.Fatalf("got %v", p.Strokes)
	}
}

func TestLimit(t *testing.T) {
	p := mustPrepare(t, strings.Repeat("ä", MaxChars), LayoutDE)
	if p.TooLong || !p.Sendable() || p.Chars != MaxChars {
		t.Fatalf("2000 must be ok: %+v", p.Chars)
	}
	p = mustPrepare(t, strings.Repeat("ä", MaxChars+1), LayoutDE)
	if !p.TooLong || p.Sendable() || len(p.Strokes) != 0 {
		t.Fatalf("2001 must be too long")
	}
}

func TestEmpty(t *testing.T) {
	for _, in := range []string{"", "\n\r\n"} {
		p := mustPrepare(t, in, LayoutUS)
		if p.Sendable() || p.Chars != 0 {
			t.Errorf("%q must not be sendable", in)
		}
	}
}

func TestParseLayout(t *testing.T) {
	for in, want := range map[string]Layout{"de": LayoutDE, "DE": LayoutDE, "Us": LayoutUS, "us": LayoutUS} {
		if got, ok := ParseLayout(in); !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	if _, ok := ParseLayout("xx"); ok {
		t.Error("xx must fail")
	}
}

func TestUnknownLayout(t *testing.T) {
	if _, err := Prepare("a", Layout("xx")); err != ErrUnknownLayout {
		t.Fatalf("got %v", err)
	}
}

func TestTables(t *testing.T) {
	for name, tbl := range map[string]map[rune]Stroke{"de": tableDE, "us": tableUS} {
		seen := map[Stroke]rune{}
		for r, s := range tbl {
			if s.Keysym == 0 {
				t.Errorf("%s %q: zero keysym", name, r)
			}
			if o, dup := seen[s]; dup {
				t.Errorf("%s: %q and %q share stroke %v", name, r, o, s)
			}
			seen[s] = r
		}
	}
	for r, s := range deadDE {
		if _, dup := tableDE[r]; dup || s.Keysym == 0 {
			t.Errorf("dead %q invalid", r)
		}
	}
	// every printable ASCII char is covered on US
	for r := rune(0x20); r < 0x7f; r++ {
		if _, ok := tableUS[r]; !ok {
			t.Errorf("US missing %q", r)
		}
	}
}
