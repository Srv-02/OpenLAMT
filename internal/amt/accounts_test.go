package amt

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

const fixtureRealm = "Digest:00000000000000000000000000000000" // DigestRealm of cfg_general_get.xml

func TestAccountChangeValidate(t *testing.T) {
	if err := (AccountChange{Password: "Secret-Pass1"}).Validate(); err != nil {
		t.Errorf("password only: %v", err)
	}
	if err := (AccountChange{Password: `Se:cr,et"Pass1`}).Validate(); err != nil {
		t.Errorf(`password with : , ": %v`, err)
	}
	for name, c := range map[string]AccountChange{
		"weak password": {Password: "secret"},
		"no password":   {Name: "newadmin"},
		"system prefix": {Name: "$$admin", Password: "Secret-Pass1"},
		"name too long": {Name: strings.Repeat("a", 17), Password: "Secret-Pass1"},
		"comma in name": {Name: "a,b", Password: "Secret-Pass1"},
		"colon in name": {Name: "a:b", Password: "Secret-Pass1"},
	} {
		if err := c.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSetAdminAccountNeedsConfirmationBeforeDeviceAccess(t *testing.T) {
	for _, conf := range []Confirmation{{}, confirmed(OpOwnAccount), {op: OpAdminAccount, host: "other.example.com"}} {
		d, f := userDevice()
		_, err := d.SetAdminAccount(AccountChange{Password: "Secret-Pass1"}, conf)
		var re *RiskyChangeError
		if !errors.As(err, &re) || re.Op != OpAdminAccount || !reflect.DeepEqual(re.Fields, []string{RiskAdmin}) {
			t.Fatalf("err = %v", err)
		}
		if len(f.requests) != 0 {
			t.Errorf("%d requests before confirmation", len(f.requests))
		}
	}
	d, f := userDevice()
	if _, err := d.SetAdminAccount(AccountChange{Password: "weak"}, confirmed(OpAdminAccount)); !errors.Is(err, ErrInvalidValue) || len(f.requests) != 0 {
		t.Errorf("weak password: %v, %d requests", err, len(f.requests))
	}
}

func TestSetAdminAccountOwnLogin(t *testing.T) {
	d, f := userDevice()
	d.cfg.Password = "Old-Secret1"
	res, err := d.SetAdminAccount(AccountChange{Name: "newadmin", Password: "New-Secret2"}, confirmed(OpAdminAccount))
	if err != nil || res != (LoginChange{OldUsername: "admin", Username: "newadmin", Self: true}) {
		t.Fatalf("SetAdminAccount = %+v, %v", res, err)
	}
	body := onePut(t, &f.cfgWSMan, "SetAdminAclEntryEx|AMT_AuthorizationService")
	assertContains(t, body, "<h:Username>newadmin</h:Username>",
		"<h:DigestPassword>"+digestPassword("newadmin", fixtureRealm, "New-Secret2")+"</h:DigestPassword>")
	assertLacks(t, body, "New-Secret2")
	if d.cfg.Username != "newadmin" || d.cfg.Password != "New-Secret2" {
		t.Errorf("the connection must continue with the new login: %q", d.cfg.Username)
	}
}

func TestSetAdminAccountKeepsNameAndOtherLogin(t *testing.T) {
	d, f := userDevice()
	d.cfg.Username, d.cfg.Password = "user1", "User-Secret1"
	res, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount))
	if err != nil || res != (LoginChange{OldUsername: "admin", Username: "admin"}) {
		t.Fatalf("SetAdminAccount = %+v, %v", res, err)
	}
	assertContains(t, onePut(t, &f.cfgWSMan, "SetAdminAclEntryEx|AMT_AuthorizationService"), "<h:Username>admin</h:Username>")
	if d.cfg.Username != "user1" || d.cfg.Password != "User-Secret1" {
		t.Error("another account's change must not touch the connection's login")
	}
}

