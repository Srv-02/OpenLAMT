package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

// FavoriteSource lists the saved favorites (*favorites.Manager).
type FavoriteSource interface {
	List() ([]favorites.Favorite, error)
}

// keychainError explains a keychain that cannot be read (on Linux: no Secret Service running).
func keychainError(err error) error {
	return refuse("Cannot read the system keychain (%v). On Linux a Secret Service provider such as gnome-keyring must be running.", err)
}

const noTLSWarning = "The connection to this device is not encrypted (TLS is off in the favorite): " +
	"AMT traffic, including the screen and keystrokes of later KVM sessions, can be read on the network."

// lookup finds the allowed favorite named name (its display name in the app).
func (s *server) lookup(name string) (favorites.Favorite, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return favorites.Favorite{}, refuse("device is required: give the favorite name from list_devices.")
	}
	if !s.cfg.DeviceAllowed(name) {
		return favorites.Favorite{}, refuse("Device %q is not in allowed_devices of the openlamt-mcp config.", name)
	}
	list, err := s.favs.List()
	if err != nil {
		return favorites.Favorite{}, fmt.Errorf("read the OpenLAMT favorites: %w", err)
	}
	var found []favorites.Favorite
	for _, f := range list {
		if f.DisplayName() == name {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 0:
		return favorites.Favorite{}, refuse("No favorite named %q. Call list_devices for the names; devices are added in the OpenLAMT app.", name)
	case 1:
		return found[0], nil
	}
	return favorites.Favorite{}, refuse("%d favorites are named %q. Give them distinct names in the OpenLAMT app.", len(found), name)
}

// favKey is what sessions, mounts and kept images are kept by: the device a favorite reaches
// (host and user), not the name the agent sent. A favorite renamed in the app keeps its session;
// one that now points to another host or user gets a new one, so input never goes to the device
// a question no longer shows.
func favKey(f favorites.Favorite) string {
	return strings.ToLower(strings.TrimSpace(f.Host)) + "\x00" + f.Username
}

// openDevice looks up the favorite and opens a session (see openFav). The caller closes the
// session.
func (s *server) openDevice(ctx context.Context, name string) (*session.Session, amt.Status, favorites.Favorite, []string, error) {
	fav, err := s.lookup(name)
	if err != nil {
		return nil, amt.Status{}, fav, nil, err
	}
	sess, st, warnings, err := s.openFav(ctx, fav)
	return sess, st, fav, warnings, err
}

// openFav opens a session to exactly the host and user of fav, the favorite a call resolved
// (and a question showed), with the password and the pinned fingerprint from the keychain. A
// TLS favorite without a pin is refused: the MCP server never pins a certificate (no
// TrustPrompt is passed). The caller closes the session.
func (s *server) openFav(ctx context.Context, fav favorites.Favorite) (*session.Session, amt.Status, []string, error) {
	name := fav.DisplayName()
	cfg := amt.Config{Host: fav.Host, Username: fav.Username, UseTLS: fav.UseTLS}
	var warnings []string
	if fav.UseTLS {
		fp, err := s.keys.GetFingerprint(fav.Host, fav.Username)
		switch {
		case errors.Is(err, keychain.ErrNotFound) || err == nil && strings.TrimSpace(fp) == "":
			return nil, amt.Status{}, nil, refuse("The TLS certificate of %q is not pinned yet. "+
				"Connect once with the OpenLAMT app to pin the certificate; the MCP server never trusts an unpinned certificate.", name)
		case err != nil:
			return nil, amt.Status{}, nil, keychainError(err)
		}
		cfg.Fingerprint = fp
	} else {
		warnings = append(warnings, noTLSWarning)
	}
	pw, err := s.keys.Get(fav.Host, fav.Username)
	switch {
	case errors.Is(err, keychain.ErrNotFound) || err == nil && pw == "":
		return nil, amt.Status{}, nil, refuse("No password is stored for %q. Save the password of the favorite in the OpenLAMT app; "+
			"the MCP server takes passwords only from the keychain.", name)
	case err != nil:
		return nil, amt.Status{}, nil, keychainError(err)
	}
	cfg.Password = pw
	cfg.NeverEnable = s.cfg.ReadOnly // read_only never switches a redirection feature on
	sess, st, err := session.Open(ctx, cfg, nil, s.connect)
	if err != nil {
		return nil, amt.Status{}, nil, err
	}
	return sess, st, warnings, nil
}
