//go:build windows

package localme

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The password of a CCM activation reaches the elevated helper through a named pipe, because
// runas gives the helper no stdin:
//
//   - the app creates the pipe under a random name (128 bits), as the first and only instance
//     (FILE_FLAG_FIRST_PIPE_INSTANCE, one instance): nobody can create the name before or
//     beside it, and only one client can connect;
//   - remote clients are rejected, the pipe is outbound only;
//   - its DACL allows the current user and the Administrators group, nobody else. The user's
//     SID covers the usual elevation (same user, elevated token). Administrators is needed for
//     the over-the-shoulder elevation, where a standard user types an administrator's
//     credentials and the helper runs as that other account; an elevated administrator can read
//     the app's memory anyway, so it gains nothing. SYSTEM is not granted;
//   - the app accepts the client only if its process ID is the helper it started
//     (GetNamedPipeClientProcessId), sends the password once and closes the pipe;
//   - the helper opens the pipe with SECURITY_ANONYMOUS, so the pipe's server cannot
//     impersonate it.

// pipeConnectTimeout bounds the wait for the helper to connect; it runs already when
// ShellExecuteEx returns.
const pipeConnectTimeout = 30 * time.Second

// newPasswordPipe creates the password pipe and returns its name and server handle.
func newPasswordPipe() (string, windows.Handle, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", 0, err
	}
	name := `\\.\pipe\openlamt-` + hex.EncodeToString(b[:])
	h, err := createPipeNamed(name)
	return name, h, err
}

// createPipeNamed creates the first and only instance of the password pipe name.
func createPipeNamed(name string) (windows.Handle, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return 0, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")(A;;GR;;;BA)")
	if err != nil {
		return 0, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateNamedPipe(p,
		windows.PIPE_ACCESS_OUTBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 512, 512, 0, &sa)
	if err != nil {
		return 0, fmt.Errorf("password pipe: %w", err)
	}
	return h, nil
}

var (
	errHelperGone   = errors.New("the helper ended before it took the password")
	errPipeTimeout  = errors.New("the helper did not take the password in time")
	errForeignPipe  = errors.New("another process connected to the password pipe")
	errShortPwWrite = errors.New("the password was not sent completely")
)

// servePassword waits until the process proc connects to pipe, checks that it is proc, and
// sends password once. It returns when the helper has read it, ended, or timeout ran out.
func servePassword(pipe, proc windows.Handle, password []byte, timeout time.Duration) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	ov := windows.Overlapped{HEvent: ev}
	switch err := windows.ConnectNamedPipe(pipe, &ov); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
	case errors.Is(err, windows.ERROR_IO_PENDING):
		w, err := windows.WaitForMultipleObjects([]windows.Handle{ev, proc}, false, uint32(timeout.Milliseconds()))
		if err != nil || w != windows.WAIT_OBJECT_0 {
			windows.CancelIoEx(pipe, &ov)
			var n uint32
			windows.GetOverlappedResult(pipe, &ov, &n, true) // ov must outlive the cancelled call
			switch {
			case err != nil:
				return err
			case w == windows.WAIT_OBJECT_0+1:
				return errHelperGone
			}
			return errPipeTimeout
		}
		var n uint32
		if err := windows.GetOverlappedResult(pipe, &ov, &n, false); err != nil {
			return err
		}
	default:
		return err
	}
	var client uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &client); err != nil {
		windows.DisconnectNamedPipe(pipe)
		return err
	}
	want, err := windows.GetProcessId(proc)
	if err != nil || client != want {
		windows.DisconnectNamedPipe(pipe)
		return errForeignPipe
	}
	buf := append([]byte(nil), password...)
	defer clear(buf)
	ov = windows.Overlapped{HEvent: ev}
	windows.ResetEvent(ev)
	var n uint32
	err = windows.WriteFile(pipe, buf, &n, &ov)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(pipe, &ov, &n, true)
	}
	if err != nil {
		return err
	}
	if int(n) != len(buf) {
		return errShortPwWrite
	}
	// Wait until the helper has read the password; the caller closes the pipe afterwards.
	return windows.FlushFileBuffers(pipe)
}
