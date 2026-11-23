package sitecheck

import (
	"maps"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// siteDir is the guide, relative to this package.
const siteDir = "../../site"

var (
	attrRe    = regexp.MustCompile(`\s(href|src)="([^"]*)"`)
	idRe      = regexp.MustCompile(`\sid="([^"]+)"`)
	tagRe     = regexp.MustCompile(`<(link|script|img|use|iframe|source|video|audio)\b[^>]*>`)
	cssURLRe  = regexp.MustCompile(`url\(\s*['"]?([^'")]+)`)
	ipv4Re    = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	titleRe   = regexp.MustCompile(`<title>[^<]+</title>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// site reads every file of the guide, keyed by its path relative to siteDir with slashes.
func site(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(siteDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(siteDir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", siteDir, err)
	}
	if _, ok := files["index.html"]; !ok {
		t.Fatalf("%s/index.html is missing", siteDir)
	}
	return files
}

func pages(files map[string]string) []string {
	var out []string
	for name := range files {
		if strings.HasSuffix(name, ".html") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func ids(content string) map[string]bool {
	m := map[string]bool{}
	for _, g := range idRe.FindAllStringSubmatch(content, -1) {
		m[g[1]] = true
	}
	return m
}

// TestInternalLinksResolve follows every relative href and src: the file must exist in the
// site and a fragment must name an id in that file (a page, or the icon sprite).
func TestInternalLinksResolve(t *testing.T) {
	files := site(t)
	idsOf := map[string]map[string]bool{}
	for name, c := range files {
		idsOf[name] = ids(c)
	}
	for _, page := range pages(files) {
		dupes := map[string]int{}
		for _, g := range idRe.FindAllStringSubmatch(files[page], -1) {
			dupes[g[1]]++
		}
		for id, n := range dupes {
			if n > 1 {
				t.Errorf("%s: id %q is used %d times", page, id, n)
			}
		}
		for _, g := range attrRe.FindAllStringSubmatch(commentRe.ReplaceAllString(files[page], ""), -1) {
			ref := g[2]
			u, err := url.Parse(ref)
			if err != nil {
				t.Errorf("%s: %s=%q does not parse: %v", page, g[1], ref, err)
				continue
			}
			if u.Scheme != "" || u.Host != "" {
				continue // external links are checked by TestNoThirdPartyResources
			}
			target := page
			if u.Path != "" {
				if strings.HasPrefix(u.Path, "/") {
					t.Errorf("%s: %s=%q is absolute; the site is served below /OpenLAMT/, use a relative path", page, g[1], ref)
					continue
				}
				target = path.Clean(path.Join(path.Dir(page), u.Path))
				if strings.HasSuffix(u.Path, "/") {
					target = path.Join(target, "index.html")
				}
			}
			if _, ok := files[target]; !ok {
				t.Errorf("%s: %s=%q points to %s, which does not exist", page, g[1], ref, target)
				continue
			}
			if u.Fragment != "" && !idsOf[target][u.Fragment] {
				t.Errorf("%s: %s=%q: %s has no id %q", page, g[1], ref, target, u.Fragment)
			}
		}
	}
}

// TestEveryPageIsLinked makes sure no page is orphaned: each one is reachable from index.html.
func TestEveryPageIsLinked(t *testing.T) {
	files := site(t)
	seen := map[string]bool{"index.html": true}
	queue := []string{"index.html"}
	for len(queue) > 0 {
		page := queue[0]
		queue = queue[1:]
		for _, g := range attrRe.FindAllStringSubmatch(files[page], -1) {
			u, err := url.Parse(g[2])
			if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasSuffix(u.Path, ".html") {
				continue
			}
			target := path.Clean(path.Join(path.Dir(page), u.Path))
			if !seen[target] {
				seen[target] = true
				queue = append(queue, target)
			}
		}
	}
	for _, page := range pages(files) {
		if !seen[page] {
			t.Errorf("%s is not linked from index.html or any page it links to", page)
		}
	}
}

// TestNoThirdPartyResources keeps the guide self-contained: scripts, styles, fonts, images and
// icons come from the site itself. Plain links to other sites are fine.
func TestNoThirdPartyResources(t *testing.T) {
	files := site(t)
	for name, c := range files {
		switch {
		case strings.HasSuffix(name, ".html"):
			for _, tag := range tagRe.FindAllString(c, -1) {
				if alternateRe.MatchString(tag) {
					continue // the page's own language versions, checked by TestLanguageVersions
				}
				for _, g := range attrRe.FindAllStringSubmatch(tag, -1) {
					if isRemote(g[2]) {
						t.Errorf("%s: %s loads %q from another host", name, tag, g[2])
					}
				}
			}
			if strings.Contains(c, "<style") && cssURLRe.MatchString(c) {
				for _, g := range cssURLRe.FindAllStringSubmatch(c, -1) {
					if isRemote(g[1]) {
						t.Errorf("%s: inline CSS loads %q from another host", name, g[1])
					}
				}
			}
		case strings.HasSuffix(name, ".css"):
			if strings.Contains(c, "@import") {
				t.Errorf("%s: @import is not allowed; keep the CSS in one file", name)
			}
			for _, g := range cssURLRe.FindAllStringSubmatch(c, -1) {
				if isRemote(g[1]) {
					t.Errorf("%s: loads %q from another host", name, g[1])
				}
			}
		case strings.HasSuffix(name, ".js"):
			for _, bad := range []string{"fetch(", "XMLHttpRequest", "import(", "sendBeacon", "http://", "https://"} {
				if strings.Contains(c, bad) {
					t.Errorf("%s: contains %q; the script may only switch the theme and help navigate", name, bad)
				}
			}
		}
	}
}

func isRemote(ref string) bool {
	ref = strings.TrimSpace(strings.ToLower(ref))
	return strings.HasPrefix(ref, "http:") || strings.HasPrefix(ref, "https:") || strings.HasPrefix(ref, "//")
}

// TestPagesHaveTheBasics checks what every page needs to work without JavaScript and on a phone.
func TestPagesHaveTheBasics(t *testing.T) {
	files := site(t)
	for _, page := range pages(files) {
		c := files[page]
		lang, _ := splitLang(page)
		assets := "assets/"
		if lang != "en" {
			assets = "../assets/"
		}
		for _, want := range []string{
			"<!doctype html>", `<html lang="` + lang + `">`, `<meta charset="utf-8">`,
			`<meta name="viewport" content="width=device-width, initial-scale=1">`,
			`<link rel="stylesheet" href="` + assets + `guide.css">`, `<main id="main">`,
		} {
			if !strings.Contains(c, want) {
				t.Errorf("%s: missing %s", page, want)
			}
		}
		if !titleRe.MatchString(c) {
			t.Errorf("%s: missing a <title>", page)
		}
		if strings.Count(c, "<h1>") != 1 {
			t.Errorf("%s: wants exactly one <h1>, has %d", page, strings.Count(c, "<h1>"))
		}
	}
}

// documentation holds the address ranges reserved for examples (RFC 5737) and the masks that
// look like addresses.
var documentation = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("255.0.0.0/8"),
}

// TestExampleAddressesOnly keeps device data out of the guide: every IPv4 address is a
// documentation address or a subnet mask.
func TestExampleAddressesOnly(t *testing.T) {
	files := site(t)
	for name, c := range files {
		for _, s := range ipv4Re.FindAllString(c, -1) {
			a, err := netip.ParseAddr(s)
			if err != nil {
				continue // a version number such as 1.2.3.4000
			}
			if !slices.ContainsFunc(documentation, func(p netip.Prefix) bool { return p.Contains(a) }) {
				t.Errorf("%s: %s is not an example address; use 192.0.2.0/24", name, s)
			}
		}
	}
}

var (
	sideNavRe  = regexp.MustCompile(`(?s)<nav class="side"[^>]*>(.*?)</nav>`)
	navPageRe  = regexp.MustCompile(`<li><a href="([a-z0-9-]+\.html)"( aria-current="page")?>`)
	navTOCRe   = regexp.MustCompile(`(?s)<ol class="toc">(.*?)</ol>`)
	navFragRe  = regexp.MustCompile(`<a href="#([^"]+)"`)
	headingIDs = regexp.MustCompile(`<h2 id="([^"]+)"`)
)

