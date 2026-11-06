package favorites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Files written before kvm_auto_connect existed load with the global setting (empty override).
func TestLoadOldFileWithoutAutoConnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	old := `[{"name":"Büro","host":"device.example.com","username":"admin","use_tls":true,"layout":"de","kvm_quality":"low"}]`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := NewStore(path).Load()
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if list[0].AutoConnect != "" || list[0].AutoConnectChoice() != "" || list[0].QualityChoice() != QualityLow {
		t.Fatalf("%+v", list[0])
	}
}

func TestAutoConnectRoundTripAndOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s := NewStore(path)
	if err := s.Save([]Favorite{
		{Host: "a.example.com", Username: "u", AutoConnect: AutoConnectOn},
		{Host: "b.example.com", Username: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "kvm_auto_connect") != 1 { // empty is not written
		t.Fatalf("%s", b)
	}
	list, _ := s.Load()
	if list[0].AutoConnectChoice() != AutoConnectOn || list[1].AutoConnectChoice() != "" {
		t.Fatalf("%+v", list)
	}
}

func TestAutoConnectChoiceIgnoresUnknownValue(t *testing.T) {
	if (Favorite{AutoConnect: "maybe"}).AutoConnectChoice() != "" {
		t.Fatal("unknown value must mean the global setting")
	}
}

// Edit sets and removes the override; Upsert with an empty value keeps the stored one.
func TestAutoConnectEditAndUpsert(t *testing.T) {
	m, _ := seeded(t)
	f := fav
	f.AutoConnect = AutoConnectOff
	if err := m.Edit(fav.Host, fav.Username, f, false); err != nil {
		t.Fatal(err)
	}
	got, _, _ := m.Find(fav.Host, fav.Username)
	if got.AutoConnect != AutoConnectOff {
		t.Fatalf("%+v", got)
	}
	if err := m.Upsert(fav); err != nil { // empty keeps
		t.Fatal(err)
	}
	if got, _, _ = m.Find(fav.Host, fav.Username); got.AutoConnect != AutoConnectOff {
		t.Fatalf("upsert dropped override: %+v", got)
	}
	if err := m.Edit(fav.Host, fav.Username, fav, false); err != nil { // empty removes
		t.Fatal(err)
	}
	if got, _, _ = m.Find(fav.Host, fav.Username); got.AutoConnect != "" {
		t.Fatalf("edit kept override: %+v", got)
	}
}
