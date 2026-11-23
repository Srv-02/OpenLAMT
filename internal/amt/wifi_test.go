package amt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// readFixture returns a file under testdata/.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wifiSeq answers every Wi-Fi read and write with the fixtures: a port that is on in S0 and Sx,
// local profile sync on, UEFI sharing off, the wireless port at 192.0.2.61 with the link up,
// three AMT profiles and one synced from the operating system.
func wifiSeq() map[string][]cfgResp {
	return map[string][]cfgResp{
		"Enumerate|AMT_EthernetPortSettings":                  {{file: "enumerate.xml"}},
		"Pull|AMT_EthernetPortSettings":                       {{file: "wifi_ethernet_pull.xml"}},
		"Enumerate|CIM_WiFiPort":                              {{file: "enumerate.xml"}},
		"Pull|CIM_WiFiPort":                                   {{file: "wifi_port_pull.xml"}},
		"Get|AMT_WiFiPortConfigurationService":                {{file: "wifi_portcfg_get.xml"}},
		"Enumerate|CIM_WiFiEndpointSettings":                  {{file: "enumerate.xml"}},
		"Pull|CIM_WiFiEndpointSettings":                       {{file: "wifi_settings_pull.xml"}},
		"Enumerate|CIM_WiFiEndpointCapabilities":              {{file: "enumerate.xml"}},
		"Pull|CIM_WiFiEndpointCapabilities":                   {{file: "wifi_caps_pull.xml"}},
		"Enumerate|CIM_WiFiEndpoint":                          {{file: "enumerate.xml"}},
		"Pull|CIM_WiFiEndpoint":                               {{file: "wifi_endpoint_pull.xml"}},
		"RequestStateChange|CIM_WiFiPort":                     {{file: "wifi_rsc_ok.xml"}},
		"Put|AMT_WiFiPortConfigurationService":                {{file: "wifi_portcfg_put.xml"}},
		"AddWiFiSettings|AMT_WiFiPortConfigurationService":    {{file: "wifi_add_ok.xml"}},
		"UpdateWiFiSettings|AMT_WiFiPortConfigurationService": {{file: "wifi_update_ok.xml"}},
		"Delete|CIM_WiFiEndpointSettings":                     {{file: "wifi_delete_ok.xml"}},
	}
}

// stubLookup makes testHost resolve to addrs (nil with err set: the lookup fails).
func stubLookup(t *testing.T, addrs []string, err error) {
	t.Helper()
	old := lookupHost
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host != testHost {
			t.Errorf("lookup of %q", host)
		}
		return addrs, err
	}
	t.Cleanup(func() { lookupHost = old })
}

// wifiDevice is cfgDevice on wifiSeq whose host resolves to the wired address 192.0.2.10.
func wifiDevice(t *testing.T) (*Device, *cfgWSMan, map[string][]cfgResp) {
	t.Helper()
	stubLookup(t, []string{"192.0.2.10"}, nil)
	seq := wifiSeq()
	d, f := cfgDevice(seq)
	return d, f, seq
}

// overWiFi makes the session reach the device through its wireless address.
func overWiFi(t *testing.T) { stubLookup(t, []string{"192.0.2.61"}, nil) }

func TestWiFiReadsStateProfilesAndConnection(t *testing.T) {
	d, f, _ := wifiDevice(t)
	got, err := d.WiFi()
	if err != nil {
		t.Fatal(err)
	}
	want := WiFiSettings{
		Available: true, State: WiFiS0Sx,
		SyncKnown: true, Sync: true, UEFIShareKnown: true, UEFIShare: false,
		LinkUp: true, IPAddress: "192.0.2.61", OverWiFi: false,
		Profiles: []WiFiProfile{
			{InstanceID: "Intel(r) AMT:WiFi Endpoint Settings Office", Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal},
			{InstanceID: "Intel(r) AMT:WiFi Endpoint Settings Workshop", Name: "Workshop", SSID: "ACME-Workshop", Priority: 2, Security: WiFiWPA3Personal},
			{InstanceID: "Intel(r) AMT:WiFi Endpoint Settings Corp", Name: "Corp", SSID: "ACME-Corp", Priority: 3, Security: WiFiSecurityOther},
		},
		// the capabilities list WPA-PSK and WPA2-PSK, not WPA3-SAE
		Securities: []WiFiSecurity{WiFiWPAPersonal, WiFiWPA2Personal},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WiFi() =\n%+v\nwant\n%+v", got, want)
	}
	assertNoWrites(t, f)
}

