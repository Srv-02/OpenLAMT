package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

// The users tools: the AMT accounts of a device, as on the app's Users page. Passwords of new
// or changed accounts come as tool arguments, the one secret that passes through MCP, because
// the agent chooses it. They never reach the action log, a result, a question or a log line.

const passwordNote = " The password is part of the conversation with the AI agent and of the MCP client's records."

func (s *server) registerUsers(srv *mcp.Server) {
	closed := boolPtr(false)
	if s.cfg.Groups[GroupRead] && s.cfg.Groups[GroupUsers] {
		addTool(s, srv, &mcp.Tool{Name: "list_users", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed},
			Description: "List the AMT accounts of a device: the admin account and every user with access, permissions (realms) and " +
				"enabled state. own marks the account this favorite signs in with, system the firmware's own accounts."},
			s.listUsers)
	}
	if !s.cfg.Enabled(GroupUsers) {
		return
	}
	write := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: closed}
	risky := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed}
	addTool(s, srv, &mcp.Tool{Name: "add_user", Annotations: write, InputSchema: userSchema[addUserInput](),
		Description: "Add a digest account to a device. Password: 8 to 32 printable ASCII characters with an upper and a lower case letter, " +
			"a digit and a special character." + passwordNote},
		s.addUser)
	addTool(s, srv, &mcp.Tool{Name: "update_user", Annotations: risky, InputSchema: userSchema[updateUserInput](),
		Description: "Change name, password, access or permissions of an account. The password is always needed, because AMT stores a " +
			"digest of name and password. Leave out what stays. For the account this favorite signs in with, the OpenLAMT app asks with an " +
			"orange warning, so the person in the MCP client is always asked; the saved login follows the change. Not for the admin " +
			"account (set_admin_account)." + passwordNote},
		s.updateUser)
	addTool(s, srv, &mcp.Tool{Name: "set_user_enabled", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true, OpenWorldHint: closed},
		Description: "Enable or disable an account. The account this favorite signs in with and system accounts cannot be disabled."},
		s.setUserEnabled)
	addTool(s, srv, &mcp.Tool{Name: "delete_user", Annotations: risky,
		Description: "Delete an account; it can no longer sign in. The OpenLAMT app asks with an orange warning, so the person in the MCP " +
			"client is always asked. The account this favorite signs in with and system accounts cannot be deleted."},
		s.deleteUser)
	if s.cfg.Enabled(GroupDestructive) {
		addTool(s, srv, &mcp.Tool{Name: "set_admin_account", Annotations: risky,
			Description: "Change the name or password of the AMT admin account. A lost admin password can only be reset in the BIOS (MEBx) " +
				"at the device: a red warning in the OpenLAMT app. The person in the MCP client is always asked, and confirm must hold the " +
				"device name. The saved login follows the change." + passwordNote},
			s.setAdminAccount)
	}
}

var accessNames = map[amt.AccessPermission]string{amt.AccessNetwork: "network", amt.AccessLocal: "local", amt.AccessLocalAndNetwork: "local_and_network"}

func realmNames() []any {
	var out []any
	for _, r := range amt.AssignableRealms {
		out = append(out, r.String())
	}
	return out
}

// userSchema adds the enums of access and realms.
func userSchema[T any]() *jsonschema.Schema {
	sc, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	sc.Properties["access"].Enum = []any{"network", "local", "local_and_network"}
	sc.Properties["realms"].Items.Enum = realmNames()
	return sc
}

func parseAccess(s string) (amt.AccessPermission, error) {
	for a, name := range accessNames {
		if name == strings.TrimSpace(s) {
			return a, nil
		}
	}
	return 0, refuse("access must be network, local or local_and_network.")
}

