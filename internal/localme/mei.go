//go:build linux || windows

package localme

import (
	"io"
	"time"

	"github.com/device-management-toolkit/rpc-go/v2/pkg/amt"
	"github.com/sirupsen/logrus"
)

// The Intel ME is read with the pkg/amt package of Intel's rpc-go (Apache-2.0), version
// v3.0.0-beta.86. That tag keeps the module path rpc-go/v2, so go.mod pins it by the
// pseudo-version of the tag's commit. pkg/amt talks to the AMT host interface (PTHI) of the
// Intel ME driver directly: no LMS, no network. Only its read methods are called, see reader.

// versionTimeout bounds rpc-go's retries when the Intel ME is busy.
const versionTimeout = 10 * time.Second

// meiReader is the reader over rpc-go.
type meiReader struct{ c amt.AMTCommand }

func newReader() reader {
	logrus.SetOutput(io.Discard) // rpc-go logs to stderr; the helper's only output is its answer
	return meiReader{c: amt.NewAMTCommand()}
}

func (m meiReader) Open() error { return m.c.Initialize() }

func (m meiReader) Version() (string, error) { return m.c.GetVersionDataFromME("AMT", versionTimeout) }

func (m meiReader) OperationalState() (bool, bool, error) {
	r, err := m.c.GetChangeEnabled()
	if err != nil {
		return false, false, err
	}
	return r.IsNewInterfaceVersion(), r.IsAMTEnabled(), nil
}

func (m meiReader) ControlMode() (int, error)       { return m.c.GetControlMode() }
func (m meiReader) ProvisioningState() (int, error) { return m.c.GetProvisioningState() }
func (m meiReader) UUID() (string, error)           { return m.c.GetUUID() }
func (m meiReader) DNSSuffix() (string, error)      { return m.c.GetDNSSuffix() }

func (m meiReader) LAN(wireless bool) (Link, error) {
	s, err := m.c.GetLANInterfaceSettings(wireless)
	if err != nil {
		return Link{}, err
	}
	l := Link{Enabled: s.IsEnabled, LinkUp: s.LinkStatus == "up", DHCP: s.DHCPEnabled, MAC: s.MACAddress}
	if s.IPAddress != "0.0.0.0" {
		l.IP = s.IPAddress
	}
	return l, nil
}
