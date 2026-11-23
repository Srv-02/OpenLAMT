package amt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	alarmclock "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/alarmclock"
)

// Wake alarms (issue #14): AMT_AlarmClockService.AddAlarm creates an IPS_AlarmClockOccurrence
// that powers the device on at a time. AMT knows only create and delete; "edit" is delete and
// create again.

const (
	// MaxAlarms is the number of alarms AMT stores; AddAlarm fails with more.
	MaxAlarms = 5
	// MaxAlarmName is the longest alarm name in characters (as MeshCommander).
	MaxAlarmName = 32
	// MaxAlarmInterval is the longest repeat interval AMT accepts (800 days; from Intel's
	// reference, not measured on a device).
	MaxAlarmInterval = 800 * 24 * time.Hour
)

var (
	// ErrAlarmsFull: the device stores MaxAlarms alarms already.
	ErrAlarmsFull = errors.New("the device stores no more wake alarms")
	// ErrAlarmExists: an alarm with this name exists.
	ErrAlarmExists = errors.New("a wake alarm with this name exists")
	// ErrAlarmPast: the start time is not in the future.
	ErrAlarmPast = errors.New("the wake alarm time is not in the future")
)

// Alarm is one wake alarm of the device.
type Alarm struct {
	Name               string        // InstanceID, also the name shown
	Start              time.Time     // next wake, UTC; zero if the device's time was unreadable
	Interval           time.Duration // repeat; 0 = once
	DeleteOnCompletion bool          // a single alarm that the device deletes after it fired
}

// AlarmSpec is a new wake alarm.
type AlarmSpec struct {
	Name               string
	Start              time.Time
	Interval           time.Duration // 0 or a whole number of minutes, at most MaxAlarmInterval
	DeleteOnCompletion bool          // only without Interval
}

// Validate checks the spec against now without device access. It returns ErrAlarmPast for a
// start that is not after now and a *ValidationError for the rest.
func (s AlarmSpec) Validate(now time.Time) error {
	name := strings.TrimSpace(s.Name)
	switch {
	case name == "":
		return invalid("Name", "must not be empty")
	case utf8.RuneCountInString(name) > MaxAlarmName:
		return invalid("Name", fmt.Sprintf("at most %d characters", MaxAlarmName))
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return invalid("Name", "must not contain control characters")
	case s.Interval < 0 || s.Interval%time.Minute != 0 || s.Interval > MaxAlarmInterval:
		return invalid("Interval", "0 or whole minutes up to 800 days")
	case s.DeleteOnCompletion && s.Interval > 0:
		return invalid("DeleteOnCompletion", "only for an alarm that does not repeat")
	case !s.Start.After(now):
		return ErrAlarmPast
	}
	return nil
}

// xmlText escapes s for an XML text node or attribute value. The library writes the alarm's
// ElementName, InstanceID and the Delete selector without escaping.
func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// intervalRE is an xs:duration of days, hours, minutes and seconds ("P1DT0H0M", "P0DT0H0M0S").
var intervalRE = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseInterval reads an xs:duration; an unreadable text yields 0 (no repeat).
func parseInterval(s string) time.Duration {
	m := intervalRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n := func(i int) time.Duration {
		v, _ := strconv.Atoi(m[i])
		return time.Duration(v)
	}
	return n(1)*24*time.Hour + n(2)*time.Hour + n(3)*time.Minute + n(4)*time.Second
}

// alarmXML is one IPS_AlarmClockOccurrence of a Pull. The library's decoder fails the whole
// response on a time it cannot read, so the values are read as text here.
type alarmXML struct {
	ElementName        string `xml:"ElementName"`
	InstanceID         string `xml:"InstanceID"`
	DeleteOnCompletion string `xml:"DeleteOnCompletion"`
	StartTime          struct {
		Datetime string `xml:"Datetime"`
	} `xml:"StartTime"`
	Interval struct {
		Interval string `xml:"Interval"`
	} `xml:"Interval"`
}

func (x alarmXML) alarm() Alarm {
	a := Alarm{Name: strings.TrimSpace(x.InstanceID), Interval: parseInterval(x.Interval.Interval),
		DeleteOnCompletion: strings.TrimSpace(x.DeleteOnCompletion) == "true"}
	if a.Name == "" {
		a.Name = strings.TrimSpace(x.ElementName)
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(x.StartTime.Datetime)); err == nil {
		a.Start = t.UTC()
	}
	return a
}

// GetAlarms lists the wake alarms (IPS_AlarmClockOccurrence), soonest first.
func (d *Device) GetAlarms() ([]Alarm, error) {
	const op = "read wake alarms"
	occ := d.msgs.IPS.AlarmClockOccurrence
	items, err := enumeratePull(func() (string, error) {
		r, err := occ.Enumerate()
		return r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) ([]Alarm, string, error) {
		r, err := occ.Pull(ctx)
		if err != nil {
			return nil, "", err
		}
		out := xmlOf(r.Message)
		var body struct {
			Items []alarmXML `xml:"Body>PullResponse>Items>IPS_AlarmClockOccurrence"`
		}
		if err := xml.Unmarshal([]byte(out), &body); err != nil {
			return nil, "", fmt.Errorf("%w: unreadable wake alarms: %v", ErrIncompleteResponse, err)
		}
		var list []Alarm
		for _, x := range body.Items {
			list = append(list, x.alarm())
		}
		return list, out, nil
	})
	if err != nil {
		return nil, writeError(op, err)
	}
	sortAlarms(items)
	return items, nil
}

func sortAlarms(a []Alarm) {
	slices.SortStableFunc(a, func(x, y Alarm) int { return x.Start.Compare(y.Start) })
}

// AddAlarm creates a wake alarm. It checks the spec and the device's list first: ErrAlarmPast,
// ErrAlarmExists (same name, any case) and ErrAlarmsFull (MaxAlarms stored) are returned
// without an AddAlarm call. A non-zero ReturnValue is a *MethodError.
func (d *Device) AddAlarm(s AlarmSpec) error {
	const op = "add wake alarm"
	if err := s.Validate(timeNow()); err != nil {
		return err
	}
	name := strings.TrimSpace(s.Name)
	have, err := d.GetAlarms()
	if err != nil {
		return err
	}
	for _, a := range have {
		if strings.EqualFold(a.Name, name) {
			return ErrAlarmExists
		}
	}
	if len(have) >= MaxAlarms {
		return ErrAlarmsFull
	}
	esc := xmlText(name)
	resp, err := d.msgs.AMT.AlarmClockService.AddAlarm(alarmclock.AlarmClockOccurrence{
		ElementName: esc, InstanceID: esc, StartTime: s.Start.UTC().Truncate(time.Second),
		Interval: int(s.Interval / time.Minute), DeleteOnCompletion: s.DeleteOnCompletion,
	})
	if err != nil {
		return writeError(op, err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "AddAlarm_OUTPUT") {
		return fmt.Errorf("%s: %w: AddAlarm_OUTPUT", op, ErrIncompleteResponse)
	}
	if rv := int(resp.Body.AddAlarmOutput.ReturnValue); rv != 0 {
		return &MethodError{Method: "AddAlarm", ReturnValue: rv}
	}
	return nil
}

// DeleteAlarm deletes the wake alarm called name. The UI asks for a simple confirmation first
// (OpAlarmDelete).
func (d *Device) DeleteAlarm(name string) error {
	const op = "delete wake alarm"
	if strings.TrimSpace(name) == "" {
		return invalid("Name", "must not be empty")
	}
	_, err := d.msgs.IPS.AlarmClockOccurrence.Delete(xmlText(name))
	return writeError(op, err)
}
