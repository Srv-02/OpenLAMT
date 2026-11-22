package mcpserver

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/kvm"
)

// TestParseCombo: W3C code names, aliases, single characters, any case, spaces around "+".
func TestParseCombo(t *testing.T) {
	for in, want := range map[string][]kvm.Key{
		"Ctrl+Alt+Delete":   {kvm.ControlLeft, kvm.AltLeft, kvm.Delete},
		"ctrl + alt + del":  {kvm.ControlLeft, kvm.AltLeft, kvm.Delete},
		"ControlLeft+KeyC":  {kvm.ControlLeft, kvm.KeyC},
		"Alt+F4":            {kvm.AltLeft, kvm.F4},
		"Shift+Tab":         {kvm.ShiftLeft, kvm.Tab},
		"Win+r":             {kvm.MetaLeft, kvm.KeyR},
		"AltGr+q":           {kvm.AltRight, kvm.KeyQ},
		"Enter":             {kvm.Enter},
		"return":            {kvm.Enter},
		"Esc":               {kvm.Escape},
		"PgDn":              {kvm.PageDown},
		"up":                {kvm.ArrowUp},
		"ArrowLeft":         {kvm.ArrowLeft},
		"7":                 {kvm.Digit7},
		"Numpad7":           {kvm.Numpad7},
		"-":                 {kvm.Minus},
		"Space":             {kvm.Space},
		"F12":               {kvm.F12},
		"Menu":              {kvm.ContextMenu},
		"PrintScreen":       {kvm.PrintScreen},
		"Ctrl+Shift+Escape": {kvm.ControlLeft, kvm.ShiftLeft, kvm.Escape},
	} {
		got, err := parseCombo(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "Ctrl+", "Bogus", "Ctrl+Ctrl", "A+B+C+D+E+F+G", "ä", "Ctrl++"} {
		if _, err := parseCombo(in); err == nil {
			t.Errorf("%q: no error", in)
		} else if !strings.Contains(err.Error(), "key") {
			t.Errorf("%q: %v", in, err)
		}
	}
}
