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

// lookPkexec finds pkexec; tests replace it with a fake elevator.
var lookPkexec = func() (string, error) { return exec.LookPath("pkexec") }

func platformCheck() (Result, error) {
	out, err := runPkexec([]string{HelperFlag}, nil)
	if err != nil {
		return Result{}, err
	}
	return readAnswer(out)
}

// platformAction runs the action helper under pkexec. pkexec hands its stdin to the program
// it starts, like stdout: the password of an activation goes there, never into the arguments.
func platformAction(a Action, password []byte) (ActionResult, error) {
	out, err := runPkexec([]string{string(a)}, password)
	if err != nil {
		return ActionResult{}, err
	}
	return readActionAnswer(out)
}

// runPkexec starts the helper with args through pkexec, writes stdin to it (nil: none) and
// returns what it printed.
func runPkexec(args []string, stdin []byte) (*bytes.Buffer, error) {
	pk, err := lookPkexec()
	if err != nil {
		return nil, ErrNoPkexec
	}
	prog, err := helperProgram()
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(pk, append([]string{prog}, args...)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	return pkexecAnswer(cmd.Run(), &stdout, stderr.String())
}

// pkexecAnswer turns the end of pkexec into the helper's output or an error.
func pkexecAnswer(runErr error, stdout *bytes.Buffer, stderr string) (*bytes.Buffer, error) {
	var exit *exec.ExitError
	switch {
	case errors.As(runErr, &exit) && (exit.ExitCode() == pkexecDismissed || exit.ExitCode() == pkexecNotAuthorized):
		return nil, ErrDeclined
	case runErr != nil:
		if s := strings.TrimSpace(stderr); s != "" {
			return nil, fmt.Errorf("local helper: %w: %s", runErr, s)
		}
		return nil, fmt.Errorf("local helper: %w", runErr)
	}
	return stdout, nil
}

// helperProgram is the program pkexec starts: the AppImage file when the app runs from one
// (root may not see the AppImage's FUSE mount), else this executable. Tests replace it.
var helperProgram = func() (string, error) {
	if p := os.Getenv("APPIMAGE"); p != "" && filepath.IsAbs(p) {
		return p, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
