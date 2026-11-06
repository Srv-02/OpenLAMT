package ui

import (
	"fyne.io/fyne/v2"

	"github.com/Srv-02/openlamt/internal/kvm"
)

// keyMapper turns a Fyne key event into the physical key for the KVM session. The lookup goes
// by KeyEvent.Physical.ScanCode, which Fyne fills with GLFW's platform scancode
// (internal/driver/glfw/window.go:726): the macOS virtual key code (kVK_*), the Windows
// scan code set 1 with 0x100 for the E0 prefix, or on Linux the evdev code (Wayland) or the
// X11 keycode, which is evdev + 8. KeyEvent.Name is not consulted: it is layout-dependent for
// letters and punctuation, and the tables cover every kvm.Key.
type keyMapper struct {
	goos    string
	wayland func() bool // Linux only, evaluated lazily on the first lookup
	offset  int         // Linux: 8 for X11, 0 for Wayland
	probed  bool
}

// newKeyMapper builds the mapper for goos ("darwin", "windows", "linux"); wayland is consulted
// once on Linux and may be nil (X11 is assumed then).
func newKeyMapper(goos string, wayland func() bool) *keyMapper {
	return &keyMapper{goos: goos, wayland: wayland}
}

// Key returns the physical key of e or kvm.KeyUnknown.
func (m *keyMapper) Key(e *fyne.KeyEvent) kvm.Key { return m.scanKey(e.Physical.ScanCode) }

func (m *keyMapper) scanKey(sc int) kvm.Key {
	switch m.goos {
	case "darwin":
		return lookup(macKeys[:], sc)
	case "windows":
		return winKeys[sc]
	case "linux":
		if !m.probed {
			m.probed = true
			if m.wayland == nil || !m.wayland() {
				m.offset = 8
			}
		}
		return lookup(evdevKeys[:], sc-m.offset)
	}
	return kvm.KeyUnknown
}

func lookup(table []kvm.Key, i int) kvm.Key {
	if i < 0 || i >= len(table) {
		return kvm.KeyUnknown
	}
	return table[i]
}

// macKeys is indexed by the macOS virtual key code (HIToolbox Events.h kVK_*, as GLFW passes
// [event keyCode] through: cocoa_window.m:567). GLFW maps kVK 0x69/0x6B/0x71 to
// F13/F14/F15 (cocoa_init.m); here they are PrintScreen/ScrollLock/Pause by their position on
// a PC keyboard, deliberately. 0x0A (kVK_ISO_Section) has no
// kvm.Key; macOS swaps 0x0A and 0x32 on ISO keyboards.
var macKeys = [128]kvm.Key{
	0x00: kvm.KeyA, 0x01: kvm.KeyS, 0x02: kvm.KeyD, 0x03: kvm.KeyF, 0x04: kvm.KeyH, 0x05: kvm.KeyG,
	0x06: kvm.KeyZ, 0x07: kvm.KeyX, 0x08: kvm.KeyC, 0x09: kvm.KeyV, 0x0B: kvm.KeyB, 0x0C: kvm.KeyQ,
	0x0D: kvm.KeyW, 0x0E: kvm.KeyE, 0x0F: kvm.KeyR, 0x10: kvm.KeyY, 0x11: kvm.KeyT,
	0x12: kvm.Digit1, 0x13: kvm.Digit2, 0x14: kvm.Digit3, 0x15: kvm.Digit4, 0x16: kvm.Digit6,
	0x17: kvm.Digit5, 0x18: kvm.Equal, 0x19: kvm.Digit9, 0x1A: kvm.Digit7, 0x1B: kvm.Minus,
	0x1C: kvm.Digit8, 0x1D: kvm.Digit0, 0x1E: kvm.BracketRight, 0x1F: kvm.KeyO, 0x20: kvm.KeyU,
	0x21: kvm.BracketLeft, 0x22: kvm.KeyI, 0x23: kvm.KeyP, 0x24: kvm.Enter, 0x25: kvm.KeyL,
	0x26: kvm.KeyJ, 0x27: kvm.Quote, 0x28: kvm.KeyK, 0x29: kvm.Semicolon, 0x2A: kvm.Backslash,
	0x2B: kvm.Comma, 0x2C: kvm.Slash, 0x2D: kvm.KeyN, 0x2E: kvm.KeyM, 0x2F: kvm.Period,
	0x30: kvm.Tab, 0x31: kvm.Space, 0x32: kvm.Backquote, 0x33: kvm.Backspace, 0x35: kvm.Escape,
	0x36: kvm.MetaRight, 0x37: kvm.MetaLeft, 0x38: kvm.ShiftLeft, 0x39: kvm.CapsLock,
	0x3A: kvm.AltLeft, 0x3B: kvm.ControlLeft, 0x3C: kvm.ShiftRight, 0x3D: kvm.AltRight,
	0x3E: kvm.ControlRight, 0x41: kvm.NumpadDecimal, 0x43: kvm.NumpadMultiply, 0x45: kvm.NumpadAdd,
	0x47: kvm.NumLock, 0x4B: kvm.NumpadDivide, 0x4C: kvm.NumpadEnter, 0x4E: kvm.NumpadSubtract,
	0x52: kvm.Numpad0, 0x53: kvm.Numpad1, 0x54: kvm.Numpad2, 0x55: kvm.Numpad3, 0x56: kvm.Numpad4,
	0x57: kvm.Numpad5, 0x58: kvm.Numpad6, 0x59: kvm.Numpad7, 0x5B: kvm.Numpad8, 0x5C: kvm.Numpad9,
	0x60: kvm.F5, 0x61: kvm.F6, 0x62: kvm.F7, 0x63: kvm.F3, 0x64: kvm.F8, 0x65: kvm.F9,
	0x67: kvm.F11, 0x69: kvm.PrintScreen, 0x6B: kvm.ScrollLock, 0x6D: kvm.F10, 0x6E: kvm.ContextMenu,
	0x6F: kvm.F12, 0x71: kvm.Pause, 0x72: kvm.Insert, 0x73: kvm.Home, 0x74: kvm.PageUp,
	0x75: kvm.Delete, 0x76: kvm.F4, 0x77: kvm.End, 0x78: kvm.F2, 0x79: kvm.PageDown, 0x7A: kvm.F1,
	0x7B: kvm.ArrowLeft, 0x7C: kvm.ArrowRight, 0x7D: kvm.ArrowDown, 0x7E: kvm.ArrowUp,
}

