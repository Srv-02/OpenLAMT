// Package style is OpenLAMT's own Fyne theme (issues #44, #51): a dark and a light color table,
// Inter as the text font, and the design tokens (colors, text sizes, layout sizes) the custom
// widgets and windows use.
//
// Standard Fyne names (theme.ColorNameBackground, theme.SizeNameText, ...) map to the design
// values; the design-only values have their own names below. Widgets read them with Color and
// Size, which work without a running app (tests); theme.Color/theme.Size give the same values
// once the Theme is active.
//
// Variants: Color answers for the current variant (SetVariant, dark until set); Theme.Color
// answers for the variant Fyne passes. Fyne's test app reports variant 2, which counts as dark.
package style

import (
	"image/color"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Design-only color names. Values: HANDOFF.md section 2 and the reference mockups.
const (
	ColorNameSidebar        fyne.ThemeColorName = "openlamt-sidebar"          // #262628 sidebar background
	ColorNameToolbar        fyne.ThemeColorName = "openlamt-toolbar"          // #2A2A2C session toolbar row
	ColorNameStatusBar      fyne.ThemeColorName = "openlamt-statusbar"        // #232325 session status bar
	ColorNameCard           fyne.ThemeColorName = "openlamt-card"             // #2A2A2D form card background
	ColorNameCardBorder     fyne.ThemeColorName = "openlamt-card-border"      // rgba(255,255,255,0.06) card border, rows inside cards
	ColorNameSeparatorDark  fyne.ThemeColorName = "openlamt-separator-dark"   // rgba(0,0,0,0.55) below the toolbar, sidebar edge
	ColorNameGroupDivider   fyne.ThemeColorName = "openlamt-group-divider"    // rgba(255,255,255,0.10) toolbar groups, menu separators
	ColorNameTextSecondary  fyne.ThemeColorName = "openlamt-text-secondary"   // #A1A1A6 labels, metadata, toolbar captions
	ColorNameSectionLabel   fyne.ThemeColorName = "openlamt-section-label"    // #98989D "Favorites" heading, host under a name
	ColorNameDialogText     fyne.ThemeColorName = "openlamt-dialog-text"      // #B4B4B9 message text in confirm dialogs
	ColorNameIconMuted      fyne.ThemeColorName = "openlamt-icon-muted"       // #C7C7CC small icon buttons, sidebar tile icon
	ColorNameToolbarIcon    fyne.ThemeColorName = "openlamt-toolbar-icon"     // #E5E5EA toolbar icons and open-menu caption
	ColorNameTile           fyne.ThemeColorName = "openlamt-tile"             // rgba(255,255,255,0.08) sidebar icon tile
	ColorNameTileSelected   fyne.ThemeColorName = "openlamt-tile-selected"    // rgba(255,255,255,0.20) icon tile on a selected row
	ColorNameTileBorder     fyne.ThemeColorName = "openlamt-tile-border"      // rgba(255,255,255,0.08) detail and empty-state tile border
	ColorNameSearchField    fyne.ThemeColorName = "openlamt-search-field"     // rgba(255,255,255,0.07) sidebar search field
	ColorNameSelectedText2  fyne.ThemeColorName = "openlamt-selected-text2"   // rgba(255,255,255,0.85) host line on a selected row
	ColorNameActive         fyne.ThemeColorName = "openlamt-active"           // rgba(255,255,255,0.09) toggled toolbar item (KVM connected)
	ColorNameSwitchOff      fyne.ThemeColorName = "openlamt-switch-off"       // rgba(255,255,255,0.16) switch track when off (not in the mockups)
	ColorNameDangerTile     fyne.ThemeColorName = "openlamt-danger-tile"      // rgba(255,69,58,0.16) confirm dialog icon tile
	ColorNameErrorText      fyne.ThemeColorName = "openlamt-error-text"       // red warning text (dark #FF6961, light #D70015); "error" stays the red button fill
	ColorNameLetterbox      fyne.ThemeColorName = "openlamt-letterbox"        // #0B0B0C area around the framebuffer
	ColorNameEmptyStateTile fyne.ThemeColorName = "openlamt-empty-state-tile" // #1C1C1E icon tile of the "not connected" state
	ColorNameWhite          fyne.ThemeColorName = "openlamt-white"            // #FFFFFF text on accent, selected row
	ColorNameDialogCard     fyne.ThemeColorName = "openlamt-dialog-card"      // #232326 cards inside the About and settings dialogs
	ColorNameDialogBorder   fyne.ThemeColorName = "openlamt-dialog-border"    // rgba(255,255,255,0.12) dialog frame, number field
	ColorNameField          fyne.ThemeColorName = "openlamt-field"            // rgba(255,255,255,0.06) number field
	ColorNameAvatar         fyne.ThemeColorName = "openlamt-avatar"           // #111113 circle behind the heart
	ColorNamePill           fyne.ThemeColorName = "openlamt-pill"             // rgba(255,255,255,0.10) secondary pill button
	ColorNameLink           fyne.ThemeColorName = "openlamt-link"             // #4DA3FF hyperlink
	ColorNameLinkHover      fyne.ThemeColorName = "openlamt-link-hover"       // #7DBDFF hyperlink under the pointer
	ColorNameDetailTile     fyne.ThemeColorName = "openlamt-detail-tile"      // #2C2C2F server tile in the detail header (light #F2F2F4)
	ColorNameCardSeparator  fyne.ThemeColorName = "openlamt-card-separator"   // rgba(255,255,255,0.06) lines between card rows (light 0.07 black)
	ColorNameAvatarBorder   fyne.ThemeColorName = "openlamt-avatar-border"    // none; light rgba(0,0,0,0.06) around the avatar
	ColorNamePillBorder     fyne.ThemeColorName = "openlamt-pill-border"      // none; light rgba(0,0,0,0.12) around the secondary pill
	ColorNameTagline        fyne.ThemeColorName = "openlamt-tagline"          // #D1D1D6 tagline of the About dialog
	ColorNameMenuPanel      fyne.ThemeColorName = "openlamt-menu-panel"       // #2C2C2F (97 %) popup menu panel
	ColorNameMenuShadow     fyne.ThemeColorName = "openlamt-menu-shadow"      // rgba(0,0,0,0.55) shadow of the popup menu
)

// Design-only color names of the device management design (Main, WindowB, Settings, Dialogs,
// Danger .dc.html). The comments give the light values of the mockups; the dark values are
// derived. Existing tokens cover the rest: the navigation background is ColorNameSidebar, the
// selected navigation item "selection", table outlines ColorNameCardBorder, table row lines
// ColorNameCardSeparator, the green status dot style.StatusColor.
const (
	ColorNameSegment          fyne.ThemeColorName = "openlamt-segment"            // #EDEDF0 segmented control track
	ColorNameSegmentActive    fyne.ThemeColorName = "openlamt-segment-active"     // #FFFFFF selected segment
	ColorNameSegmentShadow    fyne.ThemeColorName = "openlamt-segment-shadow"     // rgba(0,0,0,0.14) shadow under the selected segment
	ColorNameAccentText       fyne.ThemeColorName = "openlamt-accent-text"        // #0A64D6 text and symbol of a selected segment or toggled toolbar item
	ColorNameAccentTint       fyne.ThemeColorName = "openlamt-accent-tint"        // #E8F1FE toggled toolbar item (Manage), info question tile
	ColorNameSidePanel        fyne.ThemeColorName = "openlamt-side-panel"         // #FBFBFC device management sidebar
	ColorNameTableHeader      fyne.ThemeColorName = "openlamt-table-header"       // #F7F7F8 log table header row
	ColorNameControlBorder    fyne.ThemeColorName = "openlamt-control-border"     // #D9D9DE chips, filter field, inputs, secondary buttons
	ColorNameControlText      fyne.ThemeColorName = "openlamt-control-text"       // #3A3A3C captions of segments and chips
	ColorNameChipActive       fyne.ThemeColorName = "openlamt-chip-active"        // #1C1C1E selected filter chip
	ColorNameChipActiveText   fyne.ThemeColorName = "openlamt-chip-active-text"   // #FFFFFF text of a selected filter chip
	ColorNameSeverityInfo     fyne.ThemeColorName = "openlamt-severity-info"      // #6E6E73 event log dot "Info"
	ColorNameSeverityWarning  fyne.ThemeColorName = "openlamt-severity-warning"   // #C75300 event log dot "Warning"
	ColorNameSeverityCritical fyne.ThemeColorName = "openlamt-severity-critical"  // #C2001A event log dot "Critical"
	ColorNameDanger           fyne.ThemeColorName = "openlamt-danger"             // #C2001A danger button fill, border of a mistyped confirmation
	ColorNameDangerDisabled   fyne.ThemeColorName = "openlamt-danger-disabled"    // #EBC2C8 disabled danger button
	ColorNameDangerSurface    fyne.ThemeColorName = "openlamt-danger-surface"     // #FDE8EA danger zone card, red warning box
	ColorNameDangerZoneBorder fyne.ThemeColorName = "openlamt-danger-zone-border" // #E9A3AD danger zone card border
	ColorNameDangerBoxBorder  fyne.ThemeColorName = "openlamt-danger-box-border"  // #F0B8C0 red warning box border
	ColorNameDangerZoneText   fyne.ThemeColorName = "openlamt-danger-zone-text"   // #A3001A danger zone heading and text
	ColorNameDangerBoxText    fyne.ThemeColorName = "openlamt-danger-box-text"    // #7A0014 red warning box text
	ColorNameWarningSurface   fyne.ThemeColorName = "openlamt-warning-surface"    // #FFF1E5 orange warning box, orange question tile
	ColorNameWarningBorder    fyne.ThemeColorName = "openlamt-warning-border"     // #F3CFAE orange warning box border
	ColorNameWarningText      fyne.ThemeColorName = "openlamt-warning-text"       // #6B2C00 orange warning box text
	ColorNameWarningIcon      fyne.ThemeColorName = "openlamt-warning-icon"       // #9A3F00 symbol in the orange question tile
	ColorNameFilterClear      fyne.ThemeColorName = "openlamt-filter-clear"       // #C7C7CC round clear button of the filter field
	ColorNameCodeBackground   fyne.ThemeColorName = "openlamt-code-background"    // #F2F2F4 behind the phrase in "Type … to confirm"
)

// Design-only text size names.
const (
	SizeNameSecondaryText  fyne.ThemeSizeName = "openlamt-text-secondary"  // 11.5 host under a name
	SizeNameToolbarCaption fyne.ThemeSizeName = "openlamt-toolbar-caption" // 10.5 toolbar captions
	SizeNameSectionLabel   fyne.ThemeSizeName = "openlamt-section-label"   // 11 "Favorites" heading (semibold)
	SizeNameDetailHeading  fyne.ThemeSizeName = "openlamt-detail-heading"  // 22 device name in the detail pane (bold)
	SizeNameMetadataText   fyne.ThemeSizeName = "openlamt-text-metadata"   // 12.5 "AMT … · ACM · TLS" line
	SizeNameButtonText     fyne.ThemeSizeName = "openlamt-text-button"     // 13.5 primary button, toolbar device name
	SizeNameEmptyTitle     fyne.ThemeSizeName = "openlamt-empty-title"     // 17 "Remote screen not connected"
	SizeNameSmallText      fyne.ThemeSizeName = "openlamt-text-small"      // 12 About disclaimer, credits footnote
	SizeNameCardTitle      fyne.ThemeSizeName = "openlamt-card-title"      // 14 "Support OpenLAMT" (bold)
)

// Layout sizes in Fyne units (1 unit = 1 CSS px of the mockups).
const (
	SidebarWidth           float32 = 272
	FormRowHeight          float32 = 44
	FormLabelWidth         float32 = 136
	ToolbarHeight          float32 = 64
	ToolbarItemHeight      float32 = 52
	ToolbarIconSize        float32 = 20
	StatusBarHeight        float32 = 28
	PrimaryButtonHeight    float32 = 36
	PrimaryButtonRadius    float32 = 9
	PrimaryButtonPadding   float32 = 18
	IconButtonWidth        float32 = 34
	IconButtonHeight       float32 = 28
	IconButtonRadius       float32 = 6
	CardRadius             float32 = 10
	SidebarRowRadius       float32 = 8
	SidebarRowPadV         float32 = 7
	SidebarRowPadH         float32 = 10
	SidebarTileSize        float32 = 30
	SidebarTileRadius      float32 = 7
	DetailTileSize         float32 = 64
	DetailTileRadius       float32 = 16
	MenuRadius             float32 = 10
	DialogRadius           float32 = 12
	StatusDotSize          float32 = 8
	StatusDotSizeSmall     float32 = 7
	SwitchWidth            float32 = 38
	SwitchHeight           float32 = 22
	SwitchKnob             float32 = 18
	DisabledOpacity        float64 = 0.35
	IconStroke             float32 = 1.7
	SearchFieldHeight      float32 = 30
	SearchFieldRadius      float32 = 8
	NewConnectionRowHeight float32 = 32
)

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

// white and black with alpha a (0..1), as the mockups write rgba(255,255,255,a).
func white(a float64) color.NRGBA { return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: alpha(a)} }
func black(a float64) color.NRGBA { return color.NRGBA{A: alpha(a)} }

