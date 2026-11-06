package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"
)

type promptFunc func(host, fp string) (bool, error)

func (p promptFunc) ConfirmFingerprint(host, fp string) (bool, error) { return p(host, fp) }

const fp = "abababababababababababababababababababababababababababababababab"

var ctx = context.Background()

func TestOpenReadsStatusAndKeepsConfig(t *testing.T) {
	dev := &Fake{St: amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, FP: fp}
	cfg := amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true, Fingerprint: fp}
	s, st, err := Open(ctx, cfg, nil, ConnectFake(dev))
	if err != nil || st.AMTVersion != "16.1.25" || dev.Calls() != 1 {
		t.Fatalf("%+v %v calls=%d", st, err, dev.Calls())
	}
	if c := s.Config(); c.Password != "secret-1" || c.Fingerprint != fp || c.Host != "device.example.com" {
		t.Fatalf("%+v", c)
	}
	if got := dev.Configs(); len(got) != 1 || got[0].Fingerprint != fp {
		t.Fatalf("configs %+v", got)
	}
}

func TestOpenTOFUSetsFingerprint(t *testing.T) {
	dev := &Fake{FP: fp}
	asked := 0
	prompt := promptFunc(func(host, got string) (bool, error) { asked++; return host == "device.example.com" && got == fp, nil })
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", UseTLS: true}, prompt, ConnectFake(dev))
	if err != nil || asked != 1 || s.Fingerprint() != fp || s.Config().Fingerprint != fp {
		t.Fatalf("err=%v asked=%d fp=%q", err, asked, s.Fingerprint())
	}
}

func TestOpenTrustDeclined(t *testing.T) {
	dev := &Fake{FP: fp}
	prompt := promptFunc(func(string, string) (bool, error) { return false, nil })
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com", UseTLS: true}, prompt, ConnectFake(dev))
	if !errors.Is(err, amt.ErrTrustDeclined) || s != nil || dev.Calls() != 0 {
		t.Fatalf("err=%v s=%v calls=%d", err, s, dev.Calls())
	}
}

func TestOpenAuthError(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "401 Unauthorized", "<html>secret body</html>")
	dev := &Fake{StatusErr: fmt.Errorf("enumerate power state: %w", libErr)}
	_, _, err := Open(ctx, amt.Config{Host: "device.example.com", Password: "Geheim-42"}, nil, ConnectFake(dev))
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret body") || strings.Contains(err.Error(), "Geheim-42") {
		t.Fatalf("leaks: %v", err)
	}
}

func TestOpenOtherStatusErrorPassesThrough(t *testing.T) {
	boom := errors.New("timeout")
	dev := &Fake{StatusErr: boom}
	_, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if !errors.Is(err, boom) || errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenConnectErrorNoDevice(t *testing.T) {
	boom := errors.New("dial failed")
	connect := func(context.Context, amt.Config, amt.TrustPrompt) (Device, error) { return nil, boom }
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, connect)
	if !errors.Is(err, boom) || s != nil {
		t.Fatalf("%v %v", s, err)
	}
}

func TestSessionSerializesDeviceCalls(t *testing.T) {
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Status() }()
		go func() { defer wg.Done(); s.Power(amt.PowerOn) }()
	}
	wg.Wait()
	if dev.MaxBusy() != 1 {
		t.Fatalf("max concurrent device calls = %d", dev.MaxBusy())
	}
	if dev.Calls() != 21 || len(dev.Powered()) != 20 {
		t.Fatalf("calls=%d powered=%d", dev.Calls(), len(dev.Powered()))
	}
}

func TestSessionStatusAndPowerClassify401(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "401 Unauthorized", "<html>secret body</html>")
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	dev.SetStatusErr(fmt.Errorf("enumerate power state: %w", libErr))
	if _, err := s.Status(); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") {
		t.Fatalf("status: %v", err)
	}
	dev.PowerErr = fmt.Errorf("power on: %w", libErr)
	if err := s.Power(amt.PowerOn); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") {
		t.Fatalf("power: %v", err)
	}
}

func TestAMTConnectReturnsNilInterfaceOnError(t *testing.T) {
	d, err := AMTConnect(ctx, amt.Config{Host: "2001:db8::10", UseTLS: true}, nil)
	if !errors.Is(err, amt.ErrIPv6Unsupported) || d != nil {
		t.Fatalf("%v %v", d, err)
	}
}

func TestClassifyIgnores401InsideOtherErrors(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "500 Internal Server Error", "<html>error 401 in body</html>")
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	dev.SetStatusErr(libErr)
	if _, err := s.Status(); err == nil || errors.Is(err, ErrAuth) {
		t.Fatalf("500 classified as auth: %v", err)
	}
}

func TestClassifyStripsHTTPBody(t *testing.T) {
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "500 Internal Server Error", "<html>internal detail</html>")
	dev := &Fake{}
	s, _, err := Open(ctx, amt.Config{Host: "device.example.com"}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	dev.SetStatusErr(libErr)
	_, err = s.Status()
	if err == nil || strings.Contains(err.Error(), "internal detail") || !strings.Contains(err.Error(), "500 Internal Server Error") {
		t.Fatalf("body leaked or status lost: %v", err)
	}
}

func TestOpenStatusErrorClosesIdleConnections(t *testing.T) {
	f := &Fake{StatusErr: ErrAuth}
	if _, _, err := Open(context.Background(), amt.Config{Host: "device.example.com"}, nil, ConnectFake(f)); err == nil {
		t.Fatal("want error")
	}
	if f.IdleClosed() != 1 {
		t.Fatalf("CloseIdleConnections calls = %d, want 1", f.IdleClosed())
	}
}
