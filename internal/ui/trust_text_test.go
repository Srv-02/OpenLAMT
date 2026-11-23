package ui

import (
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
)

const (
	trustRemembered = "If you trust it, OpenLAMT remembers the certificate and warns you if it ever changes."
	trustOnce       = "If you trust it, OpenLAMT accepts it for this connection only. Save the device as a favorite so that OpenLAMT remembers the certificate and warns you if it ever changes."
)

// Issue #156: only a favorite stores the pin, so only a favorite's trust dialog promises to
// remember the certificate; Quick Connect to a device that is no favorite says it is accepted
// for this connection only.
func TestTrustDialogTextMatchesWhetherThePinIsStored(t *testing.T) {
	for _, tc := range []struct {
		remembered bool
		want, not  string
	}{
		{true, trustRemembered, trustOnce},
		{false, trustOnce, trustRemembered},
	} {
		w, c := panelHarness(t)
		showTrustDialog(w, "h.example.com", fp, tc.remembered, func(bool) {})
		top := c.Overlays().Top()
		findLabel(t, top, tc.want)
		if hasText(top, tc.not) {
			t.Fatalf("remembered=%v: wrong text %q shown", tc.remembered, tc.not)
		}
	}
}

// Issue #156: the connector tells the dialog whether the pin will be stored.
func TestConnectTellsTrustDialogWhetherThePinIsStored(t *testing.T) {
	for _, favorite := range []bool{true, false} {
		h := newHarness(t)
		if favorite {
			h.addFavorite(t)
		}
		var remembered []bool
		h.c.askTrust = func(_, _ string, r bool, answer func(bool)) { remembered = append(remembered, r); answer(true) }
		h.c.start(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "secret-1", UseTLS: true}})
		h.waitOpened(t)
		h.waitDone(t)
		if len(remembered) != 1 || remembered[0] != favorite {
			t.Fatalf("favorite=%v: dialog told %v", favorite, remembered)
		}
		_, err := h.keys.GetFingerprint("device.example.com", "admin")
		if stored := err == nil; stored != favorite {
			t.Fatalf("favorite=%v: pin stored %v, the dialog text would be wrong", favorite, stored)
		}
	}
}
