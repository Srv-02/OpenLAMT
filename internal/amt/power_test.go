package amt

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	power "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
)

const powerRoute = "RequestPowerStateChange|CIM_PowerManagementService"

func TestPowerSendsCIMCode(t *testing.T) {
	cases := map[PowerAction]string{
		PowerOn:       "<h:PowerState>2</h:PowerState>",
		PowerOff:      "<h:PowerState>8</h:PowerState>",
		PowerReset:    "<h:PowerState>10</h:PowerState>",
		PowerCycle:    "<h:PowerState>5</h:PowerState>",
		PowerShutdown: "<h:PowerState>12</h:PowerState>",
	}
	for action, want := range cases {
		f := &fakeWSMan{routes: map[string]string{powerRoute: "power_action_ok.xml"}}
		if err := newDevice(f, "").Power(action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if len(f.requests) != 1 || !strings.Contains(f.requests[0], want) {
			t.Fatalf("%s: %v", action, f.requests)
		}
	}
}

func TestPowerReturnValueError(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{powerRoute: "power_action_fail.xml"}}
	err := newDevice(f, "").Power(PowerOff)
	var pe *PowerError
	if !errors.As(err, &pe) || pe.ReturnValue != 2 || pe.Action != PowerOff {
		t.Fatalf("err = %v", err)
	}
}

func TestPowerMissingOutputIsError(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{powerRoute: "empty_body.xml"}}
	if err := newDevice(f, "").Power(PowerOn); err == nil {
		t.Fatal("empty body accepted as success")
	}
}

func TestPowerActionStrings(t *testing.T) {
	cases := []struct {
		a           PowerAction
		name        string
		destructive bool
	}{
		{PowerOn, "on", false},
		{PowerOff, "off", true},
		{PowerReset, "reset", true},
		{PowerCycle, "cycle", true},
		{BootToBIOS, "bios", true},
		{PowerShutdown, "shutdown", true},
	}
	for _, c := range cases {
		if c.a.String() != c.name || c.a.Destructive() != c.destructive {
			t.Errorf("%d: got %q/%v, want %q/%v", int(c.a), c.a.String(), c.a.Destructive(), c.name, c.destructive)
		}
	}
}

func biosRoutes(pull string) map[string]string {
	return map[string]string{
		"Enumerate|CIM_ServiceAvailableToElement": "enumerate.xml", "Pull|CIM_ServiceAvailableToElement": pull,
		"Get|AMT_BootSettingData": "boot_settingdata_get.xml", "Put|AMT_BootSettingData": "boot_settingdata_put.xml",
		"ChangeBootOrder|CIM_BootConfigSetting": "changebootorder.xml", "SetBootConfigRole|CIM_BootService": "setbootconfigrole.xml",
		"RequestPowerStateChange|CIM_PowerManagementService": "power_action_ok.xml",
	}
}

var biosSequence = []string{"Enumerate|CIM_ServiceAvailableToElement", "Pull|CIM_ServiceAvailableToElement",
	"Get|AMT_BootSettingData", "ChangeBootOrder|CIM_BootConfigSetting", "Put|AMT_BootSettingData",
	"SetBootConfigRole|CIM_BootService", "ChangeBootOrder|CIM_BootConfigSetting", "RequestPowerStateChange|CIM_PowerManagementService"}

func requestKeys(f *fakeWSMan) []string {
	var keys []string
	for _, r := range f.requests {
		keys = append(keys, routeKey(r))
	}
	return keys
}

func TestBootToBIOSWhenOnResets(t *testing.T) {
	f := &fakeWSMan{routes: biosRoutes("power_state_pull.xml")}
	if err := newDevice(f, "").Power(BootToBIOS); err != nil {
		t.Fatal(err)
	}
	if keys := requestKeys(f); !reflect.DeepEqual(keys, biosSequence) {
		t.Fatalf("sequence %v", keys)
	}
	put := f.requests[4]
	for _, s := range []string{"<h:BIOSSetup>true</h:BIOSSetup>", "<h:BIOSPause>false</h:BIOSPause>",
		"<h:InstanceID>Intel(r) AMT:BootSettingData 0</h:InstanceID>",
		"<h:ElementName>Intel(r) AMT Boot Configuration Settings</h:ElementName>", "<h:OwningEntity>Intel(r) AMT</h:OwningEntity>"} {
		if !strings.Contains(put, s) {
			t.Fatalf("put body lacks %s", s)
		}
	}
	if !strings.Contains(f.requests[7], "<h:PowerState>10</h:PowerState>") {
		t.Fatal("final action is not MasterBusReset")
	}
}

