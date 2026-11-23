package amt

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// allErase is a PlatformErase capability with RPE support and every action OpenLAMT offers.
const allErase = EraseSupported | EraseSSDs | EraseVerify | EraseTPM | EraseBIOSVariables | EraseBIOSReload | EraseUnconfigure

// eraseSeq answers a device that is on, supports Remote Platform Erase with caps, has it
// allowed in the boot service together with One Click Recovery (32771) and on in the BIOS.
func eraseSeq(t *testing.T, caps EraseActions) map[string][]cfgResp {
	t.Helper()
	seq := bootSeq()
	seq["Get|AMT_BootCapabilities"] = []cfgResp{{body: withProp(t, "boot_capabilities_ocr.xml", "PlatformErase", strconv.FormatUint(uint64(caps), 10))}}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_get_rpe.xml"}}
	withBootService(t, seq, "32771")
	return seq
}

// secureEraseSeq answers an older device with Remote Secure Erase only.
func secureEraseSeq(t *testing.T) map[string][]cfgResp {
	t.Helper()
	seq := bootSeq()
	seq["Get|AMT_BootCapabilities"] = []cfgResp{{body: withProp(t, "boot_capabilities_ocr.xml", "SecureErase", "true")}}
	withBootService(t, seq, "2")
	return seq
}

func eraseDevice(seq map[string][]cfgResp, tls bool) (*Device, *cfgWSMan) {
	d, f := bootDevice(seq)
	d.cfg.UseTLS = tls
	return d, f
}

