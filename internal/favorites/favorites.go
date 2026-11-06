// Package favorites persists AMT device favorites as JSON. Passwords and
// certificate fingerprints are never stored here; they live in the keychain.
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Favorite is a saved device entry.
type Favorite struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Username string `json:"username"`
	UseTLS   bool   `json:"use_tls"`
	// Layout is the keyboard layout chosen for "Type clipboard" ("de" or "us"); empty means
	// no saved choice. A plain string keeps this package independent of internal/typing.
	Layout string `json:"layout,omitempty"`
	// Quality is the KVM quality chosen for this device (QualityHigh or QualityLow); empty means
	// Default: the global setting decides. Files written before the field existed load as empty.
	Quality string `json:"kvm_quality,omitempty"`
	// AutoConnect overrides the global "connect KVM automatically" setting for this device
	// (AutoConnectOn or AutoConnectOff); empty means the global setting applies. Files written
	// before the field existed load as empty.
	AutoConnect string `json:"kvm_auto_connect,omitempty"`
}

// Values of Favorite.AutoConnect.
const (
	AutoConnectOn  = "on"
	AutoConnectOff = "off"
)

// AutoConnectChoice returns the stored override: AutoConnectOn, AutoConnectOff, or empty for
// the global setting (also for an unknown value from a hand-edited file).
func (f Favorite) AutoConnectChoice() string {
	if f.AutoConnect == AutoConnectOn || f.AutoConnect == AutoConnectOff {
		return f.AutoConnect
	}
	return ""
}

// KVM qualities as stored in Favorite.Quality. Plain strings keep this package independent of
// internal/kvm.
const (
	QualityHigh = "high"
	QualityLow  = "low"
)

// QualityChoice returns the stored quality override: QualityHigh, QualityLow, or empty for the
// global setting (also for an unknown value from a hand-edited file).
func (f Favorite) QualityChoice() string {
	if f.Quality == QualityHigh || f.Quality == QualityLow {
		return f.Quality
	}
	return ""
}

// DisplayName is the name shown in lists: Name, or Host when Name is empty.
func (f Favorite) DisplayName() string {
	if f.Name != "" {
		return f.Name
	}
	return f.Host
}

// Store reads and writes the favorites file.
type Store struct{ path string }

// NewStore returns a Store backed by path.
func NewStore(path string) *Store { return &Store{path: path} }

// DefaultPath returns <user config dir>/openlamt/favorites.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "openlamt", "favorites.json"), nil
}

// Load returns the saved favorites; a missing file yields an empty list.
func (s *Store) Load() ([]Favorite, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return []Favorite{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var list []Favorite
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if list == nil {
		list = []Favorite{}
	}
	// Hand-edited files may hold hosts the app itself would never write. Normalize like the
	// connect path (trim, lower case) so lookups, pins and keychain accounts match. Entries
	// that become equal are kept: no silent data loss.
	for i := range list {
		list[i].Host = strings.ToLower(strings.TrimSpace(list[i].Host))
	}
	return list, nil
}

// Save writes list atomically (temp file + rename) with mode 0600.
func (s *Store) Save(list []Favorite) error {
	if list == nil {
		list = []Favorite{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".favorites-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	return nil
}
