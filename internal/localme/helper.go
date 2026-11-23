package localme

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// HelperFlag starts the program as the elevated check helper instead of the app:
//
//	openlamt --local-check               prints the Result to stdout (Linux, under pkexec)
//	openlamt --local-check --out <file>  writes it to a new file named check.json (Windows,
//	                                     where an elevated process has no stdout for the app)
//
// The check helper reads Intel AMT and nothing else: it opens no window, reads no settings and
// no keychain, and it cannot change Intel AMT (see reader).
//
// The setup actions (setup.go) are helpers with their own flag, in the same two forms:
//
//	openlamt --local-enable | --local-deactivate [--out <file>]
//	openlamt --local-activate-ccm                 reads the new password from stdin (Linux)
//	openlamt --local-activate-ccm --pipe <name> --out <file>
//	                                              reads it from the app's named pipe (Windows)
//
// They change Intel AMT through changer, read it again like Check and answer an ActionResult.
// The password never appears in the arguments, a file or the answer.
const HelperFlag = "--local-check"

// resultFile is the only file name the helper writes to.
const resultFile = "check.json"

// Exit codes of the helper. pkexec itself uses 126 and 127.
const (
	helperOK     = 0
	helperUsage  = 2 // the arguments are not one of the forms above
	helperWrite  = 3 // the answer could not be written
	helperSecret = 4 // the activation got no valid password; nothing was changed
)

// pipeName is the only form of the Windows password pipe: the app creates it with a random name.
var pipeName = regexp.MustCompile(`^\\\\\.\\pipe\\openlamt-[0-9a-f]{32}$`)

// IsHelper reports whether args (os.Args[1:]) ask for a helper.
func IsHelper(args []string) bool {
	return len(args) > 0 && (args[0] == HelperFlag || slices.Contains(actions, Action(args[0])))
}

// openReader opens the Intel ME of this computer; tests replace it.
var openReader = newReader

// openChanger opens the Intel ME of this computer for a setup action; tests replace it.
var openChanger = newChanger

// helperCall is a parsed helper command line.
type helperCall struct {
	action Action // "" for the check
	out    string // the answer file; "" for stdout
	pipe   string // the Windows password pipe of the activation
}

// parseHelper accepts exactly the forms of HelperFlag and refuses everything else.
func parseHelper(args []string) (helperCall, bool) {
	validOut := func(p string) bool { return filepath.IsAbs(p) && filepath.Base(p) == resultFile }
	if len(args) == 0 {
		return helperCall{}, false
	}
	var c helperCall
	switch a := args[0]; {
	case a == HelperFlag:
	case slices.Contains(actions, Action(a)):
		c.action = Action(a)
	default:
		return helperCall{}, false
	}
	rest := args[1:]
	if c.action == ActionActivateCCM && len(rest) == 4 && rest[0] == "--pipe" && pipeName.MatchString(rest[1]) {
		c.pipe, rest = rest[1], rest[2:]
		if rest[0] != "--out" || !validOut(rest[1]) {
			return helperCall{}, false
		}
	}
	switch {
	case len(rest) == 0:
	case len(rest) == 2 && rest[0] == "--out" && validOut(rest[1]):
		c.out = rest[1]
	default:
		return helperCall{}, false
	}
	return c, true
}

// RunHelper runs the helper for args (os.Args[1:]) and returns its exit code. It runs before
// the app starts; stdin carries the password of a Linux activation.
func RunHelper(args []string, stdin io.Reader, stdout io.Writer) int {
	c, ok := parseHelper(args)
	if !ok {
		return helperUsage
	}
	var b []byte
	var err error
	if c.action == "" {
		b, err = Encode(read(openReader()))
	} else {
		a, code := runAction(c, stdin)
		if code != helperOK {
			return code
		}
		b, err = encodeAction(a)
	}
	if err != nil {
		return helperWrite
	}
	if c.out == "" {
		if _, err := stdout.Write(b); err != nil {
			return helperWrite
		}
		return helperOK
	}
	return writeNew(c.out, b)
}

// errNoME: this platform has no Intel ME to change (macOS).
var errNoME = errors.New("no Intel ME on this platform")

// runAction reads the password (activation only), makes the change and reads the state again.
// A change that fails is the answer's Refused, not an exit code: the state is read anyway.
func runAction(c helperCall, stdin io.Reader) (ActionResult, int) {
	var secret []byte
	if c.action == ActionActivateCCM {
		var err error
		if secret, err = readPassword(c.pipe, stdin); err != nil {
			return ActionResult{}, helperSecret
		}
		defer clear(secret)
	}
	restore := quietStdio()
	defer restore()
	var err error
	if ch := openChanger(); ch == nil {
		err = errNoME
	} else {
		switch c.action {
		case ActionTurnOn:
			err = ch.TurnOn()
		case ActionActivateCCM:
			err = ch.ActivateCCM(string(secret))
		case ActionDeactivate:
			err = ch.Deactivate()
		}
	}
	res := ActionResult{State: read(openReader())}
	var foreign *ForeignLMSError
	switch {
	case errors.As(err, &foreign):
		res.ForeignPort = foreign.Port
	case err != nil:
		res.Refused = refusal(err, secret)
	}
	return res, helperOK
}

// readPassword reads the password of an activation from the pipe (Windows) or stdin (Linux).
func readPassword(pipe string, stdin io.Reader) ([]byte, error) {
	if pipe == "" {
		if stdin == nil {
			return nil, ErrPassword
		}
		return readSecret(stdin)
	}
	r, err := openPipe(pipe)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readSecret(r)
}

// quietStdio points os.Stdin and os.Stdout to the null device while rpc-go runs: nothing it
// prints can mix into the answer on stdout, and a prompt it might show reads an empty input
// instead of waiting. The helper's answer goes to the writer RunHelper got.
func quietStdio() (restore func()) {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return func() {}
	}
	in, out := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = null, null
	return func() {
		os.Stdin, os.Stdout = in, out
		null.Close()
	}
}

// writeNew writes b to a file that must not exist yet: the elevated helper never replaces a
// file, nor one that a link in its place points to.
func writeNew(path string, b []byte) int {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return helperWrite
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return helperWrite
	}
	return helperOK
}
