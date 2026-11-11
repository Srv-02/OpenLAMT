package amt

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	authorization "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/authorization"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// Realm is an AMT permission realm (AMT_AuthorizationService RealmValues).
type Realm int

const (
	RealmRedirection                Realm = 2
	RealmPTAdministration           Realm = 3 // administrator
	RealmHardwareAsset              Realm = 4
	RealmRemoteControl              Realm = 5
	RealmStorage                    Realm = 6
	RealmEventManager               Realm = 7
	RealmStorageAdmin               Realm = 8
	RealmAgentPresenceLocal         Realm = 9
	RealmAgentPresenceRemote        Realm = 10
	RealmCircuitBreaker             Realm = 11
	RealmNetworkTime                Realm = 12
	RealmGeneralInfo                Realm = 13
	RealmFirmwareUpdate             Realm = 14
	RealmEIT                        Realm = 15
	RealmLocalUN                    Realm = 16
	RealmEndpointAccessControl      Realm = 17
	RealmEndpointAccessControlAdmin Realm = 18
	RealmEventLogReader             Realm = 19
	RealmAuditLog                   Realm = 20
	RealmACL                        Realm = 21
	RealmLocalSystem                Realm = 24
)

var realmNames = map[Realm]string{
	RealmRedirection: "Redirection", RealmPTAdministration: "PTAdministration", RealmHardwareAsset: "HardwareAsset",
	RealmRemoteControl: "RemoteControl", RealmStorage: "Storage", RealmEventManager: "EventManager",
	RealmStorageAdmin: "StorageAdmin", RealmAgentPresenceLocal: "AgentPresenceLocal",
	RealmAgentPresenceRemote: "AgentPresenceRemote", RealmCircuitBreaker: "CircuitBreaker",
	RealmNetworkTime: "NetworkTime", RealmGeneralInfo: "GeneralInfo", RealmFirmwareUpdate: "FirmwareUpdate",
	RealmEIT: "EIT", RealmLocalUN: "LocalUN", RealmEndpointAccessControl: "EndpointAccessControl",
	RealmEndpointAccessControlAdmin: "EndpointAccessControlAdmin", RealmEventLogReader: "EventLogReader",
	RealmAuditLog: "AuditLog", RealmACL: "ACL", RealmLocalSystem: "LocalSystem",
}

// AssignableRealms are the realms a user can get (MeshCommander's named realms 2–21 and 24;
// 0, 1, 22 and 23 are invalid or reserved).
var AssignableRealms = []Realm{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 24}

// String returns an English identifier such as "AuditLog", "" for an unknown realm. The UI
// translates realms by their number, not by this text.
func (r Realm) String() string { return realmNames[r] }

// AccessPermission says from where an account may log in.
type AccessPermission int

const (
	AccessLocal           AccessPermission = 0 // through the local host interface only
	AccessNetwork         AccessPermission = 1
	AccessLocalAndNetwork AccessPermission = 2
)

// User is one entry of the AMT user ACL. The admin account is not part of the list.
type User struct {
	Handle       int
	Name         string // digest user name, "" for a Kerberos account
	KerberosSID  string // e.g. "S-1-5-21-…", "" for a digest account
	Access       AccessPermission
	Realms       []Realm // as the device returns them, including reserved ones
	Enabled      bool
	EnabledKnown bool // false if the device did not report the state
	System       bool // "$$" account of the firmware (e.g. $$OsAdmin): read-only here
	Self         bool // the account this session is logged in with: read-only here
}

// Protected reports whether RemoveUser and SetUserEnabled refuse the account. UpdateUser
// refuses only system accounts; the session's own account needs a confirmation (OpOwnAccount).
func (u User) Protected() bool { return u.System || u.Self }

// UserList is the admin account name and the user ACL entries.
type UserList struct {
	Admin string // the AMT admin account (GetAdminAclEntry); not changeable here
	Users []User
}

// UserSpec is a new or changed digest account. The password is required on every change,
// because AMT stores MD5(name:realm:password): the hash changes with the name.
type UserSpec struct {
	Name     string
	Password string
	Access   AccessPermission
	Realms   []Realm
}

const (
	systemAccountPrefix = "$$"
	returnInvalidHandle = 2053
	maxUserPages        = 16
)

