package amt

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
)

// bootSeq answers the reads and writes of Boot, BootInfo and SetOCR on a device that is on,
// supports HTTPS boot and has One Click Recovery allowed (EnabledState 32769).
func bootSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Get|AMT_BootCapabilities":                           {{file: "boot_capabilities_ocr.xml"}},
		"Get|AMT_BootSettingData":                            {{file: "boot_settingdata_get_https.xml"}},
		"Put|AMT_BootSettingData":                            {{file: "boot_settingdata_put.xml"}},
		"Get|CIM_BootService":                                {{file: "bootservice_get.xml"}},
		"RequestStateChange|CIM_BootService":                 {{file: "bootservice_rsc.xml"}},
		"ChangeBootOrder|CIM_BootConfigSetting":              {{file: "changebootorder.xml"}},
		"SetBootConfigRole|CIM_BootService":                  {{file: "setbootconfigrole.xml"}},
		"Enumerate|CIM_ServiceAvailableToElement":            {{file: "enumerate.xml"}},
		"Pull|CIM_ServiceAvailableToElement":                 {{file: "power_state_pull.xml"}},
		"RequestPowerStateChange|CIM_PowerManagementService": {{file: "power_action_ok.xml"}},
	}
}

func bootDevice(seq map[string][]cfgResp) (*Device, *cfgWSMan) {
	f := &cfgWSMan{seq: seq}
	return newDevice(f, ""), f
}

func withBootService(t *testing.T, seq map[string][]cfgResp, state string) {
	t.Helper()
	seq["Get|CIM_BootService"] = []cfgResp{{body: withProp(t, "bootservice_get.xml", "EnabledState", state)}}
}

func TestBootInfoReadsCapabilitiesAndOCR(t *testing.T) {
	d, _ := bootDevice(bootSeq())
	info, err := d.BootInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := BootCapabilities{PXE: true, SafeMode: true, BIOSSetup: true, BIOSPause: true, IDER: true, SOL: true,
		LockPower: true, LockReset: true, LockKeyboard: true, PasswordBypass: true, VerbosityQuiet: true,
		VerboseAll: true, SecureBootControl: true, HTTPSBoot: true}
	if info.Caps != want {
		t.Fatalf("caps %+v\nwant %+v", info.Caps, want)
	}
	if info.OCR != (OCRState{Known: true, Enabled: true, BIOSEnabled: true}) || !info.HTTPSBootAvailable() {
		t.Fatalf("ocr %+v", info.OCR)
	}
}

func TestBootInfoOCRStates(t *testing.T) {
	for state, want := range map[string]OCRState{
		"32768": {Known: true, BIOSEnabled: true},
		"32770": {Known: true, BIOSEnabled: true},
		"32771": {Known: true, Enabled: true, BIOSEnabled: true},
		"2":     {BIOSEnabled: true}, // AMT before 16: no One Click Recovery
	} {
		seq := bootSeq()
		withBootService(t, seq, state)
		d, _ := bootDevice(seq)
		info, err := d.BootInfo()
		if err != nil || info.OCR != want {
			t.Errorf("state %s: %+v, %v", state, info.OCR, err)
		}
	}
	seq := bootSeq()
	seq["Get|CIM_BootService"] = []cfgResp{{err: errors.New("fault: ActionNotSupported")}}
	d, _ := bootDevice(seq)
	if info, err := d.BootInfo(); err != nil || info.OCR.Known || info.HTTPSBootAvailable() {
		t.Fatalf("without boot service: %+v, %v", info.OCR, err)
	}
}

func TestBootInfoEmptyCapabilitiesIsIncomplete(t *testing.T) {
	seq := bootSeq()
	seq["Get|AMT_BootCapabilities"] = []cfgResp{{file: "empty_body.xml"}}
	d, _ := bootDevice(seq)
	if _, err := d.BootInfo(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("err %v", err)
	}
}

