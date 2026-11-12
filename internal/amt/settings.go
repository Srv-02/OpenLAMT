package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Settings is the AMT configuration of a device, read in one call (about 0.4 s on AMT 16).
// A section whose class the firmware lacks has Available false.
type Settings struct {
	General     GeneralSettings
	Network     NetworkSettings
	Features    Features
	Consent     ConsentSettings
	TLS         TLSSettings
	Clock       ClockInfo
	Environment EnvironmentDetection
}

// DDNSMode is how the device's DNS name is registered.
type DDNSMode int

const (
	DDNSOff          DDNSMode = iota // no dynamic DNS
	DDNSByDHCPServer                 // the DHCP server registers the name (DDNSUpdateByDHCPServerEnabled)
	DDNSClient                       // the AMT DDNS client registers it (DDNSUpdateEnabled)
)

// GeneralSettings comes from AMT_GeneralSettings. Writable through SetGeneral: HostName,
// DomainName, SharedFQDN, PingResponse, RMCPPingResponse, DynamicDNS, DDNSIntervalMinutes,
// DDNSTTLSeconds. The rest is read-only here.
type GeneralSettings struct {
	HostName            string
	DomainName          string
	SharedFQDN          bool   // the FQDN is shared with the operating system
	HostOSFQDN          string // FQDN of the operating system
	DigestRealm         string
	PingResponse        bool // ICMP echo
	RMCPPingResponse    bool
	DynamicDNS          DDNSMode
	DDNSIntervalMinutes int // periodic DDNS update, 0 = off
	DDNSTTLSeconds      int

	NetworkEnabled          bool // AMTNetworkEnabled
	NetworkInterfaceEnabled bool
	WSManOnly               bool
	IdleWakeTimeoutMinutes  int
	PrivacyLevel            int // 0 default, 1 enhanced, 2 extreme
	PowerSource             int // 0 AC, 1 DC
}

// NetworkSettings is the wired AMT_EthernetPortSettings instance. Writable through SetNetwork.
type NetworkSettings struct {
	Available    bool
	DHCP         bool
	IPSync       bool // IpSyncEnabled: AMT uses the static address of the operating system
	IPAddress    string
	SubnetMask   string
	Gateway      string
	PrimaryDNS   string
	SecondaryDNS string
	MACAddress   string
	LinkUp       bool
	SharedMAC    bool
}

// Features are the redirection services (AMT_RedirectionService, CIM_KVMRedirectionSAP).
// Writable through SetFeatures.
type Features struct {
	Redirection  bool // ListenerEnabled: the redirection port, needed by SOL, IDE-R and KVM
	SOL          bool
	IDER         bool
	KVM          bool
	KVMAvailable bool // the firmware offers KVM
}

// ConsentPolicy is IPS_OptInService.OptInRequired.
type ConsentPolicy int

const (
	ConsentNone ConsentPolicy = iota // no user consent
	ConsentKVM                       // consent for KVM only
	ConsentAll                       // consent for all redirection and boot operations
)

// ConsentSettings comes from IPS_OptInService. Policy is writable through SetConsent when
// CanModify is true.
type ConsentSettings struct {
	Available             bool
	Policy                ConsentPolicy
	CanModify             bool
	CodeTimeoutSeconds    int
	DisplayTimeoutSeconds int
}

// TLSSettings comes from AMT_TLSSettingData (read-only).
type TLSSettings struct {
	Remote TLSInterface // network interface ("802.3")
	Local  TLSInterface // local interface (LMS)
}

type TLSInterface struct {
	Available          bool
	Enabled            bool
	MutualAuth         bool
	AcceptNonSecure    bool
	NonSecureSupported bool
	TrustedCNs         []string
}

// ClockInfo is the AMT clock (GetLowAccuracyTimeSynch). Set it with SyncClock.
type ClockInfo struct {
	Available bool
	Time      time.Time     // UTC, whole seconds
	Offset    time.Duration // device clock minus local clock at the time of reading
}

