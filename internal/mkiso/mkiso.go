// Package mkiso builds an ISO9660 image with Joliet extensions from a folder.
//
// If the folder contains EFI/BOOT/BOOTX64.EFI (case-insensitive), the image is
// also UEFI-bootable through an El Torito entry that points to an embedded
// FAT32 image holding the whole folder. File data is written only once: the
// ISO9660 and Joliet records of every file point into the data region of the
// FAT32 image, which is the last thing in the ISO.
//
// Layout (2048-byte sectors):
//
//	0-15      system area (zeros)
//	16        primary volume descriptor
//	17        boot record volume descriptor (bootable only)
//	next      Joliet supplementary volume descriptor, terminator
//	          boot catalog (bootable only)
//	          path tables: primary L, primary M, Joliet L, Joliet M
//	          directory extents: primary (breadth first), then Joliet
//	M..       non-bootable: file data, each file at a sector boundary
//	          bootable: FAT32 image (reserved area, FAT1, FAT2, data region)
package mkiso

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Options controls Build.
type Options struct {
	// VolumeID is the volume label. Empty means the base name of the folder.
	VolumeID string
	// Progress receives the number of file data bytes written so far and the
	// total. It may be nil and is called from the building goroutine.
	Progress func(done, total int64)
}

// Info describes a finished image.
type Info struct {
	Size     int64    // image size in bytes
	Files    int      // regular files
	Dirs     int      // directories, the root not counted
	Bootable bool     // EFI/BOOT/BOOTX64.EFI found
	Skipped  []string // relative paths of skipped symlinks and special files
}

var (
	ErrEmpty        = errors.New("mkiso: folder contains no regular file")
	ErrFileTooLarge = errors.New("mkiso: file is 4 GiB or larger")
	ErrNameTooLong  = errors.New("mkiso: name is longer than 103 UTF-16 code units")
	ErrNameConflict = errors.New("mkiso: name conflicts with another entry (case-insensitive, after replacing special characters)")
	ErrTooManyDirs  = errors.New("mkiso: more than 65535 directories")
	ErrDirTooLarge  = errors.New("mkiso: FAT directory would need more than 65536 entries")
	ErrFileChanged  = errors.New("mkiso: file size changed while building")
	ErrTooLarge     = errors.New("mkiso: content too large for a FAT32 image")
)

// PathError wraps an error with the path (relative to the source folder,
// slash separated) it belongs to.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

// Build writes the ISO for dir to w. Gaps are produced with Seek, so a file
// stays sparse where the filesystem supports it. ctx aborts the build between
// copy blocks.
func Build(ctx context.Context, dir string, w io.WriteSeeker, opt Options) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	t, err := scan(ctx, dir)
	if err != nil {
		return Info{}, err
	}
	vol := opt.VolumeID
	if vol == "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		vol = filepath.Base(abs)
	}
	b := newBuilder(t, vol)
	if err := b.plan(); err != nil {
		return Info{}, err
	}
	if err := b.write(ctx, w, opt.Progress); err != nil {
		return Info{}, err
	}
	return Info{
		Size:     b.totalSectors * sectorSize,
		Files:    t.files,
		Dirs:     t.dirs,
		Bootable: t.bootable,
		Skipped:  t.skipped,
	}, nil
}

// TempImage is a built ISO in the cache directory. Close deletes the file.
type TempImage struct {
	f    *os.File
	path string
	size int64
	once sync.Once
	err  error
}

// ReadAt implements io.ReaderAt.
func (t *TempImage) ReadAt(p []byte, off int64) (int, error) { return t.f.ReadAt(p, off) }

// Size returns the image size in bytes.
func (t *TempImage) Size() int64 { return t.size }

// Path returns the file path of the image.
func (t *TempImage) Path() string { return t.path }

// Close closes and then removes the file. It is safe to call more than once.
func (t *TempImage) Close() error {
	t.once.Do(func() {
		cerr := t.f.Close()
		rerr := os.Remove(t.path)
		if rerr != nil && os.IsNotExist(rerr) {
			rerr = nil
		}
		t.err = errors.Join(cerr, rerr)
	})
	return t.err
}

// CacheDir returns os.UserCacheDir()/OpenLAMT/ider and creates it (0700).
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "OpenLAMT", "ider")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// BuildTemp builds the image into a randomly named file in CacheDir. On error
// or cancellation the partial file is removed.
func BuildTemp(ctx context.Context, dir string, opt Options) (*TempImage, Info, error) {
	cd, err := CacheDir()
	if err != nil {
		return nil, Info{}, err
	}
	f, err := os.CreateTemp(cd, "*.iso")
	if err != nil {
		return nil, Info{}, err
	}
	fail := func(err error) (*TempImage, Info, error) {
		f.Close()
		os.Remove(f.Name())
		return nil, Info{}, err
	}
	info, err := Build(ctx, dir, f, opt)
	if err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("mkiso: %w", err))
	}
	return &TempImage{f: f, path: f.Name(), size: info.Size}, info, nil
}

// CleanCache removes leftover *.iso files from CacheDir. Files that cannot be
// removed (for example because they are still in use) are skipped.
func CleanCache() error {
	cd, err := CacheDir()
	if err != nil {
		return err
	}
	ents, err := os.ReadDir(cd)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".iso") {
			continue
		}
		_ = os.Remove(filepath.Join(cd, e.Name()))
	}
	return nil
}
