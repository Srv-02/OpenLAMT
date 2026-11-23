package ui

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/amt"
	"github.com/Srv-02/openlamt/internal/certfile"
	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// The dialogs of the Certificates page (gesamtdesign "Verwaltung: Certificates"): the details
// of a certificate, "Import certificate", the confirmations (simple for a certificate or key,
// orange for a trusted root) and "Certificate in use". Every write runs off the Fyne thread and
// reads the store again afterwards. A private key from a .p12 file lives only in memory until
// the device has it; its password is not kept.

// window is the window the dialogs of this view open on.
func (p *certsPane) window() fyne.Window { return p.list.window() }

// certWrite runs write off the Fyne thread, reads the store again and shows an error: the TLS
// certificate the device refuses to delete as "Certificate in use" (cert), the rest in the
// error dialog.
func (p *certsPane) certWrite(cert *amt.Certificate, write func() error) {
	manageWrite(p.m, p.m.certs, write, func(err error) {
		switch {
		case err == nil:
		case cert != nil && errors.Is(err, amt.ErrCertificateInUse):
			p.showInUse(*cert)
		default:
			showError(p.window(), p.certError(err))
		}
	}, nil)
}

// certError names the device in the errors the design words with it.
func (p *certsPane) certError(err error) error {
	if errors.Is(err, amt.ErrCertificateStoreFull) {
		return userError(lang.L("{{.Name}} has no space for more certificates.", map[string]any{"Name": p.m.name}))
	}
	return err
}

// --- details

// certDetails is the details dialog of one certificate.
type certDetails struct {
	*panel
	cert   amt.Certificate
	labels []string // the rows, in order (tests)
	values []string
	del    *escButton // "Delete Certificate…"
	save   *escButton // "Save as File…"
}

// keyDescription is the Key row: "RSA 2048, private key on the device".
func keyDescription(c amt.Certificate, x *x509.Certificate) string {
	var k string
	if x != nil {
		switch pub := x.PublicKey.(type) {
		case *rsa.PublicKey:
			k = "RSA " + strconv.Itoa(pub.N.BitLen())
		case *ecdsa.PublicKey:
			k = "ECDSA " + pub.Curve.Params().Name
		}
	}
	if c.HasPrivateKey {
		if k == "" {
			return lang.L("Private key on the device")
		}
		return lang.L("{{.Key}}, private key on the device", map[string]any{"Key": k})
	}
	return k
}

// serialText is the serial number in hex pairs: "0A:3F:91:22:5C".
func serialText(x *x509.Certificate) string {
	if x == nil || x.SerialNumber == nil {
		return ""
	}
	b := x.SerialNumber.Bytes()
	if len(b) == 0 {
		b = []byte{0}
	}
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = strings.ToUpper(strconv.FormatUint(uint64(v)|0x100, 16)[1:])
	}
	return strings.Join(parts, ":")
}

// nameText is a distinguished name as the design shows it: "CN=lab.example.com, O=ACME Lab".
func nameText(n pkix.Name) string {
	seq := n.ToRDNSequence()
	parts := make([]string, 0, len(seq))
	for i := len(seq) - 1; i >= 0; i-- {
		parts = append(parts, pkix.RDNSequence{seq[i]}.String())
	}
	return strings.Join(parts, ", ")
}

// detailRows are the rows of the details (label, value, monospace); empty values are left out.
func detailRows(c amt.Certificate) [][3]string {
	x, _ := x509.ParseCertificate(c.DER)
	subject, issuer := c.Subject, c.Issuer
	if x != nil {
		subject, issuer = nameText(x.Subject), nameText(x.Issuer)
	}
	valid := ""
	if !c.NotBefore.IsZero() && !c.NotAfter.IsZero() {
		valid = lang.L("{{.From}} to {{.Until}}", map[string]any{"From": certDate(c.NotBefore), "Until": certDate(c.NotAfter)})
	}
	used := ""
	if c.TLS {
		used = lang.L("TLS")
	}
	var out [][3]string
	for _, r := range [][3]string{
		{lang.L("Subject"), subject, ""},
		{lang.L("Issuer"), issuer, ""},
		{lang.L("Valid"), valid, ""},
		{lang.L("Serial"), serialText(x), "mono"},
		{"SHA-256", amt.FormatFingerprint(c.Fingerprint), "mono"},
		{lang.L("Key"), keyDescription(c, x), ""},
		{lang.L("Used for"), used, ""},
	} {
		if r[1] != "" {
			out = append(out, r)
		}
	}
	return out
}

