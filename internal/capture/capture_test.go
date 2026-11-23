package capture

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFileNameSanitisesDeviceName(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 14, 12, 0, time.Local)
	cases := []struct{ device, want string }{
		{"Lab-Server", "openlamt-Lab-Server-2026-11-23-101412.png"},
		{"Lab Server 2", "openlamt-Lab-Server-2-2026-11-23-101412.png"},
		{`a/b\c:d*e?f"g<h>i|j`, "openlamt-a-b-c-d-e-f-g-h-i-j-2026-11-23-101412.png"},
		{"  ..dots..  ", "openlamt-dots-2026-11-23-101412.png"},
		{"Büro-PC", "openlamt-Büro-PC-2026-11-23-101412.png"},
		{"192.0.2.10", "openlamt-192.0.2.10-2026-11-23-101412.png"},
		{"tab\tand\nnewline", "openlamt-tab-and-newline-2026-11-23-101412.png"},
		{"", "openlamt-device-2026-11-23-101412.png"},
		{"///", "openlamt-device-2026-11-23-101412.png"},
		{"a--//--b", "openlamt-a-b-2026-11-23-101412.png"},
	}
	for _, c := range cases {
		if got := FileName(c.device, at, "png"); got != c.want {
			t.Errorf("FileName(%q) = %q, want %q", c.device, got, c.want)
		}
	}
	long := strings.Repeat("x", 200)
	got := FileName(long, at, "avi")
	if !strings.HasSuffix(got, "-2026-11-23-101412.avi") || len([]rune(got)) > len("openlamt--2026-11-23-101412.avi")+maxNameRunes {
		t.Fatalf("long name not shortened: %q", got)
	}
}

func TestUniquePathAddsCounter(t *testing.T) {
	dir := t.TempDir()
	name := "openlamt-x-2026-11-23-101412.png"
	if got := UniquePath(dir, name); got != filepath.Join(dir, name) {
		t.Fatalf("free name: %s", got)
	}
	for _, n := range []string{name, "openlamt-x-2026-11-23-101412-2.png"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := UniquePath(dir, name); got != filepath.Join(dir, "openlamt-x-2026-11-23-101412-3.png") {
		t.Fatalf("taken name: %s", got)
	}
}

func TestCheckFolder(t *testing.T) {
	dir := t.TempDir()
	if err := CheckFolder(dir); err != nil {
		t.Fatalf("usable folder: %v", err)
	}
	if err := CheckFolder(""); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("empty: %v", err)
	}
	missing := filepath.Join(dir, "gone")
	if err := CheckFolder(missing); !errors.Is(err, ErrFolderMissing) {
		t.Fatalf("missing: %v", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckFolder(file); !errors.Is(err, ErrFolderMissing) {
		t.Fatalf("file instead of folder: %v", err)
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o700) })
		if err := CheckFolder(ro); !errors.Is(err, ErrFolderReadOnly) {
			t.Fatalf("read-only: %v", err)
		}
		entries, _ := os.ReadDir(ro)
		if len(entries) != 0 {
			t.Fatalf("probe left %d files", len(entries))
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".openlamt") {
			t.Fatalf("probe file left: %s", e.Name())
		}
	}
}

func TestSavePNGWritesNativeResolution(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	img.SetRGBA(10, 20, color.RGBA{R: 255, A: 255})
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := SavePNG(path, img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() != 640 || got.Bounds().Dy() != 480 {
		t.Fatalf("size %v", got.Bounds())
	}
	if r, _, _, _ := got.At(10, 20).RGBA(); r>>8 != 255 {
		t.Fatalf("pixel %v", got.At(10, 20))
	}
	// A failed write leaves no partial file behind.
	bad := filepath.Join(t.TempDir(), "missing", "shot.png")
	if err := SavePNG(bad, img); err == nil {
		t.Fatal("write into a missing folder succeeded")
	}
	if _, err := os.Stat(bad); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial file: %v", err)
	}
}
