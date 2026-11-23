package mcpserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/session"
)

// solidUpdate is one FramebufferUpdate that paints the whole w x h screen in one RGB565 color.
func solidUpdate(w, h int, rgb565 uint16) []byte {
	px := []byte{byte(rgb565), byte(rgb565 >> 8)} // little-endian, the fake's pixel format
	var rects []kvmtest.Rect
	for y := 0; y < h; y += 64 {
		for x := 0; x < w; x += 64 {
			tw, th := min(64, w-x), min(64, h-y)
			rects = append(rects, kvmtest.RawRect(x, y, tw, th, bytes.Repeat(px, tw*th)))
		}
	}
	return kvmtest.FramebufferUpdate(rects...)
}

const red565 = 0xf800

// addKVM gives the fake device a fake redirection server.
func addKVM(t *testing.T, f *session.Fake, o kvmtest.Options) *kvmtest.Server {
	t.Helper()
	o.Username, o.Password = "admin", "kvm-pw"
	srv := kvmtest.Start(t, o)
	f.KVMAddr, f.KVMUser, f.KVMPassword = srv.Addr, "admin", "kvm-pw"
	return srv
}

// eventually polls cond for up to 3 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type inputEvent struct {
	kind byte // 4 key, 5 pointer
	down bool
	sym  uint32
	mask byte
	x, y int
}

// inputEvents returns the key and pointer events the fake received, without the wake key F15
// that kvm_connect sends.
func inputEvents(srv *kvmtest.Server) []inputEvent {
	var out []inputEvent
	for _, m := range srv.ClientMessages() {
		switch m[0] {
		case 4:
			sym := binary.BigEndian.Uint32(m[4:])
			if sym == 0xffcc {
				continue
			}
			out = append(out, inputEvent{kind: 4, down: m[1] == 1, sym: sym})
		case 5:
			out = append(out, inputEvent{kind: 5, mask: m[1], x: int(binary.BigEndian.Uint16(m[2:])), y: int(binary.BigEndian.Uint16(m[4:]))})
		}
	}
	return out
}

func keyEvents(srv *kvmtest.Server) []string {
	var out []string
	for _, e := range inputEvents(srv) {
		if e.kind != 4 {
			continue
		}
		dir := "up"
		if e.down {
			dir = "down"
		}
		out = append(out, fmt.Sprintf("%s:%x", dir, e.sym))
	}
	return out
}

func pointerEvents(srv *kvmtest.Server) []inputEvent {
	var out []inputEvent
	for _, e := range inputEvents(srv) {
		if e.kind == 5 {
			out = append(out, e)
		}
	}
	return out
}

