package ui

import (
	"net/url"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Srv-02/openlamt/internal/ui/icons"
	"github.com/Srv-02/openlamt/internal/ui/style"
	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// ProjectURL is the project page; the capitalisation is for readability only.
const ProjectURL = "https://github.com/Srv-02/OpenLAMT"

// supportURL is the Buy Me a Coffee page of the author.
const supportURL = "https://buymeacoffee.com/michaelmayer"

// credit is one third-party project named in NOTICE.
type credit struct {
	Name, License, URL string
}

// credits lists the projects whose code or assets OpenLAMT uses or includes (the head of
// NOTICE); the Go modules below it are only named in NOTICE itself.
var credits = []credit{
	{"Intel ui-toolkit", "Apache-2.0", "https://github.com/device-management-toolkit/ui-toolkit"},
	{"Intel go-wsman-messages", "Apache-2.0", "https://github.com/device-management-toolkit/go-wsman-messages"},
	{"MeshCommander", "Apache-2.0", "https://github.com/Ylianst/MeshCommander"},
	{"Fyne", "BSD-3-Clause", "https://github.com/fyne-io/fyne"},
	{"go-keyring", "MIT", "https://github.com/zalando/go-keyring"},
	{"zenity", "MIT", "https://github.com/ncruces/zenity"},
	{"Inter", "OFL-1.1", "https://github.com/rsms/inter"},
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// appVersion is the version to show: the one baked into the build (including any
// pre-release suffix), else the app metadata set by `fyne package`. Unpackaged builds
// ("dev" or empty) have none, and the callers then leave the version line out.
func appVersion(a fyne.App) string {
	if buildVersion != "" && buildVersion != "dev" {
		return buildVersion
	}
	if v := a.Metadata().Version; v != "" && v != "dev" {
		return v
	}
	return ""
}

// versionText is the "Version x" line, or nil when there is no version to show.
func versionText(a fyne.App, size float32) fyne.CanvasObject {
	v := appVersion(a)
	if v == "" {
		return nil
	}
	return designText(lang.L("Version {{.Version}}", map[string]any{"Version": v}),
		size, style.ColorNameTextSecondary, false)
}

// Geometry of the About dialog (About.html): 560 x 600, padding 24/24/18, 18 between the blocks.
const (
	aboutWidth  float32 = 560
	aboutHeight float32 = 600
	aboutPadH   float32 = 24
	aboutGap    float32 = 18

	aboutIconSize   float32 = 84
	supportAvatar   float32 = 48
	supportPadV     float32 = 16
	supportPadH     float32 = 18
	supportGap      float32 = 16
	supportTextLine float32 = 12.5 * 1.45
	creditsRow      float32 = 42
)

// supportTextWidth is the width of the support text: the dialog, less its padding, the
// card's padding, the avatar and the gap after it.
const supportTextWidth = aboutWidth - 2*aboutPadH - 2*supportPadH - supportAvatar - supportGap

// urlLink returns a link to u in color c (hover color h, nil for none).
func urlLink(text, u string, size float32, c, hover fyne.ThemeColorName) *widgets.Link {
	l := widgets.NewLink(text, mustURL(u))
	l.Color, l.HoverColor, l.TextSize = c, hover, size
	return l
}

// pill is one of the two pill buttons of the support card.
func pill(text string, icon fyne.Resource, iconSize float32, high bool, u string) *widgets.PrimaryButton {
	return pillAction(text, icon, iconSize, high, func() { openURL(u) })
}

// pillAction is a pill button that runs tapped.
func pillAction(text string, icon fyne.Resource, iconSize float32, high bool, tapped func()) *widgets.PrimaryButton {
	b := widgets.NewPrimaryButton(text, icon, tapped)
	b.Metrics = &widgets.ButtonMetrics{Height: 30, Radius: 15, PaddingH: 14, IconSize: iconSize, Gap: 7, TextSize: 12.5,
		Neutral: style.ColorNamePill, NeutralBorder: style.ColorNamePillBorder}
	if !high {
		b.Importance = widget.MediumImportance
	}
	return b
}

// openURL opens u in the browser; a variable so tests can see what would open.
var openURL = func(u string) {
	if a := fyne.CurrentApp(); a != nil {
		if err := a.OpenURL(mustURL(u)); err != nil {
			fyne.LogError("open "+u, err)
		}
	}
}

func aboutHeader(a fyne.App) fyne.CanvasObject {
	title := designText("OpenLAMT", style.Size(style.SizeNameDetailHeading), "foreground", true)
	titleRow := container.New(&baselineLayout{gap: 10}, title)
	if v := versionText(a, style.Size(style.SizeNameSmallText)); v != nil {
		titleRow.Add(v)
	}
	tagline := designText(lang.L("Desktop client for Intel AMT."), style.Size("text"), style.ColorNameTagline, false)
	disclaimer := designText(lang.L("Independent project, not affiliated with Intel."), style.Size(style.SizeNameSmallText), style.ColorNameTextSecondary, false)
	gh := urlLink("github.com/Srv-02/OpenLAMT", ProjectURL, style.Size(style.SizeNameMetadataText),
		style.ColorNameLink, style.ColorNameLinkHover)
	gh.Icon, gh.HoverIcon, gh.IconSize, gh.IconGap = icons.GetStroke(icons.ExternalLink, icons.Link, 2), icons.GetStroke(icons.ExternalLink, icons.LinkHover, 2), 12, 5
	link := container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), container.New(&rowLayout{fill: -1}, gh))
	texts := container.New(layout.NewCustomPaddedVBoxLayout(4), titleRow, tagline, disclaimer, link)
	return container.New(&rowLayout{gap: aboutGap, fill: 1}, widgets.NewAppIcon(icons.AppFull, aboutIconSize), texts)
}

