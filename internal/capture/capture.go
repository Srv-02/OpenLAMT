// Package capture saves the remote screen of a KVM session: screenshots as PNG and recordings
// as Motion-JPEG in an AVI file (issues #4 and #26). It knows nothing about the UI; the device
// window decides where a file goes and reports the result.
package capture

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Reasons why a configured folder cannot take a file; the device window falls back to the
// save dialog and names the reason.
var (
	ErrNoFolder       = errors.New("capture: no folder set")
	ErrFolderMissing  = errors.New("capture: folder does not exist")
	ErrFolderReadOnly = errors.New("capture: folder is not writable")
)

// maxNameRunes bounds the device part of a file name, so the whole name stays well below the
// 255 bytes most file systems allow.
const maxNameRunes = 64

// FileName returns openlamt-<device>-<YYYY-MM-DD-HHMMSS>.<ext> for a file taken at t. The device
// name is made safe for every file system: characters that Windows, macOS or Linux reject in a
// name and whitespace become "-", runs of "-" collapse, and "-" and "." are trimmed at both
// ends (Windows drops trailing dots). An empty result becomes "device".
func FileName(device string, t time.Time, ext string) string {
	var b strings.Builder
	dash := false
	for _, r := range device {
		if strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsSpace(r) || unicode.IsControl(r) || r == '-' {
			if !dash {
				b.WriteRune('-')
				dash = true
			}
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	name := strings.Trim(b.String(), "-.")
	if r := []rune(name); len(r) > maxNameRunes {
		name = strings.TrimRight(string(r[:maxNameRunes]), "-.")
	}
	if name == "" {
		name = "device"
	}
	return fmt.Sprintf("openlamt-%s-%s.%s", name, t.Format("2006-01-02-150405"), ext)
}

// maxTries bounds the search for a free name in CreateUnique.
const maxTries = 10000

// CreateUnique creates a new file for name in dir: dir/name, or dir/<name>-2.<ext>, -3 and so on
// if that name is taken (two screenshots in the same second). The name is reserved atomically
// with O_CREATE|O_EXCL, so a file or a symbolic link that appears there is never followed,
// truncated or overwritten; the caller writes into the returned file.
func CreateUnique(dir, name string) (*os.File, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, fs.ErrExist) || i > maxTries {
			return f, err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
}

// CreateChosen creates or truncates the file the user chose in the save dialog, which asked
// before it let an existing file be chosen.
func CreateChosen(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
}

// CheckFolder reports whether dir can take a new file: ErrNoFolder for an empty value,
// ErrFolderMissing if it does not exist or is not a folder, ErrFolderReadOnly if a file cannot
// be created there. The probe file is removed again.
func CheckFolder(dir string) error {
	if dir == "" {
		return ErrNoFolder
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return ErrFolderMissing
	}
	f, err := os.CreateTemp(dir, ".openlamt-probe-*")
	if err != nil {
		return ErrFolderReadOnly
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// WritePNG writes img as PNG at its native resolution into f (from CreateUnique) and closes
// it; on failure the file is removed again.
func WritePNG(f *os.File, img image.Image) error {
	err := png.Encode(f, img)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// SavePNG writes img to path, the file the user chose in the save dialog, as PNG at its native
// resolution. The file is written under a new temporary name (O_EXCL) next to it and renamed
// at the end: a failed write leaves nothing behind, an existing file (the dialog asked) is only
// replaced by a complete one, and a symbolic link at path is replaced, not followed.
func SavePNG(path string, img image.Image) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".openlamt-*.png")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = png.Encode(f, img)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