// winKeys is keyed by the Windows scan code set 1 as GLFW reports it (win32_window.c:715:
// HIWORD(lParam) & (KF_EXTENDED | 0xff), so E0-prefixed keys carry 0x100; Alt+PrtSc and
// Ctrl+Pause are normalised to 0x137 and 0x45 by GLFW). Same key set as win32_init.c:205-325.
var winKeys = map[int]kvm.Key{
	0x001: kvm.Escape, 0x002: kvm.Digit1, 0x003: kvm.Digit2, 0x004: kvm.Digit3, 0x005: kvm.Digit4,
	0x006: kvm.Digit5, 0x007: kvm.Digit6, 0x008: kvm.Digit7, 0x009: kvm.Digit8, 0x00A: kvm.Digit9,
	0x00B: kvm.Digit0, 0x00C: kvm.Minus, 0x00D: kvm.Equal, 0x00E: kvm.Backspace, 0x00F: kvm.Tab,
	0x010: kvm.KeyQ, 0x011: kvm.KeyW, 0x012: kvm.KeyE, 0x013: kvm.KeyR, 0x014: kvm.KeyT,
	0x015: kvm.KeyY, 0x016: kvm.KeyU, 0x017: kvm.KeyI, 0x018: kvm.KeyO, 0x019: kvm.KeyP,
	0x01A: kvm.BracketLeft, 0x01B: kvm.BracketRight, 0x01C: kvm.Enter, 0x01D: kvm.ControlLeft,
	0x01E: kvm.KeyA, 0x01F: kvm.KeyS, 0x020: kvm.KeyD, 0x021: kvm.KeyF, 0x022: kvm.KeyG,
	0x023: kvm.KeyH, 0x024: kvm.KeyJ, 0x025: kvm.KeyK, 0x026: kvm.KeyL, 0x027: kvm.Semicolon,
	0x028: kvm.Quote, 0x029: kvm.Backquote, 0x02A: kvm.ShiftLeft, 0x02B: kvm.Backslash,
	0x02C: kvm.KeyZ, 0x02D: kvm.KeyX, 0x02E: kvm.KeyC, 0x02F: kvm.KeyV, 0x030: kvm.KeyB,
	0x031: kvm.KeyN, 0x032: kvm.KeyM, 0x033: kvm.Comma, 0x034: kvm.Period, 0x035: kvm.Slash,
	0x036: kvm.ShiftRight, 0x037: kvm.NumpadMultiply, 0x038: kvm.AltLeft, 0x039: kvm.Space,
	0x03A: kvm.CapsLock, 0x03B: kvm.F1, 0x03C: kvm.F2, 0x03D: kvm.F3, 0x03E: kvm.F4, 0x03F: kvm.F5,
	0x040: kvm.F6, 0x041: kvm.F7, 0x042: kvm.F8, 0x043: kvm.F9, 0x044: kvm.F10, 0x045: kvm.Pause,
	0x046: kvm.ScrollLock, 0x047: kvm.Numpad7, 0x048: kvm.Numpad8, 0x049: kvm.Numpad9,
	0x04A: kvm.NumpadSubtract, 0x04B: kvm.Numpad4, 0x04C: kvm.Numpad5, 0x04D: kvm.Numpad6,
	0x04E: kvm.NumpadAdd, 0x04F: kvm.Numpad1, 0x050: kvm.Numpad2, 0x051: kvm.Numpad3,
	0x052: kvm.Numpad0, 0x053: kvm.NumpadDecimal, 0x057: kvm.F11, 0x058: kvm.F12,
	0x11C: kvm.NumpadEnter, 0x11D: kvm.ControlRight, 0x135: kvm.NumpadDivide, 0x137: kvm.PrintScreen,
	0x138: kvm.AltRight, 0x145: kvm.NumLock, 0x147: kvm.Home, 0x148: kvm.ArrowUp, 0x149: kvm.PageUp,
	0x14B: kvm.ArrowLeft, 0x14D: kvm.ArrowRight, 0x14F: kvm.End, 0x150: kvm.ArrowDown,
	0x151: kvm.PageDown, 0x152: kvm.Insert, 0x153: kvm.Delete, 0x15B: kvm.MetaLeft,
	0x15C: kvm.MetaRight, 0x15D: kvm.ContextMenu,
}

