package amt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	amtSchema = "http://intel.com/wbem/wscim/1/amt-schema/1/"
	ipsSchema = "http://intel.com/wbem/wscim/1/ips-schema/1/"
	cimSchema = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/"
	xsiNS     = "http://www.w3.org/2001/XMLSchema-instance"

	actionPut = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Put"
)

// resourceURI returns the WS-Man resource URI of an AMT_, IPS_ or CIM_ class.
func resourceURI(class string) string {
	switch {
	case strings.HasPrefix(class, "IPS_"):
		return ipsSchema + class
	case strings.HasPrefix(class, "CIM_"):
		return cimSchema + class
	default:
		return amtSchema + class
	}
}

// cimObject is one instance as the device returned it: every property in device order with
// its text, so a Put sends back the complete object with only the changed values replaced
// (as MeshCommander does). The library's request structs cannot be used for this: they drop
// false booleans (omitempty) and omit properties they do not model.
type cimObject struct {
	class string
	props []cimProp
}

type cimProp struct {
	name   string
	value  string
	nested bool // the property has child elements (an endpoint reference); not writable here
}

// parseCIMObjects returns every element named class (local name) in a WS-Man response, from
// a Get body as well as from Pull items. Properties marked xsi:nil are left out.
func parseCIMObjects(xmlOutput, class string) ([]*cimObject, error) {
	type propXML struct {
		XMLName  xml.Name
		Text     string     `xml:",chardata"`
		Nil      string     `xml:"http://www.w3.org/2001/XMLSchema-instance nil,attr"`
		Children []struct{} `xml:",any"`
	}
	type objXML struct {
		Props []propXML `xml:",any"`
	}
	dec := xml.NewDecoder(strings.NewReader(xmlOutput))
	var objs []*cimObject
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, class, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != class {
			continue
		}
		var x objXML
		if err := dec.DecodeElement(&x, &start); err != nil {
			return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, class, err)
		}
		o := &cimObject{class: class}
		for _, p := range x.Props {
			if p.Nil == "true" {
				continue
			}
			o.props = append(o.props, cimProp{name: p.XMLName.Local, value: p.Text, nested: len(p.Children) > 0})
		}
		objs = append(objs, o)
	}
}

func (o *cimObject) has(name string) bool {
	for _, p := range o.props {
		if p.name == name {
			return true
		}
	}
	return false
}

// str returns the trimmed text of the first property named name, "" if absent.
func (o *cimObject) str(name string) string {
	for _, p := range o.props {
		if p.name == name {
			return strings.TrimSpace(p.value)
		}
	}
	return ""
}

// strs returns the trimmed texts of every property named name (array properties).
func (o *cimObject) strs(name string) []string {
	var out []string
	for _, p := range o.props {
		if p.name == name {
			out = append(out, strings.TrimSpace(p.value))
		}
	}
	return out
}

func (o *cimObject) boolean(name string) bool { return o.str(name) == "true" }

// integer returns the property as int, 0 if absent or not a number.
func (o *cimObject) integer(name string) int {
	n, err := strconv.ParseInt(o.str(name), 10, 64)
	if err != nil {
		return 0
	}
	return int(n)
}

// set replaces the property's value (all occurrences collapse to one) or appends it.
func (o *cimObject) set(name, value string) { o.setAll(name, []string{value}) }

// setAll replaces every occurrence of name by values at the position of the first occurrence,
// or appends them. Empty values remove the property.
func (o *cimObject) setAll(name string, values []string) {
	pos := -1
	kept := o.props[:0:0]
	for _, p := range o.props {
		if p.name == name {
			if pos < 0 {
				pos = len(kept)
			}
			continue
		}
		kept = append(kept, p)
	}
	if pos < 0 {
		pos = len(kept)
	}
	add := make([]cimProp, 0, len(values))
	for _, v := range values {
		add = append(add, cimProp{name: name, value: v})
	}
	o.props = append(kept[:pos:pos], append(add, kept[pos:]...)...)
}

func (o *cimObject) del(name string) { o.setAll(name, nil) }

// marshal renders the object as Put body element with prefix h.
func (o *cimObject) marshal() (string, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<h:%s xmlns:h="%s">`, o.class, resourceURI(o.class))
	for _, p := range o.props {
		if p.nested {
			return "", fmt.Errorf("%w: %s.%s has nested elements", ErrIncompleteResponse, o.class, p.name)
		}
		fmt.Fprintf(&b, "<h:%s>", p.name)
		if err := xml.EscapeText(&b, []byte(p.value)); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "</h:%s>", p.name)
	}
	fmt.Fprintf(&b, "</h:%s>", o.class)
	return b.String(), nil
}

type selector struct{ name, value string }

var messageID atomic.Uint64

// envelope builds a WS-Man request in the library's format (same namespaces and header).
func envelope(action, resURI string, selectors []selector, body string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`)
	fmt.Fprintf(&b, `<Header><a:Action>%s</a:Action><a:To>/wsman</a:To><w:ResourceURI>%s</w:ResourceURI><a:MessageID>%d</a:MessageID>`,
		action, resURI, messageID.Add(1))
	b.WriteString(`<a:ReplyTo><a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address></a:ReplyTo><w:OperationTimeout>PT60S</w:OperationTimeout>`)
	if len(selectors) > 0 {
		b.WriteString("<w:SelectorSet>")
		for _, s := range selectors {
			fmt.Fprintf(&b, `<w:Selector Name=%q>`, s.name)
			_ = xml.EscapeText(&b, []byte(s.value))
			b.WriteString("</w:Selector>")
		}
		b.WriteString("</w:SelectorSet>")
	}
	b.WriteString("</Header><Body>")
	b.WriteString(body)
	b.WriteString("</Body></Envelope>")
	return b.String()
}

// putObject writes o back with a WS-Transfer Put. Errors come unwrapped from the client
// (*amterror.AMTError for SOAP faults).
func (d *Device) putObject(o *cimObject, selectors []selector) error {
	body, err := o.marshal()
	if err != nil {
		return err
	}
	_, err = d.msgs.Client.Post(envelope(actionPut, resourceURI(o.class), selectors, body))
	return err
}
