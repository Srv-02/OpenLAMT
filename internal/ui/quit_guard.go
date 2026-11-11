package ui

import (
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ider"
	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// Quit guard. Fyne has no app-wide quit hook: on macOS Cmd+Q (GLFW applicationShouldTerminate)
// and the "Quit" menu item both hand every window its own close request, synchronously in one
// pass on the Fyne thread, while the red close button of a device window reaches only that one window. The main
// window and all device windows therefore route their close intercept here. The requests are
// collected and judged one event later (fyne.Do queues behind the running pass): a batch that
// contains the main window is a quit (Cmd+Q, menu, closing the main window), anything else is
// the user closing single device windows, which close without a question unless an ISO is
// mounted on them (DeviceWindow.requestClose). Quitting closes every device window, and closing
// unmounts its ISO.
//
// A quit first asks each device window with a mounted (or prepared) ISO in turn, on that window
// and in the order of the window keys, with the question of its close button ("An ISO is still
// mounted"): Keep Open ends the quit, Unmount and Close closes that window and goes on. Then the
// KVM question counts the device windows that are still open, so no window is asked twice. The
// unmounting runs on goroutines; WaitForCleanup lets main wait for it before the process ends.

// requestClose is the close intercept of the main window and of every device window.
func (s *StartWindow) requestClose(w fyne.Window) {
	s.closeReqs = append(s.closeReqs, w)
	if s.closeQueued {
		return
	}
	s.closeQueued = true
	s.later(s.flushCloseRequests)
}

func (s *StartWindow) flushCloseRequests() {
	reqs := s.closeReqs
	s.closeReqs, s.closeQueued = nil, false
	quit := false
	for _, w := range reqs {
		quit = quit || w == s.win
	}
	if !quit {
		for _, w := range reqs {
			s.closeWindow(w)
		}
		return
	}
	if s.quitAsking { // a second Cmd+Q while a question is open
		return
	}
	s.quitAsking = true
	keys := make([]string, 0, len(s.open))
	for k := range s.open {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var ds []*DeviceWindow
	for _, k := range keys {
		ds = append(ds, s.open[k])
	}
	s.askUnmountThenQuit(ds)
}

// askUnmountThenQuit asks the next of ds with an ISO whether to unmount it and close; after the
// last one it asks about the KVM sessions and quits.
func (s *StartWindow) askUnmountThenQuit(ds []*DeviceWindow) {
	for len(ds) > 0 && (ds[0].closed || !ds[0].media.busy()) {
		ds = ds[1:]
	}
	if len(ds) == 0 {
		s.askKVMThenQuit()
		return
	}
	d := ds[0]
	d.win.RequestFocus()
	d.askClose(func() {
		d.win.Close()
		s.askUnmountThenQuit(ds[1:])
	}, func() { s.quitAsking = false })
}

// askKVMThenQuit asks before quitting while KVM sessions are open, and quits.
func (s *StartWindow) askKVMThenQuit() {
	n := s.kvmSessions()
	if n == 0 {
		s.quitAsking = false
		s.quitApp()
		return
	}
	s.win.RequestFocus()
	title, message, button := quitText(n)
	showConfirm(s.win, icons.Power, title, message, button, func() {
		s.quitAsking = false
		s.quitApp()
	}, func() { s.quitAsking = false })
}

// exitWait bounds how long the process waits for unmounting after the last window closed.
const exitWait = 3 * time.Second

// WaitForCleanup waits, at most exitWait, until the closed device windows have unmounted their
// ISOs: the IDE-R session is closed, a folder ISO deleted and the next-start boot option
// cleared. main calls it after the app stopped; it reports whether everything finished.
func (s *StartWindow) WaitForCleanup() bool { return s.exitWork.wait(exitWait) }

// pendingWork counts work that has to finish before the process exits; nil-safe (no counting).
type pendingWork struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n drops to 0
}

// add registers one piece of work; done may be called more than once.
func (p *pendingWork) add() (done func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	if p.n == 0 {
		p.idle = make(chan struct{})
	}
	p.n++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.n--
			if p.n == 0 {
				close(p.idle)
			}
			p.mu.Unlock()
		})
	}
}

// wait waits until no work is registered, or timeout; it reports whether all work finished.
func (p *pendingWork) wait(timeout time.Duration) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	n, idle := p.n, p.idle
	p.mu.Unlock()
	if n == 0 {
		return true
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-idle:
		return true
	case <-t.C:
		return false
	}
}

// trackedImage is an image whose Close ends its pending work.
type trackedImage struct {
	ider.Image
	done func()
}

func (t *trackedImage) Close() error {
	defer t.done()
	return t.Image.Close()
}

// track registers img as pending work until it is closed (a folder ISO is deleted then);
// nil-safe.
func (p *pendingWork) track(img ider.Image) ider.Image {
	if p == nil || img == nil {
		return img
	}
	return &trackedImage{Image: img, done: p.add()}
}

// closeWindow closes a window the user closed on its own; a device window asks first while an
// ISO is mounted on it.
func (s *StartWindow) closeWindow(w fyne.Window) {
	for _, d := range s.open {
		if d.win == w {
			d.requestClose(w.Close)
			return
		}
	}
	w.Close()
}

// kvmSessions counts the device windows with a connected, connecting or reconnecting KVM session.
func (s *StartWindow) kvmSessions() int {
	n := 0
	for _, d := range s.open {
		if d.kvmClient != nil || d.kvmBusy || d.kvmReconnecting {
			n++
		}
	}
	return n
}

// quitText is title, message and action button of the quit question.
func quitText(n int) (title, message, button string) {
	title = lang.L("Quit OpenLAMT?")
	button = lang.L("Quit")
	if n == 1 {
		return title, lang.L("A KVM session is still open. It is closed when you quit."), button
	}
	return title, lang.L("{{.Count}} KVM sessions are still open. They are closed when you quit.", map[string]any{"Count": n}), button
}
