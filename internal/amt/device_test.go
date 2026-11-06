package amt

import (
	"strings"
	"testing"

	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
)

func TestStatus(t *testing.T) {
	f := &fakeWSMan{routes: statusRoutes()}
	st, err := newDevice(f, "").Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.PowerState != models.PowerStateOn || st.AMTVersion != "16.1.25" || st.ControlMode != ControlModeAdmin {
		t.Fatalf("got %+v", st)
	}
	if len(f.requests) != 5 {
		t.Fatalf("%d requests", len(f.requests))
	}
}

func TestStatusClientControlMode(t *testing.T) {
	r := statusRoutes()
	r["Get|AMT_SetupAndConfigurationService"] = "setup_get_ccm.xml"
	st, err := newDevice(&fakeWSMan{routes: r}, "").Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.ControlMode != ControlModeClient {
		t.Fatalf("got %q", st.ControlMode)
	}
}

func TestStatusWithoutAMTIdentity(t *testing.T) {
	r := statusRoutes()
	r["Pull|CIM_SoftwareIdentity"] = "software_pull_noamt.xml"
	st, err := newDevice(&fakeWSMan{routes: r}, "").Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.AMTVersion != "" {
		t.Fatalf("got %q", st.AMTVersion)
	}
}

func TestStatusPostError(t *testing.T) {
	r := statusRoutes()
	delete(r, "Pull|CIM_ServiceAvailableToElement")
	f := &fakeWSMan{routes: r}
	_, err := newDevice(f, "").Status()
	if err == nil || !strings.Contains(err.Error(), "Pull|CIM_ServiceAvailableToElement") {
		t.Fatalf("err = %v", err)
	}
	if len(f.requests) != 2 {
		t.Fatalf("%d requests", len(f.requests))
	}
}

func TestFingerprint(t *testing.T) {
	if got := newDevice(&fakeWSMan{}, "ab").Fingerprint(); got != "ab" {
		t.Fatalf("got %q", got)
	}
}