// Validate checks the spec without device access. Names: 1–16 printable ASCII characters (AMT
// SDK limit). Passwords: 8–32 printable ASCII characters with upper and lower case letters, a
// digit and a special character. Unlike MeshCommander and the AMT SDK, ':', ',' and '"' are
// allowed (Michael's decision; a rejection by AMT comes back as an error). AMT only receives a
// hash and cannot check the rules.
func (u UserSpec) Validate() error {
	if err := validateName(u.Name); err != nil {
		return err
	}
	if err := validatePassword(u.Password); err != nil {
		return err
	}
	if u.Access < AccessLocal || u.Access > AccessLocalAndNetwork {
		return invalid("Access", "unknown access permission")
	}
	if len(u.Realms) == 0 {
		return invalid("Realms", "at least one realm")
	}
	for _, r := range u.Realms {
		if !slices.Contains(AssignableRealms, r) {
			return invalid("Realms", fmt.Sprintf("realm %d cannot be assigned", r))
		}
	}
	return nil
}

// validateName checks a digest user name: 1–16 printable ASCII characters without spaces and
// without : , " < > &, not starting with "$$".
func validateName(name string) error {
	if name == "" || len(name) > 16 {
		return invalid("Name", "1 to 16 characters")
	}
	if strings.HasPrefix(name, systemAccountPrefix) {
		return invalid("Name", `must not start with "$$" (reserved for system accounts)`)
	}
	for _, r := range name {
		if r <= ' ' || r > '~' || strings.ContainsRune(`:,"<>&`, r) {
			return invalid("Name", `printable ASCII without spaces and without : , " < > &`)
		}
	}
	return nil
}

// validatePassword checks the AMT strong password rule (see UserSpec.Validate).
func validatePassword(p string) error {
	if len(p) < 8 || len(p) > 32 {
		return invalid("Password", "8 to 32 characters")
	}
	var upper, lower, digit, special bool
	for _, r := range p {
		switch {
		case r < ' ' || r > '~':
			return invalid("Password", "printable ASCII characters only")
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			special = true
		}
	}
	if !upper || !lower || !digit || !special {
		return invalid("Password", "needs an upper case letter, a lower case letter, a digit and a special character")
	}
	return nil
}

func (u UserSpec) realmValues() []authorization.RealmValues {
	rs := slices.Clone(u.Realms)
	slices.Sort(rs)
	rs = slices.Compact(rs)
	out := make([]authorization.RealmValues, len(rs))
	for i, r := range rs {
		out[i] = authorization.RealmValues(r)
	}
	return out
}

