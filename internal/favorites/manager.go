package favorites

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/Srv-02/openlamt/internal/keychain"
)

var (
	ErrNotFound = errors.New("favorite not found")
	ErrExists   = errors.New("a favorite with this host and username already exists")
	// ErrNameExists: another favorite shows the same name (DisplayName). Add and Update keep
	// names unique, so a favorite can be found by its name (openlamt-mcp does that).
	ErrNameExists = errors.New("another favorite has this name")
)

// KeychainError reports that the favorites file was updated but a keychain step failed.
type KeychainError struct{ Err error }

func (e *KeychainError) Error() string { return "keychain: " + e.Err.Error() }
func (e *KeychainError) Unwrap() error { return e.Err }

// Manager edits favorites and keeps the keychain entries (password, fingerprint) in step.
type Manager struct {
	mu    sync.Mutex // serializes load-modify-save; callers may run on any goroutine
	store *Store
	keys  keychain.Store
}

// lockWrite takes the in-process mutex and the cross-process file lock. A change then reads the
// file after any other process finished writing it and saves before the next one reads.
func (m *Manager) lockWrite() (unlock func(), err error) {
	m.mu.Lock()
	release, err := m.store.Lock()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	return func() { release(); m.mu.Unlock() }, nil
}

func NewManager(store *Store, keys keychain.Store) *Manager {
	return &Manager{store: store, keys: keys}
}

// lockRead takes the same locks as lockWrite, so a read never has the file open while another
// process renames a new version over it: on Windows that rename fails (Go opens files without
// FILE_SHARE_DELETE). If the lock file cannot be taken, the read goes on without it.
func (m *Manager) lockRead() (unlock func()) {
	m.mu.Lock()
	release, err := m.store.Lock()
	if err != nil {
		return m.mu.Unlock
	}
	return func() { release(); m.mu.Unlock() }
}

func (m *Manager) List() ([]Favorite, error) {
	defer m.lockRead()()
	return m.store.Load()
}

func (m *Manager) Find(host, username string) (Favorite, bool, error) {
	defer m.lockRead()()
	list, err := m.store.Load()
	if err != nil {
		return Favorite{}, false, err
	}
	if i := index(list, host, username); i >= 0 {
		return list[i], true, nil
	}
	return Favorite{}, false, nil
}

// Upsert adds f or replaces the entry with the same host/username. An empty f.Layout,
// f.Quality or f.AutoConnect keeps the value already stored for that entry; the stored
// ReverseWheel always stays (SetReverseWheel changes it).
func (m *Manager) Upsert(f Favorite) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	if i := index(list, f.Host, f.Username); i >= 0 {
		if f.Layout == "" {
			f.Layout = list[i].Layout
		}
		if f.Quality == "" {
			f.Quality = list[i].Quality
		}
		if f.AutoConnect == "" {
			f.AutoConnect = list[i].AutoConnect
		}
		f.ReverseWheel = list[i].ReverseWheel
		list[i] = f
	} else {
		list = append(list, f)
	}
	return m.store.Save(list)
}

// Add saves f as a new favorite. A favorite with the same host and username returns ErrExists,
// one that shows the same name (DisplayName) ErrNameExists; nothing is written then.
func (m *Manager) Add(f Favorite) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	if index(list, f.Host, f.Username) >= 0 {
		return ErrExists
	}
	if nameTaken(list, f.DisplayName(), -1) {
		return ErrNameExists
	}
	return m.store.Save(append(list, f))
}

// Update applies change to the favorite host/username and saves it; it returns the favorite as
// saved. The new host and username must not belong to another favorite (ErrExists) and the name
// must stay unique (ErrNameExists). A new host or username deletes the certificate pin and the
// password of the old login: unlike Edit, the password does not move, because it belonged to the
// device the favorite pointed to before. File first, keychain second; keychain failures (other
// than ErrNotFound) return *KeychainError.
func (m *Manager) Update(host, username string, change func(*Favorite)) (Favorite, error) {
	unlock, err := m.lockWrite()
	if err != nil {
		return Favorite{}, err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return Favorite{}, err
	}
	i := index(list, host, username)
	if i < 0 {
		return Favorite{}, ErrNotFound
	}
	f := list[i]
	change(&f)
	moved := f.Host != host || f.Username != username
	if moved && index(list, f.Host, f.Username) >= 0 {
		return Favorite{}, ErrExists
	}
	if nameTaken(list, f.DisplayName(), i) {
		return Favorite{}, ErrNameExists
	}
	if f == list[i] {
		return f, nil
	}
	list[i] = f
	if err := m.store.Save(list); err != nil {
		return Favorite{}, err
	}
	if !moved {
		return f, nil
	}
	// Run both keychain steps even if one fails.
	return f, keychainErr(ignoreNotFound(m.keys.DeleteFingerprint(host, username)), ignoreNotFound(m.keys.Delete(host, username)))
}

// nameTaken reports whether a favorite other than list[skip] shows name.
func nameTaken(list []Favorite, name string, skip int) bool {
	for j, g := range list {
		if j != skip && g.DisplayName() == name {
			return true
		}
	}
	return false
}

