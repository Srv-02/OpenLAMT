package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	models "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/models"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

type startHarness struct {
	s        *StartWindow
	keys     *keychain.Memory
	dev      *session.Fake
	opened   chan amt.Config
	errs     chan error
	reloaded chan struct{}
	ended    chan struct{} // one signal per finished connection attempt (after onOpened/onError)
}

// newStart builds a start window with fakes; openDevice records the session config instead
// of opening a window. closeAll runs at cleanup for tests that use the real wiring.
func newStart(t *testing.T) *startHarness {
	t.Helper()
	englishUI(t)
	a := test.NewTempApp(t)
	h := &startHarness{keys: keychain.NewMemory(), dev: &session.Fake{FP: fp},
		opened: make(chan amt.Config, 8), errs: make(chan error, 8), reloaded: make(chan struct{}, 16), ended: make(chan struct{}, 8)}
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), h.keys)
	h.s = newStartWindow(a, mgr, h.keys, session.ConnectFake(h.dev), func(s *StartWindow) {
		// The background probe would dial the fake on its own goroutine; status_probe_test.go
		// covers it. The keychain lookup runs inline, so no goroutine touches the window.
		s.prober.Stop()
		s.lookupPassword = func(host, user string, answer func(bool)) {
			pw, err := h.keys.Get(host, user)
			answer(err == nil && pw != "")
		}
	})
	t.Cleanup(func() { h.s.closeAll(); h.s.win.Close() })
	h.s.onError = func(err error) { h.errs <- err }
	h.s.openDevice = func(sess *session.Session, _ amt.Status, _ string) { h.opened <- sess.Config() }
	h.s.onReloaded = func() { h.reloaded <- struct{}{} }
	h.s.conn.askTrust = func(_, _ string, answer func(bool)) { answer(true) }
	end := h.s.conn.onDone
	h.s.conn.onDone = func(host, user string) { end(host, user); h.ended <- struct{}{} }
	return h
}

// waitEnded blocks until the attempt finished; reads of window state after it are ordered
// behind the Fyne-thread work (the test driver runs fyne.Do on the calling goroutine).
func (h *startHarness) waitEnded(t *testing.T) {
	t.Helper()
	select {
	case <-h.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt did not end")
	}
}

// reload refreshes the list synchronously and consumes its own signal.
func (h *startHarness) reload(t *testing.T) {
	t.Helper()
	h.s.reload()
	h.waitReload(t)
}

