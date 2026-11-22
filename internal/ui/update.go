package ui

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
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
	// updateLog records why a check ended without a dialog. It names no device.
	updateLog = func(reason string, err error) { fyne.LogError(reason, err) }
	// updateRetryEvery is how often a found update waits for the window to be quiet.
	updateRetryEvery = 3 * time.Second
	// updateAsync runs network work off the Fyne thread; tests run it inline.
	updateAsync = func(f func()) { go f() }
	// updateAppImage is true when OpenLAMT runs as an AppImage: the AppImage runtime sets APPIMAGE.
	updateAppImage = os.Getenv("APPIMAGE") != ""
)

type updateNotice struct {
	timer  *time.Timer
	ctx    context.Context
	cancel context.CancelFunc
	// pending is true from EnableUpdateCheck until the answer was handled; a coffee nudge that
	// falls due meanwhile waits (coffeeWaiting) so that only one of the two appears.
	pending       bool
	coffeeWaiting bool
	// waiting is true while a newer release was found but the window is busy: the dialog
	// follows as soon as the window is quiet. The coffee nudge waits for it, too.
	waiting bool
	retry   *time.Timer
	shown   bool         // the dialog appeared in this start
	async   func(func()) // runs network work off the Fyne thread; tests run it inline
}

// EnableUpdateCheck asks for a newer release a few seconds after the main window opened, if
// the setting allows it and this is a release build. Only main calls it, so no test window
// goes online by accident.
func (s *StartWindow) EnableUpdateCheck() { s.enableUpdate(updateDelay) }

// enableUpdate is EnableUpdateCheck with a delay; a negative delay starts no timer (tests call
// runUpdateCheck).
func (s *StartWindow) enableUpdate(delay time.Duration) {
	s.stopUpdate()
	if !s.settings.UpdateCheck() {
		updateLog("update check skipped: the setting is off", nil)
		return
	}
	if !update.Valid(appVersion(s.app)) {
		updateLog("update check skipped: this build has no release version", nil)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &updateNotice{ctx: ctx, cancel: cancel, pending: true, async: updateAsync}
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
		if n.retry != nil {
			n.retry.Stop()
		}
		n.cancel()
	}
}

func (s *StartWindow) updateChecker() update.Checker {
	return update.Checker{URL: updateURL, UserAgent: "OpenLAMT/" + appVersion(s.app)}
}

// runUpdateCheck queries GitHub (blocking) and hands the answer to the Fyne thread. Errors,
// 404 and rate limits show nothing, but leave a log line with the reason.
func (s *StartWindow) runUpdateCheck(n *updateNotice) {
	rel, err := s.updateChecker().Latest(n.ctx)
	newer := err == nil && update.Newer(rel.Tag, appVersion(s.app))
	switch {
	case n.ctx.Err() != nil:
	case err != nil:
		updateLog("update check failed", err)
	case !newer:
		updateLog("update check: no newer release than "+appVersion(s.app)+" (latest is "+rel.Tag+")", nil)
	}
	fyne.Do(func() { s.updateAnswered(n, rel, newer) })
}

// updateAnswered shows the dialog for a newer release if the window is quiet (the rules of
// coffeeFire). A busy window keeps the release and shows the dialog once the window is quiet;
// then a coffee nudge that waited for the answer may follow.
func (s *StartWindow) updateAnswered(n *updateNotice, rel update.Release, newer bool) {
	if s.updates != n || !n.pending {
		return
	}
	n.pending = false
	if newer {
		switch {
		case !s.settings.UpdateCheck():
			updateLog("update "+rel.Version()+" found, not shown: the setting was turned off meanwhile", nil)
		case s.quiet():
			n.shown = true
			s.showUpdateAvailable(n, rel)
			return
		default:
			updateLog("update "+rel.Version()+" found, shown once the window is quiet", nil)
			n.waiting = true
			s.armUpdateRetry(n, rel)
			return
		}
	}
	s.updateSettled(n)
}

// updateSettled lets a coffee nudge that waited for the update answer follow.
func (s *StartWindow) updateSettled(n *updateNotice) {
	if n.coffeeWaiting {
		s.coffeeFire()
	}
}

