package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// Where a device password came from (list_devices reports it as password_source).
const (
	sourceKeychain    = "keychain"
	sourceCommand     = "command"
	sourceEnvironment = "environment"
)

// PasswordEnvPrefix starts the environment variables (and .env entries) that hold a device
// password: PasswordEnvPrefix + the encoded host (see PasswordEnvVar).
const PasswordEnvPrefix = "OPENLAMT_PASSWORD_"

// passwordCommandTimeout bounds one run of passwords.command (a variable for the tests).
var passwordCommandTimeout = 10 * time.Second

// maxCommandOutput is the most stdout the server keeps of the password command.
const maxCommandOutput = 64 << 10

// PlaintextWarning goes to stderr at start and into every tool answer while
// passwords.allow_plaintext_passwords is on.
const PlaintextWarning = "Warning: allow_plaintext_passwords is on in the openlamt-mcp config. Device passwords may come from " +
	"environment variables or a .env file in plain text; any program that can read them can read the passwords. " +
	"Use the system keychain or passwords.command instead."

// PasswordEnvVar is the environment variable (and .env key) of the password of host, after
// PasswordEnvPrefix: each lower-case letter a-z and digit of the host as an upper-case letter or
// digit, every other byte as "_" and its two upper-case hex digits ("." is "_2E", "-" is "_2D").
// The encoding is injective, so no two hosts share a variable, and it is keyed by the host, not
// the favorite name, so a password reaches only the host it was set for.
func PasswordEnvVar(host string) string {
	var b strings.Builder
	b.WriteString(PasswordEnvPrefix)
	for _, c := range []byte(host) {
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - 'a' + 'A')
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02X", c)
		}
	}
	return b.String()
}

// Check validates the plaintext source at start: with allow_plaintext_passwords, an existing
// .env file must be readable by its owner only and parse. Without the opt-in nothing is read.
func (c PasswordConfig) Check() error {
	if !c.AllowPlaintext {
		return nil
	}
	_, err := readEnvFile(c.EnvFile)
	return err
}

// readEnvFile reads the OPENLAMT_PASSWORD_ entries of the .env file at path; a missing file is
// empty. On Unix the file must not be readable or writable by group or others. A line is
// KEY=VALUE, optionally after "export "; blank lines and lines starting with # are skipped; a
// value in matching single or double quotes loses them. Errors never quote a value.
func readEnvFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s holds passwords but other users may read or change it (mode %04o): "+
			"make it readable by its owner only (mode 0600)", path, info.Mode().Perm())
	}
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE", path, n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if strings.HasPrefix(key, PasswordEnvPrefix) {
			out[key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// passwords finds the password of a favorite: the OS keychain first, then passwords.command, then
// (only with allow_plaintext_passwords) the environment and the .env file. The first source that
// has one wins.
type passwords struct {
	cfg    PasswordConfig
	keys   keychain.Store
	getenv func(string) (string, bool)
	run    func(ctx context.Context, argv []string) ([]byte, error)
}

func newPasswords(cfg PasswordConfig, keys keychain.Store) *passwords {
	return &passwords{cfg: cfg, keys: keys, getenv: os.LookupEnv, run: runPasswordCommand}
}

// passwordNotFoundError: no source had a password for the favorite. It says what was tried, so
// the agent can tell the person what is missing; it never holds a password.
type passwordNotFoundError struct {
	name        string
	keychainErr error    // the keychain could not be read
	reasons     []string // one per other source that failed in an unusual way
}

func (e *passwordNotFoundError) Error() string {
	msg := fmt.Sprintf("No password found for %q.", e.name)
	if e.keychainErr != nil {
		msg += fmt.Sprintf(" The system keychain cannot be read (%v).", e.keychainErr)
	}
	if len(e.reasons) > 0 {
		msg += " " + strings.Join(e.reasons, " ")
	}
	return msg + " A person stores it with " + commandLine("password", e.name) + " in a terminal on this computer, " +
		"or saves it in the OpenLAMT app; with passwords.command set, it belongs in the password manager. " +
		"The MCP server never takes a password from the agent."
}

// lookup returns the password of fav and its source. Every source is bound to the host of fav
// (keychain: host and user; passwords.command: {host} is required; environment: the host is the
// variable name), so a favorite pointed at another host never gets this host's password. Only
// *passwordNotFoundError and context errors are returned.
func (p *passwords) lookup(ctx context.Context, fav favorites.Favorite) (pw, source string, err error) {
	nf := &passwordNotFoundError{name: fav.DisplayName()}
	pw, err = p.keys.Get(fav.Host, fav.Username)
	switch {
	case err == nil && pw != "":
		return pw, sourceKeychain, nil
	case err != nil && !errors.Is(err, keychain.ErrNotFound):
		nf.keychainErr = err
	}
	if len(p.cfg.Command) > 0 {
		pw, err := p.fromCommand(ctx, fav)
		if err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			nf.reasons = append(nf.reasons, err.Error())
		} else if pw != "" {
			return pw, sourceCommand, nil
		}
	}
	if p.cfg.AllowPlaintext {
		pw, err := p.fromEnvironment(fav)
		if err != nil {
			nf.reasons = append(nf.reasons, err.Error())
		} else if pw != "" {
			return pw, sourceEnvironment, nil
		}
	}
	return "", "", nf
}

