package amt

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var alarmNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func alarmSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Enumerate|IPS_AlarmClockOccurrence": {{file: "enumerate.xml"}},
		"Pull|IPS_AlarmClockOccurrence":      {{file: "alarm_pull.xml"}},
		"AddAlarm|AMT_AlarmClockService":     {{file: "addalarm_ok.xml"}},
		"Delete|IPS_AlarmClockOccurrence":    {{file: "alarm_delete_ok.xml"}},
	}
}

func TestGetAlarmsParsesAndSortsBySoonest(t *testing.T) {
	d, _ := cfgDevice(alarmSeq())
	got, err := d.GetAlarms()
	if err != nil {
		t.Fatal(err)
	}
	want := []Alarm{
		{Name: "Nightly backup", Start: time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC), Interval: 24 * time.Hour},
		{Name: "Maintenance window", Start: time.Date(2026, 11, 28, 5, 30, 0, 0, time.UTC), DeleteOnCompletion: true},
		{Name: "Patch day", Start: time.Date(2026, 12, 8, 21, 0, 0, 0, time.UTC), Interval: 28 * 24 * time.Hour},
	}
	if len(got) != len(want) {
		t.Fatalf("%d alarms: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alarm %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestGetAlarmsEmpty(t *testing.T) {
	seq := alarmSeq()
	seq["Pull|IPS_AlarmClockOccurrence"] = []cfgResp{{file: "alarm_pull_empty.xml"}}
	d, _ := cfgDevice(seq)
	got, err := d.GetAlarms()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestGetAlarmsAccessDenied(t *testing.T) {
	seq := alarmSeq()
	seq["Enumerate|IPS_AlarmClockOccurrence"] = []cfgResp{{file: "access_denied_fault.xml"}}
	d, _ := cfgDevice(seq)
	if _, err := d.GetAlarms(); err == nil {
		t.Fatal("a fault was read as an empty list")
	}
}

func TestParseInterval(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"P0DT0H0M":    0,
		"P1DT0H0M0S":  24 * time.Hour,
		"P7DT0H0M":    7 * 24 * time.Hour,
		"P0DT2H30M":   150 * time.Minute,
		"PT15M":       15 * time.Minute,
		"P28DT0H0M":   28 * 24 * time.Hour,
		"":            0,
		"garbage":     0,
		"P0DT0H0M30S": 30 * time.Second,
	} {
		if got := parseInterval(in); got != want {
			t.Errorf("parseInterval(%q) = %v, want %v", in, got, want)
		}
	}
}

func goodSpec() AlarmSpec {
	return AlarmSpec{Name: "Weekly job", Start: alarmNow.Add(14 * time.Hour), Interval: 24 * time.Hour}
}

func TestAlarmSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*AlarmSpec)
		want error // nil: valid; ErrInvalidValue or ErrAlarmPast
	}{
		{"valid", func(*AlarmSpec) {}, nil},
		{"valid once, deleted afterwards", func(s *AlarmSpec) { s.Interval, s.DeleteOnCompletion = 0, true }, nil},
		{"name of 32 characters", func(s *AlarmSpec) { s.Name = strings.Repeat("ä", 32) }, nil},
		{"empty name", func(s *AlarmSpec) { s.Name = "  " }, ErrInvalidValue},
		{"name of 33 characters", func(s *AlarmSpec) { s.Name = strings.Repeat("a", 33) }, ErrInvalidValue},
		{"control character", func(s *AlarmSpec) { s.Name = "a\nb" }, ErrInvalidValue},
		{"start now", func(s *AlarmSpec) { s.Start = alarmNow }, ErrAlarmPast},
		{"start in the past", func(s *AlarmSpec) { s.Start = alarmNow.Add(-time.Hour) }, ErrAlarmPast},
		{"negative interval", func(s *AlarmSpec) { s.Interval = -time.Minute }, ErrInvalidValue},
		{"interval with seconds", func(s *AlarmSpec) { s.Interval = 90 * time.Second }, ErrInvalidValue},
		{"interval over 800 days", func(s *AlarmSpec) { s.Interval = 801 * 24 * time.Hour }, ErrInvalidValue},
		{"interval of 800 days", func(s *AlarmSpec) { s.Interval = 800 * 24 * time.Hour }, nil},
		{"delete after a repeating alarm", func(s *AlarmSpec) { s.DeleteOnCompletion = true }, ErrInvalidValue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := goodSpec()
			c.mod(&s)
			err := s.Validate(alarmNow)
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestAddAlarmSendsUTCTimeIntervalAndEscapedName(t *testing.T) {
	fixedNow(t, alarmNow)
	d, f := cfgDevice(alarmSeq())
	s := AlarmSpec{Name: `Back<up> & "co"`, Start: time.Date(2026, 11, 24, 2, 0, 0, 0, time.FixedZone("CET", 3600)), Interval: 28*24*time.Hour + 90*time.Minute}
	if err := d.AddAlarm(s); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "AddAlarm|AMT_AlarmClockService")
	assertContains(t, body,
		"<s:InstanceID>Back&lt;up&gt; &amp; &#34;co&#34;</s:InstanceID>",
		"<s:ElementName>Back&lt;up&gt; &amp; &#34;co&#34;</s:ElementName>",
		"2026-11-24T01:00:00", // 02:00 local time is 01:00 UTC
		"P28DT1H30M",
		"<s:DeleteOnCompletion>false</s:DeleteOnCompletion>")
	assertLacks(t, body, "<up>", "Back<up")
}

func TestAddAlarmDeleteOnCompletion(t *testing.T) {
	fixedNow(t, alarmNow)
	d, f := cfgDevice(alarmSeq())
	s := goodSpec()
	s.Interval, s.DeleteOnCompletion = 0, true
	if err := d.AddAlarm(s); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "AddAlarm|AMT_AlarmClockService"), "<s:DeleteOnCompletion>true</s:DeleteOnCompletion>", "P0DT0H0M")
}

