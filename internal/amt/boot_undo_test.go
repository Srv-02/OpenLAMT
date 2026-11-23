package amt

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Issue #123: a step from the Put of the boot settings onward fails, so the boot options (or an
// erase) would stay set for a later restart. Every path takes them back once; when that fails
// too, the error says so (ErrBootMayBeSet, ErrEraseMayBeSet).

// armedOp is one path that writes the boot settings, with the device it runs on.
type armedOp struct {
	name  string
	erase bool
	seq   func(t *testing.T) map[string][]cfgResp
	tls   bool
	run   func(d *Device) error
}

// biosSeq is biosRoutes as a sequence for cfgWSMan.
func biosSeq(get string) map[string][]cfgResp {
	seq := map[string][]cfgResp{}
	for route, file := range biosRoutes("power_state_pull.xml") {
		seq[route] = []cfgResp{{file: file}}
	}
	seq["Get|AMT_BootSettingData"] = []cfgResp{{file: get}}
	return seq
}

func armedOps(t *testing.T) []armedOp {
	return []armedOp{
		{name: "boot options", seq: func(*testing.T) map[string][]cfgResp { return bootSeq() },
			run: func(d *Device) error { return d.Boot(BootOptions{Target: BootPXE, LockPower: true}) }},
		{name: "one click recovery", seq: func(*testing.T) map[string][]cfgResp { return bootSeq() },
			run: func(d *Device) error { return d.Boot(BootOptions{Target: BootHTTPS, URL: "https://example.com/r.iso"}) }},
		{name: "platform erase", erase: true, tls: true, seq: func(t *testing.T) map[string][]cfgResp { return eraseSeq(t, allErase) },
			run: func(d *Device) error {
				return d.Erase(EraseOptions{Platform: true, Actions: EraseSSDs}, eraseConfirmed(t))
			}},
		{name: "secure erase", erase: true, seq: secureEraseSeq,
			run: func(d *Device) error { return d.Erase(EraseOptions{}, eraseConfirmed(t)) }},
		{name: "reboot to BIOS", seq: func(*testing.T) map[string][]cfgResp { return biosSeq("boot_settingdata_get.xml") },
			run: func(d *Device) error { return d.Power(BootToBIOS) }},
		{name: "boot from ISO", seq: func(*testing.T) map[string][]cfgResp { return biosSeq("boot_settingdata_get.xml") },
			run: func(d *Device) error { return d.Power(BootToISO) }},
	}
}

// assertCleared checks that the last Put switches every boot option off and that the boot order
// was cleared after it.
func assertCleared(t *testing.T, f *cfgWSMan) {
	t.Helper()
	puts := f.requestsFor("Put|AMT_BootSettingData")
	if len(puts) != 2 {
		t.Fatalf("%d Puts, want the write and the undo", len(puts))
	}
	assertContains(t, puts[1], "<h:PlatformErase>false</h:PlatformErase>", "<h:SecureErase>false</h:SecureErase>",
		"<h:UseIDER>false</h:UseIDER>", "<h:BIOSSetup>false</h:BIOSSetup>", "<h:LockPowerButton>false</h:LockPowerButton>",
		"<h:UefiBootNumberOfParams>0</h:UefiBootNumberOfParams>", "<h:RSEPassword></h:RSEPassword>",
		"<h:InstanceID>Intel(r) AMT:BootSettingData 0</h:InstanceID>")
	w := writes(f)
	if w[len(w)-1] != "ChangeBootOrder|CIM_BootConfigSetting" || w[len(w)-2] != "Put|AMT_BootSettingData" {
		t.Fatalf("writes %v, want the undo Put and a cleared boot order last", w)
	}
}

func TestArmedBootRefusedPowerIsTakenBack(t *testing.T) {
	for _, op := range armedOps(t) {
		t.Run(op.name, func(t *testing.T) {
			seq := op.seq(t)
			seq["RequestPowerStateChange|CIM_PowerManagementService"] = []cfgResp{{file: "power_action_fail.xml"}}
			d, f := eraseDevice(seq, op.tls)
			err := op.run(d)
			var pe *PowerError
			if !errors.As(err, &pe) || pe.ReturnValue != 2 {
				t.Fatalf("err %v", err)
			}
			if errors.Is(err, ErrBootMayBeSet) || !strings.Contains(err.Error(), "cleared the boot settings again") {
				t.Fatalf("err %v", err)
			}
			assertCleared(t, f)
		})
	}
}

func TestArmedBootDroppedConnectionIsTakenBack(t *testing.T) {
	dropped := fmt.Errorf("post: %w", io.ErrUnexpectedEOF)
	for _, op := range armedOps(t) {
		t.Run(op.name, func(t *testing.T) {
			seq := op.seq(t)
			seq["SetBootConfigRole|CIM_BootService"] = []cfgResp{{err: dropped}}
			d, f := eraseDevice(seq, op.tls)
			err := op.run(d)
			if !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrBootMayBeSet) {
				t.Fatalf("err %v", err)
			}
			if len(f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService")) != 0 {
				t.Fatal("power command after a failed step")
			}
			assertCleared(t, f)
		})
	}
}

