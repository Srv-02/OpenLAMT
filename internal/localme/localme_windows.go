//go:build windows

package localme

import "golang.org/x/sys/windows"

// meiInterface is the device interface class of the Intel MEI driver (GUID_DEVINTERFACE_HECI).
var meiInterface = windows.GUID{Data1: 0xE2D1FF34, Data2: 0x3458, Data3: 0x49A9,
	Data4: [8]byte{0x88, 0xDA, 0x8E, 0x69, 0x15, 0xCE, 0x9B, 0xE5}}

// platformPresent asks the configuration manager for present interfaces of the MEI class. It
// lists the interfaces only; no handle to the device is opened.
func platformPresent() bool {
	list, err := windows.CM_Get_Device_Interface_List("", &meiInterface, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return false
	}
	for _, s := range list {
		if s != "" {
			return true
		}
	}
	return false
}
