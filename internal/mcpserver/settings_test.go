package mcpserver

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

// deviceSettings is a device with every section readable and available.
func deviceSettings() amt.Settings {
	return amt.Settings{
		General:     amt.GeneralSettings{HostName: "nuc", DomainName: "example.com", PingResponse: true, DigestRealm: "Digest:ABCD", NetworkEnabled: true},
		Network:     amt.NetworkSettings{Available: true, DHCP: true, IPAddress: "192.0.2.10", SubnetMask: "255.255.255.0", MACAddress: "00:00:5e:00:53:01", LinkUp: true},
		Features:    amt.Features{Redirection: true, SOL: true, IDER: true, KVM: true, KVMAvailable: true},
		Consent:     amt.ConsentSettings{Available: true, Policy: amt.ConsentAll, CanModify: true, CodeTimeoutSeconds: 120},
		TLS:         amt.TLSSettings{Remote: amt.TLSInterface{Available: true, Enabled: true}},
		Clock:       amt.ClockInfo{Available: true, Time: time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC), Offset: 3 * time.Second},
		Environment: amt.EnvironmentDetection{Available: true, Domains: []string{"example.com"}},
	}
}

// askAll answers every question with accept and records the messages and checkbox titles.
type askAll struct {
	messages, titles []string
	answer           *mcp.ElicitResult
}

