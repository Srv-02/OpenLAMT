package update

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestShellExecuteInfoLayout(t *testing.T) {
	want := map[string]uintptr{"amd64": 112, "arm64": 112, "386": 60}[runtime.GOARCH]
	if got := unsafe.Sizeof(shellExecuteInfo{}); want != 0 && got != want {
		t.Fatalf("SHELLEXECUTEINFOW is %d bytes, want %d", got, want)
	}
}

func TestPowershellIsTheSystemOne(t *testing.T) {
	p, err := powershellPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(p), `\system32\windowspowershell\v1.0\powershell.exe`) {
		t.Fatalf("path %q", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("not there: %v", err)
	}
}

func TestWindowsOpensNoPackage(t *testing.T) {
	if err := openFile("x.zip"); err == nil {
		t.Fatal("Windows replaces the exe, it opens nothing")
	}
}

func TestRemoveLeftoverWithoutOldExe(t *testing.T) {
	// The test binary has no .old next to it: done at once.
	if err := RemoveLeftover(); err != nil {
		t.Fatal(err)
	}
}
