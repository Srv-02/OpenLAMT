package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/typing"
)

// Waits of the KVM tools; variables for tests.
var (
	// firstFrameWait bounds the wait for the first picture of a new session.
	firstFrameWait = 5 * time.Second
	// wakeRepeat is the pause between the two wake keys after kvm_connect (as in the app).
	wakeRepeat = 500 * time.Millisecond
)

const (
	maxWaitMS      = 10000
	maxKeyCombos   = 50
	maxScrollClick = 50
)

// openKVM opens a KVM session to device: login check, the favorite's quality (high unless the
// favorite says low), consent with code. The WS-Man session is closed again; the KVM client
// has its own socket.
func (s *server) openKVM(ctx context.Context, device string, code consentCode) (*kvm.Client, []string, error) {
	sess, _, fav, warnings, err := s.openDevice(ctx, device)
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()
	q := kvm.QualityHigh
	if fav.QualityChoice() == favorites.QualityLow {
		q = kvm.QualityLow
	}
	c, err := sess.OpenKVM(ctx, q, code)
	if err != nil {
		return nil, nil, err
	}
	return c, warnings, nil
}

// wakeScreen wakes a sleeping host screen after a connect the agent asked for, like the app:
// F15 at the first picture and once more wakeRepeat later. Reopens never do it, so a host that
// AMT keeps dropping for a black screen can still go to sleep and lock.
func wakeScreen(c *kvm.Client) {
	if _, err := c.WaitFrame(context.Background(), 0); err != nil {
		return
	}
	_ = c.WakeScreen()
	t := time.NewTimer(wakeRepeat)
	defer t.Stop()
	select {
	case <-t.C:
		_ = c.WakeScreen()
	case <-c.Done():
	}
}

// schemaFor infers the input schema of T and adds enums to its properties.
func schemaFor[T any](enums map[string][]any) *jsonschema.Schema {
	sc, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	for name, values := range enums {
		sc.Properties[name].Enum = values
	}
	return sc
}

func (s *server) registerKVM(srv *mcp.Server) {
	view, input := s.cfg.Enabled(GroupRead), s.cfg.Enabled(GroupKVMInput)
	if !view && !input {
		return
	}
	closed := boolPtr(false)
	addTool(s, srv, &mcp.Tool{Name: "kvm_connect", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: closed},
		Description: "Open the KVM (remote screen) session of a device and keep it for the other kvm_ tools. " +
			"AMT allows one KVM session per device: while this server holds it, the OpenLAMT app and other clients cannot connect, and the other way round. " +
			"Calling it again returns the open session. Devices that require user consent show a six-digit code on their screen: " +
			"call without consent_code first, ask a person at the device for the code, then call again with it. " +
			"The session closes after the idle timeout without calls, with kvm_disconnect, or when this server stops."},
		s.kvmConnect)
	addTool(s, srv, &mcp.Tool{Name: "kvm_disconnect", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: closed},
		Description: "Close the KVM session of a device (held keys and buttons are released first), so the OpenLAMT app can use KVM again."},
		s.kvmDisconnect)
	if view {
		addTool(s, srv, &mcp.Tool{Name: "kvm_screenshot", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed},
			InputSchema: schemaFor[screenshotInput](map[string][]any{"format": {"jpeg", "png"}}),
			Description: "Return the current screen of a device as an image sized for vision models (by default the longest side at most 1280 pixels, JPEG). " +
				"Needs kvm_connect first. kvm_mouse takes x and y in the pixels of the last screenshot. " +
				"Use wait_ms after input to get a picture that shows its effect."},
			s.kvmScreenshot)
	}
	if !input {
		return
	}
	act := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed}
	addTool(s, srv, &mcp.Tool{Name: "kvm_send_keys", Annotations: act,
		Description: "Press keys and key combinations on a device, one after the other, for example [\"Ctrl+Alt+Delete\"], [\"Alt+F4\"], [\"Enter\"] or [\"Win+r\"]. " +
			"A combination presses its keys in the given order and releases them in reverse. Key names: KeyboardEvent.code names (KeyA, Digit1, F5, ArrowUp, " +
			"Enter, Escape, Tab, Backspace, Delete, Home, PageDown, ControlLeft, AltRight, MetaLeft, ...), the short names Ctrl, Alt, AltGr, Shift, Win, Esc, Del, " +
			"Up, Down, Left, Right, PgUp, PgDn, Menu, or a single letter, digit or punctuation character. Keys are physical key positions of a US keyboard; " +
			"to enter text use kvm_type_text. Needs kvm_connect first."},
		s.kvmSendKeys)
	addTool(s, srv, &mcp.Tool{Name: "kvm_mouse", Annotations: act,
		InputSchema: schemaFor[mouseInput](map[string][]any{
			"action": {"move", "click", "double_click", "down", "up", "scroll"},
			"button": {"left", "right", "middle"},
		}),
		Description: "Move the mouse, click, double-click, press or release a button, or scroll the wheel on a device. " +
			"x and y are pixels of the last kvm_screenshot of this session (before the first screenshot: screen pixels); the result gives the screen point used. " +
			"For a drag use down, move and up. Needs kvm_connect first."},
		s.kvmMouse)
	addTool(s, srv, &mcp.Tool{Name: "kvm_type_text", Annotations: act,
		InputSchema: schemaFor[typeInput](map[string][]any{"layout": layoutEnum()}),
		Description: fmt.Sprintf("Type text on a device as key strokes for the keyboard layout of the device (at most %d characters per call). "+
			"Line breaks become Enter, but trailing line breaks are dropped so no command runs by accident: press Enter with kvm_send_keys. "+
			"Characters the layout cannot produce are skipped and listed. layout defaults to the favorite's layout in the OpenLAMT app, "+
			"else the layout of the system language of this computer. Needs kvm_connect first.", typing.MaxChars)},
		s.kvmTypeText)
}

