//go:build windows

package localme

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// openPipe opens the app's password pipe for reading. SECURITY_ANONYMOUS keeps the pipe's
// server from impersonating the elevated helper.
func openPipe(name string) (io.ReadCloser, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING,
		windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}