var bootWrites = []string{"ChangeBootOrder|CIM_BootConfigSetting", "Put|AMT_BootSettingData",
	"SetBootConfigRole|CIM_BootService", "ChangeBootOrder|CIM_BootConfigSetting",
	"RequestPowerStateChange|CIM_PowerManagementService"}

// writes returns the routes of f that change the device, in order.
func writes(f *cfgWSMan) []string {
	var out []string
	for _, r := range f.requests {
		k := routeKey(r)
		if !strings.HasPrefix(k, "Get|") && !strings.HasPrefix(k, "Enumerate|") && !strings.HasPrefix(k, "Pull|") {
			out = append(out, k)
		}
	}
	return out
}

func TestBootPXEWithOptionsResets(t *testing.T) {
	d, f := bootDevice(bootSeq())
	err := d.Boot(BootOptions{Target: BootPXE, SOL: true, LockPower: true, LockKeyboard: true, PasswordBypass: true,
		Verbosity: VerbosityVerbose})
	if err != nil {
		t.Fatal(err)
	}
	if got := writes(f); !reflect.DeepEqual(got, bootWrites) {
		t.Fatalf("writes %v", got)
	}
	put := onePut(t, f, "Put|AMT_BootSettingData")
	assertContains(t, put, "<h:UseSOL>true</h:UseSOL>", "<h:LockPowerButton>true</h:LockPowerButton>",
		"<h:LockKeyboard>true</h:LockKeyboard>", "<h:UserPasswordBypass>true</h:UserPasswordBypass>",
		"<h:FirmwareVerbosity>2</h:FirmwareVerbosity>", "<h:BIOSSetup>false</h:BIOSSetup>", "<h:UseIDER>false</h:UseIDER>",
		"<h:LockResetButton>false</h:LockResetButton>", "<h:InstanceID>Intel(r) AMT:BootSettingData 0</h:InstanceID>")
	orders := f.requestsFor("ChangeBootOrder|CIM_BootConfigSetting")
	if strings.Contains(orders[0], "Selector") || !strings.Contains(orders[1], "Intel(r) AMT: Force PXE Boot") {
		t.Fatalf("boot orders %q", orders)
	}
	if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); !strings.Contains(p[0], "<h:PowerState>10</h:PowerState>") {
		t.Fatal("not reset")
	}
}

func TestBootWhenOffPowersOn(t *testing.T) {
	seq := bootSeq()
	seq["Pull|CIM_ServiceAvailableToElement"] = []cfgResp{{file: "power_state_pull_off.xml"}}
	d, f := bootDevice(seq)
	if err := d.Boot(BootOptions{Target: BootBIOSSetup}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Put|AMT_BootSettingData"), "<h:BIOSSetup>true</h:BIOSSetup>")
	if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); !strings.Contains(p[0], "<h:PowerState>2</h:PowerState>") {
		t.Fatal("not power on")
	}
	if strings.Contains(f.requestsFor("ChangeBootOrder|CIM_BootConfigSetting")[1], "Selector") {
		t.Fatal("BIOS setup sets no boot source")
	}
}

func TestBootTargetsAndSources(t *testing.T) {
	cases := []struct {
		o      BootOptions
		put    []string
		source string
	}{
		{BootOptions{Target: BootHardDisk}, []string{"<h:UseSafeMode>false</h:UseSafeMode>"}, ""},
		{BootOptions{Target: BootHardDisk, SafeMode: true, BIOSPause: true}, []string{"<h:UseSafeMode>true</h:UseSafeMode>", "<h:BIOSPause>true</h:BIOSPause>"}, "Force Hard-drive Boot"},
		{BootOptions{Target: BootISO}, []string{"<h:UseIDER>true</h:UseIDER>", "<h:IDERBootDevice>1</h:IDERBootDevice>"}, ""},
		{BootOptions{Target: BootHTTPS, URL: "https://192.0.2.50:8443/x/recovery.efi"}, []string{"<h:UefiBootNumberOfParams>2</h:UefiBootNumberOfParams>"}, "Force OCR UEFI HTTPS Boot"},
	}
	for _, c := range cases {
		d, f := bootDevice(bootSeq())
		if err := d.Boot(c.o); err != nil {
			t.Fatalf("%v: %v", c.o.Target, err)
		}
		assertContains(t, onePut(t, f, "Put|AMT_BootSettingData"), c.put...)
		src := f.requestsFor("ChangeBootOrder|CIM_BootConfigSetting")[1]
		if c.source == "" && strings.Contains(src, "Selector") || c.source != "" && !strings.Contains(src, c.source) {
			t.Errorf("%v: source %q", c.o.Target, src)
		}
	}
}

