package ui

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/scan"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The scan tests run against fake listeners on 127.0.0.x. Nothing leaves the computer.

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback:", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// fakeDevices starts: 127.0.0.1 with an HTTP port (AMT 16.1), 127.0.0.2 that only answers the
// RMCP ping, 127.0.0.3 with an HTTP port of another product. It returns the scanner.
func fakeDevices(t *testing.T) *scan.Scanner {
	t.Helper()
	udp, web := freeUDPPort(t), freeTCPPort(t)
	pc, err := net.ListenPacket("udp", "127.0.0.2:"+strconv.Itoa(udp))
	if err != nil {
		t.Skip("cannot bind 127.0.0.2:", err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 12 && buf[8] == 0x80 {
				pc.WriteTo([]byte{0x06, 0x00, 0xff, 0x06, 0x00, 0x00, 0x11, 0xbe, 0x40, buf[9], 0x00, 0x10, 0, 0, 0x11, 0xbe, 0, 0, 0, 0, 0x81, 0, 0, 0, 0, 0, 0, 0}, from)
			}
		}
	}()
	serve := func(ip, server string) {
		l, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(web)))
		if err != nil {
			t.Skip("cannot bind", ip, err)
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Server", server)
		}))
		srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
	}
	serve("127.0.0.1", "Intel(R) Active Management Technology 16.1.25")
	serve("127.0.0.3", "nginx")
	return &scan.Scanner{RMCPPort: udp, HTTPPort: web, HTTPSPort: freeTCPPort(t), Timeout: 500 * time.Millisecond,
		Lookup: func(_ context.Context, a netip.Addr) string {
			if a.String() == "127.0.0.1" {
				return "lab.example.com"
			}
			return ""
		}}
}

// scanHarness opens the dialog and waits for a run to end.
type scanHarness struct {
	*startHarness
	sd       *scanDialog
	finished chan struct{}
}

func newScanHarness(t *testing.T, scanner *scan.Scanner) *scanHarness {
	t.Helper()
	h := &scanHarness{startHarness: newStart(t), finished: make(chan struct{}, 4)}
	prevScanner, prevNet := newScanner, localNetwork
	newScanner = func() *scan.Scanner { return scanner }
	localNetwork = func() (string, bool) { return "192.0.2.0/24", true }
	t.Cleanup(func() { newScanner, localNetwork = prevScanner, prevNet })
	return h
}

func (h *scanHarness) open() {
	h.sd = showScanDialog(h.s.win, h.s.mgr, h.s.showImported)
	h.sd.onFinished = func() { h.finished <- struct{}{} }
}

func (h *scanHarness) scan(t *testing.T, input string) {
	t.Helper()
	h.sd.entry.SetText(input)
	test.Tap(h.sd.goBtn)
	select {
	case <-h.finished:
	case <-time.After(15 * time.Second):
		t.Fatal("the scan did not end")
	}
}

func TestScanDialogPrefillsTheLocalNetworkAndStartsOnRequestOnly(t *testing.T) {
	h := newScanHarness(t, &scan.Scanner{})
	h.open()
	if h.sd.entry.Text != "192.0.2.0/24" {
		t.Errorf("address range %q", h.sd.entry.Text)
	}
	top := h.s.win.Canvas().Overlays().Top()
	for _, want := range []string{"Scan Network", "Address range"} {
		if !hasText(top, want) {
			t.Errorf("dialog lacks %q", want)
		}
	}
	if !hasTextPrefix(top, "A network such as 192.0.2.0/24 or a range such as 192.0.2.10-192.0.2.80.") {
		t.Errorf("hint missing: %v", textsBelow(top))
	}
	if h.sd.running || h.sd.run != 0 || !h.sd.addBtn.Disabled() || h.sd.addBtn.Text != "Add Favorites" {
		t.Error("the dialog scans or offers to add before anything ran")
	}
}

func TestScanDialogRefusesTooLargeAndInvalidInput(t *testing.T) {
	h := newScanHarness(t, &scan.Scanner{})
	h.open()
	for in, want := range map[string]string{
		"192.0.2.0/16": "Scan up to 4,096 addresses at a time, for example 192.0.2.0/20.",
		"10.0.0.0/8":   "Scan up to 4,096 addresses at a time, for example 192.0.2.0/20.",
		"lab":          "Enter a network such as 192.0.2.0/24 or a range such as 192.0.2.10-192.0.2.80.",
	} {
		h.sd.entry.SetText(in)
		test.Tap(h.sd.goBtn)
		if h.sd.running || h.sd.run != 0 || !hasText(h.s.win.Canvas().Overlays().Top(), want) {
			t.Errorf("%q: running %v, texts %v", in, h.sd.running, textsBelow(h.s.win.Canvas().Overlays().Top()))
		}
	}
	h.sd.entry.SetText("192.0.2.0/20") // the limit itself is fine
	if h.sd.inputErr != "" {
		t.Error("the error stays after the input changed")
	}
}

