//go:build windows

package mcpserver

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMediaMountRefusesWindowsAliases: a \\?\ or \\.\ spelling and an administrative share are
// refused by their text, a junction to ~/.ssh (which filepath.EvalSymlinks does not follow) by
// file identity. Nothing reaches the device.
func TestMediaMountRefusesWindowsAliases(t *testing.T) {
	files := newMediaFiles(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ssh := filepath.Join(home, ".ssh")
	mustMkdirFile(t, ssh, "keys.iso")

	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	addIDER(t, f)
	h.start()
	for _, p := range []string{`\\?\` + ssh, `\\.\` + ssh, `\\?\` + filepath.Join(ssh, "keys.iso")} {
		h.mustFail("media_mount", dev("NUC", "path", p), "a Windows device path")
	}
	h.mustFail("media_mount", dev("NUC", "path", `\\localhost\C$\Users`), "an administrative share")

	junction := filepath.Join(files.dir, "innocent")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, ssh).CombinedOutput(); err != nil {
		t.Logf("no junction: %v %s", err, out)
	} else {
		h.mustFail("media_mount", dev("NUC", "path", junction), "protected folder")
		// filepath.EvalSymlinks fails below a junction: refused as missing, nothing is mounted.
		h.mustFail("media_mount", dev("NUC", "path", filepath.Join(junction, "keys.iso")), "keys.iso")
	}
	if n := len(f.IDERStarts()); n != 0 {
		t.Fatalf("%d mounts", n)
	}
}