// openDetails shows the details of c (560 px): the rows in a card, "Delete Certificate…" at the
// left, "Save as File…" and "Close".
func (p *certsPane) openDetails(c amt.Certificate) {
	d := &certDetails{cert: c}
	d.panel = showPanel(p.window(), panelSpec{title: certName(c), action: lang.L("Close"), flush: true, width: 560},
		func(pn *panel) []fyne.CanvasObject {
			d.panel = pn
			var rows []fyne.CanvasObject
			for i, r := range detailRows(c) {
				d.labels, d.values = append(d.labels, r[0]), append(d.values, r[1])
				if i > 0 {
					rows = append(rows, hline(style.ColorNameCardBorder))
				}
				rows = append(rows, detailRow(r[0], r[1], r[2] == "mono"))
			}
			d.del = pn.newButton(lang.L("Delete Certificate…"), func() {
				pn.Dismiss()
				p.confirmDeleteCert(c)
			})
			d.del.Importance = widget.DangerImportance
			pn.leading = []fyne.CanvasObject{container.NewThemeOverride(d.del, dangerButtonTheme{overrideTheme})}
			d.save = pn.newButton(lang.L("Save as File…"), func() { p.saveCertificate(c) })
			pn.extras = []fyne.CanvasObject{d.save}
			card := widgets.NewPanel(style.ColorNameCard, style.ColorNameCardBorder, 10,
				container.New(layout.NewCustomPaddedVBoxLayout(0), rows...), 1, 1, 1, 1)
			return []fyne.CanvasObject{card}
		})
	p.details = d
}

// detailRow is one row of the details card (.r): the label in a 150 px column, the value
// wrapped next to it.
func detailRow(label, value string, mono bool) fyne.CanvasObject {
	seg := textSeg(value, theme.ColorNameForeground, false)
	if mono {
		seg.Style.TextStyle.Monospace = true
		seg.Style.SizeName = style.SizeNameSmallText
	}
	v := richLine(seg)
	l := designText(label, 13, style.ColorNameTextSecondary, false)
	return container.New(layout.NewCustomPaddedLayout(6, 6, 14, 14), container.New(&detailRowLayout{label: 150, gap: 12}, l, v))
}

// detailRowLayout puts objs[0] (a text) in a column of width label and objs[1] (a rich text
// with inner padding) after gap, both in the vertical middle, at least 32 high (44 with the
// padding of the row).
type detailRowLayout struct{ label, gap float32 }

func (l *detailRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	ip := theme.InnerPadding()
	ls := objs[0].MinSize()
	objs[0].Move(fyne.NewPos(0, (size.Height-ls.Height)/2))
	objs[0].Resize(fyne.NewSize(min(ls.Width, l.label), ls.Height))
	w := max(0, size.Width-l.label-l.gap) + 2*ip
	objs[1].Resize(fyne.NewSize(w, objs[1].MinSize().Height))
	vs := objs[1].MinSize()
	objs[1].Move(fyne.NewPos(l.label+l.gap-ip, (size.Height-vs.Height)/2))
	objs[1].Resize(fyne.NewSize(w, vs.Height))
}

func (l *detailRowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	ip := theme.InnerPadding()
	vs := objs[1].MinSize()
	return fyne.NewSize(l.label+l.gap+max(0, vs.Width-2*ip), max(32, vs.Height, objs[0].MinSize().Height))
}

// pemFileName is the suggested name of the saved certificate: its name with "_" for characters
// a file name should not have, and ".pem".
func pemFileName(c amt.Certificate) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, certName(c))
	if strings.Trim(name, "_.") == "" {
		name = "certificate"
	}
	return name + ".pem"
}

// saveCertificate is "Save as File…": the public certificate as PEM.
func (p *certsPane) saveCertificate(c amt.Certificate) {
	if len(c.DER) == 0 {
		showError(p.window(), certfile.ErrNotCertificate)
		return
	}
	win := p.window()
	p.saveFile(win, pemFileName(c), func(path string) {
		if err := os.WriteFile(path, certfile.PEM(c.DER), 0o644); err != nil {
			showError(win, err)
		}
	})
}

// --- delete

