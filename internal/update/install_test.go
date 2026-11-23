package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// winZip writes a release zip holding name with content to dir and returns its path.
func winZip(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "OpenLAMT-v0.2.0-windows-x64.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// winInstaller is a Windows installer over a fake program folder: the "running" exe is
// <app>/OpenLAMT.exe with the content "old"; file operations are real, elevation and start fake.
type winInstaller struct {
	Installer
	app, exe  string
	elevated  []string // newExe, exe, sum of the elevated call
	elevateTo error    // what the fake elevation returns
	started   []string
}

func newWinInstaller(t *testing.T, writable bool) *winInstaller {
	t.Helper()
	w := &winInstaller{app: t.TempDir()}
	w.exe = filepath.Join(w.app, "OpenLAMT.exe")
	if err := os.WriteFile(w.exe, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.Installer = Installer{
		GOOS:       "windows",
		Executable: func() (string, error) { return w.exe, nil },
		Writable:   func(string) bool { return writable },
		TempDir:    func() (string, error) { return os.MkdirTemp(t.TempDir(), "upd-") },
		Rename:     os.Rename,
		Remove:     os.Remove,
		Elevate: func(newExe, exe, sum string) error {
			w.elevated = []string{newExe, exe, sum}
			if w.elevateTo != nil {
				return w.elevateTo
			}
			b, err := os.ReadFile(newExe)
			if err != nil {
				return err
			}
			if err := os.Rename(exe, exe+".old"); err != nil {
				return err
			}
			return os.WriteFile(exe, b, 0o600)
		},
		Start: func(p string) error { w.started = append(w.started, p); return nil },
		Open:  func(string) error { t.Fatal("Windows opens no package"); return nil },
	}
	return w
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return string(b)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestInstallMacOpensTheDMG(t *testing.T) {
	var opened []string
	in := Installer{GOOS: "darwin", Open: func(p string) error { opened = append(opened, p); return nil }}
	restart, err := in.Install("/home/user/Downloads/OpenLAMT-v0.2.0-macos-arm64.dmg")
	if err != nil || restart != "" || len(opened) != 1 || opened[0] != "/home/user/Downloads/OpenLAMT-v0.2.0-macos-arm64.dmg" {
		t.Fatalf("restart %q, err %v, opened %v", restart, err, opened)
	}
	in.Open = func(string) error { return errors.New("exit status 1") }
	if _, err := in.Install("x.dmg"); !errors.Is(err, ErrOpen) {
		t.Fatalf("a failed open must be ErrOpen: %v", err)
	}
}

func TestInstallLinuxOpensTheDeb(t *testing.T) {
	var opened []string
	in := Installer{GOOS: "linux", Open: func(p string) error { opened = append(opened, p); return nil }}
	restart, err := in.Install("/home/user/Downloads/OpenLAMT-v0.2.0-linux-x64.deb")
	if err != nil || restart != "" || len(opened) != 1 {
		t.Fatalf("restart %q, err %v, opened %v", restart, err, opened)
	}
	in.Open = func(string) error { return &exec.ExitError{} }
	if _, err := in.Install("x.deb"); !errors.Is(err, ErrOpen) {
		t.Fatalf("no handler must be ErrOpen: %v", err)
	}
}

func TestInstallUnknownPlatformFails(t *testing.T) {
	if _, err := (Installer{GOOS: "plan9"}).Install("x"); err == nil {
		t.Fatal("no installer on plan9")
	}
}

func TestInstallWindowsReplacesInAWritableFolder(t *testing.T) {
	w := newWinInstaller(t, true)
	zipPath := winZip(t, t.TempDir(), "OpenLAMT.exe", []byte("new"))
	if err := os.WriteFile(w.exe+".old", []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	restart, err := w.Install(zipPath)
	if err != nil || restart != w.exe {
		t.Fatalf("restart %q, err %v", restart, err)
	}
	if got := readFile(t, w.exe); got != "new" {
		t.Errorf("exe holds %q", got)
	}
	if got := readFile(t, w.exe+".old"); got != "old" {
		t.Errorf(".old holds %q, want the running exe", got)
	}
	if got := names(t, w.app); len(got) != 2 {
		t.Errorf("folder holds %v, want the exe and .old only", got)
	}
	if w.elevated != nil || w.started != nil {
		t.Errorf("elevated %v, started %v: nothing before the quit", w.elevated, w.started)
	}
	if err := w.Restart(restart); err != nil || len(w.started) != 1 || w.started[0] != w.exe {
		t.Fatalf("restart started %v, %v", w.started, err)
	}
}

func TestInstallWindowsRenameFailureRestores(t *testing.T) {
	t.Run("running exe not renamed", func(t *testing.T) {
		w := newWinInstaller(t, true)
		zipPath := winZip(t, t.TempDir(), "OpenLAMT.exe", []byte("new"))
		w.Rename = func(string, string) error { return os.ErrPermission }
		if _, err := w.Install(zipPath); !errors.Is(err, ErrReplace) {
			t.Fatalf("err %v, want ErrReplace", err)
		}
		if got := names(t, w.app); len(got) != 1 || readFile(t, w.exe) != "old" {
			t.Fatalf("folder %v, exe %q: the new exe must be removed", got, readFile(t, w.exe))
		}
	})
	t.Run("new exe not moved in", func(t *testing.T) {
		w := newWinInstaller(t, true)
		zipPath := winZip(t, t.TempDir(), "OpenLAMT.exe", []byte("new"))
		calls := 0
		w.Rename = func(a, b string) error {
			calls++
			if calls == 2 {
				return os.ErrPermission
			}
			return os.Rename(a, b)
		}
		if _, err := w.Install(zipPath); !errors.Is(err, ErrReplace) {
			t.Fatalf("err %v, want ErrReplace", err)
		}
		if got := names(t, w.app); len(got) != 1 || readFile(t, w.exe) != "old" {
			t.Fatalf("folder %v: the old exe must be back in place", got)
		}
	})
}

func TestInstallWindowsElevatesInAProtectedFolder(t *testing.T) {
	w := newWinInstaller(t, false)
	zipPath := winZip(t, t.TempDir(), "OpenLAMT.exe", []byte("new"))
	restart, err := w.Install(zipPath)
	if err != nil || restart != w.exe {
		t.Fatalf("restart %q, err %v", restart, err)
	}
	sum := sha256.Sum256([]byte("new"))
	if len(w.elevated) != 3 || w.elevated[1] != w.exe || w.elevated[2] != hex.EncodeToString(sum[:]) {
		t.Fatalf("elevated with %v", w.elevated)
	}
	if filepath.Dir(w.elevated[0]) == w.app {
		t.Error("the new exe must not be written into the protected folder")
	}
	if _, err := os.Stat(filepath.Dir(w.elevated[0])); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary folder must be removed: %v", err)
	}
	if got := readFile(t, w.exe); got != "new" {
		t.Errorf("exe holds %q", got)
	}
}

func TestInstallWindowsElevationDeclinedOrFailed(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want error
	}{
		"declined": {ErrDeclined, ErrDeclined},
		"failed":   {errors.New("exit 5"), ErrReplace},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWinInstaller(t, false)
			w.elevateTo = c.err
			zipPath := winZip(t, t.TempDir(), "OpenLAMT.exe", []byte("new"))
			restart, err := w.Install(zipPath)
			if !errors.Is(err, c.want) || restart != "" {
				t.Fatalf("restart %q, err %v, want %v", restart, err, c.want)
			}
			if got := readFile(t, w.exe); got != "old" {
				t.Errorf("exe holds %q", got)
			}
		})
	}
}

