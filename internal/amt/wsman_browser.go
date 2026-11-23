package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

// The WSMAN browser (issue #24): read every instance of one AMT_, CIM_ or IPS_ class with
// Enumerate and Pull and hand back the properties as text. Read only.

// Limits of one class read, so that a huge class (a log with thousands of entries) cannot use
// up memory: the read stops at the first limit and reports Truncated.
const (
	WSManMaxInstances = 200     // instances kept
	WSManMaxBytes     = 1 << 20 // rendered XML of the kept instances
	wsmanPullSize     = 32      // MaxElements of one Pull
	wsmanMaxPulls     = 64      // Pulls of one read; a device that never ends is cut off here
	actionRelease     = enumerationNS + "/Release"
	wsmanFaultMissing = "DestinationUnreachable"
)

// WSManClasses returns the classes the browser offers: AMT_ first, then CIM_ and IPS_, each
// group sorted by name.
func WSManClasses() []string { return slices.Clone(wsmanClassNames) }

var wsmanClassRE = regexp.MustCompile(`^(AMT|CIM|IPS)_[A-Za-z0-9]+$`)

// ValidWSManClass reports whether name is a class name the browser reads: the prefix AMT_,
// CIM_ or IPS_ and letters and digits after it.
func ValidWSManClass(name string) bool { return wsmanClassRE.MatchString(name) }

// WSManProp is one property of an instance, as the device sent it.
type WSManProp struct {
	Name  string
	Value string // an endpoint reference or other nested value is flattened to "Name=value; ..."
}

// WSManInstance is one instance of a class. Class differs from the read class when the device
// returns derived classes (reading CIM_SettingData returns AMT_ and IPS_ objects).
type WSManInstance struct {
	Class string
	Props []WSManProp
	XML   string // the instance as indented XML without namespaces, for the clipboard
}

// WSManResult is what BrowseClass read.
type WSManResult struct {
	Class     string
	Instances []WSManInstance
	Truncated bool // the read stopped at WSManMaxInstances or WSManMaxBytes
}

// XML returns the XML of all instances, one after the other.
func (r WSManResult) XML() string {
	parts := make([]string, len(r.Instances))
	for i, in := range r.Instances {
		parts[i] = in.XML
	}
	return strings.Join(parts, "\n")
}

// ClassUnsupportedError: the device answered the Enumerate of a class with a SOAP fault that
// says the class does not exist. On a class every firmware has, the same fault is how some
// firmware refuses an account that lacks the permission.
type ClassUnsupportedError struct {
	Class string
	Fault string // the fault subcode without its prefix, e.g. "DestinationUnreachable"
}

func (e *ClassUnsupportedError) Error() string {
	return fmt.Sprintf("the device does not support %s (%s)", e.Class, e.Fault)
}

