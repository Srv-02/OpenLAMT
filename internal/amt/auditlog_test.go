package amt

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/auditlog"
)

// auditRecord builds one AMT_AuditLog record in device format (big-endian fields, see
// common.ReadShort/ReadInt and auditlog.getInitiatorInfo): app, event, initiator type, initiator
// (digest name or Kerberos SID bytes), time, MCLocationType 0, address, extended data.
func auditRecord(app, event int, initType uint8, initiator []byte, ts uint32, addr string, ex []byte) []byte {
	b := []byte{byte(app >> 8), byte(app), byte(event >> 8), byte(event), initType}
	switch initType {
	case 0:
		b = append(b, byte(len(initiator)))
		b = append(b, initiator...)
	case 1:
		b = append(b, 0, 0, 0, 0, byte(len(initiator)))
		b = append(b, initiator...)
	}
	b = binary.BigEndian.AppendUint32(b, ts)
	b = append(b, 0, byte(len(addr)))
	b = append(b, addr...)
	b = append(b, byte(len(ex)))
	return append(b, ex...)
}

// exampleSID is the binary form of the invented SID S-1-5-21-1000-2000-3000.
func exampleSID() []byte {
	b := []byte{1, 4, 0, 0, 0, 0, 0, 5}
	for _, s := range []uint32{21, 1000, 2000, 3000} {
		b = binary.LittleEndian.AppendUint32(b, s)
	}
	return b
}

func auditXML(returnValue, total int, recs ...[]byte) []byte {
	var sb strings.Builder
	sb.WriteString(`<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuditLog"><a:Body><g:ReadRecords_OUTPUT>`)
	fmt.Fprintf(&sb, "<g:TotalRecordCount>%d</g:TotalRecordCount><g:RecordsReturned>%d</g:RecordsReturned>", total, len(recs))
	for _, r := range recs {
		fmt.Fprintf(&sb, "<g:EventRecords>%s</g:EventRecords>", base64.StdEncoding.EncodeToString(r))
	}
	fmt.Fprintf(&sb, "<g:ReturnValue>%d</g:ReturnValue></g:ReadRecords_OUTPUT></a:Body></a:Envelope>", returnValue)
	return []byte(sb.String())
}

var startIndexRe = regexp.MustCompile(`<h:StartIndex>(\d+)</h:StartIndex>`)

func startIndex(msg string) int {
	m := startIndexRe.FindStringSubmatch(msg)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func auditDevice(fn func(msg string) ([]byte, error)) (*Device, *funcWSMan) {
	f := &funcWSMan{fn: fn}
	return newDevice(f, ""), f
}

const auditBase = 1767225600 // 2026-01-01 00:00:00 UTC

func TestAuditLogPages(t *testing.T) {
	const total = 250
	all := make([][]byte, total)
	for i := range all {
		all[i] = auditRecord(16, 0, 0, []byte("user1"), uint32(auditBase+60*(i+1)), "192.0.2.10", nil)
	}
	d, f := auditDevice(func(msg string) ([]byte, error) {
		start := startIndex(msg)
		if start < 1 || start > total {
			return auditXML(35, total), nil
		}
		end := min(start-1+120, total)
		return auditXML(0, total, all[start-1:end]...), nil
	})
	got, err := d.AuditLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("got %d entries, want %d", len(got), total)
	}
	if want := time.Unix(auditBase+60*total, 0).UTC(); !got[0].Time.Equal(want) || got[0].Time.Location() != time.UTC {
		t.Errorf("first entry time %v, want newest %v", got[0].Time, want)
	}
	if want := time.Unix(auditBase+60, 0).UTC(); !got[total-1].Time.Equal(want) {
		t.Errorf("last entry time %v, want oldest %v", got[total-1].Time, want)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Time.Before(got[i-1].Time) {
			t.Fatalf("entry %d not older than %d", i, i-1)
		}
	}
	var starts []int
	for _, r := range f.requests {
		starts = append(starts, startIndex(r))
	}
	if !reflect.DeepEqual(starts, []int{1, 121, 241}) {
		t.Errorf("StartIndex values %v", starts)
	}
}

