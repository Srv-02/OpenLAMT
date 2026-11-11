package mkiso

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

type builder struct {
	t    *scanResult
	vol  string
	boot bool

	pathOrder [2][]*node // directories in path table order (breadth first)
	pathSize  [2]uint32
	pathL     [2]uint32 // LBA of the L path table
	pathM     [2]uint32 // LBA of the M path table
	catLBA    uint32
	metaSec   uint32 // sectors of metadata
	fat       *fatPlan

	totalSectors int64
	now          time.Time
}

func newBuilder(t *scanResult, vol string) *builder {
	return &builder{t: t, vol: vol, boot: t.bootable, now: time.Now().UTC()}
}

func sectorsFor(n int64) int64 { return (n + sectorSize - 1) / sectorSize }

func recLen(idLen int) int {
	l := 33 + idLen
	if idLen%2 == 0 {
		l++
	}
	return l
}

func (b *builder) plan() error {
	root := b.t.root
	// identifiers and ordering, per directory
	for _, d := range b.t.allDirs {
		pn := assignShort(d.kids, false)
		for i, k := range d.kids {
			var id string
			if k.dir {
				id = pn[i].base
			} else {
				id = pn[i].String() + ";1"
				if pn[i].ext == "" {
					id = pn[i].base + ".;1"
				}
			}
			k.t[0].id = []byte(id)
			j := k.name
			if !k.dir {
				j += ";1"
			}
			ju := toUTF16(j)
			k.t[1].units = ju
			k.t[1].id = make([]byte, 2*len(ju))
			for x, u := range ju {
				binary.BigEndian.PutUint16(k.t[1].id[2*x:], u)
			}
			k.t[0].units = toUTF16(id)
		}
		if b.boot {
			fn := assignShort(d.kids, true)
			for i, k := range d.kids {
				b.setFATName(k, fn[i])
			}
		}
		for tr := 0; tr < 2; tr++ {
			s := append([]*node(nil), d.kids...)
			sort.SliceStable(s, func(x, y int) bool { return isoLess(s[x].t[tr].units, s[y].t[tr].units) })
			d.t[tr].sorted = s
		}
	}
	root.t[0].id, root.t[1].id = []byte{0}, []byte{0}

	// directory extent sizes and path tables
	for tr := 0; tr < 2; tr++ {
		for _, d := range b.t.allDirs {
			pos := 68 // "." and ".."
			for _, k := range d.t[tr].sorted {
				rl := recLen(len(k.t[tr].id))
				if pos%sectorSize+rl > sectorSize {
					pos = (pos/sectorSize + 1) * sectorSize
				}
				pos += rl
			}
			d.t[tr].size = uint32(sectorsFor(int64(pos)) * sectorSize)
		}
		order := []*node{root}
		for i := 0; i < len(order); i++ {
			for _, k := range order[i].t[tr].sorted {
				if k.dir {
					order = append(order, k)
				}
			}
		}
		if len(order) > 65535 {
			return ErrTooManyDirs
		}
		size := 0
		for i, d := range order {
			d.t[tr].num = i + 1
			l := 8 + len(d.t[tr].id)
			size += l + l%2
		}
		b.pathOrder[tr] = order
		b.pathSize[tr] = uint32(size)
	}

	if b.boot {
		if err := b.planFAT(); err != nil {
			return err
		}
	}

	// metadata layout
	cur := uint32(16) + 1 // PVD
	if b.boot {
		cur++ // boot record
	}
	cur += 2 // SVD, terminator
	if b.boot {
		b.catLBA = cur
		cur++
	}
	for tr := 0; tr < 2; tr++ {
		n := uint32(sectorsFor(int64(b.pathSize[tr])))
		b.pathL[tr] = cur
		cur += n
		b.pathM[tr] = cur
		cur += n
	}
	for tr := 0; tr < 2; tr++ {
		for _, d := range b.pathOrder[tr] {
			d.t[tr].lba = cur
			cur += d.t[tr].size / sectorSize
		}
	}
	b.metaSec = cur

	if b.boot {
		dataStart := int64(cur) + int64(b.fat.dataBytes())/sectorSize
		for _, f := range b.t.allFiles {
			if f.size > 0 {
				f.fileLBA = uint32(dataStart + int64(f.cluster) - 2)
			}
		}
		b.totalSectors = int64(cur) + b.fat.imageBytes()/sectorSize
	} else {
		pos := int64(cur)
		for _, f := range b.t.allFiles {
			if f.size > 0 {
				f.fileLBA = uint32(pos)
				pos += sectorsFor(f.size)
			}
		}
		b.totalSectors = pos
	}
	if b.totalSectors >= 1<<32 {
		return ErrTooLarge
	}
	return nil
}

// ---- metadata ----

func putBoth32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
	binary.BigEndian.PutUint32(b[4:], v)
}

func putBoth16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
	binary.BigEndian.PutUint16(b[2:], v)
}

func putDate7(b []byte, t time.Time) {
	t = t.UTC()
	y := t.Year() - 1900
	if y < 0 {
		y = 0
	}
	if y > 255 {
		y = 255
	}
	b[0], b[1], b[2] = byte(y), byte(t.Month()), byte(t.Day())
	b[3], b[4], b[5], b[6] = byte(t.Hour()), byte(t.Minute()), byte(t.Second()), 0
}

