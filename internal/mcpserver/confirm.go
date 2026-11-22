package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// confirmKey names the confirmation in the input requests of a tool call.
const confirmKey = "openlamt_confirm"

// confirm asks the person in the MCP client before a write tool runs, when the config requires
// it. It returns (nil, nil) when the call may go ahead and a result carrying the input request
// when the client has to ask first. The SDK turns that request into an elicitation (on older
// protocol versions the server sends it itself) and calls the handler again with the answer.
// A client without elicitation support and every answer but an accepted confirm:true refuse.
func (s *server) confirm(req *mcp.CallToolRequest, message string) (*mcp.CallToolResult, error) {
	if !s.cfg.RequireClientConfirmation {
		return nil, nil
	}
	if resp, ok := req.Params.InputResponses[confirmKey]; ok {
		if r, ok := resp.(*mcp.ElicitResult); ok && r.Action == "accept" && r.Content["confirm"] == true {
			return nil, nil
		}
		return nil, refuse("The action was not confirmed in the MCP client. Nothing was sent to the device.")
	}
	if p := req.Session.InitializeParams(); p == nil || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		return nil, refuse("require_client_confirmation is on in the openlamt-mcp config, but this MCP client " +
			"cannot ask for confirmation (it does not support elicitation). Nothing was sent to the device.")
	}
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{confirmKey: &mcp.ElicitParams{
		Message: message,
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"confirm": {Type: "boolean", Title: "Run this action", Description: "The AI agent asks to run this action on the device."},
			},
			Required: []string{"confirm"},
		},
	}}}, nil
}