func alpha(a float64) uint8 { return uint8(a*255 + 0.5) }

// darkColors is the dark color table (the design of issue #44).
var darkColors = map[fyne.ThemeColorName]color.Color{
	// Fyne names
	"background":          rgb(0x1E1E20),
	"button":              white(0.12),
	"disabledButton":      white(0.06),
	"disabled":            rgb(0x6E6E73),
	"error":               rgb(0xD70015),
	"focus":               color.NRGBA{R: 0x0A, G: 0x84, B: 0xFF, A: alpha(0.5)},
	"foreground":          rgb(0xF5F5F7),
	"foregroundOnError":   rgb(0xFFFFFF),
	"foregroundOnPrimary": rgb(0xFFFFFF),
	"foregroundOnSuccess": rgb(0x1E1E20),
	"foregroundOnWarning": rgb(0x1E1E20),
	"headerBackground":    rgb(0x262628),
	"hover":               white(0.09),
	"hyperlink":           rgb(0x0A84FF),
	"inputBackground":     color.Transparent,
	"inputBorder":         white(0.12),
	"menuBackground":      rgb(0x2C2C2F),
	"overlayBackground":   rgb(0x2C2C2F),
	"placeholder":         rgb(0x8E8E93),
	"pressed":             white(0.12),
	"primary":             rgb(0x0A84FF),
	"selection":           rgb(0x0A84FF),
	"separator":           white(0.06),
	"success":             rgb(0x30D158),
	"warning":             rgb(0xFF9F0A),
	// not in the design: Fyne's dark values, set here so Color needs no running app
	"shadow":                    color.NRGBA{A: 0x77},
	"scrollBar":                 color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x99},
	"scrollBarBackground":       rgb(0x202023),
	"innerWindowBorder":         rgb(0x181D25),
	"innerWindowBorderInactive": rgb(0x1B1B1B),

	// design-only names
	ColorNameSidebar:        rgb(0x262628),
	ColorNameToolbar:        rgb(0x2A2A2C),
	ColorNameStatusBar:      rgb(0x232325),
	ColorNameCard:           rgb(0x2A2A2D),
	ColorNameCardBorder:     white(0.06),
	ColorNameSeparatorDark:  black(0.55),
	ColorNameGroupDivider:   white(0.10),
	ColorNameTextSecondary:  rgb(0xA1A1A6),
	ColorNameSectionLabel:   rgb(0x98989D),
	ColorNameDialogText:     rgb(0xB4B4B9),
	ColorNameIconMuted:      rgb(0xC7C7CC),
	ColorNameToolbarIcon:    rgb(0xE5E5EA),
	ColorNameTile:           white(0.08),
	ColorNameTileSelected:   white(0.20),
	ColorNameTileBorder:     white(0.08),
	ColorNameSearchField:    white(0.07),
	ColorNameSelectedText2:  white(0.85),
	ColorNameActive:         white(0.09),
	ColorNameSwitchOff:      white(0.16),
	ColorNameDangerTile:     color.NRGBA{R: 0xFF, G: 0x45, B: 0x3A, A: alpha(0.16)},
	ColorNameErrorText:      rgb(0xFF6961), // 4.94:1 on #2C2C2F; #D70015 is 2.59:1
	ColorNameLetterbox:      rgb(0x0B0B0C),
	ColorNameEmptyStateTile: rgb(0x1C1C1E),
	ColorNameWhite:          rgb(0xFFFFFF),
	ColorNameDialogCard:     rgb(0x232326),
	ColorNameDialogBorder:   white(0.12),
	ColorNameField:          white(0.06),
	ColorNameAvatar:         rgb(0x111113),
	ColorNamePill:           white(0.10),
	ColorNameLink:           rgb(0x4DA3FF),
	ColorNameLinkHover:      rgb(0x7DBDFF),
	ColorNameDetailTile:     rgb(0x2C2C2F),
	ColorNameCardSeparator:  white(0.06),
	ColorNameAvatarBorder:   color.Transparent,
	ColorNamePillBorder:     color.Transparent,
	ColorNameTagline:        rgb(0xD1D1D6),
	ColorNameMenuPanel:      color.NRGBA{R: 0x2C, G: 0x2C, B: 0x2F, A: 0xF7},
	ColorNameMenuShadow:     black(0.55),

	// device management design, all derived
	ColorNameSegment:          white(0.08),
	ColorNameSegmentActive:    white(0.18),
	ColorNameSegmentShadow:    black(0.40),
	ColorNameAccentText:       rgb(0x4DA3FF),
	ColorNameAccentTint:       color.NRGBA{R: 0x0A, G: 0x84, B: 0xFF, A: alpha(0.20)},
	ColorNameSidePanel:        rgb(0x242426),
	ColorNameTableHeader:      rgb(0x262628),
	ColorNameControlBorder:    white(0.16),
	ColorNameControlText:      rgb(0xD1D1D6),
	ColorNameChipActive:       rgb(0xF5F5F7),
	ColorNameChipActiveText:   rgb(0x1C1C1E),
	ColorNameSeverityInfo:     rgb(0x98989D),
	ColorNameSeverityWarning:  rgb(0xFF9F0A),
	ColorNameSeverityCritical: rgb(0xFF6961),
	ColorNameDanger:           rgb(0xD70015),
	ColorNameDangerDisabled:   rgb(0x5A1E25),
	ColorNameDangerSurface:    color.NRGBA{R: 0xFF, G: 0x45, B: 0x3A, A: alpha(0.12)},
	ColorNameDangerZoneBorder: color.NRGBA{R: 0xFF, G: 0x45, B: 0x3A, A: alpha(0.45)},
	ColorNameDangerBoxBorder:  color.NRGBA{R: 0xFF, G: 0x45, B: 0x3A, A: alpha(0.35)},
	ColorNameDangerZoneText:   rgb(0xFF6961),
	ColorNameDangerBoxText:    rgb(0xFF8A80),
	ColorNameWarningSurface:   color.NRGBA{R: 0xFF, G: 0x9F, B: 0x0A, A: alpha(0.12)},
	ColorNameWarningBorder:    color.NRGBA{R: 0xFF, G: 0x9F, B: 0x0A, A: alpha(0.40)},
	ColorNameWarningText:      rgb(0xFFC78A),
	ColorNameWarningIcon:      rgb(0xFF9F0A),
	ColorNameFilterClear:      rgb(0x636366),
	ColorNameCodeBackground:   white(0.10),
}

