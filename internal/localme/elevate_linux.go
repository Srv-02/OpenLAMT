//go:build linux

package localme

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pkexec exits with 126 when the authentication dialog was dismissed and with 127 when the
// user is not authorized or could not authenticate.
const (
	pkexecDismissed     = 126
	pkexecNotAuthorized = 127
)

func platformCheck() (Result, error) {
	pk, err := exec.LookPath("pkexec")
	if err != nil {
		return Result{}, ErrNoPkexec
	}
	prog, err := helperProgram()
	if err != nil {
		return Result{}, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(pk, prog, HelperFlag)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return pkexecAnswer(cmd.Run(), &stdout, stderr.String())
}

// pkexecAnswer turns the end of pkexec into the Result or an error.
func pkexecAnswer(runErr error, stdout *bytes.Buffer, stderr string) (Result, error) {
	var exit *exec.ExitError
	switch {
	case errors.As(runErr, &exit) && (exit.ExitCode() == pkexecDismissed || exit.ExitCode() == pkexecNotAuthorized):
		return Result{}, ErrDeclined
	case runErr != nil:
		if s := strings.TrimSpace(stderr); s != "" {
			return Result{}, fmt.Errorf("check: %w: %s", runErr, s)
		}
		return Result{}, fmt.Errorf("check: %w", runErr)
	}
	return readAnswer(stdout)
}

// helperProgram is the program pkexec starts: the AppImage file when the app runs from one
// (root may not see the AppImage's FUSE mount), else this executable.
func helperProgram() (string, error) {
	if p := os.Getenv("APPIMAGE"); p != "" && filepath.IsAbs(p) {
		return p, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
