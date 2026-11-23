package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

type deviceInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
}

// --- list_devices

type deviceInfo struct {
	Name              string   `json:"name"`
	Host              string   `json:"host"`
	Username          string   `json:"username"`
	TLS               bool     `json:"tls"`
	CertificatePinned bool     `json:"certificate_pinned" jsonschema:"TLS only: the certificate was pinned in the OpenLAMT app; without a pin a TLS device is refused"`
	PasswordStored    bool     `json:"password_stored" jsonschema:"a password is in the keychain; without one the device cannot be used"`
	Warnings          []string `json:"warnings,omitempty"`
}

type listDevicesOutput struct {
	Devices []deviceInfo `json:"devices"`
}

func (s *server) listDevices(_ context.Context, _ *mcp.CallToolRequest, _ struct{}, _ *record) (*mcp.CallToolResult, listDevicesOutput, error) {
	list, err := s.favs.List()
	if err != nil {
		return nil, listDevicesOutput{}, fmt.Errorf("read the OpenLAMT favorites: %w", err)
	}
	count := map[string]int{}
	for _, f := range list {
		count[f.DisplayName()]++
	}
	out := listDevicesOutput{Devices: []deviceInfo{}}
	for _, f := range list {
		name := f.DisplayName()
		if !s.cfg.DeviceAllowed(name) {
			continue
		}
		d := deviceInfo{Name: name, Host: f.Host, Username: f.Username, TLS: f.UseTLS}
		pw, err := s.keys.Get(f.Host, f.Username)
		d.PasswordStored = err == nil && pw != ""
		keyErr := err
		if f.UseTLS {
			fp, err := s.keys.GetFingerprint(f.Host, f.Username)
			d.CertificatePinned = err == nil && fp != ""
			if keyErr == nil || errors.Is(keyErr, keychain.ErrNotFound) {
				keyErr = err
			}
		} else {
			d.Warnings = append(d.Warnings, noTLSWarning)
		}
		if keyErr != nil && !errors.Is(keyErr, keychain.ErrNotFound) {
			d.Warnings = append(d.Warnings, explain(keychainError(keyErr)))
		}
		if count[name] > 1 {
			d.Warnings = append(d.Warnings, "Another favorite has the same name: give them distinct names in the OpenLAMT app to use them here.")
		}
		out.Devices = append(out.Devices, d)
	}
	return nil, out, nil
}

// --- device_status

type statusOutput struct {
	Device                    string   `json:"device"`
	PowerState                string   `json:"power_state" jsonschema:"on, sleep_light, sleep_deep, hibernate, off_soft, off_hard, other or unknown"`
	PowerStateCode            int      `json:"power_state_code" jsonschema:"CIM power state as AMT reports it (2 on, 8 off soft, 6 off hard, ...); 0 if unknown"`
	AMTVersion                string   `json:"amt_version"`
	ControlMode               string   `json:"control_mode" jsonschema:"ACM (admin control mode), CCM (client control mode: user consent needed for redirection and boot options) or unknown"`
	TLS                       bool     `json:"tls"`
	GracefulShutdownSupported bool     `json:"graceful_shutdown_supported" jsonschema:"power_action shutdown is available (AMT offers it only while the Intel Local Manageability Service runs in the operating system)"`
	NotPermitted              []string `json:"not_permitted,omitempty" jsonschema:"parts the account may not read"`
	Warnings                  []string `json:"warnings,omitempty"`
}

// powerStateName returns the name of a power state (CIM_AssociatedPowerManagementService).
func powerStateName(st amt.Status) string {
	if !st.Allowed(amt.StatusPower) || st.PowerState == 0 {
		return "unknown"
	}
	switch st.PowerState {
	case models.PowerStateOn:
		return "on"
	case models.PowerStateSleepLight:
		return "sleep_light"
	case models.PowerStateSleepDeep:
		return "sleep_deep"
	case models.PowerStateHibernate:
		return "hibernate"
	case models.PowerStateOffSoft:
		return "off_soft"
	case models.PowerStateOffHard:
		return "off_hard"
	}
	return "other"
}

func powerKnown(st amt.Status) bool { return st.Allowed(amt.StatusPower) && st.PowerState != 0 }

