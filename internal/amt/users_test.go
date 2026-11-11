package amt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeACL is one user ACL entry of userWSMan.
type fakeACL struct {
	name    string // digest user name
	sid     string // base64 Kerberos SID
	access  int
	realms  []int
	enabled bool
}

// userWSMan emulates AMT_AuthorizationService for a fixed set of entries and records requests.
// rv overrides the ReturnValue of a method; pageSize limits EnumerateUserAclEntries.
type userWSMan struct {
	cfgWSMan
	admin    string
	entries  map[int]*fakeACL
	rv       map[string]int
	pageSize int
	nextH    int
}

var handleRE = regexp.MustCompile(`<h:Handle>(\d+)</h:Handle>`)
var startRE = regexp.MustCompile(`<h:StartIndex>(\d+)</h:StartIndex>`)

func authEnvelope(method, inner string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuthorizationService"><a:Header/><a:Body><h:` +
		method + `_OUTPUT>` + inner + `</h:` + method + `_OUTPUT></a:Body></a:Envelope>`
}

func (f *userWSMan) Post(msg string) ([]byte, error) {
	route := routeKey(msg)
	method, class, _ := strings.Cut(route, "|")
	if class != classAuth {
		return f.cfgWSMan.Post(msg)
	}
	f.requests = append(f.requests, msg)
	h := -1
	if m := handleRE.FindStringSubmatch(msg); m != nil {
		h, _ = strconv.Atoi(m[1])
	}
	rv := f.rv[method]
	e := f.entries[h]
	if h >= 0 && e == nil && rv == 0 {
		rv = 2053 // invalid handle
	}
	inner := ""
	if rv == 0 {
		switch method {
		case "GetAdminAclEntry":
			inner = "<h:Username>" + f.admin + "</h:Username>"
		case "EnumerateUserAclEntries":
			start := 1
			if m := startRE.FindStringSubmatch(msg); m != nil {
				start, _ = strconv.Atoi(m[1])
			}
			var hs []int
			for k := range f.entries {
				hs = append(hs, k)
			}
			slices.Sort(hs)
			inner = fmt.Sprintf("<h:TotalCount>%d</h:TotalCount>", len(hs))
			page := hs[min(start-1, len(hs)):]
			if f.pageSize > 0 && len(page) > f.pageSize {
				page = page[:f.pageSize]
			}
			inner += fmt.Sprintf("<h:HandlesCount>%d</h:HandlesCount>", len(page))
			for _, x := range page {
				inner += fmt.Sprintf("<h:Handles>%d</h:Handles>", x)
			}
		case "GetUserAclEntryEx":
			if e.name != "" {
				inner = "<h:DigestUsername>" + e.name + "</h:DigestUsername>"
			} else {
				inner = "<h:KerberosUserSid>" + e.sid + "</h:KerberosUserSid>"
			}
			inner += fmt.Sprintf("<h:AccessPermission>%d</h:AccessPermission>", e.access)
			for _, r := range e.realms {
				inner += fmt.Sprintf("<h:Realms>%d</h:Realms>", r)
			}
		case "GetAclEnabledState":
			inner = fmt.Sprintf("<h:Enabled>%v</h:Enabled>", e.enabled)
		case "AddUserAclEntryEx":
			inner = fmt.Sprintf("<h:Handle>%d</h:Handle>", f.nextH)
		}
	}
	return []byte(authEnvelope(method, inner+fmt.Sprintf("<h:ReturnValue>%d</h:ReturnValue>", rv))), nil
}

func userDevice() (*Device, *userWSMan) {
	f := &userWSMan{
		cfgWSMan: cfgWSMan{seq: settingsSeq()},
		admin:    "admin",
		entries: map[int]*fakeACL{
			1: {name: "$$OsAdmin", access: 0, realms: []int{4, 6, 13, 16, 17, 19, 24}, enabled: true},
			2: {name: "user1", access: 2, realms: []int{2, 3}, enabled: true},
			3: {name: "operator", access: 1, realms: []int{5}, enabled: false},
			4: {sid: base64.StdEncoding.EncodeToString([]byte("\x01\x05\x00\x00\x00\x00\x00\x05\x15\x00\x00\x00\x01\x00\x00\x00\x02\x00\x00\x00\x03\x00\x00\x00\xe9\x03\x00\x00")), access: 1, realms: []int{2}, enabled: true},
		},
		rv:    map[string]int{},
		nextH: 7,
	}
	d := newDevice(f, "")
	d.cfg.Username = "admin"
	d.cfg.Host = testHost
	return d, f
}

