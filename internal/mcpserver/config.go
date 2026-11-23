// Package mcpserver is the logic of openlamt-mcp: an MCP server that gives AI agents the AMT
// functions of OpenLAMT for the devices saved as favorites. It has no Fyne dependency.
package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// ConfigFileName is the config file in OpenLAMT's config folder (next to favorites.json).
	ConfigFileName = "openlamt-mcp.json"
	// ActionLogFileName is the default action log in the same folder.
	ActionLogFileName = "openlamt-mcp-actions.log"
	// EnvFileName is the file of plaintext passwords next to the config file; it is read only
	// with passwords.allow_plaintext_passwords.
	EnvFileName = ".env"
)

// Group is a tool group the config file switches on or off.
type Group string

const (
	GroupRead        Group = "read"        // status, hardware, logs
	GroupPowerBoot   Group = "power_boot"  // power actions, boot to BIOS
	GroupMedia       Group = "media"       // IDE-R (later cut)
	GroupKVMInput    Group = "kvm_input"   // keyboard, mouse and text input to the host, over KVM and over the serial console
	GroupSettings    Group = "settings"    // AMT settings (later cut)
	GroupUsers       Group = "users"       // AMT accounts (later cut)
	GroupDestructive Group = "destructive" // actions that interrupt a running system
	GroupFavorites   Group = "favorites"   // network scan and the favorites themselves
)

// Groups lists every group in the order of the config file.
var Groups = []Group{GroupRead, GroupPowerBoot, GroupMedia, GroupKVMInput, GroupSettings, GroupUsers, GroupDestructive, GroupFavorites}

// Config is the operator's policy for the MCP server.
type Config struct {
	ReadOnly                  bool
	AllowedDevices            []string // favorite names; empty means all
	Groups                    map[Group]bool
	RequireClientConfirmation bool
	ActionLog                 bool
	ActionLogPath             string
	KVMIdleTimeout            time.Duration // a KVM session without a tool call this long is closed; 0 = never
	SOLIdleTimeout            time.Duration // the same for a serial console (SOL) session
	MediaAllowedPaths         []string      // folders media_mount may mount from (cleaned, absolute); empty means anywhere
	Screenshot                ScreenshotConfig
	Passwords                 PasswordConfig
	Source                    string // the file read; empty when the defaults apply
}

// PasswordConfig says where the server finds device passwords besides the OS keychain.
type PasswordConfig struct {
	// Command prints the password of a favorite on stdout; nil for none. Its arguments may hold
	// the placeholders {name}, {host} and {user}.
	Command []string
	// AllowPlaintext lets the server read passwords from environment variables and EnvFile.
	AllowPlaintext bool
	EnvFile        string // EnvFileName next to the config file
}

// ScreenshotConfig is the default size and format of kvm_screenshot; a call may override it.
type ScreenshotConfig struct {
	MaxWidth, MaxHeight int    // the image fits into this box, aspect kept, never upscaled
	Format              string // "jpeg" or "png"
	JPEGQuality         int    // 1 to 100
}

// Limits of the screenshot settings, in the config file and in a call.
const (
	minScreenshotSide = 16
	maxScreenshotSide = 8192
)

// DefaultScreenshot suits vision models: the longest side at most 1280 pixels keeps the text
// of a 1920x1080 screen readable and stays below the size at which common models scale an
// image down again; JPEG at quality 80 keeps it near 100 KB.
var DefaultScreenshot = ScreenshotConfig{MaxWidth: 1280, MaxHeight: 1280, Format: "jpeg", JPEGQuality: 80}

// DefaultKVMIdleTimeout closes a KVM session nobody used for five minutes, so the device's
// single KVM slot is free for the app again.
const DefaultKVMIdleTimeout = 5 * time.Minute

// DefaultSOLIdleTimeout does the same for the device's single SOL slot.
const DefaultSOLIdleTimeout = 5 * time.Minute

