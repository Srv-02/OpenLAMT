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

// protectedDir is a folder media_mount never mounts from, with the name the refusal gives it.
type protectedDir struct{ path, name string }

// protectedDirs lists the folders that are refused: the home folder itself, the folders where
// programs keep credentials and settings (SSH, GnuPG, AWS, Kubernetes, Docker, pass, Firefox,
// ~/.config with the GitHub CLI, ~/.local/share, the per-user config and cache folders of the
// system, all of ~/Library on macOS) and the OpenLAMT configuration (favorites,
// openlamt-mcp.json, the action log). A folder above one of them is refused too (mediaAllowed).
func (s *server) protectedDirs() []protectedDir {
	var out []protectedDir
	add := func(path, name string) {
		if path != "" && filepath.IsAbs(path) {
			out = append(out, protectedDir{filepath.Clean(path), name})
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(home, "the home folder")
		for _, d := range []struct{ rel, name string }{
			{".ssh", "SSH keys"}, {".gnupg", "GnuPG keys"}, {".aws", "AWS credentials"},
			{".config", "program settings and credentials"}, {filepath.Join(".config", "gh"), "GitHub CLI credentials"},
			{filepath.Join(".local", "share"), "program data and credentials"}, {".kube", "Kubernetes credentials"},
			{".docker", "Docker credentials"}, {".password-store", "the pass password store"}, {".mozilla", "Firefox profiles"},
		} {
			add(filepath.Join(home, d.rel), d.name)
		}
		if runtime.GOOS == "darwin" {
			add(filepath.Join(home, "Library"), "the macOS Library with the keychains")
		}
	}
	// %APPDATA% and %LOCALAPPDATA% on Windows; ~/.config and ~/.cache or ~/Library/... elsewhere.
	if d, err := os.UserConfigDir(); err == nil {
		add(d, "program settings and credentials")
	}
	if d, err := os.UserCacheDir(); err == nil {
		add(d, "program caches")
	}
	if p, err := favorites.DefaultPath(); err == nil {
		add(filepath.Dir(p), "the OpenLAMT configuration")
	}
	if s.cfg.Source != "" {
		add(filepath.Dir(s.cfg.Source), "the openlamt-mcp configuration")
	}
	if s.cfg.ActionLogPath != "" {
		add(filepath.Dir(s.cfg.ActionLogPath), "the openlamt-mcp action log")
	}
	return out
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

// mediaAllowed refuses a resolved source in, at or above a protected folder (a folder above
// would hold it), and one outside media.allowed_paths when the config sets them. The home folder
// only counts at or above it: its other subfolders may be mounted.
func (s *server) mediaAllowed(src mediaSource) error {
	for _, d := range s.protectedDirs() {
		for _, dp := range resolvedForms(d.path) {
			inside := within(src.path, dp) && (d.name != "the home folder" || within(dp, src.path))
			above := src.folder && within(dp, src.path)
			if inside || above {
				return refuse("%s is not mounted: it is or holds a protected folder (%s). openlamt-mcp never mounts the home folder "+
					"itself, a folder above it, or the folders that hold credentials and settings (~/.ssh, ~/.gnupg, ~/.aws, ~/.config, "+
					"~/.local/share, ~/.kube, ~/.docker, ~/.password-store, ~/.mozilla, ~/Library on macOS, %%APPDATA%% and %%LOCALAPPDATA%% "+
					"on Windows, the OpenLAMT configuration). Nothing was mounted.",
					visible(src.path), d.name)
			}
		}
	}
	if len(s.cfg.MediaAllowedPaths) == 0 {
		return nil
	}
	for _, a := range s.cfg.MediaAllowedPaths {
		for _, ap := range resolvedForms(a) {
			if within(src.path, ap) {
				return nil
			}
		}
	}
	return refuse("%s is outside media.allowed_paths of the openlamt-mcp config (%s). Nothing was mounted.",
		visible(src.path), visible(strings.Join(s.cfg.MediaAllowedPaths, ", ")))
}
