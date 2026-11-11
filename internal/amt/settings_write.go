package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	redirection "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/redirection"
	cimkvm "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/kvm"
)

// Every setter reads the current instance first, applies only the fields of the change that
// differ, and writes the complete instance back (Put). Without a difference nothing is written.
// Risky changes without confirmRisky return *RiskyChangeError and write nothing.

// writeError wraps a failed read or write of op. Validation, risk and method errors pass as is.
func writeError(op string, err error) error {
	if err == nil {
		return nil
	}
	if denied := accessDenied(err, op); errors.Is(denied, ErrAccessDenied) {
		return denied
	}
	return fmt.Errorf("%s: %w", op, err)
}

// changeTracker applies typed values to a cimObject and remembers whether anything changed.
type changeTracker struct {
	o       *cimObject
	changed bool
}

func (c *changeTracker) str(prop string, v *string) {
	if v != nil && (!c.o.has(prop) || c.o.str(prop) != *v) {
		c.o.set(prop, *v)
		c.changed = true
	}
}

func (c *changeTracker) boolean(prop string, v *bool) {
	if v != nil && (!c.o.has(prop) || c.o.boolean(prop) != *v) {
		c.o.set(prop, strconv.FormatBool(*v))
		c.changed = true
	}
}

func (c *changeTracker) integer(prop string, v *int) {
	if v != nil && (!c.o.has(prop) || c.o.integer(prop) != *v) {
		c.o.set(prop, strconv.Itoa(*v))
		c.changed = true
	}
}

// GeneralChange changes AMT_GeneralSettings; nil fields stay as they are. Not risky.
type GeneralChange struct {
	HostName            *string // up to 63 characters: letters, digits, hyphen; "" clears it
	DomainName          *string // up to 191 characters, dot-separated labels; "" clears it
	SharedFQDN          *bool
	PingResponse        *bool
	RMCPPingResponse    *bool
	DynamicDNS          *DDNSMode
	DDNSIntervalMinutes *int // 0 (off) or at least 20; used in DDNSClient mode
	DDNSTTLSeconds      *int // at least 1; used in DDNSClient mode
}

func validLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// validDomain checks a DNS name of dot-separated labels (no trailing dot) up to max bytes.
func validDomain(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if !validLabel(l) {
			return false
		}
	}
	return true
}

// Validate checks the change without device access.
func (c GeneralChange) Validate() error {
	if c.HostName != nil && *c.HostName != "" && !validLabel(*c.HostName) {
		return invalid("HostName", "up to 63 letters, digits or hyphens, not starting or ending with a hyphen")
	}
	if c.DomainName != nil && *c.DomainName != "" && !validDomain(*c.DomainName, 191) {
		return invalid("DomainName", "up to 191 characters of dot-separated labels")
	}
	if c.DynamicDNS != nil && (*c.DynamicDNS < DDNSOff || *c.DynamicDNS > DDNSClient) {
		return invalid("DynamicDNS", "unknown mode")
	}
	if c.DDNSIntervalMinutes != nil && *c.DDNSIntervalMinutes != 0 && (*c.DDNSIntervalMinutes < 20 || *c.DDNSIntervalMinutes > 1<<31-1) {
		return invalid("DDNSIntervalMinutes", "0 or at least 20")
	}
	if c.DDNSTTLSeconds != nil && (*c.DDNSTTLSeconds < 1 || *c.DDNSTTLSeconds > 1<<31-1) {
		return invalid("DDNSTTLSeconds", "between 1 and 2147483647")
	}
	return nil
}

// SetGeneral writes host name, domain, ping response and dynamic DNS settings.
func (d *Device) SetGeneral(c GeneralChange) error {
	const op = "set general settings"
	if err := c.Validate(); err != nil {
		return err
	}
	o, err := d.generalObject()
	if err != nil {
		return writeError(op, err)
	}
	t := changeTracker{o: o}
	t.str("HostName", c.HostName)
	t.str("DomainName", c.DomainName)
	t.boolean("SharedFQDN", c.SharedFQDN)
	t.boolean("PingResponseEnabled", c.PingResponse)
	t.boolean("RmcpPingResponseEnabled", c.RMCPPingResponse)
	if c.DynamicDNS != nil {
		client, byDHCP := *c.DynamicDNS == DDNSClient, *c.DynamicDNS == DDNSByDHCPServer
		t.boolean("DDNSUpdateEnabled", &client)
		t.boolean("DDNSUpdateByDHCPServerEnabled", &byDHCP)
	}
	t.integer("DDNSPeriodicUpdateInterval", c.DDNSIntervalMinutes)
	t.integer("DDNSTTL", c.DDNSTTLSeconds)
	if !t.changed {
		return nil
	}
	return writeError(op, d.putObject(o, nil))
}