func (h *startHarness) waitCfg(t *testing.T) amt.Config {
	t.Helper()
	select {
	case cfg := <-h.opened:
		return cfg
	case err := <-h.errs:
		t.Fatalf("error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("no session")
	}
	return amt.Config{}
}

func (h *startHarness) waitReload(t *testing.T) {
	t.Helper()
	select {
	case <-h.reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("list not reloaded")
	}
}

func TestSaveStoresFavoriteWithoutConnecting(t *testing.T) {
	h := newStart(t)
	s := h.s
	if hasSavePasswordRow(s) {
		t.Fatal("Quick Connect shows the Save password switch")
	}
	s.host.SetText(" Device.Example.COM ")
	s.name.SetText("Büro")
	s.password.SetText("secret-1")
	test.Tap(s.saveBtn)
	h.waitReload(t)
	if len(s.items) != 1 || s.items[0].Name != "Büro" || s.items[0].Host != "device.example.com" || !s.items[0].UseTLS {
		t.Fatalf("%+v", s.items)
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Fatal("password not saved")
	}
	if len(h.dev.Configs()) != 0 || len(h.opened) != 0 {
		t.Fatal("saving connected")
	}
	// The saved favorite is selected and shown read only.
	if s.mode != modeView || s.selKey != deviceKey("device.example.com", "admin") || !s.rows[s.selKey].Selected() || s.password.Text != "" {
		t.Fatalf("mode=%v sel=%q", s.mode, s.selKey)
	}

	// Without a password an existing keychain entry is left alone; the favorite is updated.
	test.Tap(s.newConnBtn)
	if s.mode != modeNew || s.host.Text != "" || s.selKey != "" {
		t.Fatal("Quick Connect did not clear the form")
	}
	s.host.SetText("device.example.com")
	test.Tap(s.useTLS)
	test.Tap(s.saveBtn)
	h.waitReload(t)
	if len(s.items) != 1 || s.items[0].UseTLS {
		t.Fatalf("not updated: %+v", s.items)
	}
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "secret-1" {
		t.Fatalf("password changed to %q", pw)
	}
	// A password typed into the form is stored with the favorite, there is no switch for it.
	test.Tap(s.newConnBtn)
	s.host.SetText("device.example.com")
	s.password.SetText("other-1")
	test.Tap(s.saveBtn)
	h.waitReload(t)
	if pw, _ := h.keys.Get("device.example.com", "admin"); pw != "other-1" {
		t.Fatalf("password is %q", pw)
	}
}

// hasSavePasswordRow reports whether the card with the switches shows "Save password in keychain".
func hasSavePasswordRow(s *StartWindow) bool {
	found := false
	walk(s.card2, func(o fyne.CanvasObject) {
		if l, ok := o.(*canvas.Text); ok && l.Text == "Save password in keychain" {
			found = true
		}
		if l, ok := o.(*widget.Label); ok && l.Text == "Save password in keychain" {
			found = true
		}
	})
	return found
}

// The switch stays where a saved password can be removed: viewing and editing a favorite.
func TestSavePasswordSwitchOnlyForFavorites(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin"})
	h.keys.Set("device.example.com", "admin", "secret-1")
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	if !hasSavePasswordRow(s) {
		t.Fatal("favorite view without the switch")
	}
	test.Tap(s.editBtn)
	if !hasSavePasswordRow(s) {
		t.Fatal("edit mode without the switch")
	}
	test.Tap(s.newConnBtn)
	if hasSavePasswordRow(s) {
		t.Fatal("Quick Connect shows the switch")
	}
}

func TestSaveRejectsInvalidInputAndReportsFailure(t *testing.T) {
	h := newStart(t)
	s := h.s
	test.Tap(s.saveBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "Host is required") {
		t.Fatalf("err = %v", err)
	}
	s.host.SetText("device.example.com")
	s.user.SetText(" ")
	test.Tap(s.saveBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "Username is required") {
		t.Fatalf("err = %v", err)
	}
	s.user.SetText("admin")
	s.password.SetText("secret-1")
	h.keys.Err = errors.New("no secret service")
	test.Tap(s.saveBtn)
	var ke *favorites.KeychainError
	if err := <-h.errs; !errors.As(err, &ke) {
		t.Fatalf("err = %v", err)
	}
	h.waitReload(t)
	if len(s.items) != 1 {
		t.Fatal("favorite not saved although only the keychain failed")
	}
}

func TestCancelButtonEndsConnecting(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.conn.connect = blockingConnect(h.dev, make(chan struct{}))
	if s.cancelBtn.Visible() || s.status.Visible() {
		t.Fatal("cancel visible while idle")
	}
	s.host.SetText("device.example.com")
	s.password.SetText("secret-1")
	test.Tap(s.connectBtn)
	if !s.cancelBtn.Visible() || !s.connectBtn.Disabled() || s.connectBtn.Visible() || !s.status.Visible() || !strings.Contains(s.status.Text, "Connecting to device.example.com") {
		t.Fatal("no cancel while connecting")
	}
	test.Tap(s.cancelBtn)
	h.waitEnded(t)
	if s.cancelBtn.Visible() || s.connectBtn.Disabled() || len(s.busy) != 0 || s.status.Text != "" {
		t.Fatalf("still busy: %q", s.status.Text)
	}
	select {
	case <-h.opened:
		t.Fatal("session opened after cancel")
	case err := <-h.errs:
		t.Fatalf("error after cancel: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDefaultsAndConnectFromForm(t *testing.T) {
	h := newStart(t)
	s := h.s
	if s.user.Text != "admin" || !s.useTLS.Checked() || s.win.Title() != "OpenLAMT" || s.mode != modeNew || s.title.Text != "Quick Connect" {
		t.Fatalf("defaults user=%q tls=%v title=%q", s.user.Text, s.useTLS.Checked(), s.win.Title())
	}
	s.host.SetText(" Device.Example.COM ")
	s.password.SetText("secret-1")
	test.Tap(s.connectBtn)
	cfg := h.waitCfg(t)
	if cfg.Host != "device.example.com" || cfg.Username != "admin" || cfg.Password != "secret-1" || !cfg.UseTLS || cfg.Fingerprint != fp {
		t.Fatalf("%+v", cfg)
	}
	if s.password.Text != "" {
		t.Fatal("password field not cleared after connecting")
	}
	if len(h.dev.Configs()) != 1 {
		t.Fatalf("%d connects", len(h.dev.Configs()))
	}
	h.waitEnded(t)
	if len(s.busy) != 0 || s.connectBtn.Disabled() || s.status.Text != "" {
		t.Fatalf("form still busy: %q", s.status.Text)
	}
}

func TestConnectRejectsInvalidInput(t *testing.T) {
	h := newStart(t)
	s := h.s
	test.Tap(s.connectBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "Host is required") {
		t.Fatalf("err = %v", err)
	}
	s.host.SetText("device.example.com:16993")
	test.Tap(s.connectBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "must not contain a port") {
		t.Fatalf("err = %v", err)
	}
	s.host.SetText("device.example.com")
	s.user.SetText(" ")
	test.Tap(s.connectBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "Username is required") {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-h.opened:
		t.Fatal("session opened")
	case <-time.After(50 * time.Millisecond):
	}
	if len(h.dev.Configs()) != 0 || len(s.busy) != 0 || s.connectBtn.Disabled() {
		t.Fatal("invalid input left the form busy or reached the connection")
	}
}

func TestConnectingStateBlocksSecondAttempt(t *testing.T) {
	h := newStart(t)
	s := h.s
	release := make(chan struct{})
	s.conn.connect = func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		<-release
		return session.ConnectFake(h.dev)(ctx, cfg, prompt)
	}
	s.host.SetText("device.example.com")
	s.password.SetText("secret-1")
	test.Tap(s.connectBtn)
	if !s.connectBtn.Disabled() || !strings.Contains(s.status.Text, "Connecting to device.example.com") {
		t.Fatalf("no connecting state: disabled=%v status=%q", s.connectBtn.Disabled(), s.status.Text)
	}
	s.begin(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "x", UseTLS: true}})
	if len(s.busy) != 1 {
		t.Fatalf("%d attempts", len(s.busy))
	}
	close(release)
	h.waitCfg(t)
	h.waitEnded(t)
	if len(s.busy) != 0 || s.connectBtn.Disabled() || s.status.Text != "" || len(h.dev.Configs()) != 1 {
		t.Fatalf("after connect: disabled=%v status=%q connects=%d", s.connectBtn.Disabled(), s.status.Text, len(h.dev.Configs()))
	}
}

