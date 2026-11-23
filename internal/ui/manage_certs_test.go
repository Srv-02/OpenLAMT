package ui

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"image/png"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/certfile"
	"github.com/Srv-02/openlamt/internal/session"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// testCertificate is a made-up certificate with its key.
type testCertificate struct {
	der []byte
	key *rsa.PrivateKey
}

func newTestCertificate(t *testing.T, cn, org string, serial int64, from, until time.Time, ca bool) testCertificate {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn, Organization: []string{org}},
		NotBefore: from, NotAfter: until, IsCA: ca, BasicConstraintsValid: ca}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return testCertificate{der: der, key: k}
}

// storeEntry is c as the store lists it.
func (c testCertificate) storeEntry(id string) amt.Certificate {
	x, _ := x509.ParseCertificate(c.der)
	sum := sha256.Sum256(c.der)
	return amt.Certificate{ID: id, Subject: "C=AT,O=" + x.Subject.Organization[0] + ",CN=" + x.Subject.CommonName,
		Issuer: "CN=" + x.Issuer.CommonName, NotBefore: x.NotBefore, NotAfter: x.NotAfter,
		Fingerprint: hex.EncodeToString(sum[:]), DER: c.der}
}

func onDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// exampleStore is the store of the design: the TLS certificate with its key, two trusted roots,
// an expired certificate and a free key pair.
func exampleStore(t *testing.T) (*session.Fake, testCertificate) {
	t.Helper()
	tlsCert := newTestCertificate(t, "Lab-Server TLS", "ACME Lab", 0x0A3F91225C, onDay(2026, 11, 10), onDay(2036, 11, 10), false)
	root := newTestCertificate(t, "ACME Lab Root CA", "ACME Lab", 2, onDay(2025, 1, 1), onDay(2035, 1, 1), true)
	old := newTestCertificate(t, "old.example.com", "ACME Lab", 3, onDay(2024, 3, 1), onDay(2025, 3, 1), false)
	a := tlsCert.storeEntry("cert-tls")
	a.HasPrivateKey, a.TLS = true, true
	r := root.storeEntry("cert-root")
	r.TrustedRoot = true
	dev := &session.Fake{FP: fp, Cfg: session.FakeConfig{Certificates: []amt.Certificate{a, r, old.storeEntry("cert-old")}}}
	dev.Certs().Keys = []amt.KeyPair{
		{ID: "key-tls", Algorithm: "RSA", Bits: 2048, Fingerprint: "4f2a" + strings.Repeat("00", 28) + "9c", Certificate: "cert-tls"},
		{ID: "key-free", Algorithm: "RSA", Bits: 2048, Fingerprint: "7be1" + strings.Repeat("00", 28) + "04"},
	}
	return dev, tlsCert
}

// certsHarness opens the Certificates page in the sidebar through the More menu.
func certsHarness(t *testing.T, dev *session.Fake, useTLS bool) (*manageHarness, *certsPane) {
	t.Helper()
	englishUI(t)
	h := newManageHarnessTLS(t, dev, useTLS)
	h.m.parent.SetContent(h.m.Sidebar())
	h.m.parent.Resize(fyne.NewSize(1000, 800))
	h.wait(t) // hardware
	h.m.side.openMore()
	var item *fyne.MenuItem
	for _, it := range h.m.side.moreMenu.Items {
		if it.Label == "Certificates" {
			item = it
		}
	}
	if item == nil {
		t.Fatal("no Certificates in the More menu")
	}
	h.m.side.dismissMore()
	item.Action()
	h.wait(t) // store
	i := h.m.side.view.current
	if h.m.side.tabs.Label(h.m.side.more) != "Certs" {
		t.Errorf("More tab label %q", h.m.side.tabs.Label(h.m.side.more))
	}
	return h, h.m.side.view.panes[i].impl.(*certsPane)
}

// cellText is the text of cell c of line l.
func cellText(l *listLine, c int) string { return richText(l.cells[c]) }

// pillText is the text of the pill in cell c of line l, "" for none.
func pillText(l *listLine, c int) (string, pillTone) {
	if p, ok := l.object(c).(*statusPill); ok {
		return p.text, p.tone
	}
	return "", 0
}

// writeDone applies the result of a write and the read after it.
func writeDone(t *testing.T, h *manageHarness) {
	t.Helper()
	h.wait(t)
	h.wait(t)
}

