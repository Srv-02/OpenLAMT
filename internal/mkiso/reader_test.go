package mkiso

// Test-only reader for ISO9660, Joliet, El Torito and FAT32. It is written
// independently of the builder and is deliberately strict.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

var (
	le16 = binary.LittleEndian.Uint16
	le32 = binary.LittleEndian.Uint32
	be16 = binary.BigEndian.Uint16
	be32 = binary.BigEndian.Uint32
)

func readAt(t *testing.T, r io.ReaderAt, off int64, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	got, err := r.ReadAt(b, off)
	if got != n {
		t.Fatalf("read %d bytes at %d: got %d, %v", n, off, got, err)
	}
	return b
}

type fileSum struct {
	size int64
	sum  [32]byte
}

func hashRange(t *testing.T, r io.ReaderAt, off, n int64) fileSum {
	t.Helper()
	h := sha256.New()
	got, err := io.Copy(h, io.NewSectionReader(r, off, n))
	if err != nil || got != n {
		t.Fatalf("hash %d bytes at %d: got %d, %v", n, off, got, err)
	}
	var fs fileSum
	fs.size = n
	copy(fs.sum[:], h.Sum(nil))
	return fs
}

// ---- ISO9660 / Joliet ----

type dirRec struct {
	id    []byte
	flags byte
	lba   uint32
	size  uint32
}

func parseDirExtent(t *testing.T, data []byte) []dirRec {
	t.Helper()
	var out []dirRec
	off := 0
	for off < len(data) {
		l := int(data[off])
		if l == 0 {
			off = (off/2048 + 1) * 2048
			continue
		}
		if off%2048+l > 2048 {
			t.Fatalf("directory record crosses a sector boundary at %d", off)
		}
		rec := data[off : off+l]
		if l%2 != 0 {
			t.Fatalf("odd directory record length %d", l)
		}
		if le32(rec[2:]) != be32(rec[6:]) || le32(rec[10:]) != be32(rec[14:]) {
			t.Fatalf("both-endian mismatch in directory record")
		}
		if le16(rec[28:]) != 1 || be16(rec[30:]) != 1 {
			t.Fatalf("volume sequence number is not 1")
		}
		idLen := int(rec[32])
		want := 33 + idLen
		if idLen%2 == 0 {
			want++
		}
		if l != want {
			t.Fatalf("record length %d, want %d (id length %d)", l, want, idLen)
		}
		out = append(out, dirRec{
			id:    append([]byte(nil), rec[33:33+idLen]...),
			flags: rec[25],
			lba:   le32(rec[2:]),
			size:  le32(rec[10:]),
		})
		off += l
	}
	return out
}

type tnode struct {
	name   string
	id     []byte
	dir    bool
	lba    uint32
	size   uint32
	kids   []*tnode
	parent *tnode
}

func idUnits(id []byte, joliet bool) []uint16 {
	if joliet {
		u := make([]uint16, len(id)/2)
		for i := range u {
			u[i] = be16(id[2*i:])
		}
		return u
	}
	u := make([]uint16, len(id))
	for i, c := range id {
		u[i] = uint16(c)
	}
	return u
}