func TestSetAdminAccountErrorsKeepLogin(t *testing.T) {
	check := func(t *testing.T, d *Device) {
		t.Helper()
		if d.cfg.Username != "admin" || d.cfg.Password != "Old-Secret1" {
			t.Error("login changed after a failed change")
		}
	}
	t.Run("ReturnValue", func(t *testing.T) {
		d, f := userDevice()
		d.cfg.Password = "Old-Secret1"
		f.rv["SetAdminAclEntryEx"] = 2054
		var me *MethodError
		if _, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount)); !errors.As(err, &me) || me.ReturnValue != 2054 {
			t.Fatalf("err = %v", err)
		}
		check(t, d)
	})
	t.Run("fault", func(t *testing.T) {
		d, f := userDevice()
		d.cfg.Password = "Old-Secret1"
		f.errs = map[string]error{"SetAdminAclEntryEx": faultErr(t, "access_denied_fault.xml")}
		_, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount))
		if !errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrCredentialsUncertain) {
			t.Fatalf("err = %v", err)
		}
		check(t, d)
	})
	t.Run("no answer", func(t *testing.T) {
		d, f := userDevice()
		d.cfg.Password = "Old-Secret1"
		f.errs = map[string]error{"SetAdminAclEntryEx": errors.New("connection reset by peer")}
		_, err := d.SetAdminAccount(AccountChange{Name: "newadmin", Password: "New-Secret2"}, confirmed(OpAdminAccount))
		var ue *CredentialsUncertainError
		if !errors.As(err, &ue) || ue.Username != "newadmin" || !ue.Self || !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "New-Secret2") {
			t.Error("the error text contains the password")
		}
		check(t, d)
	})
	t.Run("read admin fails", func(t *testing.T) {
		d, f := userDevice()
		d.cfg.Password = "Old-Secret1"
		f.rv["GetAdminAclEntry"] = 1
		var me *MethodError
		if _, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount)); !errors.As(err, &me) {
			t.Fatalf("err = %v", err)
		}
		assertNoWrites(t, &f.cfgWSMan)
		check(t, d)
	})
}

func TestUpdateOwnAccount(t *testing.T) {
	spec := UserSpec{Name: "user1-new", Password: "New-Secret2", Access: AccessNetwork, Realms: []Realm{RealmRedirection, RealmPTAdministration}}
	newOwn := func() (*Device, *userWSMan) {
		d, f := userDevice()
		d.cfg.Username, d.cfg.Password = "user1", "Old-Secret1"
		return d, f
	}

	d, f := newOwn()
	_, err := d.UpdateUser(2, spec, Confirmation{})
	var re *RiskyChangeError
	if !errors.As(err, &re) || re.Op != OpOwnAccount || !reflect.DeepEqual(re.Fields, []string{RiskOwnAccount}) {
		t.Fatalf("unconfirmed: %v", err)
	}
	if _, err := d.UpdateUser(2, spec, confirmed(OpAdminAccount)); !errors.Is(err, ErrRiskyChange) {
		t.Fatalf("confirmation of another operation: %v", err)
	}
	local := spec
	local.Access = AccessLocal
	if _, err := d.UpdateUser(2, local, confirmed(OpOwnAccount)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("own account without network access: %v", err)
	}
	assertNoWrites(t, &f.cfgWSMan)

	res, err := d.UpdateUser(2, spec, confirmed(OpOwnAccount))
	if err != nil || res != (LoginChange{OldUsername: "user1", Username: "user1-new", Self: true}) {
		t.Fatalf("UpdateUser = %+v, %v", res, err)
	}
	assertContains(t, onePut(t, &f.cfgWSMan, "UpdateUserAclEntryEx|AMT_AuthorizationService"),
		"<h:Handle>2</h:Handle><h:DigestUsername>user1-new</h:DigestUsername><h:DigestPassword>"+digestPassword("user1-new", fixtureRealm, "New-Secret2")+"</h:DigestPassword>")
	if d.cfg.Username != "user1-new" || d.cfg.Password != "New-Secret2" {
		t.Errorf("connection login %q", d.cfg.Username)
	}

	d, f = newOwn()
	f.errs = map[string]error{"UpdateUserAclEntryEx": errors.New("EOF")}
	if _, err := d.UpdateUser(2, spec, confirmed(OpOwnAccount)); !errors.Is(err, ErrCredentialsUncertain) || d.cfg.Username != "user1" {
		t.Errorf("no answer: %v, login %q", err, d.cfg.Username)
	}
	d, f = userDevice()
	f.errs = map[string]error{"UpdateUserAclEntryEx": errors.New("EOF")}
	if _, err := d.UpdateUser(3, spec, Confirmation{}); err == nil || errors.Is(err, ErrCredentialsUncertain) {
		t.Errorf("another account without answer: %v", err)
	}
}

// loginServer is a WS-Man test server whose admin login SetAdminAclEntryEx really changes: the
// digest check uses HA1 from the stored DigestPassword, as AMT does.
type loginServer struct {
	*httptest.Server
	mu   sync.Mutex
	user string
	ha1  string // hex MD5(user:realm:password)
	// dropSet: SetAdminAclEntryEx closes the connection without an answer; with apply the change
	// is stored first (the answer got lost), without it nothing changes.
	dropSet, apply bool
}

var bodyField = func(name string) *regexp.Regexp {
	return regexp.MustCompile(`<h:` + name + `>([^<]*)</h:` + name + `>`)
}

