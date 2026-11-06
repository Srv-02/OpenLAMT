package keychain

import (
	"errors"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestOSStoreRoundTrip(t *testing.T) {
	keyring.MockInit()
	s := OS{}
	if _, err := s.Get("device.example.com", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Set("device.example.com", "admin", "secret-1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("device.example.com", "admin")
	if err != nil || got != "secret-1" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := s.Delete("device.example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("device.example.com", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestAccountSeparatesUsers(t *testing.T) {
	if account("h.example.com", "a") == account("h.example.com", "b") {
		t.Fatal("accounts collide")
	}
}
func TestOSFingerprintRoundTrip(t *testing.T) {
	keyring.MockInit()
	s := OS{}
	fp := strings.Repeat("ab", 32)
	if _, err := s.GetFingerprint("device.example.com", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.SetFingerprint("device.example.com", "admin", fp); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFingerprint("device.example.com", "admin")
	if err != nil || got != fp {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := s.DeleteFingerprint("device.example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFingerprint("device.example.com", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFingerprintAndPasswordAreSeparate(t *testing.T) {
	keyring.MockInit()
	for _, s := range []Store{OS{}, NewMemory()} {
		fp := strings.Repeat("ab", 32)
		if err := s.Set("device.example.com", "admin", "secret-1"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetFingerprint("device.example.com", "admin", fp); err != nil {
			t.Fatal(err)
		}
		if pw, err := s.Get("device.example.com", "admin"); err != nil || pw != "secret-1" {
			t.Fatalf("%T: password %q, %v", s, pw, err)
		}
		if err := s.Delete("device.example.com", "admin"); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetFingerprint("device.example.com", "admin"); err != nil || got != fp {
			t.Fatalf("%T: fingerprint lost: %q %v", s, got, err)
		}
		if _, err := s.Get("device.example.com", "admin"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%T: password still present: %v", s, err)
		}
	}
}

func TestMemoryErr(t *testing.T) {
	m := NewMemory()
	m.Err = errors.New("no secret service")
	if err := m.Set("device.example.com", "admin", "x"); !errors.Is(err, m.Err) {
		t.Fatalf("Set: %v", err)
	}
	if _, err := m.GetFingerprint("device.example.com", "admin"); !errors.Is(err, m.Err) {
		t.Fatalf("GetFingerprint: %v", err)
	}
	if err := m.DeleteFingerprint("device.example.com", "admin"); !errors.Is(err, m.Err) {
		t.Fatalf("DeleteFingerprint: %v", err)
	}
}
