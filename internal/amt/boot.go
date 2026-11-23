package amt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	cimboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/boot"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

// Boot options (issue #13) and One Click Recovery (issue #21): the target and the options of
// the next boot, as the Boot Options dialog sets them, and the switch of the AMT boot service
// that allows HTTPS boot. Setting boot options needs the user consent of BootConsent first
// (Session.Boot runs both).

// BootTarget is where the device boots from at its next start.
type BootTarget int

const (
	BootHardDisk  BootTarget = iota // the default boot order; with SafeMode the hard drive in safe mode
	BootPXE                         // network boot (PXE)
	BootHTTPS                       // One Click Recovery: UEFI HTTPS boot of BootOptions.URL
	BootBIOSSetup                   // the BIOS setup screen
	BootISO                         // the image mounted over IDE-R (CD)
)

func (t BootTarget) String() string {
	switch t {
	case BootHardDisk:
		return "disk"
	case BootPXE:
		return "pxe"
	case BootHTTPS:
		return "https"
	case BootBIOSSetup:
		return "bios"
	case BootISO:
		return "iso"
	}
	return fmt.Sprintf("BootTarget(%d)", int(t))
}

// Verbosity is AMT_BootSettingData.FirmwareVerbosity: how much the firmware shows on the screen.
type Verbosity int

const (
	VerbosityDefault Verbosity = Verbosity(amtboot.SystemDefault)
	VerbosityQuiet   Verbosity = Verbosity(amtboot.QuietMinimal)
	VerbosityVerbose Verbosity = Verbosity(amtboot.VerboseAll)
	VerbosityBlank   Verbosity = Verbosity(amtboot.ScreenBlank)
)

// BootCapabilities is what AMT_BootCapabilities reports: the targets and options the firmware
// of the device offers. The UI greys out the others.
type BootCapabilities struct {
	PXE            bool // ForcePXEBoot
	SafeMode       bool // ForceHardDriveSafeModeBoot
	BIOSSetup      bool
	BIOSPause      bool
	IDER           bool
	SOL            bool
	LockPower      bool // PowerButtonLock
	LockReset      bool // ResetButtonLock
	LockSleep      bool // SleepButtonLock
	LockKeyboard   bool // KeyboardLock
	PasswordBypass bool // UserPasswordBypass
	ProgressEvents bool // ForcedProgressEvents
	VerbosityQuiet bool
	VerboseAll     bool // VerbosityVerbose
	ScreenBlank    bool // VerbosityScreenBlank
	// SecureBootControl (AMTSecureBootControl): the BIOS lets AMT turn Secure Boot off for a boot
	// it triggers. Without it EnforceSecureBoot keeps its value.
	SecureBootControl bool
	HTTPSBoot         bool // ForceUEFIHTTPSBoot: the firmware supports One Click Recovery over HTTPS
}

// OCRState is the One Click Recovery state of the device.
type OCRState struct {
	// Known: the device has the boot service of AMT 16 or later (CIM_BootService EnabledState
	// 32768 to 32771). Without it there is no One Click Recovery.
	Known       bool
	Enabled     bool // the boot service allows One Click Recovery (Settings › Remote features)
	BIOSEnabled bool // AMT_BootSettingData.UEFIHTTPSBootEnabled: HTTPS boot is on in the BIOS
}

// BootInfo is what the Boot Options dialog needs to know about the device.
type BootInfo struct {
	Caps              BootCapabilities
	EnforceSecureBoot bool // the current value; kept where SecureBootControl is false
	OCR               OCRState
	Erase             EraseState // Remote Secure Erase and Remote Platform Erase (erase.go, issue #20)
}

// HTTPSBootAvailable reports whether HTTPS boot can be chosen: the firmware supports it and the
// boot service allows it.
func (b BootInfo) HTTPSBootAvailable() bool { return b.Caps.HTTPSBoot && b.OCR.Known && b.OCR.Enabled }

// BootOptions is the next boot as the Boot Options dialog describes it.
type BootOptions struct {
	Target BootTarget
	// URL is the image of BootHTTPS: https, at most 300 bytes. The server's certificate must chain
	// to a trusted root certificate of the device; AMT hands its roots to the BIOS for this boot.
	URL               string
	SOL               bool // show the boot in the console (UseSOL)
	SafeMode          bool // BootHardDisk only
	BIOSPause         bool // pause in the BIOS before the boot; not with PXE or HTTPS (they set a boot source)
	EnforceSecureBoot bool
	LockPower         bool
	LockReset         bool
	LockSleep         bool
	LockKeyboard      bool
	PasswordBypass    bool // bypass the BIOS (user) password
	ProgressEvents    bool // log boot progress (ForcedProgressEvents)
	Verbosity         Verbosity
}

