package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// helperEnv makes the test binary act as a password command (TestHelperPasswordCommand).
const helperEnv = "OPENLAMT_TEST_PASSWORD_HELPER"

// TestHelperPasswordCommand is not a test: run as a child with helperEnv set, it plays the
// password command. Its last argument is the substituted placeholder.
func TestHelperPasswordCommand(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	arg := os.Args[len(os.Args)-1]
	switch mode {
	case "print":
		fmt.Printf("pw-for-%s\n", arg)
	case "fail":
		fmt.Fprintln(os.Stderr, "secret stderr text")
		os.Exit(3)
	case "sleep":
		time.Sleep(30 * time.Second)
	case "lines":
		fmt.Print("line1\nline2\n")
	case "empty":
	}
	os.Exit(0)
}

// helperCommand is a passwords.command that runs the test binary in mode with placeholder as
// its last argument.
func helperCommand(t *testing.T, mode, placeholder string) []string {
	t.Helper()
	t.Setenv(helperEnv, mode)
	return []string{os.Args[0], "-test.run=^TestHelperPasswordCommand$", "--", placeholder}
}

var labFav = favorites.Favorite{Name: "Lab Server", Host: "lab.example.com", Username: "admin", UseTLS: true}

func noEnv(string) (string, bool) { return "", false }

