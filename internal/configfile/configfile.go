// Package configfile reads and writes the configuration file of "Export Config" and "Import
// Config": the favorites (name, host, user, TLS, KVM choices, optionally the pinned certificate
// fingerprint) and the global settings as readable JSON with a format version. Passwords are
// never part of it.
package configfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/typing"
)

const (
	// Format marks the file as an OpenLAMT configuration.
	Format = "openlamt-config"
	// Version is the format version this build writes and reads. A file with a higher one comes
	// from a newer OpenLAMT.
	Version = 1
	// MaxSize is the largest file Read accepts; a configuration is a few kilobytes.
	MaxSize = 4 << 20
)

// Favorite is a favorite of the file: the saved entry and, if the export included pins, the
// pinned certificate fingerprint (64 hex characters).
type Favorite struct {
	favorites.Favorite
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Settings are the global settings of the file. A nil field is not part of the file and not
// applied. The values are those of the preferences (seconds, minutes, "high"/"low", "de"/"en"
// or empty for the system, "light"/"dark" or empty for the system).
type Settings struct {
	PollIntervalSeconds *int    `json:"poll_interval_seconds,omitempty"`
	TypeLayout          *string `json:"type_layout,omitempty"`
	KVMIdleMinutes      *int    `json:"kvm_idle_minutes,omitempty"`
	KVMAutoConnect      *bool   `json:"kvm_auto_connect,omitempty"`
	KVMQuality          *string `json:"kvm_quality,omitempty"`
	Language            *string `json:"language,omitempty"`
	Appearance          *string `json:"appearance,omitempty"`
	UpdateCheck         *bool   `json:"update_check,omitempty"`
	ColorBlind          *bool   `json:"color_blind,omitempty"`
}

// File is the content of a configuration file.
type File struct {
	Format    string     `json:"format"`
	Version   int        `json:"version"`
	App       string     `json:"app,omitempty"`      // the OpenLAMT version that wrote the file
	Exported  string     `json:"exported,omitempty"` // RFC 3339, UTC
	Favorites []Favorite `json:"favorites"`
	Settings  *Settings  `json:"settings,omitempty"`
}

// ErrNotConfig means the file is not an OpenLAMT configuration (other JSON or no JSON object).
var ErrNotConfig = errors.New("not an OpenLAMT configuration file")

// Reason says what is wrong in a DamagedError.
type Reason int

const (
	ReasonSyntax     Reason = iota // broken JSON at Line
	ReasonType                     // a value of the wrong type at Line
	ReasonNoVersion                // no format version
	ReasonNoHost                   // favorite Item has no host
	ReasonBadHost                  // favorite Item has a host with a port or a slash
	ReasonNoUser                   // favorite Item has no user name
	ReasonBadPin                   // favorite Item has no valid fingerprint
	ReasonBadSetting               // the setting named Setting has a value out of range
)

// DamagedError means the file claims to be a configuration but cannot be read. Line is set for
// ReasonSyntax and ReasonType, Item (1-based) for the favorite reasons, Setting for ReasonBadSetting.
type DamagedError struct {
	Reason  Reason
	Line    int
	Item    int
	Setting string
}

func (e *DamagedError) Error() string {
	switch e.Reason {
	case ReasonSyntax, ReasonType:
		return fmt.Sprintf("damaged configuration file: line %d", e.Line)
	case ReasonNoVersion:
		return "damaged configuration file: no format version"
	case ReasonBadSetting:
		return "damaged configuration file: invalid value of " + e.Setting
	}
	return fmt.Sprintf("damaged configuration file: favorite %d", e.Item)
}

// NewerError means the file was written by a newer OpenLAMT with a format this build does not
// know. App is that OpenLAMT's version, if the file names it.
type NewerError struct{ App string }

func (e *NewerError) Error() string {
	return "configuration file of a newer OpenLAMT (" + e.App + ")"
}

// Options say what Build puts into the file.
type Options struct {
	Favorites    bool
	Fingerprints bool      // the pinned fingerprints of the favorites (needs Favorites)
	Settings     *Settings // nil leaves the settings out
	App          string    // version of this OpenLAMT
	Now          time.Time
}

// Build collects the file. The fingerprints come from the keychain, one per favorite; a
// favorite without a pin gets none, a keychain that fails aborts the export.
func Build(favs []favorites.Favorite, keys keychain.Store, o Options) (File, error) {
	f := File{Format: Format, Version: Version, App: o.App, Exported: o.Now.UTC().Format(time.RFC3339), Favorites: []Favorite{}}
	if o.Favorites {
		for _, fav := range favs {
			e := Favorite{Favorite: fav}
			if o.Fingerprints {
				fp, err := keys.GetFingerprint(fav.Host, fav.Username)
				switch {
				case err == nil:
					e.Fingerprint = fp
				case !errors.Is(err, keychain.ErrNotFound):
					return File{}, &favorites.KeychainError{Err: err}
				}
			}
			f.Favorites = append(f.Favorites, e)
		}
	}
	f.Settings = o.Settings
	return f, nil
}

// Encode renders the file as indented JSON.
func Encode(f File) ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Write saves the file at path with mode 0600, through a temporary file next to it.
func Write(path string, f File) error {
	b, err := Encode(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".openlamt-config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read reads and validates the file at path.
func Read(path string) (File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, MaxSize+1))
	if err != nil {
		return File{}, err
	}
	if len(b) > MaxSize {
		return File{}, ErrNotConfig
	}
	return Parse(b)
}

// Parse validates data and returns the file with normalized favorites (host in lower case,
// fingerprint as 64 lower-case hex characters, unknown KVM choices cleared). Nothing is
// applied. Errors: ErrNotConfig, *DamagedError, *NewerError.
func Parse(data []byte) (File, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			// A file that is no JSON at all (a text file) is not ours; broken JSON is damaged.
			if !looksLikeJSON(data) {
				return File{}, ErrNotConfig
			}
			return File{}, &DamagedError{Reason: ReasonSyntax, Line: lineOf(data, syn.Offset)}
		}
		return File{}, ErrNotConfig // valid JSON, but not an object
	}
	var format string
	if json.Unmarshal(probe["format"], &format) != nil || format != Format {
		return File{}, ErrNotConfig
	}
	var head struct {
		Version int    `json:"version"`
		App     string `json:"app"`
	}
	_ = json.Unmarshal(data, &head) // a wrong type shows below
	if head.Version > Version {
		return File{}, &NewerError{App: head.App}
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) {
			return File{}, &DamagedError{Reason: ReasonType, Line: lineOf(data, typ.Offset)}
		}
		return File{}, &DamagedError{Reason: ReasonSyntax, Line: 1}
	}
	if f.Version < 1 {
		return File{}, &DamagedError{Reason: ReasonNoVersion}
	}
	for i := range f.Favorites {
		if err := normalize(&f.Favorites[i], i+1); err != nil {
			return File{}, err
		}
	}
	return f, nil
}