// lightColors is the light color table (issue #51). Values come from the light design handoff
// (HANDOFF.md section 2 and the Light-*.html mockups); the ones the handoff does not cover are
// derived in its system and marked "derived".
var lightColors = map[fyne.ThemeColorName]color.Color{
	// Fyne names
	"background":          rgb(0xFFFFFF),
	"button":              black(0.06), // derived
	"disabledButton":      black(0.04), // derived
	"disabled":            rgb(0xAEAEB2),
	"error":               rgb(0xD70015),
	"focus":               color.NRGBA{R: 0x00, G: 0x7A, B: 0xFF, A: alpha(0.5)}, // derived
	"foreground":          rgb(0x1D1D1F),
	"foregroundOnError":   rgb(0xFFFFFF),
	"foregroundOnPrimary": rgb(0xFFFFFF),
	"foregroundOnSuccess": rgb(0xFFFFFF),
	"foregroundOnWarning": rgb(0x1D1D1F),
	"headerBackground":    rgb(0xF2F2F4), // derived
	"hover":               black(0.05),
	"hyperlink":           rgb(0x0066CC),
	"inputBackground":     color.Transparent,
	"inputBorder":         black(0.12), // derived
	"menuBackground":      rgb(0xFFFFFF),
	"overlayBackground":   rgb(0xFFFFFF),
	"placeholder":         rgb(0x8E8E93),
	"pressed":             black(0.08),
	"primary":             rgb(0x007AFF),
	"selection":           rgb(0x007AFF),
	"separator":           black(0.07),
	"success":             rgb(0x28A745),
	"warning":             rgb(0xFF9500), // derived
	// not in the design: Fyne's light values (derived)
	"shadow":                    color.NRGBA{A: 0x44},
	"scrollBar":                 color.NRGBA{A: 0x99},
	"scrollBarBackground":       rgb(0xDBDBDB),
	"innerWindowBorder":         rgb(0xFFFFFF),
	"innerWindowBorderInactive": rgb(0xF9F9F9),

	// design-only names
	ColorNameSidebar:        rgb(0xF2F2F4),
	ColorNameToolbar:        rgb(0xF5F5F7),
	ColorNameStatusBar:      rgb(0xF2F2F4),
	ColorNameCard:           rgb(0xF5F5F7),
	ColorNameCardBorder:     black(0.06),
	ColorNameSeparatorDark:  black(0.10),
	ColorNameGroupDivider:   black(0.12),
	ColorNameTextSecondary:  rgb(0x636366),
	ColorNameSectionLabel:   rgb(0x636366),
	ColorNameDialogText:     rgb(0x48484A), // derived
	ColorNameIconMuted:      rgb(0x48484A),
	ColorNameToolbarIcon:    rgb(0x1D1D1F),
	ColorNameTile:           black(0.06),
	ColorNameTileSelected:   white(0.22),
	ColorNameTileBorder:     black(0.06),
	ColorNameSearchField:    black(0.06),
	ColorNameSelectedText2:  white(0.90),
	ColorNameActive:         black(0.06),
	ColorNameSwitchOff:      rgb(0xE3E3E8),
	ColorNameDangerTile:     color.NRGBA{R: 0xFF, G: 0x3B, B: 0x30, A: alpha(0.12)}, // derived
	ColorNameErrorText:      rgb(0xD70015),                                          // 5.38:1 on white
	ColorNameLetterbox:      rgb(0xE3E3E6),
	ColorNameEmptyStateTile: rgb(0xF2F2F4), // derived
	ColorNameWhite:          rgb(0xFFFFFF),
	ColorNameDialogCard:     rgb(0xF5F5F7),
	ColorNameDialogBorder:   black(0.12),
	ColorNameField:          rgb(0xFFFFFF), // derived; drawn with ColorNameDialogBorder
	ColorNameAvatar:         rgb(0xFFFFFF), // drawn with a border of ColorNameCardBorder
	ColorNamePill:           rgb(0xFFFFFF), // drawn with a border of ColorNameDialogBorder
	ColorNameLink:           rgb(0x0066CC),
	ColorNameLinkHover:      rgb(0x004C99),
	ColorNameDetailTile:     rgb(0xF2F2F4), // derived
	ColorNameCardSeparator:  black(0.07),
	ColorNameAvatarBorder:   black(0.06),
	ColorNamePillBorder:     black(0.12),
	ColorNameTagline:        rgb(0x3A3A3C), // derived
	ColorNameMenuPanel:      rgb(0xFFFFFF),
	ColorNameMenuShadow:     black(0.18), // derived

	// device management design (mockups)
	ColorNameSegment:          rgb(0xEDEDF0),
	ColorNameSegmentActive:    rgb(0xFFFFFF),
	ColorNameSegmentShadow:    black(0.14),
	ColorNameAccentText:       rgb(0x0A64D6),
	ColorNameAccentTint:       rgb(0xE8F1FE),
	ColorNameSidePanel:        rgb(0xFBFBFC),
	ColorNameTableHeader:      rgb(0xF7F7F8),
	ColorNameControlBorder:    rgb(0xD9D9DE),
	ColorNameControlText:      rgb(0x3A3A3C),
	ColorNameChipActive:       rgb(0x1C1C1E),
	ColorNameChipActiveText:   rgb(0xFFFFFF),
	ColorNameSeverityInfo:     rgb(0x6E6E73),
	ColorNameSeverityWarning:  rgb(0xC75300),
	ColorNameSeverityCritical: rgb(0xC2001A),
	ColorNameDanger:           rgb(0xC2001A),
	ColorNameDangerDisabled:   rgb(0xEBC2C8),
	ColorNameDangerSurface:    rgb(0xFDE8EA),
	ColorNameDangerZoneBorder: rgb(0xE9A3AD),
	ColorNameDangerBoxBorder:  rgb(0xF0B8C0),
	ColorNameDangerZoneText:   rgb(0xA3001A),
	ColorNameDangerBoxText:    rgb(0x7A0014),
	ColorNameWarningSurface:   rgb(0xFFF1E5),
	ColorNameWarningBorder:    rgb(0xF3CFAE),
	ColorNameWarningText:      rgb(0x6B2C00),
	ColorNameWarningIcon:      rgb(0x9A3F00),
	ColorNameFilterClear:      rgb(0xC7C7CC),
	ColorNameCodeBackground:   rgb(0xF2F2F4),
}

