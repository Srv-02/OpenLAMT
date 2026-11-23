package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/mcpserver"
)

// fakeTerminal answers the prompts of a subcommand.
type fakeTerminal struct {
	tty       bool
	passwords []string // answers of readPassword, in order
	line      string   // answer of readLine
	prompts   []string
}

func (f *fakeTerminal) isTerminal() bool { return f.tty }

func (f *fakeTerminal) readPassword(prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if len(f.passwords) == 0 {
		return "", errors.New("no more input")
	}
	pw := f.passwords[0]
	f.passwords = f.passwords[1:]
	return pw, nil
}

func (f *fakeTerminal) readLine(prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	return f.line, nil
}

// saveFavorites writes list as the favorites of the isolated config folder dir.
func saveFavorites(t *testing.T, dir string, list ...favorites.Favorite) {
	t.Helper()
	path, err := favorites.DefaultPath()
	if err != nil || !strings.HasPrefix(path, dir) {
		t.Fatalf("favorites path %q outside %q: %v", path, dir, err)
	}
	if err := favorites.NewStore(path).Save(list); err != nil {
		t.Fatal(err)
	}
}

// runSub runs openlamt-mcp with args and the fakes.
func runSub(t *testing.T, args []string, d deps) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runWith(args, &out, &errOut, noServe(t), d)
	return code, out.String(), errOut.String()
}

var (
	labTLS   = favorites.Favorite{Name: "Lab Server", Host: "lab.example.com", Username: "admin", UseTLS: true}
	nucPlain = favorites.Favorite{Name: "NUC", Host: "192.0.2.10", Username: "admin"}
)

// TestPasswordSubcommandStoresInKeychain: the password is asked twice without echo and stored
// in the keychain; it never appears in the output.
func TestPasswordSubcommandStoresInKeychain(t *testing.T) {
	dir := isolate(t)
	saveFavorites(t, dir, labTLS, nucPlain)
	keys := keychain.NewMemory()
	term := &fakeTerminal{tty: true, passwords: []string{"S3cret!", "S3cret!"}}
	code, stdout, stderr := runSub(t, []string{"password", "Lab Server"}, deps{keys: keys, term: term})
	if code != 0 || !strings.Contains(stdout, "Saved the password") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if pw, _ := keys.Get("lab.example.com", "admin"); pw != "S3cret!" {
		t.Fatalf("stored %q", pw)
	}
	if strings.Contains(stdout+stderr, "S3cret") || len(term.prompts) != 2 {
		t.Fatalf("output %q %q, prompts %q", stdout, stderr, term.prompts)
	}
}

