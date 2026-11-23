package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

// Hand-built WS-Management calls for classes the library does not model (System Defense,
// agent presence): Enumerate and Pull, Create, Delete and a method call, in the envelope of
// cimobject.go. Faults come from the client as *amterror.AMTError (HTTP 400).

const (
	transferNS   = "http://schemas.xmlsoap.org/ws/2004/09/transfer"
	actionCreate = transferNS + "/Create"
	actionDelete = transferNS + "/Delete"
	addressingNS = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	wsmanNS      = "http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd"
	anonymousURI = addressingNS + "/role/anonymous"
)

// pullRaw enumerates class and returns the raw response of every Pull. A fault on Pull is
// wrapped with errPullFault (the class exists).
func (d *Device) pullRaw(class string) ([]string, error) {
	uri := resourceURI(class)
	return enumeratePull(func() (string, error) {
		raw, err := d.msgs.Client.Post(envelope(actionEnum, uri, nil, `<Enumerate xmlns="`+enumerationNS+`" />`))
		if err != nil {
			return "", err
		}
		var r struct {
			Ctx string `xml:"Body>EnumerateResponse>EnumerationContext"`
		}
		if err := xml.Unmarshal(raw, &r); err != nil {
			return "", fmt.Errorf("%w: unreadable enumerate response: %v", ErrIncompleteResponse, err)
		}
		return strings.TrimSpace(r.Ctx), nil
	}, func(ctx string) ([]string, string, error) {
		raw, err := d.msgs.Client.Post(envelope(actionPull, uri, nil,
			`<Pull xmlns="`+enumerationNS+`"><EnumerationContext>`+xmlText(ctx)+`</EnumerationContext><MaxElements>32</MaxElements></Pull>`))
		var amtErr *amterror.AMTError
		if errors.As(err, &amtErr) {
			return nil, "", fmt.Errorf("%w: %w", errPullFault, err)
		}
		if err != nil {
			return nil, "", err
		}
		return []string{string(raw)}, string(raw), nil
	})
}

