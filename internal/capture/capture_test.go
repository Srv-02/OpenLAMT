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
	"unicode/utf8"
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

// TestFileNameLimitsBytes: a device name of multi-byte characters is cut by bytes at a
// character boundary, so the file (with a counter of CreateUnique) can be created on file
// systems that allow 255 bytes per name; the device part of a short or ASCII name stays as
// before.
func TestFileNameLimitsBytes(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 14, 12, 0, time.Local)
	for _, device := range []string{strings.Repeat("😀", 64), strings.Repeat("日", 100), strings.Repeat("ä", 200), "x" + strings.Repeat("😀", 63)} {
		got := FileName(device, at, "avi")
		part := strings.TrimSuffix(strings.TrimPrefix(got, "openlamt-"), "-2026-11-23-101412.avi")
		if !utf8.ValidString(got) || len(part) > maxNameBytes || len(part) < maxNameBytes-3 || !strings.HasPrefix(device, part) {
			t.Fatalf("%q: device part %q (%d bytes)", device, part, len(part))
		}
		if len(got)+len("-10000") > 200 {
			t.Fatalf("%q: %d bytes", got, len(got))
		}
		dir := t.TempDir()
		for range 2 { // the second one gets the counter -2
			f, err := CreateUnique(dir, got)
			if err != nil {
				t.Fatalf("%q: %v", got, err)
			}
			f.Close()
		}
	}
	ascii := strings.Repeat("y", maxNameRunes)
	if got := FileName(ascii+"zzz", at, "avi"); got != "openlamt-"+ascii+"-2026-11-23-101412.avi" {
		t.Fatalf("ASCII name %q", got)
	}
}

// TestCreateUniqueNeverReusesAName: a taken name moves on to the next counter, and a symbolic
// link planted at the name is not followed: its target stays untouched (O_EXCL, no truncate).
func TestCreateUniqueNeverReusesAName(t *testing.T) {
	dir := t.TempDir()
	name := "openlamt-x-2026-11-23-101412.png"
	f, err := CreateUnique(dir, name)
	if err != nil || f.Name() != filepath.Join(dir, name) {
		t.Fatalf("free name: %v %v", f, err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(dir, "openlamt-x-2026-11-23-101412-2.png"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = CreateUnique(dir, name)
	if err != nil || f.Name() != filepath.Join(dir, "openlamt-x-2026-11-23-101412-3.png") {
		t.Fatalf("taken names: %v %v", f, err)
	}
	f.Close()
	if b, _ := os.ReadFile(filepath.Join(dir, "openlamt-x-2026-11-23-101412-2.png")); string(b) != "keep" {
		t.Fatal("an existing file was truncated")
	}

	if runtime.GOOS == "windows" { // symbolic links need a privilege there
		return
	}
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := "openlamt-y-2026-11-23-101412.avi"
	if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	f, err = CreateUnique(dir, link)
	if err != nil || f.Name() != filepath.Join(dir, "openlamt-y-2026-11-23-101412-2.avi") {
		t.Fatalf("symlink name: %v %v", f, err)
	}
	f.WriteString("frame")
	f.Close()
	if b, _ := os.ReadFile(target); string(b) != "secret" {
		t.Fatalf("symlink target changed: %q", b)
	}
}

// TestWritePNGRemovesFileOnError: a failed write leaves no file behind.
func TestWritePNGRemovesFileOnError(t *testing.T) {
	dir := t.TempDir()
	f, err := CreateUnique(dir, "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	f.Close() // writing into a closed file fails
	if err := WritePNG(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(filepath.Join(dir, "shot.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file left: %v", err)
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
