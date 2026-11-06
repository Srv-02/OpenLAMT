package amt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Srv-02/openlamt/internal/kvm"
	redirection "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/redirection"
	cimkvm "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/kvm"
	optin "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/ips/optin"
)

const redirectionServiceNS = "http://intel.com/wbem/wscim/1/amt-schema/1/AMT_RedirectionService"

// ConsentPrompt asks the user for the consent code AMT shows on the device screen (CCM).
type ConsentPrompt interface{ ConsentCode() (string, error) }

var (
	ErrConsentRequired    = errors.New("user consent required but no prompt available")
	ErrInvalidConsentCode = errors.New("consent code must be six digits")
)

// ConsentError is returned when the device rejects a consent step. Step is "StartOptIn" or "SendOptInCode".
type ConsentError struct {
	Step        string
	ReturnValue int
}

func (e *ConsentError) Error() string {
	return fmt.Sprintf("user consent %s failed: device returned ReturnValue %d", e.Step, e.ReturnValue)
}

// KVMError is returned when the device rejects a KVM activation step.
type KVMError struct {
	Step        string
	ReturnValue int
}

func (e *KVMError) Error() string {
	return fmt.Sprintf("kvm %s failed: device returned ReturnValue %d", e.Step, e.ReturnValue)
}

// EnableKVM makes the redirection listener and the KVM SAP available, writing only when needed.
func (d *Device) EnableKVM() error {
	rs, err := d.msgs.AMT.RedirectionService.Get()
	if err != nil {
		return fmt.Errorf("get redirection service: %w", err)
	}
	cur := rs.Body.GetAndPutResponse
	if cur.Name == "" {
		return fmt.Errorf("get redirection service: %w: AMT_RedirectionService", ErrIncompleteResponse)
	}
	if !cur.ListenerEnabled {
		_, err := d.msgs.AMT.RedirectionService.Put(&redirection.RedirectionRequest{
			H:                       redirectionServiceNS,
			CreationClassName:       cur.CreationClassName,
			ElementName:             cur.ElementName,
			EnabledState:            cur.EnabledState,
			ListenerEnabled:         true,
			Name:                    cur.Name,
			SystemCreationClassName: cur.SystemCreationClassName,
			SystemName:              cur.SystemName,
		})
		if err != nil {
			return fmt.Errorf("enable redirection listener: %w", err)
		}
	}

	sap, err := d.msgs.CIM.KVMRedirectionSAP.Get()
	if err != nil {
		return fmt.Errorf("get kvm redirection sap: %w", err)
	}
	if sap.Body.GetResponse.CreationClassName == "" {
		return fmt.Errorf("get kvm redirection sap: %w: CIM_KVMRedirectionSAP", ErrIncompleteResponse)
	}
	switch sap.Body.GetResponse.EnabledState {
	case cimkvm.EnabledStateEnabled, cimkvm.EnabledStateEnabledButOffline:
		return nil
	}
	resp, err := d.msgs.CIM.KVMRedirectionSAP.RequestStateChange(cimkvm.RedirectionSAPEnable)
	if err != nil {
		return fmt.Errorf("enable kvm redirection sap: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "RequestStateChange_OUTPUT") {
		return fmt.Errorf("enable kvm redirection sap: %w: RequestStateChange_OUTPUT", ErrIncompleteResponse)
	}
	if rv := int(resp.Body.RequestStateChange_OUTPUT.ReturnValue); rv != 0 {
		return &KVMError{Step: "KVMRedirectionSAP.RequestStateChange", ReturnValue: rv}
	}
	return nil
}

// UserConsent reads IPS_OptInService. With OptInRequired 0 or OptInState 3/4 it returns nil.
// Otherwise it starts opt-in when needed, asks the prompt and sends the six digit code.
func (d *Device) UserConsent(prompt ConsentPrompt) error {
	svc, err := d.msgs.IPS.OptInService.Get()
	if err != nil {
		return fmt.Errorf("get opt-in service: %w", err)
	}
	cur := svc.Body.GetAndPutResponse
	if cur.Name == "" {
		return fmt.Errorf("get opt-in service: %w: IPS_OptInService", ErrIncompleteResponse)
	}
	if cur.OptInRequired == uint32(optin.OptInRequiredNone) ||
		cur.OptInState == int(optin.Received) || cur.OptInState == int(optin.InSession) {
		return nil
	}
	if prompt == nil {
		return ErrConsentRequired
	}
	if cur.OptInState == int(optin.NotStarted) {
		resp, err := d.msgs.IPS.OptInService.StartOptIn()
		if err != nil {
			return fmt.Errorf("start opt-in: %w", err)
		}
		if resp.Message == nil || !strings.Contains(resp.XMLOutput, "StartOptIn_OUTPUT") {
			return fmt.Errorf("start opt-in: %w: StartOptIn_OUTPUT", ErrIncompleteResponse)
		}
		if rv := resp.Body.StartOptInResponse.ReturnValue; rv != 0 {
			return &ConsentError{Step: "StartOptIn", ReturnValue: rv}
		}
	}
	raw, err := prompt.ConsentCode()
	if err != nil {
		return fmt.Errorf("read consent code: %w", err)
	}
	code, err := parseConsentCode(raw)
	if err != nil {
		return err
	}
	resp, err := d.msgs.IPS.OptInService.SendOptInCode(code)
	if err != nil {
		return fmt.Errorf("send opt-in code: %w", err)
	}
	if resp.Message == nil || !strings.Contains(resp.XMLOutput, "SendOptInCode_OUTPUT") {
		return fmt.Errorf("send opt-in code: %w: SendOptInCode_OUTPUT", ErrIncompleteResponse)
	}
	if rv := resp.Body.SendOptInCodeResponse.ReturnValue; rv != 0 {
		return &ConsentError{Step: "SendOptInCode", ReturnValue: rv}
	}
	return nil
}

// parseConsentCode trims whitespace and requires exactly six ASCII digits.
func parseConsentCode(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) != 6 {
		return 0, ErrInvalidConsentCode
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalidConsentCode
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, ErrInvalidConsentCode
	}
	return n, nil
}

// OpenKVM runs EnableKVM, UserConsent and kvm.Dial on RedirectionAddress with the pinned TLS config
// and the AMT version of the last Status (session.Open reads it before any KVM connect).
func (d *Device) OpenKVM(ctx context.Context, quality kvm.Quality, prompt ConsentPrompt) (*kvm.Client, error) {
	if err := d.EnableKVM(); err != nil {
		return nil, err
	}
	if err := d.UserConsent(prompt); err != nil {
		return nil, err
	}
	if d.cfg.UseTLS && d.tlsCfg == nil {
		// Without the pinned config kvm.Dial would connect in plain text.
		return nil, errors.New("kvm: TLS is enabled but the device has no pinned TLS config")
	}
	return kvm.Dial(ctx, kvm.Config{
		Addr:        d.cfg.RedirectionAddress(),
		Username:    d.cfg.Username,
		Password:    d.cfg.Password,
		Quality:     quality,
		AMTMajor:    int(d.amtMajor.Load()),
		TLS:         d.tlsCfg,
		DialContext: d.cfg.dialContext,
	})
}
