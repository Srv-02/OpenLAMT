package mkiso

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

const (
	sectorSize     = 2048
	maxJolietUnits = 103
	maxFileSize    = 1<<32 - 1
)

// treeData is the per-tree (0 primary, 1 Joliet) state of a node.
type treeData struct {
	id     []byte   // identifier as stored in directory records
	units  []uint16 // identifier as code units, for ordering
	sorted []*node  // children in directory record order (directories only)
	lba    uint32   // directory extent
	size   uint32   // directory extent size in bytes
	num    int      // path table number, 1-based
}

type node struct {
	name   string // sanitized name
	rel    string // original relative path, slash separated
	src    string // absolute source path
	dir    bool
	size   int64
	mtime  time.Time
	parent *node
	kids   []*node
	t      [2]treeData

	fileLBA uint32 // first sector of the file data (0 for empty files)

	// FAT32
	short   [11]byte
	lfn     []uint16 // nil when the short name alone is exact
	lfnN    int
	cluster uint32
	nclus   uint32
}

type scanResult struct {
	root     *node
	files    int
	dirs     int
	skipped  []string
	bootable bool
	total    int64   // bytes of file data
	allDirs  []*node // depth first pre-order, root first
	allFiles []*node // depth first pre-order
}

// sanitizeName normalizes a file name to NFC and replaces characters that are
// invalid in Joliet or FAT as well as a trailing dot or space with '_'.
func sanitizeName(s string) string {
	s = norm.NFC.String(strings.ToValidUTF8(s, "_"))
	r := []rune(s)
	for i, c := range r {
		if unicode.IsControl(c) || strings.ContainsRune(`*/:;?\"<>|`, c) {
			r[i] = '_'
		}
	}
	for i := len(r) - 1; i >= 0 && (r[i] == '.' || r[i] == ' '); i-- {
		r[i] = '_'
	}
	return string(r)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func toUTF16(s string) []uint16 { return utf16.Encode([]rune(s)) }

func scan(ctx context.Context, dir string) (*scanResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &PathError{Path: ".", Err: os.ErrInvalid}
	}
	s := &scanResult{root: &node{dir: true, src: abs, mtime: fi.ModTime()}}
	s.allDirs = append(s.allDirs, s.root)
	if err := s.walk(ctx, s.root); err != nil {
		return nil, err
	}
	if s.files == 0 {
		return nil, ErrEmpty
	}
	s.bootable = s.findBoot()
	return s, nil
}

func (s *scanResult) walk(ctx context.Context, n *node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ents, err := os.ReadDir(n.src)
	if err != nil {
		return &PathError{Path: relOrDot(n.rel), Err: err}
	}
	seen := make(map[string]struct{}, len(ents))
	for _, e := range ents {
		rel := e.Name()
		if n.rel != "" {
			rel = n.rel + "/" + e.Name()
		}
		typ := e.Type()
		if !typ.IsRegular() && !typ.IsDir() {
			s.skipped = append(s.skipped, rel)
			continue
		}
		info, err := e.Info()
		if err != nil {
			return &PathError{Path: rel, Err: err}
		}
		k := &node{
			name:   sanitizeName(e.Name()),
			rel:    rel,
			src:    filepath.Join(n.src, e.Name()),
			dir:    typ.IsDir(),
			mtime:  info.ModTime(),
			parent: n,
		}
		if !k.dir {
			k.size = info.Size()
			if k.size > maxFileSize {
				return &PathError{Path: rel, Err: ErrFileTooLarge}
			}
		}
		if utf16Len(k.name) > maxJolietUnits {
			return &PathError{Path: rel, Err: ErrNameTooLong}
		}
		key := strings.ToLower(k.name)
		if _, dup := seen[key]; dup {
			return &PathError{Path: rel, Err: ErrNameConflict}
		}
		seen[key] = struct{}{}
		n.kids = append(n.kids, k)
		if k.dir {
			s.dirs++
			s.allDirs = append(s.allDirs, k)
			if err := s.walk(ctx, k); err != nil {
				return err
			}
		} else {
			s.files++
			s.total += k.size
			s.allFiles = append(s.allFiles, k)
		}
	}
	return nil
}

func relOrDot(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func (s *scanResult) findBoot() bool {
	child := func(n *node, name string, dir bool) *node {
		for _, k := range n.kids {
			if k.dir == dir && strings.EqualFold(k.name, name) {
				return k
			}
		}
		return nil
	}
	n := child(s.root, "EFI", true)
	if n == nil {
		return false
	}
	if n = child(n, "BOOT", true); n == nil {
		return false
	}
	return child(n, "BOOTX64.EFI", false) != nil
}
