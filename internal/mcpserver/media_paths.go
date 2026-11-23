package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Srv-02/openlamt/internal/favorites"
)

// A mounted folder or file can be read on the managed machine, and through KVM by the agent:
// media_mount must not hand out the credentials of this computer. These folders are refused
// whatever the config says; media.allowed_paths can only narrow the rest.
//
// A folder has more than one spelling: a link, a bind mount on Linux, a firmlink on macOS
// (/System/Volumes/Data/Users is /Users), a junction or a \\?\ path on Windows. So the paths are
// compared as text and by file identity (os.SameFile), and the folder build checks the identity
// of every folder it reads (protectedSubdir).

// protectedDir is a folder media_mount never mounts from, with the name the refusal gives it.
// fi is the folder itself (os.Stat), nil when it cannot be read; home marks the home folder,
// whose subfolders may be mounted.
type protectedDir struct {
	path, name string
	fi         os.FileInfo
	home       bool
}

// testProtectedDirs is added to protectedDirs; tests protect a folder under another spelling.
var testProtectedDirs []protectedDir

// protectedDirs lists the folders that are refused: the home folder itself, the folders where
// programs keep credentials and settings (SSH, GnuPG, AWS, Azure, Kubernetes, Docker, pass,
// Firefox, Thunderbird, the NSS key store, Claude Code, Cargo, Maven, Gradle, Terraform, Flatpak
// and Snap app data, ~/.config with the GitHub CLI, ~/.local/share and ~/.local/state, the
// per-user config and cache folders of the system, all of ~/Library on macOS) and the OpenLAMT
// configuration (favorites, openlamt-mcp.json, the action log). A folder above one of them is
// refused too (mediaAllowed).
func (s *server) protectedDirs() []protectedDir {
	var out []protectedDir
	add := func(path, name string, home bool) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		d := protectedDir{path: filepath.Clean(path), name: name, home: home}
		if fi, err := os.Stat(d.path); err == nil {
			d.fi = fi
		}
		out = append(out, d)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(home, "the home folder", true)
		for _, d := range []struct{ rel, name string }{
			{".ssh", "SSH keys"}, {".gnupg", "GnuPG keys"}, {".aws", "AWS credentials"}, {".azure", "Azure credentials"},
			{".config", "program settings and credentials"}, {filepath.Join(".config", "gh"), "GitHub CLI credentials"},
			{filepath.Join(".local", "share"), "program data and credentials"}, {filepath.Join(".local", "state"), "program state"},
			{".kube", "Kubernetes credentials"}, {".docker", "Docker credentials"}, {".password-store", "the pass password store"},
			{".mozilla", "Firefox profiles"}, {".thunderbird", "Thunderbird profiles"}, {".pki", "the NSS certificate and key store"},
			{".claude", "Claude Code credentials"}, {".cargo", "Cargo credentials"}, {".m2", "Maven settings and credentials"},
			{".gradle", "Gradle credentials"}, {".terraform.d", "Terraform credentials"},
			{filepath.Join(".var", "app"), "Flatpak app data"}, {"snap", "Snap app data"},
		} {
			add(filepath.Join(home, d.rel), d.name, false)
		}
		if runtime.GOOS == "darwin" {
			add(filepath.Join(home, "Library"), "the macOS Library with the keychains", false)
		}
	}
	// %APPDATA% and %LOCALAPPDATA% on Windows; ~/.config and ~/.cache or ~/Library/... elsewhere.
	if d, err := os.UserConfigDir(); err == nil {
		add(d, "program settings and credentials", false)
	}
	if d, err := os.UserCacheDir(); err == nil {
		add(d, "program caches", false)
	}
	if p, err := favorites.DefaultPath(); err == nil {
		add(filepath.Dir(p), "the OpenLAMT configuration", false)
	}
	if s.cfg.Source != "" {
		add(filepath.Dir(s.cfg.Source), "the openlamt-mcp configuration", false)
	}
	if s.cfg.ActionLogPath != "" {
		add(filepath.Dir(s.cfg.ActionLogPath), "the openlamt-mcp action log", false)
	}
	return append(out, testProtectedDirs...)
}

