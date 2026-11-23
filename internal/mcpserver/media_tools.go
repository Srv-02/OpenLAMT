package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ider"
)

// openIDER mounts img on the device of fav: login check, IDE-R turned on if off, consent with
// code. The WS-Man session is closed again; the IDE-R client has its own socket.
func (s *server) openIDER(ctx context.Context, fav favorites.Favorite, img ider.Image, code consentCode, agreed bool) (*ider.Client, []string, error) {
	sess, warnings, err := s.openRedirection(ctx, fav, agreed)
	if err != nil {
		return nil, nil, err
	}
	defer sess.Close()
	c, err := sess.OpenIDER(ctx, img, ider.StartNow, code)
	if err != nil {
		return nil, nil, &redirOpenError{kind: "ider", err: s.offError(err, "media_mount")}
	}
	return c, warnings, nil
}

// clearISOBoot switches the IDE-R boot option of the device of fav (the one the mount was made
// on) off, bounded by clearBootTimeout. It runs after a request has ended (unmount, end of the
// session, Close), so it has its own context.
func (s *server) clearISOBoot(fav favorites.Favorite) error {
	ctx, cancel := context.WithTimeout(context.Background(), clearBootTimeout)
	defer cancel()
	sess, _, _, err := s.openFav(ctx, fav)
	if err != nil {
		return err
	}
	defer sess.Close()
	return sess.ClearISOBoot()
}

func (s *server) registerMedia(srv *mcp.Server) {
	if !s.cfg.Enabled(GroupMedia) {
		return
	}
	closed := boolPtr(false)
	addTool(s, srv, &mcp.Tool{Name: "media_mount", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: closed},
		Description: "Mount an .iso file or a folder of the computer that runs openlamt-mcp on a device as a read-only CD drive " +
			"(media redirection, IDE-R), for example to install an operating system or run a tool from it. A folder is built into a temporary ISO " +
			"image first; a big folder takes a while, and the build runs within this call, so an MCP client with a short tool timeout may abort it. " +
			"AMT allows one IDE-R session per device: while this server holds it, the Media menu of the OpenLAMT app, MeshCommander and other clients " +
			"cannot mount, and the other way round. If IDE-R is off on the device, it is turned on and stays on, only with the tool group \"settings\" " +
			"(a settings change, like set_remote_features); with require_client_confirmation the mount question includes it. " +
			"Devices that require user consent show a six-digit code on their screen: call without consent_code first, ask a person at the device " +
			"for the code, then call again with it (the image is kept for that call). The image stays mounted until media_unmount, until the device " +
			"ends the session, or until this server stops. Then use media_boot_next or boot_from_iso to boot from it. " +
			"Links in the path are resolved first. The home folder itself, folders above it, the folders that hold credentials and settings " +
			"(for example ~/.ssh, ~/.config, ~/Library, %APPDATA%) and the OpenLAMT configuration are never mounted, and the operator may limit " +
			"mounts to media.allowed_paths."},
		s.mediaMount)
	addTool(s, srv, &mcp.Tool{Name: "media_unmount", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: closed},
		Description: "Unmount the image this server mounted on a device (a device installing or reading from it fails then) and clear the " +
			"IDE-R boot option if media_boot_next set it. Also drops an image kept for a media_mount call with consent_code."},
		s.mediaUnmount)
	addTool(s, srv, &mcp.Tool{Name: "media_status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed},
		Description: "Show what this server has mounted on a device: name, path, size, bytes the device read, the read rate since the previous " +
			"media_status call and whether the IDE-R boot option is set. Contacts no device. After the device ended the session it says mounted false."},
		s.mediaStatus)
	addTool(s, srv, &mcp.Tool{Name: "media_boot_next", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: closed},
		Description: "Set (enabled true) or clear (enabled false) the IDE-R boot option of a device: at its next start it boots once from the image " +
			"mounted with media_mount. Nothing restarts now; use boot_from_iso to restart into the image. media_unmount clears the option."},
		s.mediaBootNext)
	if s.cfg.Enabled(GroupPowerBoot) {
		addTool(s, srv, &mcp.Tool{Name: "boot_from_iso", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed},
			Description: "Restart a running device (a hard reset) or power on a device that is off, booting once from the image mounted with media_mount. " +
				"A device that is not off needs the tool group \"destructive\"."},
			s.bootFromISO)
	}
}