func newLoginServer(t *testing.T, user, pass string) *loginServer {
	t.Helper()
	ls := &loginServer{user: user, ha1: md5hex(user + ":" + fixtureRealm + ":" + pass)}
	ls.Server = httptest.NewServer(http.HandlerFunc(ls.serve))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *loginServer) serve(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		w.Header().Set("WWW-Authenticate", `Digest realm="`+fixtureRealm+`", nonce="0123456789abcdef", qop="auth", algorithm=MD5`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := map[string]string{}
	for _, m := range digestPair.FindAllStringSubmatch(auth, -1) {
		p[m[1]] = m[2]
	}
	if nc := regexp.MustCompile(`nc=([0-9a-f]+)`).FindStringSubmatch(auth); p["nc"] == "" && nc != nil {
		p["nc"] = nc[1]
	}
	ls.mu.Lock()
	user, ha1 := ls.user, ls.ha1
	ls.mu.Unlock()
	ha2 := md5hex("POST:" + p["uri"])
	if p["username"] != user || p["response"] != md5hex(ha1+":"+p["nonce"]+":"+p["nc"]+":"+p["cnonce"]+":"+p["qop"]+":"+ha2) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	b, _ := io.ReadAll(r.Body)
	body := string(b)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	switch method, _, _ := strings.Cut(routeKey(body), "|"); method {
	case "Get":
		data, _ := os.ReadFile(filepath.Join("testdata", "cfg_general_get.xml"))
		_, _ = w.Write(data)
	case "GetAdminAclEntry":
		_, _ = io.WriteString(w, authEnvelope(method, "<h:Username>"+user+"</h:Username><h:ReturnValue>0</h:ReturnValue>"))
	case "SetAdminAclEntryEx":
		ls.mu.Lock()
		if !ls.dropSet || ls.apply {
			raw, _ := base64.StdEncoding.DecodeString(bodyField("DigestPassword").FindStringSubmatch(body)[1])
			ls.user, ls.ha1 = bodyField("Username").FindStringSubmatch(body)[1], hex.EncodeToString(raw)
		}
		drop := ls.dropSet
		ls.mu.Unlock()
		if drop {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		_, _ = io.WriteString(w, authEnvelope(method, "<h:ReturnValue>0</h:ReturnValue>"))
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func connectLogin(t *testing.T, ls *loginServer, user, pass string) *Device {
	t.Helper()
	d, err := Connect(context.Background(), Config{Host: testHost, Username: user, Password: pass, dialContext: dialTo(ls.Server)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.digestRealm(); err != nil {
		t.Fatalf("login before the change: %v", err)
	}
	return d
}

func TestSetAdminAccountAgainstDigestServer(t *testing.T) {
	ls := newLoginServer(t, "admin", "Old-Secret1")
	d := connectLogin(t, ls, "admin", "Old-Secret1")
	if _, err := d.SetAdminAccount(AccountChange{Name: "newadmin", Password: "New-Secret2"}, confirmed(OpAdminAccount)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.digestRealm(); err != nil {
		t.Fatalf("the connection must work with the new login: %v", err)
	}
	if ok, err := d.loginWorks("admin", "Old-Secret1"); ok || err != nil {
		t.Errorf("old login still works: %v %v", ok, err)
	}
	if ok, err := d.loginWorks("newadmin", "New-Secret2"); !ok || err != nil {
		t.Errorf("new login: %v %v", ok, err)
	}
}

func TestSetAdminAccountLostAnswer(t *testing.T) {
	ls := newLoginServer(t, "admin", "Old-Secret1")
	ls.dropSet, ls.apply = true, true
	d := connectLogin(t, ls, "admin", "Old-Secret1")
	res, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount))
	if err != nil || !res.Self {
		t.Fatalf("applied change with lost answer: %+v, %v", res, err)
	}
	if _, err := d.digestRealm(); err != nil || d.cfg.Password != "New-Secret2" {
		t.Fatalf("the connection must use the new login: %v", err)
	}
}

func TestSetAdminAccountNotApplied(t *testing.T) {
	ls := newLoginServer(t, "admin", "Old-Secret1")
	ls.dropSet = true
	d := connectLogin(t, ls, "admin", "Old-Secret1")
	_, err := d.SetAdminAccount(AccountChange{Password: "New-Secret2"}, confirmed(OpAdminAccount))
	if err == nil || errors.Is(err, ErrCredentialsUncertain) {
		t.Fatalf("change without effect: %v", err)
	}
	if _, err := d.digestRealm(); err != nil || d.cfg.Password != "Old-Secret1" {
		t.Fatalf("the connection must keep the old login: %v", err)
	}
}
