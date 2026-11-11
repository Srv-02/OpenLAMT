package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// recordingStore is a LoginStore that records calls and fails with err.
type recordingStore struct {
	logins []string
	pins   []string
	err    error
}

func (r *recordingStore) ChangeLogin(host, oldUser, newUser, newPassword string) error {
	r.logins = append(r.logins, host+"|"+oldUser+"|"+newUser+"|"+newPassword)
	return r.err
}

func (r *recordingStore) ChangeFingerprint(host, user, fp string) error {
	r.pins = append(r.pins, host+"|"+user+"|"+fp)
	return r.err
}

// openAs opens a session on dev logged in as user.
func openAs(t *testing.T, dev *Fake, user, password string) *Session {
	t.Helper()
	s, _, err := Open(context.Background(), amt.Config{Host: "device.example.com", Username: user, Password: password}, nil, ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionAdminAccountOwnLogin(t *testing.T) {
	dev := configFake()
	s := openAs(t, dev, "admin", "Old-Secret1")
	store := &recordingStore{}
	change := amt.AccountChange{Name: "newadmin", Password: "New-Secret2"}

	if err := s.SetAdminAccount(change, amt.Confirmation{}, store); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("unconfirmed: %v", err)
	}
	if err := s.SetAdminAccount(change, conf(amt.OpAdminAccount), store); err != nil {
		t.Fatal(err)
	}
	if c := s.Config(); c.Username != "newadmin" || c.Password != "New-Secret2" {
		t.Errorf("session login %q", c.Username)
	}
	if want := "device.example.com|admin|newadmin|New-Secret2"; len(store.logins) != 1 || store.logins[0] != want {
		t.Errorf("store calls %v", store.logins)
	}
	if l, _ := s.Users(); l.Admin != "newadmin" {
		t.Errorf("admin %q", l.Admin)
	}
}

func TestSessionAdminAccountOfAnotherLogin(t *testing.T) {
	dev := configFake()
	s := openAs(t, dev, "user1", "User-Secret1")
	store := &recordingStore{}
	if err := s.SetAdminAccount(amt.AccountChange{Password: "New-Secret2"}, conf(amt.OpAdminAccount), store); err != nil {
		t.Fatal(err)
	}
	if c := s.Config(); c.Username != "user1" || c.Password != "User-Secret1" {
		t.Error("the session login must stay")
	}
	if want := "device.example.com|admin|admin|New-Secret2"; len(store.logins) != 1 || store.logins[0] != want {
		t.Errorf("a saved admin favorite of this host must follow too: %v", store.logins)
	}
	if err := s.SetAdminAccount(amt.AccountChange{Password: "New-Secret3"}, conf(amt.OpAdminAccount), nil); err != nil {
		t.Errorf("without store: %v", err)
	}
}

func TestSessionNotSavedReturnsPasswordOnce(t *testing.T) {
	dev := configFake()
	s := openAs(t, dev, "admin", "Old-Secret1")
	store := &recordingStore{err: &favorites.KeychainError{Err: errors.New("keychain locked")}}
	err := s.SetAdminAccount(amt.AccountChange{Password: "New-Secret2"}, conf(amt.OpAdminAccount), store)
	var ns *NotSavedError
	if !errors.As(err, &ns) || !errors.Is(err, ErrNotSaved) || ns.Username != "admin" || ns.Password != "New-Secret2" {
		t.Fatalf("err = %#v", err)
	}
	var ke *favorites.KeychainError
	if !errors.As(err, &ke) {
		t.Error("the cause must stay reachable")
	}
	if strings.Contains(err.Error(), "New-Secret2") {
		t.Error("the error text contains the password")
	}
	if c := s.Config(); c.Password != "New-Secret2" {
		t.Error("the device accepted the change: the session must use the new login anyway")
	}
}

func TestSessionAccountUncertainKeepsType(t *testing.T) {
	dev := configFake()
	libErr := fmt.Errorf("%w: %v\n%v", errors.New("wsman.Client post received"), "401 Unauthorized", "<html>secret body</html>")
	dev.Cfg.WriteErr = &amt.CredentialsUncertainError{Username: "newadmin", Self: true, Err: libErr}
	s := openAs(t, dev, "admin", "Old-Secret1")
	store := &recordingStore{}
	err := s.SetAdminAccount(amt.AccountChange{Name: "newadmin", Password: "New-Secret2"}, conf(amt.OpAdminAccount), store)
	var ue *amt.CredentialsUncertainError
	if !errors.As(err, &ue) || !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret body") {
		t.Fatalf("err = %v", err)
	}
	if c := s.Config(); c.Username != "admin" || c.Password != "Old-Secret1" || len(store.logins) != 0 {
		t.Error("an uncertain change must neither switch nor save the login")
	}
}

func TestSessionOwnAccount(t *testing.T) {
	dev := configFake() // handle 3 "operator" is the session's own account
	s := openAs(t, dev, "operator", "Old-Secret1")
	store := &recordingStore{}
	spec := amt.UserSpec{Name: "operator2", Password: "New-Secret2", Access: amt.AccessNetwork, Realms: []amt.Realm{5}}
	if err := s.UpdateUser(3, spec, amt.Confirmation{}, store); !errors.Is(err, amt.ErrRiskyChange) {
		t.Fatalf("unconfirmed: %v", err)
	}
	if err := s.UpdateUser(3, spec, conf(amt.OpOwnAccount), store); err != nil {
		t.Fatal(err)
	}
	if c := s.Config(); c.Username != "operator2" || c.Password != "New-Secret2" {
		t.Errorf("session login %q", c.Username)
	}
	if want := "device.example.com|operator|operator2|New-Secret2"; len(store.logins) != 1 || store.logins[0] != want {
		t.Errorf("store %v", store.logins)
	}
	if l, _ := s.Users(); !l.Users[2].Self || l.Users[2].Name != "operator2" {
		t.Errorf("own entry %+v", l.Users[2])
	}

	// another account: the session login stays, a saved login of that account follows
	if err := s.UpdateUser(2, amt.UserSpec{Name: "user1", Password: "User-Secret2", Access: amt.AccessNetwork, Realms: []amt.Realm{2}}, amt.Confirmation{}, store); err != nil {
		t.Fatal(err)
	}
	if s.Config().Username != "operator2" || store.logins[1] != "device.example.com|user1|user1|User-Secret2" {
		t.Errorf("other account: %q %v", s.Config().Username, store.logins)
	}
}

func TestSessionAdminAccountWithFavoritesManager(t *testing.T) {
	keys := keychain.NewMemory()
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	const fp = "abababababababababababababababababababababababababababababababab"
	mgr.Upsert(favorites.Favorite{Name: "Office", Host: "device.example.com", Username: "admin", UseTLS: true})
	keys.Set("device.example.com", "admin", "Old-Secret1")
	keys.SetFingerprint("device.example.com", "admin", fp)

	dev := configFake()
	s := openAs(t, dev, "admin", "Old-Secret1")
	if err := s.SetAdminAccount(amt.AccountChange{Name: "newadmin", Password: "New-Secret2"}, conf(amt.OpAdminAccount), mgr); err != nil {
		t.Fatal(err)
	}
	if f, ok, _ := mgr.Find("device.example.com", "newadmin"); !ok || f.Name != "Office" {
		t.Errorf("favorite %+v %v", f, ok)
	}
	if pw, _ := keys.Get("device.example.com", "newadmin"); pw != "New-Secret2" {
		t.Errorf("password %q", pw)
	}
	if got, _ := keys.GetFingerprint("device.example.com", "newadmin"); got != fp {
		t.Errorf("pin %q", got)
	}
}
