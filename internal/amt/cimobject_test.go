package amt

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const objXML = `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_Example" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><a:Header/><a:Body><g:AMT_Example><g:Name>a &amp; b</g:Name><g:Flag>false</g:Flag><g:List>one</g:List><g:List>two</g:List><g:Empty xsi:nil="true"/><g:Num>42</g:Num></g:AMT_Example></a:Body></a:Envelope>`

func TestParseCIMObjectKeepsOrderAndText(t *testing.T) {
	objs, err := parseCIMObjects(objXML, "AMT_Example")
	if err != nil || len(objs) != 1 {
		t.Fatalf("parse: %v, %d objects", err, len(objs))
	}
	o := objs[0]
	if got := o.str("Name"); got != "a & b" {
		t.Errorf("Name = %q", got)
	}
	if o.boolean("Flag") || !o.has("Flag") {
		t.Errorf("Flag: has %v, value %v", o.has("Flag"), o.boolean("Flag"))
	}
	if got := o.strs("List"); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("List = %v", got)
	}
	if got := o.integer("Num"); got != 42 {
		t.Errorf("Num = %d", got)
	}
	if o.has("Missing") || o.str("Missing") != "" || o.integer("Missing") != 0 {
		t.Error("missing property must read as absent and zero")
	}
}

func TestCIMObjectMarshalWritesEveryPropertyInOrder(t *testing.T) {
	objs, _ := parseCIMObjects(objXML, "AMT_Example")
	o := objs[0]
	o.set("Flag", "true")
	o.set("Added", "x<y")
	got, err := o.marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `<h:AMT_Example xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_Example">` +
		`<h:Name>a &amp; b</h:Name><h:Flag>true</h:Flag><h:List>one</h:List><h:List>two</h:List>` +
		`<h:Num>42</h:Num><h:Added>x&lt;y</h:Added></h:AMT_Example>`
	if got != want {
		t.Errorf("marshal:\n got %s\nwant %s", got, want)
	}
}

func TestCIMObjectSetAllAndDelete(t *testing.T) {
	objs, _ := parseCIMObjects(objXML, "AMT_Example")
	o := objs[0]
	o.setAll("List", []string{"three"})
	o.del("Name")
	o.setAll("New", []string{"n1", "n2"})
	o.setAll("Num", nil)
	got, _ := o.marshal()
	want := `<h:AMT_Example xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_Example">` +
		`<h:Flag>false</h:Flag><h:List>three</h:List><h:New>n1</h:New><h:New>n2</h:New></h:AMT_Example>`
	if got != want {
		t.Errorf("marshal:\n got %s\nwant %s", got, want)
	}
}

func TestCIMObjectNamespaces(t *testing.T) {
	for class, want := range map[string]string{
		"AMT_GeneralSettings":   "http://intel.com/wbem/wscim/1/amt-schema/1/AMT_GeneralSettings",
		"IPS_OptInService":      "http://intel.com/wbem/wscim/1/ips-schema/1/IPS_OptInService",
		"CIM_KVMRedirectionSAP": "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_KVMRedirectionSAP",
	} {
		if got := resourceURI(class); got != want {
			t.Errorf("resourceURI(%s) = %s", class, got)
		}
	}
}

func TestCIMObjectRefusesNestedProperties(t *testing.T) {
	x := `<Envelope><Body><AMT_Example><Ref><Address>x</Address></Ref></AMT_Example></Body></Envelope>`
	objs, err := parseCIMObjects(x, "AMT_Example")
	if err != nil || len(objs) != 1 {
		t.Fatalf("parse: %v", err)
	}
	if _, err := objs[0].marshal(); !errors.Is(err, ErrIncompleteResponse) {
		t.Errorf("marshal of nested property: %v", err)
	}
}

func TestParseCIMObjectsFindsPullItems(t *testing.T) {
	x := `<a:Envelope xmlns:a="s" xmlns:h="n"><a:Body><PullResponse><Items><h:AMT_X><h:InstanceID>0</h:InstanceID></h:AMT_X><h:AMT_X><h:InstanceID>1</h:InstanceID></h:AMT_X></Items></PullResponse></a:Body></a:Envelope>`
	objs, err := parseCIMObjects(x, "AMT_X")
	if err != nil || len(objs) != 2 || objs[1].str("InstanceID") != "1" {
		t.Fatalf("objs = %v, err %v", objs, err)
	}
	if _, err := parseCIMObjects("<broken", "AMT_X"); err == nil {
		t.Error("broken XML must fail")
	}
}

func TestEnvelopeHasActionResourceAndSelectors(t *testing.T) {
	env := envelope(actionPut, resourceURI("AMT_EthernetPortSettings"),
		[]selector{{"InstanceID", "Intel(r) AMT Ethernet Port Settings 0"}}, "<h:X/>")
	for _, want := range []string{
		`<a:Action>http://schemas.xmlsoap.org/ws/2004/09/transfer/Put</a:Action>`,
		`<w:ResourceURI>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_EthernetPortSettings</w:ResourceURI>`,
		`<w:SelectorSet><w:Selector Name="InstanceID">Intel(r) AMT Ethernet Port Settings 0</w:Selector></w:SelectorSet>`,
		`<Body><h:X/></Body>`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf("envelope lacks %s:\n%s", want, env)
		}
	}
	if routeKey(env) != "Put|AMT_EthernetPortSettings" {
		t.Errorf("routeKey = %s", routeKey(env))
	}
}
