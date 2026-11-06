// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/Utilities/AMTKeyCodeTable.ts, src/core/Utilities/AMTKeyCodeConverter.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import "strconv"

// Key is a physical key position. Names follow W3C KeyboardEvent.code, the identifier
// ui-toolkit's AMTKeyCodeTable is keyed on; mapping from Fyne events is Etappe 3.
type Key uint8

const (
	KeyUnknown Key = iota
	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	Digit0
	Digit1
	Digit2
	Digit3
	Digit4
	Digit5
	Digit6
	Digit7
	Digit8
	Digit9
	F1
	F2
	F3
	F4
	F5
	F6
	F7
	F8
	F9
	F10
	F11
	F12
	Enter
	Escape
	Backspace
	Tab
	Space
	Minus
	Equal
	BracketLeft
	BracketRight
	Backslash
	Semicolon
	Quote
	Backquote
	Comma
	Period
	Slash
	CapsLock
	PrintScreen
	ScrollLock
	Pause
	Insert
	Home
	PageUp
	Delete
	End
	PageDown
	ArrowRight
	ArrowLeft
	ArrowDown
	ArrowUp
	NumLock
	NumpadDivide
	NumpadMultiply
	NumpadSubtract
	NumpadAdd
	NumpadEnter
	Numpad0
	Numpad1
	Numpad2
	Numpad3
	Numpad4
	Numpad5
	Numpad6
	Numpad7
	Numpad8
	Numpad9
	NumpadDecimal
	ControlLeft
	ShiftLeft
	AltLeft
	MetaLeft
	ControlRight
	ShiftRight
	AltRight
	MetaRight
	ContextMenu

	keyCount // sentinel
)

// keysyms holds the unshifted X11 keysym per key (letters lowercase).
var keysyms = [keyCount]uint32{
	Enter: 0xff0d, NumpadEnter: 0xff0d, Escape: 0xff1b, Backspace: 0xff08, Tab: 0xff09, Space: 0x20,
	Minus: 0x2d, NumpadSubtract: 0x2d, Equal: 0x3d, BracketLeft: 0x5b, BracketRight: 0x5d,
	Backslash: 0x5c, Semicolon: 0x3b, Quote: 0x27, Backquote: 0x60, Comma: 0x2c,
	Period: 0x2e, NumpadDecimal: 0x2e, Slash: 0x2f, NumpadDivide: 0x2f,
	NumpadMultiply: 0x2a, NumpadAdd: 0x2b,
	Insert: 0xff63, Home: 0xff50, PageUp: 0xff55, PageDown: 0xff56, End: 0xff57, Delete: 0xffff,
	ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54,
	ShiftLeft: 0xffe1, ShiftRight: 0xffe2, ControlLeft: 0xffe3, ControlRight: 0xffe4,
	AltLeft: 0xffe9, AltRight: 0xffea, MetaLeft: 0xffe7, MetaRight: 0xffe8,
	// X11 values; AMTKeyCodeTable.ts carries other codes for these (plan assumption 8)
	CapsLock: 0xffe5, NumLock: 0xff7f, ScrollLock: 0xff14, Pause: 0xff13,
	PrintScreen: 0xff61, ContextMenu: 0xff67,
}