func isOff(st amt.Status) bool {
	return powerKnown(st) && (st.PowerState == models.PowerStateOffHard || st.PowerState == models.PowerStateOffSoft)
}

func isOn(st amt.Status) bool { return powerKnown(st) && st.PowerState == models.PowerStateOn }

func (s *server) deviceStatus(ctx context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, statusOutput, error) {
	rec.device = in.Device
	sess, st, fav, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, statusOutput{}, err
	}
	defer sess.Close()
	out := statusOutput{Device: in.Device, PowerState: powerStateName(st), AMTVersion: st.AMTVersion,
		ControlMode: string(st.ControlMode), TLS: fav.UseTLS, Warnings: warnings}
	if powerKnown(st) {
		out.PowerStateCode = int(st.PowerState)
	}
	if out.ControlMode == "" {
		out.ControlMode = string(amt.ControlModeUnknown)
	}
	for _, p := range []struct {
		part amt.StatusPart
		name string
	}{{amt.StatusPower, "power_state"}, {amt.StatusVersion, "amt_version"}, {amt.StatusControlMode, "control_mode"}} {
		if !st.Allowed(p.part) {
			out.NotPermitted = append(out.NotPermitted, p.name)
		}
	}
	states, err := sess.PowerStates()
	switch {
	case err == nil:
		out.GracefulShutdownSupported = slices.Contains(states, power.PowerOffSoftGraceful)
	case errors.Is(err, amt.ErrAccessDenied):
		out.NotPermitted = append(out.NotPermitted, "power_capabilities")
	default:
		out.Warnings = append(out.Warnings, "Cannot read the supported power states: "+explain(err))
	}
	return nil, out, nil
}

// --- hardware_info

type hwSystem struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Version      string `json:"version,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	ChassisType  string `json:"chassis_type,omitempty"`
	UUID         string `json:"uuid,omitempty"`
}

type hwBoard struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Version      string `json:"version,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
}

type hwBIOS struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	Version      string `json:"version,omitempty"`
	ReleaseDate  string `json:"release_date,omitempty" jsonschema:"YYYY-MM-DD"`
}

type hwCPU struct {
	Socket          string `json:"socket,omitempty"`
	Name            string `json:"name,omitempty"`
	Manufacturer    string `json:"manufacturer,omitempty"`
	MaxClockMHz     int    `json:"max_clock_mhz,omitempty"`
	CurrentClockMHz int    `json:"current_clock_mhz,omitempty"`
}

type hwMemory struct {
	Bank          string `json:"bank,omitempty"`
	Manufacturer  string `json:"manufacturer,omitempty"`
	PartNumber    string `json:"part_number,omitempty"`
	SerialNumber  string `json:"serial_number,omitempty"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	SpeedMTs      int    `json:"speed_mts,omitempty"`
	Type          string `json:"type,omitempty"`
}

type hwDrive struct {
	ID            string `json:"id"`
	CapacityBytes uint64 `json:"capacity_bytes"`
}

type hwAMT struct {
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
}

type hardwareOutput struct {
	Device   string     `json:"device"`
	System   hwSystem   `json:"system"`
	Board    hwBoard    `json:"board"`
	BIOS     hwBIOS     `json:"bios"`
	CPUs     []hwCPU    `json:"cpus"`
	Memory   []hwMemory `json:"memory"`
	Drives   []hwDrive  `json:"drives"`
	AMT      hwAMT      `json:"amt"`
	Warnings []string   `json:"warnings,omitempty"`
}

func (s *server) hardwareInfo(ctx context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, hardwareOutput, error) {
	rec.device = in.Device
	sess, _, _, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, hardwareOutput{}, err
	}
	defer sess.Close()
	hw, err := sess.HardwareInfo()
	if err != nil {
		return nil, hardwareOutput{}, err
	}
	out := hardwareOutput{
		Device:   in.Device,
		System:   hwSystem(hw.System),
		Board:    hwBoard(hw.Board),
		BIOS:     hwBIOS{Manufacturer: hw.BIOS.Manufacturer, Version: hw.BIOS.Version},
		CPUs:     []hwCPU{},
		Memory:   []hwMemory{},
		Drives:   []hwDrive{},
		AMT:      hwAMT(hw.AMT),
		Warnings: warnings,
	}
	if !hw.BIOS.ReleaseDate.IsZero() {
		out.BIOS.ReleaseDate = hw.BIOS.ReleaseDate.Format(time.DateOnly)
	}
	for _, c := range hw.CPUs {
		out.CPUs = append(out.CPUs, hwCPU(c))
	}
	for _, m := range hw.Memory {
		out.Memory = append(out.Memory, hwMemory(m))
	}
	for _, d := range hw.Drives {
		out.Drives = append(out.Drives, hwDrive(d))
	}
	return nil, out, nil
}

// --- event_log and audit_log

const (
	defaultLogLimit = 50
	maxEventLimit   = 390 // AMT keeps at most 390 event records
	maxAuditLimit   = 1000
)

type eventLogInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum number of entries to return, newest first (default 50, at most 390)"`
	MinSeverity string `json:"min_severity,omitempty" jsonschema:"lowest severity to return: info (default), warning or critical"`
	Since       string `json:"since,omitempty" jsonschema:"only entries at or after this time, RFC 3339 (e.g. 2026-11-20T08:00:00Z)"`
	Contains    string `json:"contains,omitempty" jsonschema:"only entries whose source or description contains this text (case-insensitive)"`
}

