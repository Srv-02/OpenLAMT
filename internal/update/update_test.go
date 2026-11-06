package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"v0.1.0", "0.2.0", false},
		{"v0.1.0", "0.1.0-rc16", true},  // the final version is newer than its rc
		{"v0.1.0-rc16", "0.1.0", false}, // and an rc of it is not
		{"v0.1.0-rc16", "0.1.0-rc9", true},
		{"v0.1.0-rc9", "0.1.0-rc16", false},
		{"v0.1.0-rc.10", "0.1.0-rc.2", true},
		{"v0.2.0-rc1", "0.1.0", true},
		{"v1.0.0", "0.10.3", true},
		{"v0.10.0", "0.9.0", true},
		{"v0.2.0", "dev", false},
		{"v0.2.0", "", false},
		{"latest", "0.1.0", false},
		{"v0.2", "0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.tag, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	for s, want := range map[string]bool{"0.1.0": true, "v0.1.0-rc14": true, "dev": false, "": false, "0.1": false, "v1.2.3+build.5": true} {
		if Valid(s) != want {
			t.Errorf("Valid(%q) != %v", s, want)
		}
	}
}

func releaseServer(t *testing.T, status int, body string, hits *int, ua *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		*ua = r.Header.Get("User-Agent")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatest(t *testing.T) {
	var hits int
	var ua string
	srv := releaseServer(t, 200, `{"tag_name":"v0.2.0","html_url":"https://example.com/r","body":"* a","assets":[{"name":"OpenLAMT-v0.2.0-linux-x64.deb","browser_download_url":"https://example.com/d","size":3,"digest":"sha256:ab"}]}`, &hits, &ua)
	r, err := Checker{URL: srv.URL, UserAgent: "OpenLAMT/0.1.0"}.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "v0.2.0" || r.Version() != "0.2.0" || r.HTMLURL != "https://example.com/r" || len(r.Assets) != 1 || r.Assets[0].Digest != "sha256:ab" {
		t.Fatalf("%+v", r)
	}
	if ua != "OpenLAMT/0.1.0" || hits != 1 {
		t.Fatalf("user agent %q, hits %d", ua, hits)
	}
}

func TestLatestErrors(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"not found":    {404, `{"message":"Not Found"}`},
		"rate limit":   {403, `{"message":"API rate limit exceeded"}`},
		"server error": {500, ``},
		"no json":      {200, `<html>`},
		"no version":   {200, `{"tag_name":"nightly"}`},
	} {
		t.Run(name, func(t *testing.T) {
			var hits int
			var ua string
			srv := releaseServer(t, c.status, c.body, &hits, &ua)
			if _, err := (Checker{URL: srv.URL}).Latest(context.Background()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestAssetFor(t *testing.T) {
	r := Release{Tag: "v0.2.0", Assets: []Asset{
		{Name: "OpenLAMT-v0.2.0-macos-arm64.zip", URL: "https://example.com/mac"},
		{Name: "OpenLAMT-v0.2.0-windows-x64.zip", URL: "https://example.com/win"},
		{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: "https://example.com/deb"},
	}}
	for _, c := range []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "https://example.com/mac"},
		{"windows", "amd64", "https://example.com/win"},
		{"linux", "amd64", "https://example.com/deb"},
		{"darwin", "amd64", ""},
		{"linux", "arm64", ""},
		{"windows", "arm64", ""},
	} {
		a, ok := AssetFor(r, c.goos, c.goarch)
		if ok != (c.want != "") || a.URL != c.want {
			t.Errorf("%s/%s: %+v %v", c.goos, c.goarch, a, ok)
		}
	}
	if _, ok := AssetFor(Release{Tag: "v0.2.0"}, "linux", "amd64"); ok {
		t.Error("a release without assets has none")
	}
}

func TestNotes(t *testing.T) {
	body := "## What's Changed\r\n* feat(ui): Sprache in den Einstellungen by @someone in https://example.com/pull/106\r\n" +
		"- **Fix** for `Power Off` ([#105](https://example.com/105))\n\n" +
		"Plain line\n* c\n* d\n* e\n* f\n* g\n\n**Full Changelog**: https://example.com/compare"
	want := []string{"• feat(ui): Sprache in den Einstellungen", "• Fix for Power Off (#105)", "Plain line", "• c", "• d", "• e"}
	if got := Notes(body, 6); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := Notes("", 6); len(got) != 0 {
		t.Fatalf("empty body: %q", got)
	}
}

func assetServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestDownloadNeverOverwrites(t *testing.T) {
	content := []byte("package bytes")
	srv := assetServer(t, content)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "OpenLAMT-v0.2.0-linux-x64.deb"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := Asset{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: srv.URL + "/pkg", Digest: digestOf(content)}
	var c Checker
	p1, err := c.Download(context.Background(), a, dir)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.Download(context.Background(), a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "OpenLAMT-v0.2.0-linux-x64 (1).deb" || filepath.Base(p2) != "OpenLAMT-v0.2.0-linux-x64 (2).deb" {
		t.Fatalf("paths %s %s", p1, p2)
	}
	if old, _ := os.ReadFile(filepath.Join(dir, "OpenLAMT-v0.2.0-linux-x64.deb")); string(old) != "old" {
		t.Fatal("existing file overwritten")
	}
	if got, _ := os.ReadFile(p1); string(got) != string(content) {
		t.Fatalf("content %q", got)
	}
	if n := len(entries(t, dir)); n != 3 {
		t.Fatalf("temporary file left: %v", entries(t, dir))
	}
}

func TestDownloadWithoutDigest(t *testing.T) {
	srv := assetServer(t, []byte("x"))
	dir := t.TempDir()
	p, err := Checker{}.Download(context.Background(), Asset{Name: "OpenLAMT-v0.2.0-macos-arm64.zip", URL: srv.URL + "/pkg"}, dir)
	if err != nil || filepath.Dir(p) != dir {
		t.Fatalf("%s %v", p, err)
	}
}

func TestDownloadDigestMismatchDeletes(t *testing.T) {
	srv := assetServer(t, []byte("tampered"))
	dir := t.TempDir()
	a := Asset{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: srv.URL + "/pkg", Digest: digestOf([]byte("original"))}
	if _, err := (Checker{}).Download(context.Background(), a, dir); !errors.Is(err, ErrDigest) {
		t.Fatalf("err %v", err)
	}
	if n := entries(t, dir); len(n) != 0 {
		t.Fatalf("files left: %v", n)
	}
}

func TestDownloadErrorsLeaveNothing(t *testing.T) {
	srv := assetServer(t, []byte("x"))
	dir := t.TempDir()
	for _, a := range []Asset{
		{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: srv.URL + "/missing"},
		{Name: "../evil.deb", URL: srv.URL + "/pkg"},
	} {
		if _, err := (Checker{}).Download(context.Background(), a, dir); err == nil {
			t.Errorf("%+v: want an error", a)
		}
	}
	if n := entries(t, dir); len(n) != 0 {
		t.Fatalf("files left: %v", n)
	}
}

func TestDownloadsIn(t *testing.T) {
	home := t.TempDir()
	if got := downloadsIn(home); got != home {
		t.Fatalf("without Downloads: %s", got)
	}
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := downloadsIn(home); got != filepath.Join(home, "Downloads") {
		t.Fatalf("with Downloads: %s", got)
	}
}