// screenshot runs kvm_screenshot and decodes its image.
func (h *harness) screenshot(args map[string]any) (image.Image, string, map[string]any) {
	h.t.Helper()
	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "kvm_screenshot", Arguments: args})
	if err != nil {
		h.t.Fatal(err)
	}
	if res.IsError {
		h.t.Fatalf("kvm_screenshot: %v", res.Content)
	}
	var img *mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			img = ic
		}
	}
	if img == nil {
		h.t.Fatalf("no image content in %v", res.Content)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	var decoded image.Image
	switch img.MIMEType {
	case "image/png":
		decoded, err = png.Decode(bytes.NewReader(img.Data))
	case "image/jpeg":
		decoded, err = jpeg.Decode(bytes.NewReader(img.Data))
	default:
		h.t.Fatalf("mime %q", img.MIMEType)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return decoded, img.MIMEType, out
}

func (h *harness) kvmEntry(name string) *kvmEntry {
	h.srv.s.kvm.mu.Lock()
	defer h.srv.s.kvm.mu.Unlock()
	return h.srv.s.kvm.entries[name]
}

// TestKVMConnectKeepsOneSessionAcrossCalls: one kvm_connect, several tool calls, one
// redirection session and one WS-Man login.
func TestKVMConnectKeepsOneSessionAcrossCalls(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{Width: 128, Height: 64, Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.start()
	out := h.mustOK("kvm_connect", dev("NUC"))
	if out["already_open"] != false || out["screen_width"] != float64(128) || out["screen_height"] != float64(64) || out["quality"] != "high" {
		t.Fatalf("%v", out)
	}
	if out["idle_timeout_seconds"] != float64(300) {
		t.Fatalf("idle timeout %v", out["idle_timeout_seconds"])
	}
	out = h.mustOK("kvm_connect", dev("NUC"))
	if out["already_open"] != true {
		t.Fatalf("second connect %v", out)
	}
	h.screenshot(dev("NUC"))
	h.mustOK("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}))
	h.screenshot(dev("NUC"))
	if n := srv.Connections(); n != 1 {
		t.Fatalf("redirection sessions %d", n)
	}
	if n := len(h.connectCalls()); n != 1 {
		t.Fatalf("WS-Man logins %d", n)
	}
	if w, _ := out["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "not encrypted") {
		t.Fatalf("warnings %v", out["warnings"])
	}
}

// TestKVMToolsNeedConnect: without kvm_connect nothing reaches the device.
func TestKVMToolsNeedConnect(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{})
	h.start()
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}), "call kvm_connect first")
	h.mustFail("kvm_mouse", dev("NUC", "action", "click", "x", 1, "y", 1), "call kvm_connect first")
	h.mustFail("kvm_type_text", dev("NUC", "text", "hi"), "call kvm_connect first")
	h.mustFail("kvm_connect", dev("Ghost"), "No favorite named")
	if srv.Connections() != 0 || len(h.connectCalls()) != 0 {
		t.Fatalf("contacted the device: %d %d", srv.Connections(), len(h.connectCalls()))
	}
}

// TestKVMConsentFlow: CCM consent with the consent_code parameter of cut 1.
func TestKVMConsentFlow(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Consent, f.ConsentCode = true, "123456"
	f.St.ControlMode = amt.ControlModeClient
	srv := addKVM(t, f, kvmtest.Options{})
	h.start()
	h.mustFail("kvm_connect", dev("NUC"), "call kvm_connect again with consent_code")
	h.mustFail("kvm_connect", dev("NUC", "consent_code", "654321"), "did not accept the consent code")
	h.mustFail("kvm_connect", dev("NUC", "consent_code", "12a456"), "six digits")
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	if srv.Connections() != 0 {
		t.Fatal("dialled without consent")
	}
	h.mustOK("kvm_connect", dev("NUC", "consent_code", "123456"))
	if srv.Connections() != 1 {
		t.Fatalf("connections %d", srv.Connections())
	}
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "123456") || strings.Contains(string(b), "654321") {
			t.Fatalf("action log holds a consent code: %s", b)
		}
	}
}

// TestKVMRefusedWhenAnotherClientHoldsSession: AMT refuses the redirection start while the app
// or another client has KVM open; the agent learns why and what to do.
func TestKVMRefusedWhenAnotherClientHoldsSession(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{StartStatus: 1})
	h.start()
	h.mustFail("kvm_connect", dev("NUC"), "Another KVM session to this device is open")
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	f.KVMErr = &amt.KVMError{Step: "KVMRedirectionSAP.RequestStateChange", ReturnValue: 2}
	h.mustFail("kvm_connect", dev("NUC"), "KVM may be disabled")
}