func TestEnterAndDoubleTapIgnoredWhileAttemptRuns(t *testing.T) {
	h := newStart(t)
	s := h.s
	release := make(chan struct{})
	s.conn.connect = func(ctx context.Context, cfg amt.Config, prompt amt.TrustPrompt) (session.Device, error) {
		<-release
		return session.ConnectFake(h.dev)(ctx, cfg, prompt)
	}
	s.mgr.Upsert(favorites.Favorite{Host: "other.example.com", Username: "operator", UseTLS: true})
	h.keys.Set("other.example.com", "operator", "secret-2")
	h.reload(t)
	s.host.SetText("device.example.com")
	s.password.SetText("secret-1")
	test.Tap(s.connectBtn)
	if len(s.busy) != 1 {
		t.Fatalf("%d attempts", len(s.busy))
	}
	// Enter in the password field with another host.
	s.host.SetText("third.example.com")
	s.password.OnSubmitted("")
	if len(s.busy) != 1 {
		t.Fatalf("Enter started a second attempt: %d", len(s.busy))
	}
	// Double-click on a favorite row.
	test.DoubleTap(s.rows[deviceKey("other.example.com", "operator")])
	if len(s.busy) != 1 {
		t.Fatalf("double-click started a second attempt: %d", len(s.busy))
	}
	close(release)
	h.waitCfg(t)
	h.waitEnded(t)
	if len(s.busy) != 0 || len(h.dev.Configs()) != 1 {
		t.Fatalf("busy=%d connects=%d", len(s.busy), len(h.dev.Configs()))
	}
}