func TestWiFiOffersAllSecuritiesWithoutCapabilities(t *testing.T) {
	for name, mod := range map[string]func(t *testing.T, seq map[string][]cfgResp){
		"class missing": func(t *testing.T, seq map[string][]cfgResp) {
			seq["Enumerate|CIM_WiFiEndpointCapabilities"] = []cfgResp{{err: unreachableFault(t)}}
		},
		"no personal method listed": func(t *testing.T, seq map[string][]cfgResp) {
			seq["Pull|CIM_WiFiEndpointCapabilities"] = []cfgResp{{body: regexp.MustCompile(`<h:SupportedAuthenticationMethods>[0-9]+</h:SupportedAuthenticationMethods>`).
				ReplaceAllString(readFixture(t, "wifi_caps_pull.xml"), "<h:SupportedAuthenticationMethods>7</h:SupportedAuthenticationMethods>")}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, seq := wifiDevice(t)
			mod(t, seq)
			got, err := d.WiFi()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Securities, WiFiSecurities) {
				t.Errorf("Securities = %v, want %v", got.Securities, WiFiSecurities)
			}
		})
	}
}

// unreachableFault is the DestinationUnreachable fault AMT answers for a class it does not have.
func unreachableFault(t *testing.T) error {
	t.Helper()
	return faultErr(t, "unreachable_fault.xml")
}

func TestWiFiWithoutAdapter(t *testing.T) {
	for name, mod := range map[string]func(t *testing.T, seq map[string][]cfgResp){
		"no port": func(_ *testing.T, seq map[string][]cfgResp) {
			seq["Pull|CIM_WiFiPort"] = []cfgResp{{file: "wifi_port_pull_empty.xml"}}
		},
		"class missing": func(t *testing.T, seq map[string][]cfgResp) {
			seq["Enumerate|CIM_WiFiPort"] = []cfgResp{{err: unreachableFault(t)}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, seq := wifiDevice(t)
			mod(t, seq)
			got, err := d.WiFi()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, WiFiSettings{}) {
				t.Errorf("WiFi() = %+v, want the zero value", got)
			}
		})
	}
}

func TestWiFiOlderFirmware(t *testing.T) {
	d, _, seq := wifiDevice(t)
	seq["Get|AMT_WiFiPortConfigurationService"] = []cfgResp{{body: withProp(t, "wifi_portcfg_get.xml", "UEFIWiFiProfileShareEnabled", "-")}}
	got, err := d.WiFi()
	if err != nil {
		t.Fatal(err)
	}
	if !got.SyncKnown || got.UEFIShareKnown {
		t.Errorf("SyncKnown %v UEFIShareKnown %v, want true false", got.SyncKnown, got.UEFIShareKnown)
	}

	seq["Get|AMT_WiFiPortConfigurationService"] = []cfgResp{{err: unreachableFault(t)}}
	got, err = d.WiFi()
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncKnown || got.UEFIShareKnown || len(got.Profiles) != 3 {
		t.Errorf("without the service: %+v", got)
	}
}

func TestWiFiReadErrors(t *testing.T) {
	t.Run("access denied", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["Enumerate|AMT_EthernetPortSettings"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		if _, err := d.WiFi(); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("err = %v, want ErrAccessDenied", err)
		}
	})
	t.Run("transport error on the profiles", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["Pull|CIM_WiFiEndpointSettings"] = []cfgResp{{err: errors.New("connection reset")}}
		if _, err := d.WiFi(); err == nil || !strings.Contains(err.Error(), "CIM_WiFiEndpointSettings") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("other fault on the port is fatal", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["Pull|CIM_WiFiPort"] = []cfgResp{{err: otherFault(t)}}
		if _, err := d.WiFi(); err == nil {
			t.Error("want an error")
		}
	})
}