// TestKVMScreenshotScalesForVisionModels: longest side 1280 and JPEG by default, never
// upscaled, aspect kept; size and format per call or from the config.
func TestKVMScreenshotScalesForVisionModels(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{Width: 640, Height: 360, Updates: [][]byte{solidUpdate(640, 360, red565)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	img, mime, out := h.screenshot(dev("NUC"))
	if mime != "image/jpeg" || img.Bounds().Dx() != 640 || img.Bounds().Dy() != 360 {
		t.Fatalf("default: %s %v", mime, img.Bounds())
	}
	if out["width"] != float64(640) || out["screen_width"] != float64(640) || out["format"] != "jpeg" || out["frame"].(float64) < 1 {
		t.Fatalf("%v", out)
	}
	img, mime, out = h.screenshot(dev("NUC", "max_width", 320, "format", "png"))
	if mime != "image/png" || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 || out["height"] != float64(180) || out["screen_height"] != float64(360) {
		t.Fatalf("png 320: %s %v %v", mime, img.Bounds(), out)
	}
	r, g, b, _ := img.At(100, 100).RGBA()
	if r>>8 < 0xf0 || g>>8 > 0x10 || b>>8 > 0x10 {
		t.Fatalf("pixel %v", img.At(100, 100))
	}
	h.mustFail("kvm_screenshot", dev("NUC", "format", "gif"), "format")
	h.mustFail("kvm_screenshot", dev("NUC", "jpeg_quality", 101), "jpeg_quality")
	h.mustFail("kvm_screenshot", dev("NUC", "max_height", 5), "max_height")

	h2 := newHarness(t)
	f2 := h2.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f2, kvmtest.Options{Width: 640, Height: 360, Updates: [][]byte{solidUpdate(640, 360, red565)}})
	h2.cfg.Screenshot = ScreenshotConfig{MaxWidth: 160, MaxHeight: 1280, Format: "png", JPEGQuality: 80}
	h2.start()
	h2.mustOK("kvm_connect", dev("NUC"))
	img, mime, _ = h2.screenshot(dev("NUC"))
	if mime != "image/png" || img.Bounds().Dx() != 160 || img.Bounds().Dy() != 90 {
		t.Fatalf("config defaults: %s %v", mime, img.Bounds())
	}
}

// TestKVMScreenshotWaitsForNewFrame: wait_ms waits for a newer picture and, if none comes,
// returns the latest one after wait_ms without an error.
func TestKVMScreenshotWaitsForNewFrame(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{Width: 64, Height: 64, Updates: [][]byte{solidUpdate(64, 64, red565), solidUpdate(64, 64, 0x001f)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	eventually(t, "second frame", func() bool { return h.kvmEntry("NUC").client.Frame() >= 2 })
	start := time.Now()
	img, _, out := h.screenshot(dev("NUC", "format", "png", "wait_ms", 150))
	if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
		t.Fatalf("waited %v", d)
	}
	if out["frame"] != float64(2) {
		t.Fatalf("frame %v", out["frame"])
	}
	if _, _, b, _ := img.At(10, 10).RGBA(); b>>8 < 0xf0 {
		t.Fatalf("pixel %v", img.At(10, 10))
	}
	h.mustFail("kvm_screenshot", dev("NUC", "wait_ms", 10001), "wait_ms")
}

// TestKVMMouseMapsScreenshotCoordinates: x/y in the last screenshot's pixels reach the screen
// at the matching point; before a screenshot they are screen pixels.
func TestKVMMouseMapsScreenshotCoordinates(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{Width: 200, Height: 100, Updates: [][]byte{solidUpdate(200, 100, red565)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	out := h.mustOK("kvm_mouse", dev("NUC", "action", "click", "x", 10, "y", 20, "button", "right"))
	if out["screen_x"] != float64(10) || out["screen_y"] != float64(20) {
		t.Fatalf("before a screenshot: %v", out)
	}
	h.screenshot(dev("NUC", "max_width", 100))
	out = h.mustOK("kvm_mouse", dev("NUC", "action", "click", "x", 50, "y", 25))
	if out["screen_x"] != float64(101) || out["screen_y"] != float64(51) {
		t.Fatalf("mapped: %v", out)
	}
	h.mustOK("kvm_mouse", dev("NUC", "action", "scroll", "x", 50, "y", 25, "amount", 2))
	h.mustOK("kvm_mouse", dev("NUC", "action", "scroll", "x", 50, "y", 25, "amount", -1))
	h.mustOK("kvm_mouse", dev("NUC", "action", "move", "x", 500, "y", 500))
	type pe struct {
		mask byte
		x, y int
	}
	want := []pe{
		{0, 10, 20}, {4, 10, 20}, {0, 10, 20}, // right click
		{0, 101, 51}, {1, 101, 51}, {0, 101, 51}, // left click
		{16, 101, 51}, {0, 101, 51}, {16, 101, 51}, {0, 101, 51}, // scroll down 2
		{8, 101, 51}, {0, 101, 51}, // scroll up 1
		{0, 199, 99}, // move, clamped
	}
	eventually(t, "pointer events", func() bool { return len(pointerEvents(srv)) >= len(want) })
	var got []pe
	for _, e := range pointerEvents(srv) {
		got = append(got, pe{e.mask, e.x, e.y})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pointer events\n got %v\nwant %v", got, want)
	}
	h.mustFail("kvm_mouse", dev("NUC", "action", "tap", "x", 1, "y", 1), "action")
	h.mustFail("kvm_mouse", dev("NUC", "action", "click", "x", -1, "y", 1), "x and y")
	h.mustFail("kvm_mouse", dev("NUC", "action", "scroll", "x", 1, "y", 1), "amount")
}

// TestKVMMouseDragKeepsButtonHeld: down, move, up form a drag.
func TestKVMMouseDragKeepsButtonHeld(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{Width: 200, Height: 100})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.mustOK("kvm_mouse", dev("NUC", "action", "down", "x", 10, "y", 10))
	h.mustOK("kvm_mouse", dev("NUC", "action", "move", "x", 50, "y", 10))
	h.mustOK("kvm_mouse", dev("NUC", "action", "up", "x", 60, "y", 10))
	eventually(t, "pointer events", func() bool { return len(pointerEvents(srv)) >= 4 })
	var masks []byte
	for _, e := range pointerEvents(srv) {
		masks = append(masks, e.mask)
	}
	if !bytes.Equal(masks, []byte{0, 1, 1, 0}) {
		t.Fatalf("masks %v", masks)
	}
}

// TestKVMSendKeysCombos: combos press in order and release in reverse; an unknown key sends
// nothing.
func TestKVMSendKeysCombos(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter", "Ctrl+Bogus"}), "unknown key")
	h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{}), "keys")
	out := h.mustOK("kvm_send_keys", dev("NUC", "keys", []string{"Ctrl+Alt+Delete", "Enter", "shift+a"}))
	if out["sent"] != float64(3) {
		t.Fatalf("%v", out)
	}
	want := []string{"down:ffe3", "down:ffe9", "down:ffff", "up:ffff", "up:ffe9", "up:ffe3",
		"down:ff0d", "up:ff0d", "down:ffe1", "down:41", "up:41", "up:ffe1"}
	eventually(t, "key events", func() bool { return len(keyEvents(srv)) >= len(want) })
	if got := keyEvents(srv); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys\n got %v\nwant %v", got, want)
	}
	var logged string
	for _, e := range h.logEntries() {
		if e["tool"] == "kvm_send_keys" && e["outcome"] == "ok" {
			logged, _ = e["action"].(string)
		}
	}
	if logged != "Ctrl+Alt+Delete, Enter, shift+a" {
		t.Fatalf("logged %q", logged)
	}
}

