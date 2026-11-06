package typing

// X11 keysyms. Printable keys use their ASCII value (unshifted, US position).
const (
	// KeyReturn is the Enter key.
	KeyReturn uint32 = 0xff0d
	// KeyTab is the Tab key.
	KeyTab uint32 = 0xff09
	// KeySpace is the space bar.
	KeySpace uint32 = 0x20
	// KeyShiftL is the left Shift key.
	KeyShiftL uint32 = 0xffe1
	// KeyAltR is the right Alt key, used as AltGr. Assumption to verify on the test
	// device: AMT passes Alt_R on by position and the target turns it into AltGr. The
	// alternative would be ISO_Level3_Shift (0xfe03).
	KeyAltR uint32 = 0xffea
)