// EnvironmentDetection comes from AMT_EnvironmentDetectionSettingData. Writable through
// SetEnvironmentDetection.
type EnvironmentDetection struct {
	Available bool
	Domains   []string // intranet domain suffixes; empty = detection off
}

// timeNow is replaced in tests.
var timeNow = time.Now

const (
	classGeneral     = "AMT_GeneralSettings"
	classEthernet    = "AMT_EthernetPortSettings"
	classRedirection = "AMT_RedirectionService"
	classKVMSAP      = "CIM_KVMRedirectionSAP"
	classOptIn       = "IPS_OptInService"
	classTLS         = "AMT_TLSSettingData"
	classTimeSync    = "AMT_TimeSynchronizationService"
	classEnvDetect   = "AMT_EnvironmentDetectionSettingData"
	classAuth        = "AMT_AuthorizationService"

	wirelessConnection = 3 // PhysicalConnectionType "Wireless LAN"
	redirectionBase    = 32768
	optInAll           = 4294967295
)

// isMissingClass reports a SOAP fault other than AccessDenied: the class does not exist on
// this firmware.
func isMissingClass(err error) bool {
	var amtErr *amterror.AMTError
	return errors.As(err, &amtErr) && !strings.Contains(amtErr.SubCode, "AccessDenied")
}

// readError wraps a read failure of class: AccessDenied → ErrAccessDenied, else the class name.
func readError(class string, err error) error {
	if denied := accessDenied(err, "settings: "+class); errors.Is(denied, ErrAccessDenied) {
		return denied
	}
	return fmt.Errorf("settings: %s: %w", class, err)
}

// getObject runs a library Get and parses the instance of class from the raw response.
func getObject(class string, get func() (*client.Message, error)) (*cimObject, error) {
	m, err := get()
	if err != nil {
		return nil, err
	}
	objs, err := parseCIMObjects(xmlOf(m), class)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrIncompleteResponse, class)
	}
	return objs[0], nil
}

// pullObjects enumerates every instance of class.
//
// core marks a class every supported firmware has: the DestinationUnreachable fault of the
// Enumerate call (never of a Pull) then means a missing permission and yields ErrAccessDenied.
func pullObjects(class string, core bool, enum func() (*client.Message, string, error), pull func(string) (*client.Message, error)) ([]*cimObject, error) {
	return enumeratePull(func() (string, error) {
		_, ctx, err := enum()
		if core && coreClassDenied(err) {
			return "", fmt.Errorf("settings: %s: %w", class, ErrAccessDenied)
		}
		return ctx, err
	}, func(ctx string) ([]*cimObject, string, error) {
		m, err := pull(ctx)
		if err != nil {
			return nil, "", err
		}
		objs, err := parseCIMObjects(xmlOf(m), class)
		return objs, xmlOf(m), err
	})
}

func (d *Device) generalObject() (*cimObject, error) {
	return getObject(classGeneral, func() (*client.Message, error) {
		r, err := d.msgs.AMT.GeneralSettings.Get()
		return r.Message, err
	})
}

// wiredObject returns the first port that is not wireless, nil if there is none.
func (d *Device) wiredObject() (*cimObject, error) {
	objs, err := pullObjects(classEthernet, true, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.EthernetPortSettings.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.EthernetPortSettings.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return nil, err
	}
	for _, o := range objs {
		if o.integer("PhysicalConnectionType") != wirelessConnection {
			return o, nil
		}
	}
	return nil, nil
}

func (d *Device) redirectionObject() (*cimObject, error) {
	return getObject(classRedirection, func() (*client.Message, error) {
		r, err := d.msgs.AMT.RedirectionService.Get()
		return r.Message, err
	})
}

func (d *Device) kvmSAPObject() (*cimObject, error) {
	return getObject(classKVMSAP, func() (*client.Message, error) {
		r, err := d.msgs.CIM.KVMRedirectionSAP.Get()
		return r.Message, err
	})
}

