package mkiso

import (
	"context"
	"encoding/binary"
	"time"
)

// FAT32 geometry: 512-byte sectors, fixed 2048-byte clusters (4 sectors), two
// FATs. The reserved area is 32 sectors, or 34 if needed so that the data
// region starts on a 2048-byte boundary: with the image itself starting on a
// 2048-byte boundary in the ISO, every cluster is then one aligned ISO sector.
// At least 65525 clusters are used (FAT32 by definition); unused clusters are
// not written (sparse hole). Files and directories are contiguous cluster
// chains: root, directories, then files.
const (
	fatClusterSize = 2048
	fatMinClusters = 65525
	fatMaxClusters = 0x0FFFFFF5
	fatEOC         = 0x0FFFFFFF
	fatMaxEntries  = 65536
)

type fatPlan struct {
	nclus  uint32 // data clusters in the image
	used   uint32 // clusters holding directories and files
	fatSz  uint32 // sectors (512 bytes) per FAT
	rsv    uint32 // reserved sectors (512 bytes)
	fat    []uint32
	label  [11]byte
	serial uint32
}

func (p *fatPlan) dataBytes() int64  { return int64(p.rsv+2*p.fatSz) * 512 }
func (p *fatPlan) imageBytes() int64 { return p.dataBytes() + int64(p.nclus)*fatClusterSize }

func (b *builder) setFATName(k *node, s short83) {
	var sh [11]byte
	for i := range sh {
		sh[i] = ' '
	}
	copy(sh[:8], s.base)
	copy(sh[8:], s.ext)
	k.short = sh
	k.lfn = nil
	k.lfnN = 0
	if !(s.natural && s.String() == k.name) {
		k.lfn = toUTF16(k.name)
		k.lfnN = (len(k.lfn) + 12) / 13
	}
}

func (b *builder) planFAT() error {
	p := &fatPlan{label: fatLabel(b.vol), serial: uint32(b.now.Unix())}
	c := uint32(2)
	for _, d := range b.t.allDirs {
		entries := 2
		if d.parent == nil {
			entries = 1 // volume label
		}
		for _, k := range d.kids {
			entries += 1 + k.lfnN
		}
		if entries > fatMaxEntries {
			return &PathError{Path: relOrDot(d.rel), Err: ErrDirTooLarge}
		}
		d.nclus = uint32((int64(entries)*32 + fatClusterSize - 1) / fatClusterSize)
		d.cluster = c
		c += d.nclus
	}
	for _, f := range b.t.allFiles {
		if f.size == 0 {
			continue
		}
		f.nclus = uint32((f.size + fatClusterSize - 1) / fatClusterSize)
		f.cluster = c
		if uint64(c)+uint64(f.nclus) > fatMaxClusters {
			return ErrTooLarge
		}
		c += f.nclus
	}
	p.used = c - 2
	p.nclus = max(p.used, fatMinClusters)
	if p.nclus > fatMaxClusters {
		return ErrTooLarge
	}
	p.fatSz = (p.nclus + 2) * 4 / 512
	if (p.nclus+2)*4%512 != 0 {
		p.fatSz++
	}
	p.rsv = 32
	if (p.rsv+2*p.fatSz)%4 != 0 {
		p.rsv = 34
	}
	p.fat = make([]uint32, p.used+2)
	p.fat[0], p.fat[1] = 0x0FFFFFF8, fatEOC
	chain := func(n *node) {
		for i := uint32(0); i < n.nclus; i++ {
			next := n.cluster + i + 1
			if i == n.nclus-1 {
				next = fatEOC
			}
			p.fat[n.cluster+i] = next
		}
	}
	for _, d := range b.t.allDirs {
		chain(d)
	}
	for _, f := range b.t.allFiles {
		chain(f)
	}
	b.fat = p
	return nil
}

func dosDateTime(t time.Time) (date, tm uint16) {
	t = t.UTC()
	y := t.Year()
	if y < 1980 {
		return 1<<5 | 1, 0
	}
	if y > 2107 {
		y = 2107
		t = time.Date(2107, 12, 31, 23, 59, 58, 0, time.UTC)
	}
	return uint16(y-1980)<<9 | uint16(t.Month())<<5 | uint16(t.Day()),
		uint16(t.Hour())<<11 | uint16(t.Minute())<<5 | uint16(t.Second()/2)
}

func shortChecksum(n [11]byte) byte {
	var s byte
	for _, c := range n {
		s = (s>>1 | s<<7) + c
	}
	return s
}