// TestKVMTypeTextUsesLayout: the favorite's layout is the default, the parameter wins;
// characters the layout lacks are skipped and reported; the text is never logged.
func TestKVMTypeTextUsesLayout(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	h.favs[0].Layout = "de"
	srv := addKVM(t, f, kvmtest.Options{})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	out := h.mustOK("kvm_type_text", dev("NUC", "text", "zy<"))
	if out["layout"] != "de" || out["typed"] != float64(2) {
		t.Fatalf("%v", out)
	}
	sk, _ := out["skipped"].([]any)
	if len(sk) != 1 || sk[0].(map[string]any)["char"] != "<" || sk[0].(map[string]any)["column"] != float64(3) {
		t.Fatalf("skipped %v", out["skipped"])
	}
	want := []string{"down:79", "up:79", "down:7a", "up:7a"} // German QWERTZ: z on the US y key
	eventually(t, "key events", func() bool { return len(keyEvents(srv)) >= len(want) })
	if got := keyEvents(srv); !reflect.DeepEqual(got, want) {
		t.Fatalf("de keys %v", got)
	}
	out = h.mustOK("kvm_type_text", dev("NUC", "text", "z", "layout", "us"))
	if out["layout"] != "us" {
		t.Fatalf("%v", out)
	}
	want = append(want, "down:7a", "up:7a")
	eventually(t, "key events", func() bool { return len(keyEvents(srv)) >= len(want) })
	h.mustFail("kvm_type_text", dev("NUC", "text", "x", "layout", "klingon"), "layout")
	h.mustFail("kvm_type_text", dev("NUC", "text", strings.Repeat("a", 2001)), "2000")
	h.mustFail("kvm_type_text", dev("NUC", "text", "<<"), "no character")
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "zy<") {
			t.Fatalf("action log holds the typed text: %s", b)
		}
	}
}