func eraseConfirmed(t *testing.T) Confirmation {
	t.Helper()
	c, err := ConfirmChecked(OpErase, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// tlvOf decodes the UefiBootParametersArray of a Put.
func tlvOf(t *testing.T, put string) []byte {
	t.Helper()
	i := strings.Index(put, "<h:UefiBootParametersArray>") + len("<h:UefiBootParametersArray>")
	raw, err := base64.StdEncoding.DecodeString(put[i : i+strings.Index(put[i:], "<")])
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEraseInfoReadsCapabilities(t *testing.T) {
	d, _ := eraseDevice(eraseSeq(t, allErase), true)
	info, err := d.BootInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := EraseState{Platform: allErase, Enabled: true, BIOSEnabled: true}
	if info.Erase != want || !info.PlatformEraseSupported() || !info.EraseSupported() {
		t.Fatalf("erase %+v", info.Erase)
	}
	if !info.OCR.Enabled {
		t.Fatal("OCR bit lost")
	}

	seq := eraseSeq(t, allErase)
	withBootService(t, seq, "32769") // OCR on, RPE off
	d, _ = eraseDevice(seq, true)
	if info, _ := d.BootInfo(); info.Erase.Enabled || !info.PlatformEraseSupported() {
		t.Fatalf("rpe off: %+v", info.Erase)
	}

	// Without the boot service of AMT 16 there is no platform erase, whatever the BIOS says.
	seq = eraseSeq(t, allErase)
	withBootService(t, seq, "2")
	d, _ = eraseDevice(seq, true)
	if info, _ := d.BootInfo(); info.PlatformEraseSupported() || info.EraseSupported() {
		t.Fatalf("no boot service: %+v", info.Erase)
	}

	d, _ = eraseDevice(secureEraseSeq(t), false)
	if info, _ := d.BootInfo(); !info.Erase.Secure || info.PlatformEraseSupported() || !info.EraseSupported() {
		t.Fatalf("secure erase: %+v", info.Erase)
	}

	d, _ = eraseDevice(bootSeq(), true) // PlatformErase 0, SecureErase false
	if info, _ := d.BootInfo(); info.EraseSupported() {
		t.Fatalf("none: %+v", info.Erase)
	}
}

// Remote Platform Erase as Intel's console sends it: no boot order change, the Put with
// PlatformErase and the TLV parameters, the boot config role, then a hard power cycle.
func TestPlatformEraseSequence(t *testing.T) {
	d, f := eraseDevice(eraseSeq(t, allErase), true)
	const pw = "s3cret<&>"
	err := d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs | EraseVerify | EraseTPM, Password: pw}, eraseConfirmed(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Put|AMT_BootSettingData", "SetBootConfigRole|CIM_BootService", "RequestPowerStateChange|CIM_PowerManagementService"}
	if got := writes(f); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes %v", got)
	}
	put := onePut(t, f, "Put|AMT_BootSettingData")
	assertContains(t, put, "<h:PlatformErase>true</h:PlatformErase>", "<h:SecureErase>false</h:SecureErase>",
		"<h:RSEPassword></h:RSEPassword>", "<h:UefiBootNumberOfParams>2</h:UefiBootNumberOfParams>",
		"<h:BIOSSetup>false</h:BIOSSetup>", "<h:UseIDER>false</h:UseIDER>", "<h:InstanceID>Intel(r) AMT:BootSettingData 0</h:InstanceID>")
	// TLV: vendor 0x8086, type, 32-bit length, value (all little-endian); 1 = the actions, 20 =
	// the SSD master password.
	wantTLV := []byte{0x86, 0x80, 1, 0, 4, 0, 0, 0}
	wantTLV = binary.LittleEndian.AppendUint32(wantTLV, uint32(EraseSSDs|EraseVerify|EraseTPM))
	wantTLV = append(wantTLV, 0x86, 0x80, 20, 0, byte(len(pw)), 0, 0, 0)
	wantTLV = append(wantTLV, pw...)
	if raw := tlvOf(t, put); !reflect.DeepEqual(raw, wantTLV) {
		t.Fatalf("tlv % x\nwant % x", raw, wantTLV)
	}
	if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); !strings.Contains(p[0], "<h:PowerState>5</h:PowerState>") {
		t.Fatalf("not a power cycle: %s", p[0])
	}
}

// Without SSD erase or without a password the TLV holds the actions alone.
func TestPlatformEraseWithoutPassword(t *testing.T) {
	for _, o := range []EraseOptions{
		{Platform: true, Actions: EraseTPM | EraseUnconfigure, Password: "ignored"},
		{Platform: true, Actions: EraseSSDs},
	} {
		d, f := eraseDevice(eraseSeq(t, allErase), true)
		if err := d.Erase(o, eraseConfirmed(t)); err != nil {
			t.Fatal(err)
		}
		put := onePut(t, f, "Put|AMT_BootSettingData")
		assertContains(t, put, "<h:UefiBootNumberOfParams>1</h:UefiBootNumberOfParams>")
		want := binary.LittleEndian.AppendUint32([]byte{0x86, 0x80, 1, 0, 4, 0, 0, 0}, uint32(o.Actions))
		if raw := tlvOf(t, put); !reflect.DeepEqual(raw, want) {
			t.Errorf("%v: tlv % x", o.Actions, raw)
		}
	}
}

func TestPlatformEraseWhenOffPowersOn(t *testing.T) {
	seq := eraseSeq(t, allErase)
	seq["Pull|CIM_ServiceAvailableToElement"] = []cfgResp{{file: "power_state_pull_off.xml"}}
	d, f := eraseDevice(seq, true)
	if err := d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs}, eraseConfirmed(t)); err != nil {
		t.Fatal(err)
	}
	if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); !strings.Contains(p[0], "<h:PowerState>2</h:PowerState>") {
		t.Fatalf("not power on: %s", p[0])
	}
}