func TestListShowsFavoritesAndDoubleTapConnects(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	s.mgr.Upsert(favorites.Favorite{Host: "other.example.com", Username: "operator"})
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.reload(t)
	if len(s.items) != 2 || s.selKey != "" || len(s.list.Objects) != 2 || !s.editBtn.Disabled() || !s.delBtn.Disabled() {
		t.Fatalf("items=%d selected=%q rows=%d", len(s.items), s.selKey, len(s.list.Objects))
	}
	other := s.list.Objects[1].(*widgets.SidebarRow)
	if other.Name() != "other.example.com" || other.Host() != "other.example.com" || other.Dot() != widgets.DotUnknown {
		t.Fatalf("row %q %q", other.Name(), other.Host())
	}
	row := s.list.Objects[0].(*widgets.SidebarRow)
	if row.Name() != "Büro" || row.Host() != "device.example.com" {
		t.Fatalf("row %q %q", row.Name(), row.Host())
	}

	// Tap selects: the detail shows the favorite read only, with user and TLS visible.
	test.Tap(other)
	if s.selKey != deviceKey("other.example.com", "operator") || !other.Selected() || row.Selected() || s.mode != modeView ||
		s.editBtn.Disabled() || s.title.Text != "other.example.com" || s.values["user"].Text != "operator" ||
		s.useTLS.Checked() || !s.useTLS.Disabled() || s.values["password"].Text != "Not saved" || s.savePassword.Checked() {
		t.Fatalf("tap did not select: %q user=%q", s.selKey, s.values["user"].Text)
	}
	test.Tap(row)
	if !row.Selected() || other.Selected() || s.values["host"].Text != "device.example.com" || !s.useTLS.Checked() ||
		s.values["password"].Text != "Saved in keychain" || !s.savePassword.Checked() || s.connectBtn.Disabled() || !s.connectBtn.Visible() {
		t.Fatal("second tap did not select")
	}
	test.DoubleTap(row)
	cfg := h.waitCfg(t)
	h.waitEnded(t)
	if cfg.Host != "device.example.com" || cfg.Password != "secret-1" || !cfg.UseTLS {
		t.Fatalf("%+v", cfg)
	}
	// Connect in the header connects the selected favorite as well.
	test.Tap(other)
	s.conn.askPassword = func(_, _ string, _ bool, answer func(string, bool, bool)) { answer("secret-2", false, true) }
	test.Tap(s.connectBtn)
	cfg = h.waitCfg(t)
	h.waitEnded(t)
	if cfg.Host != "other.example.com" || cfg.Username != "operator" || cfg.UseTLS {
		t.Fatalf("%+v", cfg)
	}
	// Reload keeps the selection while the favorite exists.
	h.reload(t)
	if s.selKey != deviceKey("other.example.com", "operator") || !s.rows[s.selKey].Selected() {
		t.Fatal("reload lost the selection")
	}
}

