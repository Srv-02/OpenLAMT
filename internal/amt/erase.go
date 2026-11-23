package amt

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

// Remote Secure Erase and Remote Platform Erase (issue #20): the BIOS erases the drives (and,
// with Remote Platform Erase, more) at the next start. Both are boot options of
// AMT_BootSettingData, so they need the user consent of BootConsent first (Session.Erase runs
// both) and the red confirmation amt.OpErase.
//
// Remote Platform Erase (AMT 16 and later) is allowed in the boot service (CIM_BootService
// EnabledState bit 2, Settings › Remote features, SetRPE) and needs TLS. Its actions travel as
// TLV parameters in UefiBootParametersArray, built by hand here: the library's CreateTLVBuffer
// writes every type 20 parameter as the boolean of One Click Recovery, which is the SSD master
// password for Remote Platform Erase. Sources: MeshCommander (powerActionDlgRPEEx, actions 106
// and 107) and Intel's console (internal/usecase/devices/wsman/rpe.go).

// EraseActions is the bitmask of AMT_BootCapabilities.PlatformErase (what the BIOS can erase)
// and of the actions parameter of Remote Platform Erase.
type EraseActions uint32

const (
	EraseSupported     EraseActions = 1 << 0  // capability only: the BIOS supports Remote Platform Erase
	ErasePyriteRevert  EraseActions = 1 << 1  // not offered: it needs the PSID of the drive
	EraseSSDs          EraseActions = 1 << 2  // securely erase all SSDs
	EraseVerify        EraseActions = 1 << 3  // verify the storage erase
	EraseTPM           EraseActions = 1 << 6  // clear the TPM
	EraseOEM           EraseActions = 1 << 16 // not offered: OEM custom action with OEM data
	EraseBIOSVariables EraseActions = 1 << 25 // clear the BIOS NVM variables
	EraseBIOSReload    EraseActions = 1 << 26 // reload the BIOS golden (factory) configuration
	EraseUnconfigure   EraseActions = 1 << 31 // unconfigure Intel AMT (CSME): remote access ends
)

// EraseOffered lists the actions of Remote Platform Erase that OpenLAMT offers, in the order of
// the Erase Drives dialog.
var EraseOffered = []EraseActions{EraseSSDs, EraseVerify, EraseTPM, EraseBIOSVariables, EraseBIOSReload, EraseUnconfigure}

func eraseOfferedMask() EraseActions {
	var m EraseActions
	for _, a := range EraseOffered {
		m |= a
	}
	return m
}

// EraseState is what the device tells about erasing its drives.
type EraseState struct {
	Secure      bool         // AMT_BootCapabilities.SecureErase: Remote Secure Erase (older devices)
	Platform    EraseActions // AMT_BootCapabilities.PlatformErase
	Enabled     bool         // the boot service allows Remote Platform Erase (Settings › Remote features)
	BIOSEnabled bool         // AMT_BootSettingData.RPEEnabled: Remote Platform Erase is on in the BIOS
}

// PlatformEraseSupported reports whether the device offers Remote Platform Erase: the BIOS
// supports it and the device has the boot service of AMT 16 that switches it.
func (b BootInfo) PlatformEraseSupported() bool {
	return b.Erase.Platform&EraseSupported != 0 && b.OCR.Known
}

// EraseSupported reports whether the drives can be erased remotely at all: Remote Platform
// Erase, else Remote Secure Erase.
func (b BootInfo) EraseSupported() bool { return b.PlatformEraseSupported() || b.Erase.Secure }

// EraseOptions is what the Erase Drives dialog asks for.
type EraseOptions struct {
	// Platform: Remote Platform Erase with Actions; false: Remote Secure Erase of the boot drive.
	Platform bool
	Actions  EraseActions
	// Password is the drive password, "" for none: the RSE password, or with Remote Platform
	// Erase the SSD master password (sent with EraseSSDs only). Printable ASCII, at most 32
	// characters. It never appears in an error.
	Password string
}

// maxErasePassword is the limit of AMT_BootSettingData.RSEPassword (32 ASCII characters), the
// length of an ATA master password.
const maxErasePassword = 32

// Parameter types of the Remote Platform Erase TLV (MeshCommander powerActionDlgRPEEx).
const (
	rpeParamActions     = 1  // uint32 EraseActions
	rpeParamSSDPassword = 20 // the SSD master password
	intelVendor         = 0x8086
)

var (
	// ErrEraseNeedsTLS: Remote Platform Erase only runs over a TLS connection.
	ErrEraseNeedsTLS = errors.New("remote platform erase needs a TLS connection")
	// ErrRPEOff: the boot service does not allow Remote Platform Erase (Settings › Remote
	// features).
	ErrRPEOff = errors.New("remote platform erase is off on the device")
	// ErrRPEOffInBIOS: the device refused to allow Remote Platform Erase because it is off in its
	// BIOS.
	ErrRPEOffInBIOS = errors.New("remote platform erase: it is off in the BIOS of the device")
)

// bootServiceRPE is the Remote Platform Erase bit of CIM_BootService.EnabledState (AMT 16).
const bootServiceRPE = 2

// SetRPE allows (on) or forbids Remote Platform Erase in the AMT boot service and keeps One
// Click Recovery as it is. No confirmation: it only allows the erase, which has its own red one.
// When the device refuses while the BIOS has it off, the error wraps ErrRPEOffInBIOS.
func (d *Device) SetRPE(on bool) error {
	return d.setBootService("set remote platform erase", bootServiceRPE, on, func(cur amtboot.BootSettingDataResponse) error {
		if !cur.RPEEnabled {
			return ErrRPEOffInBIOS
		}
		return nil
	})
}