// TestSideNavListsEveryPage keeps the guide's side navigation the same on every page: it lists
// every page of its language in one order (the same in every language), marks the page itself
// as current, and its table of contents names every section (h2) of that page.
func TestSideNavListsEveryPage(t *testing.T) {
	files := site(t)
	var want []string // the same order in every language
	for _, page := range pages(files) {
		lang, name := splitLang(page)
		var all []string
		for _, p := range pages(files) {
			if l, n := splitLang(p); l == lang {
				all = append(all, n)
			}
		}
		nav := sideNavRe.FindStringSubmatch(files[page])
		if nav == nil {
			t.Errorf("%s: no <nav class=\"side\">", page)
			continue
		}
		var got []string
		current := ""
		for _, g := range navPageRe.FindAllStringSubmatch(nav[1], -1) {
			got = append(got, g[1])
			if g[2] != "" {
				if current != "" {
					t.Errorf("%s: more than one page marked current", page)
				}
				current = g[1]
			}
		}
		if current != name {
			t.Errorf("%s: the side navigation marks %q as current", page, current)
		}
		if want == nil {
			want = got
			sorted := slices.Clone(got)
			slices.Sort(sorted)
			if !slices.Equal(sorted, all) {
				t.Errorf("%s: the side navigation lists %v, the site has %v", page, got, all)
			}
		} else if !slices.Equal(got, want) {
			t.Errorf("%s: the side navigation lists %v, other pages list %v", page, got, want)
		}
		var toc []string
		if m := navTOCRe.FindStringSubmatch(nav[1]); m != nil {
			for _, g := range navFragRe.FindAllStringSubmatch(m[1], -1) {
				toc = append(toc, g[1])
			}
		}
		var sections []string
		for _, g := range headingIDs.FindAllStringSubmatch(files[page], -1) {
			sections = append(sections, g[1])
		}
		if !slices.Equal(toc, sections) {
			t.Errorf("%s: the table of contents lists %v, the sections are %v", page, toc, sections)
		}
	}
}

