//go:build !windows

package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeOpener puts a shell script named like the platform's opener (open on macOS, xdg-open
// elsewhere) first on PATH: it records its argument in a file and exits with code.
func fakeOpener(t *testing.T, code string) (log string) {
	t.Helper()
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > '" + log + "'\nexit " + code + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestOpenFileUsesThePlatformOpener(t *testing.T) {
	pkg := "/home/user/Downloads/OpenLAMT-v0.2.0-linux-x64.deb"
	log := fakeOpener(t, "0")
	if err := openFile(pkg); err != nil {
		t.Fatalf("exit 0: %v", err)
	}
	if b, _ := os.ReadFile(log); string(b) != pkg {
		t.Fatalf("opener got %q", b)
	}
	fakeOpener(t, "4") // xdg-open: no app for the file type
	if err := openFile(pkg); err == nil || !strings.Contains(err.Error(), "exit status 4") {
		t.Fatalf("exit 4: %v", err)
	}
}

func TestNoElevationOrLeftoverOutsideWindows(t *testing.T) {
	if err := elevateReplace("a", "b", "c"); err == nil {
		t.Error("elevation must fail outside Windows")
	}
	if err := RemoveLeftover(); err != nil {
		t.Errorf("RemoveLeftover: %v", err)
	}
}