func TestAuditLogInitiators(t *testing.T) {
	setup := []byte(nil)
	d, _ := auditDevice(func(string) ([]byte, error) {
		return auditXML(0, 5,
			auditRecord(16, 0, 0, []byte("user1"), auditBase, "192.0.2.10", setup),
			auditRecord(16, 0, 2, nil, auditBase+1, "0000:0000:0000:0000:0000:0000:0000:0001", setup),
			auditRecord(16, 0, 1, exampleSID(), auditBase+2, "", setup),
			auditRecord(29, 0, 3, nil, auditBase+3, "192.0.2.11", nil),
			auditRecord(16, 1, 2, nil, auditBase+4, "", []byte{0x01}),
		), nil
	})
	got, err := d.AuditLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d entries", len(got))
	}
	// newest first: got[4] is the first record on the device
	first := got[4]
	want := AuditLogEntry{
		Time: time.Unix(auditBase, 0).UTC(), AppID: 16, EventID: 0,
		App: "Security Admin Events", Event: "AMT Provisioning Started",
		InitiatorType: 0, Initiator: "user1", Address: "192.0.2.10",
		Details: "Intel AMT transitioned to setup mode.",
	}
	if first != want {
		t.Errorf("digest entry\n got %+v\nwant %+v", first, want)
	}
	if got[3].InitiatorType != 2 || got[3].Initiator != "" || got[3].Address != "::1" {
		t.Errorf("local entry %+v", got[3])
	}
	if got[2].InitiatorType != 1 || got[2].Initiator != "S-1-5-21-1000-2000-3000" {
		t.Errorf("kerberos entry %+v", got[2])
	}
	if got[1].InitiatorType != 3 || got[1].AppID != 29 || got[1].Event != "KVM Opt-In Enabled" || got[1].Address != "192.0.2.11" {
		t.Errorf("kvm entry %+v", got[1])
	}
	if got[0].Details == "" || got[0].EventID != 1 {
		t.Errorf("provisioning completed entry %+v", got[0])
	}
}

func TestAuditLogEmptyAndErrors(t *testing.T) {
	d, _ := auditDevice(func(string) ([]byte, error) { return auditXML(35, 0), nil })
	got, err := d.AuditLog()
	if err != nil || len(got) != 0 {
		t.Fatalf("invalid index at start 1: %v, %v", got, err)
	}
	d, _ = auditDevice(func(string) ([]byte, error) { return auditXML(1, 0), nil })
	if got, err = d.AuditLog(); err == nil || got != nil || !strings.Contains(err.Error(), "ReturnValue 1") {
		t.Fatalf("ReturnValue 1: %v, %v", got, err)
	}
	d, _ = auditDevice(func(string) ([]byte, error) { return nil, faultErr(t, "access_denied_fault.xml") })
	if _, err = d.AuditLog(); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("access denied: %v", err)
	}
	d, _ = auditDevice(func(string) ([]byte, error) { return nil, errors.New("connection reset") })
	if _, err = d.AuditLog(); err == nil || !strings.Contains(err.Error(), "read audit log") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("transport error: %v", err)
	}
	d, _ = auditDevice(func(string) ([]byte, error) { return nil, nil })
	if _, err = d.AuditLog(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("empty response: %v", err)
	}
}

func TestAuditLogNeverEnding(t *testing.T) {
	rec := auditRecord(16, 0, 2, nil, auditBase, "", nil)
	d, f := auditDevice(func(string) ([]byte, error) { return auditXML(0, 1000000, rec), nil })
	_, err := d.AuditLog()
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("got %v", err)
	}
	if len(f.requests) != 64 {
		t.Fatalf("requests = %d, want 64", len(f.requests))
	}
}

func TestAuditLogTruncatedRecord(t *testing.T) {
	// Digest name length 50, but only 3 bytes follow.
	broken := []byte{0, 16, 0, 0, 0, 50, 'a', 'b', 'c'}
	d, _ := auditDevice(func(string) ([]byte, error) { return auditXML(0, 1, broken), nil })
	got, err := d.AuditLog()
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	want := AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started"}
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
}

