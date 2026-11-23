package mcpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
)

// Settings and account changes ask like the OpenLAMT app: a change the app writes at once asks
// only with require_client_confirmation; a change the app guards with an orange or red warning
// always asks the person in the MCP client, and a red one also needs the device name in the
// argument confirm.

// change describes a settings or account write for its question.
type change struct {
	tool    string    // the tool name, part of the request state
	in      any       // the decoded input, part of the request state
	level   amt.Level // the app's level; 0 for a change the app writes without asking
	what    string    // what the agent asks for, as in confirmMessage
	warning string    // the app's warning; "" for none
	notes   []string  // more lines for the question, e.g. the TLS warning for a password
}

// riskWarnings are the warnings of the app's orange and red dialogs, in the MCP server's own
// English (the app's texts are Fyne translations this server cannot load).
var riskWarnings = map[amt.Operation]string{
	amt.OpHostName: "Favorites that use the name stop working until you edit them. With dynamic DNS, the device " +
		"may no longer be found under its current name.",
	amt.OpFeaturesOff: "Open sessions of the features switched off end right away, those of the OpenLAMT app and of " +
		"other clients included.",
	amt.OpTLS: "AMT restarts TLS with the new certificate. OpenLAMT updates the saved fingerprint for this device; " +
		"other clients see a changed certificate.",
	amt.OpNetwork: "Risk of lockout: a wrong address makes the device unreachable. Only the BIOS (MEBx) at the device " +
		"can fix it. Favorites that use the old address stop working until you edit them.",
	amt.OpEnvironment: "Risk of lockout: outside these domains, AMT closes its network access. Only the BIOS (MEBx) at " +
		"the device can fix a wrong entry.",
	amt.OpDisableNetwork: "This cannot be undone remotely. The device stops answering on the network right away: no " +
		"power control, KVM or media, not from OpenLAMT and not from any other tool. Only the BIOS (MEBx) at the device " +
		"can turn it back on.",
	amt.OpOwnAccount: "OpenLAMT signs in to this device with this account. A wrong name or password, or a removed " +
		"permission, can lock you out until an administrator fixes it.",
	amt.OpAdminAccount: "Risk of lockout: if the new password is lost, only the BIOS (MEBx) at the device can reset it.",
}

// deleteUserWarning is the warning of the app's orange "Delete user" dialog, which has no
// operation of its own.
const deleteUserWarning = "The user can no longer sign in to the device. This cannot be undone."

func levelName(l amt.Level) string {
	if l == amt.LevelRed {
		return "red"
	}
	return "orange"
}

// callState binds a question to the call it asks about: an HMAC over the tool name and the
// decoded input. An answer that comes back with other arguments (another value, another
// password) does not match and is refused. The key is the server's own, so the state reveals
// nothing about the input, the password included.
func (s *server) callState(tool string, in any) string {
	b, err := json.Marshal(in)
	if err != nil {
		panic(err) // the inputs are plain structs
	}
	mac := hmac.New(sha256.New, s.stateKey)
	mac.Write([]byte(tool))
	mac.Write([]byte{0})
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// confirmChange asks the person in the MCP client before c, as the app does: orange and red
// always, other changes only with require_client_confirmation. A red change must carry the
// device name in confirm (checked before anything is asked). It returns (nil, nil) when the
// call may go ahead.
func (s *server) confirmChange(req *mcp.CallToolRequest, c change, fav favorites.Favorite, confirm string) (*mcp.CallToolResult, error) {
	if c.level == amt.LevelRed && !strings.EqualFold(strings.TrimSpace(confirm), fav.DisplayName()) {
		return nil, refuse("This change can lock OpenLAMT out of the device (a red warning in the OpenLAMT app). "+
			"Pass confirm with the device name %s to show that you mean this device. Nothing was sent to the device.",
			quoteShort(fav.DisplayName()))
	}
	risky := c.level >= amt.LevelOrange
	if !risky && !s.cfg.RequireClientConfirmation {
		return nil, nil
	}
	msg := fmt.Sprintf("The AI agent asks OpenLAMT to %s on device %q (%s).", c.what, fav.DisplayName(), fav.Host)
	if c.warning != "" {
		msg += "\n\nWarning (" + levelName(c.level) + " in the OpenLAMT app): " + c.warning
	}
	for _, n := range c.notes {
		msg += "\n\n" + n
	}
	msg += "\n\nAllow it?"
	q := question{message: msg, state: s.callState(c.tool, c.in), risk: risky,
		stale: "The arguments of the call changed while the question was open, so the answer was for another change. " +
			"Nothing was sent to the device; call the tool again."}
	if risky {
		q.title = fmt.Sprintf("I understand the risk for %q (%s)", fav.DisplayName(), fav.Host)
	}
	return s.ask(req, q)
}

// confirmation is the amt proof for op after the person agreed: the app's checkbox.
func confirmation(op amt.Operation, host string) amt.Confirmation {
	c, err := amt.ConfirmChecked(op, host, true)
	if err != nil {
		panic(err) // op is a known operation
	}
	return c
}

// passwordNoTLS is the warning for a password sent without TLS (the app asks first, #37).
func passwordNoTLS(user string) string {
	return fmt.Sprintf("The connection to this device has no TLS. The password travels as a digest that works like the "+
		"password itself: anyone who captures it on the network can sign in as %s.", user)
}

// writeOutput is the result of a settings or account write.
type writeOutput struct {
	Device   string   `json:"device"`
	Result   string   `json:"result"`
	Warnings []string `json:"warnings,omitempty"`
}

// onOff names a switch value in a question.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
