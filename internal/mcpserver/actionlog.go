package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ActionLog appends one JSON line per tool call. It never holds passwords or consent codes.
type ActionLog struct {
	mu   sync.Mutex
	path string
}

// ActionEntry is one line of the action log.
type ActionEntry struct {
	Time    time.Time `json:"time"`
	Tool    string    `json:"tool"`
	Device  string    `json:"device,omitempty"`
	Action  string    `json:"action,omitempty"`
	Outcome string    `json:"outcome"` // ok, refused, error, confirmation_requested
	Message string    `json:"message,omitempty"`
}

// OpenActionLog creates the folder and the file (mode 0600) if needed, so a path that cannot
// be written fails at start, not at the first action.
func OpenActionLog(path string) (*ActionLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("action log: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("action log: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("action log: %w", err)
	}
	return &ActionLog{path: path}, nil
}

// Path is the file the log writes to.
func (l *ActionLog) Path() string { return l.path }

// Write appends e.
func (l *ActionLog) Write(e ActionEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("action log: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("action log: %w", err)
	}
	return f.Close()
}