// maxBootURL is MaxSizes[OCR_EFI_NETWORK_DEVICE_PATH] of the library.
const maxBootURL = 300

// ErrOCROffInBIOS: the device refused to switch One Click Recovery on because HTTPS boot is
// off in its BIOS.
var ErrOCROffInBIOS = errors.New("one click recovery: HTTPS boot is off in the BIOS of the device")

var (
	// ErrBootMayBeSet: a step after the write of the boot settings failed and taking them back
	// failed too (see BootMayBeSetError). The next start of the device may still use them.
	ErrBootMayBeSet = errors.New("the boot options may still be set for the next start of the device")
	// ErrEraseMayBeSet is ErrBootMayBeSet for an erase: the next start may erase the drives.
	ErrEraseMayBeSet = errors.New("the erase may still be set: the next start of the device may erase the drives")
	// ErrAsleep: the device sleeps (S3). Power On only resumes it without the start of the BIOS,
	// so boot options and an erase would wait for a later restart. Nothing was written.
	ErrAsleep = errors.New("the device is asleep: waking it does not restart it, so boot options cannot apply")
)

// BootMayBeSetError is returned when a step from the write of the boot settings onward failed
// (Err) and the best-effort undo failed as well (ClearErr). It matches ErrBootMayBeSet and, with
// Erase, ErrEraseMayBeSet.
type BootMayBeSetError struct {
	Erase    bool  // the boot settings carry an erase
	Err      error // the step that failed
	ClearErr error // why taking the boot settings back failed
}

func (e *BootMayBeSetError) Error() string {
	s := ErrBootMayBeSet
	if e.Erase {
		s = ErrEraseMayBeSet
	}
	return fmt.Sprintf("%v: %v; clearing them failed: %v", s, e.Err, e.ClearErr)
}

func (e *BootMayBeSetError) Is(target error) bool {
	return target == ErrBootMayBeSet || e.Erase && target == ErrEraseMayBeSet
}

func (e *BootMayBeSetError) Unwrap() error { return e.Err }

// refuseAsleep refuses a device in Sleep - Light or Deep (S3) before anything is written: Power
// On only resumes it without a start of the BIOS, so the one-shot boot settings (an erase too)
// would stay set for a later restart. Hibernate (S4) starts through the BIOS and is not refused.
func refuseAsleep(op string, state models.PowerState) error {
	if state == models.PowerStateSleepLight || state == models.PowerStateSleepDeep {
		return fmt.Errorf("%s: %w", op, ErrAsleep)
	}
	return nil
}

// undoBoot runs after step failed from the Put of the boot settings onward: one best-effort Put
// with every boot option off (identity and EnforceSecureBoot from cur, UseIDER and the erase off)
// and the default boot order. When that works the error says so; when it fails too the error is
// *BootMayBeSetError.
func (d *Device) undoBoot(op string, cur amtboot.BootSettingDataResponse, erase bool, step error) error {
	if cerr := d.clearBootSettings(cur); cerr != nil {
		return &BootMayBeSetError{Erase: erase, Err: fmt.Errorf("%s: %w", op, step), ClearErr: cerr}
	}
	return fmt.Errorf("%s: %w (OpenLAMT cleared the boot settings again)", op, step)
}

// clearBootSettings puts the boot settings with every option off and clears the boot order.
func (d *Device) clearBootSettings(cur amtboot.BootSettingDataResponse) error {
	if _, err := d.msgs.AMT.BootSettingData.Put(clearedBootRequest(cur)); err != nil {
		return fmt.Errorf("put boot settings: %w", err)
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("clear boot order: %w", err)
	}
	return nil
}

// clearedBootRequest is the Put of the boot settings with every option off; identity fields and
// EnforceSecureBoot come from cur.
func clearedBootRequest(cur amtboot.BootSettingDataResponse) amtboot.BootSettingDataRequest {
	return amtboot.BootSettingDataRequest{
		ElementName:       cur.ElementName,
		EnforceSecureBoot: cur.EnforceSecureBoot,
		InstanceID:        cur.InstanceID,
		OwningEntity:      cur.OwningEntity,
	}
}

