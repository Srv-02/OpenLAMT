package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// metaApp reports a fixed app metadata version, like a packaged build.
type metaApp struct {
	fyne.App
	version string
}

func (a metaApp) Metadata() fyne.AppMetadata { return fyne.AppMetadata{Version: a.version} }

func withBuildVersion(t *testing.T, v string) {
	t.Helper()
	prev := buildVersion
	buildVersion = v
	t.Cleanup(func() { buildVersion = prev })
}

func TestAppVersionOrder(t *testing.T) {
	withBuildVersion(t, "0.1.0-rc14")
	if got := appVersion(metaApp{version: "0.1.0"}); got != "0.1.0-rc14" {
		t.Fatalf("baked version must win over metadata: %q", got)
	}
	buildVersion = ""
	if got := appVersion(metaApp{version: "0.1.0"}); got != "0.1.0" {
		t.Fatalf("metadata fallback: %q", got)
	}
	for _, v := range []string{"", "dev"} {
		if got := appVersion(metaApp{version: v}); got != "" {
			t.Fatalf("metadata %q must give no version, got %q", v, got)
		}
	}
	buildVersion = "dev"
	if got := appVersion(metaApp{}); got != "" {
		t.Fatalf("baked dev must give no version, got %q", got)
	}
}

func versionDialogs(t *testing.T) map[string]string {
	t.Helper()
	englishUI(t)
	h := newStart(t)
	out := map[string]string{}
	for name, show := range map[string]func(){
		"about":    func() { showAboutDialog(h.s.win, h.s.app) },
		"settings": func() { showSettingsDialog(h.s.win, &h.s.settings) },
		"coffee":   func() { showCoffeeDialog(h.s.win, h.s.app) },
	} {
		show()
		texts, _, _ := dialogTexts(h.s.win)
		out[name] = strings.Join(texts, "|")
		closeOverlays(h.s.win)
	}
	return out
}

func TestVersionShownWithPrereleaseSuffix(t *testing.T) {
	withBuildVersion(t, "0.1.0-rc14")
	for name, all := range versionDialogs(t) {
		if !strings.Contains(all, "OpenLAMT") || !strings.Contains(all, "Version 0.1.0-rc14") {
			t.Errorf("%s: %q", name, all)
		}
	}
}

func TestVersionLineOmittedWithoutVersion(t *testing.T) {
	withBuildVersion(t, "")
	for name, all := range versionDialogs(t) {
		if !strings.Contains(all, "OpenLAMT") || strings.Contains(all, "Version") {
			t.Errorf("%s: %q", name, all)
		}
	}
}
