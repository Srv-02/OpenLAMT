package session

import "github.com/Srv-02/openlamt/internal/amt"

// The WSMAN browser (issue #24) as a session capability (capabilities.go). A device without it
// answers ErrUnsupported.

// WSManDevice is what the WSMAN Browser page needs.
type WSManDevice interface {
	BrowseClass(class string) (amt.WSManResult, error)
}

var _ WSManDevice = (*amt.Device)(nil)

// BrowseWSMan reads the instances of one AMT_, CIM_ or IPS_ class (amt.Device.BrowseClass for the
// limits and the errors).
func (s *Session) BrowseWSMan(class string) (amt.WSManResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[WSManDevice](s)
	if err != nil {
		return amt.WSManResult{}, err
	}
	res, err := d.BrowseClass(class)
	return res, classify(err)
}
