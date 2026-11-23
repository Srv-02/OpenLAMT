package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/session"
)

// newPassword is the password the agent sets; it must never come back.
const newPassword = "N3w-Agent-Pass!"

// deviceUsers is an ACL with the favorite's own account "operator", another user and a system
// account; the admin account is "admin".
func deviceUsers() amt.UserList {
	return amt.UserList{Admin: "admin", Users: []amt.User{
		{Handle: 1, Name: "operator", Access: amt.AccessNetwork, Realms: []amt.Realm{amt.RealmPTAdministration}, Enabled: true, EnabledKnown: true, Self: true},
		{Handle: 2, Name: "bob", Access: amt.AccessLocalAndNetwork, Realms: []amt.Realm{amt.RealmRedirection, 22}, Enabled: true, EnabledKnown: true},
		{Handle: 3, Name: "$$OsAdmin", Access: amt.AccessLocal, Realms: []amt.Realm{amt.RealmLocalSystem}, System: true},
	}}
}

// noPassword fails the test if the password shows up in the action log or the questions.
func noPassword(t *testing.T, h *harness, asked []string) {
	t.Helper()
	b, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), newPassword) {
		t.Fatalf("the action log holds the password: %s", b)
	}
	for _, q := range asked {
		if strings.Contains(q, newPassword) {
			t.Fatalf("a question holds the password: %s", q)
		}
	}
}

