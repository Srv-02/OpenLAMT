package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ider/idertest"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/redir"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/sol"
)

// isoSize is the size of the test ISO: 512 sectors of 2048 bytes.
const isoSize = 512 * 2048

// mediaFiles points the image cache (os.UserCacheDir on every platform) at a temp folder and
// returns the folder mkiso builds into, an ISO file and a folder with one file. Paths are
// resolved, so a temp folder behind a symlink (macOS /var) is not shown as a link.
type mediaFiles struct {
	cache, iso, folder, dir string
}

func newMediaFiles(t *testing.T) mediaFiles {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)
	cd, err := mkiso.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := mediaFiles{cache: cd, iso: filepath.Join(dir, "x.iso"), folder: filepath.Join(dir, "tools"), dir: dir}
	if err := os.WriteFile(m.iso, bytes.Repeat([]byte{0x5a}, isoSize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(m.folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.folder, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

// cachedImages counts the built images in the cache.
func (m mediaFiles) cachedImages(t *testing.T) int {
	t.Helper()
	l, err := filepath.Glob(filepath.Join(m.cache, "*.iso"))
	if err != nil {
		t.Fatal(err)
	}
	return len(l)
}

// addIDER gives the fake device a fake IDE-R server.
func addIDER(t *testing.T, f *session.Fake) *idertest.Server {
	t.Helper()
	srv := idertest.Start(t, idertest.Options{Username: "admin", Password: "ider-pw"})
	f.IDERAddr, f.IDERUser, f.IDERPassword = srv.Addr, "admin", "ider-pw"
	return srv
}

// callResult is a tool call that ran on another goroutine.
type callResult struct {
	text    string
	out     map[string]any
	isError bool
	err     error
}

// callAsync runs a tool on its own goroutine, so the test goroutine can play the device side
// meanwhile (idertest calls t.Fatal, which must stay on the test goroutine).
func (h *harness) callAsync(name string, args map[string]any) <-chan callResult {
	ch := make(chan callResult, 1)
	go func() {
		res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		r := callResult{err: err}
		if err == nil {
			var parts []string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					parts = append(parts, tc.Text)
				}
			}
			r.text, r.isError = strings.Join(parts, "\n"), res.IsError
			if res.StructuredContent != nil {
				b, _ := json.Marshal(res.StructuredContent)
				_ = json.Unmarshal(b, &r.out)
			}
		}
		ch <- r
	}()
	return ch
}

func (h *harness) await(ch <-chan callResult) callResult {
	h.t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			h.t.Fatalf("protocol error %v", r.err)
		}
		return r
	case <-time.After(10 * time.Second):
		h.t.Fatal("the tool call did not return")
		return callResult{}
	}
}

// mount runs media_mount and plays the device side of the IDE-R session.
func (h *harness) mount(srv *idertest.Server, args map[string]any) (*idertest.Peer, callResult) {
	h.t.Helper()
	ch := h.callAsync("media_mount", args)
	p := srv.AcceptSession(h.t)
	h.t.Cleanup(p.Disconnect)
	r := h.await(ch)
	if r.isError {
		h.t.Fatalf("media_mount %v: %s", args, r.text)
	}
	return p, r
}

// waitClosed reads from the peer until the client has closed the connection.
func waitClosed(t *testing.T, p *idertest.Peer) {
	t.Helper()
	for {
		_, err := p.TryRead(5 * time.Second)
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("the IDE-R connection is still open")
		}
		if err != nil {
			return
		}
	}
}

