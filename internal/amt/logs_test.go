package amt

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// funcWSMan answers every Post with the result of fn.
type funcWSMan struct {
	fakeWSMan
	fn func(msg string) ([]byte, error)
}

func (f *funcWSMan) Post(msg string) ([]byte, error) {
	f.requests = append(f.requests, msg)
	return f.fn(msg)
}

// eventRecord builds the 21 bytes of an AMT_MessageLog record (layout: messagelog/log.go) as Base64.
func eventRecord(ts uint32, sensor, offset, severity, entity uint8, data [8]byte) string {
	b := make([]byte, 21)
	binary.LittleEndian.PutUint32(b, ts)
	b[5] = sensor
	b[7] = offset
	b[9] = severity
	b[11] = entity
	copy(b[13:], data[:])
	return base64.StdEncoding.EncodeToString(b)
}

func getRecordsXML(returnValue int, records ...string) []byte {
	var sb strings.Builder
	sb.WriteString(`<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_MessageLog"><a:Body><g:GetRecords_OUTPUT>`)
	fmt.Fprintf(&sb, "<g:IterationIdentifier>1</g:IterationIdentifier><g:NoMoreRecords>true</g:NoMoreRecords>")
	for _, r := range records {
		fmt.Fprintf(&sb, "<g:RecordArray>%s</g:RecordArray>", r)
	}
	fmt.Fprintf(&sb, "<g:ReturnValue>%d</g:ReturnValue></g:GetRecords_OUTPUT></a:Body></a:Envelope>", returnValue)
	return []byte(sb.String())
}

func eventDevice(resp []byte, err error) (*Device, *funcWSMan) {
	f := &funcWSMan{fn: func(string) ([]byte, error) { return resp, err }}
	return newDevice(f, ""), f
}

func TestEventLog(t *testing.T) {
	boot := [8]byte{0, 19}
	zero := base64.StdEncoding.EncodeToString(make([]byte, 21))
	short := base64.StdEncoding.EncodeToString(make([]byte, 5))
	resp := getRecordsXML(0,
		eventRecord(1767225600, 15, 2, 0, 34, boot),       // 2026-01-01 00:00:00 UTC, newest
		eventRecord(1767225000, 15, 0, 16, 35, [8]byte{}), // older
		zero, short, zero)
	d, f := eventDevice(resp, nil)
	got, err := d.EventLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (null records dropped): %+v", len(got), got)
	}
	e := got[0]
	if !e.Time.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || e.Time.Location() != time.UTC {
		t.Errorf("time %v", e.Time)
	}
	if e.Description != "Starting operating system boot process" || e.Source != "BIOS" {
		t.Errorf("description %q source %q", e.Description, e.Source)
	}
	if e.SensorType != 15 || e.EventOffset != 2 || e.EventData != [8]byte{0, 19} || e.Severity != SeverityInfo {
		t.Errorf("raw fields %+v", e)
	}
	if got[1].Source != "Intel(r) ME" || got[1].Severity != SeverityCritical || !got[1].Time.Before(e.Time) {
		t.Errorf("second %+v", got[1])
	}
	if len(f.requests) != 1 || !strings.Contains(f.requests[0], "<h:MaxReadRecords>390</h:MaxReadRecords>") {
		t.Errorf("requests %d", len(f.requests))
	}
}

func TestEventLogSeverity(t *testing.T) {
	tests := []struct {
		raw  uint8
		want Severity
		str  string
	}{
		{0, SeverityInfo, "info"}, {1, SeverityInfo, "info"}, {2, SeverityInfo, "info"}, {4, SeverityInfo, "info"},
		{8, SeverityWarning, "warning"}, {16, SeverityCritical, "critical"}, {32, SeverityCritical, "critical"},
		{99, SeverityInfo, "info"},
	}
	for _, tc := range tests {
		d, _ := eventDevice(getRecordsXML(0, eventRecord(1767225600, 15, 0, tc.raw, 34, [8]byte{0, 1})), nil)
		got, err := d.EventLog()
		if err != nil || len(got) != 1 {
			t.Fatalf("severity %d: %v %v", tc.raw, got, err)
		}
		if got[0].Severity != tc.want || got[0].Severity.String() != tc.str {
			t.Errorf("severity %d: got %v (%s), want %v", tc.raw, got[0].Severity, got[0].Severity, tc.want)
		}
	}
	if Severity(7).String() != "info" {
		t.Error("unknown Severity value must print as info")
	}
}

func TestEventLogReturnValues(t *testing.T) {
	d, _ := eventDevice(getRecordsXML(3), nil)
	got, err := d.EventLog()
	if err != nil || len(got) != 0 {
		t.Fatalf("ReturnValue 3: %v, %v", got, err)
	}
	d, _ = eventDevice(getRecordsXML(1), nil)
	got, err = d.EventLog()
	if err == nil || got != nil || !strings.Contains(err.Error(), "ReturnValue 1") {
		t.Fatalf("ReturnValue 1: %v, %v", got, err)
	}
}

func TestEventLogErrors(t *testing.T) {
	d, _ := eventDevice(nil, faultErr(t, "access_denied_fault.xml"))
	if _, err := d.EventLog(); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("got %v", err)
	}
	d, _ = eventDevice(nil, errors.New("connection reset"))
	if _, err := d.EventLog(); err == nil || !strings.Contains(err.Error(), "read event log") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("got %v", err)
	}
}
