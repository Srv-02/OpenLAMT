package mcpserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// openPins opens a pin file in a new folder over a memory keychain.
func openPins(t *testing.T) (pins keychain.Store, path string, inner *keychain.Memory) {
	t.Helper()
	path = filepath.Join(t.TempDir(), PinFileName)
	inner = keychain.NewMemory()
	pins, err := OpenPinFile(path, inner)
	if err != nil {
		t.Fatal(err)
	}
	return pins, path, inner
}

// TestPinFileRoundTrip: a missing file has no pins and reading does not create it; a pin is
// written as "user@host": "fingerprint" into a file only its owner can read, and deleted again.
func TestPinFileRoundTrip(t *testing.T) {
	pins, path, _ := openPins(t)
	if _, err := pins.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("get from a missing file: %v", err)
	}
	if err := pins.DeleteFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("delete from a missing file: %v", err)
	}
	if err := pins.SetFingerprint("lab.example.com", "admin", "not a fingerprint"); err == nil {
		t.Fatal("stored a value that is no fingerprint")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file exists though nothing was pinned: %v", err)
	}
	if err := pins.SetFingerprint("lab.example.com", "admin", testFP); err != nil {
		t.Fatal(err)
	}
	if fp, err := pins.GetFingerprint("lab.example.com", "admin"); err != nil || fp != testFP {
		t.Fatalf("get: %q %v", fp, err)
	}
	b, err := os.ReadFile(path)
	if want := "{\n  \"admin@lab.example.com\": \"" + testFP + "\"\n}\n"; err != nil || string(b) != want {
		t.Fatalf("file %q, want %q (%v)", b, want, err)
	}
	info, err := os.Stat(path)
	if err != nil || !isWindows() && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if e.Name() != PinFileName && e.Name() != PinFileName+".lock" {
			t.Errorf("left %s behind", e.Name())
		}
	}
	if err := pins.DeleteFingerprint("lab.example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := pins.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

// TestPinFileKeysLikeTheKeychain: an entry is kept by user name and host exactly as the keychain
// keeps it ("user@host"), so the same logins are separate, or the same, in both.
func TestPinFileKeysLikeTheKeychain(t *testing.T) {
	pins, _, _ := openPins(t)
	mem := keychain.NewMemory()
	logins := [][2]string{{"lab.example.com", "admin"}, {"lab.example.com", "ops"}, {"LAB.example.com", "admin"},
		{"192.0.2.10", "admin"}, {"b@c", "a"}, {"c", "a@b"}}
	for i, l := range logins {
		fp := strings.Repeat(fmt.Sprintf("%02x", i+1), 32)
		for _, s := range []keychain.Store{pins, mem} {
			if err := s.SetFingerprint(l[0], l[1], fp); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, l := range logins {
		got, err := pins.GetFingerprint(l[0], l[1])
		want, _ := mem.GetFingerprint(l[0], l[1])
		if err != nil || got != want {
			t.Errorf("%s on %s: %q (%v), the keychain has %q", l[1], l[0], got, err, want)
		}
	}
}

// TestPinFilePasswordsStayInTheKeychain: passwords go to the keychain behind the pin file, a
// keychain that cannot be read fails only them, and no fingerprint ever reaches the keychain.
func TestPinFilePasswordsStayInTheKeychain(t *testing.T) {
	pins, path, inner := openPins(t)
	if err := pins.Set("lab.example.com", "admin", "S3cret!"); err != nil {
		t.Fatal(err)
	}
	if pw, _ := inner.Get("lab.example.com", "admin"); pw != "S3cret!" {
		t.Fatalf("the keychain has %q", pw)
	}
	if pw, err := pins.Get("lab.example.com", "admin"); err != nil || pw != "S3cret!" {
		t.Fatalf("get: %q %v", pw, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a password created the pin file: %v", err)
	}
	if err := pins.SetFingerprint("lab.example.com", "admin", testFP); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("the fingerprint reached the keychain: %v", err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "S3cret") {
		t.Fatal("the pin file holds the password")
	}
	broken := errors.New("the name org.freedesktop.secrets was not provided")
	inner.Err = broken
	var pf *pinFileError
	if _, err := pins.Get("lab.example.com", "admin"); !errors.Is(err, broken) || errors.As(err, &pf) {
		t.Fatalf("password from a broken keychain: %v", err)
	}
	if err := pins.Delete("lab.example.com", "admin"); !errors.Is(err, broken) {
		t.Fatalf("delete: %v", err)
	}
	if fp, err := pins.GetFingerprint("lab.example.com", "admin"); err != nil || fp != testFP {
		t.Fatalf("fingerprint with a broken keychain: %q %v", fp, err)
	}
}

// TestPinFileMustBePrivate: a pin file other users may read or change is refused when it is
// opened (the server does not start) and at every later use; nothing is written into it.
func TestPinFileMustBePrivate(t *testing.T) {
	if isWindows() {
		t.Skip("no Unix file modes on Windows")
	}
	pins, path, inner := openPins(t)
	if err := pins.SetFingerprint("lab.example.com", "admin", testFP); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o644, 0o660, 0o604, 0o620} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		var pf *pinFileError
		_, err := OpenPinFile(path, inner)
		if !errors.As(err, &pf) || !strings.Contains(err.Error(), "0600") || !strings.Contains(err.Error(), PinFileName) {
			t.Errorf("open with mode %04o: %v", mode, err)
		}
		if _, err := pins.GetFingerprint("lab.example.com", "admin"); !errors.As(err, &pf) {
			t.Errorf("get with mode %04o: %v", mode, err)
		}
		if err := pins.SetFingerprint("lab.example.com", "admin", otherFP); !errors.As(err, &pf) {
			t.Errorf("set with mode %04o: %v", mode, err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if fp, err := pins.GetFingerprint("lab.example.com", "admin"); err != nil || fp != testFP {
		t.Fatalf("after 0600: %q %v (a refused write changed the pin)", fp, err)
	}
}

// TestPinFileRefusesDamagedContent: a file that is not an object of fingerprints is refused
// without showing what it holds and is never overwritten; an empty file has no pins, and a
// fingerprint a person wrote with colons and capitals counts.
func TestPinFileRefusesDamagedContent(t *testing.T) {
	pins, path, inner := openPins(t)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`s3cret-value`, `[]`, `"s3cret-value"`, `{"admin@lab.example.com": 5}`,
		`{"admin@lab.example.com": "s3cret-value"}`, `{"admin@lab.example.com": ""}`, `{} {}`,
		`{"admin@lab.example.com": "` + testFP + `"`} {
		write(body)
		var pf *pinFileError
		_, err := OpenPinFile(path, inner)
		if !errors.As(err, &pf) || !strings.Contains(err.Error(), PinFileName) || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: open: %v", body, err)
		}
		if _, err := pins.GetFingerprint("lab.example.com", "admin"); !errors.As(err, &pf) || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: get: %v", body, err)
		}
		if err := pins.SetFingerprint("new.example.com", "admin", testFP); !errors.As(err, &pf) {
			t.Errorf("%s: set: %v", body, err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Errorf("%s: the damaged file was overwritten with %q", body, b)
		}
	}
	for _, body := range []string{"", " \n", "null"} {
		write(body)
		if _, err := OpenPinFile(path, inner); err != nil {
			t.Errorf("empty file %q: %v", body, err)
		}
		if _, err := pins.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("empty file %q: get: %v", body, err)
		}
	}
	write(`{"admin@lab.example.com": "` + amt.FormatFingerprint(testFP) + `"}`)
	if fp, err := pins.GetFingerprint("lab.example.com", "admin"); err != nil || fp != testFP {
		t.Fatalf("fingerprint with colons: %q %v", fp, err)
	}
}

// TestPinFileConcurrentStoresLoseNothing: two stores on one file stand for two openlamt-mcp
// processes (the server and "trust"). Every pin written from many goroutines must survive.
func TestPinFileConcurrentStoresLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), PinFileName)
	var stores []keychain.Store
	for range 2 {
		s, err := OpenPinFile(path, keychain.NewMemory())
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, s)
	}
	const n = 40
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if err := stores[i%2].SetFingerprint(fmt.Sprintf("h%d.example.com", i), "admin", testFP); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i := range n {
		if fp, err := stores[0].GetFingerprint(fmt.Sprintf("h%d.example.com", i), "admin"); err != nil || fp != testFP {
			t.Fatalf("pin %d lost: %q %v", i, fp, err)
		}
	}
}

// TestPinFileReadsInAReadOnlyFolder: where the lock file cannot be created (a folder the user may
// not write, pins provided by another tool), reading still works; writing fails and changes nothing.
func TestPinFileReadsInAReadOnlyFolder(t *testing.T) {
	if isWindows() || os.Geteuid() == 0 {
		t.Skip("needs Unix folder permissions and a user who is not root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, PinFileName)
	body := `{"admin@lab.example.com": "` + testFP + `"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	pins, err := OpenPinFile(path, keychain.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	if fp, err := pins.GetFingerprint("lab.example.com", "admin"); err != nil || fp != testFP {
		t.Fatalf("get: %q %v", fp, err)
	}
	var pf *pinFileError
	if err := pins.SetFingerprint("new.example.com", "admin", testFP); !errors.As(err, &pf) {
		t.Fatalf("set in a read-only folder: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != body {
		t.Fatalf("the file changed: %q", b)
	}
}