// Remote Secure Erase as MeshCommander sends it (power actions 104/105): clear the boot order,
// Put SecureErase with the password (XML-escaped: the library writes it as is), the boot config
// role, clear the boot order, then reset (on) or power on (off). It needs no TLS.
func TestSecureEraseSequence(t *testing.T) {
	d, f := eraseDevice(secureEraseSeq(t), false)
	if err := d.Erase(EraseOptions{Password: "a<b&c"}, eraseConfirmed(t)); err != nil {
		t.Fatal(err)
	}
	if got := writes(f); !reflect.DeepEqual(got, bootWrites) {
		t.Fatalf("writes %v", got)
	}
	put := onePut(t, f, "Put|AMT_BootSettingData")
	assertContains(t, put, "<h:SecureErase>true</h:SecureErase>", "<h:RSEPassword>a&lt;b&amp;c</h:RSEPassword>",
		"<h:PlatformErase>false</h:PlatformErase>", "<h:UefiBootNumberOfParams>0</h:UefiBootNumberOfParams>")
	for _, o := range f.requestsFor("ChangeBootOrder|CIM_BootConfigSetting") {
		if strings.Contains(o, "Selector") {
			t.Fatalf("boot source set: %s", o)
		}
	}
	if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); !strings.Contains(p[0], "<h:PowerState>10</h:PowerState>") {
		t.Fatalf("not reset: %s", p[0])
	}
}

// Without the confirmation of the red dialog nothing is sent, not even a read.
func TestEraseNeedsConfirmation(t *testing.T) {
	other, _ := ConfirmChecked(OpErase, "other.example.com", true)
	boot, _ := ConfirmChecked(OpBootOptions, "", true)
	for name, c := range map[string]Confirmation{"none": {}, "other device": other, "other operation": boot} {
		d, f := eraseDevice(eraseSeq(t, allErase), true)
		if err := d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs}, c); !errors.Is(err, ErrRiskyChange) || len(f.requests) != 0 {
			t.Errorf("%s: err %v, %d requests", name, err, len(f.requests))
		}
	}
}

// Remote Platform Erase needs TLS (Intel: "This command needs to execute over TLS"); without it
// nothing is sent. Remote Secure Erase does not.
func TestPlatformEraseNeedsTLS(t *testing.T) {
	d, f := eraseDevice(eraseSeq(t, allErase), false)
	if err := d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs}, eraseConfirmed(t)); !errors.Is(err, ErrEraseNeedsTLS) || len(f.requests) != 0 {
		t.Fatalf("err %v, %d requests", err, len(f.requests))
	}
}

func TestEraseRefusesWhatTheDeviceLacks(t *testing.T) {
	rpeOff := eraseSeq(t, allErase)
	withBootService(t, rpeOff, "32769")
	noTPM := eraseSeq(t, allErase&^EraseTPM)
	noRPE := eraseSeq(t, 0)
	const secret = "pw-never-in-errors"
	cases := map[string]struct {
		seq  map[string][]cfgResp
		o    EraseOptions
		want error
	}{
		"rpe off":          {rpeOff, EraseOptions{Platform: true, Actions: EraseSSDs}, ErrRPEOff},
		"action missing":   {noTPM, EraseOptions{Platform: true, Actions: EraseTPM}, ErrInvalidValue},
		"no action":        {eraseSeq(t, allErase), EraseOptions{Platform: true}, ErrInvalidValue},
		"pyrite revert":    {eraseSeq(t, allErase|ErasePyriteRevert), EraseOptions{Platform: true, Actions: ErasePyriteRevert}, ErrInvalidValue},
		"oem action":       {eraseSeq(t, allErase|EraseOEM), EraseOptions{Platform: true, Actions: EraseOEM}, ErrInvalidValue},
		"support bit":      {eraseSeq(t, allErase), EraseOptions{Platform: true, Actions: EraseSupported}, ErrInvalidValue},
		"no rpe":           {noRPE, EraseOptions{Platform: true, Actions: EraseSSDs}, ErrInvalidValue},
		"no secure erase":  {eraseSeq(t, allErase), EraseOptions{}, ErrInvalidValue},
		"rse actions":      {secureEraseSeq(t), EraseOptions{Actions: EraseSSDs}, ErrInvalidValue},
		"long password":    {eraseSeq(t, allErase), EraseOptions{Platform: true, Actions: EraseSSDs, Password: strings.Repeat("x", 33) + secret}, ErrInvalidValue},
		"non-ascii":        {secureEraseSeq(t), EraseOptions{Password: "pässwort" + secret}, ErrInvalidValue},
		"control in pw":    {secureEraseSeq(t), EraseOptions{Password: "a\nb" + secret}, ErrInvalidValue},
		"rse long":         {secureEraseSeq(t), EraseOptions{Password: strings.Repeat("y", 33)}, ErrInvalidValue},
		"rpe off with tls": {rpeOff, EraseOptions{Platform: true, Actions: EraseTPM}, ErrRPEOff},
	}
	for name, c := range cases {
		d, f := eraseDevice(c.seq, true)
		err := d.Erase(c.o, eraseConfirmed(t))
		if !errors.Is(err, c.want) || len(writes(f)) != 0 {
			t.Errorf("%s: err %v, writes %v", name, err, writes(f))
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error shows the password", name)
		}
	}
}