// readAll lets the host read the whole test ISO, then waits for the answer to an empty read: the
// client handles commands in order, so the counters of the first read are set by then.
func readAll(t *testing.T, p *idertest.Peer) {
	t.Helper()
	p.Written(t, []byte{0x28, 0, 0, 0, 0, 0, 0, 0x02, 0, 0, 0, 0}, 0x10, 0) // READ_10, 512 blocks
	p.Written(t, []byte{0x28, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 0x10, 0)    // READ_10, 0 blocks
	for {
		if m := p.Read(t); m.Cmd == 0x51 {
			return
		}
	}
}

func (h *harness) mediaEntry(name string) *mediaEntry {
	h.srv.s.media.mu.Lock()
	defer h.srv.s.media.mu.Unlock()
	return h.srv.s.media.entries[name]
}

// countBuilds counts the folder images built; release, when not nil, holds each build until it
// is closed or the build's context ends. started gets one value per build.
func countBuilds(t *testing.T, release <-chan struct{}) (*atomic.Int32, <-chan struct{}) {
	t.Helper()
	orig := buildImage
	t.Cleanup(func() { buildImage = orig })
	var n atomic.Int32
	started := make(chan struct{}, 8)
	buildImage = func(ctx context.Context, dir string, opt mkiso.Options) (*mkiso.TempImage, mkiso.Info, error) {
		n.Add(1)
		started <- struct{}{}
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, mkiso.Info{}, ctx.Err()
			}
		}
		return orig(ctx, dir, opt)
	}
	return &n, started
}

// TestMediaMountISOAndStatus: an ISO is mounted as a CD drive, media_status shows it with the
// read rate since the previous status call, media_unmount closes the session.
func TestMediaMountISOAndStatus(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	h.start()
	clock, skew := skewClock()
	h.srv.s.media.now = clock
	out := h.mustOK("media_status", dev("NUC"))
	if out["mounted"] != false || out["mounting"] != false || out["pending_consent"] != false {
		t.Fatalf("status before mount %v", out)
	}
	p, r := h.mount(srv, dev("NUC", "path", files.iso))
	for k, v := range map[string]any{"device": "NUC", "name": "x.iso", "path": files.iso, "kind": "iso", "size_bytes": float64(isoSize)} {
		if r.out[k] != v {
			t.Errorf("mount %s = %v, want %v", k, r.out[k], v)
		}
	}
	if _, ok := r.out["files"]; ok {
		t.Errorf("files for an ISO: %v", r.out)
	}
	if !strings.Contains(r.text, "read-only CD drive") || !strings.Contains(r.text, "media_boot_next") || !strings.Contains(r.text, "media_unmount") {
		t.Errorf("text %q", r.text)
	}
	if st := f.IDERStarts(); !reflect.DeepEqual(st, []ider.StartMode{ider.StartNow}) {
		t.Fatalf("starts %v", st)
	}
	out = h.mustOK("media_status", dev("NUC"))
	for k, v := range map[string]any{"mounted": true, "mounting": false, "name": "x.iso", "kind": "iso", "path": files.iso,
		"size_bytes": float64(isoSize), "bytes_read": float64(0), "read_rate_mb_s": float64(0), "boot_next": false} {
		if out[k] != v {
			t.Errorf("status %s = %v, want %v", k, out[k], v)
		}
	}
	if _, ok := out["last_read"]; ok {
		t.Errorf("last_read before a read: %v", out)
	}

	readAll(t, p)
	skew.Store(int64(time.Second))
	out = h.mustOK("media_status", dev("NUC"))
	if out["bytes_read"] != float64(isoSize) || out["read_rate_mb_s"] != 1.0 || out["last_read"] == nil {
		t.Fatalf("status after a read %v", out)
	}
	if _, err := time.Parse(time.RFC3339, out["last_read"].(string)); err != nil {
		t.Fatalf("last_read %v", out["last_read"])
	}
	skew.Store(int64(2 * time.Second))
	if out = h.mustOK("media_status", dev("NUC")); out["read_rate_mb_s"] != float64(0) || out["mounted_seconds"].(float64) < 2 {
		t.Fatalf("status without reads %v", out)
	}

	out = h.mustOK("media_unmount", dev("NUC"))
	if out["unmounted"] != true || out["boot_option_cleared"] != false {
		t.Fatalf("unmount %v", out)
	}
	waitClosed(t, p)
	if out = h.mustOK("media_status", dev("NUC")); out["mounted"] != false {
		t.Fatalf("status after unmount %v", out)
	}
	if out = h.mustOK("media_unmount", dev("NUC")); out["unmounted"] != false {
		t.Fatalf("second unmount %v", out)
	}
	var actions []string
	for _, e := range h.logEntries() {
		if e["tool"] == "media_mount" {
			actions = append(actions, e["action"].(string))
		}
	}
	if !reflect.DeepEqual(actions, []string{"iso file " + files.iso}) {
		t.Fatalf("log %q", actions)
	}
}

