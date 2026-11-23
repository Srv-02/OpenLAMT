package configfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

const (
	pinA = "abababababababababababababababababababababababababababababababab"
	pinB = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
)

func manager(t *testing.T) (*favorites.Manager, *keychain.Memory) {
	t.Helper()
	keys := keychain.NewMemory()
	return favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys), keys
}

func ptr[T any](v T) *T { return &v }

func seeded(t *testing.T) (*favorites.Manager, *keychain.Memory) {
	t.Helper()
	m, keys := manager(t)
	for _, f := range []favorites.Favorite{
		{Name: "Lab", Host: "lab.example.com", Username: "admin", UseTLS: true, Layout: "de", Quality: "low", AutoConnect: "on"},
		{Name: "Build", Host: "build.example.com", Username: "admin"},
	} {
		if err := m.Upsert(f); err != nil {
			t.Fatal(err)
		}
	}
	keys.Set("lab.example.com", "admin", "hunter2-secret")
	keys.Set("build.example.com", "admin", "another-secret")
	keys.SetFingerprint("lab.example.com", "admin", pinA)
	return m, keys
}

var settings = &Settings{PollIntervalSeconds: ptr(20), KVMQuality: ptr("low"), Language: ptr("de"), UpdateCheck: ptr(false)}

func build(t *testing.T, o Options) File {
	t.Helper()
	m, keys := seeded(t)
	list, _ := m.List()
	o.App, o.Now = "1.2.0", time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	f, err := Build(list, keys, o)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The file holds favorites, pins and settings, and never a password.
func TestExportHoldsNoPassword(t *testing.T) {
	f := build(t, Options{Favorites: true, Fingerprints: true, Settings: settings})
	b, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, secret := range []string{"hunter2-secret", "another-secret", "password"} {
		if strings.Contains(text, secret) {
			t.Errorf("the file contains %q:\n%s", secret, text)
		}
	}
	for _, want := range []string{`"format": "openlamt-config"`, `"version": 1`, `"app": "1.2.0"`, `"exported": "2026-11-23T10:00:00Z"`,
		`"host": "lab.example.com"`, `"kvm_quality": "low"`, `"kvm_auto_connect": "on"`, `"fingerprint": "` + pinA + `"`,
		`"poll_interval_seconds": 20`, `"language": "de"`, `"update_check": false`} {
		if !strings.Contains(text, want) {
			t.Errorf("the file lacks %s:\n%s", want, text)
		}
	}
}

func TestExportOptions(t *testing.T) {
	noPins := build(t, Options{Favorites: true, Settings: settings})
	if b, _ := Encode(noPins); strings.Contains(string(b), "fingerprint") {
		t.Errorf("pins exported although they were turned off:\n%s", b)
	}
	onlySettings := build(t, Options{Settings: settings})
	if len(onlySettings.Favorites) != 0 || onlySettings.Settings == nil {
		t.Errorf("only settings: %+v", onlySettings)
	}
	onlyFavs := build(t, Options{Favorites: true, Fingerprints: true})
	if len(onlyFavs.Favorites) != 2 || onlyFavs.Settings != nil {
		t.Errorf("only favorites: %+v", onlyFavs)
	}
	if onlyFavs.Favorites[1].Fingerprint != "" {
		t.Error("a favorite without pin got one")
	}
}

func TestExportKeychainFailure(t *testing.T) {
	m, keys := seeded(t)
	list, _ := m.List()
	keys.Err = errors.New("locked")
	if _, err := Build(list, keys, Options{Favorites: true, Fingerprints: true}); err == nil {
		t.Fatal("a keychain that fails must stop the export")
	}
	if _, err := Build(list, keys, Options{Favorites: true}); err != nil {
		t.Fatalf("without pins the keychain is not asked: %v", err)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	f := build(t, Options{Favorites: true, Fingerprints: true, Settings: settings})
	path := filepath.Join(t.TempDir(), "openlamt-config-2026-11-23.json")
	if err := Write(path, f); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("mode %v %v", st, err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Favorites) != 2 || got.Favorites[0].Fingerprint != pinA || got.Favorites[0].Layout != "de" ||
		got.Settings == nil || *got.Settings.PollIntervalSeconds != 20 || *got.Settings.UpdateCheck {
		t.Errorf("round trip: %+v", got)
	}
}

func TestParseErrors(t *testing.T) {
	notConfig := []string{`{"hello": "world"}`, `[1, 2]`, `"text"`, `42`, "plain text\nline 2", ``, `{"format": "something-else", "version": 1}`}
	for _, in := range notConfig {
		if _, err := Parse([]byte(in)); !errors.Is(err, ErrNotConfig) {
			t.Errorf("%q: %v, want ErrNotConfig", in, err)
		}
	}

	broken := "{\n  \"format\": \"openlamt-config\",\n  \"version\": 1,\n  \"favorites\": [\n    {\"host\": \"a.example.com\" \"username\": \"admin\"}\n  ]\n}"
	_, err := Parse([]byte(broken))
	var dmg *DamagedError
	if !errors.As(err, &dmg) || dmg.Line != 5 {
		t.Errorf("broken JSON: %v, want a damaged file at line 5", err)
	}

	_, err = Parse([]byte(`{"format": "openlamt-config", "version": 1, "favorites": {"host": "x"}}`))
	if !errors.As(err, &dmg) {
		t.Errorf("wrong type: %v", err)
	}

	_, err = Parse([]byte(`{"format": "openlamt-config", "version": 2, "app": "2.0.0", "favorites": [{"future": true}]}`))
	var newer *NewerError
	if !errors.As(err, &newer) || newer.App != "2.0.0" {
		t.Errorf("newer version: %v", err)
	}

	for name, c := range map[string]struct {
		in     string
		reason Reason
		item   int
	}{
		"no version": {`{"format": "openlamt-config", "favorites": []}`, ReasonNoVersion, 0},
		"no host":    {`{"format": "openlamt-config", "version": 1, "favorites": [{"host": "a.example.com", "username": "u"}, {"host": " ", "username": "admin"}]}`, ReasonNoHost, 2},
		"port":       {`{"format": "openlamt-config", "version": 1, "favorites": [{"host": "a.example.com:16993", "username": "admin"}]}`, ReasonBadHost, 1},
		"no user":    {`{"format": "openlamt-config", "version": 1, "favorites": [{"host": "a.example.com"}]}`, ReasonNoUser, 1},
		"bad pin":    {`{"format": "openlamt-config", "version": 1, "favorites": [{"host": "a.example.com", "username": "admin", "fingerprint": "1234"}]}`, ReasonBadPin, 1},
	} {
		if _, err := Parse([]byte(c.in)); !errors.As(err, &dmg) || dmg.Reason != c.reason || dmg.Item != c.item {
			t.Errorf("%s: %v, want reason %d item %d", name, err, c.reason, c.item)
		}
	}
}

func TestParseNormalizes(t *testing.T) {
	in := `{"format": "openlamt-config", "version": 1, "favorites": [
	  {"name": "A", "host": " Lab.Example.COM ", "username": "admin", "kvm_quality": "ultra", "kvm_auto_connect": "maybe", "layout": "xx",
	   "fingerprint": "` + strings.ToUpper(pinA[:2]) + `:` + pinA[2:] + `"}]}`
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got := f.Favorites[0]
	if got.Host != "lab.example.com" || got.Quality != "" || got.AutoConnect != "" || got.Layout != "" || got.Fingerprint != pinA {
		t.Errorf("normalized: %+v", got)
	}
}

func TestReadRefusesHugeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, make([]byte, MaxSize+10), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrNotConfig) {
		t.Fatalf("%v", err)
	}
}