// CIM_BootService EnabledState of AMT 16 and later: the low bits switch One Click Recovery (1)
// and Remote Platform Erase (2).
const (
	bootServiceBase = 32768
	bootServiceOCR  = 1
	bootServiceMax  = 32771
)

// BootInfo reads AMT_BootCapabilities, AMT_BootSettingData and CIM_BootService. A device
// without the boot service of AMT 16 (an older one answers with a fault or another state) has
// no One Click Recovery; that is not an error.
func (d *Device) BootInfo() (BootInfo, error) {
	const op = "read boot options"
	resp, err := d.msgs.AMT.BootCapabilities.Get()
	if err != nil {
		return BootInfo{}, accessDenied(fmt.Errorf("%s: get boot capabilities: %w", op, err), op)
	}
	c := resp.Body.BootCapabilitiesGetResponse
	if c.InstanceID == "" {
		return BootInfo{}, fmt.Errorf("%s: %w: AMT_BootCapabilities", op, ErrIncompleteResponse)
	}
	cur, err := d.getBootSettings(op)
	if err != nil {
		return BootInfo{}, accessDenied(err, op)
	}
	info := BootInfo{
		Caps: BootCapabilities{
			PXE: c.ForcePXEBoot, SafeMode: c.ForceHardDriveSafeModeBoot, BIOSSetup: c.BIOSSetup, BIOSPause: c.BIOSPause,
			IDER: c.IDER, SOL: c.SOL, LockPower: c.PowerButtonLock, LockReset: c.ResetButtonLock,
			LockSleep: c.SleepButtonLock, LockKeyboard: c.KeyboardLock, PasswordBypass: c.UserPasswordBypass,
			ProgressEvents: c.ForcedProgressEvents, VerbosityQuiet: c.VerbosityQuiet, VerboseAll: c.VerbosityVerbose,
			ScreenBlank: c.VerbosityScreenBlank, SecureBootControl: c.AMTSecureBootControl, HTTPSBoot: c.ForceUEFIHTTPSBoot,
		},
		EnforceSecureBoot: cur.EnforceSecureBoot,
	}
	info.OCR.BIOSEnabled = cur.UEFIHTTPSBootEnabled
	info.Erase = EraseState{Secure: c.SecureErase, Platform: EraseActions(uint32(c.PlatformErase)), BIOSEnabled: cur.RPEEnabled}
	if state, ok, err := d.bootServiceState(); err != nil {
		return BootInfo{}, err
	} else if ok {
		info.OCR.Known = true
		info.OCR.Enabled = (state-bootServiceBase)&bootServiceOCR != 0
		info.Erase.Enabled = (state-bootServiceBase)&bootServiceRPE != 0
	}
	return info, nil
}

// bootServiceState reads CIM_BootService.EnabledState; ok is false on a device without the
// states of AMT 16 (a fault other than a refused read, or another value).
func (d *Device) bootServiceState() (state int, ok bool, err error) {
	resp, err := d.msgs.CIM.BootService.Get()
	if IsAccessDenied(err) {
		return 0, false, accessDenied(err, "read boot service")
	}
	if err != nil {
		return 0, false, nil
	}
	s := int(resp.Body.ServiceGetResponse.EnabledState)
	return s, s >= bootServiceBase && s <= bootServiceMax, nil
}

// SetOCR allows (on) or forbids One Click Recovery in the AMT boot service and keeps Remote
// Platform Erase as it is (both share CIM_BootService.EnabledState). No confirmation: it only
// allows a boot target. When the device refuses while HTTPS boot is off in the BIOS, the error
// wraps ErrOCROffInBIOS.
func (d *Device) SetOCR(on bool) error {
	return d.setBootService("set one click recovery", bootServiceOCR, on, func(cur amtboot.BootSettingDataResponse) error {
		if !cur.UEFIHTTPSBootEnabled {
			return ErrOCROffInBIOS
		}
		return nil
	})
}

// setBootService switches bit of CIM_BootService.EnabledState and keeps the other one. When the
// device refuses to switch on, biosOff tells from the boot settings whether the BIOS has the
// feature off (its error is wrapped then).
func (d *Device) setBootService(op string, bit int, on bool, biosOff func(amtboot.BootSettingDataResponse) error) error {
	state, ok, err := d.bootServiceState()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	want := state &^ bit
	if on {
		want |= bit
	}
	if want == state {
		return nil
	}
	if _, err := d.msgs.CIM.BootService.RequestStateChange(want); err != nil {
		if on && !IsAccessDenied(err) {
			if cur, gerr := d.getBootSettings(op); gerr == nil {
				if berr := biosOff(cur); berr != nil {
					return fmt.Errorf("%s: %w: %w", op, berr, err)
				}
			}
		}
		return writeError(op, err)
	}
	return nil
}

