package localme

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// "Check" (#11) reads Intel AMT through the Intel ME driver of this computer. The driver needs
// administrator rights, the app never has them: Check starts the app's own program a second
// time, elevated, with HelperFlag. That process only reads, prints a Result as JSON and exits.
// The reading uses the rpc-go library of Intel (a v3 beta, see mei.go) and never starts LMS or
// any network service.

// Schema is the version of the JSON contract between the elevated helper and the app.
const Schema = 1

// maxResultSize bounds the helper's answer; a Result is well below 2 KiB.
const maxResultSize = 64 << 10

// Why Intel AMT was not found. The app shows the same "No Intel AMT found" card for every
// reason; the reason and Detail only help a bug report.
const (
	ReasonNoDriver = "no-driver" // the Intel ME driver or its AMT interface could not be opened
	ReasonNoAMT    = "no-amt"    // the Intel ME answered, but reported no Intel AMT
)

// Control modes of an activated Intel AMT; ModeNone is not activated, "" is unknown.
const (
	ModeNone = "none"
	ModeCCM  = "ccm"
	ModeACM  = "acm"
)

// Link is one network interface as Intel AMT reports it.
type Link struct {
	Enabled bool   `json:"enabled"`
	LinkUp  bool   `json:"linkUp"`
	DHCP    bool   `json:"dhcp"`
	IP      string `json:"ip,omitempty"`
	MAC     string `json:"mac,omitempty"`
}

// Result is the answer of a check. It holds nothing secret: no password and no account of
// Intel AMT is read.
type Result struct {
	Schema int `json:"schema"`
	// Found: Intel AMT answered and reported its version.
	Found  bool   `json:"found"`
	Reason string `json:"reason,omitempty"` // when not found: ReasonNoDriver or ReasonNoAMT
	Detail string `json:"detail,omitempty"` // when not found: the technical error, if any

	Version      string `json:"version,omitempty"`      // "16.1.25"
	TurnedOff    bool   `json:"turnedOff,omitempty"`    // AMT is found but its operational state is off
	ControlMode  string `json:"controlMode,omitempty"`  // ModeNone, ModeCCM, ModeACM; "" unknown
	Provisioning string `json:"provisioning,omitempty"` // "pre", "in", "post"; "" unknown
	UUID         string `json:"uuid,omitempty"`
	DNSSuffix    string `json:"dnsSuffix,omitempty"`
	Wired        *Link  `json:"wired,omitempty"`
	Wireless     *Link  `json:"wireless,omitempty"`

	// Checked is set by the app when the answer arrives; the helper leaves it empty.
	Checked time.Time `json:"checked,omitzero"`
}

// ShortVersion is major.minor of the version ("16.1"), or the version as it is.
func (r Result) ShortVersion() string {
	if p := strings.SplitN(r.Version, ".", 3); len(p) >= 2 {
		return p[0] + "." + p[1]
	}
	return r.Version
}

// Encode is the helper's answer.
func Encode(r Result) ([]byte, error) {
	r.Schema = Schema
	return json.Marshal(r)
}

// Decode reads the helper's answer (and the stored last result).
func Decode(b []byte) (Result, error) {
	var r Result
	if len(b) > maxResultSize {
		return r, errors.New("check answer too large")
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, fmt.Errorf("check answer: %w", err)
	}
	if r.Schema != Schema {
		return Result{}, fmt.Errorf("check answer has schema %d, want %d", r.Schema, Schema)
	}
	return r, nil
}

// reader is what a check asks the Intel ME. It has read methods only: the helper cannot change
// Intel AMT, because nothing it calls can.
type reader interface {
	Open() error                                       // the AMT host interface of the Intel ME is reachable
	Version() (string, error)                          // AMT code version, "16.1.25"
	OperationalState() (supported, on bool, err error) // AMT 12 and later with the new interface
	ControlMode() (int, error)                         // 0 not activated, 1 CCM, 2 ACM
	ProvisioningState() (int, error)                   // 0 pre, 1 in, 2 post
	UUID() (string, error)
	DNSSuffix() (string, error)
	LAN(wireless bool) (Link, error)
}

// read asks r for everything a Result holds. A field that cannot be read stays empty.
func read(r reader) Result {
	res := Result{Schema: Schema}
	if r == nil {
		res.Reason = ReasonNoDriver
		return res
	}
	if err := r.Open(); err != nil {
		res.Reason, res.Detail = ReasonNoDriver, err.Error()
		return res
	}
	v, err := r.Version()
	if err != nil || strings.TrimSpace(v) == "" {
		res.Reason = ReasonNoAMT
		if err != nil {
			res.Detail = err.Error()
		}
		return res
	}
	res.Found, res.Version = true, strings.TrimSpace(v)
	// The operational state exists from AMT 12 on; rpc-go's amtinfo reads it the same way.
	if major, err := strconv.Atoi(strings.SplitN(res.Version, ".", 2)[0]); err == nil && major > 11 {
		if supported, on, err := r.OperationalState(); err == nil && supported {
			res.TurnedOff = !on
		}
	}
	if m, err := r.ControlMode(); err == nil {
		res.ControlMode = map[int]string{0: ModeNone, 1: ModeCCM, 2: ModeACM}[m]
	}
	if p, err := r.ProvisioningState(); err == nil {
		res.Provisioning = map[int]string{0: "pre", 1: "in", 2: "post"}[p]
	}
	if u, err := r.UUID(); err == nil {
		res.UUID = u
	}
	if d, err := r.DNSSuffix(); err == nil {
		res.DNSSuffix = d
	}
	if l, err := r.LAN(false); err == nil {
		res.Wired = &l
	}
	if l, err := r.LAN(true); err == nil {
		res.Wireless = &l
	}
	return res
}
