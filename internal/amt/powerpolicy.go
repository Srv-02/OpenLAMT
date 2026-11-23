package amt

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Power policy (issue #27): AMT power packages (AMT_SystemPowerScheme), the one in use (the
// IsCurrent flag of CIM_ElementSettingData) and the idle timeout (AMT_GeneralSettings.
// IdleWakeTimeout, minutes).

const (
	classPowerScheme = "AMT_SystemPowerScheme"
	classSettingData = "CIM_ElementSettingData"

	// MinIdleTimeout and MaxIdleTimeout bound AMT_GeneralSettings.IdleWakeTimeout in minutes.
	MinIdleTimeout = 1
	MaxIdleTimeout = 65535
)

// PowerScheme is one power package the device offers.
type PowerScheme struct {
	ID          string // InstanceID, "SCHEME 0"
	GUID        string // SchemeGUID (base64), the second key of the instance
	Description string // "Desktop: ON in S0, ME Wake in S3, S4-5"
	// KeepsRemoteStart is true when AMT stays on in S4-S5, so a switched-off device can be
	// powered on remotely. Read from the description ("S4-5" or "S5"); an unreadable
	// description counts as false, so the UI warns.
	KeepsRemoteStart bool
}

// PowerPolicy is the power packages, the active one and the idle timeout.
type PowerPolicy struct {
	Schemes []PowerScheme
	// Current is the ID of the active package; "" if the device did not tell (the read of
	// CIM_ElementSettingData failed or no entry is marked current).
	Current            string
	IdleTimeoutMinutes int
}

// CurrentScheme returns the active package.
func (p PowerPolicy) CurrentScheme() (PowerScheme, bool) {
	for _, s := range p.Schemes {
		if s.ID == p.Current && s.ID != "" {
			return s, true
		}
	}
	return PowerScheme{}, false
}

// Scheme returns the package with ID id.
func (p PowerPolicy) Scheme(id string) (PowerScheme, bool) {
	for _, s := range p.Schemes {
		if s.ID == id {
			return s, true
		}
	}
	return PowerScheme{}, false
}

// RisksRemoteStart reports whether switching to id turns AMT off in S4-S5 where it is on now
// (or where the active package is unknown): the device can then no longer be powered on
// remotely. This is the operation OpPowerPolicy confirms.
func (p PowerPolicy) RisksRemoteStart(id string) bool {
	to, ok := p.Scheme(id)
	if !ok || to.KeepsRemoteStart {
		return false
	}
	cur, known := p.CurrentScheme()
	return !known || cur.KeepsRemoteStart
}

func keepsRemoteStart(description string) bool {
	d := strings.ToUpper(description)
	return strings.Contains(d, "S4-5") || strings.Contains(d, "S5")
}

// GetPowerPolicy reads the power packages, the active one and the idle timeout. The active
// package is optional: a failing CIM_ElementSettingData read leaves Current empty.
func (d *Device) GetPowerPolicy() (PowerPolicy, error) {
	const op = "read power policy"
	objs, err := pullObjects(classPowerScheme, false, func() (*client.Message, string, error) {
		r, err := d.msgs.AMT.SystemPowerScheme.Enumerate()
		return r.Message, r.Body.EnumerateResponse.EnumerationContext, err
	}, func(ctx string) (*client.Message, error) {
		r, err := d.msgs.AMT.SystemPowerScheme.Pull(ctx)
		return r.Message, err
	})
	if err != nil {
		return PowerPolicy{}, writeError(op, err)
	}
	var p PowerPolicy
	for _, o := range objs {
		desc := o.str("Description")
		p.Schemes = append(p.Schemes, PowerScheme{ID: o.str("InstanceID"), GUID: o.str("SchemeGUID"),
			Description: desc, KeepsRemoteStart: keepsRemoteStart(desc)})
	}
	if cur, err := d.currentSchemeID(); err == nil {
		p.Current = cur
	}
	g, err := d.generalObject()
	if err != nil {
		return PowerPolicy{}, writeError(op, err)
	}
	p.IdleTimeoutMinutes = g.integer("IdleWakeTimeout")
	return p, nil
}

// settingDataXML is one CIM_ElementSettingData: an association whose SettingData reference
// carries the InstanceID of the setting; IsCurrent 1 marks the one in use.
type settingDataXML struct {
	IsCurrent   string `xml:"IsCurrent"`
	SettingData struct {
		ResourceURI string `xml:"ReferenceParameters>ResourceURI"`
		Selectors   []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"ReferenceParameters>SelectorSet>Selector"`
	} `xml:"SettingData"`
}

const (
	enumerationNS = "http://schemas.xmlsoap.org/ws/2004/09/enumeration"
	actionEnum    = enumerationNS + "/Enumerate"
	actionPull    = enumerationNS + "/Pull"
)

// currentSchemeID enumerates CIM_ElementSettingData and returns the InstanceID of the power
// scheme marked current. MeshCommander reads the active package the same way.
func (d *Device) currentSchemeID() (string, error) {
	uri := resourceURI(classSettingData)
	items, err := enumeratePull(func() (string, error) {
		raw, err := d.msgs.Client.Post(envelope(actionEnum, uri, nil,
			`<Enumerate xmlns="`+enumerationNS+`" />`))
		if err != nil {
			return "", err
		}
		var r struct {
			Ctx string `xml:"Body>EnumerateResponse>EnumerationContext"`
		}
		if err := xml.Unmarshal(raw, &r); err != nil {
			return "", fmt.Errorf("%w: unreadable enumerate response: %v", ErrIncompleteResponse, err)
		}
		return r.Ctx, nil
	}, func(ctx string) ([]settingDataXML, string, error) {
		raw, err := d.msgs.Client.Post(envelope(actionPull, uri, nil,
			`<Pull xmlns="`+enumerationNS+`"><EnumerationContext>`+xmlText(ctx)+`</EnumerationContext><MaxElements>32</MaxElements></Pull>`))
		if err != nil {
			return nil, "", err
		}
		var r struct {
			Items []settingDataXML `xml:"Body>PullResponse>Items>CIM_ElementSettingData"`
		}
		if err := xml.Unmarshal(raw, &r); err != nil {
			return nil, "", fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
		}
		return r.Items, string(raw), nil
	})
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if strings.TrimSpace(it.IsCurrent) != "1" || !strings.HasSuffix(strings.TrimSpace(it.SettingData.ResourceURI), classPowerScheme) {
			continue
		}
		for _, s := range it.SettingData.Selectors {
			if s.Name == "InstanceID" {
				return strings.TrimSpace(s.Value), nil
			}
		}
	}
	return "", nil
}

// SetPowerScheme activates the power package id (AMT_SystemPowerScheme.SetPowerScheme on the
// instance with its InstanceID and SchemeGUID keys). A change that turns AMT off in S4-S5
// where it was on (PowerPolicy.RisksRemoteStart) needs confirm for OpPowerPolicy. The device
// may refuse with ReturnValue 38 when its flash write limit is reached.
func (d *Device) SetPowerScheme(id string, confirm Confirmation) error {
	const op = "set power scheme"
	p, err := d.GetPowerPolicy()
	if err != nil {
		return err
	}
	s, ok := p.Scheme(id)
	if !ok {
		return invalid("Scheme", "the device offers no such power package")
	}
	if p.RisksRemoteStart(id) {
		if err := d.confirmed(confirm, OpPowerPolicy, "Scheme"); err != nil {
			return err
		}
	}
	uri := resourceURI(classPowerScheme)
	raw, err := d.msgs.Client.Post(envelope(uri+"/SetPowerScheme", uri,
		[]selector{{"InstanceID", s.ID}, {"SchemeGUID", s.GUID}},
		`<h:SetPowerScheme_INPUT xmlns:h="`+uri+`"></h:SetPowerScheme_INPUT>`))
	if err != nil {
		return writeError(op, err)
	}
	var out struct {
		Output *struct {
			ReturnValue int `xml:"ReturnValue"`
		} `xml:"Body>SetPowerScheme_OUTPUT"`
	}
	if err := xml.Unmarshal(raw, &out); err != nil || out.Output == nil {
		return fmt.Errorf("%s: %w: SetPowerScheme_OUTPUT", op, ErrIncompleteResponse)
	}
	if rv := out.Output.ReturnValue; rv != 0 {
		return &MethodError{Method: "SetPowerScheme", ReturnValue: rv}
	}
	return nil
}

// SetIdleTimeout sets the minutes after which the ME goes to power saving
// (AMT_GeneralSettings.IdleWakeTimeout).
func (d *Device) SetIdleTimeout(minutes int) error {
	return d.SetGeneral(GeneralChange{IdleWakeTimeoutMinutes: &minutes}, Confirmation{})
}