// siteURL is where GitHub Pages serves the guide; the hreflang alternates name it in full.
const siteURL = "https://srv-02.github.io/OpenLAMT/"

// languages are the guide's languages: English at the top, every other one in its own folder
// with the same file names.
var languages = []string{"en", "de"}

var (
	alternateRe = regexp.MustCompile(`^<link rel="alternate" hreflang="[a-z-]+" href="` + regexp.QuoteMeta(siteURL) + `[^"]*">$`)
	hreflangRe  = regexp.MustCompile(`<link rel="alternate" hreflang="([a-z-]+)" href="([^"]*)">`)
	langNavRe   = regexp.MustCompile(`(?s)<nav class="lang" aria-label="[^"]+">(.*?)</nav>`)
	langLinkRe  = regexp.MustCompile(`<a href="([^"]+)" hreflang="([a-z]+)" lang="([a-z]+)"( aria-current="page")?>([A-Z]+)</a>`)
)

// splitLang returns the language of a page and its name within that language.
func splitLang(page string) (lang, name string) {
	if l, n, ok := strings.Cut(page, "/"); ok {
		return l, n
	}
	return "en", page
}

// pagePath is the path of page name in lang, relative to the site.
func pagePath(lang, name string) string {
	if lang == "en" {
		return name
	}
	return lang + "/" + name
}

// pageURL is the published address of page name in lang; index.html is its folder.
func pageURL(lang, name string) string {
	p := pagePath(lang, name)
	if name == "index.html" {
		p = strings.TrimSuffix(p, "index.html")
	}
	return siteURL + p
}