func TestStartSelectsFirstFavorite(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	keys := keychain.NewMemory()
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	mgr.Upsert(favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	s := newStartWindow(a, mgr, keys, session.ConnectFake(&session.Fake{FP: fp}), func(s *StartWindow) {
		s.prober.Stop()
		s.lookupPassword = func(_, _ string, answer func(bool)) { answer(false) }
	})
	t.Cleanup(func() { s.win.Close() })
	if s.mode != modeView || s.selKey != deviceKey("device.example.com", "admin") || s.title.Text != "Büro" {
		t.Fatalf("mode=%v sel=%q", s.mode, s.selKey)
	}
}

// editFavorite selects f's row and switches to edit mode through the toolbar.
func editFavorite(t *testing.T, h *startHarness, host, user string) {
	t.Helper()
	test.Tap(h.s.rows[deviceKey(host, user)])
	test.Tap(h.s.editBtn)
	if h.s.mode != modeEdit {
		t.Fatal("Edit did not switch to edit mode")
	}
}

func TestRenameEditDelete(t *testing.T) {
	h := newStart(t)
	s := h.s
	f := favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true}
	s.mgr.Upsert(f)
	h.keys.Set("device.example.com", "admin", "secret-1")
	h.reload(t)

	// Rename: the name field of edit mode.
	editFavorite(t, h, "device.example.com", "admin")
	if s.name.Text != "Büro" || s.host.Text != "device.example.com" || !s.editSaveBtn.Visible() || s.connectBtn.Visible() || !s.editBtn.Visible() {
		t.Fatal("edit mode not filled")
	}
	s.name.SetText(" Keller ")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].Name != "Keller" || s.mode != modeView || s.title.Text != "Keller" {
		t.Fatalf("%+v", s.items)
	}
	// Cancel discards the changes.
	test.Tap(s.editBtn)
	s.name.SetText("Discarded")
	test.Tap(s.editCancelBtn)
	if s.mode != modeView || s.title.Text != "Keller" || len(h.reloaded) != 0 {
		t.Fatal("cancel saved")
	}
	// Host and TLS: the password moves to the new key, the new favorite stays selected.
	editFavorite(t, h, "device.example.com", "admin")
	s.host.SetText(" New.Example.com ")
	test.Tap(s.useTLS)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].Host != "new.example.com" || s.items[0].UseTLS || s.selKey != deviceKey("new.example.com", "admin") {
		t.Fatalf("%+v", s.items)
	}
	if pw, _ := h.keys.Get("new.example.com", "admin"); pw != "secret-1" {
		t.Fatal("password not moved")
	}
	// Invalid input is rejected and edit mode stays open.
	editFavorite(t, h, "new.example.com", "admin")
	s.host.SetText("")
	test.Tap(s.editSaveBtn)
	if err := <-h.errs; !strings.Contains(err.Error(), "Host is required") {
		t.Fatalf("err = %v", err)
	}
	s.host.SetText("new.example.com")
	s.user.SetText(" ")
	s.password.OnSubmitted("") // Enter in the password field saves in edit mode
	if err := <-h.errs; !strings.Contains(err.Error(), "Username is required") {
		t.Fatalf("err = %v", err)
	}
	if s.items[0].Host != "new.example.com" || len(h.reloaded) != 0 || s.mode != modeEdit {
		t.Fatal("invalid edit applied")
	}
	s.remove(s.items[0], true)
	h.waitReload(t)
	if len(s.items) != 0 || s.mode != modeNew || s.selKey != "" {
		t.Fatalf("%+v", s.items)
	}
	if _, err := h.keys.Get("new.example.com", "admin"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("password kept")
	}
}

func TestEditErrorIsReportedAndListReloaded(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.mgr.Upsert(favorites.Favorite{Host: "device.example.com", Username: "admin"})
	s.mgr.Upsert(favorites.Favorite{Host: "other.example.com", Username: "admin"})
	h.reload(t)
	editFavorite(t, h, "other.example.com", "admin")
	s.host.SetText("device.example.com")
	test.Tap(s.editSaveBtn)
	if err := <-h.errs; !errors.Is(err, favorites.ErrExists) {
		t.Fatalf("err = %v", err)
	}
	h.waitReload(t)
	if len(s.items) != 2 || s.mode != modeEdit || s.host.Text != "device.example.com" {
		t.Fatalf("%+v mode=%v", s.items, s.mode)
	}
}

// A quality chosen in the device window reaches the manager: the read-only view shows it at
// once, and saving edit mode without touching the quality keeps it, also when edit mode was
// already open while the quality changed.
func TestEditKeepsQualityChosenInDeviceWindow(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp})
	saved := make(chan struct{}, 4)
	d.onQualitySaved = func() { saved <- struct{}{} }
	setQuality := func(q kvm.Quality) {
		t.Helper()
		d.setQuality(q)
		recv(t, saved)
		select { // the start window reloads after the save (before the fix it did not)
		case <-h.reloaded:
		case <-time.After(500 * time.Millisecond):
		}
	}

	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	setQuality(kvm.QualityLow)
	if s.values["quality"].Text != "Low" {
		t.Fatalf("view shows %q", s.values["quality"].Text)
	}
	editFavorite(t, h, "device.example.com", "admin")
	s.name.SetText("Keller")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if f := s.items[0]; f.Name != "Keller" || f.QualityChoice() != favorites.QualityLow {
		t.Fatalf("%+v", f)
	}

	// Edit mode open while the device window switches back to High.
	editFavorite(t, h, "device.example.com", "admin")
	setQuality(kvm.QualityHigh)
	s.name.SetText("Lab")
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if f := s.items[0]; f.Name != "Lab" || f.QualityChoice() != favorites.QualityHigh || s.values["quality"].Text != "High" {
		t.Fatalf("%+v", f)
	}
	// A quality changed in edit mode is still written.
	editFavorite(t, h, "device.example.com", "admin")
	s.qualitySel.SetSelectedIndex(2)
	test.Tap(s.editSaveBtn)
	h.waitReload(t)
	if s.items[0].QualityChoice() != favorites.QualityLow {
		t.Fatalf("%+v", s.items[0])
	}
	d.poller.Stop()
	d.win.Close()
}

