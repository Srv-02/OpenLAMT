package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	auditlog "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/auditlog"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Settings of the AMT security audit log (issue #15): whether it records, what happens when it is
// full, which events the policy records, and clearing it. The library only reads the records, so
// the requests are built here (Intel AMT SDK: AMT_AuditLog, AMT_AuditPolicyRule).
//
// The audit lock (AMT_AuditLog.SetAuditLock) is used only inside ClearAuditLog, as MeshCommander
// does: AMT clears the log only while it is locked. A lock holds at most 5 minutes and, while it
// holds, AMT records nothing and refuses critical actions, so it is never offered on its own.

const (
	classAuditLog    = "AMT_AuditLog"
	classAuditPolicy = "AMT_AuditPolicyRule"

	auditDisabledBit = 0x01 // AMT_AuditLog.AuditState: Disabled
	auditStateOff    = 3    // EnabledState / RequestedState: Disabled
	auditStateOn     = 2    // EnabledState / RequestedState: Enabled

	auditLockFlag   = 0  // SetAuditLock Flag AUDIT_LOG_LOCK
	auditUnlockFlag = 2  // SetAuditLock Flag AUDIT_LOG_UNLOCK
	auditLockSecs   = 30 // the lock around ClearLog; AMT allows at most 300
	auditBulkChunk  = 20 // events per SetAuditPolicyBulk call (Intel: keep a request under 6 kB)

	// AuditMinDays and AuditMaxDays bound MinDaysToKeep (Intel: 1 to 100).
	AuditMinDays = 1
	AuditMaxDays = 100
)

// AuditStorage is AMT_AuditLog.StoragePolicy: what the log does when it is full.
type AuditStorage int

const (
	// AuditStopWhenFull (NO_ROLL_OVER) keeps every record; AMT refuses critical actions while
	// the log is full.
	AuditStopWhenFull AuditStorage = 0
	// AuditOverwriteOldest (ROLL_OVER, the default) overwrites the oldest records.
	AuditOverwriteOldest AuditStorage = 1
	// AuditOverwriteAfterDays (RESTRICTED_ROLL_OVER) overwrites only records older than
	// MinDaysToKeep days.
	AuditOverwriteAfterDays AuditStorage = 2
)

// AuditEvent is one auditable event: the application (event group, e.g. 17 Remote Control) and
// the event within it.
type AuditEvent struct {
	App, Event int
}

// AuditSettings is the state of the audit log as AMT reports it.
type AuditSettings struct {
	Recording     bool         // the log records events (RequestStateChange Enabled)
	Storage       AuditStorage // when the log is full
	MinDaysToKeep int          // with AuditOverwriteAfterDays, else 0
	Records       int          // CurrentNumberOfRecords
	PercentUsed   int          // 100 - PercentageFree
	Policy        []AuditEvent // the events the policy records, sorted
}

// Recorded reports whether the policy records e.
func (s AuditSettings) Recorded(e AuditEvent) bool { return slices.Contains(s.Policy, e) }

// KnownAuditEvents returns the events the library names for app (application id), sorted.
func KnownAuditEvents(app int) []AuditEvent {
	var out []AuditEvent
	for k := range auditlog.AMTAuditLogEventToString {
		if k/100 == app {
			out = append(out, AuditEvent{App: app, Event: k % 100})
		}
	}
	sortAuditEvents(out)
	return out
}

func sortAuditEvents(e []AuditEvent) {
	slices.SortFunc(e, func(a, b AuditEvent) int {
		if a.App != b.App {
			return a.App - b.App
		}
		return a.Event - b.Event
	})
}

// decodeAuditEventID splits AMT_AuditPolicyRule.AuditApplicationEventID. Intel documents the
// application id in the most significant bits and the event id in the 16 least significant ones;
// both an application id in bits 24-31 and one in bits 16-31 are accepted.
func decodeAuditEventID(v uint32) AuditEvent {
	hi := v >> 16
	if hi > 0xFF {
		hi >>= 8
	}
	return AuditEvent{App: int(hi), Event: int(v & 0xFFFF)}
}

// AuditSettings reads AMT_AuditLog and the audit policy. A missing permission is ErrAccessDenied.
func (d *Device) AuditSettings() (AuditSettings, error) {
	const op = "read audit settings"
	o, err := d.auditLogObject()
	if err != nil {
		return AuditSettings{}, readAuditError(op, err)
	}
	s := AuditSettings{
		Recording:   o.integer("AuditState")&auditDisabledBit == 0 && o.integer("EnabledState") != auditStateOff,
		Storage:     AuditStorage(o.integer("StoragePolicy")),
		Records:     o.integer("CurrentNumberOfRecords"),
		PercentUsed: 100 - o.integer("PercentageFree"),
	}
	if !o.has("StoragePolicy") {
		s.Storage = AuditOverwriteOldest
	}
	if s.Storage == AuditOverwriteAfterDays {
		s.MinDaysToKeep = o.integer("MinDaysToKeep")
	}
	rules, err := d.auditPolicyObjects()
	if err != nil {
		return AuditSettings{}, readAuditError(op, err)
	}
	for _, r := range rules {
		for _, v := range r.strs("AuditApplicationEventID") {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				continue
			}
			if e := decodeAuditEventID(uint32(n)); !slices.Contains(s.Policy, e) {
				s.Policy = append(s.Policy, e)
			}
		}
	}
	sortAuditEvents(s.Policy)
	return s, nil
}

