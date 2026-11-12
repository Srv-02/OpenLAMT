package ui

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/mkiso"
	"github.com/Srv-02/openlamt/internal/redir"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/sol"
	"github.com/Srv-02/openlamt/internal/update"
)

// exportedErrors lists every exported error the internal packages declare, as "pkg.Name": the
// variables named Err… and the exported types with an Error method.
func exportedErrors(t *testing.T) []string {
	t.Helper()
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					for _, n := range spec.(*ast.ValueSpec).Names {
						if strings.HasPrefix(n.Name, "Err") && n.IsExported() {
							found[f.Name.Name+"."+n.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name != "Error" || d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok && id.IsExported() {
					found[f.Name.Name+"."+id.Name] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// errorSamples has one value for every exported error that describe must turn into a sentence.
func errorSamples() map[string]error {
	return map[string]error{
		"amt.CertificateUncertainError": &amt.CertificateUncertainError{Fingerprint: "ab", Err: errors.New("x")},
		"amt.ConsentError":              &amt.ConsentError{Step: "StartOptIn", ReturnValue: 2},
		"amt.CredentialsUncertainError": &amt.CredentialsUncertainError{Username: "admin", Err: errors.New("x")},
		"amt.ErrAccessDenied":           amt.ErrAccessDenied,
		"amt.ErrCertificateUncertain":   amt.ErrCertificateUncertain,
		"amt.ErrConfirmationMismatch":   amt.ErrConfirmationMismatch,
		"amt.ErrConsentRequired":        amt.ErrConsentRequired,
		"amt.ErrCredentialsUncertain":   amt.ErrCredentialsUncertain,
		"amt.ErrIDERUnsupported":        amt.ErrIDERUnsupported,
		"amt.ErrIPv6Unsupported":        amt.ErrIPv6Unsupported,
		"amt.ErrIncompleteResponse":     amt.ErrIncompleteResponse,
		"amt.ErrInvalidConsentCode":     amt.ErrInvalidConsentCode,
		"amt.ErrInvalidValue":           amt.ErrInvalidValue,
		"amt.ErrNotModifiable":          amt.ErrNotModifiable,
		"amt.ErrProtectedAccount":       amt.ErrProtectedAccount,
		"amt.ErrRiskyChange":            amt.ErrRiskyChange,
		"amt.FingerprintMismatchError":  &amt.FingerprintMismatchError{Host: "device.example.com"},
		"amt.IDERError":                 &amt.IDERError{Step: "RequestStateChange", ReturnValue: 1},
		"amt.KVMError":                  &amt.KVMError{Step: "RequestStateChange", ReturnValue: 1},
		"amt.MethodError":               &amt.MethodError{Method: "SetHighAccuracyTimeSynch", ReturnValue: 1},
		"amt.PowerError":                &amt.PowerError{Action: amt.PowerOff, ReturnValue: 1},
		"amt.RiskyChangeError":          &amt.RiskyChangeError{Op: amt.OpNetwork, Fields: []string{"IPv4"}},
		"amt.SOLError":                  &amt.SOLError{Step: "RequestStateChange", ReturnValue: 1},
		"amt.ValidationError":           &amt.ValidationError{Field: "HostName", Reason: "must not be empty"},
		"favorites.ErrExists":           favorites.ErrExists,
		"favorites.ErrNotFound":         favorites.ErrNotFound,
		"favorites.KeychainError":       &favorites.KeychainError{Err: errors.New("x")},
		"ider.ErrClosedByDevice":        ider.ErrClosedByDevice,
		"ider.ErrToggleFailed":          ider.ErrToggleFailed,
		"kvm.ErrAuthDataTooLong":        kvm.ErrAuthDataTooLong,
		"kvm.ErrAuthUnsupported":        kvm.ErrAuthUnsupported,
		"kvm.ErrClosed":                 kvm.ErrClosed,
		"kvm.ErrFieldTooLong":           kvm.ErrFieldTooLong,
		"kvm.ErrRedirProtocol":          kvm.ErrRedirProtocol,
		"kvm.ErrTileTooLarge":           kvm.ErrTileTooLarge,
		"kvm.ErrZlibHeader":             kvm.ErrZlibHeader,
		"kvm.RFBError":                  &kvm.RFBError{Stage: "serverinit", Reason: "x"},
		"kvm.TileError":                 &kvm.TileError{Subencoding: 1, Reason: "x"},
		"mkiso.ErrDirTooLarge":          mkiso.ErrDirTooLarge,
		"mkiso.ErrEmpty":                mkiso.ErrEmpty,
		"mkiso.ErrFileChanged":          mkiso.ErrFileChanged,
		"mkiso.ErrFileTooLarge":         mkiso.ErrFileTooLarge,
		"mkiso.ErrNameConflict":         mkiso.ErrNameConflict,
		"mkiso.ErrNameTooLong":          mkiso.ErrNameTooLong,
		"mkiso.ErrTooLarge":             mkiso.ErrTooLarge,
		"mkiso.ErrTooManyDirs":          mkiso.ErrTooManyDirs,
		"mkiso.PathError":               &mkiso.PathError{Path: "dir/file.bin", Err: mkiso.ErrNameTooLong},
		"redir.ErrAuthDataTooLong":      redir.ErrAuthDataTooLong,
		"redir.ErrAuthUnsupported":      redir.ErrAuthUnsupported,
		"redir.ErrFieldTooLong":         redir.ErrFieldTooLong,
		"redir.ErrRedirProtocol":        redir.ErrRedirProtocol,
		"redir.RedirectionError":        &redir.RedirectionError{Stage: "start", Status: 1},
		"session.ErrAuth":               session.ErrAuth,
		"session.ErrNotSaved":           session.ErrNotSaved,
		"session.ErrStopped":            session.ErrStopped,
		"session.NotSavedError":         &session.NotSavedError{Username: "admin", Err: errors.New("x")},
		"sol.ErrClosedByDevice":         sol.ErrClosedByDevice,
		"sol.ErrStartFailed":            sol.ErrStartFailed,
		"ui.ErrInterval":                ErrInterval,
		"update.ErrDigest":              update.ErrDigest,
	}
}

// errorExceptions are exported errors that never reach the error dialog, each with the reason.
var errorExceptions = map[string]string{
	"amt.ErrTrustDeclined":    "the user's own choice; connect.go shows nothing",
	"keychain.ErrNotFound":    "control flow; connect.go falls back to asking for the password",
	"ider.ErrClosed":          "the user's own unmount; media.go skips the report",
	"sol.ErrClosed":           "the user's own close; console.go skips the report",
	"kvm.ErrUnmappedKey":      "key events ignore the error; typing plans only contain mapped keys",
	"typing.ErrNotSendable":   "unreachable: type_clipboard.go only sends plans with typeable characters",
	"typing.ErrUnknownLayout": "unreachable: resolveLayout only returns known layouts",
}

// TestDescribeCoversExportedErrors parses the internal packages for exported errors and checks
// that describe gives each a sentence of its own instead of the "Unexpected error" fallback.
// A new exported error fails this test until it has a case in describe or an entry in
// errorExceptions.
func TestDescribeCoversExportedErrors(t *testing.T) {
	englishUI(t)
	samples := errorSamples()
	for _, name := range exportedErrors(t) {
		_, sampled := samples[name]
		_, excused := errorExceptions[name]
		switch {
		case sampled && excused:
			t.Errorf("%s is both in errorSamples and errorExceptions", name)
		case !sampled && !excused:
			t.Errorf("%s: add a case to describe and a sample to errorSamples, or an entry with a reason to errorExceptions", name)
		}
	}
	found := map[string]bool{}
	for _, name := range exportedErrors(t) {
		found[name] = true
	}
	for name := range samples {
		if !found[name] {
			t.Errorf("errorSamples has %s, which is no longer declared", name)
		}
	}
	for name := range errorExceptions {
		if !found[name] {
			t.Errorf("errorExceptions has %s, which is no longer declared", name)
		}
	}
	for name, err := range samples {
		got := describe(err)
		if strings.HasPrefix(got, "Unexpected error") {
			t.Errorf("%s: describe falls back to %q", name, got)
		}
		wrapped := describe(fmt.Errorf("context: %w", err))
		if wrapped != got {
			t.Errorf("%s: wrapped error gives %q, want %q", name, wrapped, got)
		}
	}
}

func TestDescribeMethodError(t *testing.T) {
	englishUI(t)
	cases := []struct {
		err  *amt.MethodError
		want string
	}{
		{&amt.MethodError{Method: "AddUserAclEntryEx", ReturnValue: 2058}, "already has an account with this name"},
		{&amt.MethodError{Method: "AddUserAclEntryEx", ReturnValue: 23}, "cannot store more accounts"},
		{&amt.MethodError{Method: "AddUserAclEntryEx", ReturnValue: 5}, "AddUserAclEntryEx (return value 5)"},
		{&amt.MethodError{Method: "CommitChanges", ReturnValue: 2058}, "CommitChanges (return value 2058)"},
	}
	for _, c := range cases {
		if got := describe(c.err); !strings.Contains(got, c.want) {
			t.Errorf("describe(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestDescribeValidationErrorHidesReason(t *testing.T) {
	englishUI(t)
	err := &amt.ValidationError{Field: "HostName", Reason: "must be a valid DNS label"}
	if got := describe(err); strings.Contains(got, "HostName") || strings.Contains(got, "DNS label") {
		t.Errorf("describe shows technical text: %q", got)
	}
}

func TestDescribeISOPathError(t *testing.T) {
	englishUI(t)
	got := describe(&mkiso.PathError{Path: "dir/file.bin", Err: mkiso.ErrNameTooLong})
	if !strings.HasPrefix(got, "dir/file.bin: A file or folder name is too long") {
		t.Errorf("got %q", got)
	}
}
