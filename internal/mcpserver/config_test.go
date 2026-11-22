package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
