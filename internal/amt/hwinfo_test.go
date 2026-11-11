package amt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

// seqResp is one canned answer of seqWSMan: the content of a testdata file, or an error.
type seqResp struct {
	file string
	err  error
}

// seqWSMan is a fake WS-Man client with a sequence of answers per route; the last one repeats.
type seqWSMan struct {
	fakeWSMan
	seq   map[string][]seqResp
	calls map[string]int
}

func (f *seqWSMan) Post(msg string) ([]byte, error) {
	f.requests = append(f.requests, msg)
	key := routeKey(msg)
	list, ok := f.seq[key]
	if !ok {
		return nil, fmt.Errorf("fake: no route for %q", key)
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	i := f.calls[key]
	f.calls[key]++
	if i >= len(list) {
		i = len(list) - 1
	}
	if list[i].err != nil {
		return nil, list[i].err
	}
	return os.ReadFile(filepath.Join("testdata", list[i].file))
}

// faultErr is what the WS-Man client returns for an HTTP 400 with the fixture as body.
func faultErr(t *testing.T, file string) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return amterror.DecodeAMTErrorString(string(b))
}

func hwSeq() map[string][]seqResp {
	pulls := map[string][]seqResp{
		"CIM_ComputerSystemPackage": {{file: "hw_computer_pull.xml"}},
		"CIM_Chassis":               {{file: "hw_chassis_pull.xml"}},
		"CIM_Card":                  {{file: "hw_card_pull.xml"}},
		"CIM_BIOSElement":           {{file: "hw_bios_pull.xml"}},
		"CIM_Processor":             {{file: "hw_processor_pull.xml"}},
		"CIM_Chip":                  {{file: "hw_chip_pull_1.xml"}, {file: "hw_chip_pull_2.xml"}},
		"CIM_PhysicalMemory":        {{file: "hw_memory_pull.xml"}},
		"CIM_MediaAccessDevice":     {{file: "hw_media_pull.xml"}},
		"CIM_SoftwareIdentity":      {{file: "hw_software_pull.xml"}},
	}
	seq := map[string][]seqResp{}
	for class, list := range pulls {
		seq["Enumerate|"+class] = []seqResp{{file: "enumerate.xml"}}
		seq["Pull|"+class] = list
	}
	return seq
}

func hwDevice(seq map[string][]seqResp) (*Device, *seqWSMan) {
	f := &seqWSMan{seq: seq}
	return newDevice(f, ""), f
}

func TestHardwareInfoFull(t *testing.T) {
	d, f := hwDevice(hwSeq())
	got, err := d.HardwareInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := HardwareInfo{
		System: SystemInfo{
			Manufacturer: "Example Corp", Model: "EX-1000", Version: "Example Mini 1",
			SerialNumber: "SN-TEST-0001", ChassisType: "MiniPC",
			UUID: "33221100-5544-7766-8899-AABBCCDDEEFF",
		},
		Board: BoardInfo{Manufacturer: "Example Corp", Model: "EX-BOARD", Version: "A1"},
		BIOS: BIOSInfo{
			Manufacturer: "Example Corp", Version: "EXB10001",
			ReleaseDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		CPUs: []CPUInfo{{Socket: "CPU 0", Name: "Example CPU 9000", Manufacturer: "Example Corp", MaxClockMHz: 4800, CurrentClockMHz: 2400}},
		Memory: []MemoryModule{
			{Bank: "BANK 0", Manufacturer: "Example Memory", PartNumber: "EXM-16G-5600", SerialNumber: "MEM-0001", CapacityBytes: 16 << 30, SpeedMTs: 5600, Type: "DDR5"},
			{Bank: "BANK 1", Manufacturer: "Example Memory", PartNumber: "EXM-16G-5600", SerialNumber: "MEM-0002", CapacityBytes: 16 << 30, SpeedMTs: 6400, Type: ""},
		},
		Drives: []Drive{
			{ID: "MEDIA DEV 0", CapacityBytes: 1000204886000},
			{ID: "MEDIA DEV 2", CapacityBytes: 250059350000},
		},
		AMT: AMTInfo{Version: "16.1.25", Build: "9999"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	// Order of the classes, and the chip enumeration needs both pulls.
	var order []string
	for _, r := range f.requests {
		if k := routeKey(r); strings.HasPrefix(k, "Enumerate|") {
			order = append(order, strings.TrimPrefix(k, "Enumerate|"))
		}
	}
	wantOrder := []string{"CIM_ComputerSystemPackage", "CIM_Chassis", "CIM_Card", "CIM_BIOSElement", "CIM_Processor", "CIM_Chip", "CIM_PhysicalMemory", "CIM_MediaAccessDevice", "CIM_SoftwareIdentity"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order %v", order)
	}
	if f.calls["Pull|CIM_Chip"] != 2 {
		t.Fatalf("chip pulls = %d, want 2", f.calls["Pull|CIM_Chip"])
	}
}

func TestHardwareInfoClassMissing(t *testing.T) {
	seq := hwSeq()
	seq["Enumerate|CIM_MediaAccessDevice"] = []seqResp{{err: faultErr(t, "unreachable_fault.xml")}}
	d, _ := hwDevice(seq)
	got, err := d.HardwareInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Drives) != 0 || got.System.Model != "EX-1000" || got.AMT.Version != "16.1.25" {
		t.Fatalf("got %+v", got)
	}
}

func TestHardwareInfoAccessDenied(t *testing.T) {
	seq := hwSeq()
	seq["Enumerate|CIM_Chassis"] = []seqResp{{err: faultErr(t, "access_denied_fault.xml")}}
	d, f := hwDevice(seq)
	_, err := d.HardwareInfo()
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("got %v", err)
	}
	if f.calls["Enumerate|CIM_Card"] != 0 {
		t.Fatal("must stop at the first denied class")
	}
}

func TestHardwareInfoTransportError(t *testing.T) {
	seq := hwSeq()
	seq["Pull|CIM_Processor"] = []seqResp{{err: errors.New("connection reset")}}
	d, _ := hwDevice(seq)
	_, err := d.HardwareInfo()
	if err == nil || !strings.Contains(err.Error(), "CIM_Processor") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("got %v", err)
	}
}

func TestHardwareInfoUnauthorizedIsAbort(t *testing.T) {
	seq := hwSeq()
	seq["Enumerate|CIM_Card"] = []seqResp{{err: errors.New("wsman.Client post received: 401 Unauthorized\n")}}
	d, _ := hwDevice(seq)
	_, err := d.HardwareInfo()
	if err == nil || errors.Is(err, ErrAccessDenied) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %v", err)
	}
}

func TestFormatPlatformGUID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"00112233445566778899AABBCCDDEEFF", "33221100-5544-7766-8899-AABBCCDDEEFF"},
		{"00112233445566778899aabbccddeeff", "33221100-5544-7766-8899-AABBCCDDEEFF"},
		{"00112233", ""},
		{"00112233445566778899AABBCCDDEEFF00", ""},
		{"0011223344556677889GAABBCCDDEEFF", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := formatPlatformGUID(tc.in); got != tc.want {
			t.Errorf("formatPlatformGUID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
