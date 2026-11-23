package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
)

// The settings tools: get_settings reads what the app's Settings page shows, the set_ tools
// write what it writes, at the app's confirmation level (see changes.go).

func (s *server) registerSettings(srv *mcp.Server) {
	closed := boolPtr(false)
	// Reading writes nothing, so read_only keeps it; the operator still switches it off with
	// the group settings (or read).
	if s.cfg.Groups[GroupRead] && s.cfg.Groups[GroupSettings] {
		addTool(s, srv, &mcp.Tool{Name: "get_settings", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed},
			Description: "Read the AMT settings of a device, as the Settings page of the OpenLAMT app shows them: general (host name, domain, ping, " +
				"dynamic DNS), network (DHCP or static IPv4), remote features (KVM, IDE-R, SOL), user consent, TLS with the certificates AMT holds, " +
				"the AMT clock and environment detection. Sections the account may not read are listed in not_permitted, sections the " +
				"firmware lacks in not_available."},
			s.getSettings)
	}
	if !s.cfg.Enabled(GroupSettings) {
		return
	}
	write := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: closed}
	risky := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: closed}
	addTool(s, srv, &mcp.Tool{Name: "set_general", Annotations: risky, InputSchema: setGeneralSchema(),
		Description: "Change the AMT host name, the domain, the ping response or dynamic DNS of a device. Leave out what stays. " +
			"Host name, domain and dynamic DNS can make the device unreachable under its name: the OpenLAMT app asks with an orange warning, " +
			"so the person in the MCP client is always asked."},
		s.setGeneral)
	addTool(s, srv, &mcp.Tool{Name: "set_remote_features", Annotations: risky, InputSchema: setFeaturesSchema(),
		Description: "Turn KVM, media redirection (IDE-R) or Serial over LAN (SOL) on or off, or set the user consent policy of a device. " +
			"Leave out what stays. Turning a feature off ends its open sessions: the OpenLAMT app asks with an orange warning, so the person " +
			"in the MCP client is always asked."},
		s.setRemoteFeatures)
	addTool(s, srv, &mcp.Tool{Name: "sync_clock", Annotations: write,
		Description: "Set the AMT clock of a device to the time of this computer."},
		s.syncClock)
	addTool(s, srv, &mcp.Tool{Name: "set_tls_certificate", Annotations: risky,
		Description: "Make AMT present another certificate of its store for TLS (certificate_id from get_settings, a usable one). " +
			"AMT restarts TLS; the saved certificate pin of the device follows, as in the OpenLAMT app. The app asks with an orange warning, " +
			"so the person in the MCP client is always asked."},
		s.setTLSCertificate)
	if !s.cfg.Enabled(GroupDestructive) {
		return
	}
	lockout := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed}
	addTool(s, srv, &mcp.Tool{Name: "set_network", Annotations: lockout, InputSchema: setNetworkSchema(),
		Description: "Change the IPv4 configuration of the wired AMT port of a device: DHCP or a static address. A wrong address makes " +
			"the device unreachable until someone fixes it in the BIOS (MEBx) at the device: a red warning in the OpenLAMT app. The person " +
			"in the MCP client is always asked, and confirm must hold the device name."},
		s.setNetwork)
	addTool(s, srv, &mcp.Tool{Name: "set_environment_detection", Annotations: lockout,
		Description: "Set the intranet domains of environment detection (at most 5; an empty list turns it off). Outside these " +
			"domains AMT closes its network access, so a wrong entry can lock OpenLAMT out: a red warning in the OpenLAMT app. The person " +
			"in the MCP client is always asked, and confirm must hold the device name."},
		s.setEnvironmentDetection)
	addTool(s, srv, &mcp.Tool{Name: "disable_amt_network", Annotations: lockout,
		Description: "Turn AMT off on the network. The device stops answering right away, to OpenLAMT and every other tool, and only " +
			"the BIOS (MEBx) at the device turns it back on: a red warning in the OpenLAMT app. The person in the MCP client is always asked, " +
			"and confirm must hold the device name."},
		s.disableAMTNetwork)
}

// --- get_settings

