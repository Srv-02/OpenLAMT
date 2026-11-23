package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/mcpserver"
)

// deps are what run needs from the system; tests replace them.
type deps struct {
	keys keychain.Store
	term terminal
	// fetchFP reads the certificate fingerprint of a TLS host (trust).
	fetchFP func(ctx context.Context, host string) (string, error)
}

func defaultDeps(prompts io.Writer) deps {
	return deps{
		keys: keychain.OS{},
		term: ttyTerminal{in: os.Stdin, out: prompts},
		fetchFP: func(ctx context.Context, host string) (string, error) {
			return amt.FetchFingerprint(ctx, amt.Config{Host: host, UseTLS: true}.Address())
		},
	}
}

// terminal is the person's terminal of the subcommands.
type terminal interface {
	// isTerminal reports whether input comes from a terminal (not a pipe or a file).
	isTerminal() bool
	// readPassword shows prompt and reads a line without echo.
	readPassword(prompt string) (string, error)
	// readLine shows prompt and reads a line.
	readLine(prompt string) (string, error)
}

// ttyTerminal reads from in (stdin) and writes prompts to out (stderr).
type ttyTerminal struct {
	in  *os.File
	out io.Writer
}

func (t ttyTerminal) isTerminal() bool { return term.IsTerminal(int(t.in.Fd())) }

func (t ttyTerminal) readPassword(prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	b, err := term.ReadPassword(int(t.in.Fd()))
	fmt.Fprintln(t.out)
	return string(b), err
}