func TestCertificatesPageIsRegisteredInMore(t *testing.T) {
	englishUI(t)
	m := &manager{}
	m.pages = managePages(m)
	i := slices.IndexFunc(m.pages, func(p managePage) bool { return p.title == "Certificates" })
	if i < 0 || m.pages[i].section != sectionMore || m.pages[i].icon != icons.Certificate || m.pages[i].tab != "Certs" {
		t.Fatalf("pages %v", pageTitles(m))
	}
	for _, d := range managePageDefs {
		if _, title := d.names(); title == "Certificates" && d.order != 40 {
			t.Errorf("order %d, want 40", d.order)
		}
	}
}

func TestCertificatesTable(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	if h.m.certs.reads != 1 || len(p.list.table.lines) != 3 || len(p.keys.lines) != 2 {
		t.Fatalf("reads %d, %d certificates, %d keys", h.m.certs.reads, len(p.list.table.lines), len(p.keys.lines))
	}
	want := [][3]string{
		{"Lab-Server TLS", "Certificate with key", "Nov 10, 2036"},
		{"ACME Lab Root CA", "Trusted root", "Jan 01, 2035"},
		{"old.example.com", "Certificate", ""},
	}
	for i, w := range want {
		l := p.list.table.lines[i]
		if got := [3]string{cellText(l, 0), cellText(l, 1), cellText(l, 2)}; got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
		if l.onTap == nil || l.trail == nil {
			t.Errorf("row %d: no tap or chevron", i)
		}
	}
	if s, tone := pillText(p.list.table.lines[0], 3); s != "TLS" || tone != pillOK {
		t.Errorf("TLS pill %q %v", s, tone)
	}
	if s, _ := pillText(p.list.table.lines[1], 3); s != "" {
		t.Errorf("trusted root used for %q", s)
	}
	if s, tone := pillText(p.list.table.lines[2], 2); s != "Expired Mar 2025" || tone != pillBad {
		t.Errorf("expired pill %q %v", s, tone)
	}
	k0, k1 := p.keys.lines[0], p.keys.lines[1]
	if cellText(k0, 0) != "RSA 2048 · 4F:2A:…:9C" || cellText(k0, 1) != "Lab-Server TLS" || !k0.cells[0].Segments[0].(*widget.TextSegment).Style.TextStyle.Monospace {
		t.Errorf("key row %q | %q", cellText(k0, 0), cellText(k0, 1))
	}
	if cellText(k1, 1) != "none" || k1.action.Disabled() || !k0.action.Disabled() || k0.action.Text != "Delete…" {
		t.Errorf("free key %q, Delete… disabled %v / %v", cellText(k1, 1), k0.action.Disabled(), k1.action.Disabled())
	}
	if got := richText(p.footnote); got != "Click a certificate for details. Choose the TLS certificate in Settings › Clock and TLS." || p.footnote.Wrapping != fyne.TextWrapWord {
		t.Errorf("footnote %q", got)
	}
	if p.importB.Text != "Import…" || p.list.refresh == nil {
		t.Error("Import… and Refresh in the head")
	}
}

func TestCertificatesWindowCountAndRefresh(t *testing.T) {
	dev, _ := exampleStore(t)
	h, _ := certsHarness(t, dev, true)
	h.m.OpenWindow()
	pane := h.m.win.view.pane()
	w := pane.impl.(*certsPane)
	if w.count == nil || w.count.Text != "· 3 certificates" || pane.info != w.count {
		t.Fatalf("count %v", w.count)
	}
	if certificatesText(1) != "1 certificate" {
		t.Error("singular")
	}
	test.Tap(w.list.refresh)
	h.wait(t)
	if h.m.certs.reads != 2 {
		t.Fatalf("reads %d", h.m.certs.reads)
	}
}

func TestCertificatesReadError(t *testing.T) {
	dev, _ := exampleStore(t)
	dev.Certs().ReadErr = errors.New("example failure")
	_, p := certsHarness(t, dev, true)
	if !p.list.err.box.Visible() || p.keys.Visible() {
		t.Errorf("error box %v, keys %v", p.list.err.box.Visible(), p.keys.Visible())
	}
}