func TestInstallWindowsBadArchive(t *testing.T) {
	w := newWinInstaller(t, true)
	dir := t.TempDir()
	noExe := winZip(t, dir, "README.txt", []byte("x"))
	if _, err := w.Install(noExe); !errors.Is(err, ErrNoExe) {
		t.Fatalf("zip without exe: %v", err)
	}
	broken := filepath.Join(dir, "broken.zip")
	if err := os.WriteFile(broken, []byte("no zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Install(broken); !errors.Is(err, ErrNoExe) {
		t.Fatalf("broken zip: %v", err)
	}
	if got := names(t, w.app); len(got) != 1 || readFile(t, w.exe) != "old" {
		t.Fatalf("folder %v", got)
	}
}

func TestRemoveLeftoverRetriesWhileTheOldExeRuns(t *testing.T) {
	busy := 3
	var removed []string
	var slept int
	remove := func(p string) error {
		removed = append(removed, p)
		if busy > 0 {
			busy--
			return os.ErrPermission
		}
		return nil
	}
	if err := removeLeftover(`C:\Apps\OpenLAMT.exe.old`, remove, func(time.Duration) { slept++ }, 10, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 4 || slept != 3 {
		t.Fatalf("removed %d times, slept %d", len(removed), slept)
	}
	if err := removeLeftover("x", func(string) error { return os.ErrNotExist }, func(time.Duration) { t.Fatal("no wait") }, 10, time.Second); err != nil {
		t.Fatalf("a missing file is done: %v", err)
	}
	if err := removeLeftover("x", func(string) error { return os.ErrPermission }, func(time.Duration) {}, 3, time.Second); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("gives up with the last error: %v", err)
	}
}

func TestRemoveLeftoverDeletesTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "OpenLAMT.exe.old")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeLeftover(p, os.Remove, func(time.Duration) {}, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
}

