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

// Status is the basic state of an AMT device.
type Status struct {
	PowerState  models.PowerState
	AMTVersion  string // empty if the device reports no "AMT" software identity
	ControlMode ControlMode

	// PowerStates is CIM_PowerManagementCapabilities.PowerStatesSupported (see PowerStates).
	// Status leaves it nil; session.Poller fills it. nil means unknown or none.
	PowerStates []power.PowerState
}

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
	return &Device{
		msgs: wsman.Messages{
			Client: c,
			AMT:    wsmanamt.NewMessages(c),
			CIM:    wsmancim.NewMessages(c),
			IPS:    ips.NewMessages(c),
		},
		fingerprint: fingerprint,
	}
}

// Fingerprint returns the pinned certificate fingerprint this device was created with.
func (d *Device) Fingerprint() string { return d.fingerprint }

// Status reads power state, AMT version and control mode.
func (d *Device) Status() (Status, error) {
	var st Status
	var err error

	if st.PowerState, err = d.powerState(); err != nil {
		return Status{}, err
	}

	ctx, err := d.msgs.CIM.SoftwareIdentity.Enumerate()
	if err != nil {
		return Status{}, fmt.Errorf("enumerate software identity: %w", err)
	}
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

	setup, err := d.msgs.AMT.SetupAndConfigurationService.Get()
	if err != nil {
		return Status{}, fmt.Errorf("get setup and configuration: %w", err)
	}
	switch setup.Body.GetResponse.ProvisioningMode {
	case provisioningModeAdmin:
		st.ControlMode = ControlModeAdmin
	case provisioningModeClient:
		st.ControlMode = ControlModeClient
	default:
		st.ControlMode = ControlModeUnknown
	}
	return st, nil
}

// powerState reads the current power state via CIM_ServiceAvailableToElement.
func (d *Device) powerState() (models.PowerState, error) {
	ctx, err := d.msgs.CIM.ServiceAvailableToElement.Enumerate()
	if err != nil {
		return 0, fmt.Errorf("enumerate power state: %w", err)
	}
	pull, err := d.msgs.CIM.ServiceAvailableToElement.Pull(ctx.Body.EnumerateResponse.EnumerationContext)
	if err != nil {
		return 0, fmt.Errorf("pull power state: %w", err)
	}
	items := pull.Body.PullResponse.AssociatedPowerManagementService
	if len(items) == 0 {
		return 0, fmt.Errorf("%w: device returned no power state", ErrIncompleteResponse)
	}
	return items[0].PowerState, nil
}
