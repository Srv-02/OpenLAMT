package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
)

// userWrites are the account tools that send a password, each with the arguments for a device
// "NUC" and a check that it changed the device.
var userWrites = []struct {
	tool    string
	args    map[string]any
	changed func(amt.UserList) bool
}{
	{"add_user", dev("NUC", "name", "carol", "password", newPassword, "realms", []string{"Redirection"}),
		func(l amt.UserList) bool { return len(l.Users) == 4 }},
	{"update_user", dev("NUC", "user", "bob", "new_name", "robert", "password", newPassword),
		func(l amt.UserList) bool { return l.Users[1].Name == "robert" }},
	{"set_admin_account", dev("NUC", "password", newPassword, "confirm", "NUC"),
		func(l amt.UserList) bool { return false }}, // the admin password is not in the list; see the result
}

// TestPasswordWithoutTLSAsksFirst (#125): without TLS every account tool that carries a
// password asks before it sends it, also without require_client_confirmation; a client that
// cannot ask gets a refusal that names the reason; with TLS the plain tools ask nothing.
func TestPasswordWithoutTLSAsksFirst(t *testing.T) {
	for _, w := range userWrites {
		t.Run(w.tool+"/asks", func(t *testing.T) {
			a := &askAll{answer: &mcp.ElicitResult{Action: "decline"}}
			h := newHarness(t)
			f := h.addDeviceAs("NUC", "192.0.2.10", "operator", false)
			f.Cfg.Users = deviceUsers()
			h.elicit = a.handler
			h.start()
			h.mustFail(w.tool, w.args, "not confirmed")
			if len(a.messages) != 1 || !strings.Contains(a.messages[0], "no TLS") {
				t.Fatalf("asked %q", a.messages)
			}
			if w.changed(f.Cfg.Users) || len(f.Cfg.Users.Users) != 3 {
				t.Fatalf("written after a declined question: %+v", f.Cfg.Users)
			}
			a.answer = nil // accept
			out := mustOKNoPassword(t, h, w.tool, w.args)
			if len(a.messages) != 2 {
				t.Fatalf("asked %q", a.messages)
			}
			if b, _ := json.Marshal(out["warnings"]); !strings.Contains(string(b), "has no TLS. The password travels") {
				t.Fatalf("the result lost the warning: %v", out)
			}
			noPassword(t, h, a.messages)
		})
		t.Run(w.tool+"/no elicitation", func(t *testing.T) {
			h := newHarness(t)
			f := h.addDeviceAs("NUC", "192.0.2.10", "operator", false)
			f.Cfg.Users = deviceUsers()
			h.start()
			text := h.mustFail(w.tool, w.args, "no TLS")
			if !strings.Contains(text, "cannot ask for confirmation") || strings.Contains(text, "require_client_confirmation") ||
				strings.Contains(text, newPassword) {
				t.Fatalf("refusal %q", text)
			}
			if len(f.Cfg.Users.Users) != 3 || f.Cfg.Users.Users[1].Name != "bob" {
				t.Fatalf("written: %+v", f.Cfg.Users)
			}
		})
	}
	for _, w := range userWrites[:2] {
		t.Run(w.tool+"/TLS", func(t *testing.T) {
			h := newHarness(t)
			f := h.addDeviceAs("NUC", "192.0.2.10", "operator", true)
			f.Cfg.Users = deviceUsers()
			h.start() // no elicitation: a question would be refused
			mustOKNoPassword(t, h, w.tool, w.args)
			if !w.changed(f.Cfg.Users) {
				t.Fatalf("not written: %+v", f.Cfg.Users)
			}
		})
	}
}

// TestRefusalWithoutElicitationNamesTheRightReason (#125): require_client_confirmation is not
// the reason when the config does not ask for it.
func TestRefusalWithoutElicitationNamesTheRightReason(t *testing.T) {
	h := newHarness(t)
	h.addDevice("NUC", "192.0.2.10", true)
	h.cfg.RequireClientConfirmation = true
	h.start()
	text := h.mustFail("power_action", dev("NUC", "action", "reset"), "cannot ask for confirmation")
	if !strings.Contains(text, "A question is required") || !strings.Contains(text, "require_client_confirmation") {
		t.Fatalf("%q", text)
	}
}

// secretTextSetup connects the KVM or serial console session of a device without TLS and
// returns how many bytes the device got so far.
func secretTextSetup(t *testing.T, h *harness, name string) (received func() int) {
	t.Helper()
	f := h.addDevice("NUC", "192.0.2.10", false)
	if name == "kvm_type_text" {
		srv := addKVM(t, f, kvmtest.Options{})
		h.start()
		h.mustOK("kvm_connect", dev("NUC"))
		return func() int { return len(keyEvents(srv)) }
	}
	d := addSOL(t, f)
	d.accept()
	h.start()
	h.mustOK("sol_connect", dev("NUC"))
	p := d.peer()
	got := 0
	return func() int {
		// Only called after a call that must have sent: read what arrived.
		got += len(expectData(t, p))
		return got
	}
}

// TestSecretTextWithoutTLSAsksFirst (#125): kvm_type_text and sol_type_text with secret on a
// device without TLS ask first without require_client_confirmation, with the TLS warning and
// without the text; a client that cannot ask gets a refusal; a text that is not secret asks
// nothing.
func TestSecretTextWithoutTLSAsksFirst(t *testing.T) {
	for _, name := range []string{"kvm_type_text", "sol_type_text"} {
		t.Run(name, func(t *testing.T) {
			a := &askAll{answer: &mcp.ElicitResult{Action: "decline"}}
			h := newHarness(t)
			h.elicit = a.handler
			received := secretTextSetup(t, h, name)
			args := dev("NUC", "text", "Hunter2", "secret", true)
			h.mustFail(name, args, "not confirmed")
			if len(a.messages) != 1 || !strings.Contains(a.messages[0], "no TLS") || strings.Contains(a.messages[0], "Hunter2") {
				t.Fatalf("asked %q", a.messages)
			}
			if name == "kvm_type_text" && received() != 0 {
				t.Fatal("typed after a declined question")
			}
			a.answer = nil // accept
			h.mustOK(name, args)
			if len(a.messages) != 2 || received() == 0 {
				t.Fatalf("asked %q", a.messages)
			}
			h.mustOK(name, dev("NUC", "text", "ls")) // not secret: no question
			if len(a.messages) != 2 {
				t.Fatalf("asked for a text that is not secret: %q", a.messages)
			}
		})
		t.Run(name+"/no elicitation", func(t *testing.T) {
			h := newHarness(t)
			received := secretTextSetup(t, h, name)
			text := h.mustFail(name, dev("NUC", "text", "Hunter2", "secret", true), "no TLS")
			if !strings.Contains(text, "cannot ask for confirmation") || strings.Contains(text, "require_client_confirmation") {
				t.Fatalf("refusal %q", text)
			}
			if name == "kvm_type_text" && received() != 0 {
				t.Fatal("typed without an answer")
			}
		})
	}
}