func TestScanDialogListsDevicesAndAddsFavorites(t *testing.T) {
	h := newScanHarness(t, fakeDevices(t))
	addFavorites(t, h.startHarness, favorites.Favorite{Name: "Existing", Host: "127.0.0.3", Username: "admin"})
	h.open()
	h.scan(t, "127.0.0.1-127.0.0.4")

	// 127.0.0.3 has a web port of another product and no ping answer: not a device.
	if len(h.sd.rows) != 2 {
		t.Fatalf("%d devices found, want 2", len(h.sd.rows))
	}
	first, second := h.sd.rows[0], h.sd.rows[1]
	if first.res.Addr.String() != "127.0.0.1" || first.res.Version != "16.1" || first.res.Name != "lab.example.com" || first.res.TLS || !first.res.HTTP {
		t.Errorf("first device %+v", first.res)
	}
	if second.res.Addr.String() != "127.0.0.2" || !second.res.PortsClosed() {
		t.Errorf("second device %+v", second.res)
	}
	if !first.ticked || second.ticked {
		t.Errorf("ticked: first %v second %v; a device with open ports is ticked, one with closed ports is not", first.ticked, second.ticked)
	}
	top := h.s.win.Canvas().Overlays().Top()
	for _, want := range []string{"127.0.0.1", "16.1", "No TLS", "Ports closed", "Not activated?", "unknown", "4 addresses scanned in 0 s · 2 found"} {
		if !hasText(top, want) {
			t.Errorf("result lacks %q: %v", want, textsBelow(top))
		}
	}
	if h.sd.addBtn.Text != "Add 1 Favorite" || h.sd.addBtn.Disabled() {
		t.Errorf("button %q disabled %v", h.sd.addBtn.Text, h.sd.addBtn.Disabled())
	}

	// Ticking the grey row adds it too.
	h.sd.rows[1].ticked = true
	h.sd.render()
	if h.sd.addBtn.Text != "Add 2 Favorites" {
		t.Errorf("button %q", h.sd.addBtn.Text)
	}
	test.Tap(h.sd.addBtn)

	list, _ := h.s.mgr.List()
	if len(list) != 3 {
		t.Fatalf("favorites: %+v", list)
	}
	lab, plain := list[1], list[2]
	if lab.Name != "lab.example.com" || lab.Host != "127.0.0.1" || lab.Username != "admin" || lab.UseTLS {
		t.Errorf("added favorite %+v", lab)
	}
	if plain.Name != "127.0.0.2" || plain.Host != "127.0.0.2" {
		t.Errorf("a device without a DNS name is named by its address: %+v", plain)
	}
	if _, err := h.keys.Get("127.0.0.1", "admin"); err == nil {
		t.Error("an added favorite has a password")
	}
	if h.s.selKey != deviceKey("127.0.0.1", "admin") {
		t.Errorf("selected %q, want the first new favorite", h.s.selKey)
	}
	if h.s.win.Canvas().Overlays().Top() != nil {
		t.Error("the dialog stays open after Add")
	}
}

func TestScanDialogMarksKnownHostsAndShowsTheEmptyState(t *testing.T) {
	h := newScanHarness(t, fakeDevices(t))
	addFavorites(t, h.startHarness, favorites.Favorite{Name: "Lab", Host: "lab.example.com", Username: "admin"})
	h.open()
	h.scan(t, "127.0.0.1-127.0.0.2")
	first := h.sd.rows[0]
	if !first.known || first.ticked {
		t.Errorf("a device that is a favorite is known=%v ticked=%v", first.known, first.ticked)
	}
	if !hasText(h.s.win.Canvas().Overlays().Top(), "Already a favorite") {
		t.Error("note missing")
	}
	h.sd.rows[1].ticked = false
	h.sd.render()
	if !h.sd.addBtn.Disabled() {
		t.Error("Add is on with nothing ticked")
	}

	h.scan(t, "127.0.0.10-127.0.0.12")
	top := h.s.win.Canvas().Overlays().Top()
	if len(h.sd.rows) != 0 || !hasText(top, "No Intel AMT devices found in 127.0.0.10-127.0.0.12") ||
		!hasText(top, "A firewall blocks ports 623, 16992 or 16993.") {
		t.Errorf("empty state: %v", textsBelow(top))
	}
}

func TestSidebarOffersTheScan(t *testing.T) {
	h := newScanHarness(t, &scan.Scanner{})
	s := h.s
	if !hasText(s.list.Objects[0], "No favorites yet") {
		t.Fatal("no empty list")
	}
	hasBtn := false
	walk(s.list, func(o fyne.CanvasObject) {
		if b, ok := o.(*widgets.PrimaryButton); ok && b.Text == "Scan Network…" {
			hasBtn = true
			test.Tap(b)
		}
	})
	if !hasBtn {
		t.Fatal("the empty list has no button Scan Network…")
	}
	if !hasText(s.win.Canvas().Overlays().Top(), "Scan Network") {
		t.Fatal("the button does not open the dialog")
	}
	s.win.Canvas().Overlays().Top().Hide()
	s.win.Canvas().Overlays().Remove(s.win.Canvas().Overlays().Top())
	test.Tap(s.scanBtn)
	if !hasText(s.win.Canvas().Overlays().Top(), "Scan Network") {
		t.Fatal("the icon of the sidebar footer does not open the dialog")
	}
}

// Closing the dialog ends a running scan.
func TestScanDialogCloseStopsTheScan(t *testing.T) {
	h := newScanHarness(t, &scan.Scanner{Timeout: 300 * time.Millisecond, Workers: 1, RMCPPort: freeUDPPort(t), HTTPPort: freeTCPPort(t), HTTPSPort: freeTCPPort(t)})
	h.open()
	h.sd.entry.SetText("127.0.2.1-127.0.2.200")
	test.Tap(h.sd.goBtn)
	h.sd.d.Close()
	select {
	case <-h.finished:
	case <-time.After(15 * time.Second):
		t.Fatal("the scan kept running after the dialog closed")
	}
	if strings.Contains(h.sd.status(), "Scanning") {
		t.Errorf("status %q", h.sd.status())
	}
}
