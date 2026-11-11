package amt

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
	bios "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/bios"
	card "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/card"
	chassis "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/chassis"
	chip "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/chip"
	computer "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/computer"
	mediaaccess "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/mediaaccess"
	physical "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/physical"
	processor "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/processor"
	software "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/software"
	client "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
)

// HardwareInfo is the read-only hardware inventory of a device.
type HardwareInfo struct {
	System SystemInfo
	Board  BoardInfo
	BIOS   BIOSInfo
	CPUs   []CPUInfo
	Memory []MemoryModule
	Drives []Drive
	AMT    AMTInfo
}

// SystemInfo comes from CIM_Chassis and CIM_ComputerSystemPackage.
type SystemInfo struct {
	Manufacturer string
	Model        string // CIM Model, on many vendors the machine type number
	Version      string // CIM Version, on many vendors the product name
	SerialNumber string
	ChassisType  string // e.g. "MiniPC", "" if unknown
	UUID         string // SMBIOS system UUID, formatted, "" if absent
}

// BoardInfo comes from CIM_Card.
type BoardInfo struct{ Manufacturer, Model, Version, SerialNumber string }

// BIOSInfo comes from the primary CIM_BIOSElement.
type BIOSInfo struct {
	Manufacturer string
	Version      string
	ReleaseDate  time.Time // zero if absent or unparsable
}

// CPUInfo is a CIM_Processor joined with its CIM_Chip (Chip.Tag == Processor.DeviceID).
type CPUInfo struct {
	Socket          string // CIM_Processor.DeviceID, e.g. "CPU 0"
	Name            string // CIM_Chip.Version, "" if no chip matched
	Manufacturer    string // CIM_Chip.Manufacturer, "" if no chip matched
	MaxClockMHz     int
	CurrentClockMHz int
}

// MemoryModule is a CIM_PhysicalMemory.
type MemoryModule struct {
	Bank          string
	Manufacturer  string
	PartNumber    string
	SerialNumber  string
	CapacityBytes uint64
	SpeedMTs      int    // ConfiguredMemoryClockSpeed, else MaxMemorySpeed, else 0 (SMBIOS MT/s, e.g. 5600)
	Type          string // e.g. "DDR5", "" if unknown
}

// Drive is a CIM_MediaAccessDevice; drives of size 0 are omitted.
type Drive struct {
	ID            string // e.g. "MEDIA DEV 0"
	CapacityBytes uint64 // MaxMediaSize * 1000
}

// AMTInfo comes from CIM_SoftwareIdentity.
type AMTInfo struct {
	Version string // InstanceID "AMT"
	Build   string // InstanceID "Build Number"
}

const (
	enumValueNotFound = "Value not found in map"
	enumUnknown       = "Unknown"
)

// enumText maps the library's placeholder texts for unknown enum values to "".
func enumText(s string) string {
	if s == enumValueNotFound || s == enumUnknown {
		return ""
	}
	return strings.TrimSpace(s)
}

// xmlOf returns the raw response of a library call, "" if there is none.
func xmlOf(m *client.Message) string {
	if m == nil {
		return ""
	}
	return m.XMLOutput
}

// hwClass enumerates one CIM class. A missing class (SOAP fault on Enumerate) yields no items and no error,
// AccessDenied yields ErrAccessDenied, everything else aborts with the class name.
func hwClass[T any](class string, enum func() (string, error), pull func(string) ([]T, string, error)) ([]T, error) {
	enumFailed := false
	items, err := enumeratePull(func() (string, error) {
		ctx, err := enum()
		enumFailed = err != nil
		return ctx, err
	}, pull)
	if err == nil {
		return items, nil
	}
	if denied := accessDenied(err, "hardware info: "+class); errors.Is(denied, ErrAccessDenied) {
		return nil, denied
	}
	var amtErr *amterror.AMTError
	if enumFailed && errors.As(err, &amtErr) {
		return nil, nil
	}
	return nil, fmt.Errorf("hardware info: %s: %w", class, err)
}