func TestAuditLogLibraryDecoderPanics(t *testing.T) {
	// AppID 16 / EventID 5 with ex data {2} indexes {"Intel AMT", "MEBx"}[2] in the library decoder.
	ex := []byte{2}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("library decoder no longer panics for app 16, event 5, ex {2}: pick another combination")
			}
		}()
		auditlog.GetAuditLogExtendedDataString(16, 5, string(ex))
	}()

	d, _ := auditDevice(func(string) ([]byte, error) {
		return auditXML(0, 3,
			auditRecord(16, 0, 0, []byte("user1"), auditBase, "192.0.2.10", nil),
			auditRecord(16, 5, 0, []byte("user1"), auditBase+1, "192.0.2.10", ex),
			auditRecord(16, 0, 2, nil, auditBase+2, "", nil),
		), nil
	})
	got, err := d.AuditLog()
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d entries, %v", len(got), err)
	}
	bad := got[1]
	if bad.Details != "" || bad.AppID != 16 || bad.EventID != 5 || bad.Initiator != "user1" || bad.Address != "192.0.2.10" ||
		bad.Event != "ACL Access with Invalid Credentials" {
		t.Errorf("panicking record %+v", bad)
	}
	if got[0].Details == "" || got[2].Details == "" {
		t.Errorf("neighbours lost their details: %+v / %+v", got[0], got[2])
	}
}

func TestParseAuditRecord(t *testing.T) {
	digest := auditRecord(16, 0, 0, []byte("user1"), auditBase, "192.0.2.10", nil)
	local := auditRecord(16, 0, 2, nil, auditBase, "0000:0000:0000:0000:0000:0000:0000:0001", nil)
	kvm := auditRecord(29, 0, 3, nil, auditBase, "", nil)
	kerb := auditRecord(16, 0, 1, exampleSID(), auditBase, "", nil)
	ts := time.Unix(auditBase, 0).UTC()

	tests := []struct {
		name string
		in   []byte
		want AuditLogEntry
	}{
		{"digest", digest, AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1", Address: "192.0.2.10", Details: "Intel AMT transitioned to setup mode."}},
		{"local and ::1", local, AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", InitiatorType: 2, Address: "::1", Details: "Intel AMT transitioned to setup mode."}},
		{"kvm default port", kvm, AuditLogEntry{Time: ts, AppID: 29, App: "Keyboard Video Mouse Events", Event: "KVM Opt-In Enabled", InitiatorType: 3, Details: "User consent for a KVM session is now required."}},
		{"known app, unknown event id", auditRecord(16, 99, 2, nil, auditBase, "", nil), AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", EventID: 99, InitiatorType: 2}},
		{"kerberos", kerb, AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", InitiatorType: 1, Initiator: "S-1-5-21-1000-2000-3000", Details: "Intel AMT transitioned to setup mode."}},
		{"unknown ids", auditRecord(99, 7, 2, nil, auditBase, "", nil), AuditLogEntry{Time: ts, AppID: 99, EventID: 7, InitiatorType: 2}},
		{"empty", nil, AuditLogEntry{}},
		{"3 bytes", digest[:3], AuditLogEntry{}},
		{"only app and event", digest[:4], AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started"}},
		{"no digest length", digest[:5], AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started"}},
		{"digest name cut", digest[:8], AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started"}},
		{"kerberos length cut", kerb[:12], AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", InitiatorType: 1}},
		{"kerberos sid too short", auditRecord(16, 0, 1, []byte{1, 2, 3}, auditBase, "", nil), AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", InitiatorType: 1, Details: "Intel AMT transitioned to setup mode."}},
		{"time cut", digest[:12], AuditLogEntry{AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1"}},
		{"address length missing", digest[:15], AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1"}},
		{"address cut", digest[:20], AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1"}},
		{"ex length missing", digest[:len(digest)-1], AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1", Address: "192.0.2.10"}},
		{"ex data cut", append(digest[:len(digest)-1:len(digest)-1], 5, 1), AuditLogEntry{Time: ts, AppID: 16, App: "Security Admin Events", Event: "AMT Provisioning Started", Initiator: "user1", Address: "192.0.2.10"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAuditRecord(tc.in); got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestAuditLogMissingReadRecordsOutput(t *testing.T) {
	body := []byte(`<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope"><a:Body></a:Body></a:Envelope>`)
	d, _ := auditDevice(func(string) ([]byte, error) { return body, nil })
	if _, err := d.AuditLog(); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("got %v", err)
	}
}