// TestMediaMountFolderBuildsImage: a folder becomes a temporary ISO, deleted after the unmount.
func TestMediaMountFolderBuildsImage(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	h.start()
	p, r := h.mount(srv, dev("NUC", "path", files.folder))
	for k, v := range map[string]any{"name": "tools", "kind": "folder", "path": files.folder, "files": float64(1), "uefi_bootable": false} {
		if r.out[k] != v {
			t.Errorf("mount %s = %v, want %v", k, r.out[k], v)
		}
	}
	if n := files.cachedImages(t); n != 1 {
		t.Fatalf("%d images while mounted", n)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["kind"] != "folder" || out["mounted"] != true {
		t.Fatalf("status %v", out)
	}
	h.mustOK("media_unmount", dev("NUC"))
	waitClosed(t, p)
	if n := files.cachedImages(t); n != 0 {
		t.Fatalf("%d images after unmount", n)
	}
}

// TestMediaMountValidation: the path is checked before any device contact.
func TestMediaMountValidation(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	other := filepath.Join(files.dir, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustFail("media_mount", dev("NUC", "path", ""), "absolute path")
	h.mustFail("media_mount", dev("NUC", "path", filepath.Join("images", "x.iso")), "absolute path")
	h.mustFail("media_mount", dev("NUC", "path", filepath.Join(files.dir, "missing.iso")), "does not exist")
	h.mustFail("media_mount", dev("NUC", "path", other), "must be an .iso file or a folder")
	h.mustFail("media_mount", dev("NUC", "path", files.iso, "consent_code", "12a456"), "six digits")
	h.mustFail("media_mount", dev("Nope", "path", files.iso), "No favorite")
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("%d device connections", n)
	}
}

// TestMediaMountTwiceRefused: one mount per device; another client holding IDE-R is named with
// the app's Media menu and media_mount, and the built image is deleted.
func TestMediaMountTwiceRefused(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	release := make(chan struct{})
	_, started := countBuilds(t, release)
	h.start()

	ch := h.callAsync("media_mount", dev("NUC", "path", files.folder))
	<-started
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "is in progress")
	h.mustFail("media_unmount", dev("NUC"), "is in progress")
	h.mustFail("media_boot_next", dev("NUC", "enabled", true), "is in progress")
	if out := h.mustOK("media_status", dev("NUC")); out["mounting"] != true || out["mounted"] != false {
		t.Fatalf("status while mounting %v", out)
	}
	close(release)
	p := srv.AcceptSession(t)
	t.Cleanup(p.Disconnect)
	if r := h.await(ch); r.isError {
		t.Fatalf("mount: %s", r.text)
	}
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "has «tools» mounted by this server already: call media_unmount first")
	h.mustOK("media_unmount", dev("NUC"))
	waitClosed(t, p)

	f.IDERErr = &redir.RedirectionError{Stage: "start", Status: 1}
	text := h.mustFail("media_mount", dev("NUC", "path", files.folder), "Another IDE-R session to this device is open")
	if !strings.Contains(text, "Media menu") || !strings.Contains(text, "call media_mount again") || strings.Contains(text, "KVM") {
		t.Fatalf("%q", text)
	}
	if n := files.cachedImages(t); n != 0 {
		t.Fatalf("%d images after a failed mount", n)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["mounted"] != false || out["mounting"] != false || out["pending_consent"] != false {
		t.Fatalf("status after a failed mount %v", out)
	}
	f.IDERErr = &redir.RedirectionError{Stage: "auth", Status: 1}
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "IDE-R login failed")
	f.IDERErr = &amt.IDERError{Step: "RedirectionService.RequestStateChange", ReturnValue: 2}
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "refused to enable media redirection")
	f.IDERErr = amt.ErrIDERUnsupported
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "does not support media redirection")
}

