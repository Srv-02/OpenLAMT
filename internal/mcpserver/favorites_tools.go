package mcpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/scan"
)

// Scanner finds AMT devices (*scan.Scanner); see scan.Scanner.Scan.
type Scanner interface {
	Scan(ctx context.Context, targets []netip.Addr, onProgress func(done, total int), onResult func(scan.Result))
}

func (s *server) registerFavorites(srv *mcp.Server) {
	if !s.cfg.Groups[GroupFavorites] {
		return
	}
	// The scan only reads (no password, no setting): it stays in read_only.
	addTool(s, srv, &mcp.Tool{Name: "scan_network", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
		Description: "Find Intel AMT devices in an IPv4 network (192.0.2.0/24), a range (192.0.2.10-192.0.2.80 or 192.0.2.10-80) " +
			"or at one address, as the scan of the OpenLAMT app does: up to 4096 addresses, no password, nothing is changed. " +
			"Returns address, DNS name, AMT version, open ports (tls: 16993, http: 16992) and whether the device is a favorite already."},
		s.scanNetwork)
	if !s.cfg.Enabled(GroupFavorites) || s.editor == nil {
		return
	}
	addTool(s, srv, &mcp.Tool{Name: "add_favorite", Annotations: &mcp.ToolAnnotations{OpenWorldHint: boolPtr(false), DestructiveHint: boolPtr(false)},
		InputSchema: addFavoriteSchema(),
		Description: "Save a device as an OpenLAMT favorite (also seen by the app): name, host, user name and TLS. Never takes a password: " +
			"a person provides it with \"openlamt-mcp password <name>\". With TLS, the person in the MCP client may be shown the " +
			"certificate fingerprint to trust it; otherwise a person runs \"openlamt-mcp trust <name>\"."},
		s.addFavorite)
	destructive := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}
	addTool(s, srv, &mcp.Tool{Name: "update_favorite", Annotations: destructive,
		Description: "Change the name, host, user name or TLS of a favorite. Always asks the person in the MCP client first. " +
			"A new host or user name deletes the saved password and the trusted certificate of the favorite, as in the app: " +
			"a person provides the password of the new host separately."},
		s.updateFavorite)
	addTool(s, srv, &mcp.Tool{Name: "delete_favorite", Annotations: destructive,
		Description: "Delete a favorite together with its password in the system keychain and its trusted certificate. " +
			"Always asks the person in the MCP client first. The app loses the favorite too."},
		s.deleteFavorite)
}

// --- names, hosts and user names the agent sends

const (
	maxFavoriteName = 64
	maxHost         = 253
	maxUsername     = 32 // AMT allows up to 32 characters
)

// notInName are characters a favorite name from the agent may not hold: the name ends up in a
// command line the person copies (openlamt-mcp trust <name>) and may end up in the path of a
// password manager (passwords.command), so quotes, shell characters and path separators are out.
const notInName = "\"'`$\\/!%^;|&<>"

func badRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

// checkName validates the name of a favorite.
func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", refuse("name must not be empty.")
	case utf8.RuneCountInString(name) > maxFavoriteName:
		return "", refuse("name is longer than %d characters.", maxFavoriteName)
	case strings.ContainsFunc(name, badRune) || strings.ContainsAny(name, notInName) || strings.HasPrefix(name, "-"):
		return "", refuse("name %s holds a character that is not allowed: no control characters, none of %s and no leading \"-\".",
			quoteShort(name), notInName)
	}
	return name, nil
}

// checkHost validates a host as the app does (trimmed, lower case, no port, no IPv6) and allows
// only the characters of a host name or IPv4 address.
func checkHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", refuse("host is required: a host name or an IPv4 address.")
	}
	if strings.Contains(host, ":") {
		return "", refuse("host must not contain a port or be an IPv6 address.")
	}
	ok := len(host) <= maxHost && !strings.HasPrefix(host, "-") && !strings.HasPrefix(host, ".") && !strings.Contains(host, "..")
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			ok = false
		}
	}
	if !ok {
		return "", refuse("host %s is not a host name or IPv4 address.", quoteShort(host))
	}
	return host, nil
}

