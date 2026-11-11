package session

import (
	"errors"

	"github.com/Srv-02/openlamt/internal/amt"
)

// LoginStore keeps what OpenLAMT saved about a device: passwords and pins in the OS keychain,
// user names in the favorites (*favorites.Manager implements it). Both methods only replace
// entries that exist.
type LoginStore interface {
	// ChangeLogin follows an accepted change of account oldUser on host to newUser/newPassword.
	ChangeLogin(host, oldUser, newUser, newPassword string) error
	// ChangeFingerprint replaces the stored pin of host/user after a certificate change.
	ChangeFingerprint(host, user, fingerprint string) error
}

// ErrNotSaved: the device accepted the change and the session already uses it, but storing it
// in the keychain or the favorites failed. The concrete error is *NotSavedError.
var ErrNotSaved = errors.New("the device accepted the change, but OpenLAMT could not save it")

// NotSavedError hands the new login or pin to the UI once, so it is not lost when the keychain
// fails: the UI shows Password to the user (who must note it) and then drops the error. Error()
// never contains the password.
type NotSavedError struct {
	Username    string // the account's new name
	Password    string // the new password; "" for a pin change
	Fingerprint string // the new pin; "" for a login change
	Err         error
}

func (e *NotSavedError) Error() string        { return ErrNotSaved.Error() + ": " + e.Err.Error() }
func (e *NotSavedError) Is(target error) bool { return target == ErrNotSaved }
func (e *NotSavedError) Unwrap() error        { return e.Err }

// SetAdminAccount changes name and/or password of the AMT admin account; it needs a
// confirmation for amt.OpAdminAccount. After the device accepted it, the session continues with
// the new login if it is logged in as admin (Config returns it), and store (nil: nothing saved)
// follows the change for host/old admin name: keychain password, pin and favorite. A failure of
// store returns *NotSavedError; the change on the device stands. A request without answer whose
// effect could not be tested returns *amt.CredentialsUncertainError: the session keeps the old
// login and the UI must keep the new password.
func (s *Session) SetAdminAccount(c amt.AccountChange, confirm amt.Confirmation, store LoginStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.dev.SetAdminAccount(c, confirm)
	if err != nil {
		return classify(err)
	}
	return s.loginChanged(res, c.Password, store)
}

// UpdateUser replaces name, password, access and realms of an account. The session's own
// account needs a confirmation for amt.OpOwnAccount; the session then continues with the new
// login. Saving and errors as SetAdminAccount.
func (s *Session) UpdateUser(handle int, u amt.UserSpec, confirm amt.Confirmation, store LoginStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.dev.UpdateUser(handle, u, confirm)
	if err != nil {
		return classify(err)
	}
	return s.loginChanged(res, u.Password, store)
}

// loginChanged takes over the new login of the session's own account and lets store follow
// the change. Call it under s.mu.
func (s *Session) loginChanged(res amt.LoginChange, password string, store LoginStore) error {
	s.cfgMu.Lock()
	host := s.cfg.Host
	if res.Self {
		s.cfg.Username, s.cfg.Password = res.Username, password
	}
	s.cfgMu.Unlock()
	if store == nil {
		return nil
	}
	if err := store.ChangeLogin(host, res.OldUsername, res.Username, password); err != nil {
		return &NotSavedError{Username: res.Username, Password: password, Err: err}
	}
	return nil
}