var keyNames = [keyCount]string{
	Enter: "Enter", Escape: "Escape", Backspace: "Backspace", Tab: "Tab", Space: "Space",
	Minus: "Minus", Equal: "Equal", BracketLeft: "BracketLeft", BracketRight: "BracketRight",
	Backslash: "Backslash", Semicolon: "Semicolon", Quote: "Quote", Backquote: "Backquote",
	Comma: "Comma", Period: "Period", Slash: "Slash", CapsLock: "CapsLock",
	PrintScreen: "PrintScreen", ScrollLock: "ScrollLock", Pause: "Pause", Insert: "Insert",
	Home: "Home", PageUp: "PageUp", Delete: "Delete", End: "End", PageDown: "PageDown",
	ArrowRight: "ArrowRight", ArrowLeft: "ArrowLeft", ArrowDown: "ArrowDown", ArrowUp: "ArrowUp",
	NumLock: "NumLock", NumpadDivide: "NumpadDivide", NumpadMultiply: "NumpadMultiply",
	NumpadSubtract: "NumpadSubtract", NumpadAdd: "NumpadAdd", NumpadEnter: "NumpadEnter",
	NumpadDecimal: "NumpadDecimal", ControlLeft: "ControlLeft", ShiftLeft: "ShiftLeft",
	AltLeft: "AltLeft", MetaLeft: "MetaLeft", ControlRight: "ControlRight",
	ShiftRight: "ShiftRight", AltRight: "AltRight", MetaRight: "MetaRight", ContextMenu: "ContextMenu",
}

var keysByName = map[string]Key{}

// keysBySym is the inverse of keysyms for KeyForKeysym.
var keysBySym = map[uint32]Key{}

func init() {
	for i := Key(0); i < 26; i++ {
		keysyms[KeyA+i] = 0x61 + uint32(i)
		keyNames[KeyA+i] = "Key" + string(rune('A'+i))
	}
	for i := Key(0); i < 10; i++ {
		keysyms[Digit0+i] = 0x30 + uint32(i)
		keysyms[Numpad0+i] = 0x30 + uint32(i)
		keyNames[Digit0+i] = "Digit" + string(rune('0'+i))
		keyNames[Numpad0+i] = "Numpad" + string(rune('0'+i))
	}
	for i := Key(0); i < 12; i++ {
		keysyms[F1+i] = 0xffbe + uint32(i)
		keyNames[F1+i] = "F" + strconv.Itoa(int(i)+1)
	}
	for k := KeyUnknown + 1; k < keyCount; k++ {
		keysByName[keyNames[k]] = k
	}
	// main block first; numpad keys only fill keysyms no main-block key has
	for pass := 0; pass < 2; pass++ {
		for k := KeyUnknown + 1; k < keyCount; k++ {
			if isNumpad(k) != (pass == 1) || keysyms[k] == 0 {
				continue
			}
			if _, taken := keysBySym[keysyms[k]]; !taken {
				keysBySym[keysyms[k]] = k
			}
		}
	}
}

func isNumpad(k Key) bool {
	return k >= NumpadDivide && k <= NumpadDecimal
}

// String returns the W3C code name, or "Key(N)" for an unknown key.
func (k Key) String() string {
	if k > KeyUnknown && k < keyCount {
		return keyNames[k]
	}
	return "Key(" + strconv.Itoa(int(k)) + ")"
}

// ParseKey resolves an exact, case-sensitive W3C code name.
func ParseKey(name string) (Key, bool) {
	k, ok := keysByName[name]
	return k, ok
}

// Keysym returns the X11 keysym AMT expects for the key in the US layout: letters lowercase,
// uppercase while shift is held (AMTKeyCodeConverter.ts:13-14); digits, numpad digits and
// punctuation always the unshifted character. ok is false for KeyUnknown.
func Keysym(k Key, shift bool) (sym uint32, ok bool) {
	if k <= KeyUnknown || k >= keyCount {
		return 0, false
	}
	sym = keysyms[k]
	if shift && k >= KeyA && k <= KeyZ {
		sym -= 0x20
	}
	return sym, true
}

// KeyForKeysym returns the key whose unshifted keysym is sym, the inverse of Keysym(k, false).
// The main block wins over the numpad (Digit0 over Numpad0, Minus over NumpadSubtract, Enter
// over NumpadEnter); '*' and '+' exist only on the numpad and resolve to NumpadMultiply and
// NumpadAdd. Shifted keysyms (uppercase letters, '!') and unknown ones yield false.
func KeyForKeysym(sym uint32) (Key, bool) {
	k, ok := keysBySym[sym]
	return k, ok
}
