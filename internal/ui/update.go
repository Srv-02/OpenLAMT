package ui

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/update"
)

// The update check: once per start, a few seconds after the main window opened, OpenLAMT asks
// GitHub for the latest release and offers a newer one for download. It installs nothing.
const (
	updateDelay     = 5 * time.Second
	updateNoteLines = 6
)

// Hooks the tests replace: the API endpoint, the folder downloads go to, and the platform.
var (
	updateURL    = update.LatestURL
	downloadsDir = update.DownloadsDir
	updateGOOS   = runtime.GOOS
	updateGOARCH = runtime.GOARCH
)

type updateNotice struct {
	timer  *time.Timer
	ctx    context.Context
	cancel context.CancelFunc
	// pending is true from EnableUpdateCheck until the answer was handled; a coffee nudge that
	// falls due meanwhile waits (coffeeWaiting) so that only one of the two appears.
	pending       bool
	coffeeWaiting bool
	shown         bool         // the dialog appeared in this start
	async         func(func()) // runs network work off the Fyne thread; tests run it inline
}

// EnableUpdateCheck asks for a newer release a few seconds after the main window opened, if
// the setting allows it and this is a release build. Only main calls it, so no test window
// goes online by accident.
func (s *StartWindow) EnableUpdateCheck() { s.enableUpdate(updateDelay) }

// enableUpdate is EnableUpdateCheck with a delay; a negative delay starts no timer (tests call
// runUpdateCheck).
func (s *StartWindow) enableUpdate(delay time.Duration) {
	s.stopUpdate()
	if !s.settings.UpdateCheck() || !update.Valid(appVersion(s.app)) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &updateNotice{ctx: ctx, cancel: cancel, pending: true, async: func(f func()) { go f() }}
	s.updates = n
	if delay >= 0 {
		n.timer = time.AfterFunc(delay, func() { s.runUpdateCheck(n) })
	}
}

// stopUpdate ends a pending check and a running download (the main window closes).
func (s *StartWindow) stopUpdate() {
	if n := s.updates; n != nil {
		if n.timer != nil {
			n.timer.Stop()
		}
		n.cancel()
	}
}

func (s *StartWindow) updateChecker() update.Checker {
	return update.Checker{URL: updateURL, UserAgent: "OpenLAMT/" + appVersion(s.app)}
}

// runUpdateCheck queries GitHub (blocking) and hands the answer to the Fyne thread. Errors,
// 404 and rate limits stay silent.
func (s *StartWindow) runUpdateCheck(n *updateNotice) {
	rel, err := s.updateChecker().Latest(n.ctx)
	newer := err == nil && update.Newer(rel.Tag, appVersion(s.app))
	fyne.Do(func() { s.updateAnswered(n, rel, newer) })
}

// updateAnswered shows the dialog for a newer release if the window is quiet (the rules of
// coffeeFire), otherwise nothing; then a coffee nudge that waited for the answer may follow.
func (s *StartWindow) updateAnswered(n *updateNotice, rel update.Release, newer bool) {
	if s.updates != n || !n.pending {
		return
	}
	n.pending = false
	if newer && s.quiet() && s.settings.UpdateCheck() {
		n.shown = true
		s.showUpdateAvailable(n, rel)
		return
	}
	if n.coffeeWaiting {
		s.coffeeFire()
	}
}

// quiet reports whether a notice may appear: no device window (which includes a running KVM
// session), no connection attempt and no other dialog.
func (s *StartWindow) quiet() bool {
	return len(s.open) == 0 && len(s.busy) == 0 && s.win.Canvas().Overlays().Top() == nil
}

// updateIntro is the text of the dialog without the release notes.
func updateIntro(current string) string {
	return lang.L("You are using {{.Current}}. The new version is saved to your Downloads folder; you install it yourself.",
		map[string]any{"Current": current})
}