func (d *Device) optInObject() (*cimObject, error) {
	return getObject(classOptIn, func() (*client.Message, error) {
		r, err := d.msgs.IPS.OptInService.Get()
		return r.Message, err
	})
}

func (d *Device) envDetectObject() (*cimObject, error) {
	return getObject(classEnvDetect, func() (*client.Message, error) {
		r, err := d.msgs.AMT.EnvironmentDetectionSettingData.Get()
		return r.Message, err
	})
}

func (d *Device) tlsObjects() ([]*cimObject, error) {
	return pullObjects(classTLS, true, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.TLSSettingData.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.TLSSettingData.Pull(ctx)
		return r.Message, err
	})
}

func generalFrom(o *cimObject) GeneralSettings {
	g := GeneralSettings{
		HostName:                o.str("HostName"),
		DomainName:              o.str("DomainName"),
		SharedFQDN:              o.boolean("SharedFQDN"),
		HostOSFQDN:              o.str("HostOSFQDN"),
		DigestRealm:             o.str("DigestRealm"),
		PingResponse:            o.boolean("PingResponseEnabled"),
		RMCPPingResponse:        o.boolean("RmcpPingResponseEnabled"),
		DDNSIntervalMinutes:     o.integer("DDNSPeriodicUpdateInterval"),
		DDNSTTLSeconds:          o.integer("DDNSTTL"),
		NetworkEnabled:          o.integer("AMTNetworkEnabled") == 1,
		NetworkInterfaceEnabled: o.boolean("NetworkInterfaceEnabled"),
		WSManOnly:               o.boolean("WsmanOnlyMode"),
		IdleWakeTimeoutMinutes:  o.integer("IdleWakeTimeout"),
		PrivacyLevel:            o.integer("PrivacyLevel"),
		PowerSource:             o.integer("PowerSource"),
	}
	switch {
	case o.boolean("DDNSUpdateEnabled"):
		g.DynamicDNS = DDNSClient
	case o.boolean("DDNSUpdateByDHCPServerEnabled"):
		g.DynamicDNS = DDNSByDHCPServer
	}
	return g
}

func networkFrom(o *cimObject) NetworkSettings {
	if o == nil {
		return NetworkSettings{}
	}
	return NetworkSettings{
		Available:    true,
		DHCP:         o.boolean("DHCPEnabled"),
		IPSync:       o.boolean("IpSyncEnabled"),
		IPAddress:    o.str("IPAddress"),
		SubnetMask:   o.str("SubnetMask"),
		Gateway:      o.str("DefaultGateway"),
		PrimaryDNS:   o.str("PrimaryDNS"),
		SecondaryDNS: o.str("SecondaryDNS"),
		MACAddress:   o.str("MACAddress"),
		LinkUp:       o.boolean("LinkIsUp"),
		SharedMAC:    o.boolean("SharedMAC"),
	}
}

// featuresFrom decodes the redirection service and the KVM SAP (nil if absent).
func featuresFrom(redir, kvm *cimObject) Features {
	f := Features{Redirection: redir.boolean("ListenerEnabled")}
	if s := redir.integer("EnabledState"); s >= redirectionBase && s <= redirectionBase+3 {
		f.IDER = s&1 != 0
		f.SOL = s&2 != 0
	}
	if kvm != nil {
		f.KVMAvailable = true
		s := kvm.integer("EnabledState")
		f.KVM = s == 2 || s == 6 // Enabled, Enabled but Offline
	}
	return f
}

func consentFrom(o *cimObject) ConsentSettings {
	c := ConsentSettings{
		Available:             true,
		CanModify:             o.integer("CanModifyOptInPolicy") == 1,
		CodeTimeoutSeconds:    o.integer("OptInCodeTimeout"),
		DisplayTimeoutSeconds: o.integer("OptInDisplayTimeout"),
	}
	switch o.integer("OptInRequired") {
	case 0:
		c.Policy = ConsentNone
	case 1:
		c.Policy = ConsentKVM
	default:
		c.Policy = ConsentAll
	}
	return c
}

