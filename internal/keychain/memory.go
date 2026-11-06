package keychain

import "sync"

// Memory is an in-memory Store for tests. A non-nil Err is returned by every call,
// which simulates a keychain that is not available (Linux without Secret Service).
type Memory struct {
	Err  error
	mu   sync.Mutex
	data map[string]string // service + "\x00" + account
}

func NewMemory() *Memory { return &Memory{data: map[string]string{}} }

var _ Store = (*Memory)(nil)

func (m *Memory) Set(host, username, password string) error {
	return m.set(Service, account(host, username), password)
}
func (m *Memory) Get(host, username string) (string, error) {
	return m.get(Service, account(host, username))
}
func (m *Memory) Delete(host, username string) error { return m.del(Service, account(host, username)) }
func (m *Memory) SetFingerprint(host, username, fp string) error {
	return m.set(FingerprintService, account(host, username), fp)
}
func (m *Memory) GetFingerprint(host, username string) (string, error) {
	return m.get(FingerprintService, account(host, username))
}
func (m *Memory) DeleteFingerprint(host, username string) error {
	return m.del(FingerprintService, account(host, username))
}

func (m *Memory) set(service, acct, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.data[service+"\x00"+acct] = v
	return nil
}

func (m *Memory) get(service, acct string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return "", m.Err
	}
	v, ok := m.data[service+"\x00"+acct]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) del(service, acct string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	if _, ok := m.data[service+"\x00"+acct]; !ok {
		return ErrNotFound
	}
	delete(m.data, service+"\x00"+acct)
	return nil
}