func TestBootToBIOSWhenOffPowersOn(t *testing.T) {
	f := &fakeWSMan{routes: biosRoutes("power_state_pull_off.xml")}
	if err := newDevice(f, "").Power(BootToBIOS); err != nil {
		t.Fatal(err)
	}
	if keys := requestKeys(f); !reflect.DeepEqual(keys, biosSequence) {
		t.Fatalf("sequence %v", keys)
	}
	if !strings.Contains(f.requests[7], "<h:PowerState>2</h:PowerState>") {
		t.Fatal("final action is not PowerOn")
	}
}

func TestBootToBIOSStopsOnFailedStep(t *testing.T) {
	routes := biosRoutes("power_state_pull.xml")
	delete(routes, "Put|AMT_BootSettingData")
	f := &fakeWSMan{routes: routes}
	err := newDevice(f, "").Power(BootToBIOS)
	if err == nil || len(f.requests) != 5 {
		t.Fatalf("err = %v, requests = %d", err, len(f.requests))
	}
	if !strings.Contains(err.Error(), "may still be set") {
		t.Fatalf("err lacks warning: %v", err)
	}
}

func TestBootToBIOSSetBootConfigRoleFailure(t *testing.T) {
	routes := biosRoutes("power_state_pull.xml")
	routes["SetBootConfigRole|CIM_BootService"] = "setbootconfigrole_fail.xml"
	err := newDevice(&fakeWSMan{routes: routes}, "").Power(BootToBIOS)
	var pe *PowerError
	if !errors.As(err, &pe) || pe.ReturnValue != 2 || !strings.Contains(err.Error(), "may still be set") {
		t.Fatalf("err = %v", err)
	}
}

func TestBootToBIOSEmptyBootSettingsAbortsBeforeChange(t *testing.T) {
	routes := biosRoutes("power_state_pull.xml")
	routes["Get|AMT_BootSettingData"] = "empty_body.xml"
	f := &fakeWSMan{routes: routes}
	err := newDevice(f, "").Power(BootToBIOS)
	if err == nil || !strings.Contains(err.Error(), "device returned no boot settings") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "may still be set") {
		t.Fatalf("nothing changed, err must not warn: %v", err)
	}
	keys := requestKeys(f)
	if keys[len(keys)-1] != "Get|AMT_BootSettingData" {
		t.Fatalf("requests must end at the Get: %v", keys)
	}
}

const capsRoute = "Get|CIM_PowerManagementCapabilities"

func TestPowerStates(t *testing.T) {
	cases := map[string][]power.PowerState{
		"power_caps_get.xml":          {5, 8, 2, 10, 11}, // as measured on AMT 16.1 without LMS
		"power_caps_get_graceful.xml": {2, 5, 8, 10, 11, 12},
		"empty_body.xml":              nil,
	}
	for file, want := range cases {
		f := &fakeWSMan{routes: map[string]string{capsRoute: file}}
		got, err := newDevice(f, "").PowerStates()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, %v; want %v", file, got, err, want)
		}
		if len(f.requests) != 1 || routeKey(f.requests[0]) != capsRoute {
			t.Errorf("%s: requests %v", file, requestKeys(f))
		}
	}
}

func TestPowerStatesError(t *testing.T) {
	f := &fakeWSMan{routes: map[string]string{}}
	if got, err := newDevice(f, "").PowerStates(); err == nil || got != nil {
		t.Fatalf("got %v, %v; want an error", got, err)
	}
}

// TestPowerStatesOverHTTP reads the capabilities through the digest test server.
func TestPowerStatesOverHTTP(t *testing.T) {
	routes := statusRoutes()
	routes[capsRoute] = "power_caps_get_graceful.xml"
	ts := newAMTServer(t, false, "admin", "secret-1", routes)
	d, err := Connect(context.Background(), Config{Host: "device.example.com", Username: "admin", Password: "secret-1", dialContext: dialTo(ts)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.PowerStates()
	if err != nil || !slices.Contains(got, power.PowerOffSoftGraceful) {
		t.Fatalf("%v, %v", got, err)
	}
}