func TestWiFiDetectsSessionOverWiFi(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		addrs []string
		err   error
		link  string
		want  bool
	}{
		{name: "host is the wireless address", host: "192.0.2.61", want: true},
		{name: "host is the wired address", host: "192.0.2.10", want: false},
		{name: "name resolves to the wireless address", host: testHost, addrs: []string{"192.0.2.99", "192.0.2.61"}, want: true},
		{name: "name resolves to the wired address", host: testHost, addrs: []string{"192.0.2.10"}, want: false},
		{name: "lookup fails: assume Wi-Fi", host: testHost, err: errors.New("no such host"), want: true},
		{name: "wireless link down", host: "192.0.2.61", link: "false", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubLookup(t, tt.addrs, tt.err)
			seq := wifiSeq()
			if tt.link != "" {
				seq["Pull|AMT_EthernetPortSettings"] = []cfgResp{{body: strings.Replace(readFixture(t, "wifi_ethernet_pull.xml"),
					"<h:LinkIsUp>true</h:LinkIsUp><h:LinkPreference>", "<h:LinkIsUp>"+tt.link+"</h:LinkIsUp><h:LinkPreference>", 1)}}
			}
			d, _ := cfgDevice(seq)
			d.cfg.Host = tt.host
			got, err := d.WiFi()
			if err != nil {
				t.Fatal(err)
			}
			if got.OverWiFi != tt.want {
				t.Errorf("OverWiFi = %v, want %v", got.OverWiFi, tt.want)
			}
		})
	}
}

func TestSetWiFiState(t *testing.T) {
	t.Run("writes the requested state", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiState(WiFiS0, Confirmation{}); err != nil {
			t.Fatal(err)
		}
		body := onePut(t, f, "RequestStateChange|CIM_WiFiPort")
		assertContains(t, body, "<h:RequestedState>32768</h:RequestedState>")
	})
	t.Run("same state writes nothing", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiState(WiFiS0Sx, Confirmation{}); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, f)
	})
	t.Run("off without Wi-Fi session needs no confirmation", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiState(WiFiOff, Confirmation{}); err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "RequestStateChange|CIM_WiFiPort"), "<h:RequestedState>3</h:RequestedState>")
	})
	t.Run("off over Wi-Fi needs the lockout confirmation", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		overWiFi(t)
		err := d.SetWiFiState(WiFiOff, confirmed(OpWiFiDelete))
		var re *RiskyChangeError
		if !errors.As(err, &re) || re.Op != OpWiFiLockout || !reflect.DeepEqual(re.Fields, []string{RiskWiFi}) {
			t.Fatalf("err = %v, want RiskyChangeError for OpWiFiLockout", err)
		}
		assertNoWrites(t, f)
		if err := d.SetWiFiState(WiFiOff, confirmed(OpWiFiLockout)); err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "RequestStateChange|CIM_WiFiPort"), "<h:RequestedState>3</h:RequestedState>")
	})
	t.Run("on while over Wi-Fi needs no confirmation", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		overWiFi(t)
		if err := d.SetWiFiState(WiFiS0, Confirmation{}); err != nil {
			t.Fatal(err)
		}
		onePut(t, f, "RequestStateChange|CIM_WiFiPort")
	})
	t.Run("refusal", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["RequestStateChange|CIM_WiFiPort"] = []cfgResp{{file: "wifi_rsc_fail.xml"}}
		err := d.SetWiFiState(WiFiS0, Confirmation{})
		var me *MethodError
		if !errors.As(err, &me) || me.Method != "CIM_WiFiPort.RequestStateChange" || me.ReturnValue != 2 {
			t.Errorf("err = %v, want MethodError 2", err)
		}
	})
	t.Run("unknown state", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiState(WiFiState(2), Confirmation{}); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("err = %v, want ErrInvalidValue", err)
		}
		assertNoWrites(t, f)
	})
	t.Run("no adapter", func(t *testing.T) {
		d, f, seq := wifiDevice(t)
		seq["Pull|CIM_WiFiPort"] = []cfgResp{{file: "wifi_port_pull_empty.xml"}}
		if err := d.SetWiFiState(WiFiS0, Confirmation{}); !errors.Is(err, ErrNotModifiable) {
			t.Errorf("err = %v, want ErrNotModifiable", err)
		}
		assertNoWrites(t, f)
	})
}