// TestLanguageVersions keeps the languages in step: every page exists in every language, its
// language switch leads to the same page in each language and marks its own, its hreflang
// alternates name every version, and both versions have the same ids, so a link to a section
// works in each language.
func TestLanguageVersions(t *testing.T) {
	files := site(t)
	byLang := map[string][]string{}
	for _, page := range pages(files) {
		lang, name := splitLang(page)
		if !slices.Contains(languages, lang) || strings.Contains(name, "/") {
			t.Errorf("%s: pages live at the top (English) or in one of the folders %v", page, languages[1:])
			continue
		}
		byLang[lang] = append(byLang[lang], name)
	}
	for _, lang := range languages[1:] {
		if !slices.Equal(byLang[lang], byLang["en"]) {
			t.Errorf("the %s pages %v differ from the English pages %v", lang, byLang[lang], byLang["en"])
		}
	}
	for _, page := range pages(files) {
		lang, name := splitLang(page)
		c := files[page]

		nav := langNavRe.FindStringSubmatch(c)
		if nav == nil {
			t.Errorf("%s: no language switch (<nav class=\"lang\">) in the header", page)
		} else {
			links := langLinkRe.FindAllStringSubmatch(nav[1], -1)
			var got []string
			for _, l := range links {
				href, hreflang, attrLang, current, label := l[1], l[2], l[3], l[4] != "", l[5]
				got = append(got, hreflang)
				if attrLang != hreflang || label != strings.ToUpper(hreflang) {
					t.Errorf("%s: language link %q is labelled %s (lang %s, hreflang %s)", page, href, label, attrLang, hreflang)
				}
				if target := path.Clean(path.Join(path.Dir(page), href)); target != pagePath(hreflang, name) {
					t.Errorf("%s: the %s link points to %s, wants %s", page, label, target, pagePath(hreflang, name))
				}
				if current != (hreflang == lang) {
					t.Errorf("%s: the %s link has aria-current=%v", page, label, current)
				}
			}
			if !slices.Equal(got, languages) {
				t.Errorf("%s: the language switch offers %v, wants %v", page, got, languages)
			}
		}

		want := map[string]string{"x-default": pageURL("en", name)}
		for _, l := range languages {
			want[l] = pageURL(l, name)
		}
		got := map[string]string{}
		for _, g := range hreflangRe.FindAllStringSubmatch(c, -1) {
			got[g[1]] = g[2]
		}
		if !maps.Equal(got, want) {
			t.Errorf("%s: hreflang alternates are %v, want %v", page, got, want)
		}

		if lang != "en" {
			en, de := ids(files[name]), ids(c)
			if !maps.Equal(en, de) {
				var missing, extra []string
				for id := range en {
					if !de[id] {
						missing = append(missing, id)
					}
				}
				for id := range de {
					if !en[id] {
						extra = append(extra, id)
					}
				}
				slices.Sort(missing)
				slices.Sort(extra)
				t.Errorf("%s: ids differ from %s: missing %v, extra %v", page, name, missing, extra)
			}
		}
	}
}

// supportURL and issuesURL are the support and bug report links; supportURL is the app's own
// (internal/ui/about.go).
const (
	supportURL = "https://buymeacoffee.com/michaelmayer"
	issuesURL  = "https://github.com/Srv-02/OpenLAMT/issues"
)

// TestAboutPage checks that every page links the About page from its side navigation and that
// the About page in each language has the support, bug report and license links.
func TestAboutPage(t *testing.T) {
	files := site(t)
	for _, page := range pages(files) {
		nav := sideNavRe.FindStringSubmatch(files[page])
		if nav == nil || !strings.Contains(nav[1], `<li><a href="about.html"`) {
			t.Errorf("%s: the side navigation does not link about.html", page)
		}
	}
	for _, lang := range languages {
		page := pagePath(lang, "about.html")
		c, ok := files[page]
		if !ok {
			t.Errorf("%s is missing", page)
			continue
		}
		for _, want := range []string{
			`href="` + supportURL + `"`, `href="` + issuesURL + `"`,
			`href="https://github.com/Srv-02/OpenLAMT/blob/main/LICENSE"`,
			`href="https://github.com/Srv-02/OpenLAMT/blob/main/NOTICE"`,
			"Intel Corporation", "Apache License 2.0",
		} {
			if !strings.Contains(c, want) {
				t.Errorf("%s: missing %s", page, want)
			}
		}
	}
}

var (
	anchorRe = regexp.MustCompile(`(?s)<a\s([^>]*)>(.*?)</a>`)
	pagerRe  = regexp.MustCompile(`(?s)<nav class="pager"[^>]*>(.*?)</nav>`)
	hrefRe   = regexp.MustCompile(`\shref="([^"]*)"`)
	classRe  = regexp.MustCompile(`\sclass="([^"]*)"`)
)

