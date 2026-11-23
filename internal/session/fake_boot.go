package session

import (
	"sync"

	"github.com/Srv-02/openlamt/internal/amt"
)

// FakeBoot is the boot state of a Fake (issue #13, #21): BootInfo returns Info, Boot records the
// options and fails with BootErr, SetOCR switches Info.OCR.Enabled or fails with OCRErr. Gate,
// when set, holds Boot until it is closed or receives.
type FakeBoot struct {
	mu      sync.Mutex
	Info    amt.BootInfo
	InfoErr error
	BootErr error
	OCRErr  error
	Gate    chan struct{}
	boots   []amt.BootOptions
	ocr     []bool
}

// BootFake returns the boot state of f.
func (f *Fake) BootFake() *FakeBoot { return fakeState[FakeBoot](f, "boot") }

var _ BootDevice = (*Fake)(nil)

func (f *Fake) BootInfo() (amt.BootInfo, error) {
	b := f.BootFake()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Info, b.InfoErr
}

func (f *Fake) Boot(o amt.BootOptions) error {
	b := f.BootFake()
	if b.Gate != nil {
		<-b.Gate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.BootErr != nil {
		return b.BootErr
	}
	b.boots = append(b.boots, o)
	return nil
}

func (f *Fake) SetOCR(on bool) error {
	b := f.BootFake()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.OCRErr != nil {
		return b.OCRErr
	}
	b.ocr = append(b.ocr, on)
	b.Info.OCR.Enabled = on
	return nil
}

// Boots lists the options of every successful Boot.
func (b *FakeBoot) Boots() []amt.BootOptions {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]amt.BootOptions(nil), b.boots...)
}

// OCRWrites lists the values of every successful SetOCR.
func (b *FakeBoot) OCRWrites() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.ocr...)
}

// SetInfo replaces the boot info.
func (b *FakeBoot) SetInfo(info amt.BootInfo) {
	b.mu.Lock()
	b.Info = info
	b.mu.Unlock()
}
