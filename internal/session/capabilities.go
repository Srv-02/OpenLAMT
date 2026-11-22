package session

import "errors"

// ErrUnsupported: the device behind the session does not offer the capability (a fake without
// it, or a future device type).
var ErrUnsupported = errors.New("the device does not support this function")

// Session capabilities: how a feature adds device functions without editing session.go and
// fake.go.
//
// Device (session.go) lists the functions that every feature of the app shares. A new feature
// does not add its methods there. It adds two files instead.
//
// internal/session/<feature>.go holds a small interface with exactly the *amt.Device methods the
// feature needs, the compile-time check against *amt.Device, and the Session methods. Each
// Session method takes s.mu, gets the capability with capability, calls it and classifies the
// error:
//
//	type AlarmDevice interface {
//		GetAlarms() ([]amt.Alarm, error)
//	}
//
//	var _ AlarmDevice = (*amt.Device)(nil)
//
//	func (s *Session) Alarms() ([]amt.Alarm, error) {
//		s.mu.Lock()
//		defer s.mu.Unlock()
//		d, err := capability[AlarmDevice](s)
//		if err != nil {
//			return nil, err
//		}
//		list, err := d.GetAlarms()
//		return list, classify(err)
//	}
//
// internal/session/fake_<feature>.go holds the fake state and the interface methods on *Fake.
// The state lives in the Fake through fakeState, so the Fake struct gets no new field:
//
//	type FakeAlarms struct {
//		Err  error
//		List []amt.Alarm
//	}
//
//	func (f *Fake) Alarms() *FakeAlarms { return fakeState[FakeAlarms](f, "alarms") }
//
//	func (f *Fake) GetAlarms() ([]amt.Alarm, error) { return f.Alarms().List, f.Alarms().Err }
//
// The interface is asserted on s.dev at each call. A Device without the capability answers
// ErrUnsupported instead of breaking the build, and a feature run leaves the Device interface,
// session.go and the Fake struct untouched. capabilities_test.go runs the pattern end to end.
//
// capability returns the device of s as T, or ErrUnsupported. The caller holds s.mu.
func capability[T any](s *Session) (T, error) {
	d, ok := s.dev.(T)
	if !ok {
		var zero T
		return zero, ErrUnsupported
	}
	return d, nil
}

// fakeState returns the state of one feature of f, created on first use. key is the feature's
// name; T is the feature's state struct (see the comment above). It lets fake_<feature>.go add state
// without a new field in the Fake struct.
func fakeState[T any](f *Fake, key string) *T {
	if v, ok := f.features.Load(key); ok {
		return v.(*T)
	}
	v, _ := f.features.LoadOrStore(key, new(T))
	return v.(*T)
}