// NetworkChange changes the wired port; nil fields stay as they are. With DHCP or IPSync the
// address fields are not written (the DHCP server or the operating system provides them), as
// in MeshCommander. Every effective change is risky (RiskNetwork).
type NetworkChange struct {
	DHCP         *bool
	IPSync       *bool
	IPAddress    *string
	SubnetMask   *string
	Gateway      *string // "" = none
	PrimaryDNS   *string // "" = none
	SecondaryDNS *string // "" = none
}

// Apply returns the settings the change leads to.
func (c NetworkChange) Apply(cur NetworkSettings) NetworkSettings {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	n := cur
	if c.DHCP != nil {
		n.DHCP = *c.DHCP
	}
	if c.IPSync != nil {
		n.IPSync = *c.IPSync
	}
	set(&n.IPAddress, c.IPAddress)
	set(&n.SubnetMask, c.SubnetMask)
	set(&n.Gateway, c.Gateway)
	set(&n.PrimaryDNS, c.PrimaryDNS)
	set(&n.SecondaryDNS, c.SecondaryDNS)
	return n
}

// static reports whether the addresses of n are written (no DHCP, no IP sync).
func (n NetworkSettings) static() bool { return !n.DHCP && !n.IPSync }

func sameNetwork(a, b NetworkSettings) bool {
	if a.DHCP != b.DHCP || a.IPSync != b.IPSync {
		return false
	}
	if !a.static() {
		return true
	}
	return a.IPAddress == b.IPAddress && a.SubnetMask == b.SubnetMask && a.Gateway == b.Gateway &&
		a.PrimaryDNS == b.PrimaryDNS && a.SecondaryDNS == b.SecondaryDNS
}

// Risky returns [RiskNetwork] if the change differs from cur, else nil.
func (c NetworkChange) Risky(cur NetworkSettings) []string {
	if sameNetwork(cur, c.Apply(cur)) {
		return nil
	}
	return []string{RiskNetwork}
}

func parseIPv4(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	return a, err == nil && a.Is4()
}

// maskBits returns the prefix length of a contiguous IPv4 mask, -1 if it is none.
func maskBits(s string) int {
	a, ok := parseIPv4(s)
	if !ok {
		return -1
	}
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n := 0
	for v&0x80000000 != 0 {
		n++
		v <<= 1
	}
	if v != 0 || n == 0 {
		return -1
	}
	return n
}

// Validate checks the resulting static configuration; with DHCP or IP sync nothing is checked.
func (c NetworkChange) Validate(cur NetworkSettings) error {
	n := c.Apply(cur)
	if !n.static() {
		return nil
	}
	ip, ok := parseIPv4(n.IPAddress)
	if !ok {
		return invalid("IPAddress", "an IPv4 address")
	}
	bits := maskBits(n.SubnetMask)
	if bits < 0 {
		return invalid("SubnetMask", "a contiguous IPv4 mask such as 255.255.255.0")
	}
	prefix := netip.PrefixFrom(ip, bits).Masked()
	if bits < 31 {
		b := prefix.Addr().As4()
		hostBits := uint32(1)<<(32-bits) - 1
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		i := ip.As4()
		h := uint32(i[0])<<24 | uint32(i[1])<<16 | uint32(i[2])<<8 | uint32(i[3])
		if h == v || h == v|hostBits {
			return invalid("IPAddress", "the network or broadcast address of the subnet")
		}
	}
	if n.Gateway != "" {
		gw, ok := parseIPv4(n.Gateway)
		if !ok || !prefix.Contains(gw) {
			return invalid("Gateway", "an IPv4 address in the subnet of IPAddress")
		}
	}
	for field, v := range map[string]string{"PrimaryDNS": n.PrimaryDNS, "SecondaryDNS": n.SecondaryDNS} {
		if _, ok := parseIPv4(v); v != "" && !ok {
			return invalid(field, "an IPv4 address")
		}
	}
	return nil
}

var networkAddressProps = []string{"IPAddress", "SubnetMask", "DefaultGateway", "PrimaryDNS", "SecondaryDNS"}

