package favorites

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Srv-02/openlamt/internal/keychain"
)

const fp2 = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"

// flakyKeys fails the named calls of an in-memory keychain.
type flakyKeys struct {
	*keychain.Memory
	fail map[string]bool
}

var errKeychain = errors.New("keychain locked")

func (k *flakyKeys) Set(h, u, p string) error {
	if k.fail["Set"] {
		return errKeychain
	}
	return k.Memory.Set(h, u, p)
}

func (k *flakyKeys) SetFingerprint(h, u, f string) error {
	if k.fail["SetFingerprint"] {
		return errKeychain
	}
	return k.Memory.SetFingerprint(h, u, f)
}

func (k *flakyKeys) Delete(h, u string) error {
	if k.fail["Delete"] {
		return errKeychain
	}
	return k.Memory.Delete(h, u)
}

func flakySeeded(t *testing.T, fail ...string) (*Manager, *flakyKeys) {
	t.Helper()
	keys := &flakyKeys{Memory: keychain.NewMemory(), fail: map[string]bool{}}
	m := NewManager(NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	if err := m.Upsert(fav); err != nil {
		t.Fatal(err)
	}
	keys.Memory.Set("device.example.com", "admin", "secret-1")
	keys.Memory.SetFingerprint("device.example.com", "admin", fp)
	for _, f := range fail {
		keys.fail[f] = true
	}
	return m, keys
}

func TestChangeLoginRenamesFavoriteAndMovesSecrets(t *testing.T) {
	m, keys := seeded(t)
	if err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2"); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 1 || list[0].Username != "newadmin" || list[0].Name != "Büro" || !list[0].UseTLS {
		t.Fatalf("favorites %+v", list)
	}
	if pw, _ := keys.Get("device.example.com", "newadmin"); pw != "New-Secret2" {
		t.Errorf("new password %q", pw)
	}
	if got, _ := keys.GetFingerprint("device.example.com", "newadmin"); got != fp {
		t.Errorf("the pin must move with the login (same certificate), got %q", got)
	}
	if _, err := keys.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("old password left behind: %v", err)
	}
	if _, err := keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("old pin left behind: %v", err)
	}
}

func TestChangeLoginPasswordOnly(t *testing.T) {
	m, keys := seeded(t)
	if err := m.ChangeLogin("device.example.com", "admin", "admin", "New-Secret2"); err != nil {
		t.Fatal(err)
	}
	if pw, _ := keys.Get("device.example.com", "admin"); pw != "New-Secret2" {
		t.Errorf("password %q", pw)
	}
	if got, _ := keys.GetFingerprint("device.example.com", "admin"); got != fp {
		t.Errorf("pin %q", got)
	}
}

func TestChangeLoginDoesNotCreateEntries(t *testing.T) {
	m, keys := newManager(t)
	if err := m.Upsert(fav); err != nil { // favorite without saved password and pin
		t.Fatal(err)
	}
	if err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2"); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Get("device.example.com", "newadmin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("a password the user did not save must stay unsaved: %v", err)
	}
	if list, _ := m.List(); list[0].Username != "newadmin" {
		t.Errorf("favorite %+v", list[0])
	}
	if err := m.ChangeLogin("other.example.com", "admin", "x", "New-Secret2"); err != nil {
		t.Errorf("no favorite, no entries: %v", err)
	}
}

func TestChangeLoginConflictsWithExistingFavorite(t *testing.T) {
	m, keys := seeded(t)
	m.Upsert(Favorite{Host: "device.example.com", Username: "newadmin"})
	if err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2"); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if pw, _ := keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Error("nothing may change on a conflict")
	}
}

func TestChangeLoginKeychainFailures(t *testing.T) {
	m, keys := flakySeeded(t, "Set")
	err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2")
	var ke *KeychainError
	if !errors.As(err, &ke) || !errors.Is(err, errKeychain) {
		t.Fatalf("err = %v", err)
	}
	if list, _ := m.List(); list[0].Username != "admin" {
		t.Error("the favorite must not be renamed when the new password could not be stored")
	}
	if pw, _ := keys.Memory.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Error("the old entry must stay when the new one could not be written")
	}

	m, keys = flakySeeded(t, "SetFingerprint")
	if err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2"); !errors.As(err, &ke) {
		t.Fatalf("pin failure: %v", err)
	}
	if pw, _ := keys.Memory.Get("device.example.com", "newadmin"); pw != "New-Secret2" {
		t.Error("the new password must be stored even if the pin fails")
	}
	if got, _ := keys.Memory.GetFingerprint("device.example.com", "admin"); got != fp {
		t.Error("a pin that could not be moved must not be deleted")
	}

	m, keys = flakySeeded(t, "Delete")
	if err := m.ChangeLogin("device.example.com", "admin", "newadmin", "New-Secret2"); err != nil {
		t.Errorf("removing the old password is best effort: %v", err)
	}
}

func TestChangeFingerprint(t *testing.T) {
	m, keys := seeded(t)
	const fp3 = "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	m.Upsert(Favorite{Host: "device.example.com", Username: "operator"})
	keys.SetFingerprint("device.example.com", "operator", fp)
	m.Upsert(Favorite{Host: "device.example.com", Username: "auditor"})
	keys.SetFingerprint("device.example.com", "auditor", fp3)          // pinned another certificate
	m.Upsert(Favorite{Host: "device.example.com", Username: "viewer"}) // no pin
	if err := m.ChangeFingerprint("device.example.com", "admin", fp, fp2); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[string]string{"admin": fp2, "operator": fp2, "auditor": fp3} {
		if got, _ := keys.GetFingerprint("device.example.com", user); got != want {
			t.Errorf("%s: pin %q", user, got)
		}
	}
	if _, err := keys.GetFingerprint("device.example.com", "viewer"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("no stored pin, nothing to replace: %v", err)
	}
	if err := m.ChangeFingerprint("other.example.com", "admin", fp, fp2); err != nil {
		t.Fatal(err)
	}
	m2, _ := flakySeeded(t, "SetFingerprint")
	var ke *KeychainError
	if err := m2.ChangeFingerprint("device.example.com", "admin", fp, fp2); !errors.As(err, &ke) {
		t.Errorf("err = %v", err)
	}
}
