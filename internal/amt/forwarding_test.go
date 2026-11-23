package amt

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

func forwardingSeq() map[string][]cfgResp {
	seq := auditSeq()
	seq["Pull|CIM_FilterCollection"] = []cfgResp{{file: "fwd_filter_pull.xml"}}
	seq["Pull|CIM_ListenerDestination"] = []cfgResp{{file: "fwd_listener_pull.xml"}}
	seq["Pull|CIM_FilterCollectionSubscription"] = []cfgResp{{file: "fwd_subscription_pull.xml"}}
	seq["Subscribe|CIM_FilterCollection"] = []cfgResp{{file: "fwd_subscribe_ok.xml"}}
	seq["Unsubscribe|CIM_FilterCollectionSubscription"] = []cfgResp{{file: "fwd_unsubscribe_ok.xml"}}
	return seq
}

// The subscriptions are joined with their listener by name, not by position; a subscription
// without its listener is left out, an unknown filter keeps its id without the prefix.
func TestEventForwardingJoinsSubscriptionsAndListeners(t *testing.T) {
	d, _ := cfgDevice(forwardingSeq())
	got, err := d.EventForwarding()
	if err != nil {
		t.Fatal(err)
	}
	want := Forwarding{
		Filters: []EventFilter{
			{"Intel(r) AMT:All", "All"}, {"Intel(r) AMT:FW ProgressEvents", "FW ProgressEvents"},
			{"Intel(r) AMT:User", "User"}, {"Intel(r) AMT:Platform", "Platform"}, {"Intel(r) AMT:Features", "Features"},
		},
		Targets: []ForwardingTarget{
			{Address: "http://192.0.2.5:16997/events", Delivery: DeliveryPush, FilterID: "Intel(r) AMT:All", FilterName: "All", Listener: "Listener Destination 0"},
			{Address: "https://siem.example.com/amt?a=1&b=2", Delivery: DeliveryPushWithAck, FilterID: "Intel(r) AMT:Hidden", FilterName: "Hidden", Listener: "Listener Destination 1"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestEventForwardingEmpty(t *testing.T) {
	seq := forwardingSeq()
	seq["Pull|CIM_ListenerDestination"] = []cfgResp{{file: "fwd_empty_pull.xml"}}
	seq["Pull|CIM_FilterCollectionSubscription"] = []cfgResp{{file: "fwd_empty_pull.xml"}}
	d, _ := cfgDevice(seq)
	got, err := d.EventForwarding()
	if err != nil || len(got.Targets) != 0 || len(got.Filters) != 5 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestEventForwardingErrors(t *testing.T) {
	for _, route := range []string{"Enumerate|CIM_FilterCollection", "Enumerate|CIM_ListenerDestination", "Enumerate|CIM_FilterCollectionSubscription"} {
		seq := forwardingSeq()
		seq[route] = []cfgResp{{err: amterror.NewAMTError("b:AccessDenied", "x", "")}}
		d, _ := cfgDevice(seq)
		if _, err := d.EventForwarding(); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("%s: %v", route, err)
		}
	}
	seq := forwardingSeq()
	seq["Pull|CIM_FilterCollectionSubscription"] = []cfgResp{{body: "<not xml"}}
	d, _ := cfgDevice(seq)
	if _, err := d.EventForwarding(); !errors.Is(err, ErrIncompleteResponse) {
		t.Errorf("broken pull: %v", err)
	}
}

func TestTargetSpecValidate(t *testing.T) {
	ok := TargetSpec{Address: "http://192.0.2.5:16997/events", Delivery: DeliveryPush, FilterID: "Intel(r) AMT:All"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"", "192.0.2.5", "ftp://example.com", "http://", " http://example.com", "http:/example.com",
		"http://example.com/" + strings.Repeat("a", 240)} {
		c := ok
		c.Address = s
		if err := c.Validate(); !errors.Is(err, ErrTargetAddress) {
			t.Errorf("%q: %v", s, err)
		}
	}
	for name, mut := range map[string]func(*TargetSpec){
		"delivery": func(s *TargetSpec) { s.Delivery = 4 },
		"filter":   func(s *TargetSpec) { s.FilterID = " " },
		"user":     func(s *TargetSpec) { s.User = "u" },
		"password": func(s *TargetSpec) { s.Password = "p" },
		"long":     func(s *TargetSpec) { s.User, s.Password = strings.Repeat("u", 33), "p" },
	} {
		c := ok
		mut(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c := ok
	c.Address = "https://siem.example.com/amt"
	if err := c.Validate(); err != nil {
		t.Errorf("https: %v", err)
	}
}

func TestAddForwardingTargetSubscribes(t *testing.T) {
	d, f := cfgDevice(forwardingSeq())
	err := d.AddForwardingTarget(TargetSpec{Address: "http://192.0.2.5:16997/events?a=1&b=2", Delivery: DeliveryPush, FilterID: "Intel(r) AMT:All"})
	if err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Subscribe|CIM_FilterCollection")
	assertContains(t, body,
		"<a:Action>http://schemas.xmlsoap.org/ws/2004/08/eventing/Subscribe</a:Action>",
		"<w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_FilterCollection</w:ResourceURI>",
		`<w:SelectorSet><w:Selector Name="InstanceID">Intel(r) AMT:All</w:Selector></w:SelectorSet></Header>`,
		`<e:Subscribe xmlns:e="http://schemas.xmlsoap.org/ws/2004/08/eventing"><e:Delivery Mode="http://schemas.xmlsoap.org/ws/2004/08/eventing/DeliveryModes/Push">`+
			`<e:NotifyTo><a:Address>http://192.0.2.5:16997/events?a=1&amp;b=2</a:Address></e:NotifyTo></e:Delivery></e:Subscribe>`)
	assertLacks(t, body, "IssuedTokens", "w:Auth")
}

func TestAddForwardingTargetWithAckAndDigest(t *testing.T) {
	d, f := cfgDevice(forwardingSeq())
	err := d.AddForwardingTarget(TargetSpec{Address: "https://siem.example.com/amt", Delivery: DeliveryPushWithAck,
		FilterID: "Intel(r) AMT:User", User: "collector", Password: "p<w>&d"})
	if err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Subscribe|CIM_FilterCollection")
	head, _, _ := strings.Cut(body, "</Header>")
	assertContains(t, head, `<w:Selector Name="InstanceID">Intel(r) AMT:User</w:Selector>`,
		`<t:IssuedTokens xmlns:t="http://schemas.xmlsoap.org/ws/2005/02/trust"`,
		"<se:Username>collector</se:Username>",
		`#PasswordText">p&lt;w&gt;&amp;d</se:Password>`)
	assertContains(t, body, `<e:Delivery Mode="http://schemas.dmtf.org/wbem/wsman/1/wsman/PushWithAck">`,
		`</e:NotifyTo><w:Auth Profile="http://schemas.dmtf.org/wbem/wsman/1/wsman/secprofile/http/digest"/></e:Delivery>`)
}

func TestAddForwardingTargetRefusals(t *testing.T) {
	d, f := cfgDevice(forwardingSeq())
	if err := d.AddForwardingTarget(TargetSpec{Address: "example.com", Delivery: DeliveryPush, FilterID: "x"}); !errors.Is(err, ErrTargetAddress) || len(f.requests) != 0 {
		t.Fatalf("invalid: %v", err)
	}
	seq := forwardingSeq()
	fault := amterror.NewAMTError("e:DeliveryModeRequestedUnavailable", "The requested delivery mode is not supported.", "")
	seq["Subscribe|CIM_FilterCollection"] = []cfgResp{{err: fault}}
	d, _ = cfgDevice(seq)
	var ae *amterror.AMTError
	if err := d.AddForwardingTarget(TargetSpec{Address: "http://example.com", Delivery: DeliveryPush, FilterID: "x"}); !errors.As(err, &ae) || ae.Message != fault.Message {
		t.Fatalf("fault: %v", err)
	}
	seq["Subscribe|CIM_FilterCollection"] = []cfgResp{{file: "fwd_unsubscribe_ok.xml"}}
	d, _ = cfgDevice(seq)
	if err := d.AddForwardingTarget(TargetSpec{Address: "http://example.com", Delivery: DeliveryPush, FilterID: "x"}); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("no SubscribeResponse: %v", err)
	}
	seq["Subscribe|CIM_FilterCollection"] = []cfgResp{{err: amterror.NewAMTError("b:AccessDenied", "x", "")}}
	d, _ = cfgDevice(seq)
	if err := d.AddForwardingTarget(TargetSpec{Address: "http://example.com", Delivery: DeliveryPush, FilterID: "x"}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied: %v", err)
	}
}

func TestDeleteForwardingTargetUnsubscribesByFilterAndListener(t *testing.T) {
	d, f := cfgDevice(forwardingSeq())
	if err := d.DeleteForwardingTarget(ForwardingTarget{Address: "http://192.0.2.5", FilterID: "Intel(r) AMT:All", Listener: "Listener Destination 0"}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Unsubscribe|CIM_FilterCollectionSubscription")
	assertContains(t, body,
		"<a:Action>http://schemas.xmlsoap.org/ws/2004/08/eventing/Unsubscribe</a:Action>",
		"<w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_FilterCollectionSubscription</w:ResourceURI>",
		`<w:Selector Name="Filter"><a:EndpointReference><a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>`+
			`<a:ReferenceParameters><w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_FilterCollection</w:ResourceURI>`+
			`<w:SelectorSet><w:Selector Name="InstanceID">Intel(r) AMT:All</w:Selector></w:SelectorSet></a:ReferenceParameters></a:EndpointReference></w:Selector>`,
		`<w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ListenerDestinationWSManagement</w:ResourceURI>`,
		`<w:Selector Name="CreationClassName">CIM_ListenerDestinationWSMAN</w:Selector><w:Selector Name="Name">Listener Destination 0</w:Selector>`,
		`<w:Selector Name="SystemName">Intel(r) AMT</w:Selector>`,
		`<Body><e:Unsubscribe xmlns:e="http://schemas.xmlsoap.org/ws/2004/08/eventing"/></Body>`)

	if err := d.DeleteForwardingTarget(ForwardingTarget{FilterID: "x"}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("no listener: %v", err)
	}
	seq := forwardingSeq()
	seq["Unsubscribe|CIM_FilterCollectionSubscription"] = []cfgResp{{err: amterror.NewAMTError("b:AccessDenied", "x", "")}}
	d, _ = cfgDevice(seq)
	if err := d.DeleteForwardingTarget(ForwardingTarget{FilterID: "x", Listener: "y"}); !errors.Is(err, ErrAccessDenied) {
		t.Errorf("denied: %v", err)
	}
}
