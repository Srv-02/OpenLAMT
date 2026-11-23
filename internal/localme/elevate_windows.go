//go:build windows

package localme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The elevated helper has no stdout the app could read: ShellExecuteEx with the verb "runas"
// starts it after the UAC prompt, the app waits for it and reads the answer from a file in a
// new temporary folder of the user. runas cannot hand the helper a stdin either: the password
// of an activation goes through a named pipe the app serves (servePassword).

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swHide                = 0
)

func platformCheck() (Result, error) {
	var r Result
	err := runHelperFile(HelperFlag, nil, func(f *os.File) (err error) {
		r, err = readAnswer(f)
		return err
	})
	return r, err
}

func platformAction(a Action, password []byte) (ActionResult, error) {
	var r ActionResult
	err := runHelperFile(string(a), password, func(f *os.File) (err error) {
		r, err = readActionAnswer(f)
		return err
	})
	return r, err
}

// runHelperFile runs the helper with flag through UAC, lets it write its answer to a new
// temporary folder and hands the answer file to read. A non-nil password is served through a
// new pipe whose name the helper gets with --pipe.
func runHelperFile(flag string, password []byte, read func(*os.File) error) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "openlamt-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, resultFile)
	params := flag
	var serve func(windows.Handle) error
	if password != nil {
		name, pipe, err := newPasswordPipe()
		if err != nil {
			return err
		}
		closed := false
		closePipe := func() {
			if !closed {
				closed = true
				windows.CloseHandle(pipe)
			}
		}
		defer closePipe() // the helper never started
		params += " --pipe " + name
		serve = func(proc windows.Handle) error {
			// Closing the pipe ends the password for the helper, which reads up to the end.
			defer closePipe()
			return servePassword(pipe, proc, password, pipeConnectTimeout)
		}
	}
	code, err := runElevated(exe, params+" --out "+syscall.EscapeArg(out), serve)
	if err != nil {
		return err
	}
	if code != helperOK {
		return fmt.Errorf("local helper: exit code %d", code)
	}
	f, err := os.Open(out)
	if err != nil {
		return fmt.Errorf("local helper: %w", err)
	}
	defer f.Close()
	return read(f)
}

// runElevated starts exe with params through UAC, runs serve (may be nil) with the started
// process before it waits for it, and returns its exit code. A cancelled UAC prompt is
// ErrDeclined.
func runElevated(exe, params string, serve func(windows.Handle) error) (uint32, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	args, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return 0, err
	}
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess | seeMaskNoAsync, lpVerb: verb, lpFile: file, lpParameters: args, nShow: swHide}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return 0, ErrDeclined
		}
		return 0, fmt.Errorf("local helper: %w", callErr)
	}
	if info.hProcess == 0 {
		return 0, errors.New("local helper: no helper process")
	}
	defer windows.CloseHandle(info.hProcess)
	var serveErr error
	if serve != nil {
		serveErr = serve(info.hProcess)
	}
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("local helper: %w", err)
	}
	if serveErr != nil {
		return 0, fmt.Errorf("local helper: %w", serveErr)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, fmt.Errorf("local helper: %w", err)
	}
	return code, nil
}