// armUpdateRetry looks again after updateRetryEvery whether the window is quiet.
func (s *StartWindow) armUpdateRetry(n *updateNotice, rel update.Release) {
	n.retry = time.AfterFunc(updateRetryEvery, func() {
		if n.ctx.Err() == nil {
			fyne.Do(func() { s.updateRetry(n, rel) })
		}
	})
}

// updateRetry shows the waiting dialog if the window is quiet now, and looks again otherwise.
func (s *StartWindow) updateRetry(n *updateNotice, rel update.Release) {
	if s.updates != n || !n.waiting {
		return
	}
	switch {
	case !s.settings.UpdateCheck():
		n.waiting = false
		updateLog("update "+rel.Version()+" not shown: the setting was turned off meanwhile", nil)
		s.updateSettled(n)
	case s.quiet():
		n.waiting = false
		n.shown = true
		s.showUpdateAvailable(n, rel)
	default:
		s.armUpdateRetry(n, rel)
	}
}

// updateCheckState is the outcome of "Check Now".
type updateCheckState int

const (
	updateCheckFound  updateCheckState = iota // a newer release: its dialog opens
	updateCheckLatest                         // this is the latest version
	updateCheckFailed                         // the check did not work
)

// updateCheckResult is what the settings row "Check for updates" shows after "Check Now":
// text for the state Latest and Failed, none for Found (the update dialog says it).
type updateCheckResult struct {
	state updateCheckState
	text  string
}

// checkUpdateNow asks GitHub now, whatever the setting says, and reports the outcome on the Fyne
// thread. A newer release opens the update dialog (over the settings dialog that asked).
func (s *StartWindow) checkUpdateNow(report func(updateCheckResult)) {
	n := s.updates
	if n == nil || n.ctx.Err() != nil {
		ctx, cancel := context.WithCancel(context.Background())
		n = &updateNotice{ctx: ctx, cancel: cancel, async: updateAsync}
		s.updates = n
	}
	current := appVersion(s.app)
	if !update.Valid(current) {
		updateLog("manual update check: this build has no release version", nil)
		report(updateCheckResult{updateCheckFailed, updateFailText(lang.L("this build has no release version."))})
		return
	}
	n.async(func() {
		rel, err := s.updateChecker().Latest(n.ctx)
		fyne.Do(func() { s.checkAnswered(n, rel, err, current, report) })
	})
}

func (s *StartWindow) checkAnswered(n *updateNotice, rel update.Release, err error, current string, report func(updateCheckResult)) {
	if n.ctx.Err() != nil {
		return // the main window closed
	}
	switch {
	case err != nil:
		updateLog("manual update check failed", err)
		report(updateCheckResult{updateCheckFailed, updateFailText(updateFailReason(err))})
	case !update.Newer(rel.Tag, current):
		updateLog("manual update check: no newer release than "+current+" (latest is "+rel.Tag+")", nil)
		report(updateCheckResult{updateCheckLatest,
			lang.L("You are using the latest version ({{.Version}}).", map[string]any{"Version": current})})
	default:
		// This answer replaces an automatic one that is still pending or waiting.
		n.pending, n.waiting, n.shown = false, false, true
		if n.retry != nil {
			n.retry.Stop()
		}
		report(updateCheckResult{state: updateCheckFound})
		s.showUpdateAvailable(n, rel)
	}
}

func updateFailText(reason string) string {
	return lang.L("Could not check for updates: {{.Reason}}", map[string]any{"Reason": reason})
}

// updateFailReason is the reason of a failed check in words of the user.
func updateFailReason(err error) string {
	var se *update.StatusError
	var ne net.Error
	switch {
	case errors.As(err, &se) && (se.Code == 403 || se.Code == 429):
		return lang.L("GitHub's rate limit is reached. Try again later.")
	case errors.As(err, &se):
		return lang.L("GitHub answered with HTTP {{.Status}}. Try again later.", map[string]any{"Status": se.Code})
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return lang.L("GitHub did not answer. Try again later.")
	}
	return lang.L("GitHub could not be reached. Try again later.")
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
		asset, ok := update.AssetForRunning(rel, updateGOOS, updateGOARCH, updateAppImage)
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
