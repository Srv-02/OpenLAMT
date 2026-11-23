package localme

import (
	"bytes"
	"errors"
	"io"
)

var (
	// ErrDeclined: the administrator rights were not granted (the prompt was cancelled or
	// the password was wrong). Nothing was read and nothing changed.
	ErrDeclined = errors.New("administrator rights were not granted")
	// ErrNoPkexec: Linux without pkexec (polkit), which asks for the administrator rights.
	ErrNoPkexec = errors.New("pkexec not found")
)

// Check reads Intel AMT through an elevated helper: the operating system asks for
// administrator rights (pkexec on Linux, UAC on Windows), the helper answers and exits. The
// app itself never runs elevated. Never offered on macOS.
func Check() (Result, error) { return platformCheck() }

// readAnswer reads at most maxResultSize+1 bytes of the helper's answer and decodes it.
func readAnswer(r io.Reader) (Result, error) {
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(r, maxResultSize+1)); err != nil {
		return Result{}, err
	}
	return Decode(b.Bytes())
}