type settingsGeneral struct {
	HostName               string `json:"host_name"`
	DomainName             string `json:"domain_name"`
	SharedFQDN             bool   `json:"shared_fqdn" jsonschema:"the FQDN is shared with the operating system"`
	HostOSFQDN             string `json:"host_os_fqdn,omitempty"`
	DigestRealm            string `json:"digest_realm,omitempty"`
	PingResponse           bool   `json:"ping_response"`
	RMCPPingResponse       bool   `json:"rmcp_ping_response"`
	DynamicDNS             string `json:"dynamic_dns" jsonschema:"off, client (AMT registers its name in DNS) or dhcp_server (the DHCP server registers it)"`
	DDNSIntervalMinutes    int    `json:"ddns_interval_minutes"`
	DDNSTTLSeconds         int    `json:"ddns_ttl_seconds"`
	AMTNetworkEnabled      bool   `json:"amt_network_enabled"`
	WSManOnly              bool   `json:"wsman_only"`
	IdleWakeTimeoutMinutes int    `json:"idle_wake_timeout_minutes"`
	PrivacyLevel           int    `json:"privacy_level" jsonschema:"0 default, 1 enhanced, 2 extreme"`
}

type settingsNetwork struct {
	DHCP         bool   `json:"dhcp"`
	IPSync       bool   `json:"ip_sync" jsonschema:"AMT uses the static address of the operating system"`
	IPAddress    string `json:"ip_address,omitempty"`
	SubnetMask   string `json:"subnet_mask,omitempty"`
	Gateway      string `json:"gateway,omitempty"`
	PrimaryDNS   string `json:"primary_dns,omitempty"`
	SecondaryDNS string `json:"secondary_dns,omitempty"`
	MACAddress   string `json:"mac_address,omitempty"`
	LinkUp       bool   `json:"link_up"`
	SharedMAC    bool   `json:"shared_mac" jsonschema:"AMT shares the MAC address with the operating system"`
}

type settingsFeatures struct {
	Redirection *bool `json:"redirection,omitempty" jsonschema:"the redirection port, needed by KVM, SOL and IDE-R"`
	KVM         *bool `json:"kvm,omitempty"`
	IDER        *bool `json:"ider,omitempty"`
	SOL         *bool `json:"sol,omitempty"`
}

type settingsConsent struct {
	Policy                string `json:"policy" jsonschema:"none, kvm (consent for KVM only) or all (consent for every redirection and boot)"`
	Changeable            bool   `json:"changeable" jsonschema:"the policy may be changed over the network"`
	CodeTimeoutSeconds    int    `json:"code_timeout_seconds"`
	DisplayTimeoutSeconds int    `json:"display_timeout_seconds"`
}

type settingsTLSInterface struct {
	Enabled         bool     `json:"enabled"`
	MutualAuth      bool     `json:"mutual_auth"`
	AcceptNonSecure bool     `json:"accept_non_secure"`
	TrustedCNs      []string `json:"trusted_cns,omitempty"`
}

type settingsCertificate struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer,omitempty"`
	NotAfter    string `json:"not_after,omitempty" jsonschema:"RFC 3339, UTC"`
	Fingerprint string `json:"fingerprint,omitempty" jsonschema:"SHA-256 of the certificate, the pin OpenLAMT saves"`
	Current     bool   `json:"current" jsonschema:"the certificate AMT presents now"`
	Usable      bool   `json:"usable" jsonschema:"set_tls_certificate accepts it"`
}

type settingsTLS struct {
	Network      *settingsTLSInterface `json:"network_interface,omitempty"`
	Local        *settingsTLSInterface `json:"local_interface,omitempty"`
	Certificates []settingsCertificate `json:"certificates,omitempty"`
}

type settingsClock struct {
	Time          string `json:"time" jsonschema:"the AMT clock, RFC 3339, UTC"`
	OffsetSeconds int64  `json:"offset_seconds" jsonschema:"AMT clock minus the clock of this computer"`
}

type settingsEnvironment struct {
	Domains []string `json:"domains" jsonschema:"intranet domain suffixes; empty: detection is off"`
}