func putDirEntry(dst []byte, name [11]byte, attr byte, mtime time.Time, cluster, size uint32) {
	copy(dst, name[:])
	dst[11] = attr
	date, tm := dosDateTime(mtime)
	binary.LittleEndian.PutUint16(dst[14:], tm)
	binary.LittleEndian.PutUint16(dst[16:], date)
	binary.LittleEndian.PutUint16(dst[18:], date)
	binary.LittleEndian.PutUint16(dst[20:], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(dst[22:], tm)
	binary.LittleEndian.PutUint16(dst[24:], date)
	binary.LittleEndian.PutUint16(dst[26:], uint16(cluster))
	binary.LittleEndian.PutUint32(dst[28:], size)
}

func (b *builder) fatDir(d *node) []byte {
	buf := make([]byte, int(d.nclus)*fatClusterSize)
	off := 0
	if d.parent == nil {
		putDirEntry(buf[off:], b.fat.label, 0x08, d.mtime, 0, 0)
		off += 32
	} else {
		putDirEntry(buf[off:], [11]byte{'.', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '}, 0x10, d.mtime, d.cluster, 0)
		off += 32
		pc := uint32(0)
		if d.parent.parent != nil {
			pc = d.parent.cluster
		}
		putDirEntry(buf[off:], [11]byte{'.', '.', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '}, 0x10, d.parent.mtime, pc, 0)
		off += 32
	}
	for _, k := range d.kids {
		if k.lfn != nil {
			sum := shortChecksum(k.short)
			for i := k.lfnN; i >= 1; i-- {
				e := buf[off : off+32]
				e[0] = byte(i)
				if i == k.lfnN {
					e[0] |= 0x40
				}
				e[11], e[13] = 0x0F, sum
				for j := 0; j < 13; j++ {
					idx := (i-1)*13 + j
					u := uint16(0xFFFF)
					if idx < len(k.lfn) {
						u = k.lfn[idx]
					} else if idx == len(k.lfn) {
						u = 0
					}
					var pos int
					switch {
					case j < 5:
						pos = 1 + 2*j
					case j < 11:
						pos = 14 + 2*(j-5)
					default:
						pos = 28 + 2*(j-11)
					}
					binary.LittleEndian.PutUint16(e[pos:], u)
				}
				off += 32
			}
		}
		if k.dir {
			putDirEntry(buf[off:], k.short, 0x10, k.mtime, k.cluster, 0)
		} else {
			putDirEntry(buf[off:], k.short, 0x20, k.mtime, k.cluster, uint32(k.size))
		}
		off += 32
	}
	return buf
}

func (b *builder) fatReserved() []byte {
	p := b.fat
	buf := make([]byte, p.rsv*512)
	bs := buf[:512]
	copy(bs, []byte{0xEB, 0x58, 0x90})
	copy(bs[3:], "OPENLAMT")
	binary.LittleEndian.PutUint16(bs[11:], 512)
	bs[13] = fatClusterSize / 512
	binary.LittleEndian.PutUint16(bs[14:], uint16(p.rsv))
	bs[16] = 2
	bs[21] = 0xF8
	binary.LittleEndian.PutUint16(bs[24:], 63)
	binary.LittleEndian.PutUint16(bs[26:], 255)
	binary.LittleEndian.PutUint32(bs[32:], p.rsv+2*p.fatSz+4*p.nclus)
	binary.LittleEndian.PutUint32(bs[36:], p.fatSz)
	binary.LittleEndian.PutUint32(bs[44:], 2)
	binary.LittleEndian.PutUint16(bs[48:], 1)
	binary.LittleEndian.PutUint16(bs[50:], 6)
	bs[64] = 0x80
	bs[66] = 0x29
	binary.LittleEndian.PutUint32(bs[67:], p.serial)
	copy(bs[71:], p.label[:])
	copy(bs[82:], "FAT32   ")
	bs[510], bs[511] = 0x55, 0xAA

	fs := buf[512:1024]
	binary.LittleEndian.PutUint32(fs[0:], 0x41615252)
	binary.LittleEndian.PutUint32(fs[484:], 0x61417272)
	free := p.nclus - p.used
	next := uint32(0xFFFFFFFF)
	if free > 0 {
		next = p.used + 2
	}
	binary.LittleEndian.PutUint32(fs[488:], free)
	binary.LittleEndian.PutUint32(fs[492:], next)
	binary.LittleEndian.PutUint32(fs[508:], 0xAA550000)

	copy(buf[6*512:], bs)
	copy(buf[7*512:], fs)
	return buf
}

func (b *builder) writeFAT(ctx context.Context, o *out, cp *copier) error {
	p := b.fat
	start := o.pos
	if _, err := o.Write(b.fatReserved()); err != nil {
		return err
	}
	chunk := make([]byte, 64<<10)
	for c := int64(0); c < 2; c++ {
		for i := 0; i < len(p.fat); {
			n := min(len(p.fat)-i, len(chunk)/4)
			for j := 0; j < n; j++ {
				binary.LittleEndian.PutUint32(chunk[4*j:], p.fat[i+j])
			}
			if _, err := o.Write(chunk[:4*n]); err != nil {
				return err
			}
			i += n
		}
		if err := o.skipTo(start + int64(p.rsv)*512 + (c+1)*int64(p.fatSz)*512); err != nil {
			return err
		}
	}
	for _, d := range b.t.allDirs {
		if _, err := o.Write(b.fatDir(d)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	for _, f := range b.t.allFiles {
		if f.size == 0 {
			continue
		}
		if err := cp.file(f); err != nil {
			return err
		}
		if err := o.zeros(int64(f.nclus)*fatClusterSize - f.size); err != nil {
			return err
		}
	}
	return nil
}