// openFakeDevice opens a device window through the real wiring with a ready session.
func openFakeDevice(t *testing.T, h *startHarness, host string, dev *session.Fake) *DeviceWindow {
	t.Helper()
	cfg := amt.Config{Host: host, Username: "admin", UseTLS: true, Fingerprint: dev.FP}
	sess, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	// addDevice instead of showDevice: a running poller would render on its own goroutine,
	// which the test driver (fyne.Do runs inline) cannot serialize with this test.
	d := h.s.addDevice(sess, st, host)
	if h.s.open[deviceKey(host, "admin")] != d {
		t.Fatal("device window not registered")
	}
	return d
}

func TestOpenDeviceWindowsAreRegisteredAndShareTheInterval(t *testing.T) {
	h := newStart(t)
	s := h.s
	dev := &session.Fake{FP: fp}
	d1 := openFakeDevice(t, h, "one.example.com", dev)
	d2 := openFakeDevice(t, h, "two.example.com", dev)
	if len(s.open) != 2 || d1.poller.Interval() != 10*time.Second {
		t.Fatalf("open=%d interval=%v", len(s.open), d1.poller.Interval())
	}
	if err := s.settings.SetPollInterval(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	if d1.poller.Interval() != 30*time.Second || d2.poller.Interval() != 30*time.Second || s.settings.PollInterval() != 30*time.Second {
		t.Fatalf("%v %v", d1.poller.Interval(), d2.poller.Interval())
	}
	if err := s.settings.SetPollInterval(0); !errors.Is(err, ErrInterval) {
		t.Fatalf("err = %v", err)
	}
	// Connecting to an open device focuses its window instead of connecting again.
	s.begin(connectRequest{cfg: amt.Config{Host: "one.example.com", Username: "admin", Password: "x", UseTLS: true}})
	if len(s.busy) != 0 || len(dev.Configs()) != 2 {
		t.Fatal("second connection to an open device started")
	}
	d1.win.Close()
	d1.poller.Stop()
	if _, still := s.open[deviceKey("one.example.com", "admin")]; still || len(s.open) != 1 {
		t.Fatal("closed window still registered")
	}
}

func TestAbortedWindowLeavesRegistry(t *testing.T) {
	h := newStart(t)
	s := h.s
	dev := &session.Fake{FP: fp}
	d := openFakeDevice(t, h, "device.example.com", dev)
	d.onError = func(err error) { h.errs <- err }
	mm := &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)}
	d.apply(amt.Status{}, mm)
	<-h.errs
	if _, still := s.open[deviceKey("device.example.com", "admin")]; still {
		t.Fatal("aborted window still registered")
	}
	// After "Reset fingerprint" the user may connect again: a new attempt starts.
	s.begin(connectRequest{cfg: amt.Config{Host: "device.example.com", Username: "admin", Password: "x", UseTLS: true}})
	h.waitCfg(t)
	h.waitEnded(t)
	if len(h.dev.Configs()) != 1 { // the start window's connect function, not the first session's fake
		t.Fatalf("%d connects", len(h.dev.Configs()))
	}
	d.poller.Stop()
	d.win.Close()
}

func TestStartWindowOpensDeviceWindow(t *testing.T) {
	h := newStart(t)
	s := h.s
	s.openDevice = func(sess *session.Session, st amt.Status, name string) { s.addDevice(sess, st, name) } // registry without poller
	before := len(s.app.Driver().AllWindows())
	s.host.SetText("device.example.com")
	s.password.SetText("secret-1")
	test.Tap(s.connectBtn)
	h.waitEnded(t)
	d, ok := s.open[deviceKey("device.example.com", "admin")]
	if !ok || d.win.Title() != "device.example.com" || len(s.app.Driver().AllWindows()) != before+1 {
		t.Fatalf("device window missing: %v", s.open)
	}
}

