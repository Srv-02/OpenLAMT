package mcpserver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func allGroupsOn(t *testing.T, c Config) {
	t.Helper()
	for _, g := range Groups {
		if !c.Enabled(g) {
			t.Errorf("group %s off", g)
		}
	}
}

// TestLoadMissingDefaultFileAllowsEverything: without a config file everything is allowed and
// the action log is on, next to the config file (Michael's decision).
func TestLoadMissingDefaultFileAllowsEverything(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, ConfigFileName), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.ReadOnly || len(c.AllowedDevices) != 0 || c.RequireClientConfirmation || c.Source != "" {
		t.Fatalf("%+v", c)
	}
	allGroupsOn(t, c)
	if !c.DeviceAllowed("anything") {
		t.Fatal("device not allowed")
	}
	if !c.ActionLog || c.ActionLogPath != filepath.Join(dir, ActionLogFileName) {
		t.Fatalf("action log %v %q", c.ActionLog, c.ActionLogPath)
	}
	want := ScreenshotConfig{MaxWidth: 1280, MaxHeight: 1280, Format: "jpeg", JPEGQuality: 80}
	if c.KVMIdleTimeout != 5*time.Minute || c.Screenshot != want {
		t.Fatalf("kvm defaults %v %+v", c.KVMIdleTimeout, c.Screenshot)
	}
	if c.SOLIdleTimeout != 5*time.Minute {
		t.Fatalf("sol idle timeout %v", c.SOLIdleTimeout)
	}
}

// TestLoadExplicitMissingFileFails: a file named with --config must exist.
func TestLoadExplicitMissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json"), true); err == nil {
		t.Fatal("no error")
	}
}

// TestLoadPartialFileKeepsDefaults: a key missing in the file keeps its default.
func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	p := writeConfig(t, `{"tool_groups": {"destructive": false}, "allowed_devices": [" Lab Server "]}`)
	c, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.ReadOnly || c.Enabled(GroupDestructive) || !c.Enabled(GroupPowerBoot) || !c.Enabled(GroupRead) || !c.ActionLog || c.Source != p {
		t.Fatalf("%+v", c)
	}
	if !c.DeviceAllowed("Lab Server") || c.DeviceAllowed("Other") {
		t.Fatalf("allowed %v", c.AllowedDevices)
	}
}

