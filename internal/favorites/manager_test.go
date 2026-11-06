package favorites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/keychain"
)

var fav = Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}

const fp = "abababababababababababababababababababababababababababababababab"

func newManager(t *testing.T) (*Manager, *keychain.Memory) {
	t.Helper()
	keys := keychain.NewMemory()
	return NewManager(NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys), keys
}

func seeded(t *testing.T) (*Manager, *keychain.Memory) {
	t.Helper()
	m, keys := newManager(t)
	if err := m.Upsert(fav); err != nil {
		t.Fatal(err)
	}
	keys.Set("device.example.com", "admin", "secret-1")
	keys.SetFingerprint("device.example.com", "admin", fp)
	return m, keys
}

func TestUpsertAddsThenUpdates(t *testing.T) {
	m, _ := newManager(t)
	if err := m.Upsert(fav); err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(Favorite{Host: "other.example.com", Username: "admin", UseTLS: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(Favorite{Name: "Lager", Host: "device.example.com", Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 2 || list[0].Name != "Lager" || list[0].UseTLS || list[1].Host != "other.example.com" {
		t.Fatalf("%+v", list)
	}
	if f, ok, err := m.Find("device.example.com", "admin"); err != nil || !ok || f.Name != "Lager" {
		t.Fatalf("find: %+v %v %v", f, ok, err)
	}
	if _, ok, _ := m.Find("device.example.com", "root"); ok {
		t.Fatal("found favorite for other user")
	}
}

func TestEditSameKeyKeepsKeychain(t *testing.T) {
	m, keys := seeded(t)
	edited := Favorite{Name: "Neu", Host: "device.example.com", Username: "admin", UseTLS: false}
	if err := m.Edit("device.example.com", "admin", edited, false); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 1 || list[0] != edited {
		t.Fatalf("%+v", list)
	}
	if pw, _ := keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Fatal("password lost")
	}
	if got, _ := keys.GetFingerprint("device.example.com", "admin"); got != fp {
		t.Fatal("fingerprint lost")
	}
}

func TestEditResetFingerprintOnly(t *testing.T) {
	m, keys := seeded(t)
	if err := m.Edit("device.example.com", "admin", fav, true); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("fingerprint: %v", err)
	}
	if pw, _ := keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Fatal("password lost")
	}
}

func TestEditMovesKeychainEntries(t *testing.T) {
	m, keys := seeded(t)
	moved := Favorite{Name: "Büro", Host: "new.example.com", Username: "operator", UseTLS: true}
	if err := m.Edit("device.example.com", "admin", moved, false); err != nil {
		t.Fatal(err)
	}
	if pw, err := keys.Get("new.example.com", "operator"); err != nil || pw != "secret-1" {
		t.Fatalf("moved password: %q %v", pw, err)
	}
	if _, err := keys.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("old password: %v", err)
	}
	if _, err := keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("old fingerprint: %v", err)
	}
	if _, err := keys.GetFingerprint("new.example.com", "operator"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("fingerprint must not move: %v", err)
	}
}

