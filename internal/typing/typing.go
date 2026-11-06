// Package typing translates text into key strokes for the "type clipboard" feature
// and sends them through a KeySender. It has no UI and does not depend on the KVM code.
//
// AMT is assumed to pass keys on by US physical position (to be verified on the test
// device). For every character the package therefore computes which physical key, named
// by the keysym it produces on a US layout, plus modifiers yields that character on the
// target layout.
package typing

import (
	"errors"
	"strings"
	"time"
)

// Layout of the target system. The string is the stable code stored in favorites.json and
// in the preferences.
type Layout string

const (
	// LayoutDE is the German/Austrian (T1) layout.
	LayoutDE Layout = "de"
	// LayoutUS is the US layout.
	LayoutUS Layout = "us"
	// LayoutCHDE is the Swiss German layout.
	LayoutCHDE Layout = "ch-de"
	// LayoutCHFR is the Swiss French layout.
	LayoutCHFR Layout = "ch-fr"
	// LayoutFR is the French AZERTY layout.
	LayoutFR Layout = "fr"
	// LayoutBE is the Belgian AZERTY layout.
	LayoutBE Layout = "be"
	// LayoutUK is the British layout.
	LayoutUK Layout = "uk"
	// LayoutIT is the Italian layout.
	LayoutIT Layout = "it"
	// LayoutES is the Spanish layout.
	LayoutES Layout = "es"
	// LayoutPT is the Portuguese layout.
	LayoutPT Layout = "pt"
	// LayoutSEFI is the Swedish/Finnish layout.
	LayoutSEFI Layout = "se-fi"
	// LayoutNO is the Norwegian layout.
	LayoutNO Layout = "no"
	// LayoutDK is the Danish layout.
	LayoutDK Layout = "dk"
	// LayoutBR is the Brazilian ABNT2 layout.
	LayoutBR Layout = "br"
	// LayoutRU is the Russian layout (Cyrillic, the target must have it active).
	LayoutRU Layout = "ru"
	// LayoutJP is the Japanese JIS layout (ASCII characters and symbols only).
	LayoutJP Layout = "jp"
)

// Layouts lists every layout in the order of the choice menus.
func Layouts() []Layout {
	return []Layout{LayoutDE, LayoutUS, LayoutUK, LayoutCHDE, LayoutCHFR, LayoutFR, LayoutBE, LayoutIT,
		LayoutES, LayoutPT, LayoutBR, LayoutSEFI, LayoutNO, LayoutDK, LayoutRU, LayoutJP}
}

// Label is the short name shown in menus, e.g. "CH-DE".
func (l Layout) Label() string { return strings.ToUpper(string(l)) }

// MaxChars is the maximum number of runes that may be typed in one run.
const MaxChars = 2000

// DefaultDelay is the pause between two strokes; to be verified on the test device.
const DefaultDelay = 30 * time.Millisecond

var (
	// ErrUnknownLayout is returned by Prepare for a layout that is not in Layouts.
	ErrUnknownLayout = errors.New("typing: unknown layout")
	// ErrNotSendable is returned by Type for a plan that is not sendable.
	ErrNotSendable = errors.New("typing: plan is not sendable")
)

// ParseLayout parses a layout code or label ("de", "CH-DE", "gb" for "uk"), case-insensitively.
func ParseLayout(s string) (Layout, bool) {
	l := Layout(strings.ToLower(strings.TrimSpace(s)))
	if l == "gb" {
		l = LayoutUK
	}
	for _, known := range Layouts() {
		if l == known {
			return l, true
		}
	}
	return "", false
}

// KeySender sends one key event. A later adapter maps keysyms to internal/kvm.
type KeySender interface {
	SendKey(keysym uint32, down bool) error
}

// Stroke is one key press with modifiers. Keysym is always the unshifted keysym of
// the physical key on a US layout.
type Stroke struct {
	Keysym       uint32
	Shift, AltGr bool
	// Dead marks a dead key; Type follows it with Space so it produces the character itself.
	Dead bool
	// Compose marks a dead key that starts a composed character (ã = dead ~ + a): the next
	// stroke follows at once instead of Space, and the two count as one character.
	Compose bool
}