func TestCertificateRowOpensDetails(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	p.list.table.lines[0].onTap()
	d := p.details
	if d == nil || !open(h.m.parent) {
		t.Fatal("no details")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Lab-Server TLS")
	wantLabels := []string{"Subject", "Issuer", "Valid", "Serial", "SHA-256", "Key", "Used for"}
	if !slices.Equal(d.labels, wantLabels) {
		t.Fatalf("labels %v", d.labels)
	}
	if d.values[0] != "CN=Lab-Server TLS, O=ACME Lab" || d.values[2] != "Nov 10, 2026 to Nov 10, 2036" || d.values[3] != "0A:3F:91:22:5C" ||
		d.values[5] != "RSA 1024, private key on the device" || d.values[6] != "TLS" || len(strings.Split(d.values[4], ":")) != 32 {
		t.Errorf("values %q", d.values)
	}
	if d.panel.frame.MinSize().Width != 560 {
		t.Errorf("width %v", d.panel.frame.MinSize().Width)
	}
	if d.del.Text != "Delete Certificate…" || d.del.Importance != widget.DangerImportance || d.save.Text != "Save as File…" || d.action.Text != "Close" {
		t.Error("buttons")
	}
	if len(d.panel.leading) != 1 || len(d.panel.extras) != 1 {
		t.Error("Delete Certificate… at the left, Save as File… left of Close")
	}
	test.Tap(d.action)
	if open(h.m.parent) {
		t.Fatal("Close")
	}
	// a trusted root without key: no "Used for", no private key
	p.list.table.lines[1].onTap()
	if slices.Contains(p.details.labels, "Used for") || p.details.values[5] != "RSA 1024" {
		t.Errorf("root %v %q", p.details.labels, p.details.values)
	}
}

func TestDetailRowsTapThroughTheTable(t *testing.T) {
	dev, _ := exampleStore(t)
	_, p := certsHarness(t, dev, true)
	l := p.list.table.lines[2]
	p.list.table.Tapped(&fyne.PointEvent{Position: fyne.NewPos(10, l.y+l.h/2)})
	if p.details == nil || p.details.cert.ID != "cert-old" {
		t.Fatalf("tap on the third row opened %+v", p.details)
	}
}

func TestSaveCertificateAsPEM(t *testing.T) {
	dev, tlsCert := exampleStore(t)
	_, p := certsHarness(t, dev, true)
	path := filepath.Join(t.TempDir(), "out.pem")
	var name string
	p.saveFile = func(_ fyne.Window, n string, chosen func(string)) { name = n; chosen(path) }
	p.list.table.lines[0].onTap()
	test.Tap(p.details.save)
	if name != "Lab-Server_TLS.pem" {
		t.Errorf("suggested %q", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" || string(blk.Bytes) != string(tlsCert.der) {
		t.Fatal("not the certificate as PEM")
	}
	if strings.Contains(string(b), "PRIVATE") {
		t.Fatal("a private key in the file")
	}
	if pemFileName(amt.Certificate{Subject: "CN=../..", Fingerprint: ""}) != "certificate.pem" {
		t.Error("a name of only dots")
	}
}

func TestDeleteCertificateSimpleConfirmation(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	p.list.table.lines[2].onTap()
	test.Tap(p.details.del)
	c := p.confirm
	if c == nil || p.rootDlg != nil {
		t.Fatal("no simple confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete “old.example.com”?")
	findLabel(t, top, "The certificate is removed from Lab. This cannot be undone.")
	if c.spec.icon != icons.Delete || !c.spec.danger || c.action.Text != "Delete Certificate" || c.cancel == nil {
		t.Errorf("simple level: tile %q danger %v button %q", c.spec.icon, c.spec.danger, c.action.Text)
	}
	test.Tap(c.action)
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"DeleteCertificate"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.list.table.lines) != 2 {
		t.Errorf("not reloaded: %d rows", len(p.list.table.lines))
	}
}

func TestDeleteCertificateCancel(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	p.list.table.lines[2].onTap()
	test.Tap(p.details.del)
	test.Tap(p.confirm.cancel)
	if open(h.m.parent) || dev.ConfigWrites() != nil {
		t.Fatalf("cancel: open %v writes %v", open(h.m.parent), dev.ConfigWrites())
	}
}

func TestDeleteTrustedRootNeedsTheOrangeConfirmation(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	p.list.table.lines[1].onTap()
	test.Tap(p.details.del)
	d := p.rootDlg
	if d == nil || p.confirm != nil {
		t.Fatal("no orange confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete “ACME Lab Root CA”?")
	if !hasRichText(t, top, "Lab no longer trusts servers signed by this root. HTTPS boot from such servers stops working.", false) ||
		d.warnBox == nil || d.check.Text != "I understand the risk for Lab (device.example.com)" || d.action.Text != "Delete Certificate" {
		t.Errorf("warning box %v, checkbox %q, button %q", d.warnBox != nil, d.check.Text, d.action.Text)
	}
	if !d.action.Disabled() {
		t.Fatal("enabled before the box is ticked")
	}
	test.Tap(d.check)
	test.Tap(d.action)
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"DeleteCertificate"}) {
		t.Fatalf("writes %v", got)
	}
}

func TestDeleteTLSCertificateShowsInUse(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	p.list.table.lines[0].onTap()
	test.Tap(p.details.del)
	test.Tap(p.confirm.action)
	writeDone(t, h)
	q := p.inUse
	if q == nil || dev.ConfigWrites() != nil {
		t.Fatalf("no in-use info, writes %v", dev.ConfigWrites())
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Certificate in use")
	findLabel(t, top, "Lab uses “Lab-Server TLS” for TLS. Choose another TLS certificate in Settings first, then delete this one.")
	if q.spec.icon != icons.Info || q.spec.tone != toneInfo || q.action.Text != "OK" || len(q.extras) != 1 {
		t.Fatal("info tile, OK and Open Settings")
	}
	test.Tap(q.extras[0].(*escButton))
	if open(h.m.parent) {
		t.Fatal("Open Settings leaves the info open")
	}
	if h.m.pages[h.m.side.view.current].icon != icons.DeviceSettings || h.m.side.tabs.Selected() != h.m.side.view.current {
		t.Fatalf("page %q", h.m.pages[h.m.side.view.current].title)
	}
}

func TestShowSettingsInTheWindow(t *testing.T) {
	dev, _ := exampleStore(t)
	h, _ := certsHarness(t, dev, true)
	h.m.OpenWindow()
	h.m.showSettings(false)
	if h.m.pages[h.m.win.view.current].icon != icons.DeviceSettings {
		t.Fatalf("window page %q", h.m.pages[h.m.win.view.current].title)
	}
}

func TestDeleteKeyPair(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	test.Tap(p.keys.lines[1].action)
	c := p.confirm
	if c == nil {
		t.Fatal("no confirmation")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Delete key “RSA 2048 · 7B:E1:…:04”?")
	findLabel(t, top, "The key pair is removed from Lab. This cannot be undone.")
	if c.spec.icon != icons.Delete || c.action.Text != "Delete Key" {
		t.Error("simple level")
	}
	test.Tap(c.action)
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"DeleteKeyPair"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.keys.lines) != 1 {
		t.Errorf("not reloaded: %d keys", len(p.keys.lines))
	}
}

func TestLinkedKeyDeleteIsGrayWithTooltip(t *testing.T) {
	dev, _ := exampleStore(t)
	_, p := certsHarness(t, dev, true)
	del := p.keys.lines[0].action
	test.Tap(del)
	if p.confirm != nil {
		t.Fatal("a disabled Delete… opened a confirmation")
	}
	if del.OnHover == nil {
		t.Fatal("no tooltip")
	}
	del.OnHover(true)
	if p.tip == nil || p.tipText != "Used by “Lab-Server TLS”. Delete the certificate first." {
		t.Fatalf("tooltip %q", p.tipText)
	}
	del.OnHover(false)
	if p.tip != nil {
		t.Fatal("tooltip stays")
	}
	if p.keys.lines[1].action.OnHover != nil {
		t.Error("a free key needs no tooltip")
	}
}

// importFile writes data to a temp file and makes it the choice of the file dialog; exts gets
// the filter the dialog was opened with.
func importFile(t *testing.T, p *certsPane, name string, data []byte, exts *[]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	p.openFile = func(_ fyne.Window, e []string, chosen func(string)) {
		if exts != nil {
			*exts = e
		}
		chosen(path)
	}
}

func TestImportTrustedRoot(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	root := newTestCertificate(t, "New Root", "ACME Lab", 9, onDay(2026, 1, 1), onDay(2030, 1, 1), true)
	var exts []string
	importFile(t, p, "new-root.pem", certfile.PEM(root.der), &exts)
	test.Tap(p.importB)
	d := p.importDlg
	if d == nil || d.kind.Selected() != 0 || d.pwBox.Visible() || !d.action.Disabled() || d.action.Text != "Import" {
		t.Fatal("start: Trusted root, no password, Import disabled")
	}
	top := h.m.parent.Canvas().Overlays().Top()
	findLabel(t, top, "Import certificate")
	if !hasRichText(t, top, ".pem, .cer or .crt. For a certificate with key: .p12 or .pfx with password.", false) {
		t.Error("hint")
	}
	test.Tap(d.choose)
	if !slices.Equal(exts, []string{".pem", ".cer", ".crt"}) || d.file.Text != "new-root.pem" || d.action.Disabled() {
		t.Fatalf("filter %v, file %q", exts, d.file.Text)
	}
	test.Tap(d.action)
	if open(h.m.parent) {
		t.Fatal("dialog stays open")
	}
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"AddTrustedRootCertificate"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.list.table.lines) != 4 || cellText(p.list.table.lines[3], 0) != "New Root" || cellText(p.list.table.lines[3], 1) != "Trusted root" {
		t.Errorf("table %d rows", len(p.list.table.lines))
	}
}

func TestImportRejectsANonCertificateAndStaysOpen(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	importFile(t, p, "notes.pem", []byte("hello"), nil)
	test.Tap(p.importB)
	test.Tap(p.importDlg.choose)
	test.Tap(p.importDlg.action)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "The file is not a certificate.")
	if dev.ConfigWrites() != nil || p.importDlg.done {
		t.Fatalf("writes %v, import dialog closed %v", dev.ConfigWrites(), p.importDlg.done)
	}
}