// windowsAliasPath reports why media_mount refuses p on Windows, or "": a device or verbatim
// path (\\?\, \\.\, \??\, also with forward slashes, which covers \\?\Volume{...} and
// \\?\UNC\...) or an administrative share (\\host\C$). Such spellings reach a folder by another
// name than its drive letter path. It looks at the text only, so it is tested on every system.
func windowsAliasPath(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) >= 2 && p[1] == ':' {
		return ""
	}
	if len(p) >= 3 && (p[:3] == `\\?` || p[:3] == `\\.` || p[:3] == `\??`) && (len(p) == 3 || p[3] == '\\') {
		return "a Windows device path"
	}
	if rest, ok := strings.CutPrefix(p, `\\`); ok {
		if parts := strings.SplitN(rest, `\`, 3); len(parts) >= 2 && strings.HasSuffix(parts[1], "$") {
			return "an administrative share"
		}
	}
	return ""
}

// refuseWindowsAlias refuses a path windowsAliasPath names, on Windows only.
func refuseWindowsAlias(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if why := windowsAliasPath(p); why != "" {
		return refuse("%s is not mounted: it is %s (\\\\?\\, \\\\.\\ or a share such as \\\\host\\C$). Give the path with its drive "+
			"letter, such as C:\\Images\\installer.iso, or a normal network share. Nothing was mounted.", visible(p), why)
	}
	return nil
}

// resolvedForms returns p and, if it exists, p with its links resolved, so a protected folder
// reached through a link (macOS /var, a linked home) is recognized too.
func resolvedForms(p string) []string {
	out := []string{p}
	if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
		out = append(out, r)
	}
	return out
}

// foldCase: macOS and Windows file systems ignore case by default.
var foldCase = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// within reports whether path is dir or lies inside it; both are clean and absolute.
func within(path, dir string) bool {
	if foldCase {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// statCache stats each path once per check.
type statCache map[string]os.FileInfo

func (c statCache) stat(p string) os.FileInfo {
	if fi, ok := c[p]; ok {
		return fi
	}
	fi, err := os.Stat(p)
	if err != nil {
		fi = nil
	}
	c[p] = fi
	return fi
}

// above returns the folders above p (not p itself), as far as they can be read, nearest first.
func (c statCache) above(p string) []os.FileInfo {
	var out []os.FileInfo
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return out
		}
		p = parent
		if fi := c.stat(p); fi != nil {
			out = append(out, fi)
		}
	}
}

// sameAsAny reports whether fi is one of l.
func sameAsAny(fi os.FileInfo, l []os.FileInfo) bool {
	for _, x := range l {
		if fi != nil && x != nil && os.SameFile(fi, x) {
			return true
		}
	}
	return false
}

// mediaAllowed refuses a resolved source (with src.protected set) in, at or above a protected folder (a folder above
// would hold it), and one outside media.allowed_paths when the config sets them. The home folder
// only counts at or above it: its other subfolders may be mounted. Each test runs on the text of
// the paths and on file identity: the source or a folder above it is a protected folder, or the
// source is a folder above a protected one. A folder above that no path text leads to (macOS
// /System/Volumes/Data) is caught while the image is built (protectedSubdir).
func (s *server) mediaAllowed(src mediaSource) error {
	c := statCache{}
	srcAbove := c.above(src.path)
	for _, d := range src.protected {
		hit := false
		for _, dp := range resolvedForms(d.path) {
			inside := within(src.path, dp) && (!d.home || within(dp, src.path))
			hit = hit || inside || src.folder && within(dp, src.path)
			hit = hit || src.folder && sameAsAny(src.checked, c.above(dp))
		}
		if d.fi != nil {
			hit = hit || os.SameFile(src.checked, d.fi) || !d.home && sameAsAny(d.fi, srcAbove)
		}
		if hit {
			return refuse("%s is not mounted: it is or holds a protected folder (%s), whatever path or link leads there. %s Nothing was mounted.",
				visible(src.path), d.name, protectedText)
		}
	}
	if len(s.cfg.MediaAllowedPaths) == 0 {
		return nil
	}
	chain := append([]os.FileInfo{src.checked}, srcAbove...)
	for _, a := range s.cfg.MediaAllowedPaths {
		for _, ap := range resolvedForms(a) {
			if within(src.path, ap) {
				return nil
			}
		}
		if afi := c.stat(a); afi != nil && sameAsAny(afi, chain) {
			return nil
		}
	}
	return refuse("%s is outside media.allowed_paths of the openlamt-mcp config (%s). Nothing was mounted.",
		visible(src.path), visible(strings.Join(s.cfg.MediaAllowedPaths, ", ")))
}

// protectedText is what a refusal says about the protected folders.
const protectedText = "openlamt-mcp never mounts the home folder itself, a folder above it, or the folders that hold credentials and " +
	"settings (such as ~/.ssh, ~/.gnupg, ~/.aws, ~/.config, ~/.local, ~/.claude, ~/.mozilla, ~/.var/app, ~/snap, ~/Library on macOS, " +
	"%APPDATA% and %LOCALAPPDATA% on Windows; the README lists them all) or the OpenLAMT configuration."

// protectedSubdir refuses a folder of the image that is a protected folder (file identity): the
// image holds it then, also when no path text shows it (a firmlink, a bind mount). sub is the
// folder below the mounted one, fi the folder itself.
func protectedSubdir(src mediaSource, sub string, fi os.FileInfo) error {
	for _, d := range src.protected {
		if d.fi != nil && os.SameFile(fi, d.fi) {
			return refuse("%s is not mounted: its folder %s is a protected folder (%s). %s Nothing was mounted.",
				visible(src.path), visible(sub), d.name, protectedText)
		}
	}
	return nil
}