// A failed step after the Put says that the erase may still be armed for the next start, and
// no power command follows.
func TestEraseFailedStepWarns(t *testing.T) {
	seq := eraseSeq(t, allErase)
	seq["SetBootConfigRole|CIM_BootService"] = []cfgResp{{file: "setbootconfigrole_fail.xml"}}
	d, f := eraseDevice(seq, true)
	err := d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs}, eraseConfirmed(t))
	var me *MethodError
	if !errors.As(err, &me) || !strings.Contains(err.Error(), "may still be set") {
		t.Fatalf("err %v", err)
	}
	if len(f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService")) != 0 {
		t.Fatal("power command after a failed step")
	}
}

// The Remote Platform Erase switch keeps One Click Recovery as it is (both share
// CIM_BootService.EnabledState: 32768 + OCR bit 1 + RPE bit 2).
func TestSetRPEKeepsOCR(t *testing.T) {
	cases := []struct {
		state string
		on    bool
		want  string // requested state, "" for no write
	}{
		{"32768", true, "32770"},
		{"32769", true, "32771"},
		{"32771", false, "32769"},
		{"32770", false, "32768"},
		{"32770", true, ""},
	}
	for _, c := range cases {
		seq := eraseSeq(t, allErase)
		withBootService(t, seq, c.state)
		d, f := eraseDevice(seq, true)
		if err := d.SetRPE(c.on); err != nil {
			t.Fatal(err)
		}
		rsc := f.requestsFor("RequestStateChange|CIM_BootService")
		switch {
		case c.want == "" && len(rsc) != 0, c.want != "" && (len(rsc) != 1 || !strings.Contains(rsc[0], "<h:RequestedState>"+c.want+"</h:RequestedState>")):
			t.Errorf("%s on=%v: %q", c.state, c.on, rsc)
		}
	}
}

func TestSetRPERefusedWithBIOSOff(t *testing.T) {
	seq := eraseSeq(t, allErase)
	withBootService(t, seq, "32768")
	seq["RequestStateChange|CIM_BootService"] = []cfgResp{{file: "bootservice_rsc_fail.xml"}}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_get_https.xml"}} // RPEEnabled missing: off
	d, _ := eraseDevice(seq, true)
	if err := d.SetRPE(true); !errors.Is(err, ErrRPEOffInBIOS) {
		t.Fatalf("err %v", err)
	}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_get_rpe.xml"}}
	d, _ = eraseDevice(seq, true)
	if err := d.SetRPE(true); err == nil || errors.Is(err, ErrRPEOffInBIOS) {
		t.Fatalf("BIOS on: err %v", err)
	}
	withBootService(t, seq, "2")
	d, f := eraseDevice(seq, true)
	if err := d.SetRPE(true); !errors.Is(err, ErrNotModifiable) || len(writes(f)) != 0 {
		t.Fatalf("no boot service: %v", err)
	}
}

// Erase Drives is the red confirmation and cannot be undone over the network.
func TestEraseConfirmationLevel(t *testing.T) {
	if r := RiskOf(OpErase); r.Level != LevelRed || r.Severity != SeverityIrreversibleRemotely {
		t.Fatalf("risk %+v", r)
	}
}
