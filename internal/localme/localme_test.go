package localme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFollowsTheProbe(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if !detect(goos, Fake(true)) {
			t.Errorf("%s: present device not detected", goos)
		}
		if detect(goos, Fake(false)) {
			t.Errorf("%s: absent device detected", goos)
		}
	}
}

func TestDetectNeverOnMacOS(t *testing.T) {
	if detect("darwin", Fake(true)) {
		t.Fatal("macOS reported an Intel ME device")
	}
}

func TestDetectWithoutProbe(t *testing.T) {
	if detect("linux", nil) {
		t.Fatal("no probe reported a device")
	}
}

func TestAnyExists(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "mei0")
	if anyExists(dev, filepath.Join(dir, "sys")) {
		t.Fatal("absent paths reported as present")
	}
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !anyExists(filepath.Join(dir, "sys"), dev) {
		t.Fatal("present path not found")
	}
}
