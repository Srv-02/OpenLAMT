package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

func TestFormatLogTime(t *testing.T) {
	at := time.Date(2026, 11, 9, 22, 47, 15, 0, time.UTC)
	for _, c := range []struct {
		t    time.Time
		loc  *time.Location
		want string
	}{
		{at, time.UTC, "Nov 09, 2026 22:47:15"},
		{at, time.FixedZone("CET", 3600), "Nov 09, 2026 23:47:15"},
		{time.Time{}, time.UTC, ""},
	} {
		if got := formatLogTime(c.t, c.loc); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestEventRows(t *testing.T) {
	englishUI(t)
	rows := eventRows(exampleEvents(), time.UTC)
	var got [][4]string
	for _, r := range rows {
		got = append(got, r.cells)
	}
	want := [][4]string{
		{"Nov 11, 2026 16:41:52", "Info", "BIOS", "Starting operating system boot process"},
		{"Nov 10, 2026 03:12:08", "Warning", "Watchdog", "Agent heartbeat timeout"},
		{"Nov 09, 2026 22:47:15", "Critical", "Power", "Power supply AC lost"},
		{"Nov 05, 2026 14:01:57", "Info", "BIOS", "Starting operating system boot process"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if !rows[1].hasSev || rows[1].sev != amt.SeverityWarning || rows[2].sev != amt.SeverityCritical {
		t.Fatalf("severities %+v", rows)
	}
}

func TestAuditRows(t *testing.T) {
	englishUI(t)
	entries := []amt.AuditLogEntry{
		{InitiatorType: 0, Initiator: "user1", Address: "192.0.2.10", Event: "KVM Session Started"},
		{InitiatorType: 1, Initiator: "S-1-5-21-1-2-3-1001", Event: "AMT Provisioning Started", Details: "example detail"},
		{InitiatorType: 2, Event: "Local event"},
		{InitiatorType: 3, Event: "KVM event"},
		{InitiatorType: 9},
	}
	var got [][4]string
	for _, r := range auditRows(entries, time.UTC) {
		if r.hasSev {
			t.Fatal("audit rows have no severity")
		}
		got = append(got, r.cells)
	}
	want := [][4]string{
		{"", "user1", "192.0.2.10", "KVM Session Started"},
		{"", "S-1-5-21-1-2-3-1001", "", "AMT Provisioning Started · example detail"},
		{"", "Local", "", "Local event"},
		{"", "KVM default port", "", "KVM event"},
		{"", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestFilterLogRows(t *testing.T) {
	englishUI(t)
	rows := eventRows(exampleEvents(), time.UTC)
	for _, c := range []struct {
		level int
		text  string
		want  []int
	}{
		{logAll, "", []int{0, 1, 2, 3}},
		{int(amt.SeverityWarning), "", []int{1}},
		{int(amt.SeverityCritical), "", []int{2}},
		{int(amt.SeverityInfo), "", []int{0, 3}},
		{logAll, "  Boot ", []int{0, 3}},
		{logAll, "watchdog", []int{1}},
		{logAll, "critical", []int{2}}, // the severity caption is searched too
		{logAll, "nov 10", []int{1}},
		{int(amt.SeverityWarning), "boot", []int{}},
	} {
		got := filterLogRows(rows, c.level, c.text, nil)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("level %d text %q: got %v, want %v", c.level, c.text, got, c.want)
		}
	}
	// the audit log has no severity: a level filter matches nothing
	if got := filterLogRows(auditRows(exampleAudit(), time.UTC), int(amt.SeverityInfo), "", nil); len(got) != 0 {
		t.Fatalf("audit with level: %v", got)
	}
}
