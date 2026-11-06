package typing

import (
	"maps"
	"sync"
)

// Key positions are written as the unshifted US keysym of the physical key.

var tableUS = buildUS()

// buildUS maps all printable ASCII. base and shifted list the US keys in the same order.
func buildUS() map[rune]Stroke {
	const base = "`1234567890-=[]\\;',./"
	const shifted = "~!@#$%^&*()_+{}|:\"<>?"
	m := map[rune]Stroke{' ': {Keysym: KeySpace}}
	for i, r := range base {
		m[r] = Stroke{Keysym: uint32(r)}
		m[rune(shifted[i])] = Stroke{Keysym: uint32(r), Shift: true}
	}
	for r := 'a'; r <= 'z'; r++ {
		m[r] = Stroke{Keysym: uint32(r)}
		m[r-'a'+'A'] = Stroke{Keysym: uint32(r), Shift: true}
	}
	return m
}

func k(c rune) Stroke  { return Stroke{Keysym: uint32(c)} }
func sh(c rune) Stroke { return Stroke{Keysym: uint32(c), Shift: true} }
func ag(c rune) Stroke { return Stroke{Keysym: uint32(c), AltGr: true} }

func dead(s Stroke) Stroke {
	s.Dead = true
	return s
}

// tableDE maps characters of the German/Austrian layout to the US position that
// produces them. Dead keys are in deadDE. The ISO key "< > |" has no US keysym, so
// '<', '>' and '|' are deliberately missing (unmappable until the device test).
var tableDE = buildDE()

func buildDE() map[rune]Stroke {
	m := map[rune]Stroke{
		' ': k(' '),
		// digit row (US 1..0); Shift and AltGr as printed on a German keyboard
		'1': k('1'), '!': sh('1'),
		'2': k('2'), '"': sh('2'), '²': ag('2'),
		'3': k('3'), '§': sh('3'), '³': ag('3'),
		'4': k('4'), '$': sh('4'),
		'5': k('5'), '%': sh('5'),
		'6': k('6'), '&': sh('6'),
		'7': k('7'), '/': sh('7'), '{': ag('7'),
		'8': k('8'), '(': sh('8'), '[': ag('8'),
		'9': k('9'), ')': sh('9'), ']': ag('9'),
		'0': k('0'), '=': sh('0'), '}': ag('0'),
		'ß': k('-'), '?': sh('-'), '\\': ag('-'), // US minus position
		'°': sh('`'), // US grave position (unshifted is the dead key ^)
		// letters on special positions
		'@': ag('q'), '€': ag('e'), 'µ': ag('m'),
		'z': k('y'), 'Z': sh('y'), // QWERTZ: US y position
		'y': k('z'), 'Y': sh('z'), // US z position
		// umlauts and the keys right of P / L / Ä
		'ü': k('['), 'Ü': sh('['),
		'+': k(']'), '*': sh(']'), '~': ag(']'),
		'ö': k(';'), 'Ö': sh(';'),
		'ä': k('\''), 'Ä': sh('\''),
		'#': k('\\'), '\'': sh('\\'), // US backslash position
		// bottom row
		',': k(','), ';': sh(','),
		'.': k('.'), ':': sh('.'),
		'-': k('/'), '_': sh('/'),
	}
	for c := 'a'; c <= 'x'; c++ {
		if _, ok := m[c]; !ok {
			m[c] = k(c)
			m[c-'a'+'A'] = sh(c)
		}
	}
	return m
}

// deadDE lists the dead keys of the German layout. They are marked Dead; Type adds the Space.
var deadDE = map[rune]Stroke{
	'^': dead(k('`')),  // US grave position
	'´': dead(k('=')),  // US equal position
	'`': dead(sh('=')), // Shift + US equal position
}

// strokesFor returns the strokes producing r on layout l.
func strokesFor(l Layout, r rune) ([]Stroke, bool) {
	switch r {
	case '\n':
		return []Stroke{{Keysym: KeyReturn}}, true
	case '\t':
		return []Stroke{{Keysym: KeyTab}}, true
	}
	tablesOnce.Do(buildTables)
	if s, ok := tables[l][r]; ok {
		return []Stroke{s}, true
	}
	c, ok := composed[l][r]
	return c, ok
}

// layoutTable returns the character table of a known layout (nil for an unknown one).
func layoutTable(l Layout) map[rune]Stroke {
	tablesOnce.Do(buildTables)
	return tables[l]
}

var (
	tablesOnce sync.Once
	tables     map[Layout]map[rune]Stroke
	composed   map[Layout]map[rune][]Stroke // characters made of a dead key and a letter
)

func buildTables() {
	de := make(map[rune]Stroke, len(tableDE)+len(deadDE))
	maps.Copy(de, tableDE)
	maps.Copy(de, deadDE)
	tables = map[Layout]map[rune]Stroke{LayoutDE: de, LayoutUS: tableUS}
	composed = map[Layout]map[rune][]Stroke{LayoutDE: composeAll(de, deadDE)}
	for l, defs := range rawLayouts {
		tables[l], composed[l] = buildTable(defs)
	}
}
