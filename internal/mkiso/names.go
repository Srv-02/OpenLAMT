package mkiso

import (
	"strconv"
	"strings"
)

func isoValid(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func fatValid(c byte) bool {
	return isoValid(c) || strings.IndexByte("!#$%&'()-@^`{}~", c) >= 0
}

// make83 derives an 8.3 name. natural reports that the name can be used as is
// (no truncation; for FAT additionally nothing but the case was changed).
func make83(name string, noExt, fat bool) (base, ext string, natural bool) {
	b, e := name, ""
	if i := strings.LastIndexByte(name, '.'); i >= 0 && !noExt {
		b, e = name[:i], name[i+1:]
	}
	natural = true
	conv := func(s string) string {
		out := make([]byte, 0, len(s))
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z':
				out = append(out, byte(r-32))
			case r < 0x80 && (fat && fatValid(byte(r)) || !fat && isoValid(byte(r))):
				out = append(out, byte(r))
			case fat && (r == ' ' || r == '.'):
				natural = false
			default:
				out = append(out, '_')
				if fat {
					natural = false
				}
			}
		}
		return string(out)
	}
	base, ext = conv(b), conv(e)
	if base == "" {
		base = "_"
		natural = false
	}
	if len(base) > 8 {
		base, natural = base[:8], false
	}
	if len(ext) > 3 {
		ext, natural = ext[:3], false
	}
	return
}

type short83 struct {
	base, ext string
	natural   bool
}

// assignShort returns unique 8.3 names for kids. Entries that fit get their
// natural name; the others get a "~N" tail.
func assignShort(kids []*node, fat bool) []short83 {
	res := make([]short83, len(kids))
	count := map[string]int{}
	for i, k := range kids {
		b, e, nat := make83(k.name, k.dir && !fat, fat)
		res[i] = short83{b, e, nat}
		count[b+"."+e]++
	}
	taken := map[string]bool{}
	for i, r := range res {
		if r.natural && count[r.base+"."+r.ext] == 1 {
			taken[r.base+"."+r.ext] = true
			continue
		}
		res[i].natural = false
	}
	for i, r := range res {
		if r.natural {
			continue
		}
		for n := 1; ; n++ {
			tail := "~" + strconv.Itoa(n)
			b := r.base
			if len(b) > 8-len(tail) {
				b = b[:8-len(tail)]
			}
			b += tail
			if !taken[b+"."+r.ext] {
				taken[b+"."+r.ext] = true
				res[i].base = b
				break
			}
		}
	}
	return res
}

func (s short83) String() string {
	if s.ext == "" {
		return s.base
	}
	return s.base + "." + s.ext
}

// isoLess orders directory records per ECMA-119 9.3: name part, then
// extension, each padded with spaces. The version number is ignored.
func isoLess(a, b []uint16) bool {
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
		l := max(len(x), len(y))
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

func fatLabel(vol string) [11]byte {
	var out [11]byte
	for i := range out {
		out[i] = ' '
	}
	i := 0
	for _, r := range vol {
		if i == len(out) {
			break
		}
		switch {
		case r >= 'a' && r <= 'z':
			out[i] = byte(r - 32)
		case r < 0x80 && (fatValid(byte(r)) || r == ' '):
			out[i] = byte(r)
		default:
			out[i] = '_'
		}
		i++
	}
	if i == 0 {
		copy(out[:], "NO NAME")
	}
	return out
}

// pvdVolumeID returns the primary volume identifier: as given, non-ASCII
// characters and controls replaced with '_', at most 32 bytes.
func pvdVolumeID(vol string) []byte {
	var out []byte
	for _, r := range vol {
		if len(out) == 32 {
			break
		}
		if r < 0x20 || r >= 0x7f {
			r = '_'
		}
		out = append(out, byte(r))
	}
	return out
}

// jolietVolumeID returns the first 16 UTF-16 code units of vol.
func jolietVolumeID(vol string) []uint16 {
	u := toUTF16(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, vol))
	if len(u) > 16 {
		u = u[:16]
		if c := u[15]; c >= 0xD800 && c < 0xDC00 {
			u = u[:15]
		}
	}
	return u
}
