//go:build !windows

package localme

import (
	"errors"
	"io"
)

// openPipe: only the Windows helper reads its password from a named pipe; elsewhere it comes on
// stdin.
func openPipe(string) (io.ReadCloser, error) { return nil, errors.ErrUnsupported }