// readAuditError maps a refused read to ErrAccessDenied (AccessDenied, or DestinationUnreachable
// for these classes, which every supported firmware has).
func readAuditError(op string, err error) error {
	if errors.Is(err, ErrAccessDenied) || coreClassDenied(err) {
		return fmt.Errorf("%s: %w", op, ErrAccessDenied)
	}
	return writeError(op, err)
}

func (d *Device) auditLogObject() (*cimObject, error) {
	return getObject(classAuditLog, func() (*client.Message, error) {
		r, err := d.msgs.AMT.AuditLog.Get()
		return r.Message, err
	})
}

func (d *Device) auditPolicyObjects() ([]*cimObject, error) {
	return d.enumerateRawClass(classAuditPolicy)
}

// AuditChange changes the audit settings; nil fields and empty lists stay as they are.
type AuditChange struct {
	Recording     *bool
	Storage       *AuditStorage
	MinDaysToKeep *int         // with AuditOverwriteAfterDays
	Enable        []AuditEvent // events the policy records from now on
	Disable       []AuditEvent // events it no longer records
}

// Risky reports whether c stops the recording (OpAuditSettings).
func (c AuditChange) Risky() bool { return c.Recording != nil && !*c.Recording }

// Validate checks the storage policy and the days.
func (c AuditChange) Validate() error {
	if c.Storage != nil && (*c.Storage < AuditStopWhenFull || *c.Storage > AuditOverwriteAfterDays) {
		return invalid("Storage", "unknown storage policy")
	}
	if c.Storage != nil && *c.Storage == AuditOverwriteAfterDays {
		if c.MinDaysToKeep == nil || *c.MinDaysToKeep < AuditMinDays || *c.MinDaysToKeep > AuditMaxDays {
			return invalid("MinDaysToKeep", "1 to 100 days")
		}
	}
	return nil
}

// SetAuditSettings writes the policy (SetAuditPolicyBulk), the storage policy (Put) and the
// recording state (RequestStateChange), in this order and only what c changes. Switching the
// recording off needs confirm for OpAuditSettings.
func (d *Device) SetAuditSettings(c AuditChange, confirm Confirmation) error {
	const op = "write audit settings"
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Risky() {
		if err := d.confirmed(confirm, OpAuditSettings, "AuditLog.Recording"); err != nil {
			return err
		}
	}
	if err := d.setAuditPolicy(c.Enable, c.Disable); err != nil {
		return writeAuditError(op, err)
	}
	if c.Storage != nil {
		if err := d.setAuditStorage(*c.Storage, c.MinDaysToKeep); err != nil {
			return writeAuditError(op, err)
		}
	}
	if c.Recording != nil {
		state := auditStateOff
		if *c.Recording {
			state = auditStateOn
		}
		inner := fmt.Sprintf("<h:RequestedState>%d</h:RequestedState>", state)
		if err := d.auditInvoke(classAuditLog, "RequestStateChange", inner, nil); err != nil {
			return writeAuditError(op, err)
		}
	}
	return nil
}

// writeAuditError keeps method errors as they are and maps refusals to ErrAccessDenied.
func writeAuditError(op string, err error) error {
	var me *MethodError
	if errors.As(err, &me) {
		return err
	}
	return writeError(op, err)
}