// The HTTPS boot parameters are the URL (type 1) and "sync root certificates" (type 20), in
// the TLV layout of the library: vendor 0x8086, type, 32-bit length, value.
func TestBootHTTPSParameters(t *testing.T) {
	d, f := bootDevice(bootSeq())
	const u = "https://192.0.2.50:8443/a1/recovery.efi"
	if err := d.Boot(BootOptions{Target: BootHTTPS, URL: u, EnforceSecureBoot: true}); err != nil {
		t.Fatal(err)
	}
	put := onePut(t, f, "Put|AMT_BootSettingData")
	assertContains(t, put, "<h:EnforceSecureBoot>true</h:EnforceSecureBoot>", "<h:UseSOL>false</h:UseSOL>")
	i := strings.Index(put, "<h:UefiBootParametersArray>") + len("<h:UefiBootParametersArray>")
	raw, err := base64.StdEncoding.DecodeString(put[i : i+strings.Index(put[i:], "<")])
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x86, 0x80, 1, 0, byte(len(u)), 0, 0, 0}, u...)
	want = append(want, 0x86, 0x80, 20, 0, 1, 0, 0, 0, 1)
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("tlv % x\nwant % x", raw, want)
	}
}

func TestBootRefusesWhatTheDeviceLacks(t *testing.T) {
	noOCR := bootSeq()
	withBootService(t, noOCR, "32768")
	cases := map[string]struct {
		seq map[string][]cfgResp
		o   BootOptions
	}{
		"ocr off":           {noOCR, BootOptions{Target: BootHTTPS, URL: "https://192.0.2.50/r.efi"}},
		"sleep lock":        {bootSeq(), BootOptions{Target: BootPXE, LockSleep: true}},
		"progress":          {bootSeq(), BootOptions{Target: BootPXE, ProgressEvents: true}},
		"blank":             {bootSeq(), BootOptions{Target: BootPXE, Verbosity: VerbosityBlank}},
		"safe mode pxe":     {bootSeq(), BootOptions{Target: BootPXE, SafeMode: true}},
		"pause with pxe":    {bootSeq(), BootOptions{Target: BootPXE, BIOSPause: true}},
		"http url":          {bootSeq(), BootOptions{Target: BootHTTPS, URL: "http://192.0.2.50/r.efi"}},
		"long url":          {bootSeq(), BootOptions{Target: BootHTTPS, URL: "https://192.0.2.50/" + strings.Repeat("a", 290)}},
		"sol with https":    {bootSeq(), BootOptions{Target: BootHTTPS, URL: "https://192.0.2.50/r.efi", SOL: true}},
		"unknown target":    {bootSeq(), BootOptions{Target: BootTarget(9)}},
		"unknown verbosity": {bootSeq(), BootOptions{Target: BootPXE, Verbosity: Verbosity(7)}},
	}
	for name, c := range cases {
		d, f := bootDevice(c.seq)
		if err := d.Boot(c.o); !errors.Is(err, ErrInvalidValue) || len(writes(f)) != 0 {
			t.Errorf("%s: err %v, writes %v", name, err, writes(f))
		}
	}
}