func decodeCommand(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("not UTF-16LE base64: %v", err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestReplaceScript(t *testing.T) {
	newExe := `C:\Users\o'brien\AppData\Local\Temp\upd\OpenLAMT-update-1.exe`
	exe := `C:\Program Files\Bob’s Tools\OpenLAMT.exe`
	args := powershellArgs(newExe, exe, "abc123")
	enc, ok := strings.CutPrefix(args, "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand ")
	if !ok {
		t.Fatalf("args %q", args)
	}
	script := decodeCommand(t, enc)
	for _, want := range []string{
		`$new = 'C:\Users\o''brien\AppData\Local\Temp\upd\OpenLAMT-update-1.exe'`,
		`$exe = 'C:\Program Files\Bob’’s Tools\OpenLAMT.exe'`,
		`if ($h -ne 'abc123') { exit 3 }`,
		`[IO.File]::Move($exe, $old)`,
		`[IO.File]::WriteAllBytes($exe, $b)`,
		`[IO.File]::Move($old, $exe); exit 5`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

// TestOpenerHelper is the opener the runOpener tests start: it exits with $OPENER_EXIT, or
// sleeps with $OPENER_SLEEP.
func TestOpenerHelper(t *testing.T) {
	code := os.Getenv("OPENER_EXIT")
	if code == "" {
		t.Skip("helper process")
	}
	if os.Getenv("OPENER_SLEEP") != "" {
		time.Sleep(10 * time.Second)
	}
	n, _ := strconv.Atoi(code)
	os.Exit(n)
}

func helperOpener(exit string, sleep bool) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpenerHelper$")
	cmd.Env = append(os.Environ(), "OPENER_EXIT="+exit)
	if sleep {
		cmd.Env = append(cmd.Env, "OPENER_SLEEP=1")
	}
	return cmd
}

func TestRunOpener(t *testing.T) {
	if err := runOpener(helperOpener("0", false), time.Minute); err != nil {
		t.Errorf("exit 0: %v", err)
	}
	var ee *exec.ExitError
	if err := runOpener(helperOpener("4", false), time.Minute); !errors.As(err, &ee) || ee.ExitCode() != 4 {
		t.Errorf("exit 4 (xdg-open: no handler): %v", err)
	}
	start := time.Now()
	if err := runOpener(helperOpener("1", true), 200*time.Millisecond); err != nil {
		t.Errorf("an opener still running counts as opened: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("runOpener must not wait for a running opener")
	}
	if err := runOpener(exec.Command(filepath.Join(t.TempDir(), "no-such-opener")), time.Minute); err == nil {
		t.Error("a missing opener is an error")
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 2000) // small enough for a Content-Length
	srv := assetServer(t, content)
	var last, total int64
	calls := 0
	c := Checker{Progress: func(done, all int64) { calls++; last, total = done, all }}
	if _, err := c.Download(context.Background(), Asset{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: srv.URL + "/pkg", Size: 2000}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if calls == 0 || last != 2000 || total != 2000 {
		t.Fatalf("calls %d, last %d of %d", calls, last, total)
	}
	// Without the asset's size the answer's Content-Length counts.
	if _, err := c.Download(context.Background(), Asset{Name: "OpenLAMT-v0.2.0-linux-x64.deb", URL: srv.URL + "/pkg"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if last != 2000 || total != 2000 {
		t.Fatalf("last %d of %d", last, total)
	}
}

func TestNewInstallerIsTheRunningSystem(t *testing.T) {
	in := NewInstaller()
	if in.GOOS != runtime.GOOS || in.Open == nil || in.Executable == nil || in.Writable == nil || in.TempDir == nil ||
		in.Rename == nil || in.Remove == nil || in.Elevate == nil || in.Start == nil {
		t.Fatalf("installer %+v", in)
	}
	if !writable(t.TempDir()) || writable(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("writable")
	}
}