func looksLikeJSON(data []byte) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

// lineOf is the 1-based line of the byte offset.
func lineOf(data []byte, offset int64) int {
	offset = min(max(offset, 0), int64(len(data)))
	return bytes.Count(data[:offset], []byte("\n")) + 1
}

// normalize checks favorite number n (1-based) and cleans it.
func normalize(f *Favorite, n int) error {
	f.Host = strings.ToLower(strings.TrimSpace(f.Host))
	f.Username = strings.TrimSpace(f.Username)
	switch {
	case f.Host == "":
		return &DamagedError{Reason: ReasonNoHost, Item: n}
	case strings.ContainsAny(f.Host, ": /\\"):
		return &DamagedError{Reason: ReasonBadHost, Item: n}
	case f.Username == "":
		return &DamagedError{Reason: ReasonNoUser, Item: n}
	}
	f.Quality, f.AutoConnect = f.QualityChoice(), f.AutoConnectChoice()
	if l, ok := typing.ParseLayout(f.Layout); ok {
		f.Layout = string(l)
	} else {
		f.Layout = ""
	}
	if f.Fingerprint != "" {
		fp, err := amt.NormalizeFingerprint(f.Fingerprint)
		if err != nil {
			return &DamagedError{Reason: ReasonBadPin, Item: n}
		}
		f.Fingerprint = fp
	}
	return nil
}

// Preview says what an import would do.
type Preview struct {
	New     []Favorite           // would be added
	Skipped []favorites.Favorite // the favorites that are already here instead: the existing one, or the earlier one of the file
	Pins    int                  // pinned fingerprints among New
}

// Plan sorts the favorites of f against the existing ones. Merge decides the same way.
func Plan(f File, existing []favorites.Favorite) Preview {
	known := map[string]favorites.Favorite{}
	for _, e := range existing {
		if _, ok := known[e.Host]; !ok {
			known[e.Host] = e
		}
	}
	var p Preview
	for _, fav := range f.Favorites {
		if have, ok := known[fav.Host]; ok {
			p.Skipped = append(p.Skipped, have)
			continue
		}
		known[fav.Host] = fav.Favorite
		p.New = append(p.New, fav)
		if fav.Fingerprint != "" {
			p.Pins++
		}
	}
	return p
}

// Import merges the favorites of f into mgr (see favorites.Manager.Merge); it returns the
// added and the skipped ones.
func Import(mgr *favorites.Manager, f File) (added, skipped []favorites.Favorite, err error) {
	items := make([]favorites.Incoming, 0, len(f.Favorites))
	for _, fav := range f.Favorites {
		items = append(items, favorites.Incoming{Favorite: fav.Favorite, Fingerprint: fav.Fingerprint})
	}
	return mgr.Merge(items)
}