// variant is the current theme variant: theme.VariantLight, anything else counts as dark.
var variant atomic.Int32

// SetVariant sets the variant Color answers for. Only theme.VariantLight selects the light
// table; every other value (dark, Fyne's test app reports 2) selects the dark one. Call it
// before the windows are refreshed, so the refresh reads the new colors. It recolors the
// primitives bound with BindRect, BindText and BindCircle itself; widgets read Color on Refresh.
func SetVariant(v fyne.ThemeVariant) {
	variant.Store(int32(v))
	recolorBound()
}

// Variant returns the current variant, normalized to theme.VariantDark or theme.VariantLight.
func Variant() fyne.ThemeVariant { return normalize(fyne.ThemeVariant(variant.Load())) }

// IsLight reports whether the current variant is light.
func IsLight() bool { return Variant() == theme.VariantLight }

func normalize(v fyne.ThemeVariant) fyne.ThemeVariant {
	if v == theme.VariantLight {
		return theme.VariantLight
	}
	return theme.VariantDark
}

var sizes = map[fyne.ThemeSizeName]float32{
	// Fyne names
	"text":            13,
	"helperText":      11.5,
	"headingText":     22,
	"subHeadingText":  15,
	"buttonRadius":    PrimaryButtonRadius,
	"cardRadius":      CardRadius,
	"dialogRadius":    DialogRadius,
	"popupRadius":     MenuRadius,
	"menuRadius":      MenuRadius,
	"selectionRadius": SidebarRowRadius,
	"inputRadius":     IconButtonRadius,
	"separator":       1,

	// design-only names
	SizeNameSecondaryText:  11.5,
	SizeNameToolbarCaption: 10.5,
	SizeNameSectionLabel:   11,
	SizeNameDetailHeading:  22,
	SizeNameMetadataText:   12.5,
	SizeNameButtonText:     13.5,
	SizeNameEmptyTitle:     17,
	SizeNameSmallText:      12,
	SizeNameCardTitle:      14,
}

