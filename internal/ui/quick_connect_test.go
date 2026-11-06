package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/ui/style"
)

func TestQuickConnectTitleMirrorsFormLive(t *testing.T) {
	h := newStart(t)
	s := h.s
	test.Tap(s.newConnBtn)
	if s.title.Text != "Quick Connect" {
		t.Fatalf("title %q", s.title.Text)
	}
	s.host.SetText("device.example.com")
	if s.title.Text != "device.example.com" {
		t.Fatalf("host title %q", s.title.Text)
	}
	s.name.SetText("Büro")
	if s.title.Text != "Büro" {
		t.Fatalf("name title %q", s.title.Text)
	}
	s.name.SetText("")
	s.host.SetText("")
	if s.title.Text != "Quick Connect" {
		t.Fatalf("empty title %q", s.title.Text)
	}

	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.editBtn)
	s.name.SetText("Lager")
	if s.title.Text != "Lager" {
		t.Fatalf("edit title %q", s.title.Text)
	}
	test.Tap(s.editCancelBtn)
	if s.title.Text != "Büro" {
		t.Fatalf("view title %q", s.title.Text)
	}
}

func TestCopyCancelReturnsToOriginal(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	key := deviceKey("device.example.com", "admin")
	test.Tap(s.rows[key])
	test.Tap(s.dupBtn)
	test.Tap(s.editCancelBtn)
	if s.mode != modeView || s.selKey != key || s.dupOf != nil || s.title.Text != "Büro" || !s.connectBtn.Visible() {
		t.Fatalf("mode=%v sel=%q title=%q", s.mode, s.selKey, s.title.Text)
	}
	if len(s.items) != 1 {
		t.Fatal("cancel saved a copy")
	}
}

// Enter in the password field of a copy saves it (like Save), and does not connect.
func TestCopyEnterInPasswordFieldSaves(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.dupBtn)
	s.host.SetText("copy.example.com")
	s.password.OnSubmitted("")
	h.waitReload(t)
	if len(s.items) != 2 || len(s.busy) != 0 || len(h.dev.Configs()) != 0 {
		t.Fatalf("items=%d busy=%d connects=%d", len(s.items), len(s.busy), len(h.dev.Configs()))
	}
}

func TestPasswordEyeHiddenOnlyWhileKeychainPlaceholder(t *testing.T) {
	h := newStart(t)
	s := h.s
	addFavorites(t, h, favorites.Favorite{Name: "Büro", Host: "device.example.com", Username: "admin", UseTLS: true})
	h.keys.Set("device.example.com", "admin", "secret-1")
	test.Tap(s.newConnBtn)
	if !s.passwordEye.Visible() {
		t.Fatal("eye hidden in an empty Quick Connect form")
	}
	test.Tap(s.rows[deviceKey("device.example.com", "admin")])
	test.Tap(s.editBtn)
	if s.password.PlaceHolder == "" || s.password.Text != "" {
		t.Fatalf("placeholder %q text %q", s.password.PlaceHolder, s.password.Text)
	}
	if s.passwordEye.Visible() {
		t.Fatal("eye visible with empty field and password in keychain")
	}
	s.password.SetText("x")
	if !s.passwordEye.Visible() {
		t.Fatal("eye hidden with typed password")
	}
	s.password.SetText("")
	if s.passwordEye.Visible() || !s.password.Password {
		t.Fatal("eye not hidden again")
	}
	// Not in the keychain: the eye stays even when the field is empty.
	test.Tap(s.editCancelBtn)
	test.Tap(s.newConnBtn)
	if !s.passwordEye.Visible() {
		t.Fatal("eye hidden without keychain password")
	}
}

// TestQuickConnectShots renders the Quick Connect states into $OPENLAMT_SHOTS. Made-up data.
func TestQuickConnectShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newStart(t)
	s := h.s
	s.app.Settings().SetTheme(style.Theme{})
	shot := func(name string) {
		t.Helper()
		c := software.NewCanvas()
		c.SetPadded(false)
		c.SetScale(shotScale())
		c.SetContent(s.win.Content())
		c.Resize(fyne.NewSize(1000, 612))
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, c.Capture()); err != nil {
			t.Fatal(err)
		}
	}
	addFavorites(t, h, favorites.Favorite{Name: "Lab-Server", Host: "192.0.2.10", Username: "admin", UseTLS: true})
	h.keys.Set("192.0.2.10", "admin", "secret-1")
	test.Tap(s.newConnBtn)
	shot("quick-connect-empty.png")
	s.name.SetText("Test-Rechner")
	s.host.SetText("192.0.2.30")
	s.password.SetText("secret-2")
	shot("quick-connect-named.png")
	test.Tap(s.rows[deviceKey("192.0.2.10", "admin")])
	test.Tap(s.dupBtn)
	shot("copy.png")
	test.Tap(s.editCancelBtn)
	test.Tap(s.editBtn)
	shot("edit.png")
}
