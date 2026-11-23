// A certificate fingerprint is no secret: an attacker who reads it learns nothing they could use.
// Only its integrity counts, because a changed pin would make openlamt-mcp trust another
// certificate. So the pins live in a plain file that only its owner may write, while passwords
// stay in the keychain. The desktop app never reads this file.

package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
)

// PinFileName is the file of trusted certificate fingerprints next to favorites.json; it is used
// only with certificates.pin_file.
const PinFileName = "openlamt-pins.json"

// pinFile is a keychain.Store that keeps certificate fingerprints in a JSON file of
// "user@host": "fingerprint" entries and passes passwords on to inner.
//
// Lock order: favorites.Manager calls the fingerprint methods under its own lock
// (favorites.json.lock); pinFile then takes openlamt-pins.json.lock. pinFile never calls back
// into favorites and never holds its lock across a call to inner, so the locks cannot form a cycle.
type pinFile struct {
	path  string
	inner keychain.Store
	mu    sync.Mutex // serializes the file access of this process; LockPath does it across processes
}

// pinFileError marks every failure of the pin file itself (not of the keychain).
type pinFileError struct{ err error }

func (e *pinFileError) Error() string { return e.err.Error() }
func (e *pinFileError) Unwrap() error { return e.err }

func pinErr(format string, args ...any) error {
	return &pinFileError{err: fmt.Errorf(format, args...)}
}

var _ keychain.Store = (*pinFile)(nil)

// OpenPinFile returns a keychain.Store that keeps fingerprints in the file at path and passwords
// in inner. It fails if an existing file cannot be used (mode, content).
func OpenPinFile(path string, inner keychain.Store) (keychain.Store, error) {
	p := &pinFile{path: path, inner: inner}
	if _, err := p.read(); err != nil {
		return nil, err
	}
	return p, nil
}

func pinKey(host, username string) string { return username + "@" + host }

func (p *pinFile) Set(host, username, password string) error {
	return p.inner.Set(host, username, password)
}

func (p *pinFile) Get(host, username string) (string, error) {
	return p.inner.Get(host, username)
}

func (p *pinFile) Delete(host, username string) error {
	return p.inner.Delete(host, username)
}

func (p *pinFile) GetFingerprint(host, username string) (string, error) {
	pins, err := p.read()
	if err != nil {
		return "", err
	}
	fp, ok := pins[pinKey(host, username)]
	if !ok {
		return "", keychain.ErrNotFound
	}
	return fp, nil
}

func (p *pinFile) SetFingerprint(host, username, fp string) error {
	fp, err := amt.NormalizeFingerprint(fp)
	if err != nil {
		return pinErr("not a SHA-256 fingerprint (64 hex characters); nothing was saved")
	}
	return p.modify(func(pins map[string]string) error {
		pins[pinKey(host, username)] = fp
		return nil
	})
}

func (p *pinFile) DeleteFingerprint(host, username string) error {
	return p.modify(func(pins map[string]string) error {
		key := pinKey(host, username)
		if _, ok := pins[key]; !ok {
			return keychain.ErrNotFound
		}
		delete(pins, key)
		return nil
	})
}

// modify runs a load-change-save under both locks. A change that fails writes nothing.
func (p *pinFile) modify(change func(map[string]string) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	unlock, err := favorites.LockPath(p.path)
	if err != nil {
		return &pinFileError{err: err}
	}
	defer unlock()
	pins, err := p.load()
	if err != nil {
		return err
	}
	if err := change(pins); err != nil {
		return err
	}
	return p.save(pins)
}

// read loads the pins under the locks. Like favorites.Manager.lockRead it reads without the file
// lock if that cannot be taken (a folder the user may not write); the lock protects a read from a
// rename by another process, which fails on Windows while the file is open.
func (p *pinFile) read() (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if unlock, err := favorites.LockPath(p.path); err == nil {
		defer unlock()
	}
	return p.load()
}

// load reads and checks the file. A missing or empty file has no pins. The caller holds mu.
func (p *pinFile) load() (map[string]string, error) {
	f, err := os.Open(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, pinErr("read %s: %w", p.path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, pinErr("read %s: %w", p.path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, pinErr("%s holds the trusted certificates but other users may read or change it (mode %04o): "+
			"make it readable and writable by its owner only (mode 0600)", p.path, info.Mode().Perm())
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, pinErr("read %s: %w", p.path, err)
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	var pins map[string]string
	if err := json.Unmarshal(b, &pins); err != nil {
		// The JSON error is left out on purpose: it can quote the content.
		return nil, pinErr("%s is damaged: it must hold one JSON object with \"user@host\": \"SHA-256 fingerprint\" "+
			"entries; repair or delete it, then run openlamt-mcp trust again", p.path)
	}
	if pins == nil {
		pins = map[string]string{}
	}
	for k, v := range pins {
		fp, err := amt.NormalizeFingerprint(v)
		if err != nil {
			return nil, pinErr("%s: the entry %q is not a SHA-256 fingerprint (64 hex characters)", p.path, k)
		}
		pins[k] = fp
	}
	return pins, nil
}

// save writes pins atomically (temp file + rename) with mode 0600.
func (p *pinFile) save(pins map[string]string) error {
	b, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return pinErr("write %s: %w", p.path, err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return pinErr("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".openlamt-pins-*.tmp")
	if err != nil {
		return pinErr("write %s: %w", p.path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return pinErr("write %s: %w", p.path, err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return pinErr("write %s: %w", p.path, err)
	}
	if err := tmp.Close(); err != nil {
		return pinErr("write %s: %w", p.path, err)
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return pinErr("write %s: %w", p.path, err)
	}
	return nil
}