// readingPath is the order of the pages that carry a pager. The other pages have none.
var readingPath = []string{
	"getting-started.html", "favorites.html", "device-window.html", "management.html",
	"amt-settings.html", "confirmations.html", "settings.html",
}

// TestExternalLinksOpenInNewTab requires every link to another site to open in a new tab
// without opener access, carry the visual marker and tell screen readers about the new tab.
func TestExternalLinksOpenInNewTab(t *testing.T) {
	files := site(t)
	for _, page := range pages(files) {
		lang, _ := splitLang(page)
		hint := "(opens in a new tab)"
		if lang == "de" {
			hint = "(öffnet in neuem Tab)"
		}
		content := commentRe.ReplaceAllString(files[page], "")
		for _, m := range anchorRe.FindAllStringSubmatch(content, -1) {
			attrs := " " + m[1]
			h := hrefRe.FindStringSubmatch(attrs)
			if h == nil || !isRemote(h[1]) {
				if strings.Contains(attrs, "_blank") {
					t.Errorf("%s: internal link %q opens in a new tab", page, h)
				}
				continue
			}
			if !strings.Contains(attrs, ` target="_blank"`) {
				t.Errorf("%s: external link %s lacks target=\"_blank\"", page, h[1])
			}
			if !strings.Contains(attrs, ` rel="noopener noreferrer"`) {
				t.Errorf("%s: external link %s lacks rel=\"noopener noreferrer\"", page, h[1])
			}
			c := classRe.FindStringSubmatch(attrs)
			if c == nil || !slices.Contains(strings.Fields(c[1]), "ext") {
				t.Errorf("%s: external link %s lacks the class ext (the arrow marker)", page, h[1])
			}
			if !strings.Contains(m[2], `<span class="sr"> `+hint+`</span>`) {
				t.Errorf("%s: external link %s lacks the screen reader hint %q", page, h[1], hint)
			}
		}
	}
}

// TestPagerFollowsReadingPath requires a pager on the reading-path pages only, with previous
// and next links that follow the path: no previous on the first, no next on the last page.
func TestPagerFollowsReadingPath(t *testing.T) {
	files := site(t)
	for _, lang := range languages {
		for _, name := range pages(files) {
			if l, _ := splitLang(name); l != lang {
				continue
			}
			_, base := splitLang(name)
			c := files[name]
			i := slices.Index(readingPath, base)
			if i < 0 {
				if pagerRe.MatchString(c) {
					t.Errorf("%s: is not on the reading path and must have no pager", name)
				}
				continue
			}
			m := pagerRe.FindStringSubmatch(c)
			if m == nil {
				t.Errorf("%s: has no pager", name)
				continue
			}
			var prev, next string
			for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
				h := hrefRe.FindStringSubmatch(" " + a[1])
				if h == nil {
					continue
				}
				switch {
				case strings.Contains(a[1], `rel="prev"`):
					prev = h[1]
				case strings.Contains(a[1], `rel="next"`):
					next = h[1]
				}
			}
			var wantPrev, wantNext string
			if i > 0 {
				wantPrev = readingPath[i-1]
			}
			if i < len(readingPath)-1 {
				wantNext = readingPath[i+1]
			}
			if prev != wantPrev || next != wantNext {
				t.Errorf("%s: pager links prev=%q next=%q, want prev=%q next=%q", name, prev, next, wantPrev, wantNext)
			}
		}
	}
}

// TestAppOpensThisGuide: the Help button of the app's main window opens the guide at siteURL
// (internal/ui/guide.go), so a moved guide breaks this test rather than the button.
func TestAppOpensThisGuide(t *testing.T) {
	b, err := os.ReadFile("../ui/guide.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"`+siteURL+`"`) {
		t.Errorf("internal/ui/guide.go does not open %s", siteURL)
	}
	if _, ok := site(t)["de/index.html"]; !ok {
		t.Error("the app opens de/ for German, but the guide has no de/index.html")
	}
}
