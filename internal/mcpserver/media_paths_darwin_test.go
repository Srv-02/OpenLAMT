//go:build darwin

package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// dataVolume returns p under the firmlink of the data volume (/System/Volumes/Data/private/var
// is /private/var), or skips when that spelling does not lead to the same folder here.
func dataVolume(t *testing.T, p string) string {
	t.Helper()
	alias := filepath.Join("/System/Volumes/Data", p)
	a, err1 := os.Stat(alias)
	b, err2 := os.Stat(p)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Skipf("no firmlink to %s: %v %v", p, err1, err2)
	}
	if r, err := filepath.EvalSymlinks(alias); err != nil || r != alias {
		t.Skipf("the firmlink spelling resolves to %q: %v", r, err)
	}
	return alias
}

// TestMediaMountRefusesFirmlinkAlias: the home folder, a folder above it and ~/.ssh are
// refused under their /System/Volumes/Data spelling; a folder there in the home folder passes,
// and media.allowed_paths admits a file under the other spelling of an allowed folder.
func TestMediaMountRefusesFirmlinkAlias(t *testing.T) {
	files := newMediaFiles(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	alias := dataVolume(t, home)
	mustMkdirFile(t, filepath.Join(home, ".ssh"), "keys.iso")
	downloads := mustMkdirFile(t, filepath.Join(home, "Downloads"), "installer.iso")

	var asked []string
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addIDER(t, f)
	h.cfg.RequireClientConfirmation = true
	h.elicit = func(r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = append(asked, r.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	h.start()
	for _, p := range []string{alias, filepath.Dir(alias), filepath.Join(alias, ".ssh"), filepath.Join(alias, ".ssh", "keys.iso")} {
		h.mustFail("media_mount", dev("NUC", "path", p), "protected folder")
	}
	if len(asked) != 0 {
		t.Fatalf("asked %q", asked)
	}
	h.mustFail("media_mount", dev("NUC", "path", filepath.Join(alias, "Downloads", "installer.iso")), "not confirmed")

	h.cfg.MediaAllowedPaths = []string{dataVolume(t, filepath.Dir(downloads))}
	h.mustFail("media_mount", dev("NUC", "path", files.iso), "outside media.allowed_paths")
	h.mustFail("media_mount", dev("NUC", "path", downloads), "not confirmed")
	if len(asked) != 2 || !strings.Contains(asked[1], downloads) {
		t.Fatalf("asked %q", asked)
	}
	if n := len(f.IDERStarts()); n != 0 {
		t.Fatalf("%d mounts", n)
	}
}