func layoutEnum() []any {
	var out []any
	for _, l := range typing.Layouts() {
		out = append(out, string(l))
	}
	return out
}

// --- kvm_connect and kvm_disconnect

type kvmConnectInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	ConsentCode string `json:"consent_code,omitempty" jsonschema:"six-digit user consent code the device shows on its screen; leave it out on the first call"`
}

type kvmConnectOutput struct {
	Device             string   `json:"device"`
	AlreadyOpen        bool     `json:"already_open" jsonschema:"the session was open already; nothing new was opened"`
	ScreenWidth        int      `json:"screen_width"`
	ScreenHeight       int      `json:"screen_height"`
	Quality            string   `json:"quality" jsonschema:"high (16 bit colour) or low (8 bit), as chosen for the favorite in the OpenLAMT app"`
	IdleTimeoutSeconds int      `json:"idle_timeout_seconds" jsonschema:"the session closes after this many seconds without a KVM tool call; 0 means never"`
	Warnings           []string `json:"warnings,omitempty"`
}

func (s *server) kvmConnect(ctx context.Context, _ *mcp.CallToolRequest, in kvmConnectInput, rec *record) (*mcp.CallToolResult, kvmConnectOutput, error) {
	rec.device = in.Device
	code, err := parseConsentCode(in.ConsentCode)
	if err != nil {
		return nil, kvmConnectOutput{}, err
	}
	if code != "" {
		rec.action = "with consent code" // never the code itself
	}
	if _, err := s.lookup(in.Device); err != nil {
		return nil, kvmConnectOutput{}, err
	}
	var out kvmConnectOutput
	err = s.kvm.connect(ctx, in.Device, code, func(e *kvmEntry, already bool) error {
		if !already {
			go wakeScreen(e.client)
		}
		w, h := e.client.Size()
		out = kvmConnectOutput{Device: in.Device, AlreadyOpen: already, ScreenWidth: w, ScreenHeight: h,
			Quality: e.client.Quality().String(), IdleTimeoutSeconds: int(s.cfg.KVMIdleTimeout / time.Second), Warnings: e.warnings}
		return nil
	})
	if err != nil {
		return nil, kvmConnectOutput{}, err
	}
	return nil, out, nil
}

type kvmDisconnectOutput struct {
	Device string `json:"device"`
	Closed bool   `json:"closed" jsonschema:"false if no session was open"`
}

func (s *server) kvmDisconnect(_ context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, kvmDisconnectOutput, error) {
	rec.device = in.Device
	if _, err := s.lookup(in.Device); err != nil {
		return nil, kvmDisconnectOutput{}, err
	}
	return nil, kvmDisconnectOutput{Device: in.Device, Closed: s.kvm.disconnect(in.Device)}, nil
}

