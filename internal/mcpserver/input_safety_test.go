package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// TestKVMTypeTextQuestionShowsFormatCharacters: a bidi override or a zero-width character in
// the text is shown as its code point, so it cannot reorder or hide what the person reads.
func TestKVMTypeTextQuestionShowsFormatCharacters(t *testing.T) {
	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addKVM(t, f, kvmtest.Options{})
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	h.mustOK("kvm_connect", dev("NUC"))
	h.mustFail("kvm_type_text", dev("NUC", "text", "echo safe\u202e;rm -rf ~\u200b\n", "layout", "us"), "not confirmed")
	if len(asked) != 1 || !strings.Contains(asked[0], "«echo safe<U+202E>;rm -rf ~<U+200B>»") || strings.ContainsRune(asked[0], '\u202e') {
		t.Fatalf("asked %q", asked)
	}
}

// TestSOLTypeTextRefusesFormatCharacters: the serial console takes printable ASCII only, so a
// bidi override never reaches a question.
func TestSOLTypeTextRefusesFormatCharacters(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	h.mustFail("sol_type_text", dev("NUC", "text", "ls\u202e"), "printable ASCII")
}

// TestUnknownKeyNamesAreShortened: an unknown key name is repeated at most 40 characters long,
// in the answer and in the action log, with control and format characters escaped.
func TestUnknownKeyNamesAreShortened(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.start()
	long := strings.Repeat("x", 5000) + "\u202e"
	for _, tool := range []string{"kvm_send_keys", "sol_send_keys"} {
		text := h.mustFail(tool, dev("NUC", "keys", []string{"Ctrl+" + long}), "(5006 characters)")
		if len(text) > 600 || strings.Contains(text, strings.Repeat("x", 41)) {
			t.Fatalf("%s: %d bytes: %.200s", tool, len(text), text)
		}
	}
	text := h.mustFail("kvm_send_keys", dev("NUC", "keys", []string{"Ctrl+\u202e"}), `\u202e`)
	if strings.ContainsRune(text, '\u202e') {
		t.Fatalf("unescaped: %q", text)
	}
	for _, e := range h.logEntries() {
		b, _ := json.Marshal(e)
		if len(b) > 800 {
			t.Fatalf("log line of %d bytes: %.200s", len(b), b)
		}
	}
}

// TestInstructionsNameConsentTools: every tool with consent_code is named where the server
// tells the agent about user consent.
func TestInstructionsNameConsentTools(t *testing.T) {
	h := newHarness(t)
	h.start()
	in := h.cs.InitializeResult().Instructions
	line := ""
	for _, l := range strings.Split(in, "\n") {
		if strings.Contains(l, "user consent") {
			line = l
		}
	}
	for _, tool := range []string{"boot_to_bios", "kvm_connect", "sol_connect", "media_mount"} {
		if !strings.Contains(line, tool) {
			t.Errorf("consent line %q lacks %s", line, tool)
		}
	}
	// Every tool that takes consent_code is in the line.
	res, err := h.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(b), `"consent_code"`) && !strings.Contains(line, tool.Name) {
			t.Errorf("%s takes consent_code but the instructions do not name it", tool.Name)
		}
	}
}
