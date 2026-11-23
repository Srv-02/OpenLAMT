package amt

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Agent presence watchdogs (issue #19): an agent in the host's operating system sends
// heartbeats to Intel AMT (AMT_AgentPresenceWatchdog, created per agent ID). On a state
// transition AMT writes an event log entry and switches a System Defense policy on or off
// (AddAction; the policy is the one AMT_AgentPresenceInterfacePolicy links to the wired port,
// shared by every watchdog). OpenLAMT only sets watchdogs up; it runs no agent.
//
// Intel documents the classes for AMT 3.0 to 11.0. AMT has no Put for a watchdog: a change is
// deleting and creating it again. The requests follow MeshCommander (AddWatchdog,
// AMT_AgentPresenceWatchdog_AddAction, PullWatchdog).

const (
	classWatchdog       = "AMT_AgentPresenceWatchdog"
	classWatchdogAction = "AMT_AgentPresenceWatchdogAction"
	classTransition     = "AMT_StateTransitionCondition"
	classDependency     = "CIM_ConcreteDependency"
	classAgentPolicy    = "AMT_AgentPresenceInterfacePolicy"

	// MaxAgentName is the longest agent name (MonitoredEntityDescription, MaxLen 16).
	MaxAgentName = 16
	// MaxWatchdogSeconds is the longest startup and heartbeat timeout (uint16 seconds).
	MaxWatchdogSeconds = 65535
)

// WatchdogState is the bitmask of the watchdog's CurrentState and of a transition.
type WatchdogState int

const (
	WatchdogNotStarted WatchdogState = 1
	WatchdogStopped    WatchdogState = 2
	WatchdogRunning    WatchdogState = 4
	WatchdogExpired    WatchdogState = 8
	WatchdogSuspended  WatchdogState = 16
)

// The transitions OpenLAMT sets up: heartbeats stop (not started or running to expired) and
// heartbeats start again (not started, stopped or expired to running).
const (
	stopFrom  = WatchdogNotStarted | WatchdogRunning
	stopTo    = WatchdogExpired
	startFrom = WatchdogNotStarted | WatchdogStopped | WatchdogExpired
	startTo   = WatchdogRunning
)

// PolicySwitch is the System Defense part of a watchdog action (AddAction ActionSd).
type PolicySwitch int

const (
	PolicyKeep       PolicySwitch = iota // no ActionSd
	PolicyActivate                       // ActionSd 0
	PolicyDeactivate                     // ActionSd 1
)

// WatchdogActions is what happens on one kind of transition.
type WatchdogActions struct {
	Event  bool // an event log entry
	Policy PolicySwitch
}

func (a WatchdogActions) any() bool { return a.Event || a.Policy != PolicyKeep }

// Watchdog is one agent presence watchdog.
type Watchdog struct {
	ID        string // agent ID as GUID text, "5f1c2a9e-0d3b-4e57-9a61-2c8b7e4d1f03"
	Name      string // MonitoredEntityDescription; may be empty
	Startup   int    // seconds until the first heartbeat
	Heartbeat int    // seconds between heartbeats
	State     WatchdogState
	OnStop    WatchdogActions // transitions into Expired
	OnStart   WatchdogActions // transitions into Running

	deviceID string // DeviceID as the device returned it (base64)
}

// Watchdogs is the agent watchdog card of the System Defense page.
type Watchdogs struct {
	// Offered is false when the device has no AMT_AgentPresenceWatchdog (AMT 12 and later).
	Offered bool
	List    []Watchdog
	// Policy is the ID of the System Defense policy the watchdogs switch; "" for none.
	Policy string
}

// WatchdogSpec is a new watchdog.
type WatchdogSpec struct {
	Name      string // up to MaxAgentName characters; may be empty
	ID        string // agent ID as GUID text (NewAgentID)
	Startup   int    // 1 to MaxWatchdogSeconds
	Heartbeat int    // 1 to MaxWatchdogSeconds
	OnStop    WatchdogActions
	OnStart   WatchdogActions
	// Policy is the ID of the System Defense policy an action switches; required when one does.
	Policy string
}

var (
	// ErrWatchdogExists: a watchdog with this agent ID exists.
	ErrWatchdogExists = errors.New("a watchdog for this agent ID exists")
	// ErrWatchdogNotFound: the device has no watchdog with this agent ID (any more).
	ErrWatchdogNotFound = errors.New("the watchdog does not exist")
)

// WatchdogPolicySharedError: another watchdog switches a different policy; all watchdogs share
// one (AMT_AgentPresenceInterfacePolicy).
type WatchdogPolicySharedError struct{ Policy string }

func (e *WatchdogPolicySharedError) Error() string {
	return fmt.Sprintf("the watchdogs switch the policy %q; all watchdogs share one", e.Policy)
}

// NewAgentID returns a random agent ID (GUID version 4).
func NewAgentID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // in text order
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// guidText formats 16 bytes as GUID text; the bytes are in the layout of a Windows GUID in
// memory (the first three fields little-endian), as MeshCommander's guidToStr reads them.
func guidText(b []byte) string {
	g := slices.Clone(b)
	slices.Reverse(g[0:4])
	slices.Reverse(g[4:6])
	slices.Reverse(g[6:8])
	h := hex.EncodeToString(g)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// guidBytes is the inverse of guidText; it accepts the text with or without hyphens and braces.
func guidBytes(s string) ([]byte, bool) {
	s = strings.NewReplacer("-", "", "{", "", "}", "").Replace(strings.TrimSpace(s))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return nil, false
	}
	slices.Reverse(b[0:4])
	slices.Reverse(b[4:6])
	slices.Reverse(b[6:8])
	return b, true
}

// ValidAgentID reports whether s is a GUID.
func ValidAgentID(s string) bool {
	_, ok := guidBytes(s)
	return ok
}

// Validate checks the spec without device access; it returns a *ValidationError.
func (s WatchdogSpec) Validate() error {
	name := strings.TrimSpace(s.Name)
	switch {
	case utf8.RuneCountInString(name) > MaxAgentName:
		return invalid("Name", fmt.Sprintf("at most %d characters", MaxAgentName))
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return invalid("Name", "must not contain control characters")
	case !ValidAgentID(s.ID):
		return invalid("ID", "a GUID")
	case s.Startup < 1 || s.Startup > MaxWatchdogSeconds:
		return invalid("Startup", "1 to 65535 seconds")
	case s.Heartbeat < 1 || s.Heartbeat > MaxWatchdogSeconds:
		return invalid("Heartbeat", "1 to 65535 seconds")
	case (s.OnStop.Policy != PolicyKeep || s.OnStart.Policy != PolicyKeep) && s.Policy == "":
		return invalid("Policy", "choose the policy the watchdog switches")
	}
	return nil
}

// switchesPolicy reports whether an action of w switches the shared policy.
func (w Watchdog) switchesPolicy() bool {
	return w.OnStop.Policy != PolicyKeep || w.OnStart.Policy != PolicyKeep
}

// GetWatchdogs reads the watchdogs with their actions and the policy they switch. A firmware
// without AMT_AgentPresenceWatchdog yields Offered false and no error.
func (d *Device) GetWatchdogs() (Watchdogs, error) {
	const op = "read agent watchdogs"
	objs, offered, err := d.optionalClass(classWatchdog)
	if err != nil {
		return Watchdogs{}, writeError(op, err)
	}
	if !offered {
		return Watchdogs{}, nil
	}
	w := Watchdogs{Offered: true}
	for _, o := range objs {
		raw := o.str("DeviceID")
		id := raw
		if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 16 {
			id = guidText(b)
		}
		w.List = append(w.List, Watchdog{ID: id, deviceID: raw, Name: o.str("MonitoredEntityDescription"),
			Startup: o.integer("StartupInterval"), Heartbeat: o.integer("TimeoutInterval"), State: WatchdogState(o.integer("CurrentState"))})
	}
	if len(w.List) > 0 {
		if err := d.watchdogActions(w.List); err != nil {
			return Watchdogs{}, writeError(op, err)
		}
	}
	links, err := d.pullLinks(classAgentPolicy)
	if err != nil && !isMissingClass(err) {
		return Watchdogs{}, writeError(op, err)
	}
	for _, l := range links {
		if l.Antecedent.selector("DeviceID") == wiredPortID {
			w.Policy = l.Dependent.selector("InstanceID")
		}
	}
	return w, nil
}

// watchdogActions reads the transitions and actions and the links between them
// (CIM_ConcreteDependency: watchdog → AMT_StateTransitionCondition → action) into list.
func (d *Device) watchdogActions(list []Watchdog) error {
	conds, err := d.pullObjectsRaw(classTransition)
	if err != nil {
		return err
	}
	acts, err := d.pullObjectsRaw(classWatchdogAction)
	if err != nil {
		return err
	}
	links, err := d.pullLinks(classDependency)
	if err != nil {
		return err
	}
	condByName := map[string]*cimObject{}
	for _, c := range conds {
		condByName[c.str("PolicyConditionName")] = c
	}
	actByName := map[string]*cimObject{}
	for _, a := range acts {
		actByName[a.str("PolicyActionName")] = a
	}
	condActions := map[string][]*cimObject{} // condition name → actions
	agentConds := map[string][]string{}      // DeviceID → condition names
	for _, l := range links {
		switch {
		case l.Antecedent.class() == classWatchdog || l.Antecedent.selector("CreationClassName") == classWatchdog:
			id := l.Antecedent.selector("DeviceID")
			agentConds[id] = append(agentConds[id], l.Dependent.selector("PolicyConditionName"))
		case l.Antecedent.class() == classTransition:
			name := l.Antecedent.selector("PolicyConditionName")
			if a := actByName[l.Dependent.selector("PolicyActionName")]; a != nil {
				condActions[name] = append(condActions[name], a)
			}
		}
	}
	for i := range list {
		for _, cn := range agentConds[list[i].deviceID] {
			c := condByName[cn]
			if c == nil {
				continue
			}
			var target *WatchdogActions
			switch to := WatchdogState(c.integer("NewState")); {
			case to&WatchdogExpired != 0:
				target = &list[i].OnStop
			case to&WatchdogRunning != 0:
				target = &list[i].OnStart
			default:
				continue
			}
			for _, a := range condActions[cn] {
				target.Event = target.Event || a.boolean("EventOnTransition")
				if a.has("ActionSd") {
					if a.integer("ActionSd") == 1 {
						target.Policy = PolicyDeactivate
					} else {
						target.Policy = PolicyActivate
					}
				}
			}
		}
	}
	return nil
}

// AddWatchdog creates a watchdog and its actions. An agent ID in use is ErrWatchdogExists; a
// different policy than the one other watchdogs switch is *WatchdogPolicySharedError, both
// without a write. A watchdog that activates a policy which drops traffic needs confirm for
// OpDefenseDrop. When an action cannot be added the new watchdog is deleted again.
func (d *Device) AddWatchdog(s WatchdogSpec, confirm Confirmation) error {
	const op = "add agent watchdog"
	if err := s.Validate(); err != nil {
		return err
	}
	raw, _ := guidBytes(s.ID)
	id := base64.StdEncoding.EncodeToString(raw)
	have, err := d.GetWatchdogs()
	if err != nil {
		return err
	}
	if !have.Offered {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	for _, w := range have.List {
		if w.deviceID == id || strings.EqualFold(w.ID, guidText(raw)) {
			return ErrWatchdogExists
		}
	}
	switches := s.OnStop.Policy != PolicyKeep || s.OnStart.Policy != PolicyKeep
	if switches {
		sd, err := d.systemDefense(false)
		if err != nil {
			return err
		}
		if _, ok := sd.Policy(s.Policy); !ok {
			return invalid("Policy", "the device has no such policy")
		}
		if have.Policy != "" && have.Policy != s.Policy && slices.ContainsFunc(have.List, Watchdog.switchesPolicy) {
			name := have.Policy
			if p, ok := sd.Policy(have.Policy); ok {
				name = p.Name
			}
			return &WatchdogPolicySharedError{Policy: name}
		}
		if s.OnStop.Policy == PolicyActivate && sd.Drops(s.Policy) {
			if err := d.confirmed(confirm, OpDefenseDrop, "Policy"); err != nil {
				return err
			}
		}
		if have.Policy != s.Policy {
			if have.Policy != "" {
				if err := d.deletePortLink(classAgentPolicy, have.Policy); err != nil {
					return writeError(op, err)
				}
			}
			if err := d.createPortLink(classAgentPolicy, s.Policy); err != nil {
				return writeError(op, err)
			}
		}
	}
	// The order of MeshCommander's AddWatchdogOk.
	body := prop("CreationClassName", classWatchdog) + prop("DeviceID", id) + prop("StartupInterval", strconv.Itoa(s.Startup)) +
		prop("SystemCreationClassName", cimSystem) + prop("SystemName", amtSystem) + prop("TimeoutInterval", strconv.Itoa(s.Heartbeat)) +
		prop("MonitoredEntityDescription", strings.TrimSpace(s.Name))
	if err := d.createRaw(classWatchdog, body); err != nil {
		return writeError(op, err)
	}
	for _, t := range []struct {
		from, to WatchdogState
		a        WatchdogActions
	}{{stopFrom, stopTo, s.OnStop}, {startFrom, startTo, s.OnStart}} {
		if !t.a.any() {
			continue
		}
		args := prop("OldState", strconv.Itoa(int(t.from))) + prop("NewState", strconv.Itoa(int(t.to))) +
			prop("EventOnTransition", strconv.FormatBool(t.a.Event))
		switch t.a.Policy {
		case PolicyActivate:
			args += prop("ActionSd", "0")
		case PolicyDeactivate:
			args += prop("ActionSd", "1")
		}
		if err := d.invokeRaw(classWatchdog, "AddAction", []selector{{"DeviceID", id}}, args); err != nil {
			_ = d.deleteRaw(classWatchdog, []selector{{"DeviceID", id}})
			return writeError(op, err)
		}
	}
	return nil
}

// DeleteWatchdog deletes the watchdog of agent id (GUID text) with its actions. The UI asks
// first (OpWatchdogDelete).
func (d *Device) DeleteWatchdog(id string) error {
	const op = "delete agent watchdog"
	have, err := d.GetWatchdogs()
	if err != nil {
		return err
	}
	for _, w := range have.List {
		if strings.EqualFold(w.ID, id) {
			return writeError(op, d.deleteRaw(classWatchdog, []selector{{"DeviceID", w.deviceID}}))
		}
	}
	return ErrWatchdogNotFound
}
