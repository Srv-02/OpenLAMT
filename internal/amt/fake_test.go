package amt

import (
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// routeKey returns path.Base(Action) + "|" + path.Base(ResourceURI) of a WS-Man request,
// e.g. "Enumerate|CIM_SoftwareIdentity".
func routeKey(request string) string {
	var env struct {
		Action      string `xml:"Header>Action"`
		ResourceURI string `xml:"Header>ResourceURI"`
	}
	if err := xml.Unmarshal([]byte(request), &env); err != nil {
		return "unparsable"
	}
	return path.Base(env.Action) + "|" + path.Base(env.ResourceURI)
}

// fakeWSMan is a client.WSMan serving canned responses from testdata/.
type fakeWSMan struct {
	routes   map[string]string // routeKey -> file name under testdata/
	requests []string          // every Post body, in order, appended before the route lookup
}

func (f *fakeWSMan) Post(msg string) ([]byte, error) {
	f.requests = append(f.requests, msg)
	key := routeKey(msg)
	name, ok := f.routes[key]
	if !ok {
		return nil, fmt.Errorf("fake: no route for %q", key)
	}
	return os.ReadFile(filepath.Join("testdata", name))
}

func (f *fakeWSMan) Connect() error                                  { return nil }
func (f *fakeWSMan) Send(data []byte) error                          { return nil }
func (f *fakeWSMan) Receive() ([]byte, error)                        { return nil, nil }
func (f *fakeWSMan) CloseConnection() error                          { return nil }
func (f *fakeWSMan) IsAuthenticated() bool                           { return false }
func (f *fakeWSMan) GetServerCertificate() (*tls.Certificate, error) { return nil, nil }

func statusRoutes() map[string]string {
	return map[string]string{
		"Enumerate|CIM_ServiceAvailableToElement": "enumerate.xml",
		"Pull|CIM_ServiceAvailableToElement":      "power_state_pull.xml",
		"Enumerate|CIM_SoftwareIdentity":          "enumerate.xml",
		"Pull|CIM_SoftwareIdentity":               "software_pull.xml",
		"Get|AMT_SetupAndConfigurationService":    "setup_get.xml",
	}
}