// Import merges: new hosts are added with their pin, an existing host is skipped and keeps
// its own pin, and nothing is replaced or removed.
func TestImportMerges(t *testing.T) {
	file := File{Format: Format, Version: Version, Favorites: []Favorite{
		{Favorite: favorites.Favorite{Name: "Lab renamed", Host: "lab.example.com", Username: "root"}, Fingerprint: pinB},
		{Favorite: favorites.Favorite{Name: "NAS", Host: "nas.example.com", Username: "admin", UseTLS: true, Quality: "high"}, Fingerprint: pinB},
		{Favorite: favorites.Favorite{Name: "Plain", Host: "plain.example.com", Username: "admin"}},
		{Favorite: favorites.Favorite{Name: "NAS again", Host: "nas.example.com", Username: "admin"}},
	}}
	m, keys := seeded(t)
	existing, _ := m.List()

	plan := Plan(file, existing)
	if len(plan.New) != 2 || len(plan.Skipped) != 2 || plan.Pins != 1 || plan.Skipped[0].Name != "Lab" {
		t.Fatalf("plan: %+v", plan)
	}

	added, skipped, err := Import(m, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || len(skipped) != 2 {
		t.Fatalf("added %d, skipped %d", len(added), len(skipped))
	}
	list, _ := m.List()
	if len(list) != 4 || list[0].Name != "Lab" || list[0].Username != "admin" || list[2].Host != "nas.example.com" || !list[2].UseTLS {
		t.Errorf("favorites after import: %+v", list)
	}
	if fp, _ := keys.GetFingerprint("nas.example.com", "admin"); fp != pinB {
		t.Errorf("pin of the new favorite %q", fp)
	}
	if fp, _ := keys.GetFingerprint("lab.example.com", "admin"); fp != pinA {
		t.Errorf("the pin of an existing favorite changed to %q", fp)
	}
	if _, err := keys.GetFingerprint("lab.example.com", "root"); err == nil {
		t.Error("a pin of a skipped favorite was stored")
	}
	if _, err := keys.Get("nas.example.com", "admin"); err == nil {
		t.Error("an imported favorite has a password")
	}
	// A second import changes nothing.
	added, skipped, err = Import(m, file)
	if err != nil || len(added) != 0 || len(skipped) != 4 {
		t.Errorf("second import: %d added, %d skipped, %v", len(added), len(skipped), err)
	}
}
