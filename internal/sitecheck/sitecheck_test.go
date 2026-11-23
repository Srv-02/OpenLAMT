package sitecheck

import (
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
		for _, want := range []string{
			"<!doctype html>", `<html lang="en">`, `<meta charset="utf-8">`,
			`<meta name="viewport" content="width=device-width, initial-scale=1">`,
			`<link rel="stylesheet" href="assets/guide.css">`, `<main id="main">`,
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
// every page in one order, marks the page itself as current, and its table of contents names
// every section (h2) of that page.
func TestSideNavListsEveryPage(t *testing.T) {
	files := site(t)
	all := pages(files)
	var want []string
	for _, page := range all {
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
		if current != page {
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