type settingsOutput struct {
	Device               string               `json:"device"`
	General              *settingsGeneral     `json:"general,omitempty"`
	Network              *settingsNetwork     `json:"network,omitempty"`
	RemoteFeatures       *settingsFeatures    `json:"remote_features,omitempty"`
	UserConsent          *settingsConsent     `json:"user_consent,omitempty"`
	TLS                  *settingsTLS         `json:"tls,omitempty"`
	Clock                *settingsClock       `json:"clock,omitempty"`
	EnvironmentDetection *settingsEnvironment `json:"environment_detection,omitempty"`
	NotPermitted         []string             `json:"not_permitted,omitempty" jsonschema:"sections the account may not read"`
	NotAvailable         []string             `json:"not_available,omitempty" jsonschema:"sections the firmware of the device lacks"`
	Warnings             []string             `json:"warnings,omitempty"`
}

var ddnsNames = map[amt.DDNSMode]string{amt.DDNSOff: "off", amt.DDNSClient: "client", amt.DDNSByDHCPServer: "dhcp_server"}

var consentNames = map[amt.ConsentPolicy]string{amt.ConsentNone: "none", amt.ConsentKVM: "kvm", amt.ConsentAll: "all"}

func tlsInterface(i amt.TLSInterface) *settingsTLSInterface {
	if !i.Available {
		return nil
	}
	return &settingsTLSInterface{Enabled: i.Enabled, MutualAuth: i.MutualAuth, AcceptNonSecure: i.AcceptNonSecure, TrustedCNs: i.TrustedCNs}
}

// settingsView turns amt.Settings into the output: a section is left out and named in
// not_permitted when the device refused it, in not_available when the firmware lacks it.
func settingsView(st amt.Settings, out *settingsOutput) {
	section := func(part amt.SettingsPart, name string, available bool, set func()) {
		switch {
		case !st.Readable(part):
			out.NotPermitted = append(out.NotPermitted, name)
		case !available:
			out.NotAvailable = append(out.NotAvailable, name)
		default:
			set()
		}
	}
	g := st.General
	section(amt.SettingsGeneral, "general", true, func() {
		out.General = &settingsGeneral{HostName: g.HostName, DomainName: g.DomainName, SharedFQDN: g.SharedFQDN, HostOSFQDN: g.HostOSFQDN,
			DigestRealm: g.DigestRealm, PingResponse: g.PingResponse, RMCPPingResponse: g.RMCPPingResponse, DynamicDNS: ddnsNames[g.DynamicDNS],
			DDNSIntervalMinutes: g.DDNSIntervalMinutes, DDNSTTLSeconds: g.DDNSTTLSeconds, AMTNetworkEnabled: g.NetworkEnabled,
			WSManOnly: g.WSManOnly, IdleWakeTimeoutMinutes: g.IdleWakeTimeoutMinutes, PrivacyLevel: g.PrivacyLevel}
	})
	n := st.Network
	section(amt.SettingsNetwork, "network", n.Available, func() {
		out.Network = &settingsNetwork{DHCP: n.DHCP, IPSync: n.IPSync, IPAddress: n.IPAddress, SubnetMask: n.SubnetMask, Gateway: n.Gateway,
			PrimaryDNS: n.PrimaryDNS, SecondaryDNS: n.SecondaryDNS, MACAddress: n.MACAddress, LinkUp: n.LinkUp, SharedMAC: n.SharedMAC}
	})
	f := st.Features
	var rf settingsFeatures
	section(amt.SettingsFeatures, "remote_features", true, func() {
		rf.Redirection, rf.IDER, rf.SOL = boolPtr(f.Redirection), boolPtr(f.IDER), boolPtr(f.SOL)
	})
	section(amt.SettingsKVM, "kvm", f.KVMAvailable, func() { rf.KVM = boolPtr(f.KVM) })
	if rf != (settingsFeatures{}) {
		out.RemoteFeatures = &rf
	}
	c := st.Consent
	section(amt.SettingsConsent, "user_consent", c.Available, func() {
		out.UserConsent = &settingsConsent{Policy: consentNames[c.Policy], Changeable: c.CanModify,
			CodeTimeoutSeconds: c.CodeTimeoutSeconds, DisplayTimeoutSeconds: c.DisplayTimeoutSeconds}
	})
	t := st.TLS
	section(amt.SettingsTLS, "tls", t.Remote.Available || t.Local.Available, func() {
		out.TLS = &settingsTLS{Network: tlsInterface(t.Remote), Local: tlsInterface(t.Local)}
	})
	ck := st.Clock
	section(amt.SettingsClock, "clock", ck.Available, func() {
		out.Clock = &settingsClock{Time: ck.Time.UTC().Format(time.RFC3339), OffsetSeconds: int64(ck.Offset / time.Second)}
	})
	e := st.Environment
	section(amt.SettingsEnvironment, "environment_detection", e.Available, func() {
		out.EnvironmentDetection = &settingsEnvironment{Domains: append([]string{}, e.Domains...)}
	})
}