// refLess implements the ECMA-119 ordering: name part, then extension, each
// padded with spaces; version ignored (all versions are 1).
func refLess(a, b []uint16) bool {
	split := func(u []uint16) (n, e []uint16) {
		for i, c := range u {
			if c == ';' {
				u = u[:i]
				break
			}
		}
		for i := len(u) - 1; i >= 0; i-- {
			if u[i] == '.' {
				return u[:i], u[i+1:]
			}
		}
		return u, nil
	}
	cmp := func(x, y []uint16) int {
		l := len(x)
		if len(y) > l {
			l = len(y)
		}
		for i := 0; i < l; i++ {
			var p, q uint16 = ' ', ' '
			if i < len(x) {
				p = x[i]
			}
			if i < len(y) {
				q = y[i]
			}
			if p != q {
				if p < q {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	an, ae := split(a)
	bn, be := split(b)
	if c := cmp(an, bn); c != 0 {
		return c < 0
	}
	return cmp(ae, be) < 0
}

func decodeName(t *testing.T, id []byte, joliet, dir bool) string {
	t.Helper()
	var s string
	if joliet {
		s = string(utf16.Decode(idUnits(id, true)))
	} else {
		s = string(id)
	}
	if !dir {
		if !strings.HasSuffix(s, ";1") {
			t.Fatalf("file identifier %q lacks ;1", s)
		}
		s = strings.TrimSuffix(s, ";1")
	}
	return s
}

func readISOTree(t *testing.T, r io.ReaderAt, root dirRec, joliet bool) *tnode {
	t.Helper()
	top := &tnode{dir: true, lba: root.lba, size: root.size, id: []byte{0}}
	var walk func(n *tnode)
	walk = func(n *tnode) {
		if n.size%2048 != 0 || n.size == 0 {
			t.Fatalf("directory extent size %d is not a sector multiple", n.size)
		}
		recs := parseDirExtent(t, readAt(t, r, int64(n.lba)*2048, int(n.size)))
		if len(recs) < 2 || len(recs[0].id) != 1 || recs[0].id[0] != 0 || len(recs[1].id) != 1 || recs[1].id[0] != 1 {
			t.Fatalf("directory at %d lacks . and ..", n.lba)
		}
		if recs[0].lba != n.lba || recs[0].size != n.size {
			t.Fatalf("'.' record does not point to its directory")
		}
		pl := n.lba
		if n.parent != nil {
			pl = n.parent.lba
		}
		if recs[1].lba != pl {
			t.Fatalf("'..' record points to %d, want %d", recs[1].lba, pl)
		}
		var prev []uint16
		for _, rc := range recs[2:] {
			k := &tnode{id: rc.id, dir: rc.flags&2 != 0, lba: rc.lba, size: rc.size, parent: n}
			k.name = decodeName(t, rc.id, joliet, k.dir)
			u := idUnits(rc.id, joliet)
			if prev != nil && !refLess(prev, u) {
				t.Fatalf("directory records not strictly sorted at %q", k.name)
			}
			prev = u
			n.kids = append(n.kids, k)
			if k.dir {
				walk(k)
			}
		}
	}
	walk(top)
	return top
}

func (n *tnode) sig() string {
	if !n.dir {
		return fmt.Sprintf("f:%d:%d", n.lba, n.size)
	}
	s := make([]string, len(n.kids))
	for i, k := range n.kids {
		s[i] = k.sig()
	}
	sort.Strings(s)
	return "d(" + strings.Join(s, ",") + ")"
}

func (n *tnode) collect(prefix string, files map[string]*tnode, dirs map[string]bool) {
	for _, k := range n.kids {
		p := prefix + k.name
		if k.dir {
			dirs[p] = true
			k.collect(p+"/", files, dirs)
		} else {
			if _, dup := files[p]; dup {
				panic("duplicate " + p)
			}
			files[p] = k
		}
	}
}

func (n *tnode) bfs() []*tnode {
	out := []*tnode{n}
	for i := 0; i < len(out); i++ {
		for _, k := range out[i].kids {
			if k.dir {
				out = append(out, k)
			}
		}
	}
	return out
}

type ptRec struct {
	extent uint32
	parent uint16
	id     []byte
}

func parsePathTable(t *testing.T, data []byte, big bool) []ptRec {
	t.Helper()
	var out []ptRec
	for off := 0; off < len(data); {
		l := int(data[off])
		if l == 0 {
			t.Fatalf("zero-length path table record")
		}
		if data[off+1] != 0 {
			t.Fatalf("extended attribute length in path table")
		}
		var ext uint32
		var par uint16
		if big {
			ext, par = be32(data[off+2:]), be16(data[off+6:])
		} else {
			ext, par = le32(data[off+2:]), le16(data[off+6:])
		}
		out = append(out, ptRec{ext, par, append([]byte(nil), data[off+8:off+8+l]...)})
		off += 8 + l
		if l%2 == 1 {
			off++
		}
	}
	return out
}

type vd struct{ raw []byte }

func readVDs(t *testing.T, r io.ReaderAt) []vd {
	t.Helper()
	var out []vd
	for lba := int64(16); lba < 40; lba++ {
		raw := readAt(t, r, lba*2048, 2048)
		if string(raw[1:6]) != "CD001" || raw[6] != 1 {
			t.Fatalf("volume descriptor at %d has bad magic", lba)
		}
		out = append(out, vd{raw})
		if raw[0] == 255 {
			return out
		}
	}
	t.Fatalf("no volume descriptor set terminator")
	return nil
}

func (v vd) rootRec(t *testing.T) dirRec {
	recs := parseDirExtent(t, v.raw[156:190])
	if len(recs) != 1 {
		t.Fatalf("bad root record")
	}
	return recs[0]
}

func checkPathTables(t *testing.T, r io.ReaderAt, v vd, root *tnode, joliet bool) {
	t.Helper()
	size := le32(v.raw[132:])
	if size != be32(v.raw[136:]) {
		t.Fatalf("path table size both-endian mismatch")
	}
	l := parsePathTable(t, readAt(t, r, int64(le32(v.raw[140:]))*2048, int(size)), false)
	m := parsePathTable(t, readAt(t, r, int64(be32(v.raw[148:]))*2048, int(size)), true)
	if fmt.Sprint(l) != fmt.Sprint(m) {
		t.Fatalf("L and M path tables differ")
	}
	dirs := root.bfs()
	if len(l) != len(dirs) {
		t.Fatalf("path table has %d records, tree has %d directories", len(l), len(dirs))
	}
	byLBA := map[uint32]int{}
	for i, d := range dirs {
		byLBA[d.lba] = i
	}
	prevParent := uint16(0)
	for i, rec := range l {
		idx, ok := byLBA[rec.extent]
		if !ok {
			t.Fatalf("path table extent %d is not a directory", rec.extent)
		}
		d := dirs[idx]
		if i == 0 {
			if idx != 0 || rec.parent != 1 || len(rec.id) != 1 || rec.id[0] != 0 {
				t.Fatalf("bad root path table record")
			}
			continue
		}
		if string(rec.id) != string(d.id) {
			t.Fatalf("path table id %q != directory id %q", rec.id, d.id)
		}
		if rec.parent < 1 || int(rec.parent) > len(l) || l[rec.parent-1].extent != d.parent.lba {
			t.Fatalf("path table parent number wrong for %q", d.name)
		}
		if rec.parent < prevParent {
			t.Fatalf("path table not ordered by parent number")
		}
		prevParent = rec.parent
	}
}

// checkCatalog validates the El Torito boot catalog and returns the load RBA.
func checkCatalog(t *testing.T, r io.ReaderAt) (rba uint32, sectors uint16) {
	t.Helper()
	br := readAt(t, r, 17*2048, 2048)
	if br[0] != 0 || string(br[1:6]) != "CD001" || br[6] != 1 {
		t.Fatalf("no boot record volume descriptor at sector 17")
	}
	if string(br[7:30]) != "EL TORITO SPECIFICATION" {
		t.Fatalf("boot system id = %q", br[7:39])
	}
	for _, c := range br[30:71] {
		if c != 0 {
			t.Fatalf("boot record not zero padded")
		}
	}
	cat := readAt(t, r, int64(le32(br[71:]))*2048, 64)
	if cat[0] != 1 || cat[1] != 0xEF || cat[2] != 0 || cat[3] != 0 || cat[30] != 0x55 || cat[31] != 0xAA {
		t.Fatalf("bad validation entry: % x", cat[:32])
	}
	var sum uint16
	for i := 0; i < 32; i += 2 {
		sum += le16(cat[i:])
	}
	if sum != 0 {
		t.Fatalf("validation entry checksum sum = %#x", sum)
	}
	e := cat[32:64]
	if e[0] != 0x88 || e[1] != 0 || le16(e[2:]) != 0 || e[4] != 0 {
		t.Fatalf("bad default entry: % x", e)
	}
	return le32(e[8:]), le16(e[6:])
}

// ---- FAT32 ----

type fentry struct {
	name    string
	short   string
	dir     bool
	cluster uint32
	size    uint32
}

type fatFS struct {
	t       *testing.T
	r       io.ReaderAt
	base    int64
	rsv     int64
	fatSz   int64
	nclus   int64
	totSec  int64
	fat     []uint32
	dataOff int64 // relative to base
	label   string
}

func openFAT(t *testing.T, r io.ReaderAt, base int64, isoSize int64) *fatFS {
	t.Helper()
	bs := readAt(t, r, base, 512)
	if bs[510] != 0x55 || bs[511] != 0xAA {
		t.Fatalf("boot sector signature missing")
	}
	f := &fatFS{t: t, r: r, base: base}
	chk := func(ok bool, msg string) {
		t.Helper()
		if !ok {
			t.Fatalf("BPB: %s", msg)
		}
	}
	chk(le16(bs[11:]) == 512, "bytes per sector")
	chk(bs[13] == 4, "sectors per cluster")
	f.rsv = int64(le16(bs[14:]))
	chk(bs[16] == 2, "number of FATs")
	chk(le16(bs[17:]) == 0 && le16(bs[19:]) == 0 && le16(bs[22:]) == 0, "FAT12/16 fields")
	chk(bs[21] == 0xF8, "media byte")
	f.totSec = int64(le32(bs[32:]))
	f.fatSz = int64(le32(bs[36:]))
	chk(le32(bs[44:]) == 2, "root cluster")
	chk(le16(bs[48:]) == 1, "FSInfo sector")
	chk(le16(bs[50:]) == 6, "backup boot sector")
	chk(bs[66] == 0x29, "extended boot signature")
	chk(string(bs[82:90]) == "FAT32   ", "fs type string")
	f.label = strings.TrimRight(string(bs[71:82]), " ")
	if string(readAt(t, r, base+6*512, 512)) != string(bs) {
		t.Fatalf("backup boot sector differs")
	}
	f.dataOff = (f.rsv + 2*f.fatSz) * 512
	chk(f.dataOff%2048 == 0, "data region not 2048 aligned")
	chk(base%2048 == 0, "image not sector aligned")
	f.nclus = (f.totSec - f.rsv - 2*f.fatSz) / 4
	chk(f.nclus >= 65525, fmt.Sprintf("only %d clusters", f.nclus))
	chk((f.nclus+2)*4 <= f.fatSz*512, "FAT too small for cluster count")
	chk(base+f.totSec*512 == isoSize, fmt.Sprintf("image end %d != ISO size %d", base+f.totSec*512, isoSize))
	raw := readAt(t, r, base+f.rsv*512, int(f.fatSz*512))
	raw2 := readAt(t, r, base+(f.rsv+f.fatSz)*512, int(f.fatSz*512))
	if string(raw) != string(raw2) {
		t.Fatalf("FAT copies differ")
	}
	f.fat = make([]uint32, f.nclus+2)
	for i := range f.fat {
		f.fat[i] = le32(raw[4*i:]) & 0x0FFFFFFF
	}
	chk(f.fat[0] == 0x0FFFFFF8 && f.fat[1] >= 0x0FFFFFF8, "FAT reserved entries")
	free := int64(0)
	next := uint32(0xFFFFFFFF)
	for c := int64(2); c < f.nclus+2; c++ {
		if f.fat[c] == 0 {
			free++
			if next == 0xFFFFFFFF {
				next = uint32(c)
			}
		}
	}
	fs := readAt(t, r, base+512, 512)
	chk(le32(fs[0:]) == 0x41615252 && le32(fs[484:]) == 0x61417272 && le32(fs[508:]) == 0xAA550000, "FSInfo signatures")
	chk(int64(le32(fs[488:])) == free, fmt.Sprintf("FSInfo free count %d, real %d", le32(fs[488:]), free))
	chk(le32(fs[492:]) == next, fmt.Sprintf("FSInfo next free %d, real %d", le32(fs[492:]), next))
	if string(readAt(t, r, base+7*512, 512)) != string(fs) {
		t.Fatalf("backup FSInfo differs")
	}
	return f
}

func (f *fatFS) chain(c uint32) []uint32 {
	f.t.Helper()
	var out []uint32
	for {
		if c < 2 || int64(c) >= f.nclus+2 {
			f.t.Fatalf("bad cluster %d in chain", c)
		}
		out = append(out, c)
		n := f.fat[c]
		if n >= 0x0FFFFFF8 {
			return out
		}
		if len(out) > int(f.nclus) {
			f.t.Fatalf("FAT chain loop")
		}
		c = n
	}
}

func (f *fatFS) clusterOff(c uint32) int64 {
	return f.base + f.dataOff + int64(c-2)*2048
}

func shortSum(b []byte) byte {
	var s byte
	for _, c := range b[:11] {
		s = (s>>1 | s<<7) + c
	}
	return s
}

func (f *fatFS) readDir(c uint32, self, parent uint32) []fentry {
	t := f.t
	t.Helper()
	var raw []byte
	for _, cl := range f.chain(c) {
		raw = append(raw, readAt(t, f.r, f.clusterOff(cl), 2048)...)
	}
	var out []fentry
	type lfn struct {
		ord  int
		sum  byte
		unit []uint16
	}
	var pend []lfn
	seenShort := map[string]bool{}
	for off := 0; off+32 <= len(raw); off += 32 {
		e := raw[off : off+32]
		if e[0] == 0 {
			break
		}
		if e[0] == 0xE5 {
			continue
		}
		if e[11] == 0x0F {
			ord := int(e[0] & 0x1F)
			if e[0]&0x40 != 0 {
				pend = nil
			}
			var u []uint16
			for _, rg := range [][2]int{{1, 11}, {14, 26}, {28, 32}} {
				for i := rg[0]; i < rg[1]; i += 2 {
					u = append(u, le16(e[i:]))
				}
			}
			if le16(e[26:]) != 0 || e[12] != 0 {
				t.Fatalf("bad LFN entry fields")
			}
			pend = append(pend, lfn{ord, e[13], u})
			continue
		}
		if e[11]&0x08 != 0 {
			if c == 2 {
				f.label = strings.TrimRight(string(e[:11]), " ")
			}
			continue
		}
		short := strings.TrimRight(string(e[:8]), " ")
		if ext := strings.TrimRight(string(e[8:11]), " "); ext != "" {
			short += "." + ext
		}
		if seenShort[short] {
			t.Fatalf("duplicate short name %q", short)
		}
		seenShort[short] = true
		ent := fentry{short: short, name: short, dir: e[11]&0x10 != 0,
			cluster: uint32(le16(e[20:]))<<16 | uint32(le16(e[26:])), size: le32(e[28:])}
		if len(pend) > 0 {
			var units []uint16
			n := len(pend)
			for i, p := range pend {
				if p.ord != n-i {
					t.Fatalf("LFN sequence broken for %q", short)
				}
				if p.sum != shortSum(e) {
					t.Fatalf("LFN checksum mismatch for %q", short)
				}
			}
			for i := n - 1; i >= 0; i-- {
				units = append(units, pend[i].unit...)
			}
			end := len(units)
			for i, u := range units {
				if u == 0 {
					end = i
					for _, pad := range units[i+1:] {
						if pad != 0xFFFF {
							t.Fatalf("LFN padding wrong for %q", short)
						}
					}
					break
				}
			}
			if end == len(units) && len(units) != 13*n {
				t.Fatalf("LFN length")
			}
			ent.name = string(utf16.Decode(units[:end]))
			pend = nil
		}
		if len(pend) != 0 {
			t.Fatalf("orphan LFN entries")
		}
		switch ent.name {
		case ".":
			if ent.cluster != self {
				t.Fatalf("'.' cluster %d, want %d", ent.cluster, self)
			}
			continue
		case "..":
			if ent.cluster != parent {
				t.Fatalf("'..' cluster %d, want %d", ent.cluster, parent)
			}
			continue
		}
		out = append(out, ent)
	}
	return out
}

func (f *fatFS) walk(c, parent uint32, prefix string, files map[string]fentry, dirs map[string]bool) {
	t := f.t
	for _, e := range f.readDir(c, c, parent) {
		p := prefix + e.name
		if e.dir {
			dirs[p] = true
			pp := c
			if c == 2 {
				pp = 0
			}
			f.walk(e.cluster, pp, p+"/", files, dirs)
			continue
		}
		files[p] = e
		if e.size == 0 {
			if e.cluster != 0 {
				t.Fatalf("empty file %q has cluster %d", p, e.cluster)
			}
			continue
		}
		ch := f.chain(e.cluster)
		if int64(len(ch)) != (int64(e.size)+2047)/2048 {
			t.Fatalf("%q: chain of %d clusters for %d bytes", p, len(ch), e.size)
		}
		for i := 1; i < len(ch); i++ {
			if ch[i] != ch[i-1]+1 {
				t.Fatalf("%q: chain not contiguous", p)
			}
		}
	}
}

// ---- full verification ----

type verifyOpts struct {
	boot       bool
	extraDirs  []string
	pvdVolume  string
	jolVolume  string
	fatLabel   string
	checkFsck  bool
	isoSizeInf int64
}

func verifyISO(t *testing.T, path string, want map[string]fileSum, o verifyOpts) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	isoSize := st.Size()
	if isoSize%2048 != 0 {
		t.Fatalf("ISO size %d not a sector multiple", isoSize)
	}
	if o.isoSizeInf != 0 && o.isoSizeInf != isoSize {
		t.Fatalf("Info.Size %d != file size %d", o.isoSizeInf, isoSize)
	}
	if got := readAt(t, f, 0, 16*2048); strings.Trim(string(got), "\x00") != "" {
		t.Fatalf("system area not zero")
	}
	vds := readVDs(t, f)
	var pvd, svd *vd
	hasBoot := false
	for i := range vds {
		switch vds[i].raw[0] {
		case 1:
			pvd = &vds[i]
		case 2:
			svd = &vds[i]
		case 0:
			hasBoot = true
		}
	}
	if pvd == nil || svd == nil {
		t.Fatalf("missing PVD or SVD")
	}
	if hasBoot != o.boot {
		t.Fatalf("boot record present=%v, want %v", hasBoot, o.boot)
	}
	if string(svd.raw[88:91]) != "%/E" {
		t.Fatalf("Joliet escape sequence = %q", svd.raw[88:91])
	}
	for _, v := range []*vd{pvd, svd} {
		if uint32(isoSize/2048) != le32(v.raw[80:]) || le32(v.raw[80:]) != be32(v.raw[84:]) {
			t.Fatalf("volume space size %d, file has %d sectors", le32(v.raw[80:]), isoSize/2048)
		}
		if le16(v.raw[120:]) != 1 || be16(v.raw[122:]) != 1 || le16(v.raw[124:]) != 1 || be16(v.raw[126:]) != 1 {
			t.Fatalf("volume set size/sequence not 1")
		}
		if le16(v.raw[128:]) != 2048 || be16(v.raw[130:]) != 2048 {
			t.Fatalf("logical block size not 2048")
		}
		if v.raw[881] != 1 {
			t.Fatalf("file structure version")
		}
	}
	if o.pvdVolume != "" {
		if got := strings.TrimRight(string(pvd.raw[40:72]), " "); got != o.pvdVolume {
			t.Fatalf("PVD volume id %q, want %q", got, o.pvdVolume)
		}
	}
	if o.jolVolume != "" {
		got := strings.TrimRight(string(utf16.Decode(idUnits(svd.raw[40:72], true))), " ")
		if got != o.jolVolume {
			t.Fatalf("Joliet volume id %q, want %q", got, o.jolVolume)
		}
	}

	ptree := readISOTree(t, f, pvd.rootRec(t), false)
	jtree := readISOTree(t, f, svd.rootRec(t), true)
	checkPathTables(t, f, *pvd, ptree, false)
	checkPathTables(t, f, *svd, jtree, true)
	if ptree.sig() != jtree.sig() {
		t.Fatalf("primary and Joliet trees differ in structure or extents")
	}
	for _, d := range ptree.bfs() {
		for _, k := range d.kids {
			if k.dir {
				continue
			}
			if len(k.name) > 12 || strings.Count(k.name, ".") != 1 {
				t.Fatalf("primary file name %q is not 8.3", k.name)
			}
			for _, c := range k.name {
				if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '~') {
					t.Fatalf("primary name %q has invalid character", k.name)
				}
			}
		}
	}

	jfiles := map[string]*tnode{}
	jdirs := map[string]bool{}
	jtree.collect("", jfiles, jdirs)
	if len(jfiles) != len(want) {
		var got []string
		for p := range jfiles {
			got = append(got, p)
		}
		sort.Strings(got)
		t.Fatalf("Joliet has %d files %q, want %d", len(jfiles), got, len(want))
	}
	wantDirs := map[string]bool{}
	for p := range want {
		for i, c := range p {
			if c == '/' {
				wantDirs[p[:i]] = true
			}
		}
	}
	for _, d := range o.extraDirs {
		wantDirs[d] = true
		for i, c := range d {
			if c == '/' {
				wantDirs[d[:i]] = true
			}
		}
	}
	if fmt.Sprint(sortedKeys(jdirs)) != fmt.Sprint(sortedKeys(wantDirs)) {
		t.Fatalf("Joliet dirs %v, want %v", sortedKeys(jdirs), sortedKeys(wantDirs))
	}
	for p, w := range want {
		n, ok := jfiles[p]
		if !ok {
			t.Fatalf("Joliet lacks %q", p)
		}
		if int64(n.size) != w.size {
			t.Fatalf("%q: size %d, want %d", p, n.size, w.size)
		}
		if w.size > 0 {
			if int64(n.lba)*2048+int64(n.size) > isoSize {
				t.Fatalf("%q extends beyond the image", p)
			}
			if got := hashRange(t, f, int64(n.lba)*2048, w.size); got != w {
				t.Fatalf("%q: Joliet content differs", p)
			}
		}
	}
	// primary tree content via extents (names differ, so match by extent)
	pfiles := map[string]*tnode{}
	pdirs := map[string]bool{}
	ptree.collect("", pfiles, pdirs)
	byExtent := map[[2]uint32]fileSum{}
	for p, n := range jfiles {
		if n.size > 0 {
			byExtent[[2]uint32{n.lba, n.size}] = want[p]
		}
	}
	for p, n := range pfiles {
		if n.size == 0 {
			continue
		}
		w, ok := byExtent[[2]uint32{n.lba, n.size}]
		if !ok {
			t.Fatalf("primary file %q has no Joliet counterpart", p)
		}
		if hashRange(t, f, int64(n.lba)*2048, int64(n.size)) != w {
			t.Fatalf("primary %q content differs", p)
		}
	}

	if !o.boot {
		// non-bootable: every extent is sector aligned (by construction) and after the metadata
		return
	}
	rba, sectors := checkCatalog(t, f)
	if sectors != 0 {
		t.Fatalf("catalog sector count %d, want 0", sectors)
	}
	fs := openFAT(t, f, int64(rba)*2048, isoSize)
	if o.fatLabel != "" && fs.label != o.fatLabel {
		t.Fatalf("FAT label %q, want %q", fs.label, o.fatLabel)
	}
	ffiles := map[string]fentry{}
	fdirs := map[string]bool{}
	fs.walk(2, 0, "", ffiles, fdirs)
	if fmt.Sprint(sortedKeys(fdirs)) != fmt.Sprint(sortedKeys(wantDirs)) {
		t.Fatalf("FAT dirs %v, want %v", sortedKeys(fdirs), sortedKeys(wantDirs))
	}
	if len(ffiles) != len(want) {
		t.Fatalf("FAT has %d files, want %d", len(ffiles), len(want))
	}
	if o.fatLabel != "" && fs.label != o.fatLabel {
		t.Fatalf("root label entry %q, want %q", fs.label, o.fatLabel)
	}
	for p, w := range want {
		e, ok := ffiles[p]
		if !ok {
			t.Fatalf("FAT lacks %q", p)
		}
		if int64(e.size) != w.size {
			t.Fatalf("FAT %q size %d, want %d", p, e.size, w.size)
		}
		if w.size == 0 {
			continue
		}
		off := fs.clusterOff(e.cluster)
		if off%2048 != 0 {
			t.Fatalf("FAT %q not 2048 aligned", p)
		}
		if got := hashRange(t, f, off, w.size); got != w {
			t.Fatalf("FAT %q content differs", p)
		}
		jn := jfiles[p]
		if int64(jn.lba)*2048 != off {
			t.Fatalf("%q: Joliet extent %d != FAT extent %d", p, int64(jn.lba)*2048, off)
		}
	}
	if o.checkFsck {
		maybeFsck(t, path, int64(rba)*2048, fs.totSec*512)
	}
}

func sortedKeys(m map[string]bool) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

func findFsck() string {
	if p, err := exec.LookPath("fsck.fat"); err == nil {
		return p
	}
	if _, err := os.Stat("/usr/sbin/fsck.fat"); err == nil {
		return "/usr/sbin/fsck.fat"
	}
	return ""
}

// extractImage copies the byte range of an ISO into a new sparse file.
func extractImage(t *testing.T, iso string, off, n int64) string {
	t.Helper()
	src, err := os.Open(iso)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out := t.TempDir() + "/fat.img"
	dst, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.Truncate(n); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<20)
	for pos := int64(0); pos < n; pos += int64(len(buf)) {
		l := int64(len(buf))
		if n-pos < l {
			l = n - pos
		}
		b := buf[:l]
		if _, err := src.ReadAt(b, off+pos); err != nil {
			t.Fatal(err)
		}
		zero := true
		for _, c := range b {
			if c != 0 {
				zero = false
				break
			}
		}
		if !zero {
			if _, err := dst.WriteAt(b, pos); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}

func maybeFsck(t *testing.T, iso string, off, n int64) {
	t.Helper()
	bin := findFsck()
	if bin == "" {
		return
	}
	img := extractImage(t, iso, off, n)
	out, err := exec.Command(bin, "-n", img).CombinedOutput()
	if err != nil {
		t.Fatalf("fsck.fat -n failed: %v\n%s", err, out)
	}
}

func execFsck(img string) ([]byte, error) {
	return exec.Command(findFsck(), "-n", img).CombinedOutput()
}