func eventLogSchema() *jsonschema.Schema {
	sc, err := jsonschema.For[eventLogInput](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["min_severity"].Enum = []any{"info", "warning", "critical"}
	return sc
}

type eventEntry struct {
	Time        string `json:"time" jsonschema:"RFC 3339, UTC"`
	Severity    string `json:"severity"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description"`
}

type logOutput[E any] struct {
	Device   string   `json:"device"`
	Total    int      `json:"total" jsonschema:"entries read from the device"`
	Matched  int      `json:"matched" jsonschema:"entries that match the filters"`
	Returned int      `json:"returned"`
	Entries  []E      `json:"entries"`
	Warnings []string `json:"warnings,omitempty"`
}

// logFilter checks limit and since, shared by both logs.
func logFilter(limit, max int, since string) (int, time.Time, error) {
	switch {
	case limit == 0:
		limit = defaultLogLimit
	case limit < 0 || limit > max:
		return 0, time.Time{}, refuse("limit must be between 1 and %d.", max)
	}
	var t time.Time
	if since = strings.TrimSpace(since); since != "" {
		var err error
		if t, err = time.Parse(time.RFC3339, since); err != nil {
			return 0, time.Time{}, refuse("since must be an RFC 3339 time such as 2026-11-20T08:00:00Z.")
		}
	}
	return limit, t, nil
}

func containsFold(text string, parts ...string) bool {
	if text == "" {
		return true
	}
	text = strings.ToLower(text)
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), text) {
			return true
		}
	}
	return false
}

func (s *server) eventLog(ctx context.Context, _ *mcp.CallToolRequest, in eventLogInput, rec *record) (*mcp.CallToolResult, logOutput[eventEntry], error) {
	rec.device = in.Device
	var zero logOutput[eventEntry]
	limit, since, err := logFilter(in.Limit, maxEventLimit, in.Since)
	if err != nil {
		return nil, zero, err
	}
	minSev := amt.SeverityInfo
	switch strings.TrimSpace(in.MinSeverity) {
	case "", "info":
	case "warning":
		minSev = amt.SeverityWarning
	case "critical":
		minSev = amt.SeverityCritical
	default:
		return nil, zero, refuse("min_severity must be info, warning or critical.")
	}
	sess, _, _, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, zero, err
	}
	defer sess.Close()
	entries, err := sess.EventLog()
	if err != nil {
		return nil, zero, err
	}
	out := logOutput[eventEntry]{Device: in.Device, Total: len(entries), Entries: []eventEntry{}, Warnings: warnings}
	for _, e := range entries {
		if e.Severity < minSev || !since.IsZero() && e.Time.Before(since) || !containsFold(in.Contains, e.Source, e.Description) {
			continue
		}
		out.Matched++
		if len(out.Entries) < limit {
			out.Entries = append(out.Entries, eventEntry{Time: e.Time.UTC().Format(time.RFC3339), Severity: e.Severity.String(), Source: e.Source, Description: e.Description})
		}
	}
	out.Returned = len(out.Entries)
	return nil, out, nil
}