// TestReadOnlyAllowsOnlyReadGroup: read_only wins over tool_groups.
func TestReadOnlyAllowsOnlyReadGroup(t *testing.T) {
	c, err := Load(writeConfig(t, `{"read_only": true, "tool_groups": {"power_boot": true}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range Groups {
		if c.Enabled(g) != (g == GroupRead) {
			t.Errorf("group %s: %v", g, c.Enabled(g))
		}
	}
}

// TestLoadRejectsUnknownKeys: a typo must not leave everything allowed silently.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	for _, body := range []string{
		`{"readonly": true}`,
		`{"tool_groups": {"kvm": false}}`,
		`{"action_log": {"file": "x.log"}}`,
		`{"read_only": "yes"}`,
		`{"allowed_devices": [""]}`,
		`{} {}`,
		`not json`,
		`{"kvm": {"idle_timeout": 5}}`,
		`{"kvm": {"idle_timeout_seconds": -1}}`,
		`{"kvm": {"screenshot_format": "gif"}}`,
		`{"kvm": {"screenshot_jpeg_quality": 0}}`,
		`{"kvm": {"screenshot_max_width": 8}}`,
		`{"kvm": {"screenshot_max_height": 100000}}`,
		`{"sol": {"idle_timeout": 5}}`,
		`{"sol": {"idle_timeout_seconds": -1}}`,
		`{"passwords": {"cmd": ["pass"]}}`,
		`{"passwords": {"command": "pass show x"}}`,
		`{"passwords": {"command": ["", "show"]}}`,
		`{"passwords": {"command": ["{name}"]}}`,
		`{"passwords": {"command": ["pass", "show", "openlamt/{name}"]}}`,
		`{"passwords": {"command": ["/usr/bin/op", "read", "op://lab/{user}/password"]}}`,
		`{"passwords": {"allow_plaintext_passwords": "yes"}}`,
		`{"tool_groups": {"favourites": false}}`,
		`{"certificates": {"pin_file": "yes"}}`,
		`{"certificates": {"pinfile": true}}`,
		`{"certificates": {"pin_file": true, "path": "pins.json"}}`,
		`{"certificate": {"pin_file": true}}`,
	} {
		if _, err := Load(writeConfig(t, body), true); err == nil {
			t.Errorf("%s: no error", body)
		} else if !strings.Contains(err.Error(), ConfigFileName) {
			t.Errorf("%s: error lacks the file: %v", body, err)
		}
	}
}

// TestLoadActionLogSettings: the log can be switched off, and a relative path is relative to
// the folder of the config file.
func TestLoadActionLogSettings(t *testing.T) {
	p := writeConfig(t, `{"action_log": {"path": "logs/actions.log"}}`)
	c, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ActionLog || c.ActionLogPath != filepath.Join(filepath.Dir(p), "logs", "actions.log") {
		t.Fatalf("%v %q", c.ActionLog, c.ActionLogPath)
	}
	abs := filepath.Join(t.TempDir(), "a.log")
	c, err = Load(writeConfig(t, `{"action_log": {"enabled": false, "path": `+quote(abs)+`}}`), true)
	if err != nil || c.ActionLog || c.ActionLogPath != abs {
		t.Fatalf("%v %v %q", err, c.ActionLog, c.ActionLogPath)
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

// TestLoadKVMSettings: idle timeout (0 = never) and the screenshot defaults.
func TestLoadKVMSettings(t *testing.T) {
	c, err := Load(writeConfig(t, `{"kvm": {"idle_timeout_seconds": 0, "screenshot_max_width": 1568, "screenshot_format": "PNG"}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	want := ScreenshotConfig{MaxWidth: 1568, MaxHeight: 1280, Format: "png", JPEGQuality: 80}
	if c.KVMIdleTimeout != 0 || c.Screenshot != want {
		t.Fatalf("%v %+v", c.KVMIdleTimeout, c.Screenshot)
	}
	c, err = Load(writeConfig(t, `{"kvm": {"idle_timeout_seconds": 60, "screenshot_jpeg_quality": 95}}`), true)
	if err != nil || c.KVMIdleTimeout != time.Minute || c.Screenshot.JPEGQuality != 95 {
		t.Fatalf("%v %v %+v", err, c.KVMIdleTimeout, c.Screenshot)
	}
}

// TestLoadSOLSettings: the SOL idle timeout, 0 = never; other keys keep their defaults.
func TestLoadSOLSettings(t *testing.T) {
	c, err := Load(writeConfig(t, `{"sol": {"idle_timeout_seconds": 0}}`), true)
	if err != nil || c.SOLIdleTimeout != 0 || c.KVMIdleTimeout != DefaultKVMIdleTimeout {
		t.Fatalf("%v %v %v", err, c.SOLIdleTimeout, c.KVMIdleTimeout)
	}
	c, err = Load(writeConfig(t, `{"sol": {"idle_timeout_seconds": 60}}`), true)
	if err != nil || c.SOLIdleTimeout != time.Minute {
		t.Fatalf("%v %v", err, c.SOLIdleTimeout)
	}
	c, err = Load(writeConfig(t, `{"sol": {}}`), true)
	if err != nil || c.SOLIdleTimeout != DefaultSOLIdleTimeout {
		t.Fatalf("%v %v", err, c.SOLIdleTimeout)
	}
}

// TestLoadMediaAllowedPaths: media.allowed_paths takes absolute folders; missing or empty means
// no restriction (everything allowed without a config, Michael's default).
func TestLoadMediaAllowedPaths(t *testing.T) {
	c, err := Load(writeConfig(t, `{}`), true)
	if err != nil || c.MediaAllowedPaths != nil {
		t.Fatalf("%v %v", err, c.MediaAllowedPaths)
	}
	abs := filepath.Join(t.TempDir(), "isos")
	body := `{"media": {"allowed_paths": [` + strconv.Quote(abs+string(filepath.Separator)) + `]}}`
	c, err = Load(writeConfig(t, body), true)
	if err != nil || len(c.MediaAllowedPaths) != 1 || c.MediaAllowedPaths[0] != abs {
		t.Fatalf("%v %v", err, c.MediaAllowedPaths)
	}
	for _, bad := range []string{`{"media": {"allowed_paths": ["isos"]}}`, `{"media": {"allowed_paths": [" "]}}`, `{"media": {"paths": []}}`} {
		if _, err := Load(writeConfig(t, bad), true); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// TestLoadPasswordSettings: the password command and the plaintext opt-in; both are off by
// default, the .env file sits next to the config file, and the favorites group can be switched off.
func TestLoadPasswordSettings(t *testing.T) {
	p := writeConfig(t, `{}`)
	c, err := Load(p, true)
	if err != nil || c.Passwords.Command != nil || c.Passwords.AllowPlaintext ||
		c.Passwords.EnvFile != filepath.Join(filepath.Dir(p), EnvFileName) || !c.Enabled(GroupFavorites) {
		t.Fatalf("%v %+v", err, c.Passwords)
	}
	c, err = Load(writeConfig(t, `{"passwords": {"command": [" pass ", "show", "openlamt/{host}"], "allow_plaintext_passwords": true},
		"tool_groups": {"favorites": false}}`), true)
	if err != nil || strings.Join(c.Passwords.Command, "|") != "pass|show|openlamt/{host}" || !c.Passwords.AllowPlaintext || c.Enabled(GroupFavorites) {
		t.Fatalf("%v %+v", err, c.Passwords)
	}
	if c, err = Load(writeConfig(t, `{"passwords": {"command": []}}`), true); err != nil || c.Passwords.Command != nil {
		t.Fatalf("empty command: %v %v", err, c.Passwords.Command)
	}
}

// TestLoadCertificateSettings: certificates.pin_file is an opt-in and off by default.
func TestLoadCertificateSettings(t *testing.T) {
	if c := Defaults(t.TempDir()); c.Certificates.PinFile {
		t.Fatal("on without a config file")
	}
	c, err := Load(writeConfig(t, `{}`), true)
	if err != nil || c.Certificates.PinFile {
		t.Fatalf("default: %v %+v", err, c.Certificates)
	}
	if c, err = Load(writeConfig(t, `{"certificates": {}}`), true); err != nil || c.Certificates.PinFile {
		t.Fatalf("empty object: %v %+v", err, c.Certificates)
	}
	if c, err = Load(writeConfig(t, `{"certificates": {"pin_file": true}}`), true); err != nil || !c.Certificates.PinFile {
		t.Fatalf("on: %v %+v", err, c.Certificates)
	}
}

// TestLoadCaptureFolder: capture.folder takes an absolute folder; without it the field stays
// empty and the server uses OpenLAMT in the home folder.
func TestLoadCaptureFolder(t *testing.T) {
	if c := Defaults(t.TempDir()); c.CaptureFolder != "" {
		t.Fatalf("default %q", c.CaptureFolder)
	}
	for _, body := range []string{`{}`, `{"capture": {}}`, `{"capture": {"folder": "  "}}`} {
		if c, err := Load(writeConfig(t, body), true); err != nil || c.CaptureFolder != "" {
			t.Fatalf("%s: %v %q", body, err, c.CaptureFolder)
		}
	}
	abs := filepath.Join(t.TempDir(), "docs")
	c, err := Load(writeConfig(t, `{"capture": {"folder": `+strconv.Quote(abs+string(filepath.Separator))+`}}`), true)
	if err != nil || c.CaptureFolder != abs {
		t.Fatalf("%v %q", err, c.CaptureFolder)
	}
	for _, bad := range []string{`{"capture": {"folder": "docs"}}`, `{"capture": {"dir": "/x"}}`, `{"capture": {"folder": 5}}`} {
		if _, err := Load(writeConfig(t, bad), true); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// TestLoadCaptureRecordAlways: capture.record_always is off unless the file turns it on (#205);
// it goes with or without capture.folder and takes only a boolean.
func TestLoadCaptureRecordAlways(t *testing.T) {
	for _, body := range []string{`{}`, `{"capture": {}}`, `{"capture": {"record_always": false}}`, `{"capture": {"folder": ""}}`} {
		c, err := Load(writeConfig(t, body), true)
		if err != nil || c.RecordAlways {
			t.Fatalf("%s: record_always %v, %v", body, c.RecordAlways, err)
		}
	}
	abs := t.TempDir()
	c, err := Load(writeConfig(t, `{"capture": {"folder": `+strconv.Quote(abs)+`, "record_always": true}}`), true)
	if err != nil || !c.RecordAlways || c.CaptureFolder != abs {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err = Load(writeConfig(t, `{"capture": {"record_always": true}}`), true); err != nil || !c.RecordAlways || c.CaptureFolder != "" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{`{"capture": {"record_always": "yes"}}`, `{"capture": {"record_always": 1}}`, `{"capture": {"recordAlways": true}}`} {
		if _, err := Load(writeConfig(t, bad), true); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
