package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"github.com/Srv-02/openlamt/internal/kvm"
)

func ev(sc int, name fyne.KeyName) *fyne.KeyEvent {
	return &fyne.KeyEvent{Name: name, Physical: fyne.HardwareKey{ScanCode: sc}}
}

func TestScanCodesPerOS(t *testing.T) {
	cases := []struct {
		goos    string
		wayland bool
		sc      int
		want    kvm.Key
	}{
		{"darwin", false, 0x00, kvm.KeyA}, {"darwin", false, 0x10, kvm.KeyY}, {"darwin", false, 0x06, kvm.KeyZ},
		{"darwin", false, 0x12, kvm.Digit1}, {"darwin", false, 0x53, kvm.Numpad1}, {"darwin", false, 0x24, kvm.Enter},
		{"darwin", false, 0x4C, kvm.NumpadEnter}, {"darwin", false, 0x37, kvm.MetaLeft}, {"darwin", false, 0x3A, kvm.AltLeft},
		{"darwin", false, 0x35, kvm.Escape}, {"darwin", false, 0x7E, kvm.ArrowUp}, {"darwin", false, 0x69, kvm.PrintScreen},
		{"darwin", false, 0x0A, kvm.KeyUnknown}, {"darwin", false, 0x7F, kvm.KeyUnknown}, {"darwin", false, 0x80, kvm.KeyUnknown},
		{"windows", false, 0x01E, kvm.KeyA}, {"windows", false, 0x015, kvm.KeyY}, {"windows", false, 0x02C, kvm.KeyZ},
		{"windows", false, 0x002, kvm.Digit1}, {"windows", false, 0x04F, kvm.Numpad1}, {"windows", false, 0x01C, kvm.Enter},
		{"windows", false, 0x11C, kvm.NumpadEnter}, {"windows", false, 0x01D, kvm.ControlLeft}, {"windows", false, 0x11D, kvm.ControlRight},
		{"windows", false, 0x038, kvm.AltLeft}, {"windows", false, 0x138, kvm.AltRight}, {"windows", false, 0x15B, kvm.MetaLeft},
		{"windows", false, 0x153, kvm.Delete}, {"windows", false, 0x053, kvm.NumpadDecimal}, {"windows", false, 0x045, kvm.Pause},
		{"windows", false, 0x145, kvm.NumLock}, {"windows", false, 0x137, kvm.PrintScreen}, {"windows", false, 0x056, kvm.KeyUnknown},
		{"linux", true, 30, kvm.KeyA}, {"linux", true, 21, kvm.KeyY}, {"linux", true, 44, kvm.KeyZ},
		{"linux", true, 2, kvm.Digit1}, {"linux", true, 79, kvm.Numpad1}, {"linux", true, 28, kvm.Enter},
		{"linux", true, 96, kvm.NumpadEnter}, {"linux", true, 125, kvm.MetaLeft}, {"linux", true, 100, kvm.AltRight},
		{"linux", true, 1, kvm.Escape}, {"linux", true, 86, kvm.KeyUnknown}, {"linux", true, 0, kvm.KeyUnknown},
		{"linux", false, 38, kvm.KeyA}, {"linux", false, 29, kvm.KeyY}, {"linux", false, 52, kvm.KeyZ},
		{"linux", false, 10, kvm.Digit1}, {"linux", false, 9, kvm.Escape}, {"linux", false, 36, kvm.Enter},
		{"linux", false, 133, kvm.MetaLeft}, {"linux", false, 7, kvm.KeyUnknown},
		{"plan9", false, 30, kvm.KeyUnknown},
	}
	for _, c := range cases {
		m := newKeyMapper(c.goos, func() bool { return c.wayland })
		if got := m.Key(ev(c.sc, "")); got != c.want {
			t.Errorf("%s wayland=%v sc=%#x: got %v want %v", c.goos, c.wayland, c.sc, got, c.want)
		}
	}
}

func TestLinuxWaylandProbeRunsOnce(t *testing.T) {
	calls := 0
	m := newKeyMapper("linux", func() bool { calls++; return true })
	for i := 0; i < 3; i++ {
		if got := m.Key(ev(30, "")); got != kvm.KeyA {
			t.Fatalf("got %v", got)
		}
	}
	if calls != 1 {
		t.Fatalf("probe called %d times", calls)
	}
	if got := newKeyMapper("linux", nil).Key(ev(38, "")); got != kvm.KeyA {
		t.Fatalf("nil probe must mean X11, got %v", got)
	}
}

// Every key of the US layout that kvm.Key names must be reachable from all three tables.
func TestAllTablesCoverEveryKVMKey(t *testing.T) {
	skip := map[string]map[kvm.Key]bool{} // none: every kvm.Key is on every platform
	tables := map[string][]kvm.Key{"darwin": macKeys[:], "linux": evdevKeys[:]}
	win := make([]kvm.Key, 0, len(winKeys))
	for _, k := range winKeys {
		win = append(win, k)
	}
	tables["windows"] = win
	for goos, tab := range tables {
		have := map[kvm.Key]bool{}
		for _, k := range tab {
			have[k] = true
		}
		for k := kvm.KeyA; ; k++ {
			name := k.String()
			if len(name) > 4 && name[:4] == "Key(" {
				break // past the last named key
			}
			if !have[k] && !skip[goos][k] {
				t.Errorf("%s: %s not mapped", goos, name)
			}
		}
	}
}