// SetNetwork writes the wired port's IPv4 configuration. Risky: confirmRisky must be true.
func (d *Device) SetNetwork(c NetworkChange, confirmRisky bool) error {
	const op = "set network settings"
	o, err := d.wiredObject()
	if err != nil {
		return writeError(op, err)
	}
	if o == nil {
		return fmt.Errorf("%s: no wired port: %w", op, ErrNotModifiable)
	}
	cur := networkFrom(o)
	if err := c.Validate(cur); err != nil {
		return err
	}
	if risky := c.Risky(cur); risky == nil {
		return nil
	} else if !confirmRisky {
		return &RiskyChangeError{Fields: risky}
	}
	n := c.Apply(cur)
	o.set("DHCPEnabled", strconv.FormatBool(n.DHCP))
	if o.has("IpSyncEnabled") || c.IPSync != nil {
		o.set("IpSyncEnabled", strconv.FormatBool(n.IPSync))
	}
	for _, p := range networkAddressProps {
		o.del(p)
	}
	if n.static() {
		for i, v := range []string{n.IPAddress, n.SubnetMask, n.Gateway, n.PrimaryDNS, n.SecondaryDNS} {
			if v != "" {
				o.set(networkAddressProps[i], v)
			}
		}
	}
	return writeError(op, d.putObject(o, []selector{{"InstanceID", o.str("InstanceID")}}))
}

// FeatureChange switches redirection services; nil fields stay as they are. Switching a
// service off is risky (an open KVM, SOL or IDE-R session ends).
type FeatureChange struct {
	Redirection *bool
	SOL         *bool
	IDER        *bool
	KVM         *bool
}

// Apply returns the features the change leads to.
func (c FeatureChange) Apply(cur Features) Features {
	n := cur
	for _, p := range []struct {
		dst *bool
		v   *bool
	}{{&n.Redirection, c.Redirection}, {&n.SOL, c.SOL}, {&n.IDER, c.IDER}, {&n.KVM, c.KVM}} {
		if p.v != nil {
			*p.dst = *p.v
		}
	}
	return n
}

// Risky lists the services the change switches off, in the order Redirection, SOL, IDER, KVM.
func (c FeatureChange) Risky(cur Features) []string {
	n := c.Apply(cur)
	var out []string
	for _, p := range []struct {
		name     string
		cur, new bool
	}{{RiskRedirection, cur.Redirection, n.Redirection}, {RiskSOL, cur.SOL, n.SOL}, {RiskIDER, cur.IDER, n.IDER}, {RiskKVM, cur.KVM, n.KVM}} {
		if p.cur && !p.new {
			out = append(out, p.name)
		}
	}
	return out
}

// rscReturnValue reads RequestStateChange_OUTPUT/ReturnValue; ok is false if it is missing.
func rscReturnValue(xmlOutput string) (int, bool) {
	var out struct {
		RV *int `xml:"Body>RequestStateChange_OUTPUT>ReturnValue"`
	}
	if xml.Unmarshal([]byte(xmlOutput), &out) != nil || out.RV == nil {
		return 0, false
	}
	return *out.RV, true
}

// SetFeatures switches the redirection listener, SOL, IDE-R and KVM in MeshCommander's order:
// RequestStateChange of SOL/IDE-R, RequestStateChange of the KVM SAP, Put of the listener.
func (d *Device) SetFeatures(c FeatureChange, confirmRisky bool) error {
	const op = "set features"
	redir, err := d.redirectionObject()
	if err != nil {
		return writeError(op, err)
	}
	kvm, err := d.kvmSAPObject()
	if err != nil && !isMissingClass(err) {
		return writeError(op, err)
	}
	cur := featuresFrom(redir, kvm)
	if c.KVM != nil && !cur.KVMAvailable {
		return fmt.Errorf("%s: KVM: %w", op, ErrNotModifiable)
	}
	n := c.Apply(cur)
	if n == cur {
		return nil
	}
	if risky := c.Risky(cur); risky != nil && !confirmRisky {
		return &RiskyChangeError{Fields: risky}
	}

	state := redir.integer("EnabledState")
	if n.SOL != cur.SOL || n.IDER != cur.IDER {
		state = redirectionBase
		if n.IDER {
			state++
		}
		if n.SOL {
			state += 2
		}
		r, err := d.msgs.AMT.RedirectionService.RequestStateChange(redirection.RequestedState(state))
		rv, ok := rscReturnValue(xmlOf(r.Message))
		if ok && rv != 0 {
			return &MethodError{Method: "AMT_RedirectionService.RequestStateChange", ReturnValue: rv}
		}
		if err != nil {
			return writeError(op, err)
		}
		if !ok {
			return fmt.Errorf("%s: %w: RequestStateChange_OUTPUT", op, ErrIncompleteResponse)
		}
	}
	if n.KVM != cur.KVM {
		req := cimkvm.RedirectionSAPDisable
		if n.KVM {
			req = cimkvm.RedirectionSAPEnable
		}
		r, err := d.msgs.CIM.KVMRedirectionSAP.RequestStateChange(req)
		if err != nil {
			return writeError(op, err)
		}
		rv, ok := rscReturnValue(xmlOf(r.Message))
		if !ok {
			return fmt.Errorf("%s: %w: RequestStateChange_OUTPUT", op, ErrIncompleteResponse)
		}
		if rv != 0 {
			return &MethodError{Method: "CIM_KVMRedirectionSAP.RequestStateChange", ReturnValue: rv}
		}
	}
	if n.Redirection != cur.Redirection {
		redir.set("ListenerEnabled", strconv.FormatBool(n.Redirection))
		redir.set("EnabledState", strconv.Itoa(state))
		if err := d.putObject(redir, nil); err != nil {
			return writeError(op, err)
		}
	}
	return nil
}