// --- media_mount

type mediaMountInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Path        string `json:"path" jsonschema:"absolute path of an .iso file or of a folder on the computer that runs openlamt-mcp"`
	ConsentCode string `json:"consent_code,omitempty" jsonschema:"six-digit user consent code the device shows on its screen; leave it out on the first call"`
}

type mediaMountOutput struct {
	Device       string   `json:"device"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Kind         string   `json:"kind" jsonschema:"iso or folder"`
	SizeBytes    int64    `json:"size_bytes" jsonschema:"size of the image"`
	Files        int      `json:"files,omitempty" jsonschema:"folder only: files on the image"`
	Skipped      []string `json:"skipped,omitempty" jsonschema:"folder only: symbolic links and special files left out, relative to the folder"`
	UEFIBootable *bool    `json:"uefi_bootable,omitempty" jsonschema:"folder only: EFI/BOOT/BOOTX64.EFI was found, so a UEFI firmware can boot the image"`
	Warnings     []string `json:"warnings,omitempty"`
}

// mediaPath checks the path media_mount was given and resolves it: an existing .iso file or
// folder, after symbolic links, outside the protected folders and inside media.allowed_paths when
// the config sets them. Everything after this (the prompt, the log, the mount) uses the resolved
// path, so a link cannot point elsewhere than what was checked and shown.
func (s *server) mediaPath(given string) (src mediaSource, size int64, err error) {
	p := strings.TrimSpace(given)
	if err := refuseWindowsAlias(p); err != nil {
		return mediaSource{}, 0, err
	}
	if p == "" || !filepath.IsAbs(p) {
		return mediaSource{}, 0, refuse("path must be an absolute path of an .iso file or of a folder on the computer that runs openlamt-mcp.")
	}
	p = filepath.Clean(p)
	resolved, err := filepath.EvalSymlinks(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return mediaSource{}, 0, refuse("%s does not exist.", visible(p))
	case err != nil:
		return mediaSource{}, 0, refuse("Cannot read %s: %v", visible(p), err)
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return mediaSource{}, 0, refuse("Cannot read %s: %v", visible(p), err)
	}
	if err := refuseWindowsAlias(resolved); err != nil { // a link that leads to such a path
		return mediaSource{}, 0, err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return mediaSource{}, 0, refuse("Cannot read %s: %v", visible(resolved), err)
	}
	// On Windows a FileInfo from os.Stat reads the file ID only when it is first compared, by
	// path, so a later comparison would see a swapped file: compare it once now to record it.
	os.SameFile(fi, fi)
	switch {
	case fi.IsDir():
		src = mediaSource{path: resolved, folder: true, checked: fi}
	case fi.Mode().IsRegular() && strings.EqualFold(filepath.Ext(resolved), ".iso"):
		src, size = mediaSource{path: resolved, checked: fi}, fi.Size()
	default:
		return mediaSource{}, 0, refuse("path must be an .iso file or a folder.")
	}
	src.protected = s.protectedDirs()
	if err := s.mediaAllowed(src); err != nil {
		return mediaSource{}, 0, err
	}
	return src, size, nil
}

// sizeText is a file size for a prompt.
func sizeText(n int64) string {
	if n < 100_000 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// mountPrompt is what the person confirming sees before media_mount runs: whether a file or a
// folder is mounted and its full resolved path (and the path as given when a link led there),
// so the agent cannot mount another file than the one shown, and, when the call may do it
// (enable), that IDE-R is turned on.
func mountPrompt(src mediaSource, given string, size int64, enable bool) string {
	shown := "«" + visible(src.path) + "»"
	if g := filepath.Clean(strings.TrimSpace(given)); g != src.path {
		shown += " (given as «" + visible(g) + "»)"
	}
	what := fmt.Sprintf("mount the ISO file %s (%s) as a read-only CD drive", shown, sizeText(size))
	if src.folder {
		what = fmt.Sprintf("mount the folder %s as a read-only CD drive (OpenLAMT builds an ISO image of it first)", shown)
	}
	if !enable {
		return what
	}
	return what + "; media redirection (IDE-R) is turned on on the device if it is off and stays on"
}

func (s *server) mediaMount(ctx context.Context, req *mcp.CallToolRequest, in mediaMountInput, rec *record) (*mcp.CallToolResult, mediaMountOutput, error) {
	rec.device = in.Device
	code, err := parseConsentCode(in.ConsentCode)
	if err != nil {
		return nil, mediaMountOutput{}, err
	}
	src, size, err := s.mediaPath(in.Path)
	if err != nil {
		return nil, mediaMountOutput{}, err
	}
	// The resolved path, the operator's own; the action log is readable by the operator only.
	rec.action = "iso file " + src.path
	if src.folder {
		rec.action = "folder " + src.path
	}
	if code != "" {
		rec.action += ", with consent code" // never the code itself
	}
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, mediaMountOutput{}, err
	}
	if err := s.media.free(fav); err != nil {
		return nil, mediaMountOutput{}, err
	}
	if res, err := s.confirmMount(req, fav, src, size, mountPrompt(src, in.Path, size, s.mayEnable(true))); res != nil || err != nil {
		return res, mediaMountOutput{}, err
	}
	// The mount question, if any, said that IDE-R is turned on: the answer covers it.
	var out mediaMountOutput
	err = s.media.mount(ctx, fav, src, code, true, func(e *mediaEntry) {
		out = mediaMountOutput{Device: fav.DisplayName(), Name: e.name, Path: e.path, Kind: mediaKind(e.folder), SizeBytes: e.size, Warnings: e.warnings}
		if i := e.info; i != nil {
			out.Files, out.Skipped, out.UEFIBootable = i.Files, i.Skipped, boolPtr(i.Bootable)
		}
	})
	if err != nil {
		return nil, mediaMountOutput{}, err
	}
	if n := len(out.Skipped); n > 0 {
		rec.action += fmt.Sprintf(", %d links or special files skipped", n)
	}
	text := fmt.Sprintf("Mounted «%s» on %q as a read-only CD drive. media_boot_next sets it for the next start, boot_from_iso restarts "+
		"the device from it now. It stays mounted until media_unmount, until the device ends the session, or until this server stops.",
		visible(out.Name), fav.DisplayName())
	if n := len(out.Skipped); n > 0 {
		text += fmt.Sprintf(" %d symbolic links or special files in the folder were left out (see skipped).", n)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
}

func mediaKind(folder bool) string {
	if folder {
		return "folder"
	}
	return "iso"
}

// mountAsks keeps, per device (favKey), the file or folder a media_mount question showed, so
// the confirmed call mounts that one: the same resolved path, kind, size and modification time
// (in the request state) and the same file (os.SameFile), not whatever the path leads to when
// the answer comes back.
type mountAsks struct {
	mu sync.Mutex
	m  map[string]mountAsk
}

type mountAsk struct {
	state   string
	checked os.FileInfo
}

func (a *mountAsks) put(key, state string, checked os.FileInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = map[string]mountAsk{}
	}
	a.m[key] = mountAsk{state: state, checked: checked}
}

// take removes the question kept for key and reports whether it showed checked under state.
func (a *mountAsks) take(key, state string, checked os.FileInfo) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	q, ok := a.m[key]
	delete(a.m, key)
	return ok && q.state == state && os.SameFile(q.checked, checked)
}

// mountSourceState names what a media_mount question shows: the resolved path, the kind, the
// size and the modification time.
func mountSourceState(src mediaSource, size int64) string {
	return fmt.Sprintf("mount %s %d %d %s", mediaKind(src.folder), size, src.checked.ModTime().UnixNano(), src.path)
}

// confirmMount asks before media_mount, when the config wants it, and holds the answer to the
// file or folder the question showed.
func (s *server) confirmMount(req *mcp.CallToolRequest, fav favorites.Favorite, src mediaSource, size int64, what string) (*mcp.CallToolResult, error) {
	if !s.cfg.RequireClientConfirmation {
		return nil, nil
	}
	state := mountSourceState(src, size)
	res, err := s.confirmFor(req, fav, confirmMessage(what, fav), state,
		"The file or folder, or the device, changed while the question was open, so the answer was for something else. "+
			"Nothing was mounted; call media_mount again.")
	switch {
	case err != nil:
		return nil, err
	case res != nil:
		s.mountAsks.put(favKey(fav), state, src.checked)
		return res, nil
	}
	if !s.mountAsks.take(favKey(fav), state, src.checked) {
		return nil, refuse("%s is not the file or folder the question showed (it was replaced while the question was open). "+
			"Nothing was mounted; call media_mount again.", visible(src.path))
	}
	return nil, nil
}

// --- media_unmount and media_status

type mediaUnmountOutput struct {
	Device            string   `json:"device"`
	Unmounted         bool     `json:"unmounted" jsonschema:"false if this server had nothing mounted on the device"`
	BootOptionCleared bool     `json:"boot_option_cleared" jsonschema:"the IDE-R boot option that media_boot_next set was cleared"`
	Warnings          []string `json:"warnings,omitempty"`
}

func (s *server) mediaUnmount(_ context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, mediaUnmountOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, mediaUnmountOutput{}, err
	}
	r, err := s.media.unmount(fav)
	if err != nil {
		return nil, mediaUnmountOutput{}, err
	}
	out := mediaUnmountOutput{Device: fav.DisplayName(), Unmounted: r.unmounted, BootOptionCleared: r.cleared}
	if r.warning != "" {
		out.Warnings = append(out.Warnings, r.warning)
	}
	return nil, out, nil
}

type mediaStatusOutput struct {
	Device         string  `json:"device"`
	Mounted        bool    `json:"mounted"`
	Mounting       bool    `json:"mounting" jsonschema:"a media_mount call is preparing or mounting an image"`
	Name           string  `json:"name,omitempty"`
	Path           string  `json:"path,omitempty"`
	Kind           string  `json:"kind,omitempty" jsonschema:"iso or folder"`
	SizeBytes      int64   `json:"size_bytes,omitempty"`
	BytesRead      int64   `json:"bytes_read" jsonschema:"image bytes the device read"`
	ReadRateMBs    float64 `json:"read_rate_mb_s" jsonschema:"MB per second the device read since the previous media_status call (the first one: since the mount)"`
	MountedSeconds int64   `json:"mounted_seconds"`
	LastRead       string  `json:"last_read,omitempty" jsonschema:"RFC 3339, UTC; left out before the first read"`
	BootNext       bool    `json:"boot_next" jsonschema:"media_boot_next set the IDE-R boot option"`
	PendingConsent bool    `json:"pending_consent" jsonschema:"an image waits for the media_mount call with consent_code"`
}

func (s *server) mediaStatus(_ context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, mediaStatusOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, mediaStatusOutput{}, err
	}
	m := s.media
	out := mediaStatusOutput{Device: fav.DisplayName()}
	err = m.inspect(fav, func(e *mediaEntry, pending bool) {
		out.PendingConsent = pending
		switch {
		case e == nil:
			return
		case e.mounting:
			out.Mounting = true
			return
		case !e.alive():
			return
		}
		now, st := m.now(), e.client.Stats()
		out.Mounted, out.Name, out.Path, out.Kind, out.SizeBytes = true, e.name, e.path, mediaKind(e.folder), e.size
		out.BytesRead, out.BootNext = st.BytesRead, e.bootNext
		out.MountedSeconds = int64(now.Sub(e.mountedAt) / time.Second)
		if !st.LastRead.IsZero() {
			out.LastRead = st.LastRead.UTC().Format(time.RFC3339)
		}
		// The rate since the previous sample, in MB/s with one decimal like the app's status bar.
		if dt := now.Sub(e.sampleAt).Seconds(); dt > 0 && st.BytesRead >= e.sampleBytes {
			out.ReadRateMBs = math.Round(float64(st.BytesRead-e.sampleBytes)/dt/1e5) / 10
		}
		e.sampleAt, e.sampleBytes = now, st.BytesRead
	})
	if err != nil {
		return nil, mediaStatusOutput{}, err
	}
	return nil, out, nil
}

// --- media_boot_next and boot_from_iso

type mediaBootNextInput struct {
	Device  string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Enabled bool   `json:"enabled" jsonschema:"true: boot from the mounted image at the next start; false: clear that again"`
}

type mediaBootNextOutput struct {
	Device   string   `json:"device"`
	BootNext bool     `json:"boot_next"`
	Warnings []string `json:"warnings,omitempty"`
}

// mountedRef is the name and number of the mount on the device of fav; it refuses without a
// running mount.
func (s *server) mountedRef(fav favorites.Favorite) (name string, gen uint64, err error) {
	err = s.media.mounted(fav, func(e *mediaEntry) error {
		name, gen = e.name, e.gen
		return nil
	})
	return name, gen, err
}

// sameMount refuses when the mount of e is not the one numbered gen that the person confirmed.
func sameMount(e *mediaEntry, gen uint64) error {
	if e.gen != gen {
		return refuse("The mounted image of %q changed meanwhile (now «%s»). Nothing was sent to the device; call the tool again.",
			e.device, visible(e.name))
	}
	return nil
}

// mountState is the request state of a confirmation about the mount numbered gen.
func mountState(gen uint64) string { return fmt.Sprintf("mount %d", gen) }

// staleMount refuses an answer about another mount or device.
const staleMount = "The mounted image or the device changed while the question was open, so the answer was for another image. " +
	"Nothing was sent to the device; call the tool again."

func (s *server) mediaBootNext(ctx context.Context, req *mcp.CallToolRequest, in mediaBootNextInput, rec *record) (*mcp.CallToolResult, mediaBootNextOutput, error) {
	rec.device, rec.action = in.Device, "off"
	if in.Enabled {
		rec.action = "on"
	}
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, mediaBootNextOutput{}, err
	}
	name, gen, err := s.mountedRef(fav)
	if err != nil {
		return nil, mediaBootNextOutput{}, err
	}
	// Clearing takes nothing away the person has to agree to, like the app's check mark.
	if in.Enabled {
		what := fmt.Sprintf("set the device to boot from «%s» at its next start (no restart now)", visible(name))
		if res, err := s.confirmFor(req, fav, confirmMessage(what, fav), mountState(gen), staleMount); res != nil || err != nil {
			return res, mediaBootNextOutput{}, err
		}
	}
	out := mediaBootNextOutput{Device: fav.DisplayName()}
	err = s.media.mounted(fav, func(e *mediaEntry) error {
		if err := sameMount(e, gen); err != nil {
			return err
		}
		sess, _, warnings, err := s.openFav(ctx, fav)
		if err != nil {
			return err
		}
		defer sess.Close()
		out.Warnings = warnings
		if in.Enabled {
			e.bootNext = true // also after a failure: the option may be set, the unmount clears it
			if err := sess.NextBootFromISO(); err != nil {
				return err
			}
		} else {
			if err := sess.ClearISOBoot(); err != nil {
				return err
			}
			e.bootNext = false
		}
		out.BootNext = e.bootNext
		return nil
	})
	if err != nil {
		return nil, mediaBootNextOutput{}, err
	}
	return nil, out, nil
}

func (s *server) bootFromISO(ctx context.Context, req *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, bootOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, bootOutput{}, err
	}
	name, gen, err := s.mountedRef(fav)
	if err != nil {
		return nil, bootOutput{}, err
	}
	rec.action = name
	what := fmt.Sprintf("restart and boot once from «%s» (a running device is reset; unsaved work is lost)", visible(name))
	if res, err := s.confirmFor(req, fav, confirmMessage(what, fav), mountState(gen), staleMount); res != nil || err != nil {
		return res, bootOutput{}, err
	}
	var out bootOutput
	err = s.media.mounted(fav, func(e *mediaEntry) error {
		if err := sameMount(e, gen); err != nil {
			return err
		}
		sess, st, warnings, err := s.openFav(ctx, fav)
		if err != nil {
			return err
		}
		defer sess.Close()
		// The app's rule (powerAllowed(BootToISO)): a device read as off is only powered on.
		if !isOff(st) && !s.cfg.Enabled(GroupDestructive) {
			return s.destructiveRefused("Boot from ISO on a device that is not off")
		}
		// A reset keeps a KVM session open (see endsKVM), so it is not closed first.
		if err := sess.Power(amt.BootToISO); err != nil {
			return err
		}
		result := "The device accepted the request: it resets and boots once from " + visible(e.name) + "."
		if isOff(st) {
			result = "The device accepted the request: it powers on and boots once from " + visible(e.name) + "."
		}
		out = bootOutput{Device: fav.DisplayName(), PowerStateBefore: powerStateName(st), Result: result, Warnings: warnings}
		return nil
	})
	if err != nil {
		return nil, bootOutput{}, err
	}
	return nil, out, nil
}
