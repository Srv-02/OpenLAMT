package typing

// level is what one level (base, Shift, AltGr) of a key produces: a character, a dead key
// that Type completes with Space, or nothing.
type level struct {
	r    rune
	dead bool
}

var none level

func lv(r rune) level { return level{r: r} }
func d(r rune) level  { return level{r: r, dead: true} }

// keyDef is one key of a layout. pos is the unshifted US keysym of the physical key (its US
// position); base, shift, altgr and shiftAltgr are the results without modifier, with Shift, with
// AltGr and with both.
// The ISO key left of Z has no keysym and no kvm.Key, so layouts leave it out.
type keyDef struct {
	pos                            rune
	base, shift, altgr, shiftAltgr level
}

// compositions lists, per dead key (spacing character), the base letter followed by the letter it
// makes, as pairs. Only Latin-1 letters.
var compositions = map[rune]string{
	'´': "aáeéiíoóuúyýAÁEÉIÍOÓUÚYÝ",
	'`': "aàeèiìoòuùAÀEÈIÌOÒUÙ",
	'^': "aâeêiîoôuûAÂEÊIÎOÔUÛ",
	'¨': "aäeëiïoöuüyÿAÄEËIÏOÖUÜ",
	'~': "aãnñoõAÃNÑOÕ",
	'¸': "cçCÇ",
}

// buildTable turns key definitions into a character table. If a character is reachable more than
// once, a direct key beats a dead key and fewer modifiers beat more (base, Shift, AltGr). Letters
// that a dead key of the layout can make but that have no key of their own (ã on a Portuguese
// layout) are added as the dead key followed by the base letter.
func buildTable(defs []keyDef) (map[rune]Stroke, map[rune][]Stroke) {
	m := map[rune]Stroke{' ': k(' ')}
	cost := map[rune]int{}
	deads := map[rune]Stroke{}
	deadCost := map[rune]int{}
	for _, def := range defs {
		for i, lvl := range []level{def.base, def.shift, def.altgr, def.shiftAltgr} {
			if lvl.r == 0 {
				continue
			}
			st := Stroke{Keysym: uint32(def.pos), Shift: i == 1 || i == 3, AltGr: i >= 2, Dead: lvl.dead}
			if lvl.dead {
				if old, ok := deadCost[lvl.r]; !ok || i < old {
					deadCost[lvl.r], deads[lvl.r] = i, st
				}
			}
			c := i
			if lvl.dead {
				c += 10
			}
			if old, ok := cost[lvl.r]; ok && old <= c {
				continue
			}
			cost[lvl.r] = c
			m[lvl.r] = st
		}
	}
	return m, composeAll(m, deads)
}

// composeAll returns the letters of compositions that table m lacks, as the Compose stroke of the
// dead key (deads, by spacing character) followed by the stroke of the base letter.
func composeAll(m, deads map[rune]Stroke) map[rune][]Stroke {
	composed := map[rune][]Stroke{}
	for dead, pairs := range compositions {
		ds, ok := deads[dead]
		if !ok {
			continue
		}
		ds.Dead, ds.Compose = false, true
		r := []rune(pairs)
		for i := 0; i+1 < len(r); i += 2 {
			base, ok := m[r[i]]
			if _, have := m[r[i+1]]; have || !ok {
				continue
			}
			composed[r[i+1]] = []Stroke{ds, base}
		}
	}
	return composed
}