// consentValue maps a ConsentPolicy to OptInRequired.
func consentValue(p ConsentPolicy) (int, bool) {
	switch p {
	case ConsentNone:
		return 0, true
	case ConsentKVM:
		return 1, true
	case ConsentAll:
		return optInAll, true
	}
	return 0, false
}

// SetConsent writes the user consent policy. Not risky; ErrNotModifiable if the device does
// not allow remote changes (CanModifyOptInPolicy 0).
func (d *Device) SetConsent(p ConsentPolicy) error {
	const op = "set user consent"
	v, ok := consentValue(p)
	if !ok {
		return invalid("Policy", "unknown consent policy")
	}
	o, err := d.optInObject()
	if err != nil {
		return writeError(op, err)
	}
	if o.integer("OptInRequired") == v {
		return nil
	}
	if o.integer("CanModifyOptInPolicy") != 1 {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	o.set("OptInRequired", strconv.Itoa(v))
	return writeError(op, d.putObject(o, nil))
}

// ValidateEnvironmentDomains checks up to 5 domain suffixes of up to 63 characters each.
func ValidateEnvironmentDomains(domains []string) error {
	if len(domains) > 5 {
		return invalid("Domains", "at most 5 domains")
	}
	for _, s := range domains {
		if len(s) > 63 || !validDomain(s, 63) {
			return invalid("Domains", "domain suffixes of up to 63 characters without spaces")
		}
	}
	return nil
}

// EnvironmentRisky returns [RiskEnvironment] if domains differ from cur: outside the listed
// domains AMT closes its local ports.
func EnvironmentRisky(cur EnvironmentDetection, domains []string) []string {
	if slices.Equal(cur.Domains, domains) || len(cur.Domains) == 0 && len(domains) == 0 {
		return nil
	}
	return []string{RiskEnvironment}
}

// SetEnvironmentDetection writes the intranet domain suffixes (nil or empty = detection off).
// Risky: confirmRisky must be true.
func (d *Device) SetEnvironmentDetection(domains []string, confirmRisky bool) error {
	const op = "set environment detection"
	if err := ValidateEnvironmentDomains(domains); err != nil {
		return err
	}
	o, err := d.envDetectObject()
	if err != nil {
		if isMissingClass(err) {
			return fmt.Errorf("%s: %w", op, ErrNotModifiable)
		}
		return writeError(op, err)
	}
	risky := EnvironmentRisky(EnvironmentDetection{Available: true, Domains: o.strs("DetectionStrings")}, domains)
	if risky == nil {
		return nil
	}
	if !confirmRisky {
		return &RiskyChangeError{Fields: risky}
	}
	o.setAll("DetectionStrings", domains)
	return writeError(op, d.putObject(o, []selector{{"InstanceID", o.str("InstanceID")}}))
}

// SyncClock sets the AMT clock to the local time (MeshCommander: SetHighAccuracyTimeSynch with
// Ta0 from GetLowAccuracyTimeSynch and Tm1 = Tm2 = now). Not risky.
func (d *Device) SyncClock() error {
	const op = "sync clock"
	ta0, err := d.readClock()
	if err != nil {
		var me *MethodError
		if errors.As(err, &me) {
			return err
		}
		return writeError(op, err)
	}
	now := timeNow().Unix()
	r, err := d.msgs.AMT.TimeSynchronizationService.SetHighAccuracyTimeSynch(ta0.Unix(), now, now)
	if err != nil {
		return writeError(op, err)
	}
	var out struct {
		RV *int `xml:"Body>SetHighAccuracyTimeSynch_OUTPUT>ReturnValue"`
	}
	if xml.Unmarshal([]byte(xmlOf(r.Message)), &out) != nil || out.RV == nil {
		return fmt.Errorf("%s: %w: SetHighAccuracyTimeSynch_OUTPUT", op, ErrIncompleteResponse)
	}
	if *out.RV != 0 {
		return &MethodError{Method: "SetHighAccuracyTimeSynch", ReturnValue: *out.RV}
	}
	return nil
}
