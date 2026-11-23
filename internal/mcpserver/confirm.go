package mcpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/favorites"
)

// confirmKey names the confirmation in the input requests of a tool call.
const confirmKey = "openlamt_confirm"

// confirm asks the person in the MCP client before a write tool runs on the device of fav, when
// the config requires it. It returns (nil, nil) when the call may go ahead and a result carrying
// the input request when the client has to ask first. The SDK turns that request into an
// elicitation (on older protocol versions the server sends it itself) and calls the handler
// again with the answer. A client without elicitation support and every answer but an accepted
// confirm:true refuse, and so does an answer about another device: the favorite was renamed or
// now has another host or user.
func (s *server) confirm(req *mcp.CallToolRequest, fav favorites.Favorite, message string) (*mcp.CallToolResult, error) {
	return s.confirmFor(req, fav, message, "", "The favorite changed while the question was open (another name, host or user), "+
		"so the answer was for another device. Nothing was sent to the device; call the tool again.")
}

// confirmFor is confirm for an action on something that may change while the question is open
// (the mounted image, the file to mount). state names it; the question carries it, bound to the
// favorite, as the request state, and an answer whose request state is not the current one is
// refused with stale: the person agreed to something else.
func (s *server) confirmFor(req *mcp.CallToolRequest, fav favorites.Favorite, message, state, stale string) (*mcp.CallToolResult, error) {
	if !s.cfg.RequireClientConfirmation {
		return nil, nil
	}
	return s.ask(req, question{message: message, state: s.bindState(fav, state), stale: stale})
}

// bindState is the request state of a question about state on the device of fav: an HMAC over
// the favorite's name, host and user and state, with the server's own key.
func (s *server) bindState(fav favorites.Favorite, state string) string {
	mac := hmac.New(sha256.New, s.stateKey)
	for _, p := range []string{fav.DisplayName(), fav.Host, fav.Username, state} {
		mac.Write([]byte(p))
		mac.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// question is what ask puts to the person in the MCP client.
type question struct {
	message string
	state   string // request state the answer must carry
	stale   string // refusal for an answer with another request state
	title   string // title of the checkbox; "" for "Run this action"
	// risk: the OpenLAMT app asks with an orange or red warning, so the question is asked even
	// without require_client_confirmation.
	risk bool
}

// ask puts q to the person in the MCP client. It returns (nil, nil) for an accepted confirm:true
// with the current request state, a result carrying the input request when the client has to ask
// first, and a refusal otherwise (also for a client without elicitation support).
func (s *server) ask(req *mcp.CallToolRequest, q question) (*mcp.CallToolResult, error) {
	if resp, ok := req.Params.InputResponses[confirmKey]; ok {
		if r, ok := resp.(*mcp.ElicitResult); ok && r.Action == "accept" && r.Content["confirm"] == true {
			if subtle.ConstantTimeCompare([]byte(req.Params.RequestState), []byte(q.state)) != 1 {
				return nil, refuse("%s", q.stale)
			}
			return nil, nil
		}
		return nil, refuse("The action was not confirmed in the MCP client. Nothing was sent to the device.")
	}
	if p := req.Session.InitializeParams(); p == nil || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		if q.risk {
			return nil, refuse("The OpenLAMT app asks before this change with a risk warning, so a person has to confirm it in the " +
				"MCP client, but this MCP client cannot ask for confirmation (it does not support elicitation). " +
				"Nothing was sent to the device. Make the change in the OpenLAMT app, or use an MCP client that supports elicitation.")
		}
		return nil, refuse("require_client_confirmation is on in the openlamt-mcp config, but this MCP client " +
			"cannot ask for confirmation (it does not support elicitation). Nothing was sent to the device.")
	}
	title := q.title
	if title == "" {
		title = "Run this action"
	}
	return &mcp.CallToolResult{RequestState: q.state, InputRequests: mcp.InputRequestMap{confirmKey: &mcp.ElicitParams{
		Message: q.message,
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"confirm": {Type: "boolean", Title: title, Description: "The AI agent asks to run this action on the device."},
			},
			Required: []string{"confirm"},
		},
	}}}, nil
}

// visible shows text in a confirmation prompt as it is, so a control or format character
// cannot hide or reorder what the person reads: line breaks as ⏎, tabs as ⇥, carriage returns
// as ␍, other C0 controls and DEL as their control pictures, C1 controls and format characters
// (bidi overrides, zero-width characters) as <U+XXXX>.
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString("⏎")
		case r == '\t':
			b.WriteString("⇥")
		case r == '\r':
			b.WriteString("␍")
		case r < 0x20:
			b.WriteRune(0x2400 + r)
		case r == 0x7f:
			b.WriteString("␡")
		case r >= 0x80 && r <= 0x9f, unicode.Is(unicode.Cf, r):
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maxQuoted is how many characters of an agent's value an error message repeats.
const maxQuoted = 40

// Limits of what the action log and stderr keep of a call: maxLogged characters of the device
// name the agent sent, maxLoggedMessage of a message or error text (a backstop: the texts
// quote agent values shortened already).
const (
	maxLogged        = 80
	maxLoggedMessage = 2000
)

// shorten cuts s after max characters and says how long it was, so a huge value cannot flood
// the action log or stderr.
func shorten(s string, max int) string {
	i, n := 0, 0
	for i < len(s) && n < max {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	if i == len(s) {
		return s
	}
	return s[:i] + fmt.Sprintf("… (%d characters)", utf8.RuneCountInString(s))
}

// quoteShort quotes a value the agent sent, for an error message and so for the action log: Go
// quoting (control and format characters escaped), at most maxQuoted characters, then the
// length, so a huge or crafted value cannot flood the log or hide in it.
func quoteShort(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= maxQuoted {
		return strconv.Quote(s)
	}
	return strconv.Quote(string([]rune(s)[:maxQuoted])) + fmt.Sprintf("… (%d characters)", n)
}