func tlsFrom(objs []*cimObject) TLSSettings {
	var t TLSSettings
	for _, o := range objs {
		i := TLSInterface{
			Available:          true,
			Enabled:            o.boolean("Enabled"),
			MutualAuth:         o.boolean("MutualAuthentication"),
			AcceptNonSecure:    o.boolean("AcceptNonSecureConnections"),
			NonSecureSupported: o.boolean("NonSecureConnectionsSupported"),
			TrustedCNs:         o.strs("TrustedCN"),
		}
		if strings.Contains(o.str("InstanceID"), "LMS") {
			t.Local = i
		} else {
			t.Remote = i
		}
	}
	return t
}

// readClock returns the AMT clock. A non-zero ReturnValue is a *MethodError.
func (d *Device) readClock() (time.Time, error) {
	r, err := d.msgs.AMT.TimeSynchronizationService.GetLowAccuracyTimeSynch()
	if err != nil {
		return time.Time{}, err
	}
	var out struct {
		Ta0 *int64 `xml:"Body>GetLowAccuracyTimeSynch_OUTPUT>Ta0"`
		RV  *int   `xml:"Body>GetLowAccuracyTimeSynch_OUTPUT>ReturnValue"`
	}
	if err := xml.Unmarshal([]byte(xmlOf(r.Message)), &out); err != nil || out.RV == nil {
		return time.Time{}, fmt.Errorf("%w: GetLowAccuracyTimeSynch_OUTPUT", ErrIncompleteResponse)
	}
	if *out.RV != 0 || out.Ta0 == nil {
		return time.Time{}, &MethodError{Method: "GetLowAccuracyTimeSynch", ReturnValue: *out.RV}
	}
	return time.Unix(*out.Ta0, 0).UTC(), nil
}

// Settings reads general, network, redirection, consent, TLS, clock and environment detection
// settings. Nothing is written.
func (d *Device) Settings() (Settings, error) {
	var s Settings

	g, err := d.generalObject()
	if err != nil {
		return Settings{}, readError(classGeneral, err)
	}
	s.General = generalFrom(g)

	// optional reads one after the other; a missing class leaves its section empty
	optional := func(class string, read func() error) error {
		err := read()
		if err == nil || isMissingClass(err) {
			return nil
		}
		return readError(class, err)
	}

	steps := []struct {
		class string
		read  func() error
	}{
		{classEthernet, func() error {
			o, err := d.wiredObject()
			s.Network = networkFrom(o)
			return err
		}},
		{classRedirection, func() error {
			redir, err := d.redirectionObject()
			if err != nil {
				return err
			}
			kvm, err := d.kvmSAPObject()
			if err != nil && !isMissingClass(err) {
				return err
			}
			s.Features = featuresFrom(redir, kvm)
			return nil
		}},
		{classOptIn, func() error {
			o, err := d.optInObject()
			if err == nil {
				s.Consent = consentFrom(o)
			}
			return err
		}},
		{classTLS, func() error {
			objs, err := d.tlsObjects()
			s.TLS = tlsFrom(objs)
			return err
		}},
		{classTimeSync, func() error {
			before := timeNow()
			t, err := d.readClock()
			var me *MethodError
			if errors.As(err, &me) {
				return nil
			}
			if err != nil {
				return err
			}
			after := timeNow()
			local := before.Add(after.Sub(before) / 2)
			s.Clock = ClockInfo{Available: true, Time: t, Offset: t.Sub(local).Round(time.Second)}
			return nil
		}},
		{classEnvDetect, func() error {
			o, err := d.envDetectObject()
			if err == nil {
				s.Environment = EnvironmentDetection{Available: true, Domains: o.strs("DetectionStrings")}
			}
			return err
		}},
	}
	for _, st := range steps {
		if err := optional(st.class, st.read); err != nil {
			return Settings{}, err
		}
	}
	return s, nil
}