func (s *server) getSettings(ctx context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, settingsOutput, error) {
	rec.device = in.Device
	sess, _, _, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, settingsOutput{}, err
	}
	defer sess.Close()
	st, err := sess.Settings()
	if err != nil {
		return nil, settingsOutput{}, err
	}
	out := settingsOutput{Device: in.Device, Warnings: warnings}
	settingsView(st, &out)
	certs, err := sess.TLSCertificates()
	switch {
	case errors.Is(err, amt.ErrAccessDenied):
		out.NotPermitted = append(out.NotPermitted, "tls_certificates")
	case err != nil:
		out.Warnings = append(out.Warnings, "Cannot read the TLS certificates: "+explain(err))
	case len(certs) > 0:
		if out.TLS == nil {
			out.TLS = &settingsTLS{}
		}
		for _, c := range certs {
			sc := settingsCertificate{ID: c.ID, Subject: c.Subject, Issuer: c.Issuer, Fingerprint: c.Fingerprint, Current: c.TLS, Usable: c.Usable()}
			if !c.NotAfter.IsZero() {
				sc.NotAfter = c.NotAfter.UTC().Format(time.RFC3339)
			}
			out.TLS.Certificates = append(out.TLS.Certificates, sc)
		}
	}
	return nil, out, nil
}

// --- the writes

// settingsWrite opens the device and runs write with the session's host (the host a
// confirmation is bound to).
func (s *server) settingsWrite(ctx context.Context, device string, write func(sess *session.Session, host string) (string, []string, error)) (writeOutput, error) {
	sess, _, _, warnings, err := s.openDevice(ctx, device)
	if err != nil {
		return writeOutput{}, err
	}
	defer sess.Close()
	result, more, err := write(sess, sess.Config().Host)
	if err != nil {
		return writeOutput{}, err
	}
	return writeOutput{Device: device, Result: result, Warnings: append(warnings, more...)}, nil
}

const acceptedReadAgain = "The device accepted the change. Call get_settings to see the values it uses now."