func TestEditWithoutStoredPassword(t *testing.T) {
	m, _ := newManager(t)
	m.Upsert(fav)
	if err := m.Edit("device.example.com", "admin", Favorite{Host: "new.example.com", Username: "admin"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestEditRejectsDuplicateKey(t *testing.T) {
	m, _ := seeded(t)
	m.Upsert(Favorite{Host: "other.example.com", Username: "admin"})
	err := m.Edit("other.example.com", "admin", Favorite{Host: "device.example.com", Username: "admin"}, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	list, _ := m.List()
	if len(list) != 2 {
		t.Fatalf("list changed: %+v", list)
	}
}

func TestDeleteKeepsOrDropsPassword(t *testing.T) {
	for _, dropPW := range []bool{false, true} {
		m, keys := seeded(t)
		if err := m.Delete("device.example.com", "admin", dropPW); err != nil {
			t.Fatal(err)
		}
		if list, _ := m.List(); len(list) != 0 {
			t.Fatalf("%+v", list)
		}
		_, err := keys.Get("device.example.com", "admin")
		if gone := errors.Is(err, keychain.ErrNotFound); gone != dropPW {
			t.Fatalf("deletePassword=%v, gone=%v", dropPW, gone)
		}
		if _, err := keys.GetFingerprint("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Fatal("fingerprint must always be deleted")
		}
	}
	m, _ := newManager(t)
	if err := m.Delete("device.example.com", "admin", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestKeychainFailureAfterFileSaved(t *testing.T) {
	m, keys := seeded(t)
	keys.Err = errors.New("no secret service")
	err := m.Delete("device.example.com", "admin", true)
	var ke *KeychainError
	if !errors.As(err, &ke) || !strings.Contains(err.Error(), "no secret service") {
		t.Fatalf("err = %v", err)
	}
	if list, _ := m.List(); len(list) != 0 {
		t.Fatal("file not updated")
	}
}

func TestLoadIgnoresLegacyFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	os.WriteFile(path, []byte(`[{"name":"Büro","host":"device.example.com","username":"admin","use_tls":true,"fingerprint":"`+fp+`"}]`), 0o600)
	s := NewStore(path)
	list, err := s.Load()
	if err != nil || len(list) != 1 || list[0].Host != "device.example.com" {
		t.Fatalf("%+v %v", list, err)
	}
	if err := s.Save(list); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "fingerprint") {
		t.Fatalf("legacy field written back: %s", b)
	}
}

func TestDisplayName(t *testing.T) {
	if got := (Favorite{Host: "device.example.com"}).DisplayName(); got != "device.example.com" {
		t.Fatalf("%q", got)
	}
	if got := fav.DisplayName(); got != "Büro" {
		t.Fatalf("%q", got)
	}
}

func TestUpsertKeepsOrOverridesLayout(t *testing.T) {
	m, _ := seeded(t)
	m.Upsert(Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", Layout: "de"})
	if err := m.Upsert(Favorite{Name: "Neu", Host: "device.example.com", Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.Layout != "de" || f.Name != "Neu" {
		t.Fatalf("kept: %+v", f)
	}
	if err := m.Upsert(Favorite{Host: "device.example.com", Username: "admin", Layout: "us"}); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.Layout != "us" {
		t.Fatalf("overridden: %+v", f)
	}
}

func TestEditSetsLayoutAsGiven(t *testing.T) {
	m, _ := seeded(t)
	m.Upsert(Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", Layout: "de"})
	if err := m.Edit("device.example.com", "admin", Favorite{Name: "X", Host: "new.example.com", Username: "admin", Layout: "de"}, false); err != nil {
		t.Fatal(err)
	}
	if f, ok, _ := m.Find("new.example.com", "admin"); !ok || f.Layout != "de" {
		t.Fatalf("%+v %v", f, ok)
	}
	// empty removes the override: the global setting applies again
	if err := m.Edit("new.example.com", "admin", Favorite{Host: "new.example.com", Username: "admin"}, false); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("new.example.com", "admin"); f.Layout != "" {
		t.Fatalf("%+v", f)
	}
}

// failingFingerprint fails only DeleteFingerprint; keychain.Memory.Err would fail every call.
type failingFingerprint struct {
	*keychain.Memory
	err error
}

func (f failingFingerprint) DeleteFingerprint(host, username string) error { return f.err }

func seededFailingFingerprint(t *testing.T) (*Manager, *keychain.Memory) {
	t.Helper()
	mem := keychain.NewMemory()
	m := NewManager(NewStore(filepath.Join(t.TempDir(), "favorites.json")), failingFingerprint{mem, errors.New("fingerprint locked")})
	if err := m.Upsert(fav); err != nil {
		t.Fatal(err)
	}
	mem.Set("device.example.com", "admin", "secret-1")
	mem.SetFingerprint("device.example.com", "admin", fp)
	return m, mem
}

func TestEditMovesPasswordDespiteFingerprintFailure(t *testing.T) {
	m, mem := seededFailingFingerprint(t)
	err := m.Edit("device.example.com", "admin", Favorite{Host: "new.example.com", Username: "admin"}, false)
	var ke *KeychainError
	if !errors.As(err, &ke) || !strings.Contains(err.Error(), "fingerprint locked") {
		t.Fatalf("err = %v", err)
	}
	if pw, err := mem.Get("new.example.com", "admin"); err != nil || pw != "secret-1" {
		t.Fatalf("password not moved: %q %v", pw, err)
	}
	if _, err := mem.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("old password still present")
	}
}

func TestDeleteDropsPasswordDespiteFingerprintFailure(t *testing.T) {
	m, mem := seededFailingFingerprint(t)
	err := m.Delete("device.example.com", "admin", true)
	var ke *KeychainError
	if !errors.As(err, &ke) || !strings.Contains(err.Error(), "fingerprint locked") {
		t.Fatalf("err = %v", err)
	}
	if _, err := mem.Get("device.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("password not deleted")
	}
}

func TestSetQuality(t *testing.T) {
	m, _ := seeded(t)
	if err := m.SetQuality("device.example.com", "admin", QualityLow); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.QualityChoice() != QualityLow || f.Name != "Büro" {
		t.Fatalf("%+v", f)
	}
	if err := m.SetQuality("device.example.com", "admin", QualityHigh); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.QualityChoice() != QualityHigh {
		t.Fatalf("%+v", f)
	}
	if err := m.SetQuality("missing.example.com", "admin", QualityLow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := m.SetQuality("device.example.com", "admin", "medium"); err == nil {
		t.Fatal("unknown quality accepted")
	}
}

// Upsert (connect, save) does not know the quality: empty keeps it. Edit (edit dialog) writes
// what it is given: empty means Default and removes the override, an explicit value stays.
func TestUpsertKeepsAndEditWritesQuality(t *testing.T) {
	m, _ := seeded(t)
	m.SetQuality("device.example.com", "admin", QualityLow)
	if err := m.Upsert(Favorite{Name: "Neu", Host: "device.example.com", Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.QualityChoice() != QualityLow {
		t.Fatalf("upsert dropped quality: %+v", f)
	}
	// Edit with the stored value keeps the explicit choice (no data loss when only the name changes).
	if err := m.Edit("device.example.com", "admin", Favorite{Name: "X", Host: "new.example.com", Username: "admin", Quality: QualityLow}, false); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("new.example.com", "admin"); f.QualityChoice() != QualityLow {
		t.Fatalf("edit dropped quality: %+v", f)
	}
	if err := m.Edit("new.example.com", "admin", Favorite{Host: "new.example.com", Username: "admin", Quality: QualityHigh}, false); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("new.example.com", "admin"); f.QualityChoice() != QualityHigh {
		t.Fatalf("explicit High lost: %+v", f)
	}
	if err := m.Edit("new.example.com", "admin", Favorite{Host: "new.example.com", Username: "admin"}, false); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("new.example.com", "admin"); f.Quality != "" || f.QualityChoice() != "" {
		t.Fatalf("Default did not remove the override: %+v", f)
	}
}

func writeRawFavorites(t *testing.T, s *Store, raw string) {
	t.Helper()
	if err := os.WriteFile(s.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNormalizesHandEditedHost(t *testing.T) {
	m, keys := newManager(t)
	writeRawFavorites(t, m.store, `[{"name":"A","host":"  Device.Example.COM ","username":"admin","use_tls":true}]`)
	if err := keys.Set("device.example.com", "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := keys.SetFingerprint("device.example.com", "admin", "aa:bb"); err != nil {
		t.Fatal(err)
	}
	f, ok, err := m.Find("device.example.com", "admin")
	if err != nil || !ok || f.Host != "device.example.com" {
		t.Fatalf("Find: %+v %v %v", f, ok, err)
	}
	if pw, err := keys.Get(f.Host, f.Username); err != nil || pw != "secret" {
		t.Fatalf("password: %q %v", pw, err)
	}
	if fp, err := keys.GetFingerprint(f.Host, f.Username); err != nil || fp != "aa:bb" {
		t.Fatalf("fingerprint: %q %v", fp, err)
	}
	if err := m.Upsert(Favorite{Name: "A", Host: "device.example.com", Username: "admin", UseTLS: true}); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 1 {
		t.Fatalf("duplicate after upsert: %+v", list)
	}
}

func TestLoadKeepsEntriesThatBecomeEqual(t *testing.T) {
	m, _ := newManager(t)
	writeRawFavorites(t, m.store, `[{"name":"A","host":"HOST.example.com","username":"admin"},{"name":"B","host":"host.example.com","username":"admin"}]`)
	list, err := m.List()
	if err != nil || len(list) != 2 || list[0].Name != "A" || list[1].Name != "B" {
		t.Fatalf("got %+v, %v", list, err)
	}
}

// SetLayout and SetAutoConnect store one field of a favorite and leave the rest alone; an
// empty value removes the override.
func TestSetLayoutAndAutoConnect(t *testing.T) {
	m, _ := seeded(t)
	if err := m.SetQuality("device.example.com", "admin", QualityLow); err != nil {
		t.Fatal(err)
	}
	if err := m.SetLayout("device.example.com", "admin", "us"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAutoConnect("device.example.com", "admin", AutoConnectOn); err != nil {
		t.Fatal(err)
	}
	f, _, _ := m.Find("device.example.com", "admin")
	if f.Layout != "us" || f.AutoConnect != AutoConnectOn || f.Quality != QualityLow || f.Name != "Büro" {
		t.Fatalf("%+v", f)
	}
	if err := m.SetLayout("device.example.com", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAutoConnect("device.example.com", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := m.Find("device.example.com", "admin"); f.Layout != "" || f.AutoConnect != "" {
		t.Fatalf("not removed: %+v", f)
	}
	if err := m.SetLayout("missing.example.com", "admin", "de"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing layout: %v", err)
	}
	if err := m.SetAutoConnect("missing.example.com", "admin", AutoConnectOff); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing auto-connect: %v", err)
	}
	if err := m.SetAutoConnect("device.example.com", "admin", "maybe"); err == nil {
		t.Fatal("unknown auto-connect value accepted")
	}
}
