package amt

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/wifiportconfiguration"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/wifi"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// The Wi-Fi port of Intel AMT (issue #22): when AMT may use it (CIM_WiFiPort), the profile
// sync with the operating system and UEFI (AMT_WiFiPortConfigurationService) and the AMT
// profiles (CIM_WiFiEndpointSettings) with WPA, WPA2 or WPA3 Personal. 802.1X profiles are
// listed but cannot be created. Passphrases are only sent, never read back, logged or kept.

const (
	classWiFiPort         = "CIM_WiFiPort"
	classWiFiService      = "AMT_WiFiPortConfigurationService"
	classWiFiSettings     = "CIM_WiFiEndpointSettings"
	classWiFiCapabilities = "CIM_WiFiEndpointCapabilities"
	classWiFiEndpoint     = "CIM_WiFiEndpoint"

	actionDelete = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Delete"

	// wifiProfilePrefix starts the InstanceID of an AMT profile; the name follows (rpc-go).
	wifiProfilePrefix = "Intel(r) AMT:WiFi Endpoint Settings "
	// wifiUserProfile marks a profile synced from the operating system; the page does not list them.
	wifiUserProfile = "User Settings"
	// wifiEndpointName is the endpoint AddWiFiSettings refers to when the device names none.
	wifiEndpointName = "WiFi Endpoint 0"

	syncOff  = 0 // localProfileSynchronizationEnabled: no local sync
	syncUser = 1 // sync of the user's profiles (the value rpc-go and MeshCommander write)
)

// RiskWiFi is the RiskyChangeError field of a change that can cut the session over Wi-Fi.
const RiskWiFi = "WiFi.Connection"

