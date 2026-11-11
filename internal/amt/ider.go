package amt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Srv-02/openlamt/internal/ider"
	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	redirection "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/redirection"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

// ErrIDERUnsupported is returned when AMT_BootCapabilities reports no IDE redirection support.
var ErrIDERUnsupported = errors.New("IDE-R is not supported by this device")

// IDERError is returned when the device rejects an IDE-R activation step.
type IDERError struct {
	Step        string
	ReturnValue int
}

func (e *IDERError) Error() string {
	return fmt.Sprintf("ider %s failed: device returned ReturnValue %d", e.Step, e.ReturnValue)
}

// AMT_RedirectionService.EnabledState values (IDE-R and SOL combinations).
const (
	redirDisabled   = 32768 // IDE-R and SOL disabled
	redirIDER       = 32769 // IDE-R enabled, SOL disabled
	redirSOL        = 32770 // SOL enabled, IDE-R disabled
	redirIDERAndSOL = 32771
)

// EnableIDER makes the redirection listener available and switches IDE-R on, keeping SOL as it is.
// It writes only when needed.
func (d *Device) EnableIDER() error {
	cur, err := d.ensureListener()
	if err != nil {
		return err
	}
	var want redirection.RequestedState
	switch int(cur.EnabledState) {
	case redirIDER, redirIDERAndSOL:
		return nil
	case redirDisabled:
		want = redirIDER
	case redirSOL:
		want = redirIDERAndSOL
	default:
		return fmt.Errorf("enable ider: unexpected redirection service state %d", int(cur.EnabledState))
	}
	resp, err := d.msgs.AMT.RedirectionService.RequestStateChange(want)
	// The library returns its own err for ReturnValue != 0; read the output first to keep the value.
	if resp.Message != nil && strings.Contains(resp.XMLOutput, "RequestStateChange_OUTPUT") {
		if rv := int(resp.Body.RequestStateChange_OUTPUT.ReturnValue); rv != 0 {
			return &IDERError{Step: "RedirectionService.RequestStateChange", ReturnValue: rv}
		}
	}
	if err != nil {
		return fmt.Errorf("enable ider: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "RequestStateChange_OUTPUT") {
		return fmt.Errorf("enable ider: %w: RequestStateChange_OUTPUT", ErrIncompleteResponse)
	}
	return nil
}

// iderSupported reads AMT_BootCapabilities once.
func (d *Device) iderSupported() error {
	resp, err := d.msgs.AMT.BootCapabilities.Get()
	if err != nil {
		return fmt.Errorf("get boot capabilities: %w", err)
	}
	if resp.Body.BootCapabilitiesGetResponse.InstanceID == "" {
		return fmt.Errorf("get boot capabilities: %w: AMT_BootCapabilities", ErrIncompleteResponse)
	}
	if !resp.Body.BootCapabilitiesGetResponse.IDER {
		return ErrIDERUnsupported
	}
	return nil
}

// OpenIDER mounts img as CD drive of the device: it checks BootCapabilities.IDER, runs EnableIDER
// and the user consent (needed for IDE-R only when OptInRequired is All) and dials the redirection
// listener with the pinned TLS config.
//
// Ownership of img: on success the returned client owns it and closes it on Close or at session
// end; on any error the caller still owns img and must close it (a large temporary image need not
// be rebuilt after a wrong consent code).
func (d *Device) OpenIDER(ctx context.Context, img ider.Image, start ider.StartMode, prompt ConsentPrompt) (*ider.Client, error) {
	if err := d.iderSupported(); err != nil {
		return nil, err
	}
	if err := d.EnableIDER(); err != nil {
		return nil, err
	}
	if err := d.userConsent(prompt, true); err != nil {
		return nil, err
	}
	if d.cfg.UseTLS && d.tlsCfg == nil {
		// Without the pinned config ider.Dial would connect in plain text.
		return nil, errors.New("ider: TLS is enabled but the device has no pinned TLS config")
	}
	return ider.Dial(ctx, ider.Config{
		Addr:        d.cfg.RedirectionAddress(),
		Username:    d.cfg.Username,
		Password:    d.cfg.Password,
		TLS:         d.tlsCfg,
		DialContext: d.cfg.dialContext,
		Image:       img,
		Start:       start,
	})
}

// isoBootRequest builds the boot settings Put for IDE-R boot (useIDER) or for clearing it.
func isoBootRequest(cur amtboot.BootSettingDataResponse, useIDER bool) amtboot.BootSettingDataRequest {
	req := amtboot.BootSettingDataRequest{
		BIOSSetup:         false,
		ElementName:       cur.ElementName,
		EnforceSecureBoot: cur.EnforceSecureBoot,
		InstanceID:        cur.InstanceID,
		OwningEntity:      cur.OwningEntity,
		UseIDER:           useIDER,
	}
	if useIDER {
		req.IDERBootDevice = amtboot.CDBoot
	}
	return req
}

func (d *Device) getBootSettings(op string) (amtboot.BootSettingDataResponse, error) {
	got, err := d.msgs.AMT.BootSettingData.Get()
	if err != nil {
		return got.Body.BootSettingDataGetResponse, fmt.Errorf("%s: get boot settings: %w", op, err)
	}
	cur := got.Body.BootSettingDataGetResponse
	if cur.InstanceID == "" {
		return cur, fmt.Errorf("%s: %w: device returned no boot settings", op, ErrIncompleteResponse)
	}
	return cur, nil
}

// setISOBoot runs the sequence of the console's SetBootOptions (IDE-R CD boot): clear the boot
// order, put UseIDER with IDERBootDevice 1, set the boot config role, clear the boot order again.
func (d *Device) setISOBoot(op string) error {
	cur, err := d.getBootSettings(op)
	if err != nil {
		return err
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("%s: clear boot order: %w", op, err)
	}
	if _, err := d.msgs.AMT.BootSettingData.Put(isoBootRequest(cur, true)); err != nil {
		return fmt.Errorf("%s: put boot settings: %w (the IDE-R boot option may still be set on the device)", op, err)
	}
	if err := d.finishISOBoot(); err != nil {
		return fmt.Errorf("%s: %w (the IDE-R boot option may still be set on the device)", op, err)
	}
	return nil
}

// finishISOBoot sends SetBootConfigRole and the closing ChangeBootOrder.
func (d *Device) finishISOBoot() error {
	resp, err := d.msgs.CIM.BootService.SetBootConfigRole("Intel(r) AMT: Boot Configuration 0", 1)
	if err != nil {
		return fmt.Errorf("set boot config role: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "SetBootConfigRole_OUTPUT") {
		return fmt.Errorf("set boot config role: %w: SetBootConfigRole_OUTPUT", ErrIncompleteResponse)
	}
	if rv := int(resp.Body.SetBootConfigRole_OUTPUT.ReturnValue); rv != 0 {
		return &PowerError{Action: BootToISO, ReturnValue: rv}
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("clear boot order: %w", err)
	}
	return nil
}

// bootToISO sets the IDE-R boot option and then resets (running) or powers on (not running) the
// machine. Precondition: an IDE-R session with an image is open (see OpenIDER), otherwise the
// machine finds no drive to boot from.
func (d *Device) bootToISO() error {
	state, err := d.powerState()
	if err != nil {
		return fmt.Errorf("boot to ISO: %w", err)
	}
	target := power.PowerOn
	if state == models.PowerStateOn {
		target = power.MasterBusReset
	}
	if err := d.setISOBoot("boot to ISO"); err != nil {
		return err
	}
	return d.requestPowerState(BootToISO, target)
}

// NextBootFromISO sets the IDE-R boot option without any power command: the machine boots from
// the mounted image at its next start. Precondition: an IDE-R session with an image is open.
// ClearISOBoot takes the option back if no restart happens.
func (d *Device) NextBootFromISO() error { return d.setISOBoot("next boot from ISO") }

// ClearISOBoot switches the IDE-R boot option off when it is set and writes nothing otherwise.
func (d *Device) ClearISOBoot() error {
	cur, err := d.getBootSettings("clear ISO boot")
	if err != nil {
		return err
	}
	if !cur.UseIDER {
		return nil
	}
	if _, err := d.msgs.AMT.BootSettingData.Put(isoBootRequest(cur, false)); err != nil {
		return fmt.Errorf("clear ISO boot: put boot settings: %w", err)
	}
	return nil
}
