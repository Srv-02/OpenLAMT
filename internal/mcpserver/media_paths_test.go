package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsAliasPath: device and verbatim paths and administrative shares are named, drive
// letter paths and normal shares are not; the check reads the text only, on every system.
func TestWindowsAliasPath(t *testing.T) {
	for p, want := range map[string]string{
		`\\?\C:\Users\u\.ssh`: "a Windows device path",
		`\\?\c:\users\u`:      "a Windows device path",
		`//?/C:/Users/u/.ssh`: "a Windows device path",
		`\\.\C:\Users\u\.ssh`: "a Windows device path",
		`\??\C:\Users\u\.ssh`: "a Windows device path",
		`\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\Users`: "a Windows device path",
		`\\?\UNC\localhost\C$\Users`:                             "a Windows device path",
		`\\?`:                                                    "a Windows device path",
		`\\localhost\C$\Users\u\.ssh`:                            "an administrative share",
		`//127.0.0.1/c$/Users`:                                   "an administrative share",
		`\\nas.example.com\ADMIN$`:                               "an administrative share",
		`C:\Images\installer.iso`:                                "",
		`c:/Images`:                                              "",
		`\\nas.example.com\isos\installer.iso`:                   "",
		`\\?x\share`:                                             "",
		`/home/u/Downloads`:                                      "",
	} {
		if got := windowsAliasPath(p); got != want {
			t.Errorf("windowsAliasPath(%q) = %q, want %q", p, got, want)
		}
	}
}

// protectByIdentity protects dir under a path text that does not lead to it, as a firmlink, a
// bind mount or a \\?\ path shows a protected folder by another name: only its file identity
// can recognize it.
func protectByIdentity(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "elsewhere", ".ssh")
	testProtectedDirs = []protectedDir{{path: other, name: "SSH keys", fi: fi}}
	t.Cleanup(func() { testProtectedDirs = nil })
}

// TestMediaMountRefusesProtectedFolderByIdentity: a protected folder whose path text does not
// match is still refused, itself, a file or folder inside it, and a folder that holds it (found
// while the image is built). Nothing reaches the device and no image stays in the cache; without
// the protection the same folder mounts.
func TestMediaMountRefusesProtectedFolderByIdentity(t *testing.T) {
	files := newMediaFiles(t)
	secret := filepath.Join(files.folder, "nested", ".ssh")
	key := mustMkdirFile(t, secret, "keys.iso")
	deeper := filepath.Join(secret, "deeper")
	mustMkdirFile(t, deeper, "id_ed25519")
	protectByIdentity(t, secret)

	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	srv := addIDER(t, f)
	h.start()
	for _, p := range []string{secret, key, deeper} {
		h.mustFail("media_mount", dev("NUC", "path", p), "it is or holds a protected folder (SSH keys)")
	}
	h.mustFail("media_mount", dev("NUC", "path", files.folder),
		"its folder "+filepath.Join("nested", ".ssh")+" is a protected folder (SSH keys)")
	if n := len(f.IDERStarts()); n != 0 {
		t.Fatalf("%d mounts", n)
	}
	if n := files.cachedImages(t); n != 0 {
		t.Fatalf("%d images left in the cache", n)
	}

	testProtectedDirs = nil
	_, r := h.mount(srv, dev("NUC", "path", files.folder))
	if r.out["files"] != float64(3) {
		t.Fatalf("mount %v", r.out)
	}
}