// SetQuality stores the KVM quality (QualityHigh or QualityLow) of the favorite host/username.
// It returns ErrNotFound if the device is not a favorite.
func (m *Manager) SetQuality(host, username, quality string) error {
	if quality != QualityHigh && quality != QualityLow {
		return fmt.Errorf("favorites: unknown KVM quality %q", quality)
	}
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	i := index(list, host, username)
	if i < 0 {
		return ErrNotFound
	}
	if list[i].Quality == quality {
		return nil
	}
	list[i].Quality = quality
	return m.store.Save(list)
}

// Edit replaces the favorite identified by host/username with f, the layout override included
// the KVM auto-connect override and the KVM quality override (empty removes each: the global
// setting applies). If host or username change, the stored fingerprint is deleted and the
// password moves to the new key. resetFingerprint deletes the fingerprint even when the key is
// unchanged. File first, keychain second; keychain failures (other than ErrNotFound) return
// *KeychainError.
func (m *Manager) Edit(host, username string, f Favorite, resetFingerprint bool) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	i := index(list, host, username)
	if i < 0 {
		return ErrNotFound
	}
	moved := f.Host != host || f.Username != username
	if moved && index(list, f.Host, f.Username) >= 0 {
		return ErrExists
	}
	list[i] = f // Layout, Quality and AutoConnect as given: empty removes the override, the global setting applies
	if err := m.store.Save(list); err != nil {
		return err
	}
	// Run every keychain step even if one fails, so a fingerprint error does not leave the password behind.
	var errs []error
	if moved || resetFingerprint {
		errs = append(errs, ignoreNotFound(m.keys.DeleteFingerprint(host, username)))
	}
	if moved {
		errs = append(errs, m.movePassword(host, username, f.Host, f.Username))
	}
	return keychainErr(errs...)
}

// keychainErr joins the non-nil errors into one *KeychainError, or returns nil.
func keychainErr(errs ...error) error {
	if err := errors.Join(errs...); err != nil {
		return &KeychainError{Err: err}
	}
	return nil
}

func (m *Manager) movePassword(oldHost, oldUser, newHost, newUser string) error {
	pw, err := m.keys.Get(oldHost, oldUser)
	if errors.Is(err, keychain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.keys.Set(newHost, newUser, pw); err != nil {
		return err
	}
	return ignoreNotFound(m.keys.Delete(oldHost, oldUser))
}

// Delete removes the favorite and always its fingerprint; the password only with deletePassword.
func (m *Manager) Delete(host, username string, deletePassword bool) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	i := index(list, host, username)
	if i < 0 {
		return ErrNotFound
	}
	list = append(list[:i], list[i+1:]...)
	if err := m.store.Save(list); err != nil {
		return err
	}
	errs := []error{ignoreNotFound(m.keys.DeleteFingerprint(host, username))}
	if deletePassword {
		errs = append(errs, ignoreNotFound(m.keys.Delete(host, username)))
	}
	return keychainErr(errs...)
}

func index(list []Favorite, host, username string) int {
	for i, f := range list {
		if f.Host == host && f.Username == username {
			return i
		}
	}
	return -1
}

func ignoreNotFound(err error) error {
	if errors.Is(err, keychain.ErrNotFound) {
		return nil
	}
	return err
}

// SetLayout stores the keyboard layout name of the favorite host/username; empty removes the
// override. It returns ErrNotFound if the device is not a favorite.
func (m *Manager) SetLayout(host, username, layout string) error {
	return m.setField(host, username, func(f *Favorite) { f.Layout = layout })
}

// SetAutoConnect stores the KVM auto-connect override (AutoConnectOn, AutoConnectOff or empty
// for the global setting) of the favorite host/username. It returns ErrNotFound if the device
// is not a favorite.
func (m *Manager) SetAutoConnect(host, username, choice string) error {
	if choice != "" && choice != AutoConnectOn && choice != AutoConnectOff {
		return fmt.Errorf("favorites: unknown auto-connect value %q", choice)
	}
	return m.setField(host, username, func(f *Favorite) { f.AutoConnect = choice })
}

// SetReverseWheel stores "Reverse Mouse Wheel" of the favorite host/username. It returns
// ErrNotFound if the device is not a favorite.
func (m *Manager) SetReverseWheel(host, username string, on bool) error {
	return m.setField(host, username, func(f *Favorite) { f.ReverseWheel = on })
}

// setField applies change to the favorite host/username and saves the file.
func (m *Manager) setField(host, username string, change func(*Favorite)) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	i := index(list, host, username)
	if i < 0 {
		return ErrNotFound
	}
	before := list[i]
	change(&list[i])
	if list[i] == before {
		return nil
	}
	return m.store.Save(list)
}