// --- kvm_screenshot

type screenshotInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	MaxWidth    int    `json:"max_width,omitempty" jsonschema:"the image is at most this wide, aspect kept, never upscaled (default from the server config, 1280)"`
	MaxHeight   int    `json:"max_height,omitempty" jsonschema:"the image is at most this high (default from the server config, 1280)"`
	Format      string `json:"format,omitempty" jsonschema:"jpeg (small, the default) or png (lossless, sharper small text)"`
	JPEGQuality int    `json:"jpeg_quality,omitempty" jsonschema:"JPEG quality 1 to 100 (default from the server config, 80)"`
	WaitMS      int    `json:"wait_ms,omitempty" jsonschema:"wait up to this many milliseconds (at most 10000) for a newer picture than the current one, e.g. right after input"`
}

type screenshotOutput struct {
	Device       string   `json:"device"`
	Width        int      `json:"width" jsonschema:"image width; kvm_mouse x refers to it"`
	Height       int      `json:"height" jsonschema:"image height; kvm_mouse y refers to it"`
	ScreenWidth  int      `json:"screen_width"`
	ScreenHeight int      `json:"screen_height"`
	Format       string   `json:"format"`
	Bytes        int      `json:"bytes"`
	Frame        uint64   `json:"frame" jsonschema:"number of screen updates received in this session; a higher number means a newer picture"`
	Warnings     []string `json:"warnings,omitempty"`
}

// screenshotConfig merges the call's settings into the configured defaults.
func (s *server) screenshotConfig(in screenshotInput) (ScreenshotConfig, error) {
	sc := s.cfg.Screenshot
	if in.MaxWidth != 0 {
		sc.MaxWidth = in.MaxWidth
	}
	if in.MaxHeight != 0 {
		sc.MaxHeight = in.MaxHeight
	}
	if f := strings.ToLower(strings.TrimSpace(in.Format)); f != "" {
		sc.Format = f
	}
	if in.JPEGQuality != 0 {
		sc.JPEGQuality = in.JPEGQuality
	}
	if err := sc.check(""); err != nil {
		return sc, refuse("%s.", err.Error())
	}
	return sc, nil
}

func (s *server) kvmScreenshot(ctx context.Context, _ *mcp.CallToolRequest, in screenshotInput, rec *record) (*mcp.CallToolResult, screenshotOutput, error) {
	rec.device = in.Device
	sc, err := s.screenshotConfig(in)
	if err != nil {
		return nil, screenshotOutput{}, err
	}
	if in.WaitMS < 0 || in.WaitMS > maxWaitMS {
		return nil, screenshotOutput{}, refuse("wait_ms must be between 0 and %d.", maxWaitMS)
	}
	if _, err := s.lookup(in.Device); err != nil {
		return nil, screenshotOutput{}, err
	}
	var (
		out  screenshotOutput
		data []byte
		mime string
	)
	err = s.kvm.use(ctx, in.Device, func(e *kvmEntry) error {
		c := e.client
		out.Warnings = append(out.Warnings, e.warnings...)
		wait := func(d time.Duration, after uint64) error {
			wctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			_, err := c.WaitFrame(wctx, after)
			return err
		}
		ended := false
		if c.Frame() == 0 {
			switch err := wait(firstFrameWait, 0); {
			case errors.Is(err, context.DeadlineExceeded):
				out.Warnings = append(out.Warnings, "The device sent no picture yet, so the image is black. "+
					"The screen may be off or asleep; try again, or press a key with kvm_send_keys.")
			case err != nil && ctx.Err() == nil:
				ended = true
			}
		}
		if in.WaitMS > 0 && !ended {
			if err := wait(time.Duration(in.WaitMS)*time.Millisecond, c.Frame()); err != nil && !errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				ended = true
			}
		}
		if ended {
			out.Warnings = append(out.Warnings, "AMT ended the KVM session while waiting; this is the last picture. The next KVM call opens the session again.")
		}
		var w, h int
		var err error
		if data, w, h, mime, err = encodeScreenshot(c.Snapshot(), sc); err != nil {
			return err
		}
		sw, sh := c.Size()
		e.imgW, e.imgH = w, h
		out = screenshotOutput{Device: in.Device, Width: w, Height: h, ScreenWidth: sw, ScreenHeight: sh, Format: sc.Format,
			Bytes: len(data), Frame: c.Frame(), Warnings: out.Warnings}
		return nil
	})
	if err != nil {
		return nil, screenshotOutput{}, err
	}
	text := fmt.Sprintf("Screen of %s: %dx%d pixels, shown as a %dx%d %s image. kvm_mouse takes x and y in this image's pixels.",
		in.Device, out.ScreenWidth, out.ScreenHeight, out.Width, out.Height, strings.ToUpper(out.Format))
	for _, w := range out.Warnings {
		text += "\nWarning: " + w
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: mime}, &mcp.TextContent{Text: text}}}, out, nil
}