func (t ttyTerminal) readLine(prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	line, err := bufio.NewReader(t.in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// subcommand runs "password" or "trust" for the favorite named in args; configFlag is --config
// given before the subcommand (it may be given after it too).
func subcommand(name string, args []string, configFlag string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("openlamt-mcp "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", configFlag, "config file (default: "+mcpserver.ConfigFileName+" in the OpenLAMT config folder)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: openlamt-mcp %s [--config <file>] <favorite name>\n", name)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		fs.Usage()
		fmt.Fprintln(stderr, "Give the favorite name as one argument; put a name with spaces in quotes.")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "openlamt-mcp %s: %v\n", name, err)
		return 1
	}
	favPath, path, explicit, err := configPaths(*configPath)
	if err != nil {
		return fail(err)
	}
	cfg, err := mcpserver.Load(path, explicit)
	if err != nil {
		return fail(err)
	}
	// The manager reads under the favorites lock, so a save by the app at the same time is no problem.
	fav, err := findFavorite(favorites.NewManager(favorites.NewStore(favPath), d.keys), strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		return fail(err)
	}
	if name == "password" {
		err = storePassword(cfg, fav, stdout, d)
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = trustCertificate(ctx, fav, stdout, d)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

// findFavorite returns the favorite whose name (as the app and list_devices show it) is name.
func findFavorite(favs mcpserver.FavoriteSource, name string) (favorites.Favorite, error) {
	list, err := favs.List()
	if err != nil {
		return favorites.Favorite{}, err
	}
	var found []favorites.Favorite
	var names []string
	for _, f := range list {
		names = append(names, fmt.Sprintf("%q", f.DisplayName()))
		if f.DisplayName() == name {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		if len(names) == 0 {
			return favorites.Favorite{}, fmt.Errorf("no favorite named %q: there are no favorites yet", name)
		}
		return favorites.Favorite{}, fmt.Errorf("no favorite named %q; the favorites are %s", name, strings.Join(names, ", "))
	}
	return favorites.Favorite{}, fmt.Errorf("%d favorites are named %q; give them distinct names first", len(found), name)
}

// errNoTerminal: a subcommand reads from a pipe or a file instead of a person's terminal.
var errNoTerminal = errors.New("this command needs a terminal: a person types the answer, it cannot come from a pipe or a file")

// storePassword asks for the password of fav twice without echo and stores it in the system
// keychain. With passwords.command set it stores nothing: the password belongs in the password
// manager the command reads.
func storePassword(cfg mcpserver.Config, fav favorites.Favorite, stdout io.Writer, d deps) error {
	if len(cfg.Passwords.Command) > 0 {
		return fmt.Errorf("passwords.command is set in the openlamt-mcp config (%s), so openlamt-mcp reads passwords from your "+
			"password manager. Store the password of %q (user %q on %s) there; nothing was saved",
			cfg.Passwords.Command[0], fav.DisplayName(), fav.Username, fav.Host)
	}
	if !d.term.isTerminal() {
		return errNoTerminal
	}
	fmt.Fprintf(stdout, "Password of %q: user %q on %s.\n", fav.DisplayName(), fav.Username, fav.Host)
	pw, err := d.term.readPassword("Password: ")
	if err != nil {
		return err
	}
	if pw == "" {
		return errors.New("the password is empty; nothing was saved")
	}
	again, err := d.term.readPassword("Repeat the password: ")
	if err != nil {
		return err
	}
	if again != pw {
		return errors.New("the passwords differ; nothing was saved")
	}
	if err := d.keys.Set(fav.Host, fav.Username, pw); err != nil {
		return fmt.Errorf("cannot write the system keychain (%v). On Linux a Secret Service provider such as gnome-keyring "+
			"must be running; without one, set passwords.command in the openlamt-mcp config (see the MCP guide)", err)
	}
	fmt.Fprintf(stdout, "Saved the password of %q in the system keychain. openlamt-mcp and the OpenLAMT app use it from now on.\n", fav.DisplayName())
	return nil
}

// trustCertificate reads the certificate of the TLS favorite fav, shows its SHA-256 fingerprint
// and pins it after the person answered yes: trust on first use, as in the app. A pin that
// matches is kept; a different one is replaced only after a warning and a yes.
func trustCertificate(ctx context.Context, fav favorites.Favorite, stdout io.Writer, d deps) error {
	if !fav.UseTLS {
		return fmt.Errorf("%q does not use TLS, so there is no certificate to trust; turn TLS on for the favorite first", fav.DisplayName())
	}
	if !d.term.isTerminal() {
		return errNoTerminal
	}
	pinned, err := d.keys.GetFingerprint(fav.Host, fav.Username)
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		pinned = ""
	case err != nil:
		return fmt.Errorf("cannot read the system keychain (%v). On Linux a Secret Service provider such as gnome-keyring must be running", err)
	}
	fmt.Fprintf(stdout, "Connecting to %s …\n", fav.Host)
	fp, err := d.fetchFP(ctx, fav.Host)
	if err != nil {
		return fmt.Errorf("cannot read the certificate of %s: %v", fav.Host, err)
	}
	if pinned != "" {
		if norm, err := amt.NormalizeFingerprint(pinned); err == nil && norm == fp {
			fmt.Fprintf(stdout, "The certificate of %q is trusted already (SHA-256):\n%s\n", fav.DisplayName(), fingerprintBlock(fp))
			return nil
		}
		fmt.Fprintf(stdout, "WARNING: the certificate of %s does not match the trusted fingerprint. This may be a man-in-the-middle attack.\n\n"+
			"Trusted:\n%s\n\nReceived:\n%s\n\n", fav.Host, fingerprintBlock(pinned), fingerprintBlock(fp))
		fmt.Fprintln(stdout, "Replace the trusted certificate only if you changed the certificate of the device yourself.")
	} else {
		fmt.Fprintf(stdout, "The device presents this certificate (SHA-256):\n%s\n\n", fingerprintBlock(fp))
		fmt.Fprintln(stdout, "If you trust it, OpenLAMT remembers the certificate and refuses the connection if it ever changes.")
	}
	question := "Trust this certificate? [y/N] "
	if pinned != "" {
		question = "Replace the trusted certificate with the received one? [y/N] "
	}
	answer, err := d.term.readLine(question)
	if err != nil {
		return err
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("not trusted; nothing was saved")
	}
	if err := d.keys.SetFingerprint(fav.Host, fav.Username, fp); err != nil {
		return fmt.Errorf("cannot write the system keychain (%v). On Linux a Secret Service provider such as gnome-keyring must be running", err)
	}
	fmt.Fprintf(stdout, "Trusted. openlamt-mcp and the OpenLAMT app accept only this certificate for %q from now on.\n", fav.DisplayName())
	return nil
}

// fingerprintBlock shows a fingerprint as four lines of eight bytes each, as the app does.
func fingerprintBlock(fp string) string {
	parts := strings.Split(amt.FormatFingerprint(strings.ReplaceAll(fp, ":", "")), ":")
	var lines []string
	for i := 0; i < len(parts); i += 8 {
		lines = append(lines, "  "+strings.Join(parts[i:min(i+8, len(parts))], ":"))
	}
	return strings.Join(lines, "\n")
}
