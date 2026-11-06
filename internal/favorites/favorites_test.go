package favorites

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "favorites.json"))
	got, err := s.Load()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "favorites.json")
	s := NewStore(path)
	want := []Favorite{{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	_, err := NewStore(path).Load()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Fatalf("corrupt file was modified: %q", b)
	}
}

func TestFileNeverContainsPassword(t *testing.T) {
	rt := reflect.TypeOf(Favorite{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(name, "password") || strings.Contains(name, "fingerprint") {
			t.Fatalf("field %s", rt.Field(i).Name)
		}
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil || !strings.HasSuffix(p, filepath.Join("openlamt", "favorites.json")) {
		t.Fatalf("%q, %v", p, err)
	}
}

func TestLayoutField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s := NewStore(path)
	if err := os.WriteFile(path, []byte(`[{"name":"a","host":"h.example.com","username":"u","use_tls":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 1 || got[0].Layout != "" {
		t.Fatalf("legacy file: %+v, %v", got, err)
	}
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "layout") {
		t.Fatalf("empty layout written: %s", b)
	}
	got[0].Layout = "de"
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load()
	if err != nil || again[0].Layout != "de" {
		t.Fatalf("round trip: %+v, %v", again, err)
	}
}

func TestQualityField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s := NewStore(path)
	// A file from before the field existed loads as High and is written back without it.
	if err := os.WriteFile(path, []byte(`[{"name":"a","host":"h.example.com","username":"u","use_tls":true,"layout":"de"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 1 || got[0].Quality != "" || got[0].QualityChoice() != "" {
		t.Fatalf("legacy file: %+v, %v", got, err)
	}
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "kvm_quality") {
		t.Fatalf("empty quality written: %s", b)
	}
	got[0].Quality = QualityLow
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"kvm_quality": "low"`) {
		t.Fatalf("quality not written: %s", b)
	}
	again, err := s.Load()
	if err != nil || again[0].QualityChoice() != QualityLow {
		t.Fatalf("reload: %+v, %v", again, err)
	}
	if (Favorite{Quality: "bogus"}).QualityChoice() != "" {
		t.Error("unknown value is not High")
	}
}