// HardwareInfo reads the hardware inventory: nine CIM classes, about 1–1.5 s on AMT 16.
// A class the firmware does not offer leaves its section empty.
func (d *Device) HardwareInfo() (HardwareInfo, error) {
	var hw HardwareInfo
	c := d.msgs.CIM

	pkgs, err := hwClass("CIM_ComputerSystemPackage",
		func() (string, error) {
			r, err := c.ComputerSystemPackage.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]computer.ComputerSystemPackage, string, error) {
			r, err := c.ComputerSystemPackage.Pull(ctx)
			return r.Body.PullResponse.Items, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	if len(pkgs) > 0 {
		hw.System.UUID = formatPlatformGUID(pkgs[0].PlatformGUID)
	}

	chassisItems, err := hwClass("CIM_Chassis",
		func() (string, error) {
			r, err := c.Chassis.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]chassis.PackageResponse, string, error) {
			r, err := c.Chassis.Pull(ctx)
			return r.Body.PullResponse.PackageItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	if len(chassisItems) > 0 {
		ch := chassisItems[0]
		hw.System.Manufacturer = strings.TrimSpace(ch.Manufacturer)
		hw.System.Model = strings.TrimSpace(ch.Model)
		hw.System.Version = strings.TrimSpace(ch.Version)
		hw.System.SerialNumber = strings.TrimSpace(ch.SerialNumber)
		hw.System.ChassisType = enumText(ch.ChassisPackageType.String())
	}

	cards, err := hwClass("CIM_Card",
		func() (string, error) {
			r, err := c.Card.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]card.PackageResponse, string, error) {
			r, err := c.Card.Pull(ctx)
			return r.Body.PullResponse.CardItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	if len(cards) > 0 {
		cd := cards[0]
		hw.Board = BoardInfo{
			Manufacturer: strings.TrimSpace(cd.Manufacturer),
			Model:        strings.TrimSpace(cd.Model),
			Version:      strings.TrimSpace(cd.Version),
			SerialNumber: strings.TrimSpace(cd.SerialNumber),
		}
	}

	biosItems, err := hwClass("CIM_BIOSElement",
		func() (string, error) {
			r, err := c.BIOSElement.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]bios.BiosElement, string, error) {
			r, err := c.BIOSElement.Pull(ctx)
			return r.Body.PullResponse.BiosElementItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	if len(biosItems) > 0 {
		b := biosItems[0]
		for _, it := range biosItems {
			if it.PrimaryBIOS {
				b = it
				break
			}
		}
		hw.BIOS.Manufacturer = strings.TrimSpace(b.Manufacturer)
		hw.BIOS.Version = strings.TrimSpace(b.Version)
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(b.ReleaseDate.DateTime)); err == nil {
			hw.BIOS.ReleaseDate = t
		}
	}

	procs, err := hwClass("CIM_Processor",
		func() (string, error) {
			r, err := c.Processor.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]processor.PackageResponse, string, error) {
			r, err := c.Processor.Pull(ctx)
			return r.Body.PullResponse.PackageItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}

	// The Chip enumeration continues with CIM_PhysicalMemory items (a CIM subclass); the
	// CIM_Chip item parser drops them.
	chips, err := hwClass("CIM_Chip",
		func() (string, error) {
			r, err := c.Chip.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]chip.PackageResponse, string, error) {
			r, err := c.Chip.Pull(ctx)
			return r.Body.PullResponse.ChipItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	for _, p := range procs {
		cpu := CPUInfo{
			Socket:          strings.TrimSpace(p.DeviceID),
			MaxClockMHz:     p.MaxClockSpeed,
			CurrentClockMHz: p.CurrentClockSpeed,
		}
		for _, ch := range chips {
			if strings.TrimSpace(ch.Tag) == cpu.Socket {
				cpu.Name = strings.TrimSpace(ch.Version)
				cpu.Manufacturer = strings.TrimSpace(ch.Manufacturer)
				break
			}
		}
		hw.CPUs = append(hw.CPUs, cpu)
	}

	mems, err := hwClass("CIM_PhysicalMemory",
		func() (string, error) {
			r, err := c.PhysicalMemory.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]physical.PhysicalMemory, string, error) {
			r, err := c.PhysicalMemory.Pull(ctx)
			return r.Body.PullResponse.MemoryItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	for _, m := range mems {
		mod := MemoryModule{
			Bank:         strings.TrimSpace(m.BankLabel),
			Manufacturer: strings.TrimSpace(m.Manufacturer),
			PartNumber:   strings.TrimSpace(m.PartNumber),
			SerialNumber: strings.TrimSpace(m.SerialNumber),
			SpeedMTs:     m.ConfiguredMemoryClockSpeed,
			Type:         enumText(m.MemoryType.String()),
		}
		if m.Capacity > 0 {
			mod.CapacityBytes = uint64(m.Capacity)
		}
		if mod.SpeedMTs == 0 {
			mod.SpeedMTs = m.MaxMemorySpeed
		}
		hw.Memory = append(hw.Memory, mod)
	}

	drives, err := hwClass("CIM_MediaAccessDevice",
		func() (string, error) {
			r, err := c.MediaAccessDevice.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]mediaaccess.MediaAccessDevice, string, error) {
			r, err := c.MediaAccessDevice.Pull(ctx)
			return r.Body.PullResponse.MediaAccessDevices, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	for _, dr := range drives {
		if dr.MaxMediaSize == 0 {
			continue
		}
		// MaxMediaSize is in KB of 1000 bytes.
		hw.Drives = append(hw.Drives, Drive{ID: strings.TrimSpace(dr.DeviceID), CapacityBytes: dr.MaxMediaSize * 1000})
	}

	sw, err := hwClass("CIM_SoftwareIdentity",
		func() (string, error) {
			r, err := c.SoftwareIdentity.Enumerate()
			return r.Body.EnumerateResponse.EnumerationContext, err
		},
		func(ctx string) ([]software.SoftwareIdentity, string, error) {
			r, err := c.SoftwareIdentity.Pull(ctx)
			return r.Body.PullResponse.SoftwareIdentityItems, xmlOf(r.Message), err
		})
	if err != nil {
		return HardwareInfo{}, err
	}
	for _, s := range sw {
		switch strings.TrimSpace(s.InstanceID) {
		case "AMT":
			hw.AMT.Version = strings.TrimSpace(s.VersionString)
		case "Build Number":
			hw.AMT.Build = strings.TrimSpace(s.VersionString)
		}
	}
	return hw, nil
}

// formatPlatformGUID turns the 32 hex characters of CIM_ComputerSystemPackage.PlatformGUID into
// the SMBIOS UUID notation: the first three fields byte-swapped, upper case. Anything else yields "".
func formatPlatformGUID(s string) string {
	if len(s) != 32 {
		return ""
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
	b[4], b[5] = b[5], b[4]
	b[6], b[7] = b[7], b[6]
	h := strings.ToUpper(hex.EncodeToString(b))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
