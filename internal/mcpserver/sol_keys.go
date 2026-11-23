package mcpserver

import (
	"strconv"
	"strings"
)

// solNamedKey is a key of sol_send_keys with its canonical name and the bytes an xterm sends.
type solNamedKey struct{ name, seq string }

// solKeys are the named keys, by lower-case name and alias. The bytes are xterm/VT100 sequences
// (normal cursor key mode); function keys F1–F4 are SS3, F5–F12 CSI ~ codes.
var solKeys = func() map[string]solNamedKey {
	m := map[string]solNamedKey{}
	add := func(name, seq string, aliases ...string) {
		k := solNamedKey{name, seq}
		m[asciiLower(name)] = k
		for _, a := range aliases {
			m[a] = k
		}
	}
	add("Enter", "\r", "return")
	add("Tab", "\t")
	add("Backspace", "\x7f", "bksp")
	add("Escape", "\x1b", "esc")
	add("Space", " ")
	add("Up", "\x1b[A", "arrowup")
	add("Down", "\x1b[B", "arrowdown")
	add("Right", "\x1b[C", "arrowright")
	add("Left", "\x1b[D", "arrowleft")
	add("Home", "\x1b[H")
	add("End", "\x1b[F")
	add("Insert", "\x1b[2~", "ins")
	add("Delete", "\x1b[3~", "del")
	add("PageUp", "\x1b[5~", "pgup")
	add("PageDown", "\x1b[6~", "pgdn")
	for i, seq := range []string{"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", "\x1b[15~", "\x1b[17~",
		"\x1b[18~", "\x1b[19~", "\x1b[20~", "\x1b[21~", "\x1b[23~", "\x1b[24~"} {
		add("F"+strconv.Itoa(i+1), seq)
	}
	return m
}()

// asciiLower lower-cases the ASCII letters of s.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// solKey resolves one entry of sol_send_keys: a named key, Ctrl+<letter>, Alt+<key>, Shift+Tab
// or a single printable ASCII character. Names match with ASCII-only case folding, so a name with
// other characters is unknown. name is the canonical spelling for the prompt and the log.
func solKey(s string) (seq []byte, name string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || !isASCII(s) {
		return nil, "", false
	}
	if len(s) == 1 { // a character, "+" included; its case matters
		if s[0] < 0x20 || s[0] > 0x7e {
			return nil, "", false
		}
		return []byte(s), s, true
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	switch len(parts) {
	case 1:
		k, ok := solKeys[asciiLower(s)]
		return []byte(k.seq), k.name, ok
	case 2:
	default:
		return nil, "", false
	}
	key := parts[1]
	switch asciiLower(parts[0]) {
	case "ctrl", "control":
		if len(key) != 1 {
			return nil, "", false
		}
		c := asciiLower(key)[0]
		if c < 'a' || c > 'z' {
			return nil, "", false
		}
		return []byte{c - 'a' + 1}, "Ctrl+" + strings.ToUpper(key), true
	case "alt":
		seq, name, ok := solKey(key)
		if !ok {
			return nil, "", false
		}
		return append([]byte{0x1b}, seq...), "Alt+" + name, true
	case "shift":
		if asciiLower(key) == "tab" {
			return []byte("\x1b[Z"), "Shift+Tab", true
		}
	}
	return nil, "", false
}
