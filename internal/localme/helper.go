package localme

import (
	"io"
	"os"
	"path/filepath"
)

// HelperFlag starts the program as the elevated check helper instead of the app:
//
//	openlamt --local-check               prints the Result to stdout (Linux, under pkexec)
//	openlamt --local-check --out <file>  writes it to a new file named check.json (Windows,
//	                                     where an elevated process has no stdout for the app)
//
// The helper reads Intel AMT and nothing else: it opens no window, reads no settings and no
// keychain, and it cannot change Intel AMT (see reader).
const HelperFlag = "--local-check"

// resultFile is the only file name the helper writes to.
const resultFile = "check.json"

// Exit codes of the helper. pkexec itself uses 126 and 127.
const (
	helperOK    = 0
	helperUsage = 2 // the arguments are not one of the two forms above
	helperWrite = 3 // the answer could not be written
)

// IsHelper reports whether args (os.Args[1:]) ask for the helper.
func IsHelper(args []string) bool { return len(args) > 0 && args[0] == HelperFlag }

// openReader opens the Intel ME of this computer; tests replace it.
var openReader = newReader

// RunHelper runs the helper for args (os.Args[1:]) and returns its exit code. It runs before
// the app starts.
func RunHelper(args []string, stdout io.Writer) int {
	var out string
	switch {
	case len(args) == 1 && args[0] == HelperFlag:
	case len(args) == 3 && args[0] == HelperFlag && args[1] == "--out" && filepath.Base(args[2]) == resultFile && filepath.IsAbs(args[2]):
		out = args[2]
	default:
		return helperUsage
	}
	b, err := Encode(read(openReader()))
	if err != nil {
		return helperWrite
	}
	if out == "" {
		if _, err := stdout.Write(b); err != nil {
			return helperWrite
		}
		return helperOK
	}
	return writeNew(out, b)
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
