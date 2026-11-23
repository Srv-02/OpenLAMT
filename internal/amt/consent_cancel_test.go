package amt

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
)

// openCase opens one redirection feature against the fake; enableKey is the last request of
// its enable step, the moment a cancel can arrive before the consent.
type openCase struct {
	name      string
	routes    func() map[string]string
	enableKey string
	open      func(ctx context.Context, d *Device, prompt ConsentPrompt) error
}

func openCases() []openCase {
	return []openCase{
		{"kvm", kvmRoutes, "Get|CIM_KVMRedirectionSAP", func(ctx context.Context, d *Device, p ConsentPrompt) error {
			_, err := d.OpenKVM(ctx, kvm.QualityHigh, p)
			return err
		}},
		{"sol", solRoutes, "Get|AMT_RedirectionService", func(ctx context.Context, d *Device, p ConsentPrompt) error {
			_, err := d.OpenSOL(ctx, p)
			return err
		}},
		{"ider", iderRoutes, "Get|AMT_RedirectionService", func(ctx context.Context, d *Device, p ConsentPrompt) error {
			_, err := d.OpenIDER(ctx, &testImage{}, ider.StartNow, p)
			return err
		}},
	}
}

// consentFake serves the routes of c with a not yet started opt-in that every feature needs
// (OptInRequired All) and a device config whose dial fails the test.
func consentFake(t *testing.T, c openCase) (*fakeWSMan, *Device) {
	routes := c.routes()
	routes["Get|IPS_OptInService"] = "optin_get_all_notstarted.xml"
	routes["StartOptIn|IPS_OptInService"] = "optin_start.xml"
	routes["CancelOptIn|IPS_OptInService"] = "optin_cancel.xml"
	routes["SendOptInCode|IPS_OptInService"] = "optin_send.xml"
	f := &fakeWSMan{routes: routes}
	d := newDevice(f, "")
	d.cfg = Config{Host: "192.0.2.10", Username: "admin", Password: "pw", dialContext: func(context.Context, string, string) (net.Conn, error) {
		t.Error("must not dial")
		return nil, errors.New("must not dial")
	}}
	return f, d
}

// ctxPrompt answers like the UI dialog: an error once ctx is cancelled.
func ctxPrompt(ctx context.Context) ConsentPrompt {
	return consentFunc(func() (string, error) {
		if ctx.Err() != nil {
			return "", errors.New("consent cancelled")
		}
		return "123456", nil
	})
}

func TestOpen_CancelDuringEnableSendsNoStartOptIn(t *testing.T) {
	for _, c := range openCases() {
		t.Run(c.name, func(t *testing.T) {
			f, d := consentFake(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.onPost = func(key string) {
				if key == c.enableKey {
					cancel()
				}
			}
			err := c.open(ctx, d, failPrompt(t))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v", err)
			}
			keys := routeKeys(f)
			if slices.Contains(keys, "StartOptIn|IPS_OptInService") || slices.Contains(keys, "CancelOptIn|IPS_OptInService") {
				t.Fatalf("requests %v", keys)
			}
		})
	}
}

func TestOpen_CancelAfterStartOptInSendsCancelOptIn(t *testing.T) {
	for _, c := range openCases() {
		t.Run(c.name, func(t *testing.T) {
			f, d := consentFake(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.onPost = func(key string) {
				if key == "StartOptIn|IPS_OptInService" {
					cancel()
				}
			}
			if err := c.open(ctx, d, ctxPrompt(ctx)); err == nil {
				t.Fatal("expected error")
			}
			keys := routeKeys(f)
			n := len(keys)
			if n < 3 || keys[n-3] != "Get|IPS_OptInService" || keys[n-2] != "StartOptIn|IPS_OptInService" || keys[n-1] != "CancelOptIn|IPS_OptInService" {
				t.Fatalf("requests %v", keys)
			}
		})
	}
}

// Without a cancel a prompt error leaves the opt-in running: the MCP server answers "code
// needed" and the agent sends the code shown on the host screen in its next call.
func TestUserConsent_PromptErrorWithoutCancelKeepsOptIn(t *testing.T) {
	routes := map[string]string{"Get|IPS_OptInService": "optin_get_kvm_notstarted.xml", "StartOptIn|IPS_OptInService": "optin_start.xml"}
	f := &fakeWSMan{routes: routes}
	err := newDevice(f, "").UserConsent(consentFunc(func() (string, error) { return "", errors.New("code needed") }))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := routeKeys(f); !slices.Equal(got, []string{"Get|IPS_OptInService", "StartOptIn|IPS_OptInService"}) {
		t.Fatalf("requests %v", got)
	}
}