// Erase arms the erase o for the next start and then restarts or powers on the device. It
// checks the confirmation (amt.OpErase) and, for Remote Platform Erase, TLS before anything is
// sent, then o against the capabilities before anything is changed.
//
// Remote Platform Erase follows Intel's console: Put PlatformErase with the TLV parameters, set
// the boot config role, then a hard power cycle (a warm reset does not run the erase) or power
// on. Remote Secure Erase follows MeshCommander: clear the boot order, Put SecureErase with the
// password, set the boot config role, clear the boot order, then reset or power on. A sleeping
// device is refused (ErrAsleep). When a step from the Put onward fails, Erase takes the boot
// settings back; if that fails too, the error is *BootMayBeSetError (ErrEraseMayBeSet).
func (d *Device) Erase(o EraseOptions, c Confirmation) error {
	const op = "erase"
	if err := d.confirmed(c, OpErase, "Erase"); err != nil {
		return err
	}
	if o.Platform && !d.cfg.UseTLS {
		return fmt.Errorf("%s: %w", op, ErrEraseNeedsTLS)
	}
	info, err := d.BootInfo()
	if err != nil {
		return err
	}
	if err := checkErase(o, info); err != nil {
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
	req := amtboot.BootSettingDataRequest{
		ElementName:       cur.ElementName,
		InstanceID:        cur.InstanceID,
		OwningEntity:      cur.OwningEntity,
		EnforceSecureBoot: cur.EnforceSecureBoot,
	}
	if o.Platform {
		req.PlatformErase = true
		req.UefiBootParametersArray, req.UefiBootNumberOfParams = eraseParams(o)
	} else {
		req.SecureErase = true
		req.RSEPassword = xmlText(o.Password) // the library writes it into the body as is
		if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
			return fmt.Errorf("%s: clear boot order: %w", op, err)
		}
	}
	if err := d.finishErase(req, o.Platform, state); err != nil {
		return d.undoBoot(op, cur, true, err)
	}
	return nil
}

// finishErase runs the steps after the first change (Put onward).
func (d *Device) finishErase(req amtboot.BootSettingDataRequest, platform bool, state models.PowerState) error {
	if _, err := d.msgs.AMT.BootSettingData.Put(req); err != nil {
		return fmt.Errorf("put boot settings: %w", err)
	}
	if err := d.setBootConfigRole(); err != nil {
		return err
	}
	if !platform {
		if _, err := d.msgs.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
			return fmt.Errorf("clear boot order: %w", err)
		}
	}
	switch {
	case state != models.PowerStateOn:
		return d.requestPowerState(PowerOn, power.PowerOn)
	case platform:
		return d.requestPowerState(PowerCycle, power.PowerCycleOffHard)
	}
	return d.requestPowerState(PowerReset, power.MasterBusReset)
}

// checkErase refuses what the device does not offer. Its errors never contain the password.
func checkErase(o EraseOptions, info BootInfo) error {
	if err := ValidateErasePassword(o.Password); err != nil {
		return err
	}
	if !o.Platform {
		if !info.Erase.Secure {
			return invalid("Erase", "Remote Secure Erase is not supported by the device")
		}
		if o.Actions != 0 {
			return invalid("Actions", "Remote Secure Erase takes no actions")
		}
		return nil
	}
	if !info.PlatformEraseSupported() {
		return invalid("Erase", "Remote Platform Erase is not supported by the device")
	}
	if !info.Erase.Enabled {
		return fmt.Errorf("erase: %w", ErrRPEOff)
	}
	if o.Actions == 0 {
		return invalid("Actions", "choose at least one")
	}
	if o.Actions&^eraseOfferedMask() != 0 {
		return invalid("Actions", "not offered by OpenLAMT")
	}
	if o.Actions&^info.Erase.Platform != 0 {
		return invalid("Actions", "not supported by the device")
	}
	return nil
}

// ValidateErasePassword checks a drive password: printable ASCII, at most 32 characters. The
// error never contains the password.
func ValidateErasePassword(pw string) error {
	if len(pw) > maxErasePassword {
		return invalid("Password", fmt.Sprintf("at most %d characters", maxErasePassword))
	}
	for i := 0; i < len(pw); i++ {
		if c := pw[i]; c < 0x20 || c > 0x7e {
			return invalid("Password", "printable ASCII characters only")
		}
	}
	return nil
}

// eraseParams builds the TLV parameters of Remote Platform Erase (base64): the actions and,
// with EraseSSDs and a password, the SSD master password. Each entry is vendor 0x8086, type
// and a 32-bit length (little-endian), then the value.
func eraseParams(o EraseOptions) (string, int) {
	tlv := func(buf []byte, typ uint16, value []byte) []byte {
		buf = binary.LittleEndian.AppendUint16(buf, intelVendor)
		buf = binary.LittleEndian.AppendUint16(buf, typ)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(value)))
		return append(buf, value...)
	}
	buf := tlv(nil, rpeParamActions, binary.LittleEndian.AppendUint32(nil, uint32(o.Actions)))
	n := 1
	if o.Actions&EraseSSDs != 0 && o.Password != "" {
		buf = tlv(buf, rpeParamSSDPassword, []byte(o.Password))
		n++
	}
	return base64.StdEncoding.EncodeToString(buf), n
}
