package amt

import (
	"errors"
	"strings"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
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
	if len(f.requests) != 5 || st.Denied != 0 {
		t.Fatalf("%d requests, Denied %b", len(f.requests), st.Denied)
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

// limitedSeq answers like the full status routes, except that the given routes fail with a fault.
func limitedSeq(t *testing.T, faults map[string]string) map[string][]seqResp {
	t.Helper()
	seq := map[string][]seqResp{}
	for key, file := range statusRoutes() {
		seq[key] = []seqResp{{file: file}}
	}
	for key, file := range faults {
		seq[key] = []seqResp{{err: faultErr(t, file)}}
	}
	return seq
}

func TestStatusLimitedAccount(t *testing.T) {
	d, f := hwDevice(limitedSeq(t, map[string]string{
		"Enumerate|CIM_ServiceAvailableToElement": "unreachable_fault.xml",
		"Enumerate|CIM_SoftwareIdentity":          "unreachable_fault.xml",
		"Get|AMT_SetupAndConfigurationService":    "access_denied_fault.xml",
	}))
	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	all := StatusPower | StatusVersion | StatusControlMode
	if st.Denied != all {
		t.Fatalf("Denied = %b, want %b", st.Denied, all)
	}
	if st.PowerState != 0 || st.AMTVersion != "" || st.ControlMode != "" {
		t.Fatalf("denied fields must stay zero: %+v", st)
	}
	if len(f.requests) != 3 {
		t.Fatalf("%d requests, want 3", len(f.requests))
	}
}

func TestStatusOnePartDenied(t *testing.T) {
	tests := []struct {
		name     string
		fault    map[string]string
		denied   StatusPart
		requests int
	}{
		{"power", map[string]string{"Enumerate|CIM_ServiceAvailableToElement": "unreachable_fault.xml"}, StatusPower, 4},
		{"version", map[string]string{"Enumerate|CIM_SoftwareIdentity": "unreachable_fault.xml"}, StatusVersion, 4},
		{"control mode", map[string]string{"Get|AMT_SetupAndConfigurationService": "access_denied_fault.xml"}, StatusControlMode, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, f := hwDevice(limitedSeq(t, tc.fault))
			st, err := d.Status()
			if err != nil {
				t.Fatal(err)
			}
			if st.Denied != tc.denied {
				t.Fatalf("Denied = %b, want %b", st.Denied, tc.denied)
			}
			if st.Allowed(tc.denied) {
				t.Fatal("Allowed must be false for the denied part")
			}
			if tc.denied != StatusPower && (st.PowerState != models.PowerStateOn || !st.Allowed(StatusPower)) {
				t.Fatalf("power part lost: %+v", st)
			}
			if tc.denied != StatusVersion && st.AMTVersion != "16.1.25" {
				t.Fatalf("version part lost: %+v", st)
			}
			if tc.denied != StatusControlMode && st.ControlMode != ControlModeAdmin {
				t.Fatalf("control mode part lost: %+v", st)
			}
			if len(f.requests) != tc.requests {
				t.Fatalf("%d requests, want %d", len(f.requests), tc.requests)
			}
		})
	}
}

func TestStatusOtherErrorsStillFail(t *testing.T) {
	tests := []struct {
		name string
		key  string
		err  error
	}{
		{"other fault on enumerate", "Enumerate|CIM_ServiceAvailableToElement", amterror.NewAMTError("a:InternalError", "x", "")},
		{"401 on enumerate", "Enumerate|CIM_ServiceAvailableToElement", errors.New("wsman.Client post received: 401 Unauthorized\n")},
		{"403 on enumerate", "Enumerate|CIM_ServiceAvailableToElement", errors.New("wsman.Client post received: 403 Forbidden\n")},
		{"500 AccessDenied body on enumerate", "Enumerate|CIM_ServiceAvailableToElement", errors.New("wsman.Client post received: 500 Internal Server Error\nAccessDenied")},
		{"403 on version", "Enumerate|CIM_SoftwareIdentity", errors.New("wsman.Client post received: 403 Forbidden\n")},
		{"403 on control mode", "Get|AMT_SetupAndConfigurationService", errors.New("wsman.Client post received: 403 Forbidden\n")},
		{"unreachable on power pull", "Pull|CIM_ServiceAvailableToElement", amterror.NewAMTError("b:DestinationUnreachable", "x", "")},
		{"access denied on power pull", "Pull|CIM_ServiceAvailableToElement", amterror.NewAMTError("e:AccessDenied", "x", "")},
		{"unreachable on version pull", "Pull|CIM_SoftwareIdentity", amterror.NewAMTError("b:DestinationUnreachable", "x", "")},
		{"access denied on version pull", "Pull|CIM_SoftwareIdentity", amterror.NewAMTError("e:AccessDenied", "x", "")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seq := limitedSeq(t, nil)
			seq[tc.key] = []seqResp{{err: tc.err}}
			d, _ := hwDevice(seq)
			if st, err := d.Status(); err == nil {
				t.Fatalf("Status succeeded: %+v", st)
			}
		})
	}
}

func TestStatusEmptyPowerListStillFails(t *testing.T) {
	seq := limitedSeq(t, nil)
	seq["Pull|CIM_ServiceAvailableToElement"] = []seqResp{{file: "software_pull_noamt.xml"}}
	d, _ := hwDevice(seq)
	if _, err := d.Status(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("err = %v, want ErrIncompleteResponse", err)
	}
}