// Boot sets the boot options o for the next start and then resets (running) or powers on (not
// running) the device, in the sequence of Intel's console (SetBootOptions): clear the boot
// order, put the boot settings, set the boot config role, set the boot source (PXE, HTTPS or the
// hard drive in safe mode) and send the power command. It checks o against BootCapabilities
// before anything is changed. BootISO needs an open IDE-R session with an image (OpenIDER).
// A sleeping device is refused (ErrAsleep). When a step from the Put onward fails, Boot takes the
// boot settings back; if that fails too, the error is *BootMayBeSetError.
func (d *Device) Boot(o BootOptions) error {
	const op = "boot"
	info, err := d.BootInfo()
	if err != nil {
		return err
	}
	if err := checkBootOptions(o, info); err != nil {
		return err
	}
	tlv, params, err := httpsBootParams(o)
	if err != nil {
		return err
	}
	state, err := d.powerState()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if err := refuseAsleep(op, state); err != nil {
		return err
	}
	cur, err := d.getBootSettings(op)
	if err != nil {
		return err
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		return fmt.Errorf("%s: clear boot order: %w", op, err)
	}
	if err := d.finishBoot(bootRequest(cur, o, info, tlv, params), bootSource(o), state); err != nil {
		return d.undoBoot(op, cur, false, err)
	}
	return nil
}

// finishBoot runs the steps after the first change (Put onward).
func (d *Device) finishBoot(req amtboot.BootSettingDataRequest, source cimboot.Source, state models.PowerState) error {
	if _, err := d.msgs.AMT.BootSettingData.Put(req); err != nil {
		return fmt.Errorf("put boot settings: %w", err)
	}
	if err := d.setBootConfigRole(); err != nil {
		return err
	}
	if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(source); err != nil {
		return fmt.Errorf("set boot source: %w", err)
	}
	if state == models.PowerStateOn {
		return d.requestPowerState(PowerReset, power.MasterBusReset)
	}
	return d.requestPowerState(PowerOn, power.PowerOn)
}