func aboutSupport() fyne.CanvasObject {
	return supportCard(lang.L("OpenLAMT is a free, independent project. If it saves you a trip to the server room, a coffee helps keep it going."),
		supportTextWidth,
		pill(lang.L("Buy me a coffee"), icons.GetStroke(icons.Coffee, icons.White, 1.9), 15, true, supportURL),
		pill(lang.L("Star on GitHub"), icons.Get(icons.Star, icons.StarYellow), 14, false, ProjectURL))
}

// supportCard is the support card: heart avatar, title, text of the given width and the pills.
func supportCard(message string, textWidth float32, buttons ...fyne.CanvasObject) fyne.CanvasObject {
	avatarBg := style.BindCircle(&canvas.Circle{StrokeWidth: 1}, style.ColorNameAvatar, style.ColorNameAvatarBorder)
	avatar := container.New(&fixedSizeLayout{size: fyne.NewSquareSize(supportAvatar)}, avatarBg, container.NewCenter(widgets.NewSymbol(icons.Get(icons.Heart, icons.HeartPink), 24)))
	title := designText(lang.L("Support OpenLAMT"), style.Size(style.SizeNameCardTitle), "foreground", true)
	text := widgets.NewParagraph(message,
		style.Size(style.SizeNameMetadataText), textWidth, supportTextLine, style.ColorNameTextSecondary)
	copyBox := container.New(layout.NewCustomPaddedVBoxLayout(3), title, text)
	pills := container.New(&rowLayout{gap: 8, fill: -1}, buttons...)
	body := container.New(layout.NewCustomPaddedVBoxLayout(10), copyBox, pills)
	row := container.New(&rowLayout{gap: supportGap, fill: 1}, avatar, body)
	return widgets.NewPanel(style.ColorNameDialogCard, style.ColorNameCardBorder, style.DialogRadius,
		row, supportPadV, supportPadH, supportPadV, supportPadH)
}

func aboutCredits() fyne.CanvasObject {
	label := container.New(layout.NewCustomPaddedLayout(0, 0, 2, 0),
		designText(lang.L("Credits"), style.Size(style.SizeNameSectionLabel), style.ColorNameSectionLabel, true))
	var cells []fyne.CanvasObject
	for _, c := range credits {
		name := urlLink(c.Name, c.URL, style.Size("text"), "foreground", "")
		cells = append(cells, widgets.NewCell(name, widgets.NewBadge(c.License)))
	}
	grid := widgets.NewCellGrid(2, creditsRow, cells...)
	notice := urlLink("NOTICE", ProjectURL+"/blob/main/NOTICE", style.Size(style.SizeNameSmallText),
		style.ColorNameLink, style.ColorNameLinkHover)
	small := style.Size(style.SizeNameSmallText)
	foot := container.New(layout.NewCustomPaddedLayout(0, 0, 2, 0), container.New(&rowLayout{fill: -1},
		designText(lang.L("All third-party libraries and licenses are listed in")+" ", small, style.ColorNameTextSecondary, false),
		notice, designText(".", small, style.ColorNameTextSecondary, false)))
	return container.New(layout.NewCustomPaddedVBoxLayout(8), label, grid, foot)
}

// showAboutDialog shows the app icon, name, version and project link, the support card and
// the credits. The body sits in a scroll container (HANDOFF.md 5.5), so a larger font, a
// longer translation or a small window never pushes Close out of the dialog.
func showAboutDialog(parent fyne.Window, a fyne.App) {
	showDesignDialog(parent, dialogSpec{width: aboutWidth, height: aboutHeight, padTop: aboutPadH, padRight: aboutPadH - scrollInset,
		padBottom: 18, padL: aboutPadH - scrollInset, gap: aboutGap, flex: 0},
		func(d *designDialog) []fyne.CanvasObject {
			// Everything above Close scrolls, so Close stays reachable in a small window; in a
			// large one the content fits and the credits take the free height.
			scroll := scrollBody(dialogSpec{gap: aboutGap, flex: 2}, aboutHeader(a), aboutSupport(), aboutCredits())
			closeBtn := dialogButton(lang.L("Close"), false, 18, d.Close)
			closeBtn.OnEscape = d.Close
			return []fyne.CanvasObject{scroll,
				container.New(&rowLayout{fill: 0}, spacer(), closeBtn)}
		})
}

// InstallAboutMenu adds the About entry to the macOS application menu, where Fyne replaces
// the standard "About" item with it. Elsewhere a main menu would only add a menu bar to
// the window, so the About dialog is reached through the settings dialog instead.
func InstallAboutMenu(a fyne.App, w fyne.Window) {
	if runtime.GOOS != "darwin" {
		return
	}
	about := fyne.NewMenuItem("About", func() { showAboutDialog(w, a) })
	w.SetMainMenu(fyne.NewMainMenu(fyne.NewMenu("OpenLAMT", about)))
}
