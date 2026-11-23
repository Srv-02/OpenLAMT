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

// Run carries out a setup action through an elevated helper, like Check, and returns its answer:
// the refusal, if any (ActionResult.Err), and the state read afterwards. password is the new
// admin password of ActionActivateCCM (nil for the others); it is checked first, handed to the
// helper on stdin (Linux) or through a named pipe (Windows), never as an argument, and wiped
// when Run returns.
func Run(a Action, password []byte) (ActionResult, error) {
	defer clear(password)
	switch a {
	case ActionActivateCCM:
		if err := ValidatePassword(password); err != nil {
			return ActionResult{}, err
		}
	case ActionTurnOn, ActionDeactivate:
		password = nil
	default:
		return ActionResult{}, errors.New("unknown setup action")
	}
	return platformAction(a, password)
}

// readAnswer reads at most maxResultSize+1 bytes of the helper's answer and decodes it.
func readAnswer(r io.Reader) (Result, error) {
	b, err := readLimited(r)
	if err != nil {
		return Result{}, err
	}
	return Decode(b)
}

// readActionAnswer reads an action helper's answer like readAnswer.
func readActionAnswer(r io.Reader) (ActionResult, error) {
	b, err := readLimited(r)
	if err != nil {
		return ActionResult{}, err
	}
	return decodeAction(b)
}

func readLimited(r io.Reader) ([]byte, error) {
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(r, maxResultSize+1)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