func TestSetWiFiSync(t *testing.T) {
	t.Run("off writes the complete service with 0", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiSync(false); err != nil {
			t.Fatal(err)
		}
		body := onePut(t, f, "Put|AMT_WiFiPortConfigurationService")
		assertContains(t, body,
			`<h:AMT_WiFiPortConfigurationService xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_WiFiPortConfigurationService">`,
			"<h:localProfileSynchronizationEnabled>0</h:localProfileSynchronizationEnabled>",
			"<h:UEFIWiFiProfileShareEnabled>false</h:UEFIWiFiProfileShareEnabled>",
			"<h:Name>Intel(r) AMT WiFiPort Configuration Service</h:Name>")
		if n := propCount(body); n != 12 {
			t.Errorf("%d properties, want all 12", n)
		}
	})
	t.Run("on when already on writes nothing", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.SetWiFiSync(true); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, f)
	})
	t.Run("unrestricted sync counts as on", func(t *testing.T) {
		d, f, seq := wifiDevice(t)
		seq["Get|AMT_WiFiPortConfigurationService"] = []cfgResp{{body: withProp(t, "wifi_portcfg_get.xml", "localProfileSynchronizationEnabled", "3")}}
		if err := d.SetWiFiSync(true); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, f)
	})
	t.Run("on from off writes 1", func(t *testing.T) {
		d, f, seq := wifiDevice(t)
		seq["Get|AMT_WiFiPortConfigurationService"] = []cfgResp{{body: withProp(t, "wifi_portcfg_get.xml", "localProfileSynchronizationEnabled", "0")}}
		if err := d.SetWiFiSync(true); err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "Put|AMT_WiFiPortConfigurationService"),
			"<h:localProfileSynchronizationEnabled>1</h:localProfileSynchronizationEnabled>")
	})
	t.Run("access denied", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["Put|AMT_WiFiPortConfigurationService"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		if err := d.SetWiFiSync(false); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("err = %v, want ErrAccessDenied", err)
		}
	})
}

func TestSetWiFiUEFIShare(t *testing.T) {
	d, f, seq := wifiDevice(t)
	if err := d.SetWiFiUEFIShare(true); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, f, "Put|AMT_WiFiPortConfigurationService")
	assertContains(t, body, "<h:UEFIWiFiProfileShareEnabled>true</h:UEFIWiFiProfileShareEnabled>",
		"<h:localProfileSynchronizationEnabled>1</h:localProfileSynchronizationEnabled>")

	seq["Get|AMT_WiFiPortConfigurationService"] = []cfgResp{{body: withProp(t, "wifi_portcfg_get.xml", "UEFIWiFiProfileShareEnabled", "-")}}
	f.requests = nil
	if err := d.SetWiFiUEFIShare(true); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("older firmware: err = %v, want ErrNotModifiable", err)
	}
	assertNoWrites(t, f)
}

const testPassphrase = "correct-horse-battery"

