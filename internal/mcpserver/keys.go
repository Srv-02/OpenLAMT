package mcpserver

import (
	"fmt"
	"strings"

	"github.com/Srv-02/openlamt/internal/kvm"
)

// maxComboKeys bounds one combo: Ctrl+Alt+Shift+Win and one key is the longest useful one.
const maxComboKeys = 5

// keyAliases are short names agents use, in lower case; everything else is a W3C
// KeyboardEvent.code name (kvm.Key.String) in any case, or a single character on the US layout.
var keyAliases = map[string]kvm.Key{
	"ctrl": kvm.ControlLeft, "control": kvm.ControlLeft, "lctrl": kvm.ControlLeft, "rctrl": kvm.ControlRight,
	"alt": kvm.AltLeft, "lalt": kvm.AltLeft, "ralt": kvm.AltRight, "altgr": kvm.AltRight, "option": kvm.AltLeft,
	"shift": kvm.ShiftLeft, "lshift": kvm.ShiftLeft, "rshift": kvm.ShiftRight,
	"win": kvm.MetaLeft, "windows": kvm.MetaLeft, "meta": kvm.MetaLeft, "super": kvm.MetaLeft, "cmd": kvm.MetaLeft, "command": kvm.MetaLeft,
	"esc": kvm.Escape, "return": kvm.Enter, "del": kvm.Delete, "ins": kvm.Insert, "bksp": kvm.Backspace,
	"up": kvm.ArrowUp, "down": kvm.ArrowDown, "left": kvm.ArrowLeft, "right": kvm.ArrowRight,
	"pgup": kvm.PageUp, "pgdn": kvm.PageDown, "pagedn": kvm.PageDown,
	"menu": kvm.ContextMenu, "apps": kvm.ContextMenu, "prtsc": kvm.PrintScreen, "print": kvm.PrintScreen,
	"capslock": kvm.CapsLock, "numlock": kvm.NumLock, "scrolllock": kvm.ScrollLock, "break": kvm.Pause,
}

// keyByLowerName holds every W3C code name of internal/kvm in lower case.
var keyByLowerName = func() map[string]kvm.Key {
	m := map[string]kvm.Key{}
	for k := kvm.KeyUnknown + 1; ; k++ {
		name := k.String()
		if strings.HasPrefix(name, "Key(") {
			return m
		}
		m[strings.ToLower(name)] = k
	}
}()

// parseKeyName resolves one key of a combo.
func parseKeyName(name string) (kvm.Key, bool) {
	lower := strings.ToLower(name)
	if k, ok := keyAliases[lower]; ok {
		return k, true
	}
	if k, ok := keyByLowerName[lower]; ok {
		return k, true
	}
	// A single character names its key on the US layout: a, 7, -, [ and so on.
	if len(lower) == 1 {
		return kvm.KeyForKeysym(uint32(lower[0]))
	}
	return kvm.KeyUnknown, false
}

// parseCombo splits a combo such as "Ctrl+Alt+Delete" into its keys, pressed in this order.
func parseCombo(s string) ([]kvm.Key, error) {
	parts := strings.Split(s, "+")
	if len(parts) > maxComboKeys {
		return nil, fmt.Errorf("%q has more than %d keys", s, maxComboKeys)
	}
	keys := make([]kvm.Key, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("%q has an empty key name", s)
		}
		k, ok := parseKeyName(p)
		if !ok {
			return nil, fmt.Errorf("unknown key %q in %q", p, s)
		}
		for _, have := range keys {
			if have == k {
				return nil, fmt.Errorf("%q names the key %s twice", s, k)
			}
		}
		keys = append(keys, k)
	}
	return keys, nil
}