// BrowseClass reads all instances of class (see ValidWSManClass), at most WSManMaxInstances
// and WSManMaxBytes. A missing class is a *ClassUnsupportedError, a missing permission
// ErrAccessDenied; a class without instances is an empty result.
func (d *Device) BrowseClass(class string) (WSManResult, error) {
	if !ValidWSManClass(class) {
		return WSManResult{}, invalid("Class", "must be an AMT_, CIM_ or IPS_ class name")
	}
	uri := resourceURI(class)
	res := WSManResult{Class: class}

	raw, err := d.msgs.Client.Post(envelope(actionEnum, uri, nil, `<Enumerate xmlns="`+enumerationNS+`" />`))
	if err != nil {
		return WSManResult{}, browseError(class, err)
	}
	var enum struct {
		Resp *struct {
			Ctx string `xml:"EnumerationContext"`
		} `xml:"Body>EnumerateResponse"`
	}
	if err := xml.Unmarshal(raw, &enum); err != nil || enum.Resp == nil {
		return WSManResult{}, fmt.Errorf("read %s: %w: unreadable enumerate response", class, ErrIncompleteResponse)
	}
	ctx, size := strings.TrimSpace(enum.Resp.Ctx), 0

	for pulls := 0; ctx != ""; pulls++ {
		if pulls >= wsmanMaxPulls {
			res.Truncated = true
			break
		}
		raw, err := d.msgs.Client.Post(envelope(actionPull, uri, nil,
			`<Pull xmlns="`+enumerationNS+`"><EnumerationContext>`+xmlText(ctx)+`</EnumerationContext><MaxElements>`+
				fmt.Sprint(wsmanPullSize)+`</MaxElements></Pull>`))
		if err != nil {
			return WSManResult{}, browseError(class, err)
		}
		items, next, end, err := parseBrowseItems(raw)
		if err != nil {
			return WSManResult{}, fmt.Errorf("read %s: %w", class, err)
		}
		for _, in := range items {
			if len(res.Instances) >= WSManMaxInstances || (size+len(in.XML) > WSManMaxBytes && len(res.Instances) > 0) {
				res.Truncated = true
				break
			}
			size += len(in.XML)
			res.Instances = append(res.Instances, in)
		}
		if res.Truncated {
			break
		}
		if end || next == "" {
			ctx = ""
			break
		}
		ctx = next
		if len(res.Instances) >= WSManMaxInstances {
			res.Truncated = true // the sequence goes on after the last instance we keep
			break
		}
	}
	if res.Truncated && ctx != "" {
		// Free the enumeration on the device (AMT keeps only a few); the answer does not matter.
		_, _ = d.msgs.Client.Post(envelope(actionRelease, uri, nil,
			`<Release xmlns="`+enumerationNS+`"><EnumerationContext>`+xmlText(ctx)+`</EnumerationContext></Release>`))
	}
	return res, nil
}

// browseError maps a failed request: a missing permission is ErrAccessDenied, the fault that
// says the class does not exist a *ClassUnsupportedError; anything else keeps its cause.
func browseError(class string, err error) error {
	var amtErr *amterror.AMTError
	if errors.As(err, &amtErr) {
		fault := amtErr.SubCode
		if _, after, ok := strings.Cut(fault, ":"); ok {
			fault = after
		}
		if strings.Contains(fault, "AccessDenied") {
			return fmt.Errorf("read %s: %w", class, ErrAccessDenied)
		}
		if strings.Contains(fault, wsmanFaultMissing) || strings.Contains(fault, "InvalidResourceURI") {
			return &ClassUnsupportedError{Class: class, Fault: fault}
		}
	}
	return writeError("read "+class, err)
}

// wsNode is one XML element of a Pull item.
type wsNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Kids    []wsNode   `xml:",any"`
}

func (n wsNode) isNil() bool {
	for _, a := range n.Attrs {
		if a.Name.Local == "nil" && a.Value == "true" {
			return true
		}
	}
	return false
}

// parseBrowseItems reads a Pull response: the instances (every child of Items, whatever its
// name), the next context and whether the sequence ended.
func parseBrowseItems(raw []byte) (items []WSManInstance, ctx string, end bool, err error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	sawPull := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", false, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "PullResponse":
			sawPull = true
		case "EnumerationContext":
			var s string
			if err := dec.DecodeElement(&s, &start); err != nil {
				return nil, "", false, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
			}
			ctx = strings.TrimSpace(s)
		case "EndOfSequence":
			end = true
			if err := dec.Skip(); err != nil {
				return nil, "", false, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
			}
		case "Items":
			for {
				t, err := dec.Token()
				if err != nil {
					return nil, "", false, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
				}
				if _, closed := t.(xml.EndElement); closed {
					break
				}
				if s, ok := t.(xml.StartElement); ok {
					var n wsNode
					if err := dec.DecodeElement(&n, &s); err != nil {
						return nil, "", false, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
					}
					items = append(items, instanceOf(n))
				}
			}
		}
	}
	if !sawPull {
		return nil, "", false, fmt.Errorf("%w: no PullResponse", ErrIncompleteResponse)
	}
	return items, ctx, end, nil
}

func instanceOf(n wsNode) WSManInstance {
	in := WSManInstance{Class: n.XMLName.Local}
	for _, k := range n.Kids {
		if k.isNil() {
			continue
		}
		in.Props = append(in.Props, WSManProp{Name: k.XMLName.Local, Value: flattenValue(k)})
	}
	var b strings.Builder
	writeXML(&b, n, 0)
	in.XML = b.String()
	return in
}