// pullObjectsRaw enumerates class and parses its instances (properties with child elements are
// kept as nested, without value).
func (d *Device) pullObjectsRaw(class string) ([]*cimObject, error) {
	pulls, err := d.pullRaw(class)
	if err != nil {
		return nil, err
	}
	var all []*cimObject
	for _, p := range pulls {
		objs, err := parseCIMObjects(p, class)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	return all, nil
}

// optionalClass reads class and reports a missing class (a fault on Enumerate other than
// AccessDenied) as offered false without error.
func (d *Device) optionalClass(class string) (objs []*cimObject, offered bool, err error) {
	objs, err = d.pullObjectsRaw(class)
	if err != nil {
		if isMissingClass(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	return objs, true, nil
}

// createRaw sends a WS-Transfer Create of class with the body properties (already rendered,
// escaped XML of the h: elements).
func (d *Device) createRaw(class, props string) error {
	uri := resourceURI(class)
	_, err := d.msgs.Client.Post(envelope(actionCreate, uri, nil,
		`<h:`+class+` xmlns:h="`+uri+`">`+props+`</h:`+class+`>`))
	return err
}

// deleteRaw sends a WS-Transfer Delete of the instance of class with selectors.
func (d *Device) deleteRaw(class string, selectors []selector) error {
	_, err := d.msgs.Client.Post(envelope(actionDelete, resourceURI(class), selectors, ""))
	return err
}

// deleteRawSel is deleteRaw with a selector set given as XML (endpoint references).
func (d *Device) deleteRawSel(class, selectorSet string) error {
	env := envelope(actionDelete, resourceURI(class), nil, "")
	env = strings.Replace(env, "</Header>", selectorSet+"</Header>", 1)
	_, err := d.msgs.Client.Post(env)
	return err
}

// invokeRaw calls method of class on the instance with selectors; args is the rendered XML of
// the h: input elements. A non-zero ReturnValue is a *MethodError.
func (d *Device) invokeRaw(class, method string, selectors []selector, args string) error {
	uri := resourceURI(class)
	raw, err := d.msgs.Client.Post(envelope(uri+"/"+method, uri, selectors,
		`<h:`+method+`_INPUT xmlns:h="`+uri+`">`+args+`</h:`+method+`_INPUT>`))
	if err != nil {
		return err
	}
	rv, ok := returnValue(raw, method+"_OUTPUT")
	if !ok {
		return fmt.Errorf("%w: %s_OUTPUT", ErrIncompleteResponse, method)
	}
	if rv != 0 {
		return &MethodError{Method: method, ReturnValue: rv}
	}
	return nil
}

// returnValue finds the ReturnValue inside the element named output.
func returnValue(raw []byte, output string) (int, bool) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) || err != nil {
			return 0, false
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != output {
			continue
		}
		var o struct {
			ReturnValue *int `xml:"ReturnValue"`
		}
		if err := dec.DecodeElement(&o, &start); err != nil || o.ReturnValue == nil {
			return 0, false
		}
		return *o.ReturnValue, true
	}
}

// prop renders one property element of a Create body with prefix h, the value escaped.
func prop(name, value string) string {
	return "<h:" + name + ">" + xmlText(value) + "</h:" + name + ">"
}

// portEPR is the endpoint reference of the wired port (CIM_EthernetPort) as MeshCommander
// writes it into a Create body of an association.
func portEPR() string {
	return `<Address xmlns="` + addressingNS + `">` + addressingNS + `</Address>` +
		`<ReferenceParameters xmlns="` + addressingNS + `"><ResourceURI xmlns="` + wsmanNS + `">` + resourceURI(classEthernetPort) +
		`</ResourceURI><SelectorSet xmlns="` + wsmanNS + `"><Selector Name="DeviceID">` + wiredPortID + `</Selector></SelectorSet></ReferenceParameters>`
}

// policyEPR is the endpoint reference of a System Defense policy in a Create body.
func policyEPR(id string) string {
	return `<Address xmlns="` + addressingNS + `">` + addressingNS + `</Address>` +
		`<ReferenceParameters xmlns="` + addressingNS + `"><ResourceURI xmlns="` + wsmanNS + `">` + resourceURI(classDefensePolicy) +
		`</ResourceURI><SelectorSet xmlns="` + wsmanNS + `"><Selector Name="InstanceID">` + xmlText(id) + `</Selector></SelectorSet></ReferenceParameters>`
}

// createPortLink links the wired port to policy id through the association class
// (AMT_NetworkPortSystemDefensePolicy or AMT_AgentPresenceInterfacePolicy).
func (d *Device) createPortLink(class, id string) error {
	return d.createRaw(class, `<h:Antecedent>`+portEPR()+`</h:Antecedent><h:Dependent>`+policyEPR(id)+`</h:Dependent>`)
}

// deletePortLink deletes the link of the wired port to policy id (selectors as MeshCommander
// sends them).
func (d *Device) deletePortLink(class, id string) error {
	epr := func(uri, sels string) string {
		return `<a:EndpointReference><a:Address>` + anonymousURI + `</a:Address><a:ReferenceParameters><w:ResourceURI>` + uri +
			`</w:ResourceURI><w:SelectorSet>` + sels + `</w:SelectorSet></a:ReferenceParameters></a:EndpointReference>`
	}
	set := `<w:SelectorSet><w:Selector Name="Antecedent">` +
		epr(resourceURI(classEthernetPort), `<w:Selector Name="CreationClassName">`+classEthernetPort+`</w:Selector><w:Selector Name="DeviceID">`+wiredPortID+`</w:Selector>`) +
		`</w:Selector><w:Selector Name="Dependent">` +
		epr(resourceURI(classDefensePolicy), `<w:Selector Name="InstanceID">`+xmlText(id)+`</w:Selector>`) +
		`</w:Selector></w:SelectorSet>`
	return d.deleteRawSel(class, set)
}

// eprXML is an endpoint reference of an association instance: the resource URI and the
// selectors (a nested endpoint reference inside a selector is kept as its inner XML).
type eprXML struct {
	ResourceURI string `xml:"ReferenceParameters>ResourceURI"`
	Selectors   []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
		Inner string `xml:",innerxml"`
	} `xml:"ReferenceParameters>SelectorSet>Selector"`
}

// selector returns the trimmed value of the selector called name.
func (e eprXML) selector(name string) string {
	for _, s := range e.Selectors {
		if s.Name == name {
			return strings.TrimSpace(s.Value)
		}
	}
	return ""
}

// class is the class name at the end of the resource URI.
func (e eprXML) class() string {
	u := strings.TrimSpace(e.ResourceURI)
	return u[strings.LastIndex(u, "/")+1:]
}

// linkXML is one instance of an association: Antecedent and Dependent.
type linkXML struct {
	Antecedent eprXML `xml:"Antecedent"`
	Dependent  eprXML `xml:"Dependent"`
	Inner      string `xml:",innerxml"`
}

// pullLinks enumerates the association class and returns its instances.
func (d *Device) pullLinks(class string) ([]linkXML, error) {
	pulls, err := d.pullRaw(class)
	if err != nil {
		return nil, err
	}
	var all []linkXML
	for _, p := range pulls {
		dec := xml.NewDecoder(strings.NewReader(p))
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, class, err)
			}
			start, ok := tok.(xml.StartElement)
			if !ok || start.Name.Local != class {
				continue
			}
			var l linkXML
			if err := dec.DecodeElement(&l, &start); err != nil {
				return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, class, err)
			}
			all = append(all, l)
		}
	}
	return all, nil
}