func TestLateCloseOfOldWindowKeepsNewRegistration(t *testing.T) {
	h := newStart(t)
	s := h.s
	dev := &session.Fake{FP: fp}
	d1 := openFakeDevice(t, h, "device.example.com", dev)
	k := deviceKey("device.example.com", "admin")
	d1.release() // aborted: entry freed
	if _, still := s.open[k]; still {
		t.Fatal("entry not freed")
	}
	cfg := amt.Config{Host: "device.example.com", Username: "admin", UseTLS: true, Fingerprint: dev.FP}
	sess, st, err := session.Open(context.Background(), cfg, nil, session.ConnectFake(dev))
	if err != nil {
		t.Fatal(err)
	}
	d2 := s.addDevice(sess, st, "device.example.com")
	d1.onClosed() // the old window's callback fires late
	if s.open[k] != d2 {
		t.Fatal("closing the old window removed the new registration")
	}
	d1.poller.Stop()
	d2.poller.Stop()
}

func TestQuitClosesDeviceWindows(t *testing.T) {
	h := newStart(t)
	dev := &session.Fake{FP: fp}
	d1 := openFakeDevice(t, h, "one.example.com", dev)
	d2 := openFakeDevice(t, h, "two.example.com", dev)
	h.s.closeDevices() // what the start window's onClosed runs before the app quits
	if !d1.closed || !d2.closed || len(h.s.open) != 0 {
		t.Fatalf("closed=%v,%v open=%d", d1.closed, d2.closed, len(h.s.open))
	}
	waitStopped(t, d1)
	waitStopped(t, d2)
}

func TestClosingTheWindowStopsTheProbe(t *testing.T) {
	englishUI(t)
	a := test.NewTempApp(t)
	keys := keychain.NewMemory()
	mgr := favorites.NewManager(favorites.NewStore(filepath.Join(t.TempDir(), "favorites.json")), keys)
	s := NewStartWindow(a, mgr, keys, session.ConnectFake(&session.Fake{FP: fp}))
	s.win.Close()
	select {
	case <-s.prober.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("background probe still running after the window closed")
	}
}

// Issue #56: for a favorite with an open device window the background probe builds no
// session; the dot follows the window's poller. After the window is closed the probe takes
// over again.
func TestOpenDeviceWindowReplacesBackgroundProbe(t *testing.T) {
	h := newStart(t)
	s := h.s
	type report struct {
		key string
		r   probeResult
	}
	got := make(chan report, 8)
	// A fresh prober wired to the start window's connect function; round runs synchronously,
	// so the connects of h.dev show exactly what one round dialed.
	s.prober = newStatusProber(h.keys, s.conn.connect, func() time.Duration { return time.Hour }, func(key string, r probeResult) { got <- report{key, r} })
	t.Cleanup(func() { s.prober.Stop(); <-s.prober.Done() })
	for _, host := range []string{"open.example.com", "other.example.com"} {
		h.keys.Set(host, "admin", "secret-1")
		h.keys.SetFingerprint(host, "admin", fp)
	}
	addFavorites(t, h,
		favorites.Favorite{Host: "open.example.com", Username: "admin", UseTLS: true},
		favorites.Favorite{Host: "other.example.com", Username: "admin", UseTLS: true})
	kOpen := deviceKey("open.example.com", "admin")
	round := func() map[string]probeResult {
		s.prober.round(s.items)
		out := map[string]probeResult{}
		for len(got) > 0 {
			r := <-got
			out[r.key] = r.r
			s.applyProbe(r.key, r.r)
		}
		return out
	}

	d := openFakeDevice(t, h, "open.example.com", &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}})
	if s.rows[kOpen].Dot() != widgets.DotOn {
		t.Fatalf("dot after connect = %v", s.rows[kOpen].Dot())
	}
	r := round()
	if _, ok := r[deviceKey("other.example.com", "admin")]; !ok || len(r) != 1 {
		t.Fatalf("round with open window reported %v", r)
	}
	for _, c := range h.dev.Configs() {
		if c.Host == "open.example.com" {
			t.Fatal("background session to a device with an open window")
		}
	}
	if len(h.dev.Configs()) != 1 {
		t.Fatalf("%d connects", len(h.dev.Configs()))
	}

	// The dot follows the window's poller, and a late probe result does not override it.
	d.apply(amt.Status{PowerState: models.PowerStateOffSoft}, nil)
	if s.rows[kOpen].Dot() != widgets.DotOff {
		t.Fatal("dot does not follow the poller (off)")
	}
	d.apply(amt.Status{PowerState: models.PowerStateHibernate}, nil)
	if s.rows[kOpen].Dot() != widgets.DotAsleep || d.headDot.State() != widgets.DotAsleep {
		t.Fatal("dot does not follow the poller (hibernate)")
	}
	d.apply(amt.Status{}, errors.New("timeout"))
	if s.rows[kOpen].Dot() != widgets.DotUnknown {
		t.Fatal("dot does not follow the poller (read failed)")
	}
	d.apply(amt.Status{PowerState: models.PowerStateOn, AMTVersion: "16.1.25", ControlMode: amt.ControlModeAdmin}, nil)
	s.applyProbe(kOpen, readProbe(amt.Status{PowerState: models.PowerStateOffSoft}))
	if s.rows[kOpen].Dot() != widgets.DotOn || s.probes[kOpen].st.AMTVersion != "16.1.25" {
		t.Fatal("late probe result overrode the window's status")
	}

	// Closed: the probe reads the device again, and a late read of the old window is ignored.
	d.win.Close()
	d.poller.Stop()
	r = round()
	if res, probed := r[kOpen]; !probed || !res.known() || res.dot != widgets.DotUnknown { // h.dev reports power state 0 (no name)
		t.Fatalf("probe did not take over after close: %v", r)
	}
	if s.rows[kOpen].Dot() != widgets.DotUnknown {
		t.Fatal("dot not taken over by the probe")
	}
	d.apply(amt.Status{PowerState: models.PowerStateOn}, nil)
	if s.rows[kOpen].Dot() != widgets.DotUnknown {
		t.Fatal("late read of a closed window changed the dot")
	}
}