// TestKVMIdleTimeoutClosesSession: a session without calls is closed and needs kvm_connect.
func TestKVMIdleTimeoutClosesSession(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{})
	h.cfg.KVMIdleTimeout = 100 * time.Millisecond
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	c := h.kvmEntry("NUC").client
	eventually(t, "idle close", func() bool { return h.kvmEntry("NUC") == nil })
	select {
	case <-c.Done():
	default:
		t.Fatal("client still running")
	}
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")
	h.mustOK("kvm_connect", dev("NUC"))
	if srv.Connections() != 2 {
		t.Fatalf("connections %d", srv.Connections())
	}
}

// TestKVMReopensDroppedSession: a session AMT ended is opened again by the next call.
func TestKVMReopensDroppedSession(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{CloseAfter: 50 * time.Millisecond, CloseConns: 1, UpdatesPerConn: true,
		Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	c := h.kvmEntry("NUC").client
	eventually(t, "drop", func() bool {
		select {
		case <-c.Done():
			return true
		default:
			return false
		}
	})
	h.screenshot(dev("NUC"))
	if srv.Connections() != 2 {
		t.Fatalf("connections %d", srv.Connections())
	}
}

// TestKVMReopenNeedingConsentPointsToConnect: a reopen that needs consent again asks for
// kvm_connect with the code.
func TestKVMReopenNeedingConsentPointsToConnect(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{CloseAfter: 50 * time.Millisecond, CloseConns: 1, UpdatesPerConn: true,
		Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	c := h.kvmEntry("NUC").client
	<-c.Done()
	f.Consent, f.ConsentCode = true, "123456"
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect again with consent_code")
	h.mustOK("kvm_connect", dev("NUC", "consent_code", "123456"))
	h.screenshot(dev("NUC"))
}

// TestPowerOffClosesKVMFirst: AMT refuses off, cycle and shutdown while KVM is open, so the
// server closes it before the command; the next KVM call opens it again. Reset keeps it.
func TestPowerOffClosesKVMFirst(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{UpdatesPerConn: true, Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	c := h.kvmEntry("NUC").client
	gate := make(chan struct{})
	f.PowerGate = gate
	done := make(chan string, 1)
	go func() {
		text, _, _ := h.call("power_action", dev("NUC", "action", "cycle"))
		done <- text
	}()
	eventually(t, "power command", f.PowerStarted)
	select {
	case <-c.Done():
	default:
		t.Fatal("power cycle sent while KVM was open")
	}
	close(gate)
	if text := <-done; !strings.Contains(text, "KVM session") {
		t.Fatalf("result %s", text)
	}
	f.PowerGate = nil
	h.screenshot(dev("NUC"))
	if srv.Connections() != 2 {
		t.Fatalf("connections %d", srv.Connections())
	}
	// Reset keeps the session, and a refused power rule closes nothing.
	h.mustOK("power_action", dev("NUC", "action", "reset"))
	h.mustFail("power_action", dev("NUC", "action", "on"), "already on")
	if c := h.kvmEntry("NUC").client; c == nil || c.Err() != nil || srv.Connections() != 2 {
		t.Fatal("reset or a refused action closed KVM")
	}
}

// TestKVMInputConfirmation: every input call asks the person in the MCP client; connect and
// screenshot ask nothing.
func TestKVMInputConfirmation(t *testing.T) {
	var asked []string
	answer := &mcp.ElicitResult{Action: "decline"}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{Updates: [][]byte{solidUpdate(128, 64, red565)}})
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return answer, nil
	}
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.screenshot(dev("NUC"))
	if len(asked) != 0 {
		t.Fatalf("asked %v", asked)
	}
	h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}), "not confirmed")
	h.mustFail("kvm_type_text", dev("NUC", "text", "Hunter2-pass", "secret", true), "not confirmed")
	h.mustFail("kvm_mouse", dev("NUC", "action", "click", "x", 1, "y", 1), "not confirmed")
	if len(keyEvents(srv)) != 0 || len(pointerEvents(srv)) != 0 {
		t.Fatal("input sent without confirmation")
	}
	if len(asked) != 3 || !strings.Contains(asked[0], "Enter") || strings.Contains(asked[1], "Hunter2") ||
		!strings.Contains(asked[1], "type 12 characters") || !strings.Contains(asked[1], "The AI agent chose to hide this text") {
		t.Fatalf("asked %q", asked)
	}
	answer = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	h.mustOK("kvm_send_keys", dev("NUC", "keys", []string{"Enter"}))
	eventually(t, "key events", func() bool { return len(keyEvents(srv)) == 2 })
}

