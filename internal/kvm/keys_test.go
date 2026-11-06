package kvm

import "testing"

func TestKeysymMatchesUIToolkitTable(t *testing.T) {
	// every AMTKeyCodeTable entry whose value is a keysym (AMTKeyCodeTable.ts:10-71)
	want := map[Key]uint32{Space: 32, Quote: 39, Minus: 45, NumpadMultiply: 42, NumpadAdd: 43, Comma: 44,
		NumpadSubtract: 45, NumpadDecimal: 46, Period: 46, Slash: 47, NumpadDivide: 47, Semicolon: 59, Equal: 61,
		BracketLeft: 91, Backslash: 92, BracketRight: 93, Backquote: 96, Backspace: 0xff08, Tab: 0xff09,
		Enter: 0xff0d, NumpadEnter: 0xff0d, Escape: 0xff1b, Delete: 0xffff, Home: 0xff50, PageUp: 0xff55,
		PageDown: 0xff56, ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54, End: 0xff57,
		Insert: 0xff63, F1: 0xffbe, F12: 0xffc9, ShiftLeft: 0xffe1, ShiftRight: 0xffe2, ControlLeft: 0xffe3,
		ControlRight: 0xffe4, AltLeft: 0xffe9, AltRight: 0xffea, MetaLeft: 0xffe7, MetaRight: 0xffe8}
	for k, w := range want {
		if got, ok := Keysym(k, false); !ok || got != w {
			t.Errorf("%s: got %#x ok=%v, want %#x", k, got, ok, w)
		}
	}
}

func TestKeysymLockKeysUseX11Values(t *testing.T) {
	want := map[Key]uint32{CapsLock: 0xffe5, NumLock: 0xff7f, ScrollLock: 0xff14, Pause: 0xff13,
		PrintScreen: 0xff61, ContextMenu: 0xff67}
	for k, w := range want {
		if got, ok := Keysym(k, false); !ok || got != w {
			t.Errorf("%s: got %#x ok=%v, want %#x", k, got, ok, w)
		}
	}
}

func TestKeysymFunctionKeysAreContiguous(t *testing.T) {
	for i := 0; i < 12; i++ {
		k := F1 + Key(i)
		if got, _ := Keysym(k, false); got != 0xffbe+uint32(i) {
			t.Errorf("%s: got %#x", k, got)
		}
	}
}

func TestKeysymLettersFollowShift(t *testing.T) {
	if s, _ := Keysym(KeyA, false); s != 'a' {
		t.Fatalf("a = %#x", s)
	}
	if s, _ := Keysym(KeyA, true); s != 'A' {
		t.Fatalf("A = %#x", s)
	}
	if s, _ := Keysym(KeyZ, true); s != 'Z' {
		t.Fatalf("Z = %#x", s)
	}
}

func TestKeysymDigitsIgnoreShift(t *testing.T) {
	for _, shift := range []bool{false, true} {
		for i := 0; i < 10; i++ {
			for _, k := range []Key{Digit0 + Key(i), Numpad0 + Key(i)} {
				if s, ok := Keysym(k, shift); !ok || s != uint32('0'+i) {
					t.Errorf("%s shift=%v: got %#x ok=%v", k, shift, s, ok)
				}
			}
		}
	}
}

func TestKeyNamesRoundTrip(t *testing.T) {
	for k := KeyUnknown + 1; k < keyCount; k++ {
		got, ok := ParseKey(k.String())
		if !ok || got != k {
			t.Fatalf("%s: parse %v ok=%v", k, got, ok)
		}
		if _, ok := Keysym(k, false); !ok {
			t.Fatalf("%s has no keysym", k)
		}
	}
	if _, ok := ParseKey("keya"); ok {
		t.Fatal("case-insensitive match accepted")
	}
	if _, ok := Keysym(KeyUnknown, false); ok {
		t.Fatal("KeyUnknown mapped")
	}
	if got := Key(250).String(); got != "Key(250)" {
		t.Fatalf("unknown name = %q", got)
	}
	if _, ok := Keysym(Key(250), false); ok {
		t.Fatal("out-of-range key mapped")
	}
	if k := KeyUnknown; k.String() != "Key(0)" {
		t.Fatalf("KeyUnknown name = %q", k.String())
	}
}

func TestKeyNamesFollowW3C(t *testing.T) {
	for k, want := range map[Key]string{KeyA: "KeyA", Digit1: "Digit1", F2: "F2", Numpad9: "Numpad9",
		ArrowUp: "ArrowUp", ControlLeft: "ControlLeft", Backquote: "Backquote", NumpadEnter: "NumpadEnter"} {
		if k.String() != want {
			t.Errorf("got %q, want %q", k.String(), want)
		}
	}
}

func TestKeyForKeysymRoundTrip(t *testing.T) {
	for k := KeyUnknown + 1; k < keyCount; k++ {
		if (k >= Numpad0 && k <= Numpad9) || k == NumpadDivide || k == NumpadSubtract ||
			k == NumpadEnter || k == NumpadDecimal {
			continue // shares its keysym with a main-block key
		}
		sym, _ := Keysym(k, false)
		if got, ok := KeyForKeysym(sym); !ok || got != k {
			t.Errorf("%s: sym %#x -> %s ok=%v", k, sym, got, ok)
		}
	}
}

func TestKeyForKeysymPrefersMainBlock(t *testing.T) {
	for sym, want := range map[uint32]Key{0x30: Digit0, 0x39: Digit9, 0x2d: Minus, 0x2e: Period, 0x2f: Slash, 0xff0d: Enter} {
		if got, ok := KeyForKeysym(sym); !ok || got != want {
			t.Errorf("%#x: got %s ok=%v, want %s", sym, got, ok, want)
		}
	}
}

func TestKeyForKeysymNumpadOnly(t *testing.T) {
	for sym, want := range map[uint32]Key{0x2a: NumpadMultiply, 0x2b: NumpadAdd} {
		if got, ok := KeyForKeysym(sym); !ok || got != want {
			t.Errorf("%#x: got %s ok=%v, want %s", sym, got, ok, want)
		}
	}
}

func TestKeyForKeysymModifiers(t *testing.T) {
	if k, ok := KeyForKeysym(0xffe1); !ok || k != ShiftLeft {
		t.Errorf("Shift_L: %s %v", k, ok)
	}
	if k, ok := KeyForKeysym(0xffea); !ok || k != AltRight {
		t.Errorf("Alt_R: %s %v", k, ok)
	}
}

func TestKeyForKeysymRejectsShiftedAndUnknown(t *testing.T) {
	for _, sym := range []uint32{0, 'A', 'Z', '!', '@', '_', '{', 0x7f, 0xfffff} {
		if k, ok := KeyForKeysym(sym); ok || k != KeyUnknown {
			t.Errorf("%#x: got %s ok=%v", sym, k, ok)
		}
	}
}
