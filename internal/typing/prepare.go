package typing

import "strings"

// Unmappable describes a character the layout cannot produce. Line and Column are
// 1-based and count runes; Index is the 0-based rune offset in the normalized text.
type Unmappable struct {
	Char                rune
	Index, Line, Column int
}

// Plan is the checked result of Prepare.
type Plan struct {
	Layout     Layout
	Text       string       // normalized text that will be typed
	Chars      int          // rune count of Text
	Multiline  bool         // Text contains a line break (each one is sent as Enter)
	TooLong    bool         // Chars > MaxChars
	Unmappable []Unmappable // characters the layout cannot produce; Type skips them
	Strokes    []Stroke     // one per typeable character, two for a composed one (dead key + letter); empty if not sendable
}

// Sendable reports whether the plan may be passed to Type. Characters the layout cannot produce
// do not matter: they are skipped and everything else is typed.
func (p Plan) Sendable() bool {
	return !p.TooLong && p.Chars > 0
}

// Typeable is the number of characters Type sends: the strokes without the second half of a
// composed character. It is 0 if only skipped characters and line breaks are left.
func (p Plan) Typeable() int {
	n := 0
	for _, s := range p.Strokes {
		if !s.Compose {
			n++
		}
	}
	return n
}

// Prepare normalizes text and translates it into strokes for layout. Line breaks
// (\r\n, \r, \n) become one Enter each, trailing line breaks are removed so that no
// command runs by accident. The error is ErrUnknownLayout for an unsupported layout.
func Prepare(text string, layout Layout) (Plan, error) {
	if layoutTable(layout) == nil {
		return Plan{}, ErrUnknownLayout
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRight(text, "\n")

	p := Plan{Layout: layout, Text: text, Multiline: strings.Contains(text, "\n")}
	var strokes []Stroke
	line, col := 1, 1
	for i, r := range []rune(text) {
		p.Chars++
		if s, ok := strokesFor(layout, r); ok {
			strokes = append(strokes, s...)
		} else {
			p.Unmappable = append(p.Unmappable, Unmappable{Char: r, Index: i, Line: line, Column: col})
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	// Skipping untypeable characters can leave line breaks at the end that were not trailing in the
	// text: they would run the command on the target, so they go like the trailing ones above.
	for n := len(strokes); n > 0 && strokes[n-1] == (Stroke{Keysym: KeyReturn}); n = len(strokes) {
		strokes = strokes[:n-1]
	}
	p.TooLong = p.Chars > MaxChars
	if p.Sendable() {
		p.Strokes = strokes
	}
	return p, nil
}
