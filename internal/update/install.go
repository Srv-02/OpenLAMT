package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// One-click update (issue #61): after the download, Install hands the package to the platform.
// macOS opens the DMG in the Finder, Linux opens the .deb in the system's software installer
// (xdg-open), and Windows replaces the running OpenLAMT.exe with the one from the zip: the
// running exe is renamed to OpenLAMT.exe.old (Windows allows renaming a running executable),
// the new one takes its place, and the caller starts it after OpenLAMT quit. In a folder the
// user cannot write to (Program Files), only that replacement runs elevated, after a UAC prompt.
// Every failure is an error, so the caller can fall back to "install it yourself".
//
// Check and use: Install works on the verified bytes in memory (Package), never on a file it
// reads back by path. The DMG is written to a new private folder (0700, not Downloads) and
// opened from there. The .deb is saved to the Downloads folder under a free name and opened
// from there: on Ubuntu the handler is the App Center snap, which has its own private /tmp and
// can read only non-hidden paths of the home folder (issue #89). The zip is never written at all. The exe hash the elevated
// step checks comes from the verified bytes, and that step re-hashes the bytes it writes, so a
// file swapped on disk meanwhile is refused instead of being installed as administrator.

// Errors of Install; the caller words its fallback after them.
var (
	ErrOpen     = errors.New("update: the package did not open")
	ErrDeclined = errors.New("update: the administrator permission was declined")
	ErrReplace  = errors.New("update: the program could not be replaced")
	ErrNoExe    = errors.New("update: the archive holds no OpenLAMT.exe")
)

const (
	// openerWait is how long Install waits for open or xdg-open to report. An opener still
	// running after it handed the file to a program that keeps running (some xdg-open modes run
	// the installer in the foreground): that counts as opened.
	openerWait = 10 * time.Second
	// leftoverTries and leftoverEvery bound how long the new instance tries to delete the old
	// exe: the old instance may still be quitting, and Windows does not delete a running exe.
	leftoverTries = 30
	leftoverEvery = time.Second
	// exeName is the program in the Windows zip of the release workflow.
	exeName = "OpenLAMT.exe"
	// oldSuffix is added to the replaced exe.
	oldSuffix = ".old"
)

// Installer hands a downloaded package to the platform. Its functions are the seams the tests
// replace; NewInstaller fills them for the running system.
type Installer struct {
	GOOS string
	// Open hands a file to the system's opener (open on macOS, xdg-open on Linux) and reports
	// whether it opened.
	Open func(path string) error
	// Executable is the path of the running program (Windows).
	Executable func() (string, error)
	// Writable reports whether files can be created in dir (Windows).
	Writable func(dir string) bool
	// PrivateDir makes a new folder only this user can use (0700 in the temp folder).
	PrivateDir func() (string, error)
	// Downloads is the folder the Linux .deb is saved to (a snap installer cannot read /tmp).
	Downloads func() (string, error)
	Rename    func(oldpath, newpath string) error
	Remove    func(path string) error
	// Elevate replaces exe with newExe as administrator (UAC prompt); sum is the SHA-256 (hex)
	// newExe must have, from the verified package. A declined prompt is ErrDeclined.
	Elevate func(newExe, exe, sum string) error
	// Start runs a program without waiting for it.
	Start func(path string) error
}

// NewInstaller is the Installer of the running system.
func NewInstaller() Installer {
	return Installer{
		GOOS:       runtime.GOOS,
		Open:       openFile,
		Executable: executable,
		Writable:   writable,
		PrivateDir: func() (string, error) { return os.MkdirTemp("", "openlamt-update-") },
		Downloads:  DownloadsDir,
		Rename:     os.Rename,
		Remove:     os.Remove,
		Elevate:    elevateReplace,
		Start:      startProgram,
	}
}

// Install hands the verified package to the platform. It returns the program to start once
// OpenLAMT quit ("" for none: macOS and Linux leave the installing to the Finder or the
// installer).
func (in Installer) Install(pkg Package) (restart string, err error) {
	switch in.GOOS {
	case "darwin", "linux":
		return "", in.openPackage(pkg)
	case "windows":
		return in.replaceExe(pkg)
	}
	return "", fmt.Errorf("update: no installer for %s", in.GOOS)
}

// Restart starts the replaced program; the caller quits right after.
func (in Installer) Restart(path string) error { return in.Start(path) }

// openPackage writes the package and opens it from there. The DMG goes to a new private
// folder, the .deb to the Downloads folder. The file stays: the installer reads it after
// OpenLAMT quit.
func (in Installer) openPackage(pkg Package) error {
	if in.GOOS == "linux" {
		dir, err := in.Downloads()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrOpen, err)
		}
		path, err := Save(dir, pkg)
		if err == nil {
			if err = in.Open(path); err != nil {
				os.Remove(path)
			}
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrOpen, err)
		}
		return nil
	}
	dir, err := in.PrivateDir()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpen, err)
	}
	path, err := Save(dir, pkg)
	if err == nil {
		err = in.Open(path)
	}
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("%w: %w", ErrOpen, err)
	}
	return nil
}