type auditLogInput struct {
	Device   string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum number of entries to return, newest first (default 50, at most 1000)"`
	Since    string `json:"since,omitempty" jsonschema:"only entries at or after this time, RFC 3339 (e.g. 2026-11-20T08:00:00Z)"`
	Contains string `json:"contains,omitempty" jsonschema:"only entries whose application, event, initiator, address or details contain this text (case-insensitive)"`
}

type auditEntry struct {
	Time      string `json:"time" jsonschema:"RFC 3339, UTC"`
	App       string `json:"app,omitempty"`
	Event     string `json:"event,omitempty"`
	AppID     int    `json:"app_id"`
	EventID   int    `json:"event_id"`
	Initiator string `json:"initiator,omitempty" jsonschema:"AMT user name, Kerberos SID, local or KVM default port"`
	Address   string `json:"address,omitempty"`
	Details   string `json:"details,omitempty"`
}

// initiatorText names who caused an audit record; local and KVM records carry no name.
func initiatorText(e amt.AuditLogEntry) string {
	if e.Initiator != "" {
		return e.Initiator
	}
	switch e.InitiatorType {
	case 2:
		return "local"
	case 3:
		return "KVM default port"
	}
	return ""
}

func (s *server) auditLog(ctx context.Context, _ *mcp.CallToolRequest, in auditLogInput, rec *record) (*mcp.CallToolResult, logOutput[auditEntry], error) {
	rec.device = in.Device
	var zero logOutput[auditEntry]
	limit, since, err := logFilter(in.Limit, maxAuditLimit, in.Since)
	if err != nil {
		return nil, zero, err
	}
	sess, _, _, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, zero, err
	}
	defer sess.Close()
	entries, err := sess.AuditLog()
	if err != nil {
		return nil, zero, err
	}
	out := logOutput[auditEntry]{Device: in.Device, Total: len(entries), Entries: []auditEntry{}, Warnings: warnings}
	for _, e := range entries {
		who := initiatorText(e)
		if !since.IsZero() && e.Time.Before(since) || !containsFold(in.Contains, e.App, e.Event, who, e.Address, e.Details) {
			continue
		}
		out.Matched++
		if len(out.Entries) < limit {
			out.Entries = append(out.Entries, auditEntry{Time: e.Time.UTC().Format(time.RFC3339), App: e.App, Event: e.Event,
				AppID: e.AppID, EventID: e.EventID, Initiator: who, Address: e.Address, Details: e.Details})
		}
	}
	out.Returned = len(out.Entries)
	return nil, out, nil
}

// --- power_action and boot_to_bios

var powerActions = map[string]amt.PowerAction{
	"on": amt.PowerOn, "off": amt.PowerOff, "reset": amt.PowerReset, "cycle": amt.PowerCycle, "shutdown": amt.PowerShutdown,
}

type powerInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Action string `json:"action" jsonschema:"on, off (hard power off), reset (hard reset), cycle (hard off, then on) or shutdown (graceful shutdown of the operating system)"`
}

