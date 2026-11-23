package localme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The setup actions of "This computer" (#11): turn Intel AMT on, activate it in Client Control
// Mode and deactivate it. Each one is a short elevated run of the helper with its own flag; the
// helper changes Intel AMT, reads the new state like Check and answers with an ActionResult, so
// one administrator prompt covers the action and the fresh state.

// Action is a setup action; its value is the helper flag that runs it.
type Action string

const (
	ActionTurnOn      Action = "--local-enable"       // turn on the operational state (AMT 16.1 and later)
	ActionActivateCCM Action = "--local-activate-ccm" // activate in Client Control Mode; reads the password
	ActionDeactivate  Action = "--local-deactivate"   // deactivate Client Control Mode (unprovision)
)

// actions are the setup actions the helper accepts.
var actions = []Action{ActionTurnOn, ActionActivateCCM, ActionDeactivate}

// changer is what a setup action asks the Intel ME to change. It is kept apart from reader:
// Check never opens one.
type changer interface {
	TurnOn() error                     // turn on the operational state (HECI only)
	ActivateCCM(password string) error // activate in Client Control Mode with this admin password
	Deactivate() error                 // deactivate Client Control Mode (HECI only)
}

// Setup is the one setup offer of the "This computer" card for a check result.
type Setup int

const (
	SetupNone         Setup = iota // no result, no Intel AMT, or an unknown control mode
	SetupTurnOn                    // AMT is off; the firmware can and may turn it on: "Turn On…"
	SetupTurnOnBIOS                // AMT is off; the firmware does not allow the change now: hint to MEBx
	SetupTurnOnLegacy              // AMT is off and older than 16.1: hint to MEBx
	SetupActivate                  // AMT is on and not activated: "Activate…"
	SetupDeactivate                // Client Control Mode: "Deactivate…"
	SetupACM                       // Admin Control Mode: hint to MEBx, no button
)

// Setup picks the one setup offer for r (design "Ablauf"): off → turn on, on and not activated →
// activate, CCM → deactivate, ACM → the hint to MEBx.
func (r Result) Setup() Setup {
	switch {
	case !r.Found:
		return SetupNone
	case r.TurnedOff && !r.TurnOnSupported:
		return SetupTurnOnLegacy
	case r.TurnedOff && !r.TurnOnAllowed:
		return SetupTurnOnBIOS
	case r.TurnedOff:
		return SetupTurnOn
	}
	switch r.ControlMode {
	case ModeNone:
		return SetupActivate
	case ModeCCM:
		return SetupDeactivate
	case ModeACM:
		return SetupACM
	}
	return SetupNone
}

// ActionResult is the answer of a setup action: why Intel AMT refused the change ("" when it
// was made) and the state read afterwards. ForeignPort is set instead of Refused when another
// program than LMS listened on that LMS port, so the activation never started. It holds no
// password.
type ActionResult struct {
	Schema      int    `json:"schema"`
	Refused     string `json:"refused,omitempty"`
	ForeignPort int    `json:"foreignPort,omitempty"`
	State       Result `json:"state"`
}

// RefusedError is a setup action that Intel AMT did not carry out; Reason is the short
// technical reason.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "intel amt refused the change: " + e.Reason }

// Err is the refusal as an error (*RefusedError or *ForeignLMSError), nil when the change was
// made.
func (a ActionResult) Err() error {
	switch {
	case a.ForeignPort != 0:
		return &ForeignLMSError{Port: a.ForeignPort}
	case a.Refused != "":
		return &RefusedError{Reason: a.Refused}
	}
	return nil
}

// encodeAction is the action helper's answer.
func encodeAction(a ActionResult) ([]byte, error) {
	a.Schema, a.State.Schema = Schema, Schema
	return json.Marshal(a)
}

// decodeAction reads the action helper's answer.
func decodeAction(b []byte) (ActionResult, error) {
	var a ActionResult
	if len(b) > maxResultSize {
		return a, errors.New("action answer too large")
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return ActionResult{}, fmt.Errorf("action answer: %w", err)
	}
	if a.Schema != Schema || a.State.Schema != Schema {
		return ActionResult{}, fmt.Errorf("action answer has schema %d, want %d", a.Schema, Schema)
	}
	return a, nil
}

// maxReason bounds the refusal reason in the answer.
const maxReason = 200

// refusal turns the error of a change into the short reason of the answer: its first line, at
// most maxReason bytes, never the password.
func refusal(err error, secret []byte) string {
	msg := err.Error()
	if line, _, ok := strings.Cut(msg, "\n"); ok {
		msg = line
	}
	if len(secret) > 0 {
		msg = strings.ReplaceAll(msg, string(secret), "***")
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > maxReason {
		msg = msg[:maxReason] + "…"
	}
	if msg == "" {
		msg = "unknown error"
	}
	return msg
}

// Password rules of the AMT admin password set by the activation.
const (
	minPassword = 8
	maxPassword = 32
)

// ErrPassword: the password breaks AMT's strong password rule.
var ErrPassword = errors.New("the password does not meet the rules of Intel AMT")

// ValidatePassword checks the admin password of a CCM activation before anything is elevated:
// 8 to 32 printable ASCII characters with an upper case letter, a lower case letter, a digit and
// a symbol. AMT's rule also forbids the characters " , and : in it.
func ValidatePassword(p []byte) error {
	if len(p) < minPassword || len(p) > maxPassword {
		return ErrPassword
	}
	var upper, lower, digit, symbol bool
	for _, c := range p {
		switch {
		case c < ' ' || c > '~' || c == '"' || c == ',' || c == ':':
			return ErrPassword
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	if !upper || !lower || !digit || !symbol {
		return ErrPassword
	}
	return nil
}

// readSecret reads the password from r: at most maxPassword bytes up to the end of r. A longer
// or invalid password is refused; the bytes read are wiped then.
func readSecret(r io.Reader) ([]byte, error) {
	buf := make([]byte, maxPassword+1)
	n := 0
	for n < len(buf) {
		k, err := r.Read(buf[n:])
		n += k
		if err != nil {
			break
		}
	}
	p := bytes.Clone(buf[:n])
	clear(buf)
	if err := ValidatePassword(p); err != nil {
		clear(p)
		return nil, err
	}
	return p, nil
}
