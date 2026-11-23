package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// elevateWait bounds the elevated replacement, the UAC prompt included.
const elevateWait = 5 * time.Minute

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
	swHide                = 0
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// openFile is not used on Windows: the update replaces the exe there.
func openFile(path string) error {
	return errors.New("update: Windows replaces the program instead of opening the package")
}

// powershellPath is Windows PowerShell in the system folder; no PATH lookup for an elevated
// program.
func powershellPath() (string, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

// elevateReplace runs the replacement script (replaceScript) in PowerShell as administrator:
// ShellExecuteEx with the verb "runas" shows the UAC prompt. A declined prompt is ErrDeclined,
// a script that does not end with 0 an error.
func elevateReplace(newExe, exe, sum string) error {
	ps, err := powershellPath()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(ps)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(powershellArgs(newExe, exe, sum))
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return ErrDeclined
		}
		return fmt.Errorf("update: ShellExecuteEx: %w", callErr)
	}
	if info.hProcess == 0 {
		return errors.New("update: the elevated replacement did not start")
	}
	defer windows.CloseHandle(info.hProcess)
	ev, err := windows.WaitForSingleObject(info.hProcess, uint32(elevateWait/time.Millisecond))
	if err != nil {
		return err
	}
	if ev != windows.WAIT_OBJECT_0 {
		return errors.New("update: the elevated replacement did not finish in time")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("update: the elevated replacement ended with %d", code)
	}
	return nil
}

// RemoveLeftover deletes OpenLAMT.exe.old next to the running exe, which an update left. The
// old instance may still be quitting, so it tries for a while. An .old in a folder only an
// administrator can write to stays; the next elevated update deletes it.
func RemoveLeftover() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	return removeLeftover(exe+oldSuffix, os.Remove, time.Sleep, leftoverTries, leftoverEvery)
}
