package amt

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman"
	wsmanamt "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt"
	wsmancim "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
	ips "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/ips"
)

// ControlMode is the AMT provisioning mode.
type ControlMode string

const (
	ControlModeAdmin   ControlMode = "ACM"
	ControlModeClient  ControlMode = "CCM"
	ControlModeUnknown ControlMode = "unknown"
)

const (
	provisioningModeAdmin  = 1
	provisioningModeClient = 4
)

// StatusPart names one read of Status.
type StatusPart uint8

const (
	StatusPower       StatusPart = 1 << iota // CIM_ServiceAvailableToElement
	StatusVersion                            // CIM_SoftwareIdentity
	StatusControlMode                        // AMT_SetupAndConfigurationService
)

// Status is the basic state of an AMT device.
type Status struct {
	PowerState  models.PowerState
	AMTVersion  string // empty if the device reports no "AMT" software identity
	ControlMode ControlMode

	// PowerStates is CIM_PowerManagementCapabilities.PowerStatesSupported (see PowerStates).
	// Status leaves it nil; session.Poller fills it. nil means unknown or none.
	PowerStates []power.PowerState

	// Denied lists the parts the account is not permitted to read; their fields keep the zero value.
	Denied StatusPart
}

// Allowed reports whether the account may read part p (the zero Status allows everything).
func (s Status) Allowed(p StatusPart) bool { return s.Denied&p == 0 }

// Device performs AMT operations over a WS-Man client.
type Device struct {
	msgs        wsman.Messages
	fingerprint string
	cfg         Config
	tlsCfg      *tls.Config // nil without TLS
	transport   *http.Transport
	amtMajor    atomic.Int32 // major AMT version from the last Status, for kvm.Config
}

// MajorVersion returns the major number of an AMT version string such as "16.1.25", 0 if
// there is none (MeshCommander: parseInt(v.split('.')[0])).
func MajorVersion(v string) int {
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// CloseIdleConnections drops the kept-alive WS-Man sockets, so a discarded device does not
// hold one of the few connection slots AMT offers.
func (d *Device) CloseIdleConnections() {
	if d.transport != nil {
		d.transport.CloseIdleConnections()
	}
}

func newDevice(c client.WSMan, fingerprint string) *Device {
	return &Device{msgs: messagesFor(c), fingerprint: fingerprint}
}

func messagesFor(c client.WSMan) wsman.Messages {
	return wsman.Messages{
		Client: c,
		AMT:    wsmanamt.NewMessages(c),
		CIM:    wsmancim.NewMessages(c),
		IPS:    ips.NewMessages(c),
	}
}

// Fingerprint returns the pinned certificate fingerprint this device was created with.
func (d *Device) Fingerprint() string { return d.fingerprint }

// Status reads power state, AMT version and control mode. A part whose first request (Enumerate
// or Get) the device refuses with a SOAP fault (HTTP 400, see coreClassDenied) is reported in
// Denied instead of failing: such an answer only goes to an authenticated sender, so a limited
// account can still connect. Every other error, also a fault on a Pull, fails as before.
func (d *Device) Status() (Status, error) {
	var st Status

	ps, denied, err := d.readPowerState()
	switch {
	case denied:
		st.Denied |= StatusPower
	case err != nil:
		return Status{}, err
	}
	st.PowerState = ps

	ctx, err := d.msgs.CIM.SoftwareIdentity.Enumerate()
	switch {
	case err != nil && coreClassDenied(err):
		st.Denied |= StatusVersion
	case err != nil:
		return Status{}, fmt.Errorf("enumerate software identity: %w", err)
	default:
		pull, err := d.msgs.CIM.SoftwareIdentity.Pull(ctx.Body.EnumerateResponse.EnumerationContext)
		if err != nil {
			return Status{}, fmt.Errorf("pull software identity: %w", err)
		}
		for _, item := range pull.Body.PullResponse.SoftwareIdentityItems {
			if item.InstanceID == "AMT" {
				st.AMTVersion = item.VersionString
				d.amtMajor.Store(int32(MajorVersion(item.VersionString)))
				break
			}
		}
	}

	setup, err := d.msgs.AMT.SetupAndConfigurationService.Get()
	switch {
	case err != nil && coreClassDenied(err):
		st.Denied |= StatusControlMode
	case err != nil:
		return Status{}, fmt.Errorf("get setup and configuration: %w", err)
	default:
		switch setup.Body.GetResponse.ProvisioningMode {
		case provisioningModeAdmin:
			st.ControlMode = ControlModeAdmin
		case provisioningModeClient:
			st.ControlMode = ControlModeClient
		default:
			st.ControlMode = ControlModeUnknown
		}
	}
	return st, nil
}

// powerState reads the current power state; a refused read is an error here.
func (d *Device) powerState() (models.PowerState, error) {
	ps, _, err := d.readPowerState()
	return ps, err
}

// readPowerState reads the current power state via CIM_ServiceAvailableToElement. denied is true
// (with err set) when the Enumerate call was refused with a permission fault, see coreClassDenied;
// a fault on the Pull is a plain error.
func (d *Device) readPowerState() (ps models.PowerState, denied bool, err error) {
	ctx, err := d.msgs.CIM.ServiceAvailableToElement.Enumerate()
	if err != nil {
		return 0, coreClassDenied(err), fmt.Errorf("enumerate power state: %w", err)
	}
	pull, err := d.msgs.CIM.ServiceAvailableToElement.Pull(ctx.Body.EnumerateResponse.EnumerationContext)
	if err != nil {
		return 0, false, fmt.Errorf("pull power state: %w", err)
	}
	items := pull.Body.PullResponse.AssociatedPowerManagementService
	if len(items) == 0 {
		return 0, false, fmt.Errorf("%w: device returned no power state", ErrIncompleteResponse)
	}
	return items[0].PowerState, false, nil
}
