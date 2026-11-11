package ui

import (
	"context"
	"errors"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/kvm"
	"github.com/Srv-02/openlamt/internal/typing"
)

// The KVM client sends the strokes of "Type clipboard".
var _ typing.KeySender = (*kvm.Client)(nil)

// favoriteStore is the part of favorites.Manager the device window needs: the favorite with its
// layout override for "Type clipboard", KVM quality and auto-connect override, and the writers
// of the three. All of them read or write a file, so they run on a goroutine.
type favoriteStore interface {
	Find(host, username string) (favorites.Favorite, bool, error)
	SetQuality(host, username, quality string) error
	SetLayout(host, username, layout string) error
}

// layoutLabel is the spelled-out name of a layout in the UI language, e.g. "German (Switzerland)".
func layoutLabel(l typing.Layout) string {
	switch l {
	case typing.LayoutDE:
		return lang.L("German")
	case typing.LayoutUS:
		return lang.L("English (US)")
	case typing.LayoutUK:
		return lang.L("English (UK)")
	case typing.LayoutCHDE:
		return lang.L("German (Switzerland)")
	case typing.LayoutCHFR:
		return lang.L("French (Switzerland)")
	case typing.LayoutFR:
		return lang.L("French")
	case typing.LayoutBE:
		return lang.L("French (Belgium)")
	case typing.LayoutIT:
		return lang.L("Italian")
	case typing.LayoutES:
		return lang.L("Spanish")
	case typing.LayoutPT:
		return lang.L("Portuguese")
	case typing.LayoutBR:
		return lang.L("Portuguese (Brazil)")
	case typing.LayoutSEFI:
		return lang.L("Swedish/Finnish")
	case typing.LayoutNO:
		return lang.L("Norwegian")
	case typing.LayoutDK:
		return lang.L("Danish")
	case typing.LayoutRU:
		return lang.L("Russian")
	case typing.LayoutJP:
		return lang.L("Japanese")
	}
	return l.Label()
}

// layoutNames returns every layout sorted alphabetically by its label in the UI language, the
// order of all layout choices.
func layoutNames() []typing.Layout {
	ls := typing.Layouts()
	fold := strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "Ä", "a", "Ö", "o", "Ü", "u")
	key := func(l typing.Layout) string { return strings.ToLower(fold.Replace(layoutLabel(l))) }
	sort.SliceStable(ls, func(i, j int) bool { return key(ls[i]) < key(ls[j]) })
	return ls
}

// layoutOfLabel returns the layout a choice field shows as label.
func layoutOfLabel(label string) (typing.Layout, bool) {
	for _, l := range typing.Layouts() {
		if layoutLabel(l) == label {
			return l, true
		}
	}
	return "", false
}

// newLayoutSelect offers "none" (no layout of its own: the next level decides) and every layout;
// current is a stored layout name or empty.
func newLayoutSelect(none, current string) *widget.Select {
	options := []string{none}
	for _, l := range layoutNames() {
		options = append(options, layoutLabel(l))
	}
	sel := widget.NewSelect(options, nil)
	sel.SetSelectedIndex(0)
	if l, ok := typing.ParseLayout(current); ok {
		sel.SetSelected(layoutLabel(l))
	}
	return sel
}

// selectedLayout returns the layout name of a newLayoutSelect, empty for "none".
func selectedLayout(sel *widget.Select) string {
	if l, ok := layoutOfLabel(sel.Selected); ok {
		return string(l)
	}
	return ""
}

func defaultReadClipboard() string {
	if a := fyne.CurrentApp(); a != nil {
		return a.Clipboard().Content()
	}
	return ""
}

// typeJob is one "Type clipboard" run: the clipboard is typed at once, without a dialog,
// because it usually holds a password that must not appear on screen. Fyne thread only.
type typeJob struct {
	d      *DeviceWindow
	client *kvm.Client // the session the strokes go to
	text   string      // clipboard content as read at the start

	ctx    context.Context
	cancel context.CancelFunc

	sending, closed bool
	stopping        bool          // stop requested while sending; finish releases the state
	done            chan struct{} // closed once the sender goroutine has left typing.Type; nil until sending
}