func parseRealms(names []string) ([]amt.Realm, error) {
	if len(names) == 0 {
		return nil, refuse("realms needs at least one permission, for example [\"PTAdministration\"] for an administrator.")
	}
	var out []amt.Realm
	for _, n := range names {
		i := slices.IndexFunc(amt.AssignableRealms, func(r amt.Realm) bool { return strings.EqualFold(r.String(), strings.TrimSpace(n)) })
		if i < 0 {
			return nil, refuse("Unknown realm %s; use the names of list_users, such as PTAdministration, Redirection or HardwareAsset.", quoteShort(n))
		}
		out = append(out, amt.AssignableRealms[i])
	}
	return out, nil
}

func realmText(rs []amt.Realm) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if n := r.String(); n != "" {
			out = append(out, n)
		} else {
			out = append(out, strconv.Itoa(int(r)))
		}
	}
	return out
}

// --- list_users

type userInfo struct {
	Handle      int      `json:"handle"`
	Name        string   `json:"name,omitempty"`
	KerberosSID string   `json:"kerberos_sid,omitempty"`
	Access      string   `json:"access" jsonschema:"network, local or local_and_network"`
	Realms      []string `json:"realms" jsonschema:"permission realms; a number for one AMT reserves"`
	Enabled     *bool    `json:"enabled,omitempty" jsonschema:"missing when the device does not report it"`
	System      bool     `json:"system" jsonschema:"an account of the firmware; read-only here"`
	Own         bool     `json:"own" jsonschema:"the account this favorite signs in with"`
}

type usersOutput struct {
	Device   string     `json:"device"`
	Admin    string     `json:"admin" jsonschema:"the AMT admin account; change it with set_admin_account"`
	Users    []userInfo `json:"users"`
	Warnings []string   `json:"warnings,omitempty"`
}

func (s *server) listUsers(ctx context.Context, _ *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, usersOutput, error) {
	rec.device = in.Device
	sess, _, _, warnings, err := s.openDevice(ctx, in.Device)
	if err != nil {
		return nil, usersOutput{}, err
	}
	defer sess.Close()
	l, err := sess.Users()
	if err != nil {
		return nil, usersOutput{}, err
	}
	out := usersOutput{Device: in.Device, Admin: l.Admin, Users: []userInfo{}, Warnings: warnings}
	for _, u := range l.Users {
		ui := userInfo{Handle: u.Handle, Name: u.Name, KerberosSID: u.KerberosSID, Access: accessNames[u.Access], Realms: realmText(u.Realms),
			System: u.System, Own: u.Self}
		if ui.Access == "" {
			ui.Access = strconv.Itoa(int(u.Access))
		}
		if u.EnabledKnown {
			ui.Enabled = boolPtr(u.Enabled)
		}
		out.Users = append(out.Users, ui)
	}
	return nil, out, nil
}

// findUser picks the account named user (its name, else without regard to case, else its
// Kerberos SID). The admin account is not in the list: it gets its own refusal.
func findUser(l amt.UserList, user string) (amt.User, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return amt.User{}, refuse("user is required: give the account name from list_users.")
	}
	for _, match := range []func(amt.User) bool{
		func(u amt.User) bool { return u.Name == user },
		func(u amt.User) bool { return u.Name != "" && strings.EqualFold(u.Name, user) },
		func(u amt.User) bool { return u.KerberosSID != "" && strings.EqualFold(u.KerberosSID, user) },
	} {
		var found []amt.User
		for _, u := range l.Users {
			if match(u) {
				found = append(found, u)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		}
		return amt.User{}, refuse("%d accounts match %s without regard to case: give the name exactly as list_users shows it.", len(found), quoteShort(user))
	}
	if strings.EqualFold(l.Admin, user) {
		return amt.User{}, refuse("%s is the AMT admin account: change it with set_admin_account. It cannot be disabled or deleted.", quoteShort(user))
	}
	return amt.User{}, refuse("The device has no account %s. Call list_users for the names.", quoteShort(user))
}

// userWrite opens the device and runs write with the session's host.
func (s *server) userWrite(ctx context.Context, fav favorites.Favorite, write func(sess *session.Session, host string) (string, []string, error)) (writeOutput, error) {
	return s.settingsWrite(ctx, fav, write)
}