type setGeneralInput struct {
	Device       string  `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	HostName     *string `json:"host_name,omitempty" jsonschema:"new AMT host name: up to 63 letters, digits and hyphens, not starting or ending with a hyphen; empty clears it"`
	DomainName   *string `json:"domain_name,omitempty" jsonschema:"new domain, dot-separated labels, up to 191 characters; empty clears it"`
	PingResponse *bool   `json:"ping_response,omitempty" jsonschema:"answer ICMP ping"`
	DynamicDNS   string  `json:"dynamic_dns,omitempty" jsonschema:"off, client (AMT registers its name in DNS) or dhcp_server (the DHCP server registers it)"`
}

func setGeneralSchema() *jsonschema.Schema {
	sc, err := jsonschema.For[setGeneralInput](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["dynamic_dns"].Enum = []any{"off", "client", "dhcp_server"}
	return sc
}

func (s *server) setGeneral(ctx context.Context, req *mcp.CallToolRequest, in setGeneralInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	var c amt.GeneralChange
	var parts []string
	trim := func(p *string) *string {
		if p == nil {
			return nil
		}
		v := strings.TrimSpace(*p)
		return &v
	}
	if c.HostName = trim(in.HostName); c.HostName != nil {
		parts = append(parts, fmt.Sprintf("set the host name to «%s»", visible(*c.HostName)))
	}
	if c.DomainName = trim(in.DomainName); c.DomainName != nil {
		parts = append(parts, fmt.Sprintf("set the domain to «%s»", visible(*c.DomainName)))
	}
	if c.PingResponse = in.PingResponse; c.PingResponse != nil {
		parts = append(parts, "turn the ping response "+onOff(*c.PingResponse))
	}
	if d := strings.TrimSpace(in.DynamicDNS); d != "" {
		var mode amt.DDNSMode
		ok := false
		for m, name := range ddnsNames {
			if name == d {
				mode, ok = m, true
			}
		}
		if !ok {
			return nil, writeOutput{}, refuse("dynamic_dns must be off, client or dhcp_server.")
		}
		c.DynamicDNS = &mode
		parts = append(parts, "set dynamic DNS to "+d)
	}
	if len(parts) == 0 {
		return nil, writeOutput{}, refuse("Give at least one of host_name, domain_name, ping_response or dynamic_dns.")
	}
	if c.HostName != nil && *c.HostName == "" {
		// As the app: a device without a host name cannot be reached under its name.
		return nil, writeOutput{}, refuse("host_name must not be empty.")
	}
	if err := c.Validate(); err != nil {
		return nil, writeOutput{}, err
	}
	what := strings.Join(parts, ", ")
	rec.action = what
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_general", in: in, what: what}
	if c.HostName != nil || c.DomainName != nil || c.DynamicDNS != nil {
		ch.level, ch.warning = amt.LevelOrange, riskWarnings[amt.OpHostName]
	}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		var conf amt.Confirmation
		if ch.level != 0 {
			conf = confirmation(amt.OpHostName, host)
		}
		if err := sess.SetGeneral(c, conf); err != nil {
			return "", nil, err
		}
		var w []string
		if c.HostName != nil || c.DomainName != nil {
			w = append(w, "Favorites that use the old name stop working until you edit them in the OpenLAMT app.")
		}
		return acceptedReadAgain, w, nil
	})
	return nil, out, err
}

type setFeaturesInput struct {
	Device      string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	KVM         *bool  `json:"kvm,omitempty" jsonschema:"KVM (remote screen) on or off"`
	IDER        *bool  `json:"ider,omitempty" jsonschema:"media redirection (IDE-R) on or off"`
	SOL         *bool  `json:"sol,omitempty" jsonschema:"Serial over LAN (SOL) on or off"`
	UserConsent string `json:"user_consent,omitempty" jsonschema:"none, kvm (consent for KVM only) or all (consent for every redirection and boot)"`
}

func setFeaturesSchema() *jsonschema.Schema {
	sc, err := jsonschema.For[setFeaturesInput](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["user_consent"].Enum = []any{"none", "kvm", "all"}
	return sc
}

func (s *server) setRemoteFeatures(ctx context.Context, req *mcp.CallToolRequest, in setFeaturesInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	fc := amt.FeatureChange{KVM: in.KVM, IDER: in.IDER, SOL: in.SOL}
	var parts []string
	off := false
	for _, f := range []struct {
		name string
		v    *bool
	}{{"KVM", in.KVM}, {"media redirection (IDE-R)", in.IDER}, {"Serial over LAN (SOL)", in.SOL}} {
		if f.v != nil {
			parts = append(parts, "turn "+f.name+" "+onOff(*f.v))
			off = off || !*f.v
		}
	}
	var policy *amt.ConsentPolicy
	if p := strings.TrimSpace(in.UserConsent); p != "" {
		for v, name := range consentNames {
			if name == p {
				policy = &v
			}
		}
		if policy == nil {
			return nil, writeOutput{}, refuse("user_consent must be none, kvm or all.")
		}
		parts = append(parts, "set user consent to "+p)
	}
	if len(parts) == 0 {
		return nil, writeOutput{}, refuse("Give at least one of kvm, ider, sol or user_consent.")
	}
	what := strings.Join(parts, ", ")
	rec.action = what
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_remote_features", in: in, what: what}
	if off {
		ch.level, ch.warning = amt.LevelOrange, riskWarnings[amt.OpFeaturesOff]
	}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		if fc != (amt.FeatureChange{}) {
			var conf amt.Confirmation
			if off {
				conf = confirmation(amt.OpFeaturesOff, host)
			}
			if err := sess.SetFeatures(fc, conf); err != nil {
				return "", nil, err
			}
		}
		if policy != nil {
			if err := sess.SetConsent(*policy); err != nil {
				if fc != (amt.FeatureChange{}) {
					return "", nil, fmt.Errorf("the features were changed, the user consent was not: %w", err)
				}
				return "", nil, err
			}
		}
		var w []string
		if off {
			w = append(w, sessionsEnd+" kvm_connect, sol_connect and media_mount turn a feature that is off on again (not in read_only).")
		}
		return acceptedReadAgain, w, nil
	})
	return nil, out, err
}

// sessionsEnd warns that a change ends open redirection sessions, also this server's.
const sessionsEnd = "Open KVM, serial console and media sessions to the device may have ended, those of this server included: " +
	"call kvm_connect, sol_connect or media_mount again where needed."

func (s *server) syncClock(ctx context.Context, req *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "sync_clock", in: in, what: "set the AMT clock to the time of this computer"}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, _ string) (string, []string, error) {
		if err := sess.SyncClock(); err != nil {
			return "", nil, err
		}
		return "The AMT clock now runs on the time of this computer.", nil, nil
	})
	return nil, out, err
}

type setTLSInput struct {
	Device        string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	CertificateID string `json:"certificate_id" jsonschema:"id of a usable certificate from get_settings (tls.certificates)"`
}

func (s *server) setTLSCertificate(ctx context.Context, req *mcp.CallToolRequest, in setTLSInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	id := strings.TrimSpace(in.CertificateID)
	if id == "" {
		return nil, writeOutput{}, refuse("certificate_id is required: take the id of a usable certificate from get_settings.")
	}
	what := fmt.Sprintf("make AMT present the certificate «%s» for TLS", visible(id))
	rec.action = "certificate " + quoteShort(id)
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_tls_certificate", in: in, level: amt.LevelOrange, what: what, warning: riskWarnings[amt.OpTLS]}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		certs, err := sess.TLSCertificates()
		if err != nil {
			return "", nil, err
		}
		i := slices.IndexFunc(certs, func(c amt.Certificate) bool { return c.ID == id })
		switch {
		case i < 0:
			return "", nil, refuse("The device has no certificate %s. Take the id of a usable certificate from get_settings.", quoteShort(id))
		case certs[i].TLS:
			return "", nil, refuse("AMT presents this certificate already. Nothing was written.")
		case !certs[i].Usable():
			return "", nil, refuse("This certificate cannot serve TLS (AMT holds no private key for it, or it is a trusted root). " +
				"Take a usable one from get_settings.")
		}
		old := sess.Fingerprint()
		err = sess.SetTLS(amt.TLSChange{Certificate: &id}, confirmation(amt.OpTLS, host), s.logins)
		var ns *session.NotSavedError
		switch {
		case errors.As(err, &ns):
			return "AMT presents the new certificate now.", []string{sessionsEnd, fmt.Sprintf("Saving the new certificate pin (%s) failed: %s. "+
				"The next connection to the device is refused until you accept the new certificate in the OpenLAMT app.",
				amt.FormatFingerprint(ns.Fingerprint), explain(ns.Err))}, nil
		case err != nil:
			return "", nil, err
		}
		if fp := sess.Fingerprint(); fp != "" && fp != old {
			return fmt.Sprintf("AMT presents the new certificate now. The saved certificate pin of the device follows it (%s).",
				amt.FormatFingerprint(fp)), []string{sessionsEnd}, nil
		}
		return "AMT presents the new certificate now.", []string{sessionsEnd}, nil
	})
	return nil, out, err
}

type setNetworkInput struct {
	Device       string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Mode         string `json:"mode" jsonschema:"dhcp, or static for the address given here (IP sync with the operating system is turned off)"`
	IPAddress    string `json:"ip_address,omitempty" jsonschema:"static only: IPv4 address"`
	SubnetMask   string `json:"subnet_mask,omitempty" jsonschema:"static only: e.g. 255.255.255.0"`
	Gateway      string `json:"gateway,omitempty" jsonschema:"static only: default gateway in the subnet; empty for none"`
	PrimaryDNS   string `json:"primary_dns,omitempty" jsonschema:"static only: empty for none"`
	SecondaryDNS string `json:"secondary_dns,omitempty" jsonschema:"static only: empty for none"`
	Confirm      string `json:"confirm,omitempty" jsonschema:"required: the device name again, to show that this device is meant: the change can lock OpenLAMT out of it"`
}

func setNetworkSchema() *jsonschema.Schema {
	sc, err := jsonschema.For[setNetworkInput](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["mode"].Enum = []any{"dhcp", "static"}
	return sc
}

// networkChange is the change of the app's network form: DHCP, or every address field as given
// with DHCP and IP sync off.
func (in setNetworkInput) networkChange() (amt.NetworkChange, string, error) {
	switch strings.TrimSpace(in.Mode) {
	case "dhcp":
		on := true
		return amt.NetworkChange{DHCP: &on}, "switch the wired AMT port to DHCP", nil
	case "static":
	default:
		return amt.NetworkChange{}, "", refuse("mode must be dhcp or static.")
	}
	off := false
	v := func(s string) *string { s = strings.TrimSpace(s); return &s }
	c := amt.NetworkChange{DHCP: &off, IPSync: &off, IPAddress: v(in.IPAddress), SubnetMask: v(in.SubnetMask),
		Gateway: v(in.Gateway), PrimaryDNS: v(in.PrimaryDNS), SecondaryDNS: v(in.SecondaryDNS)}
	// Every field is given, so the check does not depend on the device's current values.
	if err := c.Validate(amt.NetworkSettings{DHCP: true}); err != nil {
		return amt.NetworkChange{}, "", err
	}
	none := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	return c, fmt.Sprintf("give the wired AMT port the static address %s, subnet mask %s, gateway %s, DNS %s and %s",
		*c.IPAddress, *c.SubnetMask, none(*c.Gateway), none(*c.PrimaryDNS), none(*c.SecondaryDNS)), nil
}

func (s *server) setNetwork(ctx context.Context, req *mcp.CallToolRequest, in setNetworkInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	c, what, err := in.networkChange()
	if err != nil {
		return nil, writeOutput{}, err
	}
	rec.action = what
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_network", in: in, level: amt.LevelRed, what: what, warning: riskWarnings[amt.OpNetwork]}
	if res, err := s.confirmChange(req, ch, fav, in.Confirm); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		if err := sess.SetNetwork(c, confirmation(amt.OpNetwork, host)); err != nil {
			return "", nil, err
		}
		return acceptedReadAgain, []string{"If the address of the device changed, favorites that use the old address stop working " +
			"until you edit them in the OpenLAMT app.", sessionsEnd}, nil
	})
	return nil, out, err
}

type setEnvironmentInput struct {
	Device  string   `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Domains []string `json:"domains" jsonschema:"intranet domain suffixes, at most 5; an empty list turns environment detection off"`
	Confirm string   `json:"confirm,omitempty" jsonschema:"required: the device name again, to show that this device is meant: the change can lock OpenLAMT out of it"`
}