func putRecord(dst []byte, extent, size uint32, t time.Time, flags byte, id []byte) int {
	l := recLen(len(id))
	dst[0] = byte(l)
	putBoth32(dst[2:], extent)
	putBoth32(dst[10:], size)
	putDate7(dst[18:], t)
	dst[25] = flags
	putBoth16(dst[28:], 1)
	dst[32] = byte(len(id))
	copy(dst[33:], id)
	return l
}

func putDate17(b []byte, t time.Time) {
	t = t.UTC()
	copy(b, fmt.Sprintf("%04d%02d%02d%02d%02d%02d00", t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()))
	b[16] = 0
}

func spaces(b []byte, joliet bool) {
	if joliet {
		for i := 0; i+1 < len(b); i += 2 {
			b[i], b[i+1] = 0, ' '
		}
		if len(b)%2 == 1 {
			b[len(b)-1] = ' '
		}
		return
	}
	for i := range b {
		b[i] = ' '
	}
}

func (b *builder) volumeDescriptor(buf []byte, tr int) {
	joliet := tr == 1
	buf[0] = 1
	if joliet {
		buf[0] = 2
	}
	copy(buf[1:], "CD001")
	buf[6] = 1
	spaces(buf[8:40], joliet)
	spaces(buf[40:72], joliet)
	if joliet {
		for i, u := range jolietVolumeID(b.vol) {
			binary.BigEndian.PutUint16(buf[40+2*i:], u)
		}
		copy(buf[88:], "%/E")
	} else {
		copy(buf[40:], pvdVolumeID(b.vol))
	}
	putBoth32(buf[80:], uint32(b.totalSectors))
	putBoth16(buf[120:], 1)
	putBoth16(buf[124:], 1)
	putBoth16(buf[128:], sectorSize)
	putBoth32(buf[132:], b.pathSize[tr])
	binary.LittleEndian.PutUint32(buf[140:], b.pathL[tr])
	binary.BigEndian.PutUint32(buf[148:], b.pathM[tr])
	root := b.t.root
	putRecord(buf[156:], root.t[tr].lba, root.t[tr].size, root.mtime, 2, []byte{0})
	spaces(buf[190:318], joliet) // volume set
	spaces(buf[318:446], joliet) // publisher
	spaces(buf[446:574], joliet) // data preparer
	spaces(buf[574:702], joliet) // application
	app := "OPENLAMT"
	if joliet {
		for i, c := range app {
			binary.BigEndian.PutUint16(buf[574+2*i:], uint16(c))
		}
	} else {
		copy(buf[574:], app)
	}
	spaces(buf[702:739], false)
	spaces(buf[739:776], false)
	spaces(buf[776:813], false)
	putDate17(buf[813:], b.now)
	putDate17(buf[830:], b.now)
	copy(buf[847:], "0000000000000000")
	putDate17(buf[864:], b.now)
	buf[881] = 1
}

func (b *builder) pathTable(tr int, big bool) []byte {
	out := make([]byte, 0, b.pathSize[tr])
	for _, d := range b.pathOrder[tr] {
		id := d.t[tr].id
		rec := make([]byte, 8+len(id)+len(id)%2)
		rec[0] = byte(len(id))
		parent := 1
		if d.parent != nil {
			parent = d.parent.t[tr].num
		}
		if big {
			binary.BigEndian.PutUint32(rec[2:], d.t[tr].lba)
			binary.BigEndian.PutUint16(rec[6:], uint16(parent))
		} else {
			binary.LittleEndian.PutUint32(rec[2:], d.t[tr].lba)
			binary.LittleEndian.PutUint16(rec[6:], uint16(parent))
		}
		copy(rec[8:], id)
		out = append(out, rec...)
	}
	return out
}

func (b *builder) directory(d *node, tr int, dst []byte) {
	pos := 0
	parent := d
	if d.parent != nil {
		parent = d.parent
	}
	pos += putRecord(dst[pos:], d.t[tr].lba, d.t[tr].size, d.mtime, 2, []byte{0})
	pos += putRecord(dst[pos:], parent.t[tr].lba, parent.t[tr].size, parent.mtime, 2, []byte{1})
	for _, k := range d.t[tr].sorted {
		rl := recLen(len(k.t[tr].id))
		if pos%sectorSize+rl > sectorSize {
			pos = (pos/sectorSize + 1) * sectorSize
		}
		if k.dir {
			putRecord(dst[pos:], k.t[tr].lba, k.t[tr].size, k.mtime, 2, k.t[tr].id)
		} else {
			putRecord(dst[pos:], k.fileLBA, uint32(k.size), k.mtime, 0, k.t[tr].id)
		}
		pos += rl
	}
}