// setAuditPolicy sends the changes in chunks of auditBulkChunk events. AMT keeps its policy as it
// was when a call fails.
func (d *Device) setAuditPolicy(enable, disable []AuditEvent) error {
	type change struct {
		e  AuditEvent
		on bool
	}
	var all []change
	for _, e := range enable {
		all = append(all, change{e, true})
	}
	for _, e := range disable {
		all = append(all, change{e, false})
	}
	for chunk := range slices.Chunk(all, auditBulkChunk) {
		var on, apps, events, types strings.Builder
		for _, c := range chunk {
			fmt.Fprintf(&on, "<h:Enable>%t</h:Enable>", c.on)
			fmt.Fprintf(&apps, "<h:AuditedAppID>%d</h:AuditedAppID>", c.e.App)
			fmt.Fprintf(&events, "<h:EventID>%d</h:EventID>", c.e.Event)
			types.WriteString("<h:PolicyType>0</h:PolicyType>")
		}
		inner := on.String() + apps.String() + events.String() + types.String()
		if err := d.auditInvoke(classAuditPolicy, "SetAuditPolicyBulk", inner, nil); err != nil {
			return err
		}
	}
	return nil
}

// setAuditStorage writes StoragePolicy and, for AuditOverwriteAfterDays, MinDaysToKeep with a
// Put of the AMT_AuditLog instance (AMT takes only these two properties from it).
func (d *Device) setAuditStorage(s AuditStorage, days *int) error {
	o, err := d.auditLogObject()
	if err != nil {
		return err
	}
	cur := AuditStorage(o.integer("StoragePolicy"))
	if o.has("StoragePolicy") && cur == s && (s != AuditOverwriteAfterDays || o.integer("MinDaysToKeep") == *days) {
		return nil
	}
	for _, p := range slices.Clone(o.props) {
		if p.nested { // TimeOfLastRecord: read-only and not writable as text
			o.del(p.name)
		}
	}
	o.set("StoragePolicy", strconv.Itoa(int(s)))
	if s == AuditOverwriteAfterDays {
		o.set("MinDaysToKeep", strconv.Itoa(*days))
	} else {
		o.del("MinDaysToKeep")
	}
	return d.putObject(o, nil)
}

// ClearAuditLog deletes every record of the audit log (OpAuditClear): lock, ClearLog, unlock, as
// MeshCommander's ClearAuditLogEx does. The lock holds auditLockSecs at most; an unlock that
// fails after a successful clear is not reported, the lock then ends by itself.
func (d *Device) ClearAuditLog(confirm Confirmation) error {
	const op = "clear audit log"
	if err := d.confirmed(confirm, OpAuditClear, "AuditLog"); err != nil {
		return err
	}
	var handle int
	lock := fmt.Sprintf("<h:LockTimeoutInSeconds>%d</h:LockTimeoutInSeconds><h:Flag>%d</h:Flag><h:Handle>0</h:Handle>", auditLockSecs, auditLockFlag)
	if err := d.auditInvoke(classAuditLog, "SetAuditLock", lock, &handle); err != nil {
		return writeAuditError(op, err)
	}
	clearErr := d.auditInvoke(classAuditLog, "ClearLog", "", nil)
	unlock := fmt.Sprintf("<h:LockTimeoutInSeconds>0</h:LockTimeoutInSeconds><h:Flag>%d</h:Flag><h:Handle>%d</h:Handle>", auditUnlockFlag, handle)
	_ = d.auditInvoke(classAuditLog, "SetAuditLock", unlock, nil)
	if clearErr != nil {
		return writeAuditError(op, clearErr)
	}
	return nil
}

// auditInvoke calls method of class with the input elements inner (prefix h) and checks its
// ReturnValue; handle, if not nil, receives the output Handle.
func (d *Device) auditInvoke(class, method, inner string, handle *int) error {
	uri := resourceURI(class)
	body := fmt.Sprintf(`<h:%s_INPUT xmlns:h="%s">%s</h:%s_INPUT>`, method, uri, inner, method)
	out, err := d.msgs.Client.Post(envelope(uri+"/"+method, uri, nil, body))
	if err != nil {
		return err
	}
	var res struct {
		Body struct {
			Output struct {
				XMLName     xml.Name
				ReturnValue *int `xml:"ReturnValue"`
				Handle      *int `xml:"Handle"`
			} `xml:",any"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(out, &res); err != nil || res.Body.Output.XMLName.Local != method+"_OUTPUT" || res.Body.Output.ReturnValue == nil {
		return fmt.Errorf("%w: %s_OUTPUT", ErrIncompleteResponse, method)
	}
	if rv := *res.Body.Output.ReturnValue; rv != 0 {
		return &MethodError{Method: class + "." + method, ReturnValue: rv}
	}
	if handle != nil && res.Body.Output.Handle != nil {
		*handle = *res.Body.Output.Handle
	}
	return nil
}