func (s *server) setEnvironmentDetection(ctx context.Context, req *mcp.CallToolRequest, in setEnvironmentInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	domains := make([]string, 0, len(in.Domains))
	for _, d := range in.Domains {
		domains = append(domains, strings.TrimSpace(d))
	}
	if err := amt.ValidateEnvironmentDomains(domains); err != nil {
		return nil, writeOutput{}, err
	}
	what := "turn environment detection off"
	if len(domains) > 0 {
		what = "set the intranet domains of environment detection to " + strings.Join(domains, ", ")
	}
	rec.action = what
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_environment_detection", in: in, level: amt.LevelRed, what: what, warning: riskWarnings[amt.OpEnvironment]}
	if res, err := s.confirmChange(req, ch, fav, in.Confirm); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		if err := sess.SetEnvironmentDetection(domains, confirmation(amt.OpEnvironment, host)); err != nil {
			return "", nil, err
		}
		return acceptedReadAgain, nil, nil
	})
	return nil, out, err
}

type confirmInput struct {
	Device  string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Confirm string `json:"confirm,omitempty" jsonschema:"required: the device name again, to show that this device is meant: the change can lock OpenLAMT out of it"`
}

func (s *server) disableAMTNetwork(ctx context.Context, req *mcp.CallToolRequest, in confirmInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "disable_amt_network", in: in, level: amt.LevelRed, what: "turn AMT off on the network",
		warning: riskWarnings[amt.OpDisableNetwork]}
	if res, err := s.confirmChange(req, ch, fav, in.Confirm); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.settingsWrite(ctx, in.Device, func(sess *session.Session, host string) (string, []string, error) {
		if err := sess.DisableAMTNetwork(confirmation(amt.OpDisableNetwork, host)); err != nil {
			return "", nil, err
		}
		return "AMT is off on the network. The device no longer answers; only the BIOS (MEBx) at the device turns it back on.", nil, nil
	})
	return nil, out, err
}
