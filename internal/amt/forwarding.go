package amt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
)

// Event forwarding (issue #18): the WS-Eventing subscriptions through which Intel AMT sends its
// events to a listener. AMT delivers events only as WS-Management events (Push or PushWithAck);
// OpenLAMT sets the targets and receives nothing itself. The library has none of these classes,
// so the requests are built here, after MeshCommander's amtstack.Subscribe / UnSubscribe and the
// Intel AMT SDK (CIM_FilterCollection, CIM_ListenerDestinationWSManagement,
// CIM_FilterCollectionSubscription).

const (
	classFilterCollection = "CIM_FilterCollection"
	classListener         = "CIM_ListenerDestination"
	classListenerWSMan    = "CIM_ListenerDestinationWSManagement"
	classSubscription     = "CIM_FilterCollectionSubscription"

	actionEnumerateRaw = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Enumerate"
	actionPullRaw      = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Pull"
	actionSubscribe    = "http://schemas.xmlsoap.org/ws/2004/08/eventing/Subscribe"
	actionUnsubscribe  = "http://schemas.xmlsoap.org/ws/2004/08/eventing/Unsubscribe"
	eventingNS         = "http://schemas.xmlsoap.org/ws/2004/08/eventing"
	anonymousAddress   = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"

	modePush        = "http://schemas.xmlsoap.org/ws/2004/08/eventing/DeliveryModes/Push"
	modePushWithAck = "http://schemas.dmtf.org/wbem/wsman/1/wsman/PushWithAck"
	digestProfile   = "http://schemas.dmtf.org/wbem/wsman/1/wsman/secprofile/http/digest"

	filterPrefix = "Intel(r) AMT:" // of the predefined filter names

	// MaxTargetAddress is the longest address AMT takes (longer ones are ignored).
	MaxTargetAddress = 253
	// MaxTargetCredential is the longest user name or password of the target sign-in.
	MaxTargetCredential = 32
)

// Delivery is CIM_ListenerDestinationWSManagement.DeliveryMode.
type Delivery int

const (
	DeliveryPush        Delivery = 2 // Push
	DeliveryPushWithAck Delivery = 3 // PushWithAck: the listener acknowledges each event
)

// EventFilter is a predefined CIM_FilterCollection: which events a target gets.
type EventFilter struct {
	ID   string // InstanceID, e.g. "Intel(r) AMT:All"
	Name string // CollectionName without "Intel(r) AMT:", e.g. "All"
}

// ForwardingTarget is one subscription: AMT sends the events of the filter to the address.
type ForwardingTarget struct {
	Address    string // CIM_ListenerDestination.Destination
	Delivery   Delivery
	FilterID   string // the subscribed filter
	FilterName string // its name, the ID without prefix if the filter is unknown
	Listener   string // CIM_ListenerDestinationWSManagement.Name, e.g. "Listener Destination 0"
}

// Forwarding is what EventForwarding reads.
type Forwarding struct {
	Filters []EventFilter
	Targets []ForwardingTarget
}

// filterName is the shown name of a filter: CollectionName (or the id) without the prefix.
func filterName(id, collection string) string {
	n := collection
	if n == "" {
		n = id
	}
	return strings.TrimPrefix(n, filterPrefix)
}

// EventForwarding reads the filters, the listeners and the subscriptions between them. A missing
// permission is ErrAccessDenied.
func (d *Device) EventForwarding() (Forwarding, error) {
	const op = "read event forwarding"
	var f Forwarding
	filters, err := d.enumerateRawClass(classFilterCollection)
	if err != nil {
		return f, readAuditError(op, err)
	}
	names := map[string]string{}
	for _, o := range filters {
		id := o.str("InstanceID")
		if id == "" {
			continue
		}
		fl := EventFilter{ID: id, Name: filterName(id, o.str("CollectionName"))}
		names[id] = fl.Name
		f.Filters = append(f.Filters, fl)
	}
	listeners, err := d.enumerateRawClass(classListener, classListenerWSMan)
	if err != nil {
		return f, readAuditError(op, err)
	}
	subs, err := d.subscriptions()
	if err != nil {
		return f, readAuditError(op, err)
	}
	for _, s := range subs {
		i := slices.IndexFunc(listeners, func(o *cimObject) bool { return o.str("Name") == s.listener })
		if i < 0 {
			continue
		}
		l := listeners[i]
		name, ok := names[s.filter]
		if !ok {
			name = filterName(s.filter, "")
		}
		f.Targets = append(f.Targets, ForwardingTarget{Address: l.str("Destination"), Delivery: Delivery(l.integer("DeliveryMode")),
			FilterID: s.filter, FilterName: name, Listener: s.listener})
	}
	return f, nil
}

type subscriptionRef struct{ filter, listener string }

