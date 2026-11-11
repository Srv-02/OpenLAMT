package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/session"
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

func TestFormatLogTimeShort(t *testing.T) {
	at := time.Date(2026, 11, 9, 22, 47, 15, 0, time.UTC)
	if got := formatLogTimeShort(at, time.FixedZone("CET", 3600)); got != "Nov 09 23:47" {
		t.Errorf("got %q", got)
	}
	if got := formatLogTimeShort(time.Time{}, time.UTC); got != "" {
		t.Errorf("zero time: %q", got)
	}
}

// In the 400 px sidebar the time is short and the last column (Description, Event) is the
// widest; the full time stays in the row for the window and the filter.
func TestLogSidebarGivesTheLastColumnTheMostRoom(t *testing.T) {
	englishUI(t)
	events := append(exampleEvents(), amt.EventLogEntry{Time: time.Date(2026, 5, 30, 20, 0, 0, 0, time.Local),
		Severity: amt.SeverityWarning, Source: "Watchdog", Description: "Agent heartbeat timeout"})
	h := newManageHarness(t, &session.Fake{FP: fp, HW: exampleHW(), Events: events, Audit: exampleAudit()})
	h.m.parent.SetContent(h.m.Sidebar())
	h.m.parent.Resize(fyne.NewSize(manageSidebarWidth, 800))
	h.wait(t) // hardware
	for _, tab := range []int{1, 2} {
		h.m.side.tabs.Tap(tab)
		h.wait(t)
		var lines []*logLine
		walk(h.m.side.view.panes[tab].impl.(*logPane).list, func(o fyne.CanvasObject) {
			if l, ok := o.(*logLine); ok && l.row != nil {
				lines = append(lines, l)
			}
		})
		if len(lines) == 0 {
			t.Fatalf("tab %d: no log line", tab)
		}
		line := lines[0]
		r := test.WidgetRenderer(line).(*logLineRenderer)
		want := formatLogTimeShort(exampleEvents()[0].Time, time.Local)
		if tab == 2 {
			want = formatLogTimeShort(exampleAudit()[0].Time, time.Local)
		}
		if r.texts[0].Text != want || !strings.Contains(line.Text(0), ", 2026 ") {
			t.Errorf("tab %d: time %q (row %q), want %q", tab, r.texts[0].Text, line.Text(0), want)
		}
		last := line.Size().Width - line.widths[0] - line.widths[1] - line.widths[2]
		for i, w := range line.widths {
			if last <= w {
				t.Errorf("tab %d: last column %v wide, column %d %v", tab, last, i, w)
			}
		}
		// the short time, the severity "Warning" with its dot and the sources fit uncut
		for _, l := range lines {
			lr := test.WidgetRenderer(l).(*logLineRenderer)
			for i := 0; i < 3; i++ {
				if strings.HasSuffix(lr.texts[i].Text, "…") {
					t.Errorf("tab %d: column %d cut: %q", tab, i, lr.texts[i].Text)
				}
			}
		}
	}
}