// --- kvm_send_keys

type sendKeysInput struct {
	Device string   `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Keys   []string `json:"keys" jsonschema:"keys or key combinations, sent one after the other, e.g. [\"Ctrl+Alt+Delete\"] or [\"Tab\", \"Tab\", \"Enter\"]"`
}

type sendKeysOutput struct {
	Device string `json:"device"`
	Sent   int    `json:"sent" jsonschema:"number of keys and combinations sent"`
}

// confirmInput asks the person in the MCP client before an input call, when the config wants it.
func (s *server) confirmInput(req *mcp.CallToolRequest, device, what string) (*mcp.CallToolResult, error) {
	fav, err := s.lookup(device)
	if err != nil {
		return nil, err
	}
	if !s.kvm.has(device) {
		return nil, noSession(device) // do not ask a person for a call that cannot run
	}
	return s.confirm(req, confirmMessage(what, fav))
}

// sleepCtx waits d unless ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *server) kvmSendKeys(ctx context.Context, req *mcp.CallToolRequest, in sendKeysInput, rec *record) (*mcp.CallToolResult, sendKeysOutput, error) {
	rec.device = in.Device
	if len(in.Keys) == 0 || len(in.Keys) > maxKeyCombos {
		return nil, sendKeysOutput{}, refuse("keys must hold 1 to %d keys or key combinations.", maxKeyCombos)
	}
	combos := make([][]kvm.Key, 0, len(in.Keys))
	names := make([]string, 0, len(in.Keys))
	for _, k := range in.Keys {
		keys, err := parseCombo(k)
		if err != nil {
			return nil, sendKeysOutput{}, refuse("%s. Nothing was sent.", err.Error())
		}
		combos = append(combos, keys)
		names = append(names, comboText(k))
	}
	// The whole list, never shortened: the person confirming and the log see every combo. It is
	// bounded by maxKeyCombos combos of at most maxComboKeys known key names.
	list := strings.Join(names, ", ")
	rec.action = list
	if res, err := s.confirmInput(req, in.Device, "press "+list); res != nil || err != nil {
		return res, sendKeysOutput{}, err
	}
	err := s.kvm.use(ctx, in.Device, func(e *kvmEntry) error {
		c := e.client
		for i, keys := range combos {
			if i > 0 {
				if err := sleepCtx(ctx, typing.DefaultDelay); err != nil {
					return err
				}
			}
			for _, k := range keys {
				if err := c.KeyEvent(k, true); err != nil {
					_ = c.ReleaseAll()
					return err
				}
			}
			for j := len(keys) - 1; j >= 0; j-- {
				if err := c.KeyEvent(keys[j], false); err != nil {
					_ = c.ReleaseAll()
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, sendKeysOutput{}, err
	}
	return nil, sendKeysOutput{Device: in.Device, Sent: len(combos)}, nil
}

// comboText is a valid combo as written, without the spaces around "+" (so its length is
// bounded by the key names).
func comboText(combo string) string {
	parts := strings.Split(combo, "+")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, "+")
}

// --- kvm_mouse

type mouseInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Action string `json:"action" jsonschema:"move, click, double_click, down (press and hold), up (release) or scroll"`
	X      int    `json:"x" jsonschema:"pixels from the left of the last kvm_screenshot"`
	Y      int    `json:"y" jsonschema:"pixels from the top of the last kvm_screenshot"`
	Button string `json:"button,omitempty" jsonschema:"left (default), right or middle"`
	Amount int    `json:"amount,omitempty" jsonschema:"scroll only: wheel clicks, positive scrolls down, negative up (at most 50)"`
}

type mouseOutput struct {
	Device  string `json:"device"`
	Action  string `json:"action"`
	ScreenX int    `json:"screen_x" jsonschema:"the point on the screen, in screen pixels"`
	ScreenY int    `json:"screen_y"`
}

var mouseButtons = map[string]kvm.Button{"": kvm.ButtonLeft, "left": kvm.ButtonLeft, "right": kvm.ButtonRight, "middle": kvm.ButtonMiddle}

// RFB pointer mask bits of the wheel (buttons 4 and 5), as the app sends them.
const (
	wheelUp   kvm.Button = 1 << 3
	wheelDown kvm.Button = 1 << 4
)

func (s *server) kvmMouse(ctx context.Context, req *mcp.CallToolRequest, in mouseInput, rec *record) (*mcp.CallToolResult, mouseOutput, error) {
	rec.device = in.Device
	switch in.Action {
	case "move", "click", "double_click", "down", "up", "scroll":
	default:
		return nil, mouseOutput{}, refuse("action must be move, click, double_click, down, up or scroll.")
	}
	btn, ok := mouseButtons[in.Button]
	if !ok {
		return nil, mouseOutput{}, refuse("button must be left, right or middle.")
	}
	if in.X < 0 || in.Y < 0 {
		return nil, mouseOutput{}, refuse("x and y must be 0 or more.")
	}
	if in.Action == "scroll" && (in.Amount == 0 || in.Amount > maxScrollClick || in.Amount < -maxScrollClick) {
		return nil, mouseOutput{}, refuse("scroll needs amount between -%d and %d, not 0 (positive scrolls down).", maxScrollClick, maxScrollClick)
	}
	what := fmt.Sprintf("%s at %d,%d", in.Action, in.X, in.Y)
	switch in.Action {
	case "click", "double_click", "down", "up":
		name := in.Button
		if name == "" {
			name = "left"
		}
		what += " (" + name + " button)"
	case "scroll":
		what += fmt.Sprintf(" (%d)", in.Amount)
	}
	rec.action = what
	if res, err := s.confirmInput(req, in.Device, "use the mouse: "+what); res != nil || err != nil {
		return res, mouseOutput{}, err
	}
	var out mouseOutput
	err := s.kvm.use(ctx, in.Device, func(e *kvmEntry) error {
		c := e.client
		w, h := c.Size()
		x, y := screenPoint(in.X, in.Y, e.imgW, e.imgH, w, h)
		send := func(b kvm.Button) error { return c.PointerEvent(x, y, b) }
		var steps []kvm.Button
		base := e.buttons &^ btn
		switch in.Action {
		case "move":
			steps = []kvm.Button{e.buttons}
		case "click":
			steps = []kvm.Button{e.buttons, base | btn, base}
		case "double_click":
			steps = []kvm.Button{e.buttons, base | btn, base, base | btn, base}
		case "down":
			steps = []kvm.Button{e.buttons, e.buttons | btn}
		case "up":
			steps = []kvm.Button{base}
		case "scroll":
			wheel, n := wheelDown, in.Amount
			if n < 0 {
				wheel, n = wheelUp, -n
			}
			for range n {
				steps = append(steps, e.buttons|wheel, e.buttons)
			}
		}
		e.lastX, e.lastY = x, y
		for _, b := range steps {
			if err := send(b); err != nil {
				return err
			}
			if b&(wheelUp|wheelDown) == 0 {
				e.buttons = b
			}
		}
		out = mouseOutput{Device: in.Device, Action: in.Action, ScreenX: x, ScreenY: y}
		return nil
	})
	if err != nil {
		return nil, mouseOutput{}, err
	}
	return nil, out, nil
}

// --- kvm_type_text

type typeInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Text   string `json:"text" jsonschema:"the text to type"`
	Layout string `json:"layout,omitempty" jsonschema:"keyboard layout active on the device; default: the favorite's layout in the OpenLAMT app, else the system language of this computer"`
	Secret bool   `json:"secret,omitempty" jsonschema:"the text is a password or another secret: a confirmation prompt shows only its length, not the text. Only for a single line without line breaks, tabs or other control characters"`
}

// maxPreview is how much of the text a confirmation prompt shows.
const maxPreview = 500

// typePrompt is what the person confirming sees before kvm_type_text runs: the text as it will
// be typed, line breaks shown as ⏎ and counted, because each one is an Enter press that may run
// a command. A secret text shows only its length and the Enter presses.
func typePrompt(plan typing.Plan, secret bool) string {
	enters := strings.Count(plan.Text, "\n")
	what := fmt.Sprintf("type %d characters with %d Enter %s (layout %s)", plan.Chars, enters, plural(enters, "press", "presses"), plan.Layout)
	if secret {
		// The agent is not trusted: say that it chose to hide the text. typeText accepts secret
		// only for text without line breaks and other control characters.
		return fmt.Sprintf("type %d characters (layout %s). The AI agent chose to hide this text (marked as secret); "+
			"it is one line without Enter presses or other control characters", plan.Chars, plan.Layout)
	}
	r := []rune(strings.NewReplacer("\n", "⏎", "\t", "⇥").Replace(plan.Text))
	preview := string(r[:min(len(r), maxPreview)])
	if len(r) > maxPreview {
		preview += fmt.Sprintf("… (%d more characters)", len(r)-maxPreview)
	}
	return what + ": «" + preview + "»"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

type skippedChar struct {
	Char   string `json:"char"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type typeOutput struct {
	Device  string        `json:"device"`
	Layout  string        `json:"layout"`
	Typed   int           `json:"typed" jsonschema:"characters typed, line breaks included"`
	Skipped []skippedChar `json:"skipped,omitempty" jsonschema:"characters the layout cannot produce; they were not typed"`
}

// typeLayout picks the layout like the app: the call's choice, then the favorite's, then the
// system language. The app's global layout setting is a Fyne preference this server cannot read.
func typeLayout(param string, fav favorites.Favorite) (typing.Layout, error) {
	if strings.TrimSpace(param) != "" {
		l, ok := typing.ParseLayout(param)
		if !ok {
			return "", refuse("layout %q is unknown; use one of %s.", param, strings.Join(layoutNames(), ", "))
		}
		return l, nil
	}
	if l, ok := typing.ParseLayout(fav.Layout); ok {
		return l, nil
	}
	return typing.DetectLocalLayout(""), nil
}

func layoutNames() []string {
	var out []string
	for _, l := range typing.Layouts() {
		out = append(out, string(l))
	}
	return out
}

func (s *server) kvmTypeText(ctx context.Context, req *mcp.CallToolRequest, in typeInput, rec *record) (*mcp.CallToolResult, typeOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, typeOutput{}, err
	}
	layout, err := typeLayout(in.Layout, fav)
	if err != nil {
		return nil, typeOutput{}, err
	}
	plan, err := typing.Prepare(in.Text, layout)
	switch {
	case err != nil:
		return nil, typeOutput{}, err
	case plan.TooLong:
		return nil, typeOutput{}, refuse("The text has %d characters; at most %d can be typed per call.", plan.Chars, typing.MaxChars)
	case plan.Chars == 0:
		return nil, typeOutput{}, refuse("text is empty.")
	case plan.Typeable() == 0:
		return nil, typeOutput{}, refuse("There is no character in the text that layout %s can type. Nothing was sent.", layout)
	}
	// Never the text itself, also when it is not secret.
	if in.Secret && strings.ContainsFunc(in.Text, unicode.IsControl) {
		return nil, typeOutput{}, refuse("secret is only allowed for a single line of text without line breaks, tabs or other " +
			"control characters, so the person confirming knows that no Enter press is hidden. Nothing was sent.")
	}
	rec.action = fmt.Sprintf("%d characters, %d Enter, layout %s", plan.Chars, strings.Count(plan.Text, "\n"), layout)
	if in.Secret {
		rec.action += ", secret"
	}
	if res, err := s.confirmInput(req, in.Device, typePrompt(plan, in.Secret)); res != nil || err != nil {
		return res, typeOutput{}, err
	}
	err = s.kvm.use(ctx, in.Device, func(e *kvmEntry) error {
		if err := typing.Type(ctx, e.client, plan, typing.DefaultDelay, nil); err != nil {
			_ = e.client.ReleaseAll()
			return err
		}
		return nil
	})
	if err != nil {
		return nil, typeOutput{}, err
	}
	out := typeOutput{Device: in.Device, Layout: string(layout), Typed: plan.Typeable()}
	for _, u := range plan.Unmappable {
		out.Skipped = append(out.Skipped, skippedChar{Char: string(u.Char), Line: u.Line, Column: u.Column})
	}
	return nil, out, nil
}
