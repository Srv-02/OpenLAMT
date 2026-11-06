package kvm

import "strings"

// Quality selects the pixel format negotiated with AMT. The zero value is High.
type Quality uint8

const (
	QualityHigh Quality = iota // 16 bit RGB565, AMT's default (ServerInit.ts:70)
	QualityLow                 // 8 bit RGB332 (ServerInit.ts:71-79)
)

func (q Quality) String() string {
	if q == QualityLow {
		return "low"
	}
	return "high"
}

// ParseQuality parses "high" or "low", case-insensitive.
func ParseQuality(s string) (Quality, bool) {
	switch strings.ToLower(s) {
	case "high":
		return QualityHigh, true
	case "low":
		return QualityLow, true
	}
	return QualityHigh, false
}

func (q Quality) bytesPerPixel() int {
	if q == QualityLow {
		return 1
	}
	return 2
}