// TestPasswordSubcommandRefuses: no terminal, different or empty answers, an unknown favorite
// and a configured password command store nothing.
func TestPasswordSubcommandRefuses(t *testing.T) {
	dir := isolate(t)
	saveFavorites(t, dir, labTLS)
	cmdConfig := filepath.Join(dir, "with-command.json")
	if err := os.WriteFile(cmdConfig, []byte(`{"passwords": {"command": ["pass", "show", "openlamt/{host}"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		term *fakeTerminal
		want string
	}{
		{"no tty", []string{"password", "Lab Server"}, &fakeTerminal{passwords: []string{"a", "a"}}, "needs a terminal"},
		{"differ", []string{"password", "Lab Server"}, &fakeTerminal{tty: true, passwords: []string{"a", "b"}}, "differ"},
		{"empty", []string{"password", "Lab Server"}, &fakeTerminal{tty: true, passwords: []string{"", ""}}, "empty"},
		{"unknown", []string{"password", "Ghost"}, &fakeTerminal{tty: true, passwords: []string{"a", "a"}}, `no favorite named "Ghost"`},
		{"command", []string{"--config", cmdConfig, "password", "Lab Server"}, &fakeTerminal{tty: true, passwords: []string{"a", "a"}}, "password manager"},
		{"command after", []string{"password", "--config", cmdConfig, "Lab Server"}, &fakeTerminal{tty: true, passwords: []string{"a", "a"}}, "password manager"},
	} {
		keys := keychain.NewMemory()
		code, _, stderr := runSub(t, c.args, deps{keys: keys, term: c.term})
		if code != 1 || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: code=%d stderr=%q", c.name, code, stderr)
		}
		if _, err := keys.Get("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("%s: a password was stored", c.name)
		}
	}
	if code, _, stderr := runSub(t, []string{"password"}, deps{keys: keychain.NewMemory(), term: &fakeTerminal{tty: true}}); code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("no name: %d %q", code, stderr)
	}
}

// TestTrustSubcommandPinsAfterYes reads the certificate of a real TLS listener, shows its
// SHA-256 fingerprint as the app does and pins it only after "y"; a matching pin is kept and a
// different one is replaced only after a warning and "y".
func TestTrustSubcommandPinsAfterYes(t *testing.T) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	sum := sha256.Sum256(ts.Certificate().Raw)
	want := hex.EncodeToString(sum[:])
	var hosts []string
	fetch := func(ctx context.Context, host string) (string, error) {
		hosts = append(hosts, host)
		return amt.FetchFingerprint(ctx, ts.Listener.Addr().String())
	}
	dir := isolate(t)
	saveFavorites(t, dir, labTLS, nucPlain)
	keys := keychain.NewMemory()

	term := &fakeTerminal{tty: true, line: "n"}
	code, stdout, stderr := runSub(t, []string{"trust", "Lab Server"}, deps{keys: keys, term: term, fetchFP: fetch})
	formatted := amt.FormatFingerprint(want)
	if code != 1 || !strings.Contains(stderr, "not trusted") || !strings.Contains(stdout, formatted[:23]) {
		t.Fatalf("no: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := keys.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("pinned after no")
	}
	if len(hosts) != 1 || hosts[0] != "lab.example.com" {
		t.Fatalf("fetched %v", hosts)
	}

	term = &fakeTerminal{tty: true, line: "y"}
	if code, stdout, stderr = runSub(t, []string{"trust", "Lab Server"}, deps{keys: keys, term: term, fetchFP: fetch}); code != 0 || !strings.Contains(stdout, "Trusted.") {
		t.Fatalf("yes: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if pin, _ := keys.GetFingerprint("lab.example.com", "admin"); pin != want {
		t.Fatalf("pin %q, want %q", pin, want)
	}

	// Trusted already: nothing to ask.
	term = &fakeTerminal{tty: true, line: "y"}
	if code, stdout, _ = runSub(t, []string{"trust", "Lab Server"}, deps{keys: keys, term: term, fetchFP: fetch}); code != 0 ||
		!strings.Contains(stdout, "trusted already") || len(term.prompts) != 0 {
		t.Fatalf("again: code=%d stdout=%q prompts=%q", code, stdout, term.prompts)
	}

	// Another certificate is pinned: a warning with both, replaced only after yes.
	other := strings.Repeat("cd", 32)
	if err := keys.SetFingerprint("lab.example.com", "admin", other); err != nil {
		t.Fatal(err)
	}
	term = &fakeTerminal{tty: true, line: ""}
	code, stdout, _ = runSub(t, []string{"trust", "Lab Server"}, deps{keys: keys, term: term, fetchFP: fetch})
	if code != 1 || !strings.Contains(stdout, "man-in-the-middle") || !strings.Contains(stdout, "CD:CD:CD") {
		t.Fatalf("mismatch: code=%d stdout=%q", code, stdout)
	}
	if pin, _ := keys.GetFingerprint("lab.example.com", "admin"); pin != other {
		t.Fatal("replaced without yes")
	}
	term = &fakeTerminal{tty: true, line: "yes"}
	if code, _, _ = runSub(t, []string{"trust", "Lab Server"}, deps{keys: keys, term: term, fetchFP: fetch}); code != 0 {
		t.Fatalf("replace: code=%d", code)
	}
	if pin, _ := keys.GetFingerprint("lab.example.com", "admin"); pin != want {
		t.Fatal("not replaced after yes")
	}
}

// TestTrustSubcommandRefuses: no terminal, a favorite without TLS and an unreachable device pin
// nothing.
func TestTrustSubcommandRefuses(t *testing.T) {
	dir := isolate(t)
	saveFavorites(t, dir, labTLS, nucPlain)
	unreachable := func(context.Context, string) (string, error) { return "", errors.New("connection refused") }
	for _, c := range []struct {
		name, fav string
		tty       bool
		want      string
	}{
		{"no tty", "Lab Server", false, "needs a terminal"},
		{"no tls", "NUC", true, "does not use TLS"},
		{"unreachable", "Lab Server", true, "cannot read the certificate"},
	} {
		keys := keychain.NewMemory()
		code, _, stderr := runSub(t, []string{"trust", c.fav}, deps{keys: keys, term: &fakeTerminal{tty: c.tty, line: "y"}, fetchFP: unreachable})
		if code != 1 || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: code=%d stderr=%q", c.name, code, stderr)
		}
		if _, err := keys.GetFingerprint("lab.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("%s: pinned", c.name)
		}
	}
}

// TestRunRefusesPrivateEnvFileOthersCanRead: with allow_plaintext_passwords, an .env file that
// others may read stops the server before it serves.
func TestRunRefusesPrivateEnvFileOthersCanRead(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("no Unix file modes on Windows")
	}
	dir := isolate(t)
	cfg := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(cfg, []byte(`{"passwords": {"allow_plaintext_passwords": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, mcpserver.EnvFileName), []byte("OPENLAMT_PASSWORD_X=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", cfg}, &stdout, &stderr, noServe(t)); code != 1 || !strings.Contains(stderr.String(), "0600") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if err := os.Chmod(filepath.Join(dir, mcpserver.EnvFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	served := false
	code := run([]string{"--config", cfg}, &stdout, &stderr, func(context.Context, *mcp.Server) error { served = true; return nil })
	if code != 0 || !served || !strings.Contains(stderr.String(), "allow_plaintext_passwords is on") || stdout.Len() != 0 {
		t.Fatalf("code=%d served=%v stderr=%q", code, served, stderr.String())
	}
}

// TestTTYTerminalSeesNoTerminal: the real terminal check says no for input from a file, so a
// password or a "y" cannot be piped in.
func TestTTYTerminalSeesNoTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if (ttyTerminal{in: f, out: &bytes.Buffer{}}).isTerminal() {
		t.Fatal("a file counts as a terminal")
	}
}
