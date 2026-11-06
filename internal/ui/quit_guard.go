package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ui/icons"
)

// Quit guard. Fyne has no app-wide quit hook: on macOS Cmd+Q (GLFW applicationShouldTerminate)
// and the "Quit" menu item both hand every window its own close request, synchronously in one
// pass on the Fyne thread, while the red close button of a device window reaches only that one window. The main
// window and all device windows therefore route their close intercept here. The requests are
// collected and judged one event later (fyne.Do queues behind the running pass): a batch that
// contains the main window is a quit (Cmd+Q, menu, closing the main window), anything else is
// the user closing single device windows, which stays as it was, without a question.

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
			w.Close()
		}
		return
	}
	if s.quitAsking { // a second Cmd+Q while the question is open
		return
	}
	n := s.kvmSessions()
	if n == 0 {
		s.quitApp()
		return
	}
	s.quitAsking = true
	s.win.RequestFocus()
	title, message, button := quitText(n)
	showConfirm(s.win, icons.Power, title, message, button, func() {
		s.quitAsking = false
		s.quitApp()
	}, func() { s.quitAsking = false })
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