func mustOKNoPassword(t *testing.T, h *harness, name string, args map[string]any) map[string]any {
	t.Helper()
	text, out, isErr := h.call(name, args)
	if isErr {
		t.Fatalf("%s: %s", name, text)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(text, newPassword) || strings.Contains(string(b), newPassword) {
		t.Fatalf("%s echoes the password: %s", name, text)
	}
	return out
}

// TestListUsers: names, access, realms (a number for a reserved one), enabled state, and which
// account is the favorite's own and which the firmware's.
func TestListUsers(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("NUC", "192.0.2.10", "operator", false)
	f.Cfg.Users = deviceUsers()
	h.start()
	out := h.mustOK("list_users", dev("NUC"))
	users, _ := out["users"].([]any)
	if out["admin"] != "admin" || len(users) != 3 {
		t.Fatalf("%v", out)
	}
	op, bob, sys := users[0].(map[string]any), users[1].(map[string]any), users[2].(map[string]any)
	if op["own"] != true || op["access"] != "network" || !reflect.DeepEqual(op["realms"], []any{"PTAdministration"}) {
		t.Fatalf("operator %v", op)
	}
	if bob["access"] != "local_and_network" || !reflect.DeepEqual(bob["realms"], []any{"Redirection", "22"}) || bob["enabled"] != true {
		t.Fatalf("bob %v", bob)
	}
	if sys["system"] != true {
		t.Fatalf("system %v", sys)
	}
	if _, ok := sys["enabled"]; ok {
		t.Fatalf("unknown enabled state shown: %v", sys)
	}
}

// TestAddUserNeverEchoesPassword: the password reaches the device and nothing else; without
// TLS the question and the result warn that it travels as a digest anyone can reuse.
func TestAddUserNeverEchoesPassword(t *testing.T) {
	a := &askAll{}
	h := newHarness(t)
	f := h.addDeviceAs("NUC", "192.0.2.10", "operator", false)
	f.Cfg.Users = deviceUsers()
	h.cfg.RequireClientConfirmation = true
	h.elicit = a.handler
	h.start()
	out := mustOKNoPassword(t, h, "add_user", dev("NUC", "name", "carol", "password", newPassword, "realms", []string{"Redirection", "RemoteControl"}))
	w, _ := json.Marshal(out["warnings"])
	if !strings.Contains(string(w), "no TLS") || !strings.Contains(string(w), `sign in as \"carol\"`) {
		t.Fatalf("warnings %s", w)
	}
	if len(a.messages) != 1 || !strings.Contains(a.messages[0], "no TLS") || !strings.Contains(a.messages[0], `"carol" with network access`) {
		t.Fatalf("asked %q", a.messages)
	}
	added := f.Cfg.Users.Users[3]
	if added.Name != "carol" || !reflect.DeepEqual(added.Realms, []amt.Realm{amt.RealmRedirection, amt.RealmRemoteControl}) {
		t.Fatalf("added %+v", added)
	}
	h.mustFail("add_user", dev("NUC", "name", "dave", "password", "short", "realms", []string{"Redirection"}), "Invalid password: 8 to 32 characters")
	h.mustFail("add_user", dev("NUC", "name", "bob", "password", newPassword, "realms", []string{"Redirection"}), "exists already")
	h.mustFail("add_user", dev("NUC", "name", "erin", "password", newPassword, "realms", []string{}), "at least one")
	noPassword(t, h, a.messages)
}

// TestAddUserWithTLSHasNoWarning: a TLS favorite gets no digest warning.
func TestAddUserWithTLSHasNoWarning(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.start()
	out := mustOKNoPassword(t, h, "add_user", dev("Lab", "name", "carol", "password", newPassword, "realms", []string{"Redirection"}))
	if _, ok := out["warnings"]; ok {
		t.Fatalf("warnings %v", out["warnings"])
	}
}

// TestUpdateOwnAccountIsOrangeAndLoginFollows: the favorite's own account asks always, and the
// saved login follows the change, so the next call signs in with the new password.
func TestUpdateOwnAccountIsOrangeAndLoginFollows(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.start()
	h.mustFail("update_user", dev("Lab", "user", "Operator", "password", newPassword), "cannot ask for confirmation")

	a := &askAll{}
	h = newHarness(t)
	f = h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.elicit = a.handler
	h.start()
	out := mustOKNoPassword(t, h, "update_user", dev("Lab", "user", "operator", "password", newPassword))
	if !strings.Contains(out["result"].(string), "new login from now on") {
		t.Fatalf("%v", out)
	}
	if len(a.messages) != 1 || !strings.Contains(a.messages[0], "Warning (orange in the OpenLAMT app): OpenLAMT signs in") {
		t.Fatalf("asked %q", a.messages)
	}
	calls, pws := h.logins.recorded()
	if !reflect.DeepEqual(calls, []string{"login lab.example.com operator operator"}) || pws[0] != newPassword {
		t.Fatalf("logins %v", calls)
	}
	// The kept values: access and permissions stay.
	if u := f.Cfg.Users.Users[0]; u.Access != amt.AccessNetwork || !reflect.DeepEqual(u.Realms, []amt.Realm{amt.RealmPTAdministration}) {
		t.Fatalf("operator %+v", u)
	}
	h.mustOK("list_users", dev("Lab"))
	c := h.connectCalls()
	if last := c[len(c)-1]; last.Password != newPassword || last.Username != "operator" {
		t.Fatal("the next connection does not use the new login")
	}
	noPassword(t, h, a.messages)
}

// TestUpdateOtherUserKeepsValues: another account needs no question; fields left out keep the
// device's values, and a reserved realm that cannot be assigned again is named in a warning.
func TestUpdateOtherUserKeepsValues(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.start()
	out := mustOKNoPassword(t, h, "update_user", dev("Lab", "user", "BOB", "new_name", "robert", "password", newPassword))
	w, _ := json.Marshal(out["warnings"])
	u := f.Cfg.Users.Users[1]
	if u.Name != "robert" || u.Access != amt.AccessLocalAndNetwork || !reflect.DeepEqual(u.Realms, []amt.Realm{amt.RealmRedirection}) ||
		!strings.Contains(string(w), "reserved realms 22") {
		t.Fatalf("user %+v warnings %s", u, w)
	}
	if calls, _ := h.logins.recorded(); !reflect.DeepEqual(calls, []string{"login lab.example.com bob robert"}) {
		t.Fatalf("logins %v", calls)
	}
	h.mustFail("update_user", dev("Lab", "user", "admin", "password", newPassword), "set_admin_account")
	h.mustFail("update_user", dev("Lab", "user", "ghost", "password", newPassword), "no account \"ghost\"")
	h.mustFail("update_user", dev("Lab", "user", "$$OsAdmin", "password", newPassword), "cannot be changed")
	h.mustFail("update_user", dev("Lab", "user", "robert", "password", newPassword, "realms", []string{"Bogus"}), "realms")
}

// TestLoginNotSavedIsAWarning: the device accepted the change, so the call succeeds, but the
// result says the saved login is old now.
func TestLoginNotSavedIsAWarning(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.logins.err = &favorites.KeychainError{Err: errors.New("keychain locked")}
	h.elicit = (&askAll{}).handler
	h.start()
	out := mustOKNoPassword(t, h, "update_user", dev("Lab", "user", "operator", "password", newPassword))
	w, _ := json.Marshal(out["warnings"])
	if !strings.Contains(string(w), "the system keychain is locked") || !strings.Contains(string(w), `openlamt-mcp password \"Lab\"`) {
		t.Fatalf("warnings %s", w)
	}
	if strings.Contains(out["result"].(string), "from now on") {
		t.Errorf("the result says the login works: %v", out["result"])
	}
}

// TestDeleteUserAlwaysAsks: deleting is orange in the app; the own and the system account are
// refused by the device rules.
func TestDeleteUserAlwaysAsks(t *testing.T) {
	a := &askAll{}
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.elicit = a.handler
	h.start()
	h.mustOK("delete_user", dev("Lab", "user", "bob"))
	if len(f.Cfg.Users.Users) != 2 || len(a.messages) != 1 || !strings.Contains(a.messages[0], "This cannot be undone") {
		t.Fatalf("users %+v asked %q", f.Cfg.Users.Users, a.messages)
	}
	h.mustFail("delete_user", dev("Lab", "user", "operator"), "cannot be changed, disabled or deleted")
	h.mustFail("delete_user", dev("Lab", "user", "admin"), "set_admin_account")
}

// TestSetUserEnabled: needs no question; the own account is refused.
func TestSetUserEnabled(t *testing.T) {
	h := newHarness(t)
	f := h.addDeviceAs("Lab", "lab.example.com", "operator", true)
	f.Cfg.Users = deviceUsers()
	h.start()
	h.mustOK("set_user_enabled", dev("Lab", "user", "bob", "enabled", false))
	if f.Cfg.Users.Users[1].Enabled {
		t.Fatal("bob still enabled")
	}
	h.mustFail("set_user_enabled", dev("Lab", "user", "operator", "enabled", false), "cannot be changed, disabled or deleted")
}

// TestSetAdminAccountIsRed: needs destructive, confirm and a question; a favorite signed in as
// admin follows the new login.
func TestSetAdminAccountIsRed(t *testing.T) {
	a := &askAll{}
	h := newHarness(t)
	f := h.addDevice("NUC", "192.0.2.10", false)
	f.Cfg.Users = deviceUsers()
	h.elicit = a.handler
	h.start()
	h.mustFail("set_admin_account", dev("NUC", "password", newPassword), "Pass confirm")
	h.mustFail("set_admin_account", dev("NUC", "password", "weakpassword", "confirm", "NUC"), "Invalid password")
	if len(a.messages) != 0 {
		t.Fatalf("asked %q", a.messages)
	}
	out := mustOKNoPassword(t, h, "set_admin_account", dev("NUC", "password", newPassword, "confirm", "NUC"))
	if !strings.Contains(out["result"].(string), "new login from now on") || f.Cfg.Users.Admin != "admin" {
		t.Fatalf("%v", out)
	}
	w, _ := json.Marshal(out["warnings"])
	if len(a.messages) != 1 || !strings.Contains(a.messages[0], "Warning (red in the OpenLAMT app): Risk of lockout") ||
		!strings.Contains(a.messages[0], "no TLS") || !strings.Contains(string(w), "no TLS") {
		t.Fatalf("asked %q warnings %s", a.messages, w)
	}
	if calls, _ := h.logins.recorded(); !reflect.DeepEqual(calls, []string{"login 192.0.2.10 admin admin"}) {
		t.Fatalf("logins %v", calls)
	}
	noPassword(t, h, a.messages)
}

// TestUserLookupIsCaseInsensitiveButNotAmbiguous: an account named in another case is found,
// two that differ only in case are refused.
func TestUserLookupIsCaseInsensitiveButNotAmbiguous(t *testing.T) {
	l := deviceUsers()
	if u, err := findUser(l, "BOB"); err != nil || u.Handle != 2 {
		t.Fatalf("%+v %v", u, err)
	}
	l.Users = append(l.Users, amt.User{Handle: 4, Name: "Bob"})
	if _, err := findUser(l, "BOB"); err == nil || !strings.Contains(err.Error(), "2 accounts match") {
		t.Fatalf("%v", err)
	}
	if u, err := findUser(l, "Bob"); err != nil || u.Handle != 4 {
		t.Fatalf("exact name: %+v %v", u, err)
	}
	var _ session.LoginStore = (*fakeLogins)(nil)
}