// ChangeLogin follows an account change the device accepted (new name and/or password of
// oldUser on host): a stored password moves to host/newUser with the new value, a stored pin
// moves along (the certificate is the same), and a favorite host/oldUser is renamed. Entries
// that do not exist are not created: a password the user chose not to save stays unsaved.
// The new password is written first, so no later failure loses it; removing the old entries is
// best effort (the old password no longer works on the device). A favorite host/newUser that
// already exists returns ErrExists before anything changes. Keychain failures return
// *KeychainError; if the keychain cannot even be read, the pin move and the favorite rename
// still run (the old password stays where it is) and the error comes back at the end.
func (m *Manager) ChangeLogin(host, oldUser, newUser, newPassword string) error {
	unlock, err := m.lockWrite()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	moved := newUser != oldUser
	if moved && index(list, host, newUser) >= 0 {
		return ErrExists
	}
	var errs []error
	_, err = m.keys.Get(host, oldUser)
	hasPassword := err == nil
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		// Unreadable keychain: nothing is written under the new name and the old entry stays,
		// but the pin and the favorite still follow, so the login does not stay behind.
		errs = append(errs, err)
	}
	if hasPassword {
		if err := m.keys.Set(host, newUser, newPassword); err != nil {
			return &KeychainError{Err: err}
		}
	}
	if !moved {
		return keychainErr(errs...)
	}
	pin, err := m.keys.GetFingerprint(host, oldUser)
	pinMoved := false
	switch {
	case err == nil:
		err = m.keys.SetFingerprint(host, newUser, pin)
		pinMoved = err == nil
		errs = append(errs, err)
	case !errors.Is(err, keychain.ErrNotFound):
		errs = append(errs, err)
	}
	if i := index(list, host, oldUser); i >= 0 {
		list[i].Username = newUser
		if err := m.store.Save(list); err != nil {
			return errors.Join(keychainErr(errs...), err)
		}
	}
	if hasPassword {
		_ = m.keys.Delete(host, oldUser)
	}
	if pinMoved {
		_ = m.keys.DeleteFingerprint(host, oldUser)
	}
	return keychainErr(errs...)
}

// ChangeFingerprint follows a certificate change the device accepted: every pin oldFP stored
// for host/user or for a favorite of host is replaced by newFP. Other pins (another certificate)
// and logins without a stored pin stay as they are (the next connection asks again).
func (m *Manager) ChangeFingerprint(host, user, oldFP, newFP string) error {
	defer m.lockRead()()
	list, err := m.store.Load()
	if err != nil {
		return err
	}
	users := []string{user}
	for _, f := range list {
		if f.Host == host && !slices.Contains(users, f.Username) {
			users = append(users, f.Username)
		}
	}
	var errs []error
	for _, u := range users {
		pin, err := m.keys.GetFingerprint(host, u)
		switch {
		case err == nil && pin == oldFP:
			errs = append(errs, m.keys.SetFingerprint(host, u, newFP))
		case err != nil && !errors.Is(err, keychain.ErrNotFound):
			errs = append(errs, err)
		}
	}
	return keychainErr(errs...)
}

// Incoming is a favorite to merge in, with the certificate pin that goes with it (empty for none).
type Incoming struct {
	Favorite    Favorite
	Fingerprint string
}

// Merge adds the favorites of items that are new and leaves the others alone: a favorite whose
// host and user name already exist (or were added earlier in items) is skipped; another user name
// on a known host is a new favorite. It
// never replaces or deletes anything. A pin is stored only for an added favorite and only where
// the keychain has none; a different pin that is already there stays and the favorite is
// returned in pinConflicts. The favorites
// file is written once; keychain failures afterwards return *KeychainError, the favorites are
// added anyway. Passwords are not part of it.
func (m *Manager) Merge(items []Incoming) (added, skipped, pinConflicts []Favorite, err error) {
	unlock, err := m.lockWrite()
	if err != nil {
		return nil, nil, nil, err
	}
	defer unlock()
	list, err := m.store.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	type key struct{ host, user string }
	known := map[key]bool{}
	for _, f := range list {
		known[key{f.Host, f.Username}] = true
	}
	var pins []Incoming
	for _, it := range items {
		f := it.Favorite
		k := key{f.Host, f.Username}
		if known[k] {
			skipped = append(skipped, f)
			continue
		}
		known[k] = true
		list = append(list, f)
		added = append(added, f)
		if it.Fingerprint != "" {
			pins = append(pins, it)
		}
	}
	if len(added) == 0 {
		return nil, skipped, nil, nil
	}
	if err := m.store.Save(list); err != nil {
		return nil, skipped, nil, err
	}
	var errs []error
	for _, p := range pins {
		h, u := p.Favorite.Host, p.Favorite.Username
		// An imported pin never replaces a trusted one: that would let a file swap the
		// certificate the device is trusted with. Only an empty slot takes it.
		have, err := m.keys.GetFingerprint(h, u)
		switch {
		case err == nil && have == p.Fingerprint:
		case err == nil:
			pinConflicts = append(pinConflicts, p.Favorite)
		case errors.Is(err, keychain.ErrNotFound):
			errs = append(errs, m.keys.SetFingerprint(h, u, p.Fingerprint))
		default:
			errs = append(errs, err)
		}
	}
	return added, skipped, pinConflicts, keychainErr(errs...)
}