// A certificate mismatch ends the window: its last read no longer counts, the dot is neutral
// until the probe (with its own pin check) reads the device again.
func TestAbortedWindowClearsDot(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Host: "device.example.com", Username: "admin", UseTLS: true})
	k := deviceKey("device.example.com", "admin")
	d := openFakeDevice(t, h, "device.example.com", &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}})
	d.onError = func(error) {}
	if s.rows[k].Dot() != widgets.DotOn {
		t.Fatal("dot after connect")
	}
	d.apply(amt.Status{}, &amt.FingerprintMismatchError{Host: "device.example.com", Expected: fp, Actual: strings.Repeat("cd", 32)})
	if s.rows[k].Dot() != widgets.DotUnknown {
		t.Fatalf("dot after abort = %v", s.rows[k].Dot())
	}
	if s.prober.skipped(k) {
		t.Fatal("probe still skips the aborted window")
	}
	d.poller.Stop()
	d.win.Close()
}

// Closing a device window starts a probe round right away, so the window's last read does not
// stay up for a whole interval (a favorite without TLS turns neutral only through the probe).
func TestClosingDeviceWindowWakesProbe(t *testing.T) {
	h := newStart(t)
	s := h.s
	got := make(chan probeResult, 4)
	s.prober = newStatusProber(h.keys, s.conn.connect, func() time.Duration { return time.Hour }, func(_ string, r probeResult) { got <- r })
	t.Cleanup(func() { s.prober.Stop(); <-s.prober.Done() })
	addFavorites(t, h, favorites.Favorite{Host: "plain.example.com", Username: "admin"})
	k := deviceKey("plain.example.com", "admin")
	d := openFakeDevice(t, h, "plain.example.com", &session.Fake{FP: fp, St: amt.Status{PowerState: models.PowerStateOn}})
	if s.rows[k].Dot() != widgets.DotOn {
		t.Fatal("dot after connect")
	}
	select { // drop the wake of the favorites reload
	case <-s.prober.wake:
	default:
	}
	d.win.Close()
	d.poller.Stop()
	select {
	case <-s.prober.wake:
	default:
		t.Fatal("closing the window did not wake the probe")
	}
	// The round the wake starts turns the dot neutral: no TLS, no login.
	s.prober.round(s.items)
	s.applyProbe(k, <-got)
	if s.rows[k].Dot() != widgets.DotUnknown || len(h.dev.Configs()) != 0 {
		t.Fatalf("dot %v, %d connects", s.rows[k].Dot(), len(h.dev.Configs()))
	}
}