// Without AMTSecureBootControl the current EnforceSecureBoot is kept; a change is refused.
func TestBootKeepsSecureBootWithoutControl(t *testing.T) {
	seq := bootSeq()
	seq["Get|AMT_BootCapabilities"] = []cfgResp{{body: withProp(t, "boot_capabilities_ocr.xml", "AMTSecureBootControl", "false")}}
	d, f := bootDevice(seq)
	if err := d.Boot(BootOptions{Target: BootPXE, EnforceSecureBoot: true}); !errors.Is(err, ErrInvalidValue) || len(writes(f)) != 0 {
		t.Fatalf("change: %v", err)
	}
	if err := d.Boot(BootOptions{Target: BootPXE}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Put|AMT_BootSettingData"), "<h:EnforceSecureBoot>false</h:EnforceSecureBoot>")
}

func TestBootFailedStepWarns(t *testing.T) {
	seq := bootSeq()
	seq["SetBootConfigRole|CIM_BootService"] = []cfgResp{{file: "setbootconfigrole_fail.xml"}}
	d, f := bootDevice(seq)
	err := d.Boot(BootOptions{Target: BootPXE})
	var me *MethodError
	if !errors.As(err, &me) || me.ReturnValue != 2 || !strings.Contains(err.Error(), "may still be set") {
		t.Fatalf("err %v", err)
	}
	if len(f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService")) != 0 {
		t.Fatal("power command after a failed step")
	}
}

func TestSetOCRKeepsRemoteErase(t *testing.T) {
	cases := []struct {
		state string
		on    bool
		want  string // requested state, "" for no write
	}{
		{"32768", true, "32769"},
		{"32770", true, "32771"},
		{"32771", false, "32770"},
		{"32769", false, "32768"},
		{"32769", true, ""},
	}
	for _, c := range cases {
		seq := bootSeq()
		withBootService(t, seq, c.state)
		d, f := bootDevice(seq)
		if err := d.SetOCR(c.on); err != nil {
			t.Fatal(err)
		}
		rsc := f.requestsFor("RequestStateChange|CIM_BootService")
		switch {
		case c.want == "" && len(rsc) != 0, c.want != "" && (len(rsc) != 1 || !strings.Contains(rsc[0], "<h:RequestedState>"+c.want+"</h:RequestedState>")):
			t.Errorf("%s on=%v: %q", c.state, c.on, rsc)
		}
	}
}

func TestSetOCRRefusedWithBIOSOff(t *testing.T) {
	seq := bootSeq()
	withBootService(t, seq, "32768")
	seq["RequestStateChange|CIM_BootService"] = []cfgResp{{file: "bootservice_rsc_fail.xml"}}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_get.xml"}} // UEFIHTTPSBootEnabled missing: off
	d, _ := bootDevice(seq)
	if err := d.SetOCR(true); !errors.Is(err, ErrOCROffInBIOS) {
		t.Fatalf("err %v", err)
	}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_get_https.xml"}}
	d, _ = bootDevice(seq)
	if err := d.SetOCR(true); err == nil || errors.Is(err, ErrOCROffInBIOS) {
		t.Fatalf("BIOS on: err %v", err)
	}
}

func TestSetOCRWithoutBootServiceIsNotModifiable(t *testing.T) {
	seq := bootSeq()
	withBootService(t, seq, "2")
	d, f := bootDevice(seq)
	if err := d.SetOCR(true); !errors.Is(err, ErrNotModifiable) || len(writes(f)) != 0 {
		t.Fatalf("err %v", err)
	}
}

func TestBootTargetStrings(t *testing.T) {
	for tg, s := range map[BootTarget]string{BootHardDisk: "disk", BootPXE: "pxe", BootHTTPS: "https", BootBIOSSetup: "bios", BootISO: "iso"} {
		if tg.String() != s {
			t.Errorf("%d: %q", tg, tg.String())
		}
	}
	if Verbosity(amtboot.ScreenBlank) != VerbosityBlank {
		t.Fatal("verbosity mapping")
	}
}

// One Click Recovery from a file confirms with the orange level; the other boot targets are
// confirmed by the dialog itself (simple).
func TestBootConfirmationLevels(t *testing.T) {
	if RiskOf(OpBootFile).Level != LevelOrange || RiskOf(OpBootOptions).Level != LevelSimple {
		t.Fatal("levels")
	}
}