// checkUsername validates the AMT user name of a favorite.
func checkUsername(user string) (string, error) {
	user = strings.TrimSpace(user)
	switch {
	case user == "":
		return "", refuse("username must not be empty.")
	case utf8.RuneCountInString(user) > maxUsername:
		return "", refuse("username is longer than %d characters.", maxUsername)
	case strings.ContainsFunc(user, badRune) || strings.ContainsAny(user, `/\`) || strings.HasPrefix(user, "-"):
		return "", refuse("username %s holds a character that is not allowed.", quoteShort(user))
	}
	return user, nil
}

// commandLine is the openlamt-mcp subcommand a person runs for the favorite name, as the agent
// should show it. A name that is not safe to copy into a shell is left out.
func commandLine(sub, name string) string {
	switch {
	case name == "" || strings.ContainsFunc(name, badRune) || strings.ContainsAny(name, notInName) || strings.HasPrefix(name, "-"):
		name = "<favorite name>"
	case strings.ContainsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@+", r))
	}):
		name = `"` + name + `"`
	}
	return "`openlamt-mcp " + sub + " " + name + "`"
}

// checkNewFavorite refuses fav when list has its host and user or its name already, or when
// allowed_devices would not let the agent use it. skip is the favorite being changed.
func (s *server) checkNewFavorite(fav favorites.Favorite, list []favorites.Favorite, skip *favorites.Favorite) error {
	name := fav.DisplayName()
	if !s.cfg.DeviceAllowed(name) {
		return refuse("The name %s is not in allowed_devices of the openlamt-mcp config, so the favorite could not be used here.", quoteShort(name))
	}
	for _, f := range list {
		if skip != nil && f.Host == skip.Host && f.Username == skip.Username {
			continue
		}
		if f.Host == fav.Host && f.Username == fav.Username {
			return refuse("A favorite for %s on %s exists already: %s.", quoteShort(fav.Username), quoteShort(fav.Host), quoteShort(f.DisplayName()))
		}
		if f.DisplayName() == name {
			return refuse("Another favorite is named %s already; names must be unique.", quoteShort(name))
		}
	}
	return nil
}

// editError turns an error of the favorites manager into the answer: a conflict is a refusal,
// a keychain step that failed after the file was written is a warning.
func editError(err error) (warning string, _ error) {
	var ke *favorites.KeychainError
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, favorites.ErrExists):
		return "", refuse("Another favorite has this host and user name.")
	case errors.Is(err, favorites.ErrNameExists):
		return "", refuse("Another favorite has this name; names must be unique.")
	case errors.Is(err, favorites.ErrNotFound):
		return "", refuse("The favorite is gone (changed in the app meanwhile). Call list_devices.")
	case errors.As(err, &ke):
		return "The favorites were saved, but a keychain step failed: " + explain(keychainError(ke.Err)), nil
	}
	return "", fmt.Errorf("save the OpenLAMT favorites: %w", err)
}

// favoriteOutput is a favorite after add_favorite or update_favorite, with what is missing to use it.
type favoriteOutput struct {
	Name              string   `json:"name"`
	Host              string   `json:"host"`
	Username          string   `json:"username"`
	TLS               bool     `json:"tls"`
	CertificatePinned bool     `json:"certificate_pinned"`
	Fingerprint       string   `json:"certificate_sha256,omitempty" jsonschema:"the certificate the device presented (TLS), as shown to the person"`
	PasswordFound     bool     `json:"password_found"`
	NextSteps         []string `json:"next_steps,omitempty" jsonschema:"what a person still has to do before the device can be used"`
	Warnings          []string `json:"warnings,omitempty"`
}

// describe fills the state of fav: pin and password, with the steps a person still has to take.
func (s *server) describe(ctx context.Context, fav favorites.Favorite, fp string) favoriteOutput {
	name := fav.DisplayName()
	out := favoriteOutput{Name: name, Host: fav.Host, Username: fav.Username, TLS: fav.UseTLS}
	if fp != "" {
		out.Fingerprint = amt.FormatFingerprint(fp)
	}
	if _, _, err := s.pws.lookup(ctx, fav); err == nil {
		out.PasswordFound = true
	} else {
		out.NextSteps = append(out.NextSteps, s.passwordHint(name))
	}
	if fav.UseTLS {
		pin, err := s.keys.GetFingerprint(fav.Host, fav.Username)
		out.CertificatePinned = err == nil && pin != ""
		if !out.CertificatePinned {
			out.NextSteps = append(out.NextSteps, "Certificate not trusted yet: a person runs "+commandLine("trust", name)+
				" in a terminal on this computer, or connects once with the OpenLAMT app.")
		}
	} else {
		out.Warnings = append(out.Warnings, noTLSWarning)
	}
	return out
}

// --- scan_network

type scanInput struct {
	Range string `json:"range" jsonschema:"IPv4 network (192.0.2.0/24), range (192.0.2.10-192.0.2.80 or 192.0.2.10-80) or one address; up to 4096 addresses"`
}

type scanDevice struct {
	Address         string `json:"address"`
	Name            string `json:"name,omitempty" jsonschema:"reverse DNS name"`
	AMTVersion      string `json:"amt_version,omitempty"`
	TLS             bool   `json:"tls" jsonschema:"TCP 16993 (AMT over TLS) is open"`
	HTTP            bool   `json:"http" jsonschema:"TCP 16992 (AMT without TLS) is open"`
	AlreadyFavorite bool   `json:"already_favorite"`
	Note            string `json:"note,omitempty"`
}

type scanOutput struct {
	Range   string       `json:"range"`
	Scanned int          `json:"scanned" jsonschema:"addresses probed"`
	Devices []scanDevice `json:"devices"`
}

func (s *server) scanNetwork(ctx context.Context, req *mcp.CallToolRequest, in scanInput, rec *record) (*mcp.CallToolResult, scanOutput, error) {
	rec.action = "scan " + shorten(strings.TrimSpace(in.Range), maxLogged)
	targets, err := scan.ParseTargets(in.Range)
	switch {
	case errors.Is(err, scan.ErrTooMany):
		return nil, scanOutput{}, refuse("Scan up to %d addresses at a time (a /20), for example 192.0.2.0/20; scan a larger network in parts.", scan.MaxAddresses)
	case err != nil:
		return nil, scanOutput{}, refuse("range must be an IPv4 network such as 192.0.2.0/24, a range such as 192.0.2.10-192.0.2.80 or one address.")
	}
	list, err := s.favs.List()
	if err != nil {
		return nil, scanOutput{}, fmt.Errorf("read the OpenLAMT favorites: %w", err)
	}
	known := map[string]bool{}
	for _, f := range list {
		known[f.Host] = true
	}
	var token any
	if req != nil && req.Params != nil {
		token = req.Params.GetProgressToken()
	}
	step := max(1, len(targets)/20)
	var mu sync.Mutex
	out := scanOutput{Range: strings.TrimSpace(in.Range), Devices: []scanDevice{}}
	var found []scan.Result
	s.scanner.Scan(ctx, targets, func(done, total int) {
		mu.Lock()
		out.Scanned = done
		mu.Unlock()
		if token != nil && (done%step == 0 || done == total) {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: float64(done), Total: float64(total),
				Message: fmt.Sprintf("%d of %d addresses", done, total)})
		}
	}, func(r scan.Result) {
		mu.Lock()
		found = append(found, r)
		mu.Unlock()
	})
	if err := ctx.Err(); err != nil {
		return nil, scanOutput{}, err
	}
	slices.SortFunc(found, func(a, b scan.Result) int { return a.Addr.Compare(b.Addr) })
	for _, r := range found {
		d := scanDevice{Address: r.Addr.String(), Name: r.Name, AMTVersion: r.Version, TLS: r.TLS, HTTP: r.HTTP,
			AlreadyFavorite: known[r.Addr.String()] || r.Name != "" && known[strings.ToLower(r.Name)]}
		if r.PortsClosed() {
			d.Note = "Only the RMCP ping answered: AMT is probably not activated (provisioned) on this device."
		}
		out.Devices = append(out.Devices, d)
	}
	rec.action += " found " + strconv.Itoa(len(out.Devices))
	return nil, out, nil
}

// --- add_favorite

type addFavoriteInput struct {
	Name     string `json:"name,omitempty"`
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	TLS      bool   `json:"tls"`
}

func addFavoriteSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name":     {Type: "string", Description: "favorite name, unique; default: the host"},
			"host":     {Type: "string", Description: "host name or IPv4 address of the AMT device"},
			"username": {Type: "string", Description: "AMT user name; default admin"},
			"tls":      {Type: "boolean", Description: "connect over TLS (port 16993); scan_network reports whether the device offers it"},
		},
		Required: []string{"host", "tls"},
	}
}

// Keys of the add_favorite question.
const (
	confirmField = "confirm"
	trustField   = "trust_certificate"
)

// canAsk reports whether the MCP client supports elicitation.
func canAsk(req *mcp.CallToolRequest) bool {
	p := req.Session.InitializeParams()
	return p != nil && p.Capabilities != nil && p.Capabilities.Elicitation != nil
}

func (s *server) addFavorite(ctx context.Context, req *mcp.CallToolRequest, in addFavoriteInput, rec *record) (*mcp.CallToolResult, favoriteOutput, error) {
	rec.device = in.Name
	if rec.device == "" {
		rec.device = in.Host
	}
	host, err := checkHost(in.Host)
	if err != nil {
		return nil, favoriteOutput{}, err
	}
	user := in.Username
	if strings.TrimSpace(user) == "" {
		user = "admin"
	}
	if user, err = checkUsername(user); err != nil {
		return nil, favoriteOutput{}, err
	}
	name := host
	if strings.TrimSpace(in.Name) != "" {
		if name, err = checkName(in.Name); err != nil {
			return nil, favoriteOutput{}, err
		}
	}
	fav := favorites.Favorite{Name: name, Host: host, Username: user, UseTLS: in.TLS}
	rec.device = name
	rec.action = fmt.Sprintf("add host=%s user=%s tls=%t", host, user, in.TLS)
	list, err := s.favs.List()
	if err != nil {
		return nil, favoriteOutput{}, fmt.Errorf("read the OpenLAMT favorites: %w", err)
	}
	if err := s.checkNewFavorite(fav, list, nil); err != nil {
		return nil, favoriteOutput{}, err
	}

	// With TLS and a client that can ask, the person may trust the certificate right here. A
	// pin that is in the keychain already (from an earlier favorite) stays as it is.
	var fp string
	var notes []string
	pinned := false
	if fav.UseTLS {
		pin, err := s.keys.GetFingerprint(host, user)
		pinned = err == nil && pin != ""
	}
	if fav.UseTLS && !pinned && canAsk(req) {
		if fp, err = s.fetchFP(ctx, host); err != nil {
			fp = ""
			notes = append(notes, "Could not read the certificate of the device: "+explain(err))
		}
	}
	trust := false
	if s.cfg.RequireClientConfirmation || fp != "" {
		state := s.bindState(fav, "add\x00"+fp)
		resp, answered := req.Params.InputResponses[confirmKey]
		if !answered {
			if s.cfg.RequireClientConfirmation && !canAsk(req) {
				_, err := s.ask(req, question{}) // the refusal for a client without elicitation
				return nil, favoriteOutput{}, err
			}
			return addQuestion(fav, fp, state, s.cfg.RequireClientConfirmation), favoriteOutput{}, nil
		}
		if subtle.ConstantTimeCompare([]byte(req.Params.RequestState), []byte(state)) != 1 {
			return nil, favoriteOutput{}, refuse("The arguments or the certificate of the device changed while the question was open, " +
				"so the answer was for something else. Nothing was saved; call add_favorite again.")
		}
		r, _ := resp.(*mcp.ElicitResult)
		accepted := r != nil && r.Action == "accept"
		if s.cfg.RequireClientConfirmation && !(accepted && r.Content[confirmField] == true) {
			return nil, favoriteOutput{}, refuse("Adding the favorite was not confirmed in the MCP client. Nothing was saved.")
		}
		trust = fp != "" && accepted && r.Content[trustField] == true
	}

	warning, err := editError(s.editor.Add(fav))
	if err != nil {
		return nil, favoriteOutput{}, err
	}
	if trust {
		if err := s.keys.SetFingerprint(host, user, fp); err != nil {
			notes = append(notes, "The certificate could not be pinned: "+explain(keychainError(err)))
		} else {
			rec.action += " pinned"
		}
	} else if fp != "" {
		notes = append(notes, "The person did not trust the certificate in the MCP client.")
	}
	out := s.describe(ctx, fav, fp)
	if warning != "" {
		out.Warnings = append(out.Warnings, warning)
	}
	out.Warnings = append(out.Warnings, notes...)
	return nil, out, nil
}

// addQuestion asks the person in the MCP client about a new favorite: whether to add it (with
// require_client_confirmation) and, when the device presented a certificate, whether to trust it.
func addQuestion(fav favorites.Favorite, fp, state string, confirm bool) *mcp.CallToolResult {
	tls := "without TLS"
	if fav.UseTLS {
		tls = "over TLS"
	}
	msg := fmt.Sprintf("The AI agent adds the OpenLAMT favorite %q: user %q on %s, %s.", visible(fav.Name), visible(fav.Username), fav.Host, tls)
	props := map[string]*jsonschema.Schema{}
	var required []string
	if confirm {
		props[confirmField] = &jsonschema.Schema{Type: "boolean", Title: "Add this favorite", Description: "The AI agent asks to save this favorite."}
		required = append(required, confirmField)
	}
	if fp != "" {
		msg += "\n\nThe device presents this certificate (SHA-256):\n" + fingerprintLines(fp) +
			"\n\nIf you trust it, OpenLAMT remembers the certificate and refuses the connection if it ever changes. " +
			"If you are not sure, leave it unticked and check it later with \"openlamt-mcp trust\" or in the OpenLAMT app."
		props[trustField] = &jsonschema.Schema{Type: "boolean", Title: "Trust this certificate",
			Description: "Pin the certificate shown above for this favorite."}
	}
	return &mcp.CallToolResult{RequestState: state, InputRequests: mcp.InputRequestMap{confirmKey: &mcp.ElicitParams{
		Message:         msg,
		RequestedSchema: &jsonschema.Schema{Type: "object", Properties: props, Required: required},
	}}}
}

// fingerprintLines shows a fingerprint as four lines of eight bytes, as the app does.
func fingerprintLines(fp string) string {
	parts := strings.Split(amt.FormatFingerprint(fp), ":")
	var lines []string
	for i := 0; i < len(parts); i += 8 {
		lines = append(lines, strings.Join(parts[i:min(i+8, len(parts))], ":"))
	}
	return strings.Join(lines, "\n")
}

// --- update_favorite

type updateFavoriteInput struct {
	Device   string  `json:"device" jsonschema:"favorite name of the device, as returned by list_devices"`
	Name     *string `json:"name,omitempty" jsonschema:"new favorite name"`
	Host     *string `json:"host,omitempty" jsonschema:"new host name or IPv4 address; deletes the saved password and the trusted certificate"`
	Username *string `json:"username,omitempty" jsonschema:"new AMT user name; deletes the saved password and the trusted certificate"`
	TLS      *bool   `json:"tls,omitempty" jsonschema:"connect over TLS"`
}

// mediaBusy refuses a change of the device of fav while media is mounted on it: the mount
// belongs to the device the favorite points to now.
func (s *server) mediaBusy(fav favorites.Favorite) error {
	busy := false
	_ = s.media.inspect(fav, func(e *mediaEntry, pending bool) { busy = e != nil })
	if busy {
		return refuse("Media is mounted on %q. Call media_unmount first.", fav.DisplayName())
	}
	return nil
}

// closeSessions ends the KVM and serial console sessions of the device of fav: it is no longer
// the favorite's device.
func (s *server) closeSessions(fav favorites.Favorite) {
	s.kvm.disconnect(fav)
	s.sol.disconnect(fav)
}

func (s *server) updateFavorite(ctx context.Context, req *mcp.CallToolRequest, in updateFavoriteInput, rec *record) (*mcp.CallToolResult, favoriteOutput, error) {
	rec.device = in.Device
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, favoriteOutput{}, err
	}
	next := fav
	var changes []string
	if in.Name != nil {
		if next.Name, err = checkName(*in.Name); err != nil {
			return nil, favoriteOutput{}, err
		}
	}
	if in.Host != nil {
		if next.Host, err = checkHost(*in.Host); err != nil {
			return nil, favoriteOutput{}, err
		}
	}
	if in.Username != nil {
		if next.Username, err = checkUsername(*in.Username); err != nil {
			return nil, favoriteOutput{}, err
		}
	}
	if in.TLS != nil {
		next.UseTLS = *in.TLS
	}
	onOff := map[bool]string{true: "on", false: "off"}
	for _, c := range []struct{ what, from, to string }{
		{"name", fav.DisplayName(), next.DisplayName()}, {"host", fav.Host, next.Host},
		{"user", fav.Username, next.Username}, {"TLS", onOff[fav.UseTLS], onOff[next.UseTLS]},
	} {
		if c.from != c.to {
			changes = append(changes, fmt.Sprintf("%s: %s → %s", c.what, visible(c.from), visible(c.to)))
		}
	}
	if len(changes) == 0 {
		return nil, favoriteOutput{}, refuse("Nothing to change: give name, host, username or tls with a new value.")
	}
	rec.action = "update " + strings.Join(changes, ", ")
	list, err := s.favs.List()
	if err != nil {
		return nil, favoriteOutput{}, fmt.Errorf("read the OpenLAMT favorites: %w", err)
	}
	if err := s.checkNewFavorite(next, list, &fav); err != nil {
		return nil, favoriteOutput{}, err
	}
	moved := next.Host != fav.Host || next.Username != fav.Username
	if moved || next.UseTLS != fav.UseTLS {
		if err := s.mediaBusy(fav); err != nil {
			return nil, favoriteOutput{}, err
		}
	}
	msg := fmt.Sprintf("The AI agent changes the OpenLAMT favorite %q (also in the app):\n%s", visible(fav.DisplayName()), strings.Join(changes, "\n"))
	if moved {
		msg += "\n\nSaved passwords and the trusted certificate stay with the old host and user: they are deleted from the keychain " +
			"and never go to the new host. A person provides the password for the new host separately (" + commandLine("password", next.DisplayName()) +
			" in a terminal, or the password manager entry for the new host), and trusts its certificate again."
	}
	if fav.UseTLS && !next.UseTLS {
		msg += "\n\n" + noTLSWarning
	}
	state := strings.Join([]string{"update", next.Name, next.Host, next.Username, strconv.FormatBool(next.UseTLS)}, "\x00")
	if res, err := s.ask(req, question{message: msg, state: s.bindState(fav, state), risk: true, title: "Change this favorite",
		stale: "The favorite changed while the question was open, so the answer was for something else. Nothing was changed; call update_favorite again."}); res != nil || err != nil {
		return res, favoriteOutput{}, err
	}
	saved, err := s.editor.Update(fav.Host, fav.Username, func(f *favorites.Favorite) {
		f.Name, f.Host, f.Username, f.UseTLS = next.Name, next.Host, next.Username, next.UseTLS
	})
	warning, err := editError(err)
	if err != nil {
		return nil, favoriteOutput{}, err
	}
	if moved || next.UseTLS != fav.UseTLS {
		s.closeSessions(fav)
	}
	out := s.describe(ctx, saved, "")
	if warning != "" {
		out.Warnings = append(out.Warnings, warning)
	}
	return nil, out, nil
}

// --- delete_favorite

type deleteFavoriteOutput struct {
	Deleted  string   `json:"deleted"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *server) deleteFavorite(_ context.Context, req *mcp.CallToolRequest, in deviceInput, rec *record) (*mcp.CallToolResult, deleteFavoriteOutput, error) {
	rec.device = in.Device
	rec.action = "delete"
	fav, err := s.lookup(in.Device)
	if err != nil {
		return nil, deleteFavoriteOutput{}, err
	}
	if err := s.mediaBusy(fav); err != nil {
		return nil, deleteFavoriteOutput{}, err
	}
	msg := fmt.Sprintf("The AI agent deletes the OpenLAMT favorite %q (user %q on %s), also in the app. "+
		"Its saved password in the system keychain and its trusted certificate are deleted too.",
		visible(fav.DisplayName()), visible(fav.Username), fav.Host)
	if len(s.cfg.Passwords.Command) > 0 {
		msg += " A password in your password manager stays."
	}
	if res, err := s.ask(req, question{message: msg, state: s.bindState(fav, "delete"), risk: true, title: "Delete this favorite",
		stale: "The favorite changed while the question was open, so the answer was for something else. Nothing was deleted; call delete_favorite again."}); res != nil || err != nil {
		return res, deleteFavoriteOutput{}, err
	}
	warning, err := editError(s.editor.Delete(fav.Host, fav.Username, true))
	if err != nil {
		return nil, deleteFavoriteOutput{}, err
	}
	s.closeSessions(fav)
	out := deleteFavoriteOutput{Deleted: fav.DisplayName()}
	if warning != "" {
		out.Warnings = append(out.Warnings, warning)
	}
	return nil, out, nil
}