func TestUsersList(t *testing.T) {
	d, f := userDevice()
	f.pageSize = 3 // forces a second EnumerateUserAclEntries page
	got, err := d.Users()
	if err != nil {
		t.Fatal(err)
	}
	want := UserList{Admin: "admin", Users: []User{
		{Handle: 1, Name: "$$OsAdmin", Access: AccessLocal, Realms: []Realm{4, 6, 13, 16, 17, 19, 24}, Enabled: true, EnabledKnown: true, System: true},
		{Handle: 2, Name: "user1", Access: AccessLocalAndNetwork, Realms: []Realm{RealmRedirection, RealmPTAdministration}, Enabled: true, EnabledKnown: true},
		{Handle: 3, Name: "operator", Access: AccessNetwork, Realms: []Realm{RealmRemoteControl}, EnabledKnown: true},
		{Handle: 4, KerberosSID: "S-1-5-21-1-2-3-1001", Access: AccessNetwork, Realms: []Realm{RealmRedirection}, Enabled: true, EnabledKnown: true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Users:\n got %+v\nwant %+v", got, want)
	}
	if starts := len(f.requestsFor("EnumerateUserAclEntries|AMT_AuthorizationService")); starts != 2 {
		t.Errorf("%d enumerate pages, want 2", starts)
	}
	assertContains(t, f.requestsFor("EnumerateUserAclEntries|AMT_AuthorizationService")[1], "<h:StartIndex>4</h:StartIndex>")
	assertNoWrites(t, &f.cfgWSMan)
}

func TestUsersMarksSelf(t *testing.T) {
	d, _ := userDevice()
	d.cfg.Username = "USER1"
	got, err := d.Users()
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range got.Users {
		if u.Self != (u.Handle == 2) {
			t.Errorf("handle %d: Self = %v", u.Handle, u.Self)
		}
	}
}

func TestUsersErrors(t *testing.T) {
	d, f := userDevice()
	f.rv["GetAclEnabledState"] = 1
	got, err := d.Users()
	if err != nil || got.Users[1].EnabledKnown {
		t.Errorf("unknown enabled state: %+v, %v", got.Users, err)
	}

	d, f = userDevice()
	f.rv["EnumerateUserAclEntries"] = 1
	var me *MethodError
	if _, err := d.Users(); !errors.As(err, &me) || me.Method != "EnumerateUserAclEntries" {
		t.Errorf("err = %v", err)
	}

	d, f = userDevice()
	f.rv["GetAdminAclEntry"] = 16
	if _, err := d.Users(); !errors.As(err, &me) || me.ReturnValue != 16 {
		t.Errorf("err = %v", err)
	}
}

func TestUserSpecValidate(t *testing.T) {
	if err := (UserSpec{Name: strings.Repeat("a", 16), Password: "Secret-Pass1", Realms: []Realm{2}}).Validate(); err != nil {
		t.Errorf("16 characters are valid: %v", err)
	}
	ok := UserSpec{Name: "user9", Password: "Secret-Pass1", Access: AccessNetwork, Realms: []Realm{RealmRedirection}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*UserSpec){
		"empty name":         func(u *UserSpec) { u.Name = "" },
		"name of 17":         func(u *UserSpec) { u.Name = strings.Repeat("a", 17) },
		"system prefix":      func(u *UserSpec) { u.Name = "$$x" },
		"colon in name":      func(u *UserSpec) { u.Name = "a:b" },
		"space in name":      func(u *UserSpec) { u.Name = "a b" },
		"non-ASCII name":     func(u *UserSpec) { u.Name = "jürgen" },
		"short password":     func(u *UserSpec) { u.Password = "Ab1-" },
		"long password":      func(u *UserSpec) { u.Password = "Ab1-" + strings.Repeat("x", 29) },
		"no upper":           func(u *UserSpec) { u.Password = "secret-pass1" },
		"no lower":           func(u *UserSpec) { u.Password = "SECRET-PASS1" },
		"no digit":           func(u *UserSpec) { u.Password = "Secret-Pass" },
		"no special":         func(u *UserSpec) { u.Password = "SecretPass1" },
		"only colon special": func(u *UserSpec) { u.Password = "Secret:Pass1" },
		"quote in password":  func(u *UserSpec) { u.Password = `Secret"Pass1` },
		"no realm":           func(u *UserSpec) { u.Realms = nil },
		"reserved realm":     func(u *UserSpec) { u.Realms = []Realm{1} },
		"unknown access":     func(u *UserSpec) { u.Access = 3 },
	} {
		u := ok
		mod(&u)
		if err := u.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAddUser(t *testing.T) {
	d, f := userDevice()
	h, err := d.AddUser(UserSpec{Name: "user1", Password: "Secret-Pass1", Access: AccessLocalAndNetwork,
		Realms: []Realm{RealmRemoteControl, RealmRedirection, RealmRedirection}})
	if err != nil || h != 7 {
		t.Fatalf("AddUser = %d, %v", h, err)
	}
	body := onePut(t, &f.cfgWSMan, "AddUserAclEntryEx|AMT_AuthorizationService")
	assertContains(t, body,
		"<h:DigestUsername>user1</h:DigestUsername>",
		"<h:DigestPassword>lBV7M7ZmbOIBKZtnmYMf0Q==</h:DigestPassword>",
		"<h:AccessPermission>2</h:AccessPermission>",
		"<h:Realms>2</h:Realms><h:Realms>5</h:Realms></h:AddUserAclEntryEx_INPUT>")
	assertLacks(t, body, "Secret-Pass1", "KerberosUserSid")

	f.rv["AddUserAclEntryEx"] = 2058
	var me *MethodError
	if _, err := d.AddUser(UserSpec{Name: "user1", Password: "Secret-Pass1", Realms: []Realm{2}}); !errors.As(err, &me) || me.ReturnValue != 2058 {
		t.Errorf("duplicate: %v", err)
	}

	d, f = userDevice()
	if _, err := d.AddUser(UserSpec{Name: "x", Password: "weak", Realms: []Realm{2}}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("weak password: %v", err)
	}
	if len(f.requests) != 0 {
		t.Errorf("invalid input reached the device: %d requests", len(f.requests))
	}
}

func TestUpdateUser(t *testing.T) {
	d, f := userDevice()
	if err := d.UpdateUser(3, UserSpec{Name: "user2", Password: "Other-Pass2", Access: AccessNetwork, Realms: []Realm{RealmRemoteControl}}); err != nil {
		t.Fatal(err)
	}
	body := onePut(t, &f.cfgWSMan, "UpdateUserAclEntryEx|AMT_AuthorizationService")
	assertContains(t, body,
		"<a:Action>http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuthorizationService/UpdateUserAclEntryEx</a:Action>",
		`<h:UpdateUserAclEntryEx_INPUT xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_AuthorizationService">`,
		"<h:Handle>3</h:Handle><h:DigestUsername>user2</h:DigestUsername><h:DigestPassword>cN+bJDwH7WYFtQZA4HO4MQ==</h:DigestPassword><h:AccessPermission>1</h:AccessPermission><h:Realms>5</h:Realms>")
	assertLacks(t, body, "Other-Pass2")

	f.rv["UpdateUserAclEntryEx"] = 2054
	var me *MethodError
	if err := d.UpdateUser(3, UserSpec{Name: "user2", Password: "Other-Pass2", Realms: []Realm{5}}); !errors.As(err, &me) || me.ReturnValue != 2054 {
		t.Errorf("err = %v", err)
	}
}

func TestUserWritesProtectAccounts(t *testing.T) {
	spec := UserSpec{Name: "user9", Password: "Secret-Pass1", Realms: []Realm{2}}
	ops := map[string]func(d *Device, h int) error{
		"update":  func(d *Device, h int) error { return d.UpdateUser(h, spec) },
		"remove":  func(d *Device, h int) error { return d.RemoveUser(h) },
		"disable": func(d *Device, h int) error { return d.SetUserEnabled(h, false) },
	}
	for name, op := range ops {
		t.Run(name+" system account", func(t *testing.T) {
			d, f := userDevice()
			if err := op(d, 1); !errors.Is(err, ErrProtectedAccount) {
				t.Errorf("err = %v", err)
			}
			assertNoWrites(t, &f.cfgWSMan)
		})
		t.Run(name+" own account", func(t *testing.T) {
			d, f := userDevice()
			d.cfg.Username = "user1"
			if err := op(d, 2); !errors.Is(err, ErrProtectedAccount) {
				t.Errorf("err = %v", err)
			}
			assertNoWrites(t, &f.cfgWSMan)
		})
		t.Run(name+" unknown handle", func(t *testing.T) {
			d, f := userDevice()
			var me *MethodError
			if err := op(d, 99); !errors.As(err, &me) || me.ReturnValue != 2053 {
				t.Errorf("err = %v", err)
			}
			assertNoWrites(t, &f.cfgWSMan)
		})
	}
	d, _ := userDevice()
	if err := d.UpdateUser(4, spec); !errors.Is(err, ErrNotModifiable) {
		t.Errorf("update of a Kerberos account: %v", err)
	}
}

func TestRemoveUser(t *testing.T) {
	d, f := userDevice()
	if err := d.RemoveUser(3); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, &f.cfgWSMan, "RemoveUserAclEntry|AMT_AuthorizationService"), "<h:Handle>3</h:Handle>")
	f.rv["RemoveUserAclEntry"] = 1
	var me *MethodError
	if err := d.RemoveUser(3); !errors.As(err, &me) {
		t.Errorf("err = %v", err)
	}
}

func TestSetUserEnabled(t *testing.T) {
	d, f := userDevice()
	if err := d.SetUserEnabled(3, true); err != nil {
		t.Fatal(err)
	}
	assertContains(t, onePut(t, &f.cfgWSMan, "SetAclEnabledState|AMT_AuthorizationService"), "<h:Handle>3</h:Handle><h:Enabled>true</h:Enabled>")

	d, f = userDevice()
	if err := d.SetUserEnabled(2, true); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, &f.cfgWSMan)
}

func TestRealmNames(t *testing.T) {
	if RealmAuditLog.String() != "AuditLog" || Realm(99).String() != "" {
		t.Errorf("String: %q %q", RealmAuditLog.String(), Realm(99).String())
	}
	for _, r := range AssignableRealms {
		if r.String() == "" {
			t.Errorf("realm %d has no name", r)
		}
	}
	if len(AssignableRealms) != 21 {
		t.Errorf("%d assignable realms", len(AssignableRealms))
	}
}

// faultWSMan answers GetAclEnabledState with a SOAP fault, everything else like userWSMan.
type faultWSMan struct {
	*userWSMan
	fault error
}

func (f *faultWSMan) Post(msg string) ([]byte, error) {
	if strings.HasPrefix(routeKey(msg), "GetAclEnabledState|") {
		return nil, f.fault
	}
	return f.userWSMan.Post(msg)
}

func TestUsersToleratesEnabledStateFault(t *testing.T) {
	_, u := userDevice()
	d := newDevice(&faultWSMan{userWSMan: u, fault: faultErr(t, "unreachable_fault.xml")}, "")
	d.cfg.Username = "admin"
	l, err := d.Users()
	if err != nil || len(l.Users) != 4 || l.Users[1].EnabledKnown {
		t.Errorf("users %+v, err %v", l.Users, err)
	}
	d = newDevice(&faultWSMan{userWSMan: u, fault: faultErr(t, "access_denied_fault.xml")}, "")
	if _, err := d.Users(); !errors.Is(err, ErrAccessDenied) {
		t.Errorf("access denied: %v", err)
	}
}
