package ui

import (
	_ "embed"
	"strings"
)

// bakedVersion is the content of the VERSION file next to this one. It is empty in the
// repository; the release workflow writes the tag (without the leading "v", with any
// pre-release suffix such as "0.1.0-rc14") into it before building, because `fyne package`
// only takes the numeric part for the macOS bundle.
//
//go:embed VERSION
var bakedVersion string

// buildVersion is the version shown in About, settings and the nudge; tests replace it.
var buildVersion = strings.TrimSpace(bakedVersion)