func TestImportStoreFullNamesTheDevice(t *testing.T) {
	dev, _ := exampleStore(t)
	dev.Certs().Full = true
	h, p := certsHarness(t, dev, true)
	root := newTestCertificate(t, "New Root", "ACME Lab", 9, onDay(2026, 1, 1), onDay(2030, 1, 1), true)
	importFile(t, p, "root.cer", root.der, nil)
	test.Tap(p.importB)
	test.Tap(p.importDlg.choose)
	test.Tap(p.importDlg.action)
	writeDone(t, h)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Lab has no space for more certificates.")
}

// pfx returns c with its key as a .p12 file.
func pfx(t *testing.T, c testCertificate, password string) []byte {
	t.Helper()
	x, _ := x509.ParseCertificate(c.der)
	b, err := pkcs12.Modern.Encode(c.key, x, nil, password)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestImportCertificateWithKey(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, true)
	c := newTestCertificate(t, "new.example.com", "ACME Lab", 11, onDay(2026, 1, 1), onDay(2030, 1, 1), false)
	var exts []string
	importFile(t, p, "new.p12", pfx(t, c, "Secret-1"), &exts)
	test.Tap(p.importB)
	d := p.importDlg
	h0 := d.pop.Size().Height
	d.kind.Tap(1)
	if !d.pwBox.Visible() || d.pop.Size().Height <= h0 {
		t.Fatal("no password field")
	}
	d.kind.Tap(0)
	if d.pwBox.Visible() || d.pop.Size().Height != h0 {
		t.Fatalf("the dialog keeps the room of the password: %v, want %v", d.pop.Size().Height, h0)
	}
	d.kind.Tap(1)
	test.Tap(d.choose)
	if !slices.Equal(exts, []string{".p12", ".pfx"}) {
		t.Fatalf("filter %v", exts)
	}
	d.pw.SetText("wrong")
	test.Tap(d.action)
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Wrong password for the .p12 file.")
	if d.done || dev.ConfigWrites() != nil {
		t.Fatal("a wrong password closed the dialog or wrote")
	}
	d.pw.SetText("Secret-1")
	test.Tap(d.action)
	if d.pw.Text() != "" || !d.done {
		t.Fatal("the password must be cleared and the dialog closed")
	}
	if h.m.tlsAsk != nil {
		t.Fatal("a TLS session needs no warning")
	}
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"AddCertificateWithKey"}) {
		t.Fatalf("writes %v", got)
	}
	if len(p.keys.lines) != 3 || cellText(p.keys.lines[2], 1) != "new.example.com" {
		t.Errorf("keys %d", len(p.keys.lines))
	}
}