// TestMediaConsentKeepsImage: the image built for a call that needs consent is kept for the
// retry with the code (also after a wrong code) and closed by media_unmount or its expiry.
func TestMediaConsentKeepsImage(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Consent, f.ConsentCode = true, "123456"
	f.St.ControlMode = amt.ControlModeClient
	srv := addIDER(t, f)
	builds, _ := countBuilds(t, nil)
	h.start()
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "call media_mount again with consent_code")
	if out := h.mustOK("media_status", dev("NUC")); out["pending_consent"] != true || out["mounted"] != false || out["mounting"] != false {
		t.Fatalf("status %v", out)
	}
	h.mustFail("media_mount", dev("NUC", "path", files.folder, "consent_code", "654321"), "did not accept the consent code")
	if n := files.cachedImages(t); n != 1 || builds.Load() != 1 {
		t.Fatalf("%d images, %d builds after a wrong code", n, builds.Load())
	}
	_, r := h.mount(srv, dev("NUC", "path", files.folder, "consent_code", "123456"))
	if r.out["kind"] != "folder" || builds.Load() != 1 {
		t.Fatalf("mount %v, %d builds", r.out, builds.Load())
	}
	if out := h.mustOK("media_status", dev("NUC")); out["pending_consent"] != false || out["mounted"] != true {
		t.Fatalf("status after the retry %v", out)
	}
	h.mustOK("media_unmount", dev("NUC"))
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "123456") || strings.Contains(string(b), "654321") {
			t.Fatalf("action log holds a consent code: %s", b)
		}
	}

	// media_unmount closes a kept image.
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "consent_code")
	if out := h.mustOK("media_unmount", dev("NUC")); out["unmounted"] != false {
		t.Fatalf("unmount of a kept image %v", out)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["pending_consent"] != false || files.cachedImages(t) != 0 {
		t.Fatalf("kept image not closed: %v", out)
	}

	// The expiry closes only the image it belongs to.
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "consent_code")
	m := h.srv.s.media
	m.mu.Lock()
	p := m.pending["NUC"]
	m.mu.Unlock()
	m.dropPending("NUC", &pendingImage{})
	if out := h.mustOK("media_status", dev("NUC")); out["pending_consent"] != true {
		t.Fatalf("another expiry closed the kept image: %v", out)
	}
	m.dropPending("NUC", p)
	if out := h.mustOK("media_status", dev("NUC")); out["pending_consent"] != false || files.cachedImages(t) != 0 {
		t.Fatalf("expired image not closed: %v", out)
	}
	// A kept ISO is not reused for a folder: the next call builds again.
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "consent_code")
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "consent_code")
	if n := builds.Load(); n != 4 {
		t.Fatalf("%d builds", n)
	}
}