// subscriptions enumerates CIM_FilterCollectionSubscription: the filter InstanceID and the
// listener Name of each.
func (d *Device) subscriptions() ([]subscriptionRef, error) {
	return enumeratePull(func() (string, error) { return d.rawEnumerate(classSubscription) },
		func(ctx string) ([]subscriptionRef, string, error) {
			out, err := d.rawPull(classSubscription, ctx)
			if err != nil {
				return nil, "", err
			}
			refs, err := parseSubscriptions(out)
			return refs, out, err
		})
}

// parseSubscriptions reads Filter and Handler (endpoint references) of every subscription.
func parseSubscriptions(xmlOutput string) ([]subscriptionRef, error) {
	type eprXML struct {
		Selectors []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"ReferenceParameters>SelectorSet>Selector"`
	}
	sel := func(e eprXML, name string) string {
		for _, s := range e.Selectors {
			if s.Name == name {
				return strings.TrimSpace(s.Value)
			}
		}
		return ""
	}
	dec := xml.NewDecoder(strings.NewReader(xmlOutput))
	var out []subscriptionRef
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, classSubscription, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != classSubscription {
			continue
		}
		var x struct {
			Filter  eprXML `xml:"Filter"`
			Handler eprXML `xml:"Handler"`
		}
		if err := dec.DecodeElement(&x, &start); err != nil {
			return nil, fmt.Errorf("%w: unreadable %s response: %v", ErrIncompleteResponse, classSubscription, err)
		}
		r := subscriptionRef{filter: sel(x.Filter, "InstanceID"), listener: sel(x.Handler, "Name")}
		if r.filter != "" && r.listener != "" {
			out = append(out, r)
		}
	}
}

// enumerateRawClass enumerates class (its resource URI) and returns the instances named class
// or one of also (subclasses AMT returns for it).
func (d *Device) enumerateRawClass(class string, also ...string) ([]*cimObject, error) {
	return enumeratePull(func() (string, error) { return d.rawEnumerate(class) },
		func(ctx string) ([]*cimObject, string, error) {
			out, err := d.rawPull(class, ctx)
			if err != nil {
				return nil, "", err
			}
			var objs []*cimObject
			for _, c := range append([]string{class}, also...) {
				o, err := parseCIMObjects(out, c)
				if err != nil {
					return nil, "", err
				}
				objs = append(objs, o...)
			}
			return objs, out, nil
		})
}

// rawEnumerate starts an enumeration of class and returns its context.
func (d *Device) rawEnumerate(class string) (string, error) {
	out, err := d.msgs.Client.Post(envelope(actionEnumerateRaw, resourceURI(class), nil,
		`<Enumerate xmlns="http://schemas.xmlsoap.org/ws/2004/09/enumeration" />`))
	if err != nil {
		return "", err
	}
	var env struct {
		Ctx string `xml:"Body>EnumerateResponse>EnumerationContext"`
	}
	if err := xml.Unmarshal(out, &env); err != nil {
		return "", fmt.Errorf("%w: unreadable enumerate response: %v", ErrIncompleteResponse, err)
	}
	return strings.TrimSpace(env.Ctx), nil
}

// rawPull pulls the next items of an enumeration of class.
func (d *Device) rawPull(class, ctx string) (string, error) {
	var b strings.Builder
	b.WriteString(`<Pull xmlns="http://schemas.xmlsoap.org/ws/2004/09/enumeration"><EnumerationContext>`)
	_ = xml.EscapeText(&b, []byte(ctx))
	b.WriteString(`</EnumerationContext><MaxElements>999</MaxElements><MaxCharacters>99999</MaxCharacters></Pull>`)
	out, err := d.msgs.Client.Post(envelope(actionPullRaw, resourceURI(class), nil, b.String()))
	return string(out), err
}

// TargetSpec is a new forwarding target.
type TargetSpec struct {
	Address  string
	Delivery Delivery
	FilterID string
	// User and Password sign AMT in at the target with HTTP digest; both empty for none.
	User, Password string
}

// ErrTargetAddress: the address is no http:// or https:// URL with a host.
var ErrTargetAddress = errors.New("the address must start with http:// or https://")

// Validate checks the address (http:// or https://, a host, at most 253 characters), the
// delivery, the filter and the sign-in (both fields or none, at most 32 characters each).
func (s TargetSpec) Validate() error {
	if !ValidTargetAddress(s.Address) {
		return ErrTargetAddress
	}
	if s.Delivery != DeliveryPush && s.Delivery != DeliveryPushWithAck {
		return invalid("Delivery", "Push or PushWithAck")
	}
	if strings.TrimSpace(s.FilterID) == "" {
		return invalid("FilterID", "empty")
	}
	if (s.User == "") != (s.Password == "") {
		return invalid("User", "user name and password together")
	}
	if len(s.User) > MaxTargetCredential || len(s.Password) > MaxTargetCredential {
		return invalid("User", "at most 32 characters")
	}
	return nil
}

// ValidTargetAddress reports whether a is an http:// or https:// URL with a host and at most
// MaxTargetAddress characters.
func ValidTargetAddress(a string) bool {
	if a != strings.TrimSpace(a) || len(a) > MaxTargetAddress {
		return false
	}
	u, err := url.Parse(a)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		(strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://"))
}

// AddForwardingTarget subscribes the address to the filter (WS-Eventing Subscribe on
// CIM_FilterCollection). A refusal comes as the device's SOAP fault (*amterror.AMTError).
func (d *Device) AddForwardingTarget(s TargetSpec) error {
	const op = "add forwarding target"
	if err := s.Validate(); err != nil {
		return err
	}
	mode := modePush
	if s.Delivery == DeliveryPushWithAck {
		mode = modePushWithAck
	}
	var header, auth string
	if s.User != "" {
		var b strings.Builder
		b.WriteString(`<t:IssuedTokens xmlns:t="http://schemas.xmlsoap.org/ws/2005/02/trust" xmlns:se="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">`)
		b.WriteString(`<t:RequestSecurityTokenResponse><t:TokenType>http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#UsernameToken</t:TokenType>`)
		b.WriteString(`<t:RequestedSecurityToken><se:UsernameToken><se:Username>`)
		_ = xml.EscapeText(&b, []byte(s.User))
		b.WriteString(`</se:Username><se:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd#PasswordText">`)
		_ = xml.EscapeText(&b, []byte(s.Password))
		b.WriteString(`</se:Password></se:UsernameToken></t:RequestedSecurityToken></t:RequestSecurityTokenResponse></t:IssuedTokens>`)
		header = b.String()
		auth = `<w:Auth Profile="` + digestProfile + `"/>`
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<e:Subscribe xmlns:e="%s"><e:Delivery Mode="%s"><e:NotifyTo><a:Address>`, eventingNS, mode)
	_ = xml.EscapeText(&body, []byte(s.Address))
	body.WriteString(`</a:Address></e:NotifyTo>` + auth + `</e:Delivery></e:Subscribe>`)
	msg := withHeader(envelope(actionSubscribe, resourceURI(classFilterCollection),
		[]selector{{"InstanceID", s.FilterID}}, body.String()), header)
	out, err := d.msgs.Client.Post(msg)
	if err != nil {
		return writeError(op, err)
	}
	var res struct {
		Resp *struct{} `xml:"Body>SubscribeResponse"`
	}
	if xml.Unmarshal(out, &res) != nil || res.Resp == nil {
		return fmt.Errorf("%s: %w: SubscribeResponse", op, ErrIncompleteResponse)
	}
	return nil
}

// DeleteForwardingTarget ends the subscription of t (WS-Eventing Unsubscribe on
// CIM_FilterCollectionSubscription, selected by its filter and its listener). It needs the
// simple confirmation of the UI (OpSubscriptionDelete) and no Confirmation.
func (d *Device) DeleteForwardingTarget(t ForwardingTarget) error {
	const op = "delete forwarding target"
	if t.FilterID == "" || t.Listener == "" {
		return invalid("Target", "filter and listener needed")
	}
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	filter := `<a:EndpointReference><a:Address>` + anonymousAddress + `</a:Address><a:ReferenceParameters><w:ResourceURI>` +
		resourceURI(classFilterCollection) + `</w:ResourceURI><w:SelectorSet><w:Selector Name="InstanceID">` + esc(t.FilterID) +
		`</w:Selector></w:SelectorSet></a:ReferenceParameters></a:EndpointReference>`
	handler := `<a:EndpointReference><a:Address>` + anonymousAddress + `</a:Address><a:ReferenceParameters><w:ResourceURI>` +
		resourceURI(classListenerWSMan) + `</w:ResourceURI><w:SelectorSet><w:Selector Name="CreationClassName">CIM_ListenerDestinationWSMAN</w:Selector>` +
		`<w:Selector Name="Name">` + esc(t.Listener) + `</w:Selector><w:Selector Name="SystemCreationClassName">CIM_ComputerSystem</w:Selector>` +
		`<w:Selector Name="SystemName">Intel(r) AMT</w:Selector></w:SelectorSet></a:ReferenceParameters></a:EndpointReference>`
	sels := `<w:SelectorSet><w:Selector Name="Filter">` + filter + `</w:Selector><w:Selector Name="Handler">` + handler + `</w:Selector></w:SelectorSet>`
	msg := withHeader(envelope(actionUnsubscribe, resourceURI(classSubscription), nil, `<e:Unsubscribe xmlns:e="`+eventingNS+`"/>`), sels)
	if _, err := d.msgs.Client.Post(msg); err != nil {
		return writeError(op, err)
	}
	return nil
}

// withHeader inserts extra header elements before the end of the header of msg.
func withHeader(msg, extra string) string {
	if extra == "" {
		return msg
	}
	return strings.Replace(msg, "</Header>", extra+"</Header>", 1)
}
