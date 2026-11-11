package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// exampleHW is a made-up hardware inventory (no real device data).
func exampleHW() amt.HardwareInfo {
	return amt.HardwareInfo{
		System: amt.SystemInfo{Manufacturer: "Example Corp", Model: "EX-1000", Version: "Example Mini 1", SerialNumber: "SN-TEST-0001"},
		BIOS:   amt.BIOSInfo{Version: "EXB10001", ReleaseDate: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)},
		CPUs:   []amt.CPUInfo{{Socket: "CPU 0", Name: "Example CPU 9000", MaxClockMHz: 4600}},
		Memory: []amt.MemoryModule{
			{Bank: "Bank 0", CapacityBytes: 16 << 30, SpeedMTs: 5600, Type: "DDR5"},
			{Bank: "Bank 1", CapacityBytes: 16 << 30, SpeedMTs: 5600, Type: "DDR5"},
		},
		Drives: []amt.Drive{{ID: "MEDIA DEV 0", CapacityBytes: 1000204886016}, {ID: "MEDIA DEV 1", CapacityBytes: 2000398934016}},
		AMT:    amt.AMTInfo{Version: "16.1.25", Build: "2052"},
	}
}

func TestHardwareSectionsFull(t *testing.T) {
	englishUI(t)
	got := hardwareSections(exampleHW(), amt.ControlModeAdmin)
	want := []hwSection{
		{"System", []hwRow{{"Manufacturer", "Example Corp"}, {"Model", "Example Mini 1"}, {"Serial number", "SN-TEST-0001"}, {"BIOS", "EXB10001 · Jan 5, 2026"}}},
		{"Processor and memory", []hwRow{{"CPU", "Example CPU 9000 · 4.6 GHz"}, {"RAM Bank 0", "16 GB DDR5 · 5600 MT/s"}, {"RAM Bank 1", "16 GB DDR5 · 5600 MT/s"}}},
		{"Drives", []hwRow{{"MEDIA DEV 0", "1.0 TB"}, {"MEDIA DEV 1", "2.0 TB"}}},
		{"AMT", []hwRow{{"Version", "16.1.25 · Build 2052"}, {"Mode", "ACM"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestHardwareSectionsLeaveOutEmptyValues(t *testing.T) {
	englishUI(t)
	hw := amt.HardwareInfo{
		System: amt.SystemInfo{Model: "EX-1000"}, // no Version: Model is shown
		CPUs:   []amt.CPUInfo{{Name: "Example CPU 9000"}, {MaxClockMHz: 3200}},
		Memory: []amt.MemoryModule{{CapacityBytes: 8 << 30}},
		AMT:    amt.AMTInfo{Build: "2052"},
	}
	got := hardwareSections(hw, amt.ControlModeUnknown)
	want := []hwSection{
		{"System", []hwRow{{"Model", "EX-1000"}}},
		{"Processor and memory", []hwRow{{"CPU 0", "Example CPU 9000"}, {"CPU 1", "3.2 GHz"}, {"RAM", "8 GB"}}},
		{"AMT", []hwRow{{"Version", "Build 2052"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got := hardwareSections(amt.HardwareInfo{}, ""); len(got) != 0 {
		t.Fatalf("empty inventory: %+v", got)
	}
	if got := hardwareSections(amt.HardwareInfo{}, amt.ControlModeClient); !reflect.DeepEqual(got, []hwSection{{"AMT", []hwRow{{"Mode", "CCM"}}}}) {
		t.Fatalf("mode only: %+v", got)
	}
}

func TestHardwareValueFormats(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{formatGHz(4600), "4.6 GHz"},
		{formatGHz(2460), "2.5 GHz"},
		{formatGHz(0), ""},
		{formatMemory(16 << 30), "16 GB"},
		{formatMemory(3 << 29), "1.5 GB"},
		{formatMemory(512 << 20), "512 MB"},
		{formatMemory(0), ""},
		{formatDrive(1000204886016), "1.0 TB"},
		{formatDrive(2000398934016), "2.0 TB"},
		{formatDrive(512110190592), "512 GB"},
		{formatDrive(64000000), "64 MB"},
		{formatDrive(0), ""},
		{majorMinor("16.1.25"), "16.1"},
		{majorMinor("16"), "16"},
		{majorMinor(""), ""},
		{formatBIOS(amt.BIOSInfo{Version: "1.2"}), "1.2"},
		{formatBIOS(amt.BIOSInfo{ReleaseDate: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)}), "Sep 18, 2026"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestReadAgo(t *testing.T) {
	englishUI(t)
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "Read 0 s ago"},
		{12 * time.Second, "Read 12 s ago"},
		{59*time.Second + 900*time.Millisecond, "Read 59 s ago"},
		{60 * time.Second, "Read 1 min ago"},
		{59 * time.Minute, "Read 59 min ago"},
		{2 * time.Hour, "Read 2 h ago"},
	} {
		if got := readAgo(c.d); got != c.want {
			t.Errorf("%v: got %q, want %q", c.d, got, c.want)
		}
	}
}