// TestMediaBootNextAndClear: media_boot_next sets and clears the IDE-R boot option; the unmount
// and the end of the session by the device clear it when it is set.
func TestMediaBootNextAndClear(t *testing.T) {
	files := newMediaFiles(t)
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h.start()
	var logBuf bytes.Buffer
	h.srv.s.media.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	h.mustFail("media_boot_next", dev("NUC", "enabled", true), "Nothing is mounted on \"NUC\" by this server: call media_mount first")
	if len(asked) != 0 {
		t.Fatalf("asked without a mount: %q", asked)
	}
	p, _ := h.mount(srv, dev("NUC", "path", files.iso))
	asked = nil
	if out := h.mustOK("media_boot_next", dev("NUC", "enabled", true)); out["boot_next"] != true {
		t.Fatalf("%v", out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "set the device to boot from «x.iso» at its next start (no restart now)") {
		t.Fatalf("asked %q", asked)
	}
	if n, c := f.ISOCalls(); n != 1 || c != 0 {
		t.Fatalf("calls %d %d", n, c)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["boot_next"] != true {
		t.Fatalf("status %v", out)
	}
	if out := h.mustOK("media_boot_next", dev("NUC", "enabled", false)); out["boot_next"] != false || len(asked) != 1 {
		t.Fatalf("%v, asked %q", out, asked)
	}
	h.mustOK("media_boot_next", dev("NUC", "enabled", true))
	if n, c := f.ISOCalls(); n != 2 || c != 1 {
		t.Fatalf("calls %d %d", n, c)
	}
	if out := h.mustOK("media_unmount", dev("NUC")); out["unmounted"] != true || out["boot_option_cleared"] != true {
		t.Fatalf("unmount %v", out)
	}
	if n, c := f.ISOCalls(); n != 2 || c != 2 {
		t.Fatalf("calls %d %d", n, c)
	}
	waitClosed(t, p)

	// A failed clear is a warning; the image is unmounted anyway.
	p, _ = h.mount(srv, dev("NUC", "path", files.iso))
	h.mustOK("media_boot_next", dev("NUC", "enabled", true))
	f.ClearISOBootErr = errors.New("boom")
	out := h.mustOK("media_unmount", dev("NUC"))
	if out["unmounted"] != true || out["boot_option_cleared"] != false || !strings.Contains(strings.Join(anyStrings(out["warnings"]), " "), "may still be set") {
		t.Fatalf("unmount %v", out)
	}
	f.ClearISOBootErr = nil
	waitClosed(t, p)

	// The device ends the session: the mount is gone, the boot option cleared, a warning logged.
	p, _ = h.mount(srv, dev("NUC", "path", files.iso))
	h.mustOK("media_boot_next", dev("NUC", "enabled", true))
	_, before := f.ISOCalls()
	p.Close(t)
	h.srv.s.media.watchers.Wait()
	if _, c := f.ISOCalls(); c != before+1 {
		t.Fatalf("clears %d, want %d", c, before+1)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["mounted"] != false || out["boot_next"] != false {
		t.Fatalf("status after the device ended %v", out)
	}
	if s := logBuf.String(); !strings.Contains(s, "The device closed the media session.") {
		t.Fatalf("log %q", s)
	}
	h.mustFail("media_boot_next", dev("NUC", "enabled", true), "call media_mount first")
}

func anyStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, s := range l {
		out = append(out, s.(string))
	}
	return out
}

// TestBootFromISO: needs a mount; a running device needs the group destructive, one that is off
// is powered on.
func TestBootFromISO(t *testing.T) {
	files := newMediaFiles(t)
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	h.cfg.Groups[GroupDestructive] = false
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	h.start()
	h.mustFail("boot_from_iso", dev("NUC"), "call media_mount first")
	h.mount(srv, dev("NUC", "path", files.iso))
	asked = nil
	h.mustFail("boot_from_iso", dev("NUC"), `"destructive" is off`)
	if len(asked) != 1 || !strings.Contains(asked[0], "restart and boot once from «x.iso» (a running device is reset; unsaved work is lost)") {
		t.Fatalf("asked %q", asked)
	}
	if p := f.Powered(); len(p) != 0 {
		t.Fatalf("powered %v", p)
	}
	f.SetStatus(amt.Status{PowerState: models.PowerStateOffSoft, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin})
	out := h.mustOK("boot_from_iso", dev("NUC"))
	if out["power_state_before"] != "off_soft" || !strings.Contains(out["result"].(string), "powers on and boots once from x.iso") {
		t.Fatalf("%v", out)
	}
	if p := f.Powered(); !reflect.DeepEqual(p, []amt.PowerAction{amt.BootToISO}) {
		t.Fatalf("powered %v", p)
	}

	h = newHarness(t)
	f = h.addDevice("NUC", "192.0.2.10", false)
	srv = addIDER(t, f)
	h.start()
	h.mount(srv, dev("NUC", "path", files.iso))
	out = h.mustOK("boot_from_iso", dev("NUC"))
	if out["power_state_before"] != "on" || !strings.Contains(out["result"].(string), "resets and boots once from x.iso") {
		t.Fatalf("%v", out)
	}
	if p := f.Powered(); !reflect.DeepEqual(p, []amt.PowerAction{amt.BootToISO}) {
		t.Fatalf("powered %v", p)
	}
	f.PowerErr = &amt.PowerError{Action: amt.BootToISO, ReturnValue: 1}
	h.mustFail("boot_from_iso", dev("NUC"), "The device refused the boot from ISO (AMT ReturnValue 1). The IDE-R boot option may still be set")
}

// TestMediaConfirmationShowsPath: the prompt shows the full path visibly, what is mounted and
// that IDE-R is turned on; a declined mount contacts no device.
func TestMediaConfirmationShowsPath(t *testing.T) {
	files := newMediaFiles(t)
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addIDER(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "not confirmed")
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "not confirmed")
	if len(asked) != 2 {
		t.Fatalf("asked %q", asked)
	}
	for _, want := range []string{"mount the ISO file «" + files.iso + "» (1.0 MB) as a read-only CD drive",
		"media redirection (IDE-R) is turned on on the device if it is off and stays on", `device "NUC"`} {
		if !strings.Contains(asked[0], want) {
			t.Errorf("ISO prompt %q lacks %q", asked[0], want)
		}
	}
	if strings.Contains(asked[0], "given as") {
		t.Errorf("ISO prompt %q", asked[0])
	}
	for _, want := range []string{"mount the folder «" + files.folder + "» as a read-only CD drive (OpenLAMT builds an ISO image of it first)",
		"turned on on the device if it is off"} {
		if !strings.Contains(asked[1], want) {
			t.Errorf("folder prompt %q lacks %q", asked[1], want)
		}
	}

	names := map[string]string{"a‮osi.exe.iso": "a<U+202E>osi.exe.iso"}
	if runtime.GOOS != "windows" { // Windows forbids line breaks in file names
		names["two\nlines.iso"] = "two⏎lines.iso"
	}
	for name, shown := range names {
		p := filepath.Join(files.dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		asked = nil
		h.mustFail("media_mount", dev("NUC", "path", p), "not confirmed")
		if len(asked) != 1 || !strings.Contains(asked[0], "«"+filepath.Join(files.dir, shown)+"»") {
			t.Errorf("prompt for %q: %q", name, asked)
		}
	}

	link := filepath.Join(files.dir, "link.iso")
	if err := os.Symlink(files.iso, link); err != nil {
		t.Logf("no symlink: %v", err) // Windows without the privilege
	} else {
		asked = nil
		h.mustFail("media_mount", dev("NUC", "path", link), "not confirmed")
		// The resolved path is checked, mounted and shown, the link only as what was given.
		if len(asked) != 1 || !strings.Contains(asked[0], "mount the ISO file «"+files.iso+"» (given as «"+link+"»)") {
			t.Errorf("link prompt %q", asked)
		}
	}
	if n := len(h.connectCalls()); n != 0 || len(f.IDERStarts()) != 0 {
		t.Fatalf("%d device connections", n)
	}
	if out := h.mustOK("media_status", dev("NUC")); out["mounted"] != false || out["pending_consent"] != false {
		t.Fatalf("status %v", out)
	}
}

// TestMediaToolsFollowGroups: the media group offers the media tools; boot_from_iso needs
// power_boot and media.
func TestMediaToolsFollowGroups(t *testing.T) {
	has := func(names []string, name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	media := []string{"media_boot_next", "media_mount", "media_status", "media_unmount"}

	h := newHarness(t)
	h.cfg.Groups[GroupMedia] = false
	h.start()
	names := h.toolNames()
	for _, n := range append(media, "boot_from_iso") {
		if has(names, n) {
			t.Fatalf("media off offers %s: %v", n, names)
		}
	}
	if !has(names, "boot_to_bios") {
		t.Fatalf("media off: %v", names)
	}

	h = newHarness(t)
	h.cfg.Groups[GroupPowerBoot] = false
	h.start()
	names = h.toolNames()
	for _, n := range media {
		if !has(names, n) {
			t.Fatalf("power_boot off lacks %s: %v", n, names)
		}
	}
	if has(names, "boot_from_iso") {
		t.Fatalf("power_boot off offers boot_from_iso: %v", names)
	}
}

// TestServerCloseUnmountsAndClearsBoot: Close ends every mount, clears the boot options it set,
// waits for a mount in flight and refuses new calls.
func TestServerCloseUnmountsAndClearsBoot(t *testing.T) {
	files := newMediaFiles(t)
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	g := h.addDevice("Lab", "lab.example.com", false)
	srvF, srvG := addIDER(t, f), addIDER(t, g)
	h.start()
	pf, _ := h.mount(srvF, dev("NUC", "path", files.iso))
	pg, _ := h.mount(srvG, dev("Lab", "path", files.folder))
	h.mustOK("media_boot_next", dev("NUC", "enabled", true))
	c1, c2 := h.mediaEntry("NUC").client, h.mediaEntry("Lab").client
	h.srv.Close()
	for _, c := range []*ider.Client{c1, c2} {
		select {
		case <-c.Done():
		default:
			t.Fatal("mount still open after Close")
		}
	}
	waitClosed(t, pf)
	waitClosed(t, pg)
	if _, c := f.ISOCalls(); c != 1 {
		t.Fatalf("NUC clears %d", c)
	}
	if _, c := g.ISOCalls(); c != 0 {
		t.Fatalf("Lab clears %d", c)
	}
	if n := files.cachedImages(t); n != 0 {
		t.Fatalf("%d images after Close", n)
	}
	h.mustFail("media_status", dev("NUC"), "shutting down")
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "shutting down")
	h.mustFail("media_unmount", dev("NUC"), "shutting down")

	// A mount in flight ends with Close, which waits for it; a kept image is closed.
	h = newHarness(t)
	f = h.addDevice("NUC", "192.0.2.10", false)
	g = h.addDevice("Lab", "lab.example.com", false)
	g.Consent, g.ConsentCode = true, "123456"
	addIDER(t, f)
	addIDER(t, g)
	_, started := countBuilds(t, make(chan struct{}))
	h.start()
	h.mustFail("media_mount", dev("Lab", "path", files.iso), "consent_code")
	ch := h.callAsync("media_mount", dev("NUC", "path", files.folder))
	<-started
	h.srv.Close() // returns after the mount has ended
	if r := h.await(ch); !r.isError || !strings.Contains(r.text, "shutting down") {
		t.Fatalf("mount during Close: %v %q", r.isError, r.text)
	}
	m := h.srv.s.media
	m.mu.Lock()
	entries, pending := len(m.entries), len(m.pending)
	m.mu.Unlock()
	if entries != 0 || pending != 0 {
		t.Fatalf("after Close: %d entries, %d kept images", entries, pending)
	}
	if n := files.cachedImages(t); n != 0 {
		t.Fatalf("%d images after Close", n)
	}
}

// TestExplainMediaErrors: IDE-R, image and boot errors get their own sentences.
func TestExplainMediaErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{ider.ErrClosedByDevice, "The device closed the media session."},
		{sol.ErrClosedByDevice, "The device closed the serial console session."},
		{ider.ErrToggleFailed, "did not attach the virtual CD drive. Try media_mount again."},
		{amt.ErrIDERUnsupported, "This device does not support media redirection (IDE-R)."},
		{&amt.IDERError{Step: "x", ReturnValue: 5}, "refused to enable media redirection (AMT ReturnValue 5)"},
		{&redirOpenError{kind: "ider", err: &redir.RedirectionError{Stage: "auth"}}, "IDE-R login failed"},
		{mkiso.ErrEmpty, "The folder contains no files."},
		{mkiso.ErrFileTooLarge, "4 GiB or larger"},
		{mkiso.ErrNameTooLong, "name"},
		{mkiso.ErrNameConflict, "names"},
		{mkiso.ErrTooManyDirs, "folders"},
		{mkiso.ErrDirTooLarge, "entries"},
		{mkiso.ErrTooLarge, "too large"},
		{mkiso.ErrFileChanged, "changed"},
		{&mkiso.PathError{Path: "big/file.bin", Err: mkiso.ErrFileTooLarge}, "big/file.bin: A file in the folder is 4 GiB or larger"},
		{&amt.PowerError{Action: amt.BootToISO, ReturnValue: 2}, "The device refused the boot from ISO (AMT ReturnValue 2)."},
	} {
		if got := explain(c.err); !strings.Contains(got, c.want) || strings.Contains(got, "mkiso:") {
			t.Errorf("explain(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// mustMkdirFile creates dir and an empty file name in it, and returns the file's path.
func mustMkdirFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestMediaMountRefusesProtectedPaths: whatever the config says, media_mount never mounts the
// home folder itself, a folder above it, credentials (SSH, GnuPG, AWS, GitHub CLI) or the
// OpenLAMT configuration, also when a link leads there; other folders in the home folder may be
// mounted. Nothing reaches the device.
func TestMediaMountRefusesProtectedPaths(t *testing.T) {
	files := newMediaFiles(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, ".config"))
	favPath, err := favorites.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	sshISO := mustMkdirFile(t, filepath.Join(home, ".ssh"), "keys.iso")
	mustMkdirFile(t, filepath.Join(home, ".gnupg"), "pubring.kbx")
	mustMkdirFile(t, filepath.Join(home, ".aws"), "credentials")
	mustMkdirFile(t, filepath.Join(home, ".config", "gh"), "hosts.yml")
	cfgISO := mustMkdirFile(t, filepath.Dir(favPath), "backup.iso")
	downloads := mustMkdirFile(t, filepath.Join(home, "Downloads"), "installer.iso")

	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addIDER(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	refused := []string{home, filepath.Dir(home), filepath.Join(home, ".ssh"), sshISO, filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".aws"), filepath.Join(home, ".config"), filepath.Join(home, ".config", "gh"),
		filepath.Dir(favPath), cfgISO, filepath.Dir(h.cfg.ActionLogPath)}
	if link := filepath.Join(files.dir, "innocent"); os.Symlink(filepath.Join(home, ".ssh"), link) == nil {
		refused = append(refused, link)
	} else {
		t.Log("no symlink") // Windows without the privilege
	}
	for _, p := range refused {
		h.mustFail("media_mount", dev("NUC", "path", p), "protected folder")
	}
	if len(asked) != 0 {
		t.Fatalf("asked %q", asked)
	}
	// Other folders and files in the home folder pass the check (and are then declined here).
	h.mustFail("media_mount", dev("NUC", "path", downloads), "not confirmed")
	h.mustFail("media_mount", dev("NUC", "path", filepath.Dir(downloads)), "not confirmed")
	if len(asked) != 2 || !strings.Contains(asked[0], "mount the ISO file «"+downloads+"»") {
		t.Fatalf("asked %q", asked)
	}
	if n := len(h.connectCalls()); n != 0 || len(f.IDERStarts()) != 0 {
		t.Fatalf("%d device connections", n)
	}
}

// TestMediaMountAllowedPaths: with media.allowed_paths only paths inside them are mounted, judged
// after links are resolved; the prompt and the action log show the resolved path and its kind.
func TestMediaMountAllowedPaths(t *testing.T) {
	files := newMediaFiles(t)
	inner := mustMkdirFile(t, files.folder, "inner.iso")
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addIDER(t, f)
	h.cfg.MediaAllowedPaths = []string{files.folder}
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "outside media.allowed_paths")
	h.mustFail("media_mount", dev("NUC", "path", files.dir), "outside media.allowed_paths")
	if link := filepath.Join(files.folder, "out.iso"); os.Symlink(files.iso, link) == nil {
		h.mustFail("media_mount", dev("NUC", "path", link), "outside media.allowed_paths")
	}
	if len(asked) != 0 {
		t.Fatalf("asked %q", asked)
	}
	h.mustFail("media_mount", dev("NUC", "path", inner), "not confirmed")
	h.mustFail("media_mount", dev("NUC", "path", files.folder), "not confirmed")
	if len(asked) != 2 || !strings.Contains(asked[0], "the ISO file «"+inner+"»") || !strings.Contains(asked[1], "the folder «"+files.folder+"»") {
		t.Fatalf("asked %q", asked)
	}
	var actions []string
	for _, e := range h.logEntries() {
		if e["outcome"] == "confirmation_requested" {
			actions = append(actions, e["action"].(string))
		}
	}
	if len(actions) != 2 || actions[0] != "iso file "+inner || actions[1] != "folder "+files.folder {
		t.Fatalf("logged %q", actions)
	}
	if n := len(h.connectCalls()); n != 0 {
		t.Fatalf("%d device connections", n)
	}
}