// replaceExe puts the OpenLAMT.exe of the zip in place of the running exe.
func (in Installer) replaceExe(pkg Package) (string, error) {
	data, sum, err := exeFromZip(pkg.Data)
	if err != nil {
		return "", err
	}
	exe, err := in.Executable()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrReplace, err)
	}
	dir := filepath.Dir(exe)
	if !in.Writable(dir) {
		private, err := in.PrivateDir()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrReplace, err)
		}
		defer os.RemoveAll(private)
		newExe, err := writeExe(private, data)
		if err != nil {
			return "", err
		}
		if err := in.Elevate(newExe, exe, sum); err != nil {
			if errors.Is(err, ErrDeclined) {
				return "", err
			}
			return "", fmt.Errorf("%w: %w", ErrReplace, err)
		}
		return exe, nil
	}
	newExe, err := writeExe(dir, data)
	if err != nil {
		return "", err
	}
	old := exe + oldSuffix
	_ = in.Remove(old) // a leftover of an earlier update; a still locked one makes Rename fail
	if err := in.Rename(exe, old); err != nil {
		_ = in.Remove(newExe)
		return "", fmt.Errorf("%w: %w", ErrReplace, err)
	}
	if err := in.Rename(newExe, exe); err != nil {
		_ = in.Rename(old, exe)
		_ = in.Remove(newExe)
		return "", fmt.Errorf("%w: %w", ErrReplace, err)
	}
	return exe, nil
}

// exeFromZip returns the OpenLAMT.exe of the verified zip bytes and its SHA-256 (hex).
func exeFromZip(zipData []byte) (data []byte, sum string, err error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrNoExe, err)
	}
	var entry *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(path.Base(strings.ReplaceAll(f.Name, `\`, "/")), exeName) && !f.FileInfo().IsDir() {
			entry = f
			break
		}
	}
	if entry == nil {
		return nil, "", ErrNoExe
	}
	src, err := entry.Open()
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrNoExe, err)
	}
	defer src.Close()
	data, err = io.ReadAll(io.LimitReader(src, maxAssetSize+1))
	if err == nil && len(data) > maxAssetSize {
		err = fmt.Errorf("update: %s larger than %d bytes", exeName, maxAssetSize)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrNoExe, err)
	}
	h := sha256.Sum256(data)
	return data, hex.EncodeToString(h[:]), nil
}

// writeExe writes the new exe to a new file in dir.
func writeExe(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "OpenLAMT-update-*.exe")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrReplace, err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("%w: %w", ErrReplace, err)
	}
	return f.Name(), nil
}

func executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// writable tries to create (and delete) a file in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".openlamt-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func startProgram(path string) error {
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runOpener runs an opener (open, xdg-open) and waits at most wait for its answer: exit code 0
// is opened, another one an error. Still running after wait counts as opened.
func runOpener(cmd *exec.Cmd, wait time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return nil
	}
}

// removeLeftover deletes path, retrying tries times every pause; a missing file is done.
func removeLeftover(path string, remove func(string) error, sleep func(time.Duration), tries int, pause time.Duration) error {
	var err error
	for i := range tries {
		if i > 0 {
			sleep(pause)
		}
		err = remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

// psQuote is s as a single-quoted PowerShell string. PowerShell takes the typographic single
// quotes as quotes, too, so each of them is doubled as well.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// replaceScript is the PowerShell script the elevated step runs. It reads the new exe once,
// checks its SHA-256 (a user process could have swapped the file meanwhile), renames the
// running exe to .old and writes the bytes as a new file, so the exe gets the permissions of
// its folder, not those of the user's temp folder. If writing fails, the old exe is put back.
// Exit codes: 0 replaced, 2 not readable, 3 checksum, 4 rename, 5 write.
func replaceScript(newExe, exe, sum string) string {
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$new = " + psQuote(newExe),
		"$exe = " + psQuote(exe),
		"$old = $exe + '" + oldSuffix + "'",
		"try { $b = [IO.File]::ReadAllBytes($new) } catch { exit 2 }",
		"$h = -join ([Security.Cryptography.SHA256]::Create().ComputeHash($b) | ForEach-Object { $_.ToString('x2') })",
		"if ($h -ne " + psQuote(sum) + ") { exit 3 }",
		"try { if ([IO.File]::Exists($old)) { [IO.File]::Delete($old) }; [IO.File]::Move($exe, $old) } catch { exit 4 }",
		"try { [IO.File]::WriteAllBytes($exe, $b) } catch { try { [IO.File]::Delete($exe) } catch {}; [IO.File]::Move($old, $exe); exit 5 }",
		"exit 0",
	}, "\n")
}

// encodeCommand is script for powershell -EncodedCommand: UTF-16LE in base64, so no path
// needs quoting on the command line.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// powershellArgs are the arguments of the elevated PowerShell.
func powershellArgs(newExe, exe, sum string) string {
	return "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodeCommand(replaceScript(newExe, exe, sum))
}