// check validates a screenshot setting; name prefixes the field names in the error.
func (sc ScreenshotConfig) check(prefix string) error {
	for _, f := range []struct {
		name string
		v    int
	}{{"max_width", sc.MaxWidth}, {"max_height", sc.MaxHeight}} {
		if f.v < minScreenshotSide || f.v > maxScreenshotSide {
			return fmt.Errorf("%s%s must be between %d and %d", prefix, f.name, minScreenshotSide, maxScreenshotSide)
		}
	}
	if sc.Format != "jpeg" && sc.Format != "png" {
		return fmt.Errorf("%sformat must be jpeg or png", prefix)
	}
	if sc.JPEGQuality < 1 || sc.JPEGQuality > 100 {
		return fmt.Errorf("%sjpeg_quality must be between 1 and 100", prefix)
	}
	return nil
}

// Defaults is the policy without a config file: everything is allowed, the action log is on
// and written to dir.
func Defaults(dir string) Config {
	c := Config{Groups: map[Group]bool{}, ActionLog: true, ActionLogPath: filepath.Join(dir, ActionLogFileName),
		KVMIdleTimeout: DefaultKVMIdleTimeout, SOLIdleTimeout: DefaultSOLIdleTimeout, Screenshot: DefaultScreenshot,
		Passwords: PasswordConfig{EnvFile: filepath.Join(dir, EnvFileName)}}
	for _, g := range Groups {
		c.Groups[g] = true
	}
	return c
}

// Enabled reports whether the tools of g may run. read_only leaves only GroupRead (and
// scan_network of GroupFavorites, which only reads; see register).
func (c Config) Enabled(g Group) bool {
	if c.ReadOnly && g != GroupRead {
		return false
	}
	return c.Groups[g]
}

// DeviceAllowed reports whether the favorite name may be used.
func (c Config) DeviceAllowed(name string) bool {
	return len(c.AllowedDevices) == 0 || slices.Contains(c.AllowedDevices, name)
}

// fileConfig is the JSON schema; nil fields keep the default.
type fileConfig struct {
	ReadOnly       *bool    `json:"read_only"`
	AllowedDevices []string `json:"allowed_devices"`
	ToolGroups     *struct {
		Read        *bool `json:"read"`
		PowerBoot   *bool `json:"power_boot"`
		Media       *bool `json:"media"`
		KVMInput    *bool `json:"kvm_input"`
		Settings    *bool `json:"settings"`
		Users       *bool `json:"users"`
		Destructive *bool `json:"destructive"`
		Favorites   *bool `json:"favorites"`
	} `json:"tool_groups"`
	RequireClientConfirmation *bool `json:"require_client_confirmation"`
	ActionLog                 *struct {
		Enabled *bool   `json:"enabled"`
		Path    *string `json:"path"`
	} `json:"action_log"`
	KVM *struct {
		IdleTimeoutSeconds    *int    `json:"idle_timeout_seconds"`
		ScreenshotMaxWidth    *int    `json:"screenshot_max_width"`
		ScreenshotMaxHeight   *int    `json:"screenshot_max_height"`
		ScreenshotFormat      *string `json:"screenshot_format"`
		ScreenshotJPEGQuality *int    `json:"screenshot_jpeg_quality"`
	} `json:"kvm"`
	SOL *struct {
		IdleTimeoutSeconds *int `json:"idle_timeout_seconds"`
	} `json:"sol"`
	Media *struct {
		AllowedPaths []string `json:"allowed_paths"`
	} `json:"media"`
	Passwords *struct {
		Command                 []string `json:"command"`
		AllowPlaintextPasswords *bool    `json:"allow_plaintext_passwords"`
	} `json:"passwords"`
}