func TestAddWiFiProfile(t *testing.T) {
	for _, tt := range []struct {
		sec       WiFiSecurity
		auth, enc string
	}{
		{WiFiWPAPersonal, "4", "3"},
		{WiFiWPA2Personal, "6", "4"},
		{WiFiWPA3Personal, "32768", "4"},
	} {
		t.Run(tt.auth, func(t *testing.T) {
			d, f, _ := wifiDevice(t)
			err := d.AddWiFiProfile(WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab", Priority: 4, Security: tt.sec, Passphrase: testPassphrase})
			if err != nil {
				t.Fatal(err)
			}
			body := onePut(t, f, "AddWiFiSettings|AMT_WiFiPortConfigurationService")
			assertContains(t, body,
				"<q:ElementName>Lab</q:ElementName>",
				"<q:InstanceID>Intel(r) AMT:WiFi Endpoint Settings Lab</q:InstanceID>",
				"<q:AuthenticationMethod>"+tt.auth+"</q:AuthenticationMethod>",
				"<q:EncryptionMethod>"+tt.enc+"</q:EncryptionMethod>",
				"<q:SSID>ACME-Lab</q:SSID>",
				"<q:Priority>4</q:Priority>",
				"<q:PSKPassPhrase>"+testPassphrase+"</q:PSKPassPhrase>",
				`Name="Name">WiFi Endpoint 0</c:Selector>`)
			assertLacks(t, body, "IEEE8021x", "ClientCredential", "CACredential")
		})
	}
	t.Run("refusal is a method error without the passphrase", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["AddWiFiSettings|AMT_WiFiPortConfigurationService"] = []cfgResp{{file: "wifi_add_unsupported.xml"}}
		err := d.AddWiFiProfile(WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab", Priority: 4, Security: WiFiWPA3Personal, Passphrase: testPassphrase})
		var me *MethodError
		if !errors.As(err, &me) || me.Method != "AddWiFiSettings" || me.ReturnValue != 1 {
			t.Fatalf("err = %v, want MethodError 1", err)
		}
		if strings.Contains(err.Error(), testPassphrase) {
			t.Error("the error contains the passphrase")
		}
	})
	t.Run("endpoint name falls back", func(t *testing.T) {
		d, f, seq := wifiDevice(t)
		seq["Enumerate|CIM_WiFiEndpoint"] = []cfgResp{{err: unreachableFault(t)}}
		if err := d.AddWiFiProfile(WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab", Priority: 4, Security: WiFiWPA2Personal, Passphrase: testPassphrase}); err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "AddWiFiSettings|AMT_WiFiPortConfigurationService"), `Name="Name">WiFi Endpoint 0</c:Selector>`)
	})
	t.Run("invalid values write nothing", func(t *testing.T) {
		ok := WiFiProfileSpec{Name: "Lab", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal, Passphrase: testPassphrase}
		for field, mod := range map[string]func(*WiFiProfileSpec){
			"Name":             func(s *WiFiProfileSpec) { s.Name = "" },
			"Name long":        func(s *WiFiProfileSpec) { s.Name = strings.Repeat("n", 33) },
			"Name ctrl":        func(s *WiFiProfileSpec) { s.Name = "a\tb" },
			"SSID":             func(s *WiFiProfileSpec) { s.SSID = "" },
			"SSID long":        func(s *WiFiProfileSpec) { s.SSID = strings.Repeat("s", 33) },
			"Priority":         func(s *WiFiProfileSpec) { s.Priority = 0 },
			"Priority+":        func(s *WiFiProfileSpec) { s.Priority = 256 },
			"Security":         func(s *WiFiProfileSpec) { s.Security = WiFiSecurityOther },
			"Passphrase short": func(s *WiFiProfileSpec) { s.Passphrase = "short" },
			"Passphrase empty": func(s *WiFiProfileSpec) { s.Passphrase = "" },
			"Passphrase long":  func(s *WiFiProfileSpec) { s.Passphrase = strings.Repeat("p", 64) },
			"Passphrase ascii": func(s *WiFiProfileSpec) { s.Passphrase = "pässwört1" },
		} {
			t.Run(field, func(t *testing.T) {
				d, f, _ := wifiDevice(t)
				s := ok
				mod(&s)
				err := d.AddWiFiProfile(s)
				var ve *ValidationError
				if !errors.As(err, &ve) || !strings.HasPrefix(field, ve.Field) {
					t.Fatalf("err = %v, want ValidationError for %s", err, field)
				}
				if s.Passphrase != "" && strings.Contains(err.Error(), s.Passphrase) {
					t.Error("the error contains the passphrase")
				}
				assertNoWrites(t, f)
			})
		}
	})
}

func TestUpdateWiFiProfile(t *testing.T) {
	const office = "Intel(r) AMT:WiFi Endpoint Settings Office"
	t.Run("keeps the passphrase when none is given", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		err := d.UpdateWiFiProfile(office, WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab2", Priority: 5, Security: WiFiWPA3Personal})
		if err != nil {
			t.Fatal(err)
		}
		body := onePut(t, f, "UpdateWiFiSettings|AMT_WiFiPortConfigurationService")
		assertContains(t, body,
			`Name="InstanceID">`+office+`</c:Selector>`,
			"<q:InstanceID>"+office+"</q:InstanceID>",
			"<q:ElementName>Office</q:ElementName>",
			"<q:SSID>ACME-Lab2</q:SSID>",
			"<q:Priority>5</q:Priority>",
			"<q:AuthenticationMethod>32768</q:AuthenticationMethod>",
			"<q:EncryptionMethod>4</q:EncryptionMethod>")
		assertLacks(t, body, "PSKPassPhrase")
	})
	t.Run("sends a new passphrase", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		err := d.UpdateWiFiProfile(office, WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal, Passphrase: testPassphrase})
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "UpdateWiFiSettings|AMT_WiFiPortConfigurationService"), "<q:PSKPassPhrase>"+testPassphrase+"</q:PSKPassPhrase>")
	})
	t.Run("refusal", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["UpdateWiFiSettings|AMT_WiFiPortConfigurationService"] = []cfgResp{{file: "wifi_update_invalid.xml"}}
		err := d.UpdateWiFiProfile(office, WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal, Passphrase: testPassphrase})
		var me *MethodError
		if !errors.As(err, &me) || me.Method != "UpdateWiFiSettings" || me.ReturnValue != 3 {
			t.Fatalf("err = %v, want MethodError 3", err)
		}
		if strings.Contains(err.Error(), testPassphrase) {
			t.Error("the error contains the passphrase")
		}
	})
	t.Run("keeps a name another tool gave the profile", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.UpdateWiFiProfile(office, WiFiProfileSpec{Name: "Büro", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal}); err != nil {
			t.Fatal(err)
		}
		assertContains(t, onePut(t, f, "UpdateWiFiSettings|AMT_WiFiPortConfigurationService"), "<q:ElementName>Büro</q:ElementName>")
	})
	t.Run("invalid values write nothing", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		err := d.UpdateWiFiProfile(office, WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal, Passphrase: "short"})
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("err = %v, want ErrInvalidValue", err)
		}
		if err := d.UpdateWiFiProfile("", WiFiProfileSpec{Name: "Office", SSID: "ACME-Lab", Priority: 1, Security: WiFiWPA2Personal}); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("no InstanceID: err = %v, want ErrInvalidValue", err)
		}
		assertNoWrites(t, f)
	})
}