// The connection stays down after the Put: the undo fails too, and the error says that the boot
// options (or the erase) may still be set.
func TestArmedBootUndoFailsTooWarns(t *testing.T) {
	dropped := fmt.Errorf("post: %w", io.ErrUnexpectedEOF)
	for _, op := range armedOps(t) {
		for _, step := range []string{"SetBootConfigRole|CIM_BootService", "RequestPowerStateChange|CIM_PowerManagementService"} {
			t.Run(op.name+" "+step, func(t *testing.T) {
				seq := op.seq(t)
				seq[step] = []cfgResp{{err: dropped}}
				seq["Put|AMT_BootSettingData"] = []cfgResp{{file: "boot_settingdata_put.xml"}, {err: dropped}}
				d, f := eraseDevice(seq, op.tls)
				err := op.run(d)
				var be *BootMayBeSetError
				if !errors.As(err, &be) || !errors.Is(err, ErrBootMayBeSet) || be.Erase != op.erase ||
					errors.Is(err, ErrEraseMayBeSet) != op.erase || !errors.Is(be.ClearErr, io.ErrUnexpectedEOF) {
					t.Fatalf("err %v", err)
				}
				if len(f.requestsFor("Put|AMT_BootSettingData")) != 2 {
					t.Fatalf("writes %v", writes(f))
				}
				want := "the boot options may still be set"
				if op.erase {
					want = "the next start of the device may erase the drives"
				}
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err %v lacks %q", err, want)
				}
			})
		}
	}
}

// A failed Put may still have reached the device: the undo runs for it as well.
func TestArmedBootFailedPutIsTakenBack(t *testing.T) {
	for _, op := range armedOps(t) {
		t.Run(op.name, func(t *testing.T) {
			seq := op.seq(t)
			seq["Put|AMT_BootSettingData"] = []cfgResp{{err: io.ErrUnexpectedEOF}, {file: "boot_settingdata_put.xml"}}
			d, f := eraseDevice(seq, op.tls)
			if err := op.run(d); !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrBootMayBeSet) {
				t.Fatalf("err %v", err)
			}
			assertCleared(t, f)
		})
	}
}

// Issue #123: Power On only resumes a sleeping device (S3) without a start of the BIOS, so the
// boot options and an erase would stay set. Every path refuses it before anything is written;
// hibernate (S4) starts through the BIOS and is powered on as before.
func TestArmedBootRefusesSleepingDevice(t *testing.T) {
	for _, op := range armedOps(t) {
		for _, state := range []string{"3", "4"} {
			t.Run(op.name+" "+state, func(t *testing.T) {
				seq := op.seq(t)
				seq["Pull|CIM_ServiceAvailableToElement"] = []cfgResp{{body: withProp(t, "power_state_pull.xml", "PowerState", state)}}
				d, f := eraseDevice(seq, op.tls)
				if err := op.run(d); !errors.Is(err, ErrAsleep) || len(writes(f)) != 0 {
					t.Fatalf("err %v, writes %v", err, writes(f))
				}
			})
		}
		t.Run(op.name+" hibernate", func(t *testing.T) {
			seq := op.seq(t)
			seq["Pull|CIM_ServiceAvailableToElement"] = []cfgResp{{body: withProp(t, "power_state_pull.xml", "PowerState", "7")}}
			d, f := eraseDevice(seq, op.tls)
			if err := op.run(d); err != nil {
				t.Fatal(err)
			}
			if p := f.requestsFor("RequestPowerStateChange|CIM_PowerManagementService"); len(p) != 1 || !strings.Contains(p[0], "<h:PowerState>2</h:PowerState>") {
				t.Fatalf("power %q", p)
			}
		})
	}
}

// TestRefuseAsleepReadsOnlyThePowerState (issue #142): the check the session runs before the
// consent refuses S3 (3 and 4) and nothing else, and it only reads.
func TestRefuseAsleepReadsOnlyThePowerState(t *testing.T) {
	for state, asleep := range map[string]bool{"2": false, "3": true, "4": true, "7": false, "8": false} {
		seq := biosSeq("boot_settingdata_get.xml")
		seq["Pull|CIM_ServiceAvailableToElement"] = []cfgResp{{body: withProp(t, "power_state_pull.xml", "PowerState", state)}}
		d, f := bootDevice(seq)
		err := d.RefuseAsleep("boot")
		if errors.Is(err, ErrAsleep) != asleep || (!asleep && err != nil) || len(writes(f)) != 0 || len(f.requests) != 2 {
			t.Fatalf("state %s: err %v, requests %d, writes %v", state, err, len(f.requests), writes(f))
		}
	}
}

func TestNextBootFromISOFailedStepIsTakenBack(t *testing.T) {
	seq := biosSeq("boot_settingdata_get.xml")
	seq["SetBootConfigRole|CIM_BootService"] = []cfgResp{{file: "setbootconfigrole_fail.xml"}}
	d, f := bootDevice(seq)
	if err := d.NextBootFromISO(); err == nil || errors.Is(err, ErrBootMayBeSet) {
		t.Fatalf("err %v", err)
	}
	assertCleared(t, f)
}
