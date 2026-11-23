//go:build linux || windows

package localme

import (
	"errors"
	"fmt"
	"io"

	"github.com/device-management-toolkit/rpc-go/v2/pkg/activate"
	"github.com/device-management-toolkit/rpc-go/v2/pkg/amt"
	"github.com/device-management-toolkit/rpc-go/v2/pkg/configure"
	"github.com/device-management-toolkit/rpc-go/v2/pkg/utils"
	"github.com/sirupsen/logrus"
)

// The setup actions use rpc-go's public packages as they are (decisions on #11):
//
//   - TurnOn: pkg/configure.EnableAMT, which asks IsChangeToAMTEnabled and sets the operational
//     state over the AMT host interface (HECI); no network, no password.
//   - ActivateCCM: pkg/activate.Local in Client Control Mode. rpc-go talks WS-Man to the
//     firmware through LMS on 127.0.0.1 when it runs, otherwise through LME over HECI. OpenLAMT
//     neither needs LMS nor keeps a connection to it.
//   - Deactivate: the PTHI Unprovision of pkg/amt, which is what rpc-go's deactivate does for
//     Client Control Mode; Admin Control Mode is refused before anything is sent (it would need
//     the admin password, and rpc-go would ask for it on stdin).
//
// This file is the only one of the package that may change Intel AMT (TestHelperNeverWrites).

// meiChanger is the changer over rpc-go.
type meiChanger struct{}

func newChanger() changer {
	logrus.SetOutput(io.Discard) // the helper's only output is its answer
	utils.PR = noPrompt{}        // rpc-go must never wait for a password on stdin
	return meiChanger{}
}

// errPrompt: rpc-go asked for a password on stdin; the helper never answers such a prompt.
var errPrompt = errors.New("rpc-go asked for a password")

// noPrompt is rpc-go's password reader in the helper: every prompt fails at once.
type noPrompt struct{}

func (noPrompt) ReadPassword() (string, error) { return "", errPrompt }
func (noPrompt) ReadPasswordWithConfirmation(string, string) (string, error) {
	return "", errPrompt
}

func (meiChanger) TurnOn() error { return short(configure.EnableAMT(configure.BaseOptions{})) }

func (meiChanger) ActivateCCM(password string) error {
	if password == "" { // rpc-go would ask for one on stdin
		return ErrPassword
	}
	return short(activate.Local(activate.LocalOptions{Mode: activate.ModeCCM, AMTPassword: password}))
}

// controlModeCCM is the control mode of Client Control Mode in the AMT host interface.
const controlModeCCM = 1

func (meiChanger) Deactivate() error {
	c := amt.NewAMTCommand()
	mode, err := c.GetControlMode()
	if err != nil {
		return short(err)
	}
	if mode != controlModeCCM {
		return fmt.Errorf("control mode %d is not Client Control Mode", mode)
	}
	status, err := c.Unprovision()
	if err != nil {
		return short(err)
	}
	if status != 0 {
		return fmt.Errorf("unprovision status %d", status)
	}
	return nil
}

// short replaces rpc-go's coded errors ("Error 110: DeactivationFailed") by their message.
func short(err error) error {
	if ce, ok := err.(utils.CustomError); ok { // only the error itself, not one it wraps
		if ce.Details != "" {
			return errors.New(ce.Message + ": " + ce.Details)
		}
		return errors.New(ce.Message)
	}
	return err
}