// loginSaved turns a *session.NotSavedError of an accepted account change into a warning; any
// other error stays an error.
func loginSaved(err error, user string) ([]string, error) {
	var ns *session.NotSavedError
	if errors.As(err, &ns) {
		return []string{fmt.Sprintf("The device accepted the change, but saving the new login of %s in the keychain or the favorites "+
			"failed: %s. Update the password of the favorite in the OpenLAMT app, or the next call to the device fails.", user, explain(ns.Err))}, nil
	}
	return nil, err
}

// tlsNotes is the TLS warning for a password on a favorite without TLS, nil with TLS.
func tlsNotes(fav favorites.Favorite, user string) []string {
	if fav.UseTLS {
		return nil
	}
	return []string{passwordNoTLS(shownUser(user))}
}

// --- add_user

type addUserInput struct {
	Device   string   `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Name     string   `json:"name" jsonschema:"1 to 16 printable ASCII characters without spaces and without : , \" < > &"`
	Password string   `json:"password" jsonschema:"8 to 32 printable ASCII characters with upper and lower case letters, a digit and a special character"`
	Access   string   `json:"access,omitempty" jsonschema:"network (default), local or local_and_network"`
	Realms   []string `json:"realms" jsonschema:"permission realms, e.g. [\"PTAdministration\"] for an administrator or [\"Redirection\", \"RemoteControl\"]"`
}

func (s *server) addUser(ctx context.Context, req *mcp.CallToolRequest, in addUserInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	spec := amt.UserSpec{Name: strings.TrimSpace(in.Name), Password: in.Password, Access: amt.AccessNetwork}
	var err error
	if strings.TrimSpace(in.Access) != "" {
		if spec.Access, err = parseAccess(in.Access); err != nil {
			return nil, writeOutput{}, err
		}
	}
	if spec.Realms, err = parseRealms(in.Realms); err != nil {
		return nil, writeOutput{}, err
	}
	if err := spec.Validate(); err != nil {
		return nil, writeOutput{}, err
	}
	what := fmt.Sprintf("add the account %q with %s access and the permissions %s", spec.Name, accessNames[spec.Access],
		strings.Join(realmText(spec.Realms), ", "))
	rec.action = "add " + spec.Name
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "add_user", in: in, what: what, notes: tlsNotes(fav, spec.Name)}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.userWrite(ctx, fav, func(sess *session.Session, _ string) (string, []string, error) {
		h, err := sess.AddUser(spec)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("The device added the account %q (handle %d).", spec.Name, h), tlsNotes(fav, spec.Name), nil
	})
	return nil, out, err
}

// --- update_user

type updateUserInput struct {
	Device   string   `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	User     string   `json:"user" jsonschema:"current name of the account, as in list_users"`
	NewName  string   `json:"new_name,omitempty" jsonschema:"new name; leave out to keep it"`
	Password string   `json:"password" jsonschema:"the password the account has after the change (always needed; the old one, to keep it)"`
	Access   string   `json:"access,omitempty" jsonschema:"network, local or local_and_network; leave out to keep it"`
	Realms   []string `json:"realms,omitempty" jsonschema:"the complete new list of permission realms; leave out to keep them"`
}