func TestImportKeyWithoutTLSWarnsFirst(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, false)
	c := newTestCertificate(t, "new.example.com", "ACME Lab", 11, onDay(2026, 1, 1), onDay(2030, 1, 1), false)
	importFile(t, p, "new.pfx", pfx(t, c, "pw"), nil)

	run := func() {
		test.Tap(p.importB)
		p.importDlg.kind.Tap(1)
		test.Tap(p.importDlg.choose)
		p.importDlg.pw.SetText("pw")
		test.Tap(p.importDlg.action)
	}
	run()
	q := h.m.tlsAsk
	if q == nil {
		t.Fatal("no warning without TLS")
	}
	findLabel(t, h.m.parent.Canvas().Overlays().Top(), "Send the key without TLS?")
	q.Dismiss()
	if dev.ConfigWrites() != nil {
		t.Fatalf("Cancel wrote %v", dev.ConfigWrites())
	}
	run()
	test.Tap(h.m.tlsAsk.action) // Send Anyway
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"AddCertificateWithKey"}) {
		t.Fatalf("writes %v", got)
	}
}

func TestImportRootWithoutTLSNeedsNoWarning(t *testing.T) {
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, false)
	root := newTestCertificate(t, "New Root", "ACME Lab", 9, onDay(2026, 1, 1), onDay(2030, 1, 1), true)
	importFile(t, p, "root.crt", root.der, nil)
	test.Tap(p.importB)
	test.Tap(p.importDlg.choose)
	test.Tap(p.importDlg.action)
	if h.m.tlsAsk != nil {
		t.Fatal("a public certificate is no secret")
	}
	writeDone(t, h)
	if got := dev.ConfigWrites(); !slices.Equal(got, []string{"AddTrustedRootCertificate"}) {
		t.Fatalf("writes %v", got)
	}
}

