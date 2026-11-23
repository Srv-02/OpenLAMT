package session

import (
	"slices"
	"sync"
	"time"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeWSMan is the WSMAN browser state of a Fake (Fake.WSMan). Results holds the answer per
// class; a class in Errs fails with that error; any other class answers like a device without
// it (*amt.ClassUnsupportedError). Reads records the classes read, in order.
type FakeWSMan struct {
	mu      sync.Mutex
	Results map[string]amt.WSManResult
	Errs    map[string]error
	Reads   []string
}

// WSMan returns the state, created on first use.
func (f *Fake) WSMan() *FakeWSMan { return fakeState[FakeWSMan](f, "wsman") }

// Lock and Unlock guard the state for tests that change it while a page reads it.
func (w *FakeWSMan) Lock()   { w.mu.Lock() }
func (w *FakeWSMan) Unlock() { w.mu.Unlock() }

// Read returns the classes read so far.
func (w *FakeWSMan) Read() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.Reads)
}

func (f *Fake) BrowseClass(class string) (amt.WSManResult, error) {
	f.enter()
	defer f.leave()
	time.Sleep(time.Millisecond)
	w := f.WSMan()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Reads = append(w.Reads, class)
	if err := w.Errs[class]; err != nil {
		return amt.WSManResult{}, err
	}
	if r, ok := w.Results[class]; ok {
		return r, nil
	}
	return amt.WSManResult{}, &amt.ClassUnsupportedError{Class: class, Fault: "DestinationUnreachable"}
}