// showUpdateAvailable is the dialog "OpenLAMT x is available": what it does, the first lines of
// the release notes, "Don't check for updates" and Later / Download. Download stays open while
// the package loads, then the dialog "downloaded" replaces it; without a package for this
// platform it opens the release page instead.
func (s *StartWindow) showUpdateAvailable(n *updateNotice, rel update.Release) *panel {
	var stop *dialogCheck
	applyStop := func() {
		if stop.Checked {
			s.settings.SetUpdateCheck(false)
		}
	}
	var p *panel
	var dl context.CancelFunc = func() {}
	download := func() {
		applyStop()
		asset, ok := update.AssetFor(rel, updateGOOS, updateGOARCH)
		if !ok {
			p.close()
			if rel.HTMLURL != "" {
				openURL(rel.HTMLURL)
			}
			return
		}
		p.action.SetText(lang.L("Downloading…"))
		p.action.Disable()
		p.frame.Refresh() // the button row makes room for the longer text
		var ctx context.Context
		ctx, dl = context.WithCancel(n.ctx)
		n.async(func() {
			path, err := s.downloadUpdate(ctx, asset)
			fyne.Do(func() {
				if errors.Is(ctx.Err(), context.Canceled) || !p.close() {
					return // Later, Esc or the window closed while it loaded
				}
				if err != nil {
					s.onError(err)
					return
				}
				showUpdateDownloaded(s.win, rel.Version(), path)
			})
		})
	}
	p = showPanel(s.win, panelSpec{
		title:    lang.L("OpenLAMT {{.Version}} is available", map[string]any{"Version": rel.Version()}),
		action:   lang.L("Download"),
		cancel:   lang.L("Later"),
		keep:     true,
		onAction: download,
		onCancel: func() { applyStop(); dl() },
	}, func(p *panel) []fyne.CanvasObject {
		intro := widget.NewLabel(updateIntro(appVersion(s.app)))
		intro.Wrapping = fyne.TextWrapWord
		body := []fyne.CanvasObject{intro}
		if notes := update.Notes(rel.Body, updateNoteLines); len(notes) > 0 {
			news := widget.NewLabel(lang.L("What's new:") + "\n" + strings.Join(notes, "\n"))
			news.Wrapping = fyne.TextWrapWord
			body = append(body, news)
		}
		stop = p.newCheck(lang.L("Don't check for updates"), false)
		return append(body, stop)
	})
	return p
}

// downloadUpdate saves the package in the Downloads folder; a checksum mismatch becomes a
// message for the user.
func (s *StartWindow) downloadUpdate(ctx context.Context, a update.Asset) (string, error) {
	dir, err := downloadsDir()
	if err != nil {
		return "", err
	}
	path, err := s.updateChecker().Download(ctx, a, dir)
	if errors.Is(err, update.ErrDigest) {
		return "", userError(lang.L("The downloaded file does not match its checksum and was deleted. Try again later."))
	}
	return path, err
}

// showUpdateDownloaded names the saved file and offers to show its folder.
func showUpdateDownloaded(parent fyne.Window, version, path string) *panel {
	dir := filepath.Dir(path)
	shown := filepath.Join(filepath.Base(dir), filepath.Base(path))
	return showPanel(parent, panelSpec{
		title:    lang.L("OpenLAMT {{.Version}} downloaded", map[string]any{"Version": version}),
		action:   lang.L("Show in folder"),
		cancel:   lang.L("Close"),
		onAction: func() { openURL(fileURL(dir)) },
	}, func(p *panel) []fyne.CanvasObject {
		msg := widget.NewLabel(lang.L("Saved as {{.Path}}. Quit OpenLAMT, then replace the app with the new one.", map[string]any{"Path": shown}))
		msg.Wrapping = fyne.TextWrapWord
		return []fyne.CanvasObject{msg}
	})
}

// fileURL is the file:// URL of a local folder (on Windows with the drive letter as path).
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