// TestPasswordSourcesOrderAndOptIn: the keychain wins, then the command, then (only with
// allow_plaintext_passwords) the environment and the .env file.
func TestPasswordSourcesOrderAndOptIn(t *testing.T) {
	ctx := context.Background()
	keys := keychain.NewMemory()
	envFile := filepath.Join(t.TempDir(), EnvFileName)
	if err := os.WriteFile(envFile, []byte("# lab\nexport OPENLAMT_PASSWORD_LAB_EXAMPLE_COM='from-file'\nOTHER=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	p := newPasswords(PasswordConfig{EnvFile: envFile}, keys)
	p.getenv = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	var ran []string
	command := ""
	p.run = func(_ context.Context, argv []string) ([]byte, error) {
		ran = append(ran, strings.Join(argv, " "))
		return []byte(command), nil
	}
	all := []favorites.Favorite{labFav}
	lookup := func() (string, string, error) { return p.lookup(ctx, labFav, all) }

	// Nothing anywhere, no opt-in: not found, and the .env file is not read.
	if _, _, err := lookup(); !strings.Contains(fmt.Sprint(err), "No password found") {
		t.Fatalf("%v", err)
	}
	// Plaintext only with the opt-in; the environment wins over the file.
	p.cfg.AllowPlaintext = true
	if pw, src, err := lookup(); pw != "from-file" || src != sourceEnvironment || err != nil {
		t.Fatalf("file: %q %q %v", pw, src, err)
	}
	env["OPENLAMT_PASSWORD_LAB_EXAMPLE_COM"] = "from-env"
	if pw, _, _ := lookup(); pw != "from-env" {
		t.Fatalf("env: %q", pw)
	}
	// The command wins over plaintext; an empty output is no password.
	p.cfg.Command = []string{"pass", "show", "openlamt/{host}/{user}", "{name}"}
	if pw, src, _ := lookup(); pw != "from-env" || len(ran) != 1 {
		t.Fatalf("empty command output: %q %q %v", pw, src, ran)
	}
	command = "from-command\n"
	if pw, src, err := lookup(); pw != "from-command" || src != sourceCommand || err != nil {
		t.Fatalf("command: %q %q %v", pw, src, err)
	}
	if ran[1] != "pass show openlamt/lab.example.com/admin Lab Server" {
		t.Fatalf("argv %q", ran[1])
	}
	// The keychain wins over everything and runs no command.
	if err := keys.Set(labFav.Host, labFav.Username, "from-keychain"); err != nil {
		t.Fatal(err)
	}
	if pw, src, err := lookup(); pw != "from-keychain" || src != sourceKeychain || err != nil || len(ran) != 2 {
		t.Fatalf("keychain: %q %q %v %v", pw, src, err, ran)
	}
	// A keychain that cannot be read falls through to the next source.
	keys.Err = errors.New("no Secret Service")
	if pw, src, err := lookup(); pw != "from-command" || src != sourceCommand || err != nil {
		t.Fatalf("keychain down: %q %q %v", pw, src, err)
	}
}

// TestPasswordEnvCollisionIsRefused: two hosts that map to the same variable get no password
// from it, so a password never reaches the wrong host.
func TestPasswordEnvCollisionIsRefused(t *testing.T) {
	if got := PasswordEnvVar(" 192.0.2.10 "); got != "OPENLAMT_PASSWORD_192_0_2_10" {
		t.Fatalf("%q", got)
	}
	p := newPasswords(PasswordConfig{AllowPlaintext: true}, keychain.NewMemory())
	p.getenv = func(k string) (string, bool) { return "pw", k == "OPENLAMT_PASSWORD_A_B_EXAMPLE_COM" }
	a := favorites.Favorite{Name: "A", Host: "a.b.example.com", Username: "admin"}
	b := favorites.Favorite{Name: "B", Host: "a-b.example.com", Username: "admin"}
	if pw, _, err := p.lookup(context.Background(), a, []favorites.Favorite{a}); pw != "pw" || err != nil {
		t.Fatalf("alone: %q %v", pw, err)
	}
	_, _, err := p.lookup(context.Background(), a, []favorites.Favorite{a, b})
	if !strings.Contains(fmt.Sprint(err), "both map to it") {
		t.Fatalf("collision: %v", err)
	}
}

// TestPasswordCommandRuns runs a real command (the test binary): placeholders filled per
// argument, one trailing newline removed, failures and timeouts described without the output.
func TestPasswordCommandRuns(t *testing.T) {
	ctx := context.Background()
	p := newPasswords(PasswordConfig{}, keychain.NewMemory())
	all := []favorites.Favorite{labFav}

	p.cfg.Command = helperCommand(t, "print", "{name}@{host}")
	pw, src, err := p.lookup(ctx, labFav, all)
	if pw != "pw-for-Lab Server@lab.example.com" || src != sourceCommand || err != nil {
		t.Fatalf("print: %q %q %v", pw, src, err)
	}

	for _, c := range []struct{ mode, want string }{
		{"fail", "exited with status 3"},
		{"lines", "more than one line"},
		{"empty", "No password found"},
	} {
		p.cfg.Command = helperCommand(t, c.mode, "{host}")
		_, _, err := p.lookup(ctx, labFav, all)
		if msg := fmt.Sprint(err); !strings.Contains(msg, c.want) || strings.Contains(msg, "secret stderr") || strings.Contains(msg, "line1") {
			t.Errorf("%s: %v", c.mode, err)
		}
	}

	old := passwordCommandTimeout
	passwordCommandTimeout = 300 * time.Millisecond
	t.Cleanup(func() { passwordCommandTimeout = old })
	p.cfg.Command = helperCommand(t, "sleep", "{host}")
	start := time.Now()
	if _, _, err := p.lookup(ctx, labFav, all); !strings.Contains(fmt.Sprint(err), "did not finish within") {
		t.Fatalf("sleep: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the timeout took %v", d)
	}

	p.cfg.Command = []string{filepath.Join(t.TempDir(), "missing-program")}
	if _, _, err := p.lookup(ctx, labFav, all); !strings.Contains(fmt.Sprint(err), "could not be started") {
		t.Fatalf("missing: %v", err)
	}
}

// TestPasswordCommandRefusesUnsafeValues: a name, host or user that could make a path-based
// password manager read another entry, or pass for an option, never reaches the command.
func TestPasswordCommandRefusesUnsafeValues(t *testing.T) {
	p := newPasswords(PasswordConfig{Command: []string{"pass", "show", "openlamt/{name}"}}, keychain.NewMemory())
	p.run = func(context.Context, []string) ([]byte, error) {
		t.Fatal("the command ran")
		return nil, nil
	}
	for _, name := range []string{"../bank", `a\b`, "-x", "a/b", "tab\there"} {
		f := favorites.Favorite{Name: name, Host: "lab.example.com", Username: "admin"}
		if _, _, err := p.lookup(context.Background(), f, nil); !strings.Contains(fmt.Sprint(err), "passwords.command was not run") {
			t.Errorf("%q: %v", name, err)
		}
	}
	// A placeholder the command does not use is not checked.
	p.cfg.Command = []string{"pass", "show", "openlamt/{host}"}
	p.run = func(_ context.Context, argv []string) ([]byte, error) { return []byte("pw"), nil }
	f := favorites.Favorite{Name: "../odd", Host: "lab.example.com", Username: "admin"}
	if pw, _, err := p.lookup(context.Background(), f, nil); pw != "pw" || err != nil {
		t.Fatalf("%q %v", pw, err)
	}
}

// TestEnvFileMustBePrivate: an .env file others may read stops the server at start (Check)
// and is not used; a malformed line names its number, never its value.
func TestEnvFileMustBePrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnvFileName)
	cfg := PasswordConfig{AllowPlaintext: true, EnvFile: path}
	if err := cfg.Check(); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("OPENLAMT_PASSWORD_LAB_EXAMPLE_COM=s3cret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Check(); err != nil {
		t.Fatalf("0600: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Check(); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("0644: %v", err)
		}
		if err := (PasswordConfig{EnvFile: path}).Check(); err != nil {
			t.Fatalf("without the opt-in the file is not read: %v", err)
		}
		p := newPasswords(cfg, keychain.NewMemory())
		p.getenv = noEnv
		_, _, err := p.lookup(context.Background(), labFav, nil)
		if msg := fmt.Sprint(err); !strings.Contains(msg, "not used") || strings.Contains(msg, "s3cret") {
			t.Fatalf("lookup: %v", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("# ok\n\nnot a pair s3cret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Check(); err == nil || !strings.Contains(err.Error(), "line 3") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("malformed: %v", err)
	}
}