// TestKVMDisconnectAndServerClose: kvm_disconnect and Server.Close release held buttons and end
// the sessions.
func TestKVMDisconnectAndServerClose(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	g := h.addDevice("Lab", "lab.example.com", false)
	srvNUC := addKVM(t, f, kvmtest.Options{})
	addKVM(t, g, kvmtest.Options{})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.mustOK("kvm_mouse", dev("NUC", "action", "down", "x", 5, "y", 5))
	c := h.kvmEntry("NUC").client
	out := h.mustOK("kvm_disconnect", dev("NUC"))
	if out["closed"] != true {
		t.Fatalf("%v", out)
	}
	<-c.Done()
	eventually(t, "button release", func() bool { return len(pointerEvents(srvNUC)) >= 2 })
	if p := pointerEvents(srvNUC); p[len(p)-1].mask != 0 {
		t.Fatalf("button still held: %v", p)
	}
	if out := h.mustOK("kvm_disconnect", dev("NUC")); out["closed"] != false {
		t.Fatalf("second disconnect %v", out)
	}
	h.mustFail("kvm_screenshot", dev("NUC"), "call kvm_connect first")

	h.mustOK("kvm_connect", dev("NUC"))
	h.mustOK("kvm_connect", dev("Lab"))
	c1, c2 := h.kvmEntry("NUC").client, h.kvmEntry("Lab").client
	h.srv.Close()
	for _, c := range []interface{ Done() <-chan struct{} }{c1, c2} {
		select {
		case <-c.Done():
		default:
			t.Fatal("session still open after Close")
		}
	}
	h.mustFail("kvm_connect", dev("NUC"), "shutting down")
}

// TestKVMScreenshotOfBlackScreenWarns: no picture yet gives the black framebuffer and a warning.
func TestKVMScreenshotOfBlackScreenWarns(t *testing.T) {
	old := firstFrameWait
	firstFrameWait = 50 * time.Millisecond
	t.Cleanup(func() { firstFrameWait = old })
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{})
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	img, _, out := h.screenshot(dev("NUC", "format", "png"))
	if r, g, b, _ := img.At(1, 1).RGBA(); r|g|b != 0 {
		t.Fatalf("pixel %v", img.At(1, 1))
	}
	w, _ := out["warnings"].([]any)
	found := false
	for _, s := range w {
		found = found || strings.Contains(s.(string), "no picture")
	}
	if !found {
		t.Fatalf("warnings %v", w)
	}
}