func (a *askAll) handler(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	a.messages = append(a.messages, r.Params.Message)
	if sc, ok := r.Params.RequestedSchema.(map[string]any); ok {
		props, _ := sc["properties"].(map[string]any)
		c, _ := props["confirm"].(map[string]any)
		title, _ := c["title"].(string)
		a.titles = append(a.titles, title)
	} else if b, err := json.Marshal(r.Params.RequestedSchema); err == nil {
		var sc struct {
			Properties map[string]struct {
				Title string `json:"title"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(b, &sc)
		a.titles = append(a.titles, sc.Properties["confirm"].Title)
	}
	if a.answer != nil {
		return a.answer, nil
	}
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
}

// TestGetSettingsReportsSectionsAndGaps: what the device returns, with the sections it refused
// in not_permitted and those the firmware lacks in not_available (the partial model of #58).
func TestGetSettingsReportsSectionsAndGaps(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	st := deviceSettings()
	st.Unreadable = amt.SettingsNetwork
	st.Features.KVMAvailable, st.Features.KVM = false, false
	st.Consent.Available = false
	f.Cfg.Settings = st
	f.Cfg.CertsErr = amt.ErrAccessDenied
	h.start()
	out := h.mustOK("get_settings", dev("NUC"))
	g, _ := out["general"].(map[string]any)
	if g["host_name"] != "nuc" || g["dynamic_dns"] != "off" || g["ping_response"] != true {
		t.Fatalf("general %v", g)
	}
	if _, ok := out["network"]; ok {
		t.Fatalf("network shown although unreadable: %v", out["network"])
	}
	rf, _ := out["remote_features"].(map[string]any)
	if rf["redirection"] != true || rf["sol"] != true || rf["ider"] != true {
		t.Fatalf("remote_features %v", rf)
	}
	if _, ok := rf["kvm"]; ok {
		t.Fatalf("kvm shown although the firmware lacks it: %v", rf)
	}
	if !reflect.DeepEqual(out["not_permitted"], []any{"network", "tls_certificates"}) ||
		!reflect.DeepEqual(out["not_available"], []any{"kvm", "user_consent"}) {
		t.Fatalf("not_permitted %v not_available %v", out["not_permitted"], out["not_available"])
	}
	ck, _ := out["clock"].(map[string]any)
	env, _ := out["environment_detection"].(map[string]any)
	if ck["time"] != "2026-11-23T08:00:00Z" || ck["offset_seconds"] != float64(3) || !reflect.DeepEqual(env["domains"], []any{"example.com"}) {
		t.Fatalf("clock %v env %v", ck, env)
	}
	if len(f.ConfigWrites()) != 0 {
		t.Fatalf("get_settings wrote %v", f.ConfigWrites())
	}
}

// TestGetSettingsListsCertificates: the TLS section names the certificates with their ids.
func TestGetSettingsListsCertificates(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("Lab", "lab.example.com", true)
	f.Cfg.Settings = deviceSettings()
	f.Cfg.Certificates = []amt.Certificate{
		{ID: "Intel(r) AMT Certificate: Handle: 1", Subject: "CN=lab", HasPrivateKey: true, TLS: true, Fingerprint: testFP},
		{ID: "Intel(r) AMT Certificate: Handle: 2", Subject: "CN=root", TrustedRoot: true},
	}
	h.start()
	out := h.mustOK("get_settings", dev("Lab"))
	tls, _ := out["tls"].(map[string]any)
	certs, _ := tls["certificates"].([]any)
	if len(certs) != 2 {
		t.Fatalf("tls %v", tls)
	}
	c0, c1 := certs[0].(map[string]any), certs[1].(map[string]any)
	if c0["current"] != true || c0["usable"] != true || c1["usable"] != false || c1["id"] != "Intel(r) AMT Certificate: Handle: 2" {
		t.Fatalf("certificates %v", certs)
	}
}

// TestSetGeneralPingWritesWithoutQuestion: the app switches ping at once, so does the server
// (without require_client_confirmation), also for a client that cannot ask.
func TestSetGeneralPingWritesWithoutQuestion(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.start()
	h.mustOK("set_general", dev("NUC", "ping_response", false))
	if f.Cfg.Settings.General.PingResponse || !reflect.DeepEqual(f.ConfigWrites(), []string{"SetGeneral"}) {
		t.Fatalf("ping %v writes %v", f.Cfg.Settings.General.PingResponse, f.ConfigWrites())
	}
	h.mustFail("set_general", dev("NUC"), "at least one")
	h.mustFail("set_general", dev("NUC", "host_name", "-bad-"), "Invalid host_name")
	h.mustFail("set_general", dev("NUC", "host_name", " "), "must not be empty")
}

// TestOrangeChangeAlwaysAsks: a change the app guards with an orange warning asks the person in
// the MCP client although require_client_confirmation is off; a client that cannot ask and a
// declined question send nothing.
func TestOrangeChangeAlwaysAsks(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.start()
	h.mustFail("set_general", dev("NUC", "host_name", "new-name"), "cannot ask for confirmation")
	if len(h.connectCalls()) != 0 {
		t.Fatal("connected without a confirmation")
	}

	a := &askAll{answer: &mcp.ElicitResult{Action: "decline"}}
	h = newHarness(t)
	f = h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.elicit = a.handler
	h.start()
	h.mustFail("set_general", dev("NUC", "host_name", "new-name"), "not confirmed")
	if len(f.ConfigWrites()) != 0 || len(h.connectCalls()) != 0 {
		t.Fatalf("declined, but writes %v", f.ConfigWrites())
	}
	a.answer = nil
	h.mustOK("set_general", dev("NUC", "host_name", "new-name"))
	if f.Cfg.Settings.General.HostName != "new-name" {
		t.Fatalf("host name %q", f.Cfg.Settings.General.HostName)
	}
	if len(a.messages) != 2 || !strings.Contains(a.messages[1], "«new-name»") || !strings.Contains(a.messages[1], "Warning (orange in the OpenLAMT app)") ||
		!strings.Contains(a.titles[1], `I understand the risk for "NUC" (192.0.2.10)`) {
		t.Fatalf("asked %q titles %q", a.messages, a.titles)
	}
}

// TestRedChangeNeedsDestructiveAndDeviceName: set_network is a red change: offered only with
// the group destructive, refused without the device name in confirm (before anyone is
// asked), and asked always.
func TestRedChangeNeedsDestructiveAndDeviceName(t *testing.T) {
	a := &askAll{}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.elicit = a.handler
	h.start()
	static := dev("NUC", "mode", "static", "ip_address", "192.0.2.50", "subnet_mask", "255.255.255.0", "gateway", "192.0.2.1")
	h.mustFail("set_network", static, "Pass confirm with the device name \"NUC\"")
	static["confirm"] = "other"
	h.mustFail("set_network", static, "Pass confirm with the device name")
	if len(a.messages) != 0 || len(h.connectCalls()) != 0 {
		t.Fatalf("asked %q", a.messages)
	}
	static["confirm"] = " nuc "
	h.mustOK("set_network", static)
	n := f.Cfg.Settings.Network
	if n.DHCP || n.IPAddress != "192.0.2.50" || n.Gateway != "192.0.2.1" || !reflect.DeepEqual(f.ConfigWrites(), []string{"SetNetwork"}) {
		t.Fatalf("network %+v writes %v", n, f.ConfigWrites())
	}
	if len(a.messages) != 1 || !strings.Contains(a.messages[0], "Warning (red in the OpenLAMT app): Risk of lockout") {
		t.Fatalf("asked %q", a.messages)
	}
	h.mustFail("set_network", dev("NUC", "mode", "static", "ip_address", "192.0.2.50", "subnet_mask", "255.0.255.0", "confirm", "NUC"),
		"Invalid subnet_mask")
	h.mustFail("set_network", dev("NUC", "mode", "bridge", "confirm", "NUC"), "mode")

	h = newHarness(t)
	h.addDevice("NUC", "192.0.2.10", false)
	h.cfg.Groups[GroupDestructive] = false
	h.start()
	if slices.Contains(h.toolNames(), "set_network") {
		t.Fatal("set_network offered without the group destructive")
	}
}

// TestRemoteFeaturesOffIsOrangeOnIsNot: switching a feature on is written at once, switching
// one off asks (orange), and user consent needs no question.
func TestRemoteFeaturesOffIsOrangeOnIsNot(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	st := deviceSettings()
	st.Features.KVM = false
	f.Cfg.Settings = st
	h.start()
	h.mustOK("set_remote_features", dev("NUC", "kvm", true, "user_consent", "none"))
	if !f.Cfg.Settings.Features.KVM || f.Cfg.Settings.Consent.Policy != amt.ConsentNone ||
		!reflect.DeepEqual(f.ConfigWrites(), []string{"SetFeatures", "SetConsent"}) {
		t.Fatalf("features %+v consent %v writes %v", f.Cfg.Settings.Features, f.Cfg.Settings.Consent.Policy, f.ConfigWrites())
	}
	h.mustFail("set_remote_features", dev("NUC", "sol", false), "cannot ask for confirmation")
	if !f.Cfg.Settings.Features.SOL {
		t.Fatal("SOL switched off without a question")
	}
	h.mustFail("set_remote_features", dev("NUC", "user_consent", "some"), "user_consent")
}

// TestRemoteFeaturesOffWarnsAboutSessions: the result says that open sessions ended and that
// the connect tools turn the feature on again.
func TestRemoteFeaturesOffWarnsAboutSessions(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.elicit = (&askAll{}).handler
	h.start()
	out := h.mustOK("set_remote_features", dev("NUC", "kvm", false))
	w, _ := json.Marshal(out["warnings"])
	if f.Cfg.Settings.Features.KVM || !strings.Contains(string(w), "turn a feature that is off on again") {
		t.Fatalf("kvm %v warnings %s", f.Cfg.Settings.Features.KVM, w)
	}
}

// TestEnvironmentAndDisableNetworkAreRed: both need confirm and a question, then write with
// the confirmation for their operation.
func TestEnvironmentAndDisableNetworkAreRed(t *testing.T) {
	a := &askAll{}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Settings = deviceSettings()
	h.elicit = a.handler
	h.start()
	h.mustFail("set_environment_detection", dev("NUC", "domains", []string{"example.org"}), "Pass confirm")
	h.mustFail("set_environment_detection", dev("NUC", "domains", []string{"a b"}, "confirm", "NUC"), "Invalid domains")
	h.mustOK("set_environment_detection", dev("NUC", "domains", []string{"example.org"}, "confirm", "NUC"))
	h.mustFail("disable_amt_network", dev("NUC"), "Pass confirm")
	h.mustOK("disable_amt_network", dev("NUC", "confirm", "NUC"))
	if !reflect.DeepEqual(f.Cfg.Settings.Environment.Domains, []string{"example.org"}) || f.Cfg.Settings.General.NetworkEnabled ||
		!reflect.DeepEqual(f.ConfigWrites(), []string{"SetEnvironmentDetection", "DisableAMTNetwork"}) || len(a.messages) != 2 {
		t.Fatalf("settings %+v writes %v asked %d", f.Cfg.Settings, f.ConfigWrites(), len(a.messages))
	}
	if !strings.Contains(a.messages[1], "This cannot be undone remotely") {
		t.Fatalf("asked %q", a.messages[1])
	}
}

// TestSetTLSCertificateFollowsPin: a new certificate is orange; the saved pin follows the
// fingerprint the device switched to, so the next call connects with it.
func TestSetTLSCertificateFollowsPin(t *testing.T) {
	newFP := strings.Repeat("cd", 32)
	h := newHarness(t)
	f := h.addDevice("Lab", "lab.example.com", true)
	f.Cfg.Settings = deviceSettings()
	f.Cfg.Certificates = []amt.Certificate{
		{ID: "cert-1", Subject: "CN=old", HasPrivateKey: true, TLS: true, Fingerprint: testFP},
		{ID: "cert-2", Subject: "CN=new", HasPrivateKey: true, Fingerprint: newFP},
		{ID: "root", Subject: "CN=root", TrustedRoot: true},
	}
	h.elicit = (&askAll{}).handler
	h.start()
	h.mustFail("set_tls_certificate", dev("Lab", "certificate_id", "cert-1"), "presents this certificate already")
	h.mustFail("set_tls_certificate", dev("Lab", "certificate_id", "root"), "cannot serve TLS")
	h.mustFail("set_tls_certificate", dev("Lab", "certificate_id", "nope"), "no certificate")
	out := h.mustOK("set_tls_certificate", dev("Lab", "certificate_id", "cert-2"))
	if !strings.Contains(out["result"].(string), "follows it") {
		t.Fatalf("%v", out)
	}
	calls, _ := h.logins.recorded()
	if !reflect.DeepEqual(calls, []string{"pin lab.example.com admin " + testFP + " " + newFP}) {
		t.Fatalf("logins %v", calls)
	}
	if fp, _ := h.keys.GetFingerprint("lab.example.com", "admin"); fp != newFP {
		t.Fatalf("pin %q", fp)
	}
	h.mustOK("get_settings", dev("Lab"))
	c := h.connectCalls()
	if last := c[len(c)-1]; last.Fingerprint != newFP {
		t.Fatalf("next connection pins %q", last.Fingerprint)
	}
}

// TestChangedArgumentsAfterQuestionAreRefused: the answer is bound to the arguments it was
// asked for; an answer that comes back with other arguments writes nothing.
func TestChangedArgumentsAfterQuestionAreRefused(t *testing.T) {
	srv := New(Options{Config: Defaults(t.TempDir()), Favorites: favList{}})
	s := srv.s
	fav := favorites.Favorite{Name: "NUC", Host: "192.0.2.10", Username: "admin"}
	asked := setGeneralInput{Device: "NUC", HostName: new("a")}
	other := setGeneralInput{Device: "NUC", HostName: new("b")}
	accepted := mcp.InputResponseMap{confirmKey: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}}
	req := func(state string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{InputResponses: accepted, RequestState: state}}
	}
	state := s.callState("set_general", asked)
	if res, err := s.confirmChange(req(state), change{tool: "set_general", in: asked, level: amt.LevelOrange, what: "x"}, fav, ""); res != nil || err != nil {
		t.Fatalf("same arguments: %v %v", res, err)
	}
	_, err := s.confirmChange(req(state), change{tool: "set_general", in: other, level: amt.LevelOrange, what: "x"}, fav, "")
	if err == nil || !strings.Contains(err.Error(), "arguments of the call changed") {
		t.Fatalf("other arguments: %v", err)
	}
	if _, err := s.confirmChange(req(""), change{tool: "set_general", in: asked, level: amt.LevelOrange, what: "x"}, fav, ""); err == nil {
		t.Fatal("an answer without request state was taken")
	}
}

// TestSettingsWriteErrorsAreExplained: the device's refusals come back in the server's words.
func TestSettingsWriteErrorsAreExplained(t *testing.T) {
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	st := deviceSettings()
	st.Consent.CanModify = false
	f.Cfg.Settings = st
	h.start()
	h.mustFail("set_remote_features", dev("NUC", "user_consent", "none"), "does not allow this change")
	f.Cfg.WriteErr = amt.ErrAccessDenied
	h.mustFail("sync_clock", dev("NUC"), "lacks the permission")
	var _ session.LoginStore = h.logins
}