// Load reads the config file at path. A missing file yields Defaults of its folder, unless
// mustExist (the file was named with --config). Unknown keys are an error, so a typo cannot
// leave everything allowed.
func Load(path string, mustExist bool) (Config, error) {
	dir := filepath.Dir(path)
	c := Defaults(dir)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !mustExist {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var f fileConfig
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse %s: unexpected data after the JSON object", path)
	}
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&c.ReadOnly, f.ReadOnly)
	set(&c.RequireClientConfirmation, f.RequireClientConfirmation)
	for _, name := range f.AllowedDevices {
		name = strings.TrimSpace(name)
		if name == "" {
			return Config{}, fmt.Errorf("parse %s: allowed_devices holds an empty name", path)
		}
		c.AllowedDevices = append(c.AllowedDevices, name)
	}
	if g := f.ToolGroups; g != nil {
		for group, v := range map[Group]*bool{
			GroupRead: g.Read, GroupPowerBoot: g.PowerBoot, GroupMedia: g.Media, GroupKVMInput: g.KVMInput,
			GroupSettings: g.Settings, GroupUsers: g.Users, GroupDestructive: g.Destructive, GroupFavorites: g.Favorites,
		} {
			if v != nil {
				c.Groups[group] = *v
			}
		}
	}
	if l := f.ActionLog; l != nil {
		set(&c.ActionLog, l.Enabled)
		if l.Path != nil && strings.TrimSpace(*l.Path) != "" {
			p := strings.TrimSpace(*l.Path)
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			c.ActionLogPath = p
		}
	}
	if k := f.KVM; k != nil {
		if v := k.IdleTimeoutSeconds; v != nil {
			if *v < 0 {
				return Config{}, fmt.Errorf("parse %s: kvm.idle_timeout_seconds must be 0 (never) or more", path)
			}
			c.KVMIdleTimeout = time.Duration(*v) * time.Second
		}
		setInt := func(dst *int, v *int) {
			if v != nil {
				*dst = *v
			}
		}
		setInt(&c.Screenshot.MaxWidth, k.ScreenshotMaxWidth)
		setInt(&c.Screenshot.MaxHeight, k.ScreenshotMaxHeight)
		setInt(&c.Screenshot.JPEGQuality, k.ScreenshotJPEGQuality)
		if k.ScreenshotFormat != nil {
			c.Screenshot.Format = strings.ToLower(strings.TrimSpace(*k.ScreenshotFormat))
		}
		if err := c.Screenshot.check("kvm.screenshot_"); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if sl := f.SOL; sl != nil && sl.IdleTimeoutSeconds != nil {
		if v := *sl.IdleTimeoutSeconds; v < 0 {
			return Config{}, fmt.Errorf("parse %s: sol.idle_timeout_seconds must be 0 (never) or more", path)
		}
		c.SOLIdleTimeout = time.Duration(*sl.IdleTimeoutSeconds) * time.Second
	}
	if md := f.Media; md != nil {
		for _, p := range md.AllowedPaths {
			p = strings.TrimSpace(p)
			if p == "" || !filepath.IsAbs(p) {
				return Config{}, fmt.Errorf("parse %s: media.allowed_paths takes absolute folder paths, not %q", path, p)
			}
			c.MediaAllowedPaths = append(c.MediaAllowedPaths, filepath.Clean(p))
		}
	}
	if pw := f.Passwords; pw != nil {
		set(&c.Passwords.AllowPlaintext, pw.AllowPlaintextPasswords)
		if len(pw.Command) > 0 {
			prog := strings.TrimSpace(pw.Command[0])
			if prog == "" || strings.ContainsAny(prog, "{}") {
				return Config{}, fmt.Errorf("parse %s: passwords.command starts with the program to run; it must not be empty or hold a placeholder", path)
			}
			c.Passwords.Command = append([]string{prog}, pw.Command[1:]...)
			if !commandHasHost(c.Passwords.Command) {
				return Config{}, fmt.Errorf("parse %s: passwords.command must contain {host} in an argument, so each password is bound "+
					"to the host it goes to ({name} and {user} may be used in addition)", path)
			}
		}
	}
	c.Source = path
	return c, nil
}
