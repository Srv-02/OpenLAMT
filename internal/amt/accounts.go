package amt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	wsmanamt "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt"
)

// AccountChange is a new name and/or password of the AMT admin account.
type AccountChange struct {
	Name     string // "" keeps the current name
	Password string // always required: AMT stores MD5(name:realm:password)
}

// Validate checks the change without device access: name and password rules of UserSpec.
func (c AccountChange) Validate() error {
	if c.Name != "" {
		if err := validateName(c.Name); err != nil {
			return err
		}
	}
	return validatePassword(c.Password)
}

// LoginChange describes an account change the device accepted.
type LoginChange struct {
	OldUsername string // the account's name before the change
	Username    string // the account's name after the change
	// Self: the account of this connection. The device already uses the new login for every
	// following request and for new KVM, SOL and IDE-R sessions.
	Self bool
}

// ErrCredentialsUncertain: a change of name or password failed without an answer from AMT, and
// a test login with the new credentials did not settle whether it was applied. The concrete
// error is *CredentialsUncertainError. The connection keeps the old login; the caller must not
// discard the new password, because it may be the valid one now.
var ErrCredentialsUncertain = errors.New("unknown whether the device accepted the new login")

// CredentialsUncertainError names the account whose login is uncertain. It never contains the
// password.
type CredentialsUncertainError struct {
	Username string // the new name
	Self     bool   // the account of this connection
	Err      error
}

func (e *CredentialsUncertainError) Error() string {
	return fmt.Sprintf("%v for %s: %v", ErrCredentialsUncertain, e.Username, e.Err)
}

func (e *CredentialsUncertainError) Is(target error) bool { return target == ErrCredentialsUncertain }
func (e *CredentialsUncertainError) Unwrap() error        { return e.Err }

// errNoProbe: the device has no transport of its own (test client), so no test login is possible.
var errNoProbe = errors.New("no transport for a test login")

// isUnauthorized reports the library's error for an HTTP 401 answer.
func isUnauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "wsman.Client post received: 401")
}

// loginWorks logs in as user with password through a separate client on the same transport
// and pin. Any answer except 401 (also a SOAP fault such as AccessDenied) proves the login.
func (d *Device) loginWorks(user, password string) (bool, error) {
	if d.transport == nil {
		return false, errNoProbe
	}
	cfg := d.cfg
	cfg.Username, cfg.Password = user, password
	_, err := wsmanamt.NewMessages(newClient(cfg, d.transport)).GeneralSettings.Get()
	var fault *amterror.AMTError
	switch {
	case err == nil || errors.As(err, &fault):
		return true, nil
	case isUnauthorized(err):
		return false, nil
	}
	return false, err
}

// finishLogin completes an account change. reqErr is the error of the change request without an
// AMT answer (nil if AMT answered ReturnValue 0; the WS-Man client may have resent the request
// after a connection reset, so it may have been applied). On such an error a test login with the
// new credentials decides: it works → the change was applied; 401 → it was not (reqErr is
// returned); anything else → *CredentialsUncertainError. An applied change of this connection's
// own login switches the client to it.
func (d *Device) finishLogin(op string, res LoginChange, password string, reqErr error) (LoginChange, error) {
	if reqErr != nil {
		ok, err := d.loginWorks(res.Username, password)
		switch {
		case err != nil:
			return LoginChange{}, &CredentialsUncertainError{Username: res.Username, Self: res.Self, Err: writeError(op, reqErr)}
		case !ok:
			return LoginChange{}, writeError(op, reqErr)
		}
	}
	if res.Self {
		cfg := d.cfg
		cfg.Username, cfg.Password = res.Username, password
		d.relogin(cfg, d.fingerprint)
	}
	return res, nil
}

// SetAdminAccount changes name and/or password of the AMT admin account (SetAdminAclEntryEx).
// It needs confirm for OpAdminAccount: a lost admin password can only be reset at the device
// (MEBx). AMT receives only the digest hash and cannot check the password rules; Validate does.
// If this connection is logged in as admin, it continues with the new login.
func (d *Device) SetAdminAccount(c AccountChange, confirm Confirmation) (LoginChange, error) {
	const op = "set admin account"
	if err := c.Validate(); err != nil {
		return LoginChange{}, err
	}
	if err := d.confirmed(confirm, OpAdminAccount, RiskAdmin); err != nil {
		return LoginChange{}, err
	}
	a, err := d.msgs.AMT.AuthorizationService.GetAdminACLEntry()
	if err != nil {
		return LoginChange{}, writeError(op, err)
	}
	if _, err := authOutput(a.Message, "GetAdminAclEntry"); err != nil {
		return LoginChange{}, err
	}
	cur := strings.TrimSpace(a.Body.GetAdminResponse.Username)
	if cur == "" {
		return LoginChange{}, fmt.Errorf("%s: %w: admin user name", op, ErrIncompleteResponse)
	}
	realm, err := d.digestRealm()
	if err != nil {
		return LoginChange{}, writeError(op, err)
	}
	res := LoginChange{OldUsername: cur, Username: cur, Self: strings.EqualFold(cur, d.cfg.Username)}
	if c.Name != "" {
		res.Username = c.Name
	}
	r, err := d.msgs.AMT.AuthorizationService.SetAdminAclEntryEx(res.Username, digestPassword(res.Username, realm, c.Password))
	if err == nil {
		if _, err := authOutput(r.Message, "SetAdminAclEntryEx"); err != nil {
			return LoginChange{}, err
		}
	} else if fault := (*amterror.AMTError)(nil); errors.As(err, &fault) {
		return LoginChange{}, writeError(op, err) // AMT answered: nothing changed
	}
	return d.finishLogin(op, res, c.Password, err)
}
