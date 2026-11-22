package session

import (
	"errors"
	"sync"
	"testing"
)

// greeter is a stand-in for a feature's capability interface (see capability). The real
// pattern puts the interface in <feature>.go with `var _ Greeter = (*amt.Device)(nil)`.
type greeter interface {
	Greet(name string) (string, error)
}

// fakeGreeter is the feature's fake state, kept in the Fake through fakeState.
type fakeGreeter struct {
	mu    sync.Mutex
	Err   error
	Names []string
}

func (f *Fake) Greeter() *fakeGreeter { return fakeState[fakeGreeter](f, "greeter") }

func (f *Fake) Greet(name string) (string, error) {
	f.enter()
	defer f.leave()
	g := f.Greeter()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Names = append(g.Names, name)
	return "hello " + name, g.Err
}

// Greet is the Session method of the feature: mutex, capability, classify.
func (s *Session) Greet(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := capability[greeter](s)
	if err != nil {
		return "", err
	}
	out, err := d.Greet(name)
	return out, classify(err)
}

var _ greeter = (*Fake)(nil)

func TestCapabilityThroughSessionAndFake(t *testing.T) {
	f := &Fake{}
	s := &Session{dev: f}
	got, err := s.Greet("a")
	if err != nil || got != "hello a" {
		t.Fatalf("Greet: %q, %v", got, err)
	}
	boom := errors.New("boom")
	f.Greeter().Err = boom
	if _, err := s.Greet("b"); !errors.Is(err, boom) {
		t.Fatalf("fake error: %v", err)
	}
	if names := f.Greeter().Names; len(names) != 2 || names[1] != "b" {
		t.Fatalf("recorded: %v", names)
	}
	if fakeState[fakeGreeter](f, "greeter") != f.Greeter() || fakeState[struct{}](f, "other") == nil {
		t.Fatal("fakeState must return one value per key")
	}
}

// bare implements Device without any feature capability.
type bare struct{ Device }

func TestCapabilityMissingIsUnsupported(t *testing.T) {
	s := &Session{dev: bare{}}
	if _, err := s.Greet("a"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}
