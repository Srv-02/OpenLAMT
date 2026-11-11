package amt

import (
	"fmt"
	"strings"

	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

// PowerAction is a remote power operation.
type PowerAction int

const (
	PowerOn    PowerAction = iota
	PowerOff               // hard
	PowerReset             // hard
	PowerCycle             // off then on, hard
	BootToBIOS
	PowerShutdown // orderly shutdown of the operating system (Off - Soft Graceful)
	BootToISO     // boot from the mounted IDE-R image (see BootToISO below); keep last, the UI lists actions explicitly
)

func (a PowerAction) String() string {
	switch a {
	case PowerOn:
		return "on"
	case PowerOff:
		return "off"
	case PowerReset:
		return "reset"
	case PowerCycle:
		return "cycle"
	case BootToBIOS:
		return "bios"
	case PowerShutdown:
		return "shutdown"
	case BootToISO:
		return "iso"
	}
	return fmt.Sprintf("PowerAction(%d)", int(a))
}

// Destructive reports whether the action can interrupt a running system.
func (a PowerAction) Destructive() bool { return a != PowerOn }

// PowerError is returned when the device rejects a power request.
type PowerError struct {
	Action      PowerAction
	ReturnValue int
}

func (e *PowerError) Error() string {
	return fmt.Sprintf("power %s failed: device returned ReturnValue %d", e.Action, e.ReturnValue)
}

// Power performs a power action.
func (d *Device) Power(action PowerAction) error {
	switch action {
	case PowerOn:
		return d.requestPowerState(action, power.PowerOn)
	case PowerOff:
		return d.requestPowerState(action, power.PowerOffHard)
	case PowerReset:
		return d.requestPowerState(action, power.MasterBusReset)
	case PowerCycle:
		return d.requestPowerState(action, power.PowerCycleOffHard)
	case BootToBIOS:
		return d.bootToBIOS()
	case PowerShutdown:
		return d.requestPowerState(action, power.PowerOffSoftGraceful)
	case BootToISO:
		return d.bootToISO()
	}
	return fmt.Errorf("unknown power action %d", int(action))
}

// PowerStates reads CIM_PowerManagementCapabilities.PowerStatesSupported, the states
// RequestPowerStateChange accepts. AMT lists Off - Soft Graceful (12) only while the Intel Local
// Manageability Service runs in the operating system. A response without the list yields none.
func (d *Device) PowerStates() ([]power.PowerState, error) {
	resp, err := d.msgs.CIM.PowerManagementCapabilities.Get()
	if err != nil {
		return nil, fmt.Errorf("get power capabilities: %w", err)
	}
	var states []power.PowerState
	for _, s := range resp.Body.PowerManagementCapabilitiesGetResponse.PowerStatesSupported {
		states = append(states, power.PowerState(s))
	}
	return states, nil
}

// requestPowerState sends CIM_PowerManagementService.RequestPowerStateChange. The output element
// is checked on the raw XML because a missing element would unmarshal to ReturnValue 0.
func (d *Device) requestPowerState(action PowerAction, state power.PowerState) error {
	resp, err := d.msgs.CIM.PowerManagementService.RequestPowerStateChange(state)
	if err != nil {
		return fmt.Errorf("power %s: %w", action, err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "RequestPowerStateChange_OUTPUT") {
		return fmt.Errorf("power %s: %w: RequestPowerStateChange_OUTPUT", action, ErrIncompleteResponse)
	}
	if rv := int(resp.Body.RequestPowerStateChangeResponse.ReturnValue); rv != 0 {
		return &PowerError{Action: action, ReturnValue: rv}
	}
	return nil
}

// bootToBIOS sets the one-shot BIOS setup boot option and then resets (running) or powers on
// (not running) the machine, following the console's SetBootOptions sequence.
func (d *Device) bootToBIOS() error {
	state, err := d.powerState()
	if err != nil {
		return fmt.Errorf("boot to BIOS: %w", err)
	}
	target := power.PowerOn
	if state == models.PowerStateOn {
		target = power.MasterBusReset
	}
	got, err := d.msgs.AMT.BootSettingData.Get()
	if err != nil {
		return fmt.Errorf("boot to BIOS: get boot settings: %w", err)
	}
	cur := got.Body.BootSettingDataGetResponse
	if cur.InstanceID == "" {
		return fmt.Errorf("boot to BIOS: %w: device returned no boot settings", ErrIncompleteResponse)
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("boot to BIOS: clear boot order: %w", err)
	}
	if err := d.setBIOSBoot(cur, target); err != nil {
		return fmt.Errorf("boot to BIOS: %w (the BIOS setup boot option may still be set on the device)", err)
	}
	return nil
}

// setBIOSBoot runs the steps after the first change to the device (Put onward).
func (d *Device) setBIOSBoot(cur amtboot.BootSettingDataResponse, target power.PowerState) error {
	req := amtboot.BootSettingDataRequest{
		BIOSSetup:         true,
		ElementName:       cur.ElementName,
		EnforceSecureBoot: cur.EnforceSecureBoot,
		InstanceID:        cur.InstanceID,
		OwningEntity:      cur.OwningEntity,
	}
	if _, err := d.msgs.AMT.BootSettingData.Put(req); err != nil {
		return fmt.Errorf("put boot settings: %w", err)
	}
	resp, err := d.msgs.CIM.BootService.SetBootConfigRole("Intel(r) AMT: Boot Configuration 0", 1)
	if err != nil {
		return fmt.Errorf("set boot config role: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "SetBootConfigRole_OUTPUT") {
		return fmt.Errorf("set boot config role: %w: SetBootConfigRole_OUTPUT", ErrIncompleteResponse)
	}
	if rv := int(resp.Body.SetBootConfigRole_OUTPUT.ReturnValue); rv != 0 {
		return &PowerError{Action: BootToBIOS, ReturnValue: rv}
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("clear boot order: %w", err)
	}
	return d.requestPowerState(BootToBIOS, target)
}