// digestPassword is what AMT stores for a digest account: base64(MD5(name:realm:password)).
func digestPassword(name, realm, password string) string {
	sum := md5.Sum([]byte(name + ":" + realm + ":" + password))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// authOutput checks that the response has <method>_OUTPUT and returns its ReturnValue; a
// non-zero value is a *MethodError. It reads the raw XML, because the library has no response
// field for UpdateUserAclEntryEx.
func authOutput(m *client.Message, method string) (int, error) {
	var env struct {
		Body struct {
			Outputs []struct {
				XMLName xml.Name
				RV      *int `xml:"ReturnValue"`
			} `xml:",any"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal([]byte(xmlOf(m)), &env); err != nil {
		return 0, fmt.Errorf("%w: %s_OUTPUT: %v", ErrIncompleteResponse, method, err)
	}
	for _, o := range env.Body.Outputs {
		if o.XMLName.Local != method+"_OUTPUT" {
			continue
		}
		if o.RV == nil {
			break
		}
		if *o.RV != 0 {
			return *o.RV, &MethodError{Method: method, ReturnValue: *o.RV}
		}
		return 0, nil
	}
	return 0, fmt.Errorf("%w: %s_OUTPUT/ReturnValue", ErrIncompleteResponse, method)
}

// kerberosSID formats the base64 binary SID of an ACL entry, "" if absent or malformed.
func kerberosSID(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return ""
	}
	return sidString(raw)
}

// userEntry reads one ACL entry (without its enabled state).
func (d *Device) userEntry(h int) (User, error) {
	r, err := d.msgs.AMT.AuthorizationService.GetUserACLEntryEx(h)
	if err != nil {
		return User{}, err
	}
	if _, err := authOutput(r.Message, "GetUserAclEntryEx"); err != nil {
		return User{}, err
	}
	e := r.Body.GetUserResponse
	u := User{
		Handle:      h,
		Name:        strings.TrimSpace(e.DigestUsername),
		KerberosSID: kerberosSID(e.KerberosUserSid),
		Access:      AccessPermission(e.AccessPermission),
	}
	for _, rv := range e.Realms {
		u.Realms = append(u.Realms, Realm(rv))
	}
	u.System = strings.HasPrefix(u.Name, systemAccountPrefix)
	u.Self = u.Name != "" && strings.EqualFold(u.Name, d.cfg.Username)
	return u, nil
}

// userEnabled reads the enabled state; known is false if the device returns an error code.
func (d *Device) userEnabled(h int) (enabled, known bool, err error) {
	r, err := d.msgs.AMT.AuthorizationService.GetACLEnabledState(h)
	if err != nil {
		// MeshCommander tolerates faults of this call ("AMT will just freak out on this call")
		if isMissingClass(err) {
			return false, false, nil
		}
		return false, false, err
	}
	var me *MethodError
	if _, err := authOutput(r.Message, "GetAclEnabledState"); errors.As(err, &me) {
		return false, false, nil
	} else if err != nil {
		return false, false, err
	}
	return r.Body.GetACLEnabledStateResponse.Enabled, true, nil
}

// Users reads the admin account name and every user ACL entry with its enabled state.
func (d *Device) Users() (UserList, error) {
	const op = "read users"
	var list UserList
	a, err := d.msgs.AMT.AuthorizationService.GetAdminACLEntry()
	if err != nil {
		return UserList{}, writeError(op, err)
	}
	if _, err := authOutput(a.Message, "GetAdminAclEntry"); err != nil {
		return UserList{}, err
	}
	list.Admin = strings.TrimSpace(a.Body.GetAdminResponse.Username)

	var handles []int
	for page := 0; ; page++ {
		if page == maxUserPages {
			return UserList{}, fmt.Errorf("%s: %w: user enumeration did not end", op, ErrIncompleteResponse)
		}
		r, err := d.msgs.AMT.AuthorizationService.EnumerateUserACLEntries(len(handles) + 1)
		if err != nil {
			return UserList{}, writeError(op, err)
		}
		if _, err := authOutput(r.Message, "EnumerateUserAclEntries"); err != nil {
			return UserList{}, err
		}
		out := r.Body.EnumerateUserResponse
		handles = append(handles, out.Handles...)
		if len(out.Handles) == 0 || len(handles) >= out.TotalCount {
			break
		}
	}

	for _, h := range handles {
		u, err := d.userEntry(h)
		var me *MethodError
		if errors.As(err, &me) && me.ReturnValue == returnInvalidHandle {
			continue // removed between enumeration and read
		}
		if err != nil {
			return UserList{}, writeError(op, err)
		}
		if u.Enabled, u.EnabledKnown, err = d.userEnabled(h); err != nil {
			return UserList{}, writeError(op, err)
		}
		list.Users = append(list.Users, u)
	}
	return list, nil
}

// changeableUser reads entry h freshly and refuses system accounts and the session's own one.
func (d *Device) changeableUser(op string, h int) (User, error) {
	u, err := d.userEntry(h)
	var me *MethodError
	if errors.As(err, &me) {
		return User{}, err
	}
	if err != nil {
		return User{}, writeError(op, err)
	}
	if u.Protected() {
		return User{}, fmt.Errorf("%s: %w", op, ErrProtectedAccount)
	}
	return u, nil
}

// digestRealm reads AMT_GeneralSettings.DigestRealm, the realm of the password hash.
func (d *Device) digestRealm() (string, error) {
	o, err := d.generalObject()
	if err != nil {
		return "", err
	}
	realm := o.str("DigestRealm")
	if realm == "" {
		return "", fmt.Errorf("%w: DigestRealm", ErrIncompleteResponse)
	}
	return realm, nil
}

// AddUser creates a digest account and returns its handle. AMT answers a duplicate name with
// *MethodError ReturnValue 2058 and a full ACL with 23.
func (d *Device) AddUser(u UserSpec) (int, error) {
	const op = "add user"
	if err := u.Validate(); err != nil {
		return 0, err
	}
	realm, err := d.digestRealm()
	if err != nil {
		return 0, writeError(op, err)
	}
	r, err := d.msgs.AMT.AuthorizationService.AddUserAclEntryEx(u.Name, digestPassword(u.Name, realm, u.Password),
		authorization.AccessPermission(u.Access), u.realmValues())
	if err != nil {
		return 0, writeError(op, err)
	}
	if _, err := authOutput(r.Message, "AddUserAclEntryEx"); err != nil {
		return 0, err
	}
	return r.Body.AddUserResponse.Handle, nil
}

// UpdateUser replaces name, password, access and realms of digest account h (the library has
// no UpdateUserAclEntryEx; the request is built here with its input type). System accounts →
// ErrProtectedAccount; Kerberos accounts → ErrNotModifiable. The account of this connection
// needs confirm for OpOwnAccount and keeps network access; the connection continues with the
// new login. confirm is ignored for other accounts.
func (d *Device) UpdateUser(h int, u UserSpec, confirm Confirmation) (LoginChange, error) {
	const op = "update user"
	if err := u.Validate(); err != nil {
		return LoginChange{}, err
	}
	cur, err := d.userEntry(h)
	var me *MethodError
	if errors.As(err, &me) {
		return LoginChange{}, err
	}
	if err != nil {
		return LoginChange{}, writeError(op, err)
	}
	if cur.System {
		return LoginChange{}, fmt.Errorf("%s: %w", op, ErrProtectedAccount)
	}
	if cur.Name == "" {
		return LoginChange{}, fmt.Errorf("%s: Kerberos account: %w", op, ErrNotModifiable)
	}
	if cur.Self {
		if u.Access == AccessLocal {
			return LoginChange{}, invalid("Access", "the account of this connection needs network access")
		}
		if err := d.confirmed(confirm, OpOwnAccount, RiskOwnAccount); err != nil {
			return LoginChange{}, err
		}
	}
	realm, err := d.digestRealm()
	if err != nil {
		return LoginChange{}, writeError(op, err)
	}
	in := authorization.UpdateUserAclEntryEx_INPUT{
		H:                resourceURI(classAuth),
		Handle:           h,
		DigestUsername:   u.Name,
		DigestPassword:   digestPassword(u.Name, realm, u.Password),
		AccessPermission: authorization.AccessPermission(u.Access),
		Realms:           u.realmValues(),
	}
	body, err := xml.Marshal(&in)
	if err != nil {
		return LoginChange{}, writeError(op, err)
	}
	const method = "UpdateUserAclEntryEx"
	res := LoginChange{OldUsername: cur.Name, Username: u.Name, Self: cur.Self}
	out, err := d.msgs.Client.Post(envelope(amtSchema+classAuth+"/"+method, resourceURI(classAuth), nil, string(body)))
	if err == nil {
		if _, err := authOutput(&client.Message{XMLOutput: string(out)}, method); err != nil {
			return LoginChange{}, err
		}
	} else if fault := (*amterror.AMTError)(nil); errors.As(err, &fault) || !cur.Self {
		// AMT answered with a fault, or another account: its login cannot be tested from here
		// (it may lack network access), so the error stands as for any other write.
		return LoginChange{}, writeError(op, err)
	}
	return d.finishLogin(op, res, u.Password, err)
}

// RemoveUser deletes account h. System accounts and the session's own account →
// ErrProtectedAccount.
func (d *Device) RemoveUser(h int) error {
	const op = "remove user"
	if _, err := d.changeableUser(op, h); err != nil {
		return err
	}
	r, err := d.msgs.AMT.AuthorizationService.RemoveUserACLEntry(h)
	if err != nil {
		return writeError(op, err)
	}
	_, err = authOutput(r.Message, "RemoveUserAclEntry")
	return err
}

// SetUserEnabled enables or disables account h; nothing is written if it already has that
// state. System accounts and the session's own account → ErrProtectedAccount.
func (d *Device) SetUserEnabled(h int, enabled bool) error {
	const op = "set user enabled"
	if _, err := d.changeableUser(op, h); err != nil {
		return err
	}
	cur, known, err := d.userEnabled(h)
	if err != nil {
		return writeError(op, err)
	}
	if known && cur == enabled {
		return nil
	}
	r, err := d.msgs.AMT.AuthorizationService.SetACLEnabledState(h, enabled)
	if err != nil {
		return writeError(op, err)
	}
	_, err = authOutput(r.Message, "SetAclEnabledState")
	return err
}