// flattenValue is the text of a property. A nested value with one leaf (a date inside its
// wrapper) is that leaf's text; otherwise it lists the leaves as "Name=value; ..." (an endpoint
// reference: Address, ResourceURI, the selectors by their Name attribute).
func flattenValue(n wsNode) string {
	if len(n.Kids) == 0 {
		return strings.TrimSpace(n.Text)
	}
	if len(n.Kids) == 1 && len(n.Kids[0].Kids) == 0 {
		return strings.TrimSpace(n.Kids[0].Text)
	}
	var parts []string
	var walk func(wsNode)
	walk = func(k wsNode) {
		if len(k.Kids) > 0 {
			for _, c := range k.Kids {
				walk(c)
			}
			return
		}
		label := k.XMLName.Local
		for _, a := range k.Attrs {
			if a.Name.Local == "Name" {
				label = a.Value
			}
		}
		parts = append(parts, label+"="+strings.TrimSpace(k.Text))
	}
	walk(n)
	return strings.Join(parts, "; ")
}

// writeXML renders n indented by two spaces per level, without namespaces.
func writeXML(b *strings.Builder, n wsNode, depth int) {
	pad := strings.Repeat("  ", depth)
	b.WriteString(pad + "<" + n.XMLName.Local)
	for _, a := range n.Attrs {
		if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
			continue
		}
		b.WriteString(" " + a.Name.Local + `="`)
		_ = xml.EscapeText(b, []byte(a.Value))
		b.WriteString(`"`)
	}
	if len(n.Kids) == 0 {
		text := strings.TrimSpace(n.Text)
		if text == "" {
			b.WriteString("/>")
			if depth > 0 {
				b.WriteString("\n")
			}
			return
		}
		b.WriteString(">")
		_ = xml.EscapeText(b, []byte(text))
		b.WriteString("</" + n.XMLName.Local + ">")
		if depth > 0 {
			b.WriteString("\n")
		}
		return
	}
	b.WriteString(">\n")
	for _, k := range n.Kids {
		writeXML(b, k, depth+1)
	}
	b.WriteString(pad + "</" + n.XMLName.Local + ">")
	if depth > 0 {
		b.WriteString("\n")
	}
}

// WSManMeaning names the value of a property whose numbers have a known meaning ("1" of
// AMTNetworkEnabled is "Enabled"); "" if it is unknown. The CIM state properties apply to every
// class, the others to one class.
func WSManMeaning(class, prop, value string) string {
	value = strings.TrimSpace(value)
	if m, ok := wsmanMeanings[class+"."+prop][value]; ok {
		return m
	}
	return wsmanMeanings[prop][value]
}

var wsmanMeanings = map[string]map[string]string{
	"EnabledState": {"0": "Unknown", "1": "Other", "2": "Enabled", "3": "Disabled", "4": "Shutting Down",
		"5": "Not Applicable", "6": "Enabled but Offline", "7": "In Test", "8": "Deferred", "9": "Quiesce", "10": "Starting"},
	"RequestedState": {"0": "Unknown", "2": "Enabled", "3": "Disabled", "4": "Shut Down", "5": "No Change",
		"6": "Offline", "7": "Test", "8": "Deferred", "9": "Quiesce", "10": "Reboot", "11": "Reset", "12": "Not Applicable"},
	"HealthState": {"0": "Unknown", "5": "OK", "10": "Degraded/Warning", "15": "Minor failure", "20": "Major failure",
		"25": "Critical failure", "30": "Non-recoverable error"},
	"AMT_GeneralSettings.AMTNetworkEnabled":              {"0": "Disabled", "1": "Enabled"},
	"AMT_GeneralSettings.PowerSource":                    {"0": "AC", "1": "DC"},
	"AMT_GeneralSettings.PreferredAddressFamily":         {"0": "IPv4", "1": "IPv6"},
	"AMT_SetupAndConfigurationService.ProvisioningMode":  {"1": "Admin Control Mode", "4": "Client Control Mode"},
	"AMT_SetupAndConfigurationService.ProvisioningState": {"0": "Pre-provisioning", "1": "In provisioning", "2": "Post-provisioning"},
}