// evdevKeys is indexed by the Linux evdev key code (linux/input-event-codes.h KEY_*), which
// GLFW reports unchanged under Wayland (wl_window.c:1806) and plus 8 as X11 keycode
// (x11_window.c:1305). Same key set as wl_init.c:265-382; KEY_102ND (86) has no kvm.Key.
// evdev 99 (KEY_SYSRQ) is PrintScreen on purpose, where GLFW uses KEY_PRINT (210).
var evdevKeys = [128]kvm.Key{
	1: kvm.Escape, 2: kvm.Digit1, 3: kvm.Digit2, 4: kvm.Digit3, 5: kvm.Digit4, 6: kvm.Digit5,
	7: kvm.Digit6, 8: kvm.Digit7, 9: kvm.Digit8, 10: kvm.Digit9, 11: kvm.Digit0, 12: kvm.Minus,
	13: kvm.Equal, 14: kvm.Backspace, 15: kvm.Tab, 16: kvm.KeyQ, 17: kvm.KeyW, 18: kvm.KeyE,
	19: kvm.KeyR, 20: kvm.KeyT, 21: kvm.KeyY, 22: kvm.KeyU, 23: kvm.KeyI, 24: kvm.KeyO,
	25: kvm.KeyP, 26: kvm.BracketLeft, 27: kvm.BracketRight, 28: kvm.Enter, 29: kvm.ControlLeft,
	30: kvm.KeyA, 31: kvm.KeyS, 32: kvm.KeyD, 33: kvm.KeyF, 34: kvm.KeyG, 35: kvm.KeyH,
	36: kvm.KeyJ, 37: kvm.KeyK, 38: kvm.KeyL, 39: kvm.Semicolon, 40: kvm.Quote, 41: kvm.Backquote,
	42: kvm.ShiftLeft, 43: kvm.Backslash, 44: kvm.KeyZ, 45: kvm.KeyX, 46: kvm.KeyC, 47: kvm.KeyV,
	48: kvm.KeyB, 49: kvm.KeyN, 50: kvm.KeyM, 51: kvm.Comma, 52: kvm.Period, 53: kvm.Slash,
	54: kvm.ShiftRight, 55: kvm.NumpadMultiply, 56: kvm.AltLeft, 57: kvm.Space, 58: kvm.CapsLock,
	59: kvm.F1, 60: kvm.F2, 61: kvm.F3, 62: kvm.F4, 63: kvm.F5, 64: kvm.F6, 65: kvm.F7, 66: kvm.F8,
	67: kvm.F9, 68: kvm.F10, 69: kvm.NumLock, 70: kvm.ScrollLock, 71: kvm.Numpad7, 72: kvm.Numpad8,
	73: kvm.Numpad9, 74: kvm.NumpadSubtract, 75: kvm.Numpad4, 76: kvm.Numpad5, 77: kvm.Numpad6,
	78: kvm.NumpadAdd, 79: kvm.Numpad1, 80: kvm.Numpad2, 81: kvm.Numpad3, 82: kvm.Numpad0,
	83: kvm.NumpadDecimal, 87: kvm.F11, 88: kvm.F12, 96: kvm.NumpadEnter, 97: kvm.ControlRight,
	98: kvm.NumpadDivide, 99: kvm.PrintScreen, 100: kvm.AltRight, 102: kvm.Home, 103: kvm.ArrowUp,
	104: kvm.PageUp, 105: kvm.ArrowLeft, 106: kvm.ArrowRight, 107: kvm.End, 108: kvm.ArrowDown,
	109: kvm.PageDown, 110: kvm.Insert, 111: kvm.Delete, 119: kvm.Pause, 125: kvm.MetaLeft,
	126: kvm.MetaRight, 127: kvm.ContextMenu,
}