func (s *server) updateUser(ctx context.Context, req *mcp.CallToolRequest, in updateUserInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	user := strings.TrimSpace(in.User)
	if user == "" {
		return nil, writeOutput{}, refuse("user is required: give the account name from list_users.")
	}
	newName := strings.TrimSpace(in.NewName)
	if newName != "" {
		if err := amt.ValidateUserName(newName); err != nil {
			return nil, writeOutput{}, err
		}
	}
	if err := amt.ValidatePassword(in.Password); err != nil {
		return nil, writeOutput{}, err
	}
	var access *amt.AccessPermission
	if strings.TrimSpace(in.Access) != "" {
		a, err := parseAccess(in.Access)
		if err != nil {
			return nil, writeOutput{}, err
		}
		access = &a
	}
	var realms []amt.Realm
	if in.Realms != nil {
		var err error
		if realms, err = parseRealms(in.Realms); err != nil {
			return nil, writeOutput{}, err
		}
	}
	parts := []string{"set a new password"}
	if newName != "" && newName != user {
		parts = append(parts, fmt.Sprintf("rename it to %q", newName))
	}
	if access != nil {
		parts = append(parts, "give it "+accessNames[*access]+" access")
	}
	if realms != nil {
		parts = append(parts, "set its permissions to "+strings.Join(realmText(realms), ", "))
	}
	what := fmt.Sprintf("change the account %s: %s", shownUser(user), strings.Join(parts, ", "))
	rec.action = "update " + shownUser(user)
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	// The account of the favorite, as amt decides it (User.Self): the same name, any case.
	own := strings.EqualFold(user, fav.Username)
	shown := newName
	if shown == "" {
		shown = user
	}
	ch := change{tool: "update_user", in: in, what: what, notes: tlsNotes(fav, shown)}
	if own {
		ch.level, ch.warning = amt.LevelOrange, riskWarnings[amt.OpOwnAccount]+" OpenLAMT saves the new login in the keychain and the favorite."
	}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.userWrite(ctx, fav, func(sess *session.Session, host string) (string, []string, error) {
		l, err := sess.Users()
		if err != nil {
			return "", nil, err
		}
		cur, err := findUser(l, user)
		if err != nil {
			return "", nil, err
		}
		if cur.System {
			return "", nil, fmt.Errorf("update user: %w", amt.ErrProtectedAccount)
		}
		if cur.Self != own {
			// The question named another kind of account than the device reports: ask again.
			return "", nil, refuse("The device reports %s differently than expected (own account: %v). Nothing was written; call the tool again.",
				quoteShort(user), cur.Self)
		}
		spec := amt.UserSpec{Name: cur.Name, Password: in.Password, Access: cur.Access, Realms: realms}
		if newName != "" {
			spec.Name = newName
		}
		if access != nil {
			spec.Access = *access
		}
		var dropped []amt.Realm
		if realms == nil {
			// Keep the permissions; AMT may report reserved realms that cannot be assigned again.
			for _, r := range cur.Realms {
				if slices.Contains(amt.AssignableRealms, r) {
					spec.Realms = append(spec.Realms, r)
				} else {
					dropped = append(dropped, r)
				}
			}
		}
		var conf amt.Confirmation
		if own {
			conf = confirmation(amt.OpOwnAccount, host)
		}
		warnings, err := loginSaved(sess.UpdateUser(cur.Handle, spec, conf, s.logins), spec.Name)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, tlsNotes(fav, spec.Name)...)
		if len(dropped) > 0 {
			warnings = append(warnings, "The account lost the reserved realms "+strings.Join(realmText(dropped), ", ")+
				", which cannot be assigned again.")
		}
		result := fmt.Sprintf("The device changed the account %q.", spec.Name)
		if own {
			result += " OpenLAMT signs in to this device with the new login from now on."
		}
		return result, warnings, nil
	})
	return nil, out, err
}

// --- set_user_enabled and delete_user

type userInput struct {
	Device string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	User   string `json:"user" jsonschema:"name of the account (or Kerberos SID), as in list_users"`
}

type userEnabledInput struct {
	Device  string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	User    string `json:"user" jsonschema:"name of the account (or Kerberos SID), as in list_users"`
	Enabled bool   `json:"enabled" jsonschema:"true enables the account, false disables it"`
}

// shownUser is the account name for a question and the action log: checked like a user name,
// so it is short and printable; anything else is quoted short.
func shownUser(user string) string {
	user = strings.TrimSpace(user)
	if amt.ValidateUserName(user) == nil {
		return strconv.Quote(user)
	}
	return quoteShort(user)
}