func TestDeleteWiFiProfile(t *testing.T) {
	const id = "Intel(r) AMT:WiFi Endpoint Settings R&D"
	t.Run("deletes by an escaped InstanceID", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.DeleteWiFiProfile(id, Confirmation{}); err != nil {
			t.Fatal(err)
		}
		body := onePut(t, f, "Delete|CIM_WiFiEndpointSettings")
		assertContains(t, body,
			"<a:Action>http://schemas.xmlsoap.org/ws/2004/09/transfer/Delete</a:Action>",
			"<w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_WiFiEndpointSettings</w:ResourceURI>",
			`<w:Selector Name="InstanceID">Intel(r) AMT:WiFi Endpoint Settings R&amp;D</w:Selector>`,
			"<Body></Body>")
	})
	t.Run("over Wi-Fi needs the lockout confirmation", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		overWiFi(t)
		err := d.DeleteWiFiProfile(id, Confirmation{})
		var re *RiskyChangeError
		if !errors.As(err, &re) || re.Op != OpWiFiLockout {
			t.Fatalf("err = %v, want RiskyChangeError for OpWiFiLockout", err)
		}
		assertNoWrites(t, f)
		if err := d.DeleteWiFiProfile(id, confirmed(OpWiFiLockout)); err != nil {
			t.Fatal(err)
		}
		onePut(t, f, "Delete|CIM_WiFiEndpointSettings")
	})
	t.Run("fault", func(t *testing.T) {
		d, _, seq := wifiDevice(t)
		seq["Delete|CIM_WiFiEndpointSettings"] = []cfgResp{{err: faultErr(t, "access_denied_fault.xml")}}
		if err := d.DeleteWiFiProfile(id, Confirmation{}); !errors.Is(err, ErrAccessDenied) {
			t.Errorf("err = %v, want ErrAccessDenied", err)
		}
	})
	t.Run("no InstanceID", func(t *testing.T) {
		d, f, _ := wifiDevice(t)
		if err := d.DeleteWiFiProfile(" ", Confirmation{}); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("err = %v, want ErrInvalidValue", err)
		}
		assertNoWrites(t, f)
	})
}

func TestWiFiLockoutIsRedAndDeleteSimple(t *testing.T) {
	if RiskOf(OpWiFiLockout).Level != LevelRed || RiskOf(OpWiFiDelete).Level != LevelSimple {
		t.Errorf("levels: lockout %v, delete %v", RiskOf(OpWiFiLockout).Level, RiskOf(OpWiFiDelete).Level)
	}
}