// showSimpleConfirm is the simple confirmation of the design: a red symbol tile, the text,
// Cancel and the red action button.
func showSimpleConfirm(parent fyne.Window, title, text, button string, proceed func()) *panel {
	return showPanel(parent, panelSpec{icon: icons.Delete, danger: true, title: title, action: button, cancel: lang.L("Cancel"),
		onAction: proceed}, func(*panel) []fyne.CanvasObject {
		msg := widget.NewLabel(text)
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	})
}

// confirmDeleteCert asks before c is deleted: orange for a trusted root (amt.OpRootCertDelete),
// simple for any other certificate (amt.OpCertDelete). The TLS certificate is not refused here:
// the device refuses it, and "Certificate in use" explains the way out.
func (p *certsPane) confirmDeleteCert(c amt.Certificate) {
	m := p.m
	title := lang.L("Delete “{{.Name}}”?", map[string]any{"Name": certName(c)})
	run := func(conf amt.Confirmation) {
		id := c.ID
		p.certWrite(&c, func() error { return m.sess.DeleteCertificate(id, conf) })
	}
	if c.TrustedRoot {
		p.rootDlg = showOrangeConfirm(p.window(), orangeSpec{
			op: amt.OpRootCertDelete, host: m.confirmHost(), device: m.confirmDevice(), title: title,
			warning: lang.L("{{.Name}} no longer trusts servers signed by this root. HTTPS boot from such servers stops working.",
				map[string]any{"Name": m.name}),
			button: lang.L("Delete Certificate"), onConfirm: run,
		})
		return
	}
	p.confirm = showSimpleConfirm(p.window(), title,
		lang.L("The certificate is removed from {{.Name}}. This cannot be undone.", map[string]any{"Name": m.name}),
		lang.L("Delete Certificate"), func() {
			if conf, err := amt.ConfirmChecked(amt.OpCertDelete, m.confirmHost(), true); err == nil {
				run(conf)
			}
		})
}

// confirmDeleteKey asks before key pair k is deleted (simple, amt.OpCertDelete).
func (p *certsPane) confirmDeleteKey(k amt.KeyPair) {
	m := p.m
	p.confirm = showSimpleConfirm(p.window(), lang.L("Delete key “{{.Key}}”?", map[string]any{"Key": keyText(k)}),
		lang.L("The key pair is removed from {{.Name}}. This cannot be undone.", map[string]any{"Name": m.name}),
		lang.L("Delete Key"), func() {
			conf, err := amt.ConfirmChecked(amt.OpCertDelete, m.confirmHost(), true)
			if err != nil {
				return
			}
			id := k.ID
			p.certWrite(nil, func() error { return m.sess.DeleteKeyPair(id, conf) })
		})
}

// showInUse explains that the device uses c for TLS: "Open Settings" shows the Settings page,
// where "Clock and TLS" chooses another certificate.
func (p *certsPane) showInUse(c amt.Certificate) {
	p.inUse = showPanel(p.window(), panelSpec{icon: icons.Info, tone: toneInfo, title: lang.L("Certificate in use"), action: lang.L("OK")},
		func(pn *panel) []fyne.CanvasObject {
			open := pn.newButton(lang.L("Open Settings"), func() {
				pn.close()
				p.m.showSettings(p.compact)
			})
			pn.extras = []fyne.CanvasObject{open}
			msg := widget.NewLabel(lang.L("{{.Name}} uses “{{.Certificate}}” for TLS. Choose another TLS certificate in Settings first, then delete this one.",
				map[string]any{"Name": p.m.name, "Certificate": certName(c)}))
			msg.Wrapping = fyne.TextWrapWord
			return []fyne.CanvasObject{msg}
		})
}

// showSettings shows the Settings page in the view the user is in: the sidebar's tab or the
// window's page.
func (m *manager) showSettings(compact bool) { m.showPageWith(icons.DeviceSettings, compact) }

// showPageWith shows the main page with icon in the view the user is in.
func (m *manager) showPageWith(icon icons.Name, compact bool) {
	i := -1
	for j, pg := range m.pages {
		if pg.icon == icon {
			i = j
		}
	}
	switch {
	case i < 0:
	case !compact && m.win != nil:
		m.win.showPage(i)
	case m.side != nil:
		m.side.tabs.Tap(i)
	}
}

// --- import