func (s *server) setUserEnabled(ctx context.Context, req *mcp.CallToolRequest, in userEnabledInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	if strings.TrimSpace(in.User) == "" {
		return nil, writeOutput{}, refuse("user is required: give the account name from list_users.")
	}
	verb := map[bool]string{true: "enable", false: "disable"}[in.Enabled]
	rec.action = verb + " " + shownUser(in.User)
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "set_user_enabled", in: in, what: verb + " the account " + shownUser(in.User)}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.userWrite(ctx, fav, func(sess *session.Session, _ string) (string, []string, error) {
		l, err := sess.Users()
		if err != nil {
			return "", nil, err
		}
		u, err := findUser(l, in.User)
		if err != nil {
			return "", nil, err
		}
		if err := sess.SetUserEnabled(u.Handle, in.Enabled); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("The account %s is %sd.", shownUser(in.User), verb), nil, nil
	})
	return nil, out, err
}

func (s *server) deleteUser(ctx context.Context, req *mcp.CallToolRequest, in userInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	if strings.TrimSpace(in.User) == "" {
		return nil, writeOutput{}, refuse("user is required: give the account name from list_users.")
	}
	rec.action = "delete " + shownUser(in.User)
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	ch := change{tool: "delete_user", in: in, level: amt.LevelOrange, what: "delete the account " + shownUser(in.User), warning: deleteUserWarning}
	if res, err := s.confirmChange(req, ch, fav, ""); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.userWrite(ctx, fav, func(sess *session.Session, _ string) (string, []string, error) {
		l, err := sess.Users()
		if err != nil {
			return "", nil, err
		}
		u, err := findUser(l, in.User)
		if err != nil {
			return "", nil, err
		}
		if err := sess.RemoveUser(u.Handle); err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("The device deleted the account %s.", shownUser(in.User)), nil, nil
	})
	return nil, out, err
}

// --- set_admin_account

type adminInput struct {
	Device   string `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	NewName  string `json:"new_name,omitempty" jsonschema:"new name of the admin account; leave out to keep it"`
	Password string `json:"password" jsonschema:"the new password of the admin account (8 to 32 printable ASCII characters with upper and lower case letters, a digit and a special character)"`
	Confirm  string `json:"confirm,omitempty" jsonschema:"required: the device name again, to show that this device is meant: a lost admin password locks OpenLAMT out"`
}

func (s *server) setAdminAccount(ctx context.Context, req *mcp.CallToolRequest, in adminInput, rec *record) (*mcp.CallToolResult, writeOutput, error) {
	rec.device = in.Device
	c := amt.AccountChange{Name: strings.TrimSpace(in.NewName), Password: in.Password}
	if err := c.Validate(); err != nil {
		return nil, writeOutput{}, err
	}
	what := "set a new password for the AMT admin account"
	if c.Name != "" {
		what = fmt.Sprintf("rename the AMT admin account to %q and set a new password", c.Name)
	}
	rec.action = "admin account"
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, writeOutput{}, err
	}
	shown := "the admin account"
	if c.Name != "" {
		shown = strconv.Quote(c.Name)
	}
	var notes []string
	if !fav.UseTLS {
		notes = []string{passwordNoTLS(shown)}
	}
	ch := change{tool: "set_admin_account", in: in, level: amt.LevelRed, what: what, warning: riskWarnings[amt.OpAdminAccount], notes: notes}
	if res, err := s.confirmChange(req, ch, fav, in.Confirm); res != nil || err != nil {
		return res, writeOutput{}, err
	}
	out, err := s.userWrite(ctx, fav, func(sess *session.Session, host string) (string, []string, error) {
		warnings, err := loginSaved(sess.SetAdminAccount(c, confirmation(amt.OpAdminAccount, host), s.logins), shown)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, notes...)
		result := "The device changed the admin account."
		if sess.Config().Password == c.Password { // the session follows its own login (session.loginChanged)
			result += " OpenLAMT signs in to this device with the new login from now on."
		}
		return result, warnings, nil
	})
	return nil, out, err
}