func powerActionSchema() *jsonschema.Schema {
	sc, err := jsonschema.For[powerInput](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["action"].Enum = []any{"on", "off", "reset", "cycle", "shutdown"}
	return sc
}

type powerOutput struct {
	Device           string   `json:"device"`
	Action           string   `json:"action"`
	PowerStateBefore string   `json:"power_state_before"`
	Result           string   `json:"result"`
	Warnings         []string `json:"warnings,omitempty"`
}

// confirmMessage is the question the MCP client shows when it has to confirm.
func confirmMessage(what string, fav favorites.Favorite) string {
	return fmt.Sprintf("The AI agent asks OpenLAMT to %s on device %q (%s). Allow it?", what, fav.DisplayName(), fav.Host)
}

func (s *server) destructiveRefused(what string) error {
	return refuse("%s can interrupt a running system, and the tool group \"destructive\" is off in the openlamt-mcp config.", what)
}

func (s *server) powerAction(ctx context.Context, req *mcp.CallToolRequest, in powerInput, rec *record) (*mcp.CallToolResult, powerOutput, error) {
	rec.device, rec.action = in.Device, in.Action
	action, ok := powerActions[in.Action]
	if !ok {
		return nil, powerOutput{}, refuse("action must be on, off, reset, cycle or shutdown.")
	}
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, powerOutput{}, err
	}
	if action.Destructive() && !s.cfg.Enabled(GroupDestructive) {
		return nil, powerOutput{}, s.destructiveRefused("Power " + in.Action)
	}
	if res, err := s.confirm(req, fav, confirmMessage("run power "+in.Action, fav)); res != nil || err != nil {
		return res, powerOutput{}, err
	}
	sess, st, warnings, err := s.openFav(ctx, fav)
	if err != nil {
		return nil, powerOutput{}, err
	}
	defer sess.Close()
	// The rules of the device window's Power menu (ui.DeviceWindow.powerAllowed).
	switch {
	case isOn(st) && action == amt.PowerOn:
		return nil, powerOutput{}, refuse("The device is already on.")
	case isOff(st) && action != amt.PowerOn:
		return nil, powerOutput{}, refuse("The device is off (%s): only power on and boot_to_bios apply.", powerStateName(st))
	}
	if action == amt.PowerShutdown {
		states, err := sess.PowerStates()
		if err != nil || !slices.Contains(states, power.PowerOffSoftGraceful) {
			return nil, powerOutput{}, refuse("The device does not offer a graceful shutdown right now. AMT offers it only while the " +
				"Intel Local Manageability Service runs in the operating system. Use off for a hard power off.")
		}
	}
	// AMT refuses off, cycle and shutdown while KVM is open (ReturnValue 2), like the app's
	// endsKVM: close the session first. Its entry stays, so the next KVM call opens it again.
	kvmClosed := endsKVM(action) && s.kvm.closeForPower(fav)
	if err := sess.Power(action); err != nil {
		return nil, powerOutput{}, err
	}
	result := "The device accepted the request (AMT ReturnValue 0). Check device_status for the new state."
	if kvmClosed {
		result += " The KVM session was closed first, because AMT refuses this action while KVM is open; the next KVM tool call opens it again."
	}
	return nil, powerOutput{Device: fav.DisplayName(), Action: in.Action, PowerStateBefore: powerStateName(st),
		Result: result, Warnings: warnings}, nil
}

// endsKVM reports whether AMT refuses action while a KVM session is open: measured for off and
// cycle, shutdown as a precaution (see ui.endsKVM). Reset and boot to BIOS keep the session.
func endsKVM(action amt.PowerAction) bool {
	return action == amt.PowerOff || action == amt.PowerCycle || action == amt.PowerShutdown
}

type bootInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	ConsentCode string `json:"consent_code,omitempty" jsonschema:"six-digit user consent code the device shows on its screen; leave it out on the first call"`
}

type bootOutput struct {
	Device           string   `json:"device"`
	PowerStateBefore string   `json:"power_state_before"`
	Result           string   `json:"result"`
	Warnings         []string `json:"warnings,omitempty"`
}

func (s *server) bootToBIOS(ctx context.Context, req *mcp.CallToolRequest, in bootInput, rec *record) (*mcp.CallToolResult, bootOutput, error) {
	rec.device = in.Device
	code, err := parseConsentCode(in.ConsentCode)
	if err != nil {
		return nil, bootOutput{}, err
	}
	if code != "" {
		rec.action = "with consent code" // never the code itself
	}
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, bootOutput{}, err
	}
	if res, err := s.confirm(req, fav, confirmMessage("boot into BIOS setup (a running device is reset)", fav)); res != nil || err != nil {
		return res, bootOutput{}, err
	}
	sess, st, warnings, err := s.openFav(ctx, fav)
	if err != nil {
		return nil, bootOutput{}, err
	}
	defer sess.Close()
	// On a device read as off it only powers on, like the app's question rule (onlyPowersOn).
	if !isOff(st) && !s.cfg.Enabled(GroupDestructive) {
		return nil, bootOutput{}, s.destructiveRefused("Boot to BIOS on a device that is not off")
	}
	if err := sess.BootToBIOS(code); err != nil {
		return nil, bootOutput{}, err
	}
	result := "The device accepted the request: it resets into BIOS setup."
	if isOff(st) {
		result = "The device accepted the request: it powers on into BIOS setup."
	}
	return nil, bootOutput{Device: fav.DisplayName(), PowerStateBefore: powerStateName(st), Result: result, Warnings: warnings}, nil
}
