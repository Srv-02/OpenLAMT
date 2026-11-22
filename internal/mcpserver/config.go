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
)

const (
	// ConfigFileName is the config file in OpenLAMT's config folder (next to favorites.json).
	ConfigFileName = "openlamt-mcp.json"
	// ActionLogFileName is the default action log in the same folder.
	ActionLogFileName = "openlamt-mcp-actions.log"
)

// Group is a tool group the config file switches on or off.
type Group string

const (
	GroupRead        Group = "read"        // status, hardware, logs
	GroupPowerBoot   Group = "power_boot"  // power actions, boot to BIOS
	GroupMedia       Group = "media"       // IDE-R (later cut)
	GroupKVMInput    Group = "kvm_input"   // KVM keyboard and mouse (later cut)
	GroupSettings    Group = "settings"    // AMT settings (later cut)
	GroupUsers       Group = "users"       // AMT accounts (later cut)
	GroupDestructive Group = "destructive" // actions that interrupt a running system
)

// Groups lists every group in the order of the config file.
var Groups = []Group{GroupRead, GroupPowerBoot, GroupMedia, GroupKVMInput, GroupSettings, GroupUsers, GroupDestructive}

// Config is the operator's policy for the MCP server.
type Config struct {
	ReadOnly                  bool
	AllowedDevices            []string // favorite names; empty means all
	Groups                    map[Group]bool
	RequireClientConfirmation bool
	ActionLog                 bool
	ActionLogPath             string
	Source                    string // the file read; empty when the defaults apply
}

// Defaults is the policy without a config file: everything is allowed, the action log is on
// and written to dir.
func Defaults(dir string) Config {
	c := Config{Groups: map[Group]bool{}, ActionLog: true, ActionLogPath: filepath.Join(dir, ActionLogFileName)}
	for _, g := range Groups {
		c.Groups[g] = true
	}
	return c
}

// Enabled reports whether the tools of g may run. read_only leaves only GroupRead.
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
	} `json:"tool_groups"`
	RequireClientConfirmation *bool `json:"require_client_confirmation"`
	ActionLog                 *struct {
		Enabled *bool   `json:"enabled"`
		Path    *string `json:"path"`
	} `json:"action_log"`
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
			GroupSettings: g.Settings, GroupUsers: g.Users, GroupDestructive: g.Destructive,
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
	c.Source = path
	return c, nil
}