// startTyping (hook of "Type clipboard") releases held keys, reads the clipboard, resolves the
// layout on a goroutine (favorite override, then global setting, then system language) and
// types. Live keyboard input is paused until the run ends. While a run exists the buttons
// read "Stop typing" and pressing them stops it.
func (d *DeviceWindow) startTyping() {
	if d.typing != nil {
		d.closeTyping()
		return
	}
	if d.kvmClient == nil || d.closed {
		return
	}
	d.releaseKeys()
	read := d.readClipboard
	if read == nil {
		read = defaultReadClipboard
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &typeJob{d: d, client: d.kvmClient, text: read(), ctx: ctx, cancel: cancel}
	d.typing = job
	d.view.paused = true
	d.setTypingButtons(true)

	global := d.settings.TypeLayout()
	if d.layouts == nil {
		job.run(d.resolveLayout(d.layoutChoice, global), nil)
		return
	}
	store, cfg := d.layouts, d.sess.Config()
	go func() {
		f, found, err := store.Find(cfg.Host, cfg.Username)
		stored := ""
		if err == nil && found {
			stored = f.Layout
		}
		fyne.Do(func() {
			// A choice made in the Keyboard menu or the form wins; a device that is not a favorite
			// only has the session's choice.
			override := d.layoutChoice
			if found && !d.layoutChosen {
				override = stored
			}
			job.run(d.resolveLayout(override, global), err)
		})
	}()
}

// resolveLayout picks the favorite override, then the global setting, then the layout of the
// local system.
func (d *DeviceWindow) resolveLayout(override string, global typing.Layout) typing.Layout {
	if l, ok := typing.ParseLayout(override); ok {
		return l
	}
	if global != "" {
		return global
	}
	return d.localLayout()
}

func (d *DeviceWindow) localLayout() typing.Layout {
	if d.detectLayout != nil {
		return d.detectLayout()
	}
	return typing.DetectLocalLayout(string(lang.SystemLocale()))
}

// closeTyping ends a pending or running job: the context is cancelled (the sender stops
// before the next character). Safe to call at any time.
func (d *DeviceWindow) closeTyping() {
	if d.typing != nil {
		d.typing.close()
	}
}

// run checks the text and starts the sender. Characters the layout cannot type are skipped without
// a message; the messages never contain the text itself. lookupErr is a failed favorite read, reported
// without stopping the run (the global setting applies).
func (job *typeJob) run(layout typing.Layout, lookupErr error) {
	d := job.d
	if job.closed || d.closed || d.kvmClient != job.client { // stopped or session gone while the lookup ran
		job.close()
		return
	}
	if lookupErr != nil {
		d.onError(lookupErr)
	}
	plan, err := typing.Prepare(job.text, layout)
	job.text = ""
	switch {
	case err != nil: // unreachable: resolveLayout only returns known layouts
		job.fail(err)
		return
	case plan.Chars == 0:
		job.fail(userError(lang.L("The clipboard contains no text.")))
		return
	case plan.TooLong:
		job.fail(userError(lang.L("The text has {{.Count}} characters; at most {{.Max}} can be typed.",
			map[string]any{"Count": plan.Chars, "Max": typing.MaxChars})))
		return
	}

	if plan.Typeable() == 0 { // only characters the layout cannot type: nothing to send, nothing to say
		job.fail(nil)
		return
	}
	job.sending = true
	total := plan.Typeable()
	job.report(0, total)
	delay, ctx, client := d.typeDelay, job.ctx, job.client
	done := make(chan struct{})
	job.done = done
	go func() {
		defer close(done) // runs after finish: a waiter that closes the session sees finish completed
		err := typing.Type(ctx, client, plan, delay, func(done, total int) {
			fyne.Do(func() { job.report(done, total) })
		})
		fyne.Do(func() { job.finish(err) })
	}()
}

func (job *typeJob) fail(err error) {
	job.close()
	if d := job.d; !d.closed {
		d.focusView() // the button tap took the focus
		if err != nil {
			d.onError(err)
		}
	}
	if job.d.onTypeDone != nil {
		job.d.onTypeDone()
	}
}

func (job *typeJob) report(done, total int) {
	if job.closed || job.stopping {
		return
	}
	job.d.touchIdle() // typing counts as input
	job.d.kvmStatus.SetText(lang.L("Typing clipboard: {{.Done}} of {{.Total}} characters", map[string]any{"Done": done, "Total": total}))
	if job.d.onTypeProgress != nil {
		job.d.onTypeProgress(done, total)
	}
}

// finish runs when the sender goroutine ends: success, stop or error.
func (job *typeJob) finish(err error) {
	wasRunning := !job.stopping && !job.closed
	job.sending = false
	job.close()
	if d := job.d; !d.closed {
		d.focusView()
	}
	if wasRunning && err != nil && !errors.Is(err, context.Canceled) && !job.d.closed {
		// The send error names the keysym of the current character: never show it.
		job.d.onError(userError(lang.L("Typing the clipboard was interrupted; some characters may be missing on the device.")))
	}
	if job.d.onTypeDone != nil {
		job.d.onTypeDone()
	}
}

// close cancels the context, resumes live input, resets the buttons and clears the window's
// reference; idempotent. While the sender still runs only the cancel happens: it stops before
// the next character (a dead key and its Space go out together), and finish releases the state
// then, so a new run or live keys cannot mix with its last stroke.
func (job *typeJob) close() {
	if job.closed {
		return
	}
	job.cancel()
	if job.sending {
		job.stopping = true
		return
	}
	job.closed = true
	d := job.d
	if d.typing == job {
		d.typing = nil
		d.view.paused = false
		if !d.closed {
			d.setTypingButtons(false)
			if d.kvmClient != nil { // after the session ended the status reports that instead
				d.kvmStatus.SetText("")
			}
		}
	}
}

// setTypingButtons switches both "Type clipboard" buttons (window and full screen bar).
func (d *DeviceWindow) setTypingButtons(running bool) {
	text := lang.L("Type Clipboard")
	if running {
		text = lang.L("Stop typing")
	}
	d.clipBtn.SetCaption(text)
	d.barClip.SetCaption(text)
}