// certImport is "Import certificate".
type certImport struct {
	*panel
	kind   *widgets.Segmented // Trusted root | Certificate with private key
	path   string             // the chosen file; "" none
	file   *canvas.Text       // its name in the field
	choose *escButton         // "Choose File…"
	pw     *widgets.PasswordField
	pwBox  fyne.CanvasObject
}

// withKey reports whether "Certificate with private key" is selected.
func (d *certImport) withKey() bool { return d.kind.Selected() == 1 }

// sync shows the password for a certificate with key and enables Import for a chosen file.
func (d *certImport) sync() {
	setShown(d.pwBox, d.withKey())
	if d.path == "" {
		d.action.Disable()
	} else {
		d.action.Enable()
	}
	d.file.Text = filepath.Base(d.path)
	if d.path == "" {
		d.file.Text = ""
	}
	d.file.Refresh()
	d.fit()
}

// certExtensions are the file types of each kind.
func certExtensions(withKey bool) []string {
	if withKey {
		return []string{".p12", ".pfx"}
	}
	return []string{".pem", ".cer", ".crt"}
}

// openImport is "Import…".
func (p *certsPane) openImport() {
	d := &certImport{}
	d.panel = showPanel(p.window(), panelSpec{title: lang.L("Import certificate"), action: lang.L("Import"), cancel: lang.L("Cancel"),
		flush: true, keep: true, onAction: func() { p.runImport(d) }, onCancel: func() { d.pw.SetText("") }},
		func(pn *panel) []fyne.CanvasObject {
			d.panel = pn
			d.kind = widgets.NewSegmented(widgets.SegmentedText,
				widgets.TextOptions(lang.L("Trusted root"), lang.L("Certificate with private key")), func(int) {
					d.path = "" // the other kind takes other files
					d.sync()
				})
			d.kind.SetSelected(0)
			d.file = designText("", 12, theme.ColorNameForeground, false)
			d.file.TextStyle.Monospace = true
			field := container.New(&manageSizeLayout{h: 30},
				style.BindRect(&canvas.Rectangle{CornerRadius: confirmFieldRadius, StrokeWidth: 1}, style.ColorNameField, style.ColorNameControlBorder),
				container.New(layout.NewCustomPaddedLayout(0, 0, 9, 9), container.New(&rowLayout{fill: 0}, d.file)))
			d.choose = pn.newButton(lang.L("Choose File…"), func() {
				p.openFile(p.window(), certExtensions(d.withKey()), func(path string) {
					d.path = path
					d.sync()
				})
			})
			d.pw = userPasswordField(pn, func() {})
			d.pwBox = formField(lang.L("Password"), d.pw)
			return []fyne.CanvasObject{
				container.New(&rowLayout{fill: -1}, d.kind),
				formField(lang.L("File"), container.New(&rowLayout{gap: 8, fill: 0}, field, d.choose)),
				d.pwBox,
				formHint(lang.L(".pem, .cer or .crt. For a certificate with key: .p12 or .pfx with password.")),
			}
		})
	d.sync()
	p.importDlg = d
}

// readCertFile reads a file to import, at most certfile.MaxSize.
func readCertFile(path string) ([]byte, error) {
	if fi, err := os.Stat(path); err != nil {
		return nil, err
	} else if fi.Size() > certfile.MaxSize {
		return nil, certfile.ErrNotCertificate
	}
	return os.ReadFile(path)
}

// runImport is Import: the file is read and decoded while the dialog stays open, so a wrong
// file or password can be corrected; then the dialog closes and the device gets the
// certificate. A private key asks first without TLS (issue #37).
func (p *certsPane) runImport(d *certImport) {
	win, m := p.window(), p.m
	data, err := readCertFile(d.path)
	if err != nil {
		showError(win, err)
		return
	}
	if !d.withKey() {
		der, err := certfile.Certificate(data)
		if err != nil {
			showError(win, err)
			return
		}
		d.close()
		p.certWrite(nil, func() error { _, err := m.sess.AddTrustedRootCertificate(der); return err })
		return
	}
	der, key, err := certfile.PKCS12(data, d.pw.Text())
	if err != nil {
		showError(win, err)
		return
	}
	d.pw.SetText("")
	d.close()
	m.warnWithoutTLS(win, secretKey, "", func() {
		p.certWrite(nil, func() error { _, err := m.sess.AddCertificateWithKey(der, key); return err })
	})
}