// Color returns the design value for name, a Fyne or a design-only name, in the current
// variant (SetVariant); transparent for unknown names.
func Color(name fyne.ThemeColorName) color.Color { return ColorFor(name, Variant()) }

// ColorFor is Color for an explicit variant: light for theme.VariantLight, dark otherwise.
func ColorFor(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	table := darkColors
	if v == theme.VariantLight {
		table = lightColors
	}
	if c, ok := table[name]; ok {
		return c
	}
	return color.Transparent
}

// Size returns the design value for name, falling back to Fyne's default sizes.
func Size(name fyne.ThemeSizeName) float32 {
	if s, ok := sizes[name]; ok {
		return s
	}
	return fallback.Size(name)
}

// Over blends c with alpha a (0..1) over the opaque background bg; used for the 35 % opacity
// of disabled controls, which Fyne cannot apply to a whole widget.
func Over(c color.Color, a float64, bg color.Color) color.NRGBA {
	cr, cg, cb, ca := c.RGBA()
	br, bgG, bb, _ := bg.RGBA()
	f := a * float64(ca) / 0xffff
	mix := func(fg, back uint32) uint8 {
		// fg is premultiplied: undo it before mixing.
		fv := 0.0
		if ca > 0 {
			fv = float64(fg) / float64(ca) * 0xffff
		}
		return uint8((fv*f+float64(back)*(1-f))/0xffff*255 + 0.5)
	}
	return color.NRGBA{R: mix(cr, br), G: mix(cg, bgG), B: mix(cb, bb), A: 0xff}
}