// TestKVMTypeTextConfirmationShowsText: the person confirming sees what will be typed, with
// line breaks made visible and counted (each is an Enter press); a long text is cut in the
// prompt only. The action log never holds the text.
func TestKVMTypeTextConfirmationShowsText(t *testing.T) {
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{})
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.mustFail("kvm_type_text", dev("NUC", "text", "cd /tmp\nrm -rf build\n", "layout", "us"), "not confirmed")
	if len(asked) != 1 || !strings.Contains(asked[0], "«cd /tmp⏎rm -rf build»") || !strings.Contains(asked[0], "1 Enter press ") {
		t.Fatalf("asked %q", asked)
	}
	long := strings.Repeat("x", 600)
	h.mustFail("kvm_type_text", dev("NUC", "text", long, "layout", "us"), "not confirmed")
	if !strings.Contains(asked[1], strings.Repeat("x", maxPreview)+"… (100 more characters)") {
		t.Fatalf("asked %q", asked[1])
	}
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "rm -rf") || strings.Contains(string(b), "xxxx") {
			t.Fatalf("action log holds the typed text: %s", b)
		}
	}
	var actions []string
	for _, e := range h.logEntries() {
		if e["tool"] == "kvm_type_text" {
			actions = append(actions, e["action"].(string))
		}
	}
	if len(actions) != 4 || actions[0] != "20 characters, 1 Enter, layout us" {
		t.Fatalf("logged %q", actions)
	}
}

// TestKVMSendKeysConfirmationShowsEveryCombo: the prompt and the log hold the whole list; more
// than 50 combos are refused.
func TestKVMSendKeysConfirmationShowsEveryCombo(t *testing.T) {
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{})
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	keys := make([]string, 0, 50)
	for range 49 {
		keys = append(keys, "ControlRight + NumpadMultiply")
	}
	keys = append(keys, "Ctrl+Alt+Delete")
	h.mustFail("kvm_send_keys", dev("NUC", "keys", keys), "not confirmed")
	if len(asked) != 1 || !strings.HasSuffix(asked[0], "ControlRight+NumpadMultiply, Ctrl+Alt+Delete on device \"NUC\" (192.0.2.10). Allow it?") {
		t.Fatalf("asked %q", asked)
	}
	logged := h.logEntries()[1]["action"].(string)
	if !strings.HasSuffix(logged, ", Ctrl+Alt+Delete") || strings.Count(logged, "NumpadMultiply") != 49 {
		t.Fatalf("logged %q", logged)
	}
	h.mustFail("kvm_send_keys", dev("NUC", "keys", append(keys, "Enter")), "1 to 50")
	if len(asked) != 1 {
		t.Fatalf("asked %d times", len(asked))
	}
}

// TestKVMTypeTextSecretRefusesControlCharacters: the agent may hide only a single line; a hidden
// Enter press (or tab, escape, ...) could run a command the person never saw.
func TestKVMTypeTextSecretRefusesControlCharacters(t *testing.T) {
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addKVM(t, f, kvmtest.Options{})
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	for _, text := range []string{"pass\nrm -rf /", "pass\r", "pass\n", "a\tb", "a\x1bb", "a\u0085b"} {
		h.mustFail("kvm_type_text", dev("NUC", "text", text, "secret", true, "layout", "us"), "secret is only allowed for a single line")
	}
	if len(asked) != 0 || len(keyEvents(srv)) != 0 {
		t.Fatalf("asked %q, keys %v", asked, keyEvents(srv))
	}
	out := h.mustOK("kvm_type_text", dev("NUC", "text", "Hunter2", "secret", true, "layout", "us"))
	if out["typed"] != float64(7) || len(asked) != 1 || strings.Contains(asked[0], "Hunter2") ||
		!strings.Contains(asked[0], "type 7 characters") || !strings.Contains(asked[0], "The AI agent chose to hide this text") {
		t.Fatalf("out %v asked %q", out, asked)
	}
	var logged string
	for _, e := range h.logEntries() {
		if e["tool"] == "kvm_type_text" && e["outcome"] == "ok" {
			logged, _ = e["action"].(string)
		}
	}
	if logged != "7 characters, 0 Enter, layout us, secret" {
		t.Fatalf("logged %q", logged)
	}
}