// unsafePlaceholderValue reports why v must not go into the password command: a path separator
// or ".." could make a path-based password manager read another entry, a leading "-" could be
// taken for an option, and control characters have no place in an argument. The agent chooses
// names and hosts of the favorites it adds, so these values are not trusted.
func unsafePlaceholderValue(v string) string {
	switch {
	case v == "":
		return "is empty"
	case strings.ContainsAny(v, `/\`) || strings.Contains(v, ".."):
		return `holds "/", "\" or ".."`
	case strings.HasPrefix(v, "-"):
		return `starts with "-"`
	case strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }):
		return "holds a control character"
	}
	return ""
}

// errCommandWithoutHost: passwords.command has no {host}, so its password would not be bound to
// the host it goes to (Load refuses such a config; this is the second line of defense).
var errCommandWithoutHost = errors.New("passwords.command was not run: it must contain {host}, so a password is bound to its host.")

// commandHasHost reports whether an argument of the command holds {host}.
func commandHasHost(argv []string) bool {
	return slices.ContainsFunc(argv, func(a string) bool { return strings.Contains(a, "{host}") })
}

// commandArgs is passwords.command with the placeholders of fav filled in, one argument at a
// time; no shell is involved.
func (p *passwords) commandArgs(fav favorites.Favorite) ([]string, error) {
	if !commandHasHost(p.cfg.Command) {
		return nil, errCommandWithoutHost
	}
	values := map[string]string{"{name}": fav.DisplayName(), "{host}": fav.Host, "{user}": fav.Username}
	argv := make([]string, len(p.cfg.Command))
	for i, a := range p.cfg.Command {
		for ph, v := range values {
			if !strings.Contains(a, ph) {
				continue
			}
			if why := unsafePlaceholderValue(v); why != "" {
				return nil, fmt.Errorf("passwords.command was not run: the value for %s %s.", ph, why)
			}
			a = strings.ReplaceAll(a, ph, v)
		}
		argv[i] = a
	}
	return argv, nil
}

// fromCommand runs passwords.command for fav. Its stdout minus one trailing line break is the
// password; an empty output is no password. A failure is described without its output.
func (p *passwords) fromCommand(ctx context.Context, fav favorites.Favorite) (string, error) {
	argv, err := p.commandArgs(fav)
	if err != nil {
		return "", err
	}
	out, err := p.run(ctx, argv)
	if err != nil {
		return "", fmt.Errorf("passwords.command (%s) %v.", argv[0], err)
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
	if strings.ContainsAny(pw, "\r\n") {
		return "", fmt.Errorf("passwords.command (%s) printed more than one line; it must print the password only.", argv[0])
	}
	return pw, nil
}

// errOutputTooLong: the password command printed more than maxCommandOutput bytes.
var errOutputTooLong = errors.New("output too long")

// cappedBuffer keeps up to maxCommandOutput bytes and notes when more came.
type cappedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxCommandOutput - b.Len(); len(p) > room {
		b.over = true
		b.Buffer.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// runPasswordCommand runs argv without a shell, with no stdin (it must not read the MCP
// protocol), within passwordCommandTimeout. stderr is discarded: it may hold secrets. The error
// texts complete "passwords.command (<program>) …".
func runPasswordCommand(ctx context.Context, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, passwordCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out cappedBuffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, &out, nil
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return nil, fmt.Errorf("did not finish within %v", passwordCommandTimeout)
	case errors.As(err, &exit):
		return nil, fmt.Errorf("exited with status %d (its error output is not shown, it may hold secrets; run the command in a terminal to see it)", exit.ExitCode())
	case err != nil:
		return nil, fmt.Errorf("could not be started: %v", err)
	case out.over:
		return nil, errOutputTooLong
	}
	return out.Bytes(), nil
}

// fromEnvironment reads the password of fav from its environment variable, else from the .env
// file.
func (p *passwords) fromEnvironment(fav favorites.Favorite) (string, error) {
	name := PasswordEnvVar(fav.Host)
	if v, ok := p.getenv(name); ok && v != "" {
		return v, nil
	}
	vars, err := readEnvFile(p.cfg.EnvFile)
	if err != nil {
		return "", fmt.Errorf("The .env file is not used: %v.", err)
	}
	return vars[name], nil
}