func (b *builder) metadata() []byte {
	meta := make([]byte, int(b.metaSec)*sectorSize)
	sec := func(lba uint32) []byte { return meta[int(lba)*sectorSize : int(lba+1)*sectorSize] }
	lba := uint32(16)
	b.volumeDescriptor(sec(lba), 0)
	lba++
	if b.boot {
		br := sec(lba)
		copy(br[1:], "CD001")
		br[6] = 1
		copy(br[7:], "EL TORITO SPECIFICATION")
		binary.LittleEndian.PutUint32(br[71:], b.catLBA)
		lba++
	}
	b.volumeDescriptor(sec(lba), 1)
	lba++
	term := sec(lba)
	term[0] = 255
	copy(term[1:], "CD001")
	term[6] = 1
	if b.boot {
		cat := sec(b.catLBA)
		cat[0], cat[1] = 1, 0xEF
		cat[30], cat[31] = 0x55, 0xAA
		var sum uint16
		for i := 0; i < 32; i += 2 {
			sum += binary.LittleEndian.Uint16(cat[i:])
		}
		binary.LittleEndian.PutUint16(cat[28:], -sum)
		e := cat[32:]
		e[0] = 0x88 // bootable, no emulation
		// load segment 0, system type 0, sector count 0 (EDK2 then uses the rest of the medium)
		binary.LittleEndian.PutUint32(e[8:], b.metaSec)
	}
	for tr := 0; tr < 2; tr++ {
		copy(meta[int(b.pathL[tr])*sectorSize:], b.pathTable(tr, false))
		copy(meta[int(b.pathM[tr])*sectorSize:], b.pathTable(tr, true))
		for _, d := range b.pathOrder[tr] {
			off := int(d.t[tr].lba) * sectorSize
			b.directory(d, tr, meta[off:off+int(d.t[tr].size)])
		}
	}
	return meta
}

// ---- output ----

type out struct {
	w    io.WriteSeeker
	bw   *bufio.Writer
	base int64
	pos  int64
}

func (o *out) Write(p []byte) (int, error) {
	n, err := o.bw.Write(p)
	o.pos += int64(n)
	return n, err
}

// skipTo moves forward without writing (leaves a hole).
func (o *out) skipTo(pos int64) error {
	if pos == o.pos {
		return nil
	}
	if err := o.bw.Flush(); err != nil {
		return err
	}
	if _, err := o.w.Seek(o.base+pos, io.SeekStart); err != nil {
		return err
	}
	o.pos = pos
	return nil
}

func (o *out) zeros(n int64) error {
	var z [4096]byte
	for n > 0 {
		c := int64(len(z))
		if n < c {
			c = n
		}
		if _, err := o.Write(z[:c]); err != nil {
			return err
		}
		n -= c
	}
	return nil
}

type copier struct {
	ctx      context.Context
	o        *out
	buf      []byte
	progress func(done, total int64)
	done     int64
	total    int64
}

func (c *copier) file(n *node) error {
	f, err := os.Open(n.src)
	if err != nil {
		return &PathError{Path: n.rel, Err: err}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return &PathError{Path: n.rel, Err: err}
	}
	if st.Size() != n.size {
		return &PathError{Path: n.rel, Err: ErrFileChanged}
	}
	for left := n.size; left > 0; {
		chunk := c.buf
		if int64(len(chunk)) > left {
			chunk = chunk[:left]
		}
		if _, err := io.ReadFull(f, chunk); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = ErrFileChanged
			}
			return &PathError{Path: n.rel, Err: err}
		}
		if _, err := c.o.Write(chunk); err != nil {
			return err
		}
		left -= int64(len(chunk))
		c.done += int64(len(chunk))
		if c.progress != nil {
			c.progress(c.done, c.total)
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
	}
	var probe [1]byte
	if k, _ := f.Read(probe[:]); k > 0 {
		return &PathError{Path: n.rel, Err: ErrFileChanged}
	}
	return c.ctx.Err()
}

func (b *builder) write(ctx context.Context, w io.WriteSeeker, progress func(done, total int64)) error {
	base, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	o := &out{w: w, bw: bufio.NewWriterSize(w, 1<<20), base: base}
	cp := &copier{ctx: ctx, o: o, buf: make([]byte, 1<<20), progress: progress, total: b.t.total}

	if _, err := o.Write(b.metadata()); err != nil {
		return err
	}
	if b.boot {
		if err := b.writeFAT(ctx, o, cp); err != nil {
			return err
		}
	} else {
		for _, f := range b.t.allFiles {
			if f.size == 0 {
				continue
			}
			if err := o.skipTo(int64(f.fileLBA) * sectorSize); err != nil {
				return err
			}
			if err := cp.file(f); err != nil {
				return err
			}
			if err := o.zeros(sectorsFor(f.size)*sectorSize - f.size); err != nil {
				return err
			}
		}
	}
	if b.t.total == 0 && progress != nil {
		progress(0, 0)
	}
	end := b.totalSectors * sectorSize
	if o.pos < end {
		// extend the image; everything in between stays a hole
		if err := o.skipTo(end - 1); err != nil {
			return err
		}
		if _, err := o.Write([]byte{0}); err != nil {
			return err
		}
	}
	if o.pos != end {
		return fmt.Errorf("mkiso: internal error: wrote %d bytes, planned %d", o.pos, end)
	}
	return o.bw.Flush()
}