func TestAddAlarmRefusals(t *testing.T) {
	fixedNow(t, alarmNow)
	t.Run("past time sends nothing", func(t *testing.T) {
		d, f := cfgDevice(alarmSeq())
		s := goodSpec()
		s.Start = alarmNow.Add(-time.Minute)
		if err := d.AddAlarm(s); !errors.Is(err, ErrAlarmPast) || len(f.requests) != 0 {
			t.Fatalf("err %v, %d requests", err, len(f.requests))
		}
	})
	t.Run("same name in another case", func(t *testing.T) {
		d, f := cfgDevice(alarmSeq())
		s := goodSpec()
		s.Name = "PATCH DAY"
		if err := d.AddAlarm(s); !errors.Is(err, ErrAlarmExists) || len(f.requestsFor("AddAlarm|AMT_AlarmClockService")) != 0 {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("a sixth alarm", func(t *testing.T) {
		seq := alarmSeq()
		seq["Pull|IPS_AlarmClockOccurrence"] = []cfgResp{{file: "alarm_pull_five.xml"}}
		d, f := cfgDevice(seq)
		if err := d.AddAlarm(goodSpec()); !errors.Is(err, ErrAlarmsFull) || len(f.requestsFor("AddAlarm|AMT_AlarmClockService")) != 0 {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("the device refuses", func(t *testing.T) {
		seq := alarmSeq()
		seq["AddAlarm|AMT_AlarmClockService"] = []cfgResp{{file: "addalarm_fail.xml"}}
		d, _ := cfgDevice(seq)
		var me *MethodError
		if err := d.AddAlarm(goodSpec()); !errors.As(err, &me) || me.ReturnValue != 2 {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("an answer without output", func(t *testing.T) {
		seq := alarmSeq()
		seq["AddAlarm|AMT_AlarmClockService"] = []cfgResp{{file: "empty_body.xml"}}
		d, _ := cfgDevice(seq)
		if err := d.AddAlarm(goodSpec()); !errors.Is(err, ErrIncompleteResponse) {
			t.Fatalf("err %v", err)
		}
	})
}

func TestDeleteAlarmSelectsInstanceAndEscapes(t *testing.T) {
	d, f := cfgDevice(alarmSeq())
	if err := d.DeleteAlarm("Patch day"); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, f, "Delete|IPS_AlarmClockOccurrence"), `<w:Selector Name="InstanceID">Patch day</w:Selector>`)

	d, f = cfgDevice(alarmSeq())
	if err := d.DeleteAlarm("A&B<1>"); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Delete|IPS_AlarmClockOccurrence")
	assertContains(t, body, "A&amp;B&lt;1&gt;")
	assertLacks(t, body, "A&B<1>")

	if err := d.DeleteAlarm(" "); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("empty name: %v", err)
	}
}

func TestAlarmConfirmationLevel(t *testing.T) {
	if r := RiskOf(OpAlarmDelete); r.Level != LevelSimple || r.Severity != SeverityHigh {
		t.Fatalf("OpAlarmDelete = %+v", r)
	}
}
