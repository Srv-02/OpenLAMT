// Package update asks GitHub for the latest release of OpenLAMT, compares it with the running
// version and downloads the package of this platform into a folder. It has no UI and installs
// nothing: the user replaces the app by hand.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint of the latest release of the public repository.
const LatestURL = "https://api.github.com/repos/Srv-02/openlamt/releases/latest"

const (
	checkTimeout    = 10 * time.Second
	downloadTimeout = 15 * time.Minute
	maxReleaseJSON  = 1 << 20   // the answer of the API, generously
	maxAssetSize    = 512 << 20 // no release package comes near it
)

// ErrDigest reports a downloaded file whose SHA-256 differs from the one GitHub states.
var ErrDigest = errors.New("update: checksum mismatch")

// Asset is one file of a release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>"; empty for older releases
}

// Release is the part of GitHub's release object OpenLAMT uses.
type Release struct {
	Tag     string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// Version is the tag without the leading "v".
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Checker queries the release API. The zero value asks LatestURL.
type Checker struct {
	URL       string       // the API endpoint; LatestURL when empty (tests point it at httptest)
	Client    *http.Client // nil: a client with a 10 s timeout
	UserAgent string       // "OpenLAMT/<version>"
}

func (c Checker) do(ctx context.Context, url string, timeout time.Duration, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return client.Do(req)
}

// Latest fetches the latest release. Any answer but 200 (404 before the first release, rate
// limit, server error) is an error, and so is a tag that is no version.
func (c Checker) Latest(ctx context.Context) (Release, error) {
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	resp, err := c.do(ctx, url, checkTimeout, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("update: release API answered %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseJSON)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("update: release API: %w", err)
	}
	if _, ok := parse(r.Tag); !ok {
		return Release{}, fmt.Errorf("update: tag %q is no version", r.Tag)
	}
	return r, nil
}

// version is a parsed semantic version; pre holds the dot-separated pre-release identifiers.
type version struct {
	core [3]int
	pre  []string
}

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func parse(s string) (version, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return version{}, false
	}
	var v version
	for i := range 3 {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		v.core[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// Valid reports whether s is a version that can be compared ("0.1.0", "v0.1.0-rc16"); "dev"
// and the empty string are not.
func Valid(s string) bool { _, ok := parse(s); return ok }

// Newer reports whether the release tag is a newer version than current. A pre-release is
// older than its final version (0.1.0-rc16 < 0.1.0); either side not being a version is false.
func Newer(tag, current string) bool {
	a, ok1 := parse(tag)
	b, ok2 := parse(current)
	return ok1 && ok2 && compare(a, b) > 0
}

func compare(a, b version) int {
	for i := range 3 {
		if c := cmpInt(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := cmpIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.pre), len(b.pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdent compares two pre-release identifiers naturally: runs of digits as numbers, so that
// rc9 < rc16 (strict semver would compare "rc9" and "rc16" as text).
func cmpIdent(a, b string) int {
	for a != "" && b != "" {
		ra, rest1 := run(a)
		rb, rest2 := run(b)
		na, err1 := strconv.Atoi(ra)
		nb, err2 := strconv.Atoi(rb)
		var c int
		switch {
		case err1 == nil && err2 == nil:
			c = cmpInt(na, nb)
		case err1 == nil: // digits before letters, as in semver
			c = -1
		case err2 == nil:
			c = 1
		default:
			c = strings.Compare(ra, rb)
		}
		if c != 0 {
			return c
		}
		a, b = rest1, rest2
	}
	return cmpInt(len(a), len(b))
}

// run splits s into its leading run of digits or of non-digits and the rest.
func run(s string) (string, string) {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i], s[i:]
}

// platformName is the suffix the release workflow gives the package of goos/goarch.
func platformName(goos, goarch string) (string, bool) {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "macos-arm64.zip", true
	case goos == "windows" && goarch == "amd64":
		return "windows-x64.zip", true
	case goos == "linux" && goarch == "amd64":
		return "linux-x64.deb", true
	}
	return "", false
}

// AssetFor returns the package of goos/goarch, named as the release workflow names it
// (OpenLAMT-<tag>-macos-arm64.zip, -windows-x64.zip, -linux-x64.deb); false when the platform
// has no package or the release lacks it.
func AssetFor(r Release, goos, goarch string) (Asset, bool) {
	suffix, ok := platformName(goos, goarch)
	if !ok || !Valid(r.Tag) {
		return Asset{}, false
	}
	want := "OpenLAMT-" + r.Tag + "-" + suffix
	for _, a := range r.Assets {
		if a.Name == want && a.URL != "" {
			return a, true
		}
	}
	return Asset{}, false
}

var (
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	ghByline    = regexp.MustCompile(`\s+by @\S+ in \S+$`) // "* title by @user in https://…/pull/1"
	bulletStart = regexp.MustCompile(`^[-*+]\s+`)
)

// Notes turns the Markdown body of a release into at most max plain lines: headings, empty
// lines and the "Full Changelog" line are dropped, list items start with "• ", links keep
// their text, emphasis and code marks go, GitHub's "by @user in <url>" is cut off.
func Notes(body string, max int) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if len(out) == max {
			break
		}
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.Contains(l, "Full Changelog") || strings.HasPrefix(l, "<!--") {
			continue
		}
		bullet := bulletStart.MatchString(l)
		l = bulletStart.ReplaceAllString(l, "")
		l = mdLink.ReplaceAllString(l, "$1")
		l = ghByline.ReplaceAllString(l, "")
		l = strings.NewReplacer("**", "", "__", "", "`", "").Replace(l)
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if bullet {
			l = "• " + l
		}
		out = append(out, l)
	}
	return out
}

// Download saves the asset in dir under its name, or with " (1)", " (2)" … before the
// extension when that name is taken; nothing is overwritten. It writes to a temporary file in
// dir first and renames it at the end, so dir never holds half a package under the final name.
// When the asset states a sha256 digest, a different file is deleted and ErrDigest returned.
// It returns the path of the saved file.
func (c Checker) Download(ctx context.Context, a Asset, dir string) (string, error) {
	name := filepath.Base(a.Name)
	if name != a.Name || name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
		return "", fmt.Errorf("update: unusable asset name %q", a.Name)
	}
	resp, err := c.do(ctx, a.URL, downloadTimeout, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: download answered %s", resp.Status)
	}
	tmp, err := os.CreateTemp(dir, ".openlamt-download-*")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxAssetSize+1))
	if err != nil {
		return "", err
	}
	if n > maxAssetSize {
		return "", fmt.Errorf("update: download larger than %d bytes", maxAssetSize)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if want, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && !strings.EqualFold(want, hex.EncodeToString(h.Sum(nil))) {
		return "", ErrDigest
	}
	dest := freeName(dir, name)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	keep = true
	return dest, nil
}

// freeName is dir/name, or the first of "name (1).ext", "name (2).ext" … that does not exist.
func freeName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	p := filepath.Join(dir, name)
	for i := 1; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// downloadsIn is home/Downloads, or home when that folder does not exist.
func downloadsIn(home string) string {
	d := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		return d
	}
	return home
}