func TestImportSwitchingKindClearsTheFile(t *testing.T) {
	dev, _ := exampleStore(t)
	_, p := certsHarness(t, dev, true)
	importFile(t, p, "a.pem", []byte("x"), nil)
	test.Tap(p.importB)
	test.Tap(p.importDlg.choose)
	p.importDlg.kind.Tap(1)
	if p.importDlg.path != "" || p.importDlg.file.Text != "" || !p.importDlg.action.Disabled() {
		t.Fatal("a .pem stays chosen for a .p12 import")
	}
}

func TestCertificateTexts(t *testing.T) {
	englishUI(t)
	if got := keyText(amt.KeyPair{Fingerprint: "ab" + strings.Repeat("00", 30) + "cd"}); got != "AB:00:…:CD" {
		t.Errorf("key without algorithm %q", got)
	}
	if got := certName(amt.Certificate{Subject: "O=ACME", Fingerprint: "ab00cd"}); got != "O=ACME" {
		t.Errorf("name without CN %q", got)
	}
	if got := serialText(nil); got != "" {
		t.Errorf("serial of nothing %q", got)
	}
	if !expired(amt.Certificate{NotAfter: onDay(2026, 1, 1)}, onDay(2026, 6, 1)) || expired(amt.Certificate{}, onDay(2026, 6, 1)) {
		t.Error("expired")
	}
	if keyDescription(amt.Certificate{HasPrivateKey: true}, nil) != "Private key on the device" {
		t.Error("key description without certificate")
	}
}

// TestCertificatesShots renders the Certificates page (sidebar and window) and its dialogs,
// light and dark, into $OPENLAMT_SHOTS for review against the design. Skipped without the
// variable. Only made-up data.
func TestCertificatesShots(t *testing.T) {
	dir := os.Getenv("OPENLAMT_SHOTS")
	if dir == "" {
		t.Skip("OPENLAMT_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(name string, w fyne.Window) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
	restoreDarkVariant(t)
	dev, _ := exampleStore(t)
	h, p := certsHarness(t, dev, false)
	switchVariant(h.a, theme.VariantLight)
	scaleWindow(h.m.parent)
	h.m.parent.Resize(fyne.NewSize(400, 800))
	shot("certs-sidebar.png", h.m.parent)
	p.keys.lines[0].action.OnHover(true)
	shot("certs-sidebar-tooltip.png", h.m.parent)
	p.keys.lines[0].action.OnHover(false)

	h.m.OpenWindow()
	w := h.m.win
	scaleWindow(w.win)
	w.win.Resize(fyne.NewSize(1120, 720))
	wp := w.view.pane().impl.(*certsPane)
	shot("certs-window.png", w.win)
	wp.list.table.lines[0].onTap()
	shot("certs-details.png", w.win)
	test.Tap(wp.details.del)
	shot("certs-delete-simple.png", w.win)
	wp.confirm.Dismiss()
	wp.list.table.lines[1].onTap()
	test.Tap(wp.details.del)
	shot("certs-delete-root.png", w.win)
	wp.rootDlg.Dismiss()
	wp.showInUse(dev.Cfg.Certificates[0])
	shot("certs-in-use.png", w.win)
	wp.inUse.Dismiss()
	test.Tap(wp.importB)
	shot("certs-import-root.png", w.win)
	wp.importDlg.kind.Tap(1)
	shot("certs-import-key.png", w.win)
	wp.importDlg.Dismiss()
	switchVariant(h.a, theme.VariantDark)
	shot("certs-window-dark.png", w.win)
}
