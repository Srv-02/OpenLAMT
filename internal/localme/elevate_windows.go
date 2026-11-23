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
// new temporary folder of the user.

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
	exe, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	dir, err := os.MkdirTemp("", "openlamt-check-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, resultFile)
	code, err := runElevated(exe, HelperFlag+" --out "+syscall.EscapeArg(out))
	if err != nil {
		return Result{}, err
	}
	if code != helperOK {
		return Result{}, fmt.Errorf("check: helper exit code %d", code)
	}
	f, err := os.Open(out)
	if err != nil {
		return Result{}, fmt.Errorf("check: %w", err)
	}
	defer f.Close()
	return readAnswer(f)
}

// runElevated starts exe with params through UAC, waits for it and returns its exit code. A
// cancelled UAC prompt is ErrDeclined.
func runElevated(exe, params string) (uint32, error) {
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
		return 0, fmt.Errorf("check: %w", callErr)
	}
	if info.hProcess == 0 {
		return 0, errors.New("check: no helper process")
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("check: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, fmt.Errorf("check: %w", err)
	}
	return code, nil
}