// lookupHost resolves the session's host name; replaced in tests.
var lookupHost = func(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// lookupTimeout bounds the name lookup of the lockout check.
const lookupTimeout = 3 * time.Second

// WiFiState is CIM_WiFiPort.EnabledState: when Intel AMT uses the Wi-Fi port.
type WiFiState int

const (
	WiFiOff  WiFiState = WiFiState(wifi.EnabledStateWifiDisabled)      // never
	WiFiS0   WiFiState = WiFiState(wifi.EnabledStateWifiEnabledS0)     // while the host is on (S0)
	WiFiS0Sx WiFiState = WiFiState(wifi.EnabledStateWifiEnabledS0SxAC) // also in sleep and soft-off on AC power (Sx)
)

// WiFiSecurity is the security of a profile, the pair of authentication and encryption method.
type WiFiSecurity int

const (
	// WiFiSecurityOther is every other pair (open, WEP, 802.1X): listed, never created.
	WiFiSecurityOther WiFiSecurity = iota
	WiFiWPAPersonal                // WPA-PSK with TKIP
	WiFiWPA2Personal               // WPA2-PSK with CCMP (AES)
	WiFiWPA3Personal               // WPA3-SAE with CCMP; the first AMT version with it is not documented
)

// WiFiSecurities are the securities a profile can be created with, in the order of the UI.
var WiFiSecurities = []WiFiSecurity{WiFiWPAPersonal, WiFiWPA2Personal, WiFiWPA3Personal}

// methods returns the authentication and encryption method of s.
func (s WiFiSecurity) methods() (wifi.AuthenticationMethod, wifi.EncryptionMethod) {
	switch s {
	case WiFiWPAPersonal:
		return wifi.AuthenticationMethodWPAPSK, wifi.EncryptionMethodTKIP
	case WiFiWPA2Personal:
		return wifi.AuthenticationMethodWPA2PSK, wifi.EncryptionMethodCCMP
	case WiFiWPA3Personal:
		return wifi.AuthenticationMethodWPA3SAE, wifi.EncryptionMethodCCMP
	}
	return 0, 0
}

// securityOf maps a profile's methods to its security.
func securityOf(auth, enc int) WiFiSecurity {
	for _, s := range WiFiSecurities {
		a, e := s.methods()
		if int(a) == auth && int(e) == enc {
			return s
		}
	}
	return WiFiSecurityOther
}

// WiFiProfile is one AMT Wi-Fi profile.
type WiFiProfile struct {
	InstanceID string // identifies the profile for UpdateWiFiProfile and DeleteWiFiProfile
	Name       string // ElementName; cannot be changed
	SSID       string
	Priority   int // lower is tried first
	Security   WiFiSecurity
}

// WiFiSettings is the Wi-Fi state of the device.
type WiFiSettings struct {
	// Available: the device has a Wi-Fi port Intel AMT can use. Without one every other field
	// keeps its zero value.
	Available bool
	State     WiFiState

	SyncKnown      bool // AMT_WiFiPortConfigurationService was read
	Sync           bool // Intel AMT also uses the profiles of the operating system
	UEFIShareKnown bool // the firmware has UEFI profile sharing (Intel CSME 16 and later)
	UEFIShare      bool

	LinkUp    bool   // the wireless link of Intel AMT is up
	IPAddress string // of the wireless port; "" if none
	// OverWiFi: the session reaches the device at the wireless address (or the host name could
	// not be resolved, so it may). Deleting a profile or switching Wi-Fi off then needs a
	// confirmation for OpWiFiLockout.
	OverWiFi bool

	Profiles   []WiFiProfile  // AMT profiles by priority, then name; profiles of the OS are left out
	Securities []WiFiSecurity // what a profile can be created with: what the device lists, else all
}

// WiFiProfileSpec is a profile to add or the new values of one to update.
type WiFiProfileSpec struct {
	Name       string // 1 to 32 printable ASCII characters; on update the profile's current name
	SSID       string // 1 to 32 bytes
	Priority   int    // 1 to 255
	Security   WiFiSecurity
	Passphrase string // 8 to 63 printable ASCII characters; on update "" keeps the current one
}

// Validate checks the values; add requires a passphrase. The errors never contain it.
func (s WiFiProfileSpec) Validate(add bool) error {
	switch {
	case s.Name == "" || len(s.Name) > 32 || add && !printableASCII(s.Name):
		// on update the name is the profile's own, as the device returned it
		return invalid("Name", "1 to 32 printable ASCII characters")
	case s.SSID == "" || len(s.SSID) > 32 || strings.ContainsFunc(s.SSID, isControl):
		return invalid("SSID", "1 to 32 bytes without control characters")
	case s.Priority < 1 || s.Priority > 255:
		return invalid("Priority", "1 to 255")
	case !slices.Contains(WiFiSecurities, s.Security):
		return invalid("Security", "WPA, WPA2 or WPA3 Personal")
	case s.Passphrase == "" && !add:
		return nil
	case len(s.Passphrase) < 8 || len(s.Passphrase) > 63 || !printableASCII(s.Passphrase):
		return invalid("Passphrase", "8 to 63 printable ASCII characters")
	}
	return nil
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// wifiPortObjects enumerates CIM_WiFiPort; a missing class is no port.
func (d *Device) wifiPortObjects() ([]*cimObject, error) {
	objs, err := pullObjects(classWiFiPort, false, func() (*client.Message, string, error) {
		r, err := d.msgs.CIM.WiFiPort.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.CIM.WiFiPort.Pull(ctx)
		return r.Message, err
	})
	if err != nil && isMissingClass(err) {
		return nil, nil
	}
	return objs, err
}

func (d *Device) wifiServiceObject() (*cimObject, error) {
	return getObject(classWiFiService, func() (*client.Message, error) {
		r, err := d.msgs.AMT.WiFiPortConfigurationService.Get()
		return r.Message, err
	})
}

// wirelessObject returns the wireless port's AMT_EthernetPortSettings: PhysicalConnectionType 3
// (AMT 15 and later), on older firmware the instance numbered 1. nil if there is none.
func (d *Device) wirelessObject() (*cimObject, error) {
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
		if o.has("PhysicalConnectionType") {
			if o.integer("PhysicalConnectionType") == wirelessConnection {
				return o, nil
			}
			continue
		}
		if strings.HasSuffix(o.str("InstanceID"), " 1") {
			return o, nil
		}
	}
	return nil, nil
}

// overWiFi reports whether the session's host is the wireless address of port w (nil: none).
// A host name is resolved; when that fails, the session may run over Wi-Fi and the answer is yes.
func (d *Device) overWiFi(w *cimObject) bool {
	if w == nil || !w.boolean("LinkIsUp") || w.str("IPAddress") == "" {
		return false
	}
	ip := w.str("IPAddress")
	host := strings.TrimSpace(d.cfg.Host)
	if net.ParseIP(host) != nil {
		return host == ip
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	addrs, err := lookupHost(ctx, host)
	if err != nil {
		return true
	}
	return slices.Contains(addrs, ip)
}

// WiFi reads the Wi-Fi state, the AMT profiles and whether the session runs over Wi-Fi.
func (d *Device) WiFi() (WiFiSettings, error) {
	wireless, err := d.wirelessObject()
	if err != nil {
		return WiFiSettings{}, readError(classEthernet, err)
	}
	ports, err := d.wifiPortObjects()
	if err != nil {
		return WiFiSettings{}, readError(classWiFiPort, err)
	}
	if len(ports) == 0 {
		return WiFiSettings{}, nil
	}
	s := WiFiSettings{Available: true, State: WiFiState(ports[0].integer("EnabledState"))}

	svc, err := d.wifiServiceObject()
	switch {
	case err != nil && !isMissingClass(err):
		return WiFiSettings{}, readError(classWiFiService, err)
	case err == nil:
		s.SyncKnown = true
		s.Sync = svc.integer("localProfileSynchronizationEnabled") != syncOff
		s.UEFIShareKnown = svc.has("UEFIWiFiProfileShareEnabled")
		s.UEFIShare = svc.boolean("UEFIWiFiProfileShareEnabled")
	}

	if wireless != nil {
		s.LinkUp = wireless.boolean("LinkIsUp")
		s.IPAddress = wireless.str("IPAddress")
		s.OverWiFi = d.overWiFi(wireless)
	}

	if s.Profiles, err = d.wifiProfiles(); err != nil {
		return WiFiSettings{}, readError(classWiFiSettings, err)
	}
	s.Securities = d.wifiSecurities()
	return s, nil
}

// wifiProfiles reads the AMT profiles, without the ones synced from the operating system.
func (d *Device) wifiProfiles() ([]WiFiProfile, error) {
	objs, err := pullObjects(classWiFiSettings, false, func() (*client.Message, string, error) {
		r, err := d.msgs.CIM.WiFiEndpointSettings.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.CIM.WiFiEndpointSettings.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		if isMissingClass(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []WiFiProfile
	for _, o := range objs {
		id := o.str("InstanceID")
		if strings.Contains(id, wifiUserProfile) {
			continue
		}
		out = append(out, WiFiProfile{InstanceID: id, Name: o.str("ElementName"), SSID: o.str("SSID"),
			Priority: o.integer("Priority"), Security: securityOf(o.integer("AuthenticationMethod"), o.integer("EncryptionMethod"))})
	}
	slices.SortStableFunc(out, func(a, b WiFiProfile) int {
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), strings.Compare(a.Name, b.Name))
	})
	return out, nil
}

// wifiSecurities returns the securities the device lists in CIM_WiFiEndpointCapabilities; all
// of WiFiSecurities when it lists none of them or the read fails (the device then refuses what
// it cannot do, and the UI shows that).
func (d *Device) wifiSecurities() []WiFiSecurity {
	objs, err := pullObjects(classWiFiCapabilities, false, func() (*client.Message, string, error) {
		r, err := d.msgs.CIM.WiFiEndpointCapabilities.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.CIM.WiFiEndpointCapabilities.Pull(ctx)
		return r.Message, err
	})
	if err != nil || len(objs) == 0 {
		return slices.Clone(WiFiSecurities)
	}
	listed := objs[0].strs("SupportedAuthenticationMethods")
	var out []WiFiSecurity
	for _, s := range WiFiSecurities {
		a, _ := s.methods()
		if slices.Contains(listed, strconv.Itoa(int(a))) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return slices.Clone(WiFiSecurities)
	}
	return out
}

// lockoutConfirmed checks the session: over Wi-Fi the change needs confirm for OpWiFiLockout.
func (d *Device) lockoutConfirmed(op string, confirm Confirmation) error {
	w, err := d.wirelessObject()
	if err != nil {
		return writeError(op, err)
	}
	if d.overWiFi(w) {
		return d.confirmed(confirm, OpWiFiLockout, RiskWiFi)
	}
	return nil
}

// SetWiFiState sets when Intel AMT uses the Wi-Fi port. Off while the session runs over Wi-Fi
// needs confirm for OpWiFiLockout.
func (d *Device) SetWiFiState(s WiFiState, confirm Confirmation) error {
	const op = "set Wi-Fi state"
	if s != WiFiOff && s != WiFiS0 && s != WiFiS0Sx {
		return invalid("State", "Off, S0 or S0 and Sx")
	}
	ports, err := d.wifiPortObjects()
	if err != nil {
		return writeError(op, err)
	}
	if len(ports) == 0 {
		return fmt.Errorf("%s: no Wi-Fi port: %w", op, ErrNotModifiable)
	}
	if WiFiState(ports[0].integer("EnabledState")) == s {
		return nil
	}
	if s == WiFiOff {
		if err := d.lockoutConfirmed(op, confirm); err != nil {
			return err
		}
	}
	r, err := d.msgs.CIM.WiFiPort.RequestStateChange(int(s))
	rv, ok := rscReturnValue(xmlOf(r.Message))
	if ok && rv != 0 {
		return &MethodError{Method: "CIM_WiFiPort.RequestStateChange", ReturnValue: rv}
	}
	if err != nil {
		return writeError(op, err)
	}
	if !ok {
		return fmt.Errorf("%s: %w: RequestStateChange_OUTPUT", op, ErrIncompleteResponse)
	}
	return nil
}

// SetWiFiSync switches the use of the operating system's profiles. On keeps a sync that is on
// already (also the unrestricted one) and otherwise writes the user sync.
func (d *Device) SetWiFiSync(on bool) error {
	const op = "set Wi-Fi profile sync"
	o, err := d.wifiServiceObject()
	if err != nil {
		return writeError(op, err)
	}
	cur := o.integer("localProfileSynchronizationEnabled")
	if (cur != syncOff) == on {
		return nil
	}
	v := syncOff
	if on {
		v = syncUser
	}
	o.set("localProfileSynchronizationEnabled", strconv.Itoa(v))
	return writeError(op, d.putObject(o, nil))
}

// SetWiFiUEFIShare switches the sharing of the profiles with UEFI (One Click Recovery over
// Wi-Fi). Firmware without it returns ErrNotModifiable.
func (d *Device) SetWiFiUEFIShare(on bool) error {
	const op = "set Wi-Fi profile sharing with UEFI"
	o, err := d.wifiServiceObject()
	if err != nil {
		return writeError(op, err)
	}
	if !o.has("UEFIWiFiProfileShareEnabled") {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	if o.boolean("UEFIWiFiProfileShareEnabled") == on {
		return nil
	}
	o.set("UEFIWiFiProfileShareEnabled", strconv.FormatBool(on))
	return writeError(op, d.putObject(o, nil))
}

// request is the library's embedded instance of p; the passphrase only when set.
func (p WiFiProfileSpec) request(instanceID string) wifi.WiFiEndpointSettingsRequest {
	auth, enc := p.Security.methods()
	return wifi.WiFiEndpointSettingsRequest{
		ElementName: p.Name, InstanceID: instanceID, SSID: p.SSID, Priority: p.Priority,
		AuthenticationMethod: auth, EncryptionMethod: enc, PSKPassPhrase: p.Passphrase,
	}
}

// wifiEndpoint returns the Name of the device's CIM_WiFiEndpoint, wifiEndpointName if it
// cannot be read.
func (d *Device) wifiEndpoint() string {
	objs, err := pullObjects(classWiFiEndpoint, false, func() (*client.Message, string, error) {
		r, err := d.msgs.CIM.WiFiEndpoint.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.CIM.WiFiEndpoint.Pull(ctx)
		return r.Message, err
	})
	if err != nil || len(objs) == 0 || objs[0].str("Name") == "" {
		return wifiEndpointName
	}
	return objs[0].str("Name")
}

// wifiMethodError returns *MethodError for a non-zero ReturnValue of method in r, else the
// transport or fault error of the call. The library's own error for a ReturnValue carries no
// request data; it is replaced, so nothing of the request (the passphrase) reaches the caller.
func wifiMethodError(op, method string, rv wifiportconfiguration.ReturnValue, xmlOutput string, err error) error {
	if xmlOutput != "" && rv != 0 {
		return &MethodError{Method: method, ReturnValue: int(rv)}
	}
	if err != nil {
		return writeError(op, err)
	}
	if xmlOutput == "" {
		return fmt.Errorf("%s: %w: %s_OUTPUT", op, ErrIncompleteResponse, method)
	}
	return nil
}

// AddWiFiProfile creates a WPA, WPA2 or WPA3 Personal profile.
func (d *Device) AddWiFiProfile(p WiFiProfileSpec) error {
	const op = "add Wi-Fi profile"
	if err := p.Validate(true); err != nil {
		return err
	}
	r, err := d.msgs.AMT.WiFiPortConfigurationService.AddWiFiSettings(p.request(wifiProfilePrefix+p.Name),
		models.IEEE8021xSettings{}, d.wifiEndpoint(), "", "")
	return wifiMethodError(op, "AddWiFiSettings", r.Body.AddWiFiSettingsOutput.ReturnValue, xmlOf(r.Message), err)
}

// UpdateWiFiProfile writes SSID, priority, security and (if set) the passphrase of the profile
// instanceID. Its name stays (AMT cannot rename a profile); p.Name must be the current name.
func (d *Device) UpdateWiFiProfile(instanceID string, p WiFiProfileSpec) error {
	const op = "update Wi-Fi profile"
	if strings.TrimSpace(instanceID) == "" {
		return invalid("InstanceID", "empty")
	}
	if err := p.Validate(false); err != nil {
		return err
	}
	r, err := d.msgs.AMT.WiFiPortConfigurationService.UpdateWiFiSettings(p.request(instanceID), models.IEEE8021xSettings{}, "", "")
	return wifiMethodError(op, "UpdateWiFiSettings", r.Body.UpdateWiFiSettingsOutput.ReturnValue, xmlOf(r.Message), err)
}

// DeleteWiFiProfile deletes the profile instanceID. While the session runs over Wi-Fi it needs
// confirm for OpWiFiLockout. The request is built here: the library does not escape the
// InstanceID in its selector.
func (d *Device) DeleteWiFiProfile(instanceID string, confirm Confirmation) error {
	const op = "delete Wi-Fi profile"
	if strings.TrimSpace(instanceID) == "" {
		return invalid("InstanceID", "empty")
	}
	if err := d.lockoutConfirmed(op, confirm); err != nil {
		return err
	}
	_, err := d.msgs.Client.Post(envelope(actionDelete, resourceURI(classWiFiSettings), []selector{{"InstanceID", instanceID}}, ""))
	return writeError(op, err)
}