// setBootConfigRole makes the boot configuration of the boot settings the one of the next start.
func (d *Device) setBootConfigRole() error {
	resp, err := d.msgs.CIM.BootService.SetBootConfigRole("Intel(r) AMT: Boot Configuration 0", 1)
	if err != nil {
		return fmt.Errorf("set boot config role: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "SetBootConfigRole_OUTPUT") {
		return fmt.Errorf("set boot config role: %w: SetBootConfigRole_OUTPUT", ErrIncompleteResponse)
	}
	if rv := int(resp.Body.SetBootConfigRole_OUTPUT.ReturnValue); rv != 0 {
		return &MethodError{Method: "SetBootConfigRole", ReturnValue: rv}
	}
	return nil
}

// checkBootOptions refuses a target or option the device does not offer, and options that do
// not go with the target.
func checkBootOptions(o BootOptions, info BootInfo) error {
	c := info.Caps
	switch o.Target {
	case BootHardDisk:
		if o.SafeMode && !c.SafeMode {
			return invalid("SafeMode", "not supported by the device")
		}
	case BootPXE:
		if !c.PXE {
			return invalid("Target", "PXE boot is not supported by the device")
		}
	case BootHTTPS:
		if !info.HTTPSBootAvailable() {
			return invalid("Target", "HTTPS boot is not available on the device")
		}
		if o.SOL || o.PasswordBypass {
			return invalid("Options", "HTTPS boot takes no console and no password bypass")
		}
	case BootBIOSSetup:
		if !c.BIOSSetup {
			return invalid("Target", "BIOS setup is not supported by the device")
		}
	case BootISO:
		if !c.IDER {
			return invalid("Target", "IDE-R is not supported by the device")
		}
	default:
		return invalid("Target", "unknown boot target")
	}
	if o.SafeMode && o.Target != BootHardDisk {
		return invalid("SafeMode", "only with the hard disk")
	}
	if o.BIOSPause && (o.Target == BootPXE || o.Target == BootHTTPS) {
		return invalid("BIOSPause", "not with a boot source (PXE, HTTPS)")
	}
	if o.EnforceSecureBoot != info.EnforceSecureBoot && !c.SecureBootControl {
		return invalid("EnforceSecureBoot", "the BIOS does not let AMT change Secure Boot")
	}
	for _, f := range []struct {
		name     string
		set, cap bool
	}{
		{"SOL", o.SOL, c.SOL}, {"BIOSPause", o.BIOSPause, c.BIOSPause},
		{"LockPower", o.LockPower, c.LockPower}, {"LockReset", o.LockReset, c.LockReset},
		{"LockSleep", o.LockSleep, c.LockSleep}, {"LockKeyboard", o.LockKeyboard, c.LockKeyboard},
		{"PasswordBypass", o.PasswordBypass, c.PasswordBypass}, {"ProgressEvents", o.ProgressEvents, c.ProgressEvents},
		{"Verbosity", o.Verbosity == VerbosityQuiet, c.VerbosityQuiet},
		{"Verbosity", o.Verbosity == VerbosityVerbose, c.VerboseAll},
		{"Verbosity", o.Verbosity == VerbosityBlank, c.ScreenBlank},
	} {
		if f.set && !f.cap {
			return invalid(f.name, "not supported by the device")
		}
	}
	if o.Verbosity < VerbosityDefault || o.Verbosity > VerbosityBlank {
		return invalid("Verbosity", "unknown value")
	}
	return nil
}

// httpsBootParams builds the TLV parameters of BootHTTPS (UefiBootParametersArray, base64):
// the URL and "sync the root certificates of AMT to the BIOS", as Intel's console does. Other
// targets have none.
func httpsBootParams(o BootOptions) (string, int, error) {
	if o.Target != BootHTTPS {
		return "", 0, nil
	}
	u, err := url.Parse(o.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(o.URL) > maxBootURL {
		return "", 0, invalid("URL", "an https URL of at most 300 bytes")
	}
	uri, err := amtboot.NewStringParameter(amtboot.OCR_EFI_NETWORK_DEVICE_PATH, o.URL)
	if err != nil {
		return "", 0, invalid("URL", "an https URL of at most 300 bytes")
	}
	params := []amtboot.TLVParameter{uri, amtboot.NewBoolParameter(amtboot.OCR_HTTPS_CERT_SYNC_ROOT_CA, true)}
	buf, err := amtboot.CreateTLVBuffer(params)
	if err != nil {
		return "", 0, err
	}
	return base64.StdEncoding.EncodeToString(buf), len(params), nil
}

// bootRequest is the Put of the boot settings for o; identity fields come from cur.
func bootRequest(cur amtboot.BootSettingDataResponse, o BootOptions, info BootInfo, tlv string, params int) amtboot.BootSettingDataRequest {
	secure := o.EnforceSecureBoot
	if !info.Caps.SecureBootControl {
		secure = info.EnforceSecureBoot
	}
	req := amtboot.BootSettingDataRequest{
		ElementName:          cur.ElementName,
		InstanceID:           cur.InstanceID,
		OwningEntity:         cur.OwningEntity,
		BIOSSetup:            o.Target == BootBIOSSetup,
		BIOSPause:            o.BIOSPause,
		UseIDER:              o.Target == BootISO,
		UseSOL:               o.SOL,
		UseSafeMode:          o.SafeMode,
		EnforceSecureBoot:    secure,
		LockPowerButton:      o.LockPower,
		LockResetButton:      o.LockReset,
		LockSleepButton:      o.LockSleep,
		LockKeyboard:         o.LockKeyboard,
		UserPasswordBypass:   o.PasswordBypass,
		ForcedProgressEvents: o.ProgressEvents,
		FirmwareVerbosity:    amtboot.FirmwareVerbosity(o.Verbosity),
	}
	if o.Target == BootISO {
		req.IDERBootDevice = amtboot.CDBoot
	}
	if o.Target == BootHTTPS {
		req.UefiBootParametersArray = tlv
		req.UefiBootNumberOfParams = params
	}
	return req
}

// bootSource is the CIM_BootSourceSetting of o, or "" for the default order (BIOS setup and the
// IDE-R image are boot settings, not sources).
func bootSource(o BootOptions) cimboot.Source {
	switch {
	case o.Target == BootPXE:
		return cimboot.PXE
	case o.Target == BootHTTPS:
		return cimboot.OCRUEFIHTTPS
	case o.Target == BootHardDisk && o.SafeMode:
		return cimboot.HardDrive
	}
	return ""
}
