package typing

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func shag(c rune) Stroke { return Stroke{Keysym: uint32(c), Shift: true, AltGr: true} }
func dk(s Stroke) Stroke { s.Dead = true; return s }
func cp(s Stroke) Stroke { s.Compose = true; return s }

// gaps lists the printable ASCII characters each new layout cannot type: the ISO key left of Z
// (no keysym, no kvm.Key) or the JIS keys without a US position, or characters the layout lacks.
var gaps = map[Layout]string{
	LayoutDE: "<>|", LayoutUS: "",
	LayoutUK: `\|`, LayoutCHDE: `<>\`, LayoutCHFR: `<>\`, LayoutFR: "<>", LayoutBE: `<>\`, LayoutIT: "<>",
	LayoutES: "<>", LayoutPT: "<>", LayoutBR: `\|`, LayoutSEFI: "<>|", LayoutNO: "<>", LayoutDK: `<>\`,
	LayoutRU: "#$&'<>@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^`abcdefghijklmnopqrstuvwxyz{|}~",
	LayoutJP: `\_|`,
}

func TestEveryLayoutCoversASCIIExceptKnownGaps(t *testing.T) {
	if len(gaps) != len(Layouts()) {
		t.Fatalf("gaps for %d layouts, %d exist", len(gaps), len(Layouts()))
	}
	for _, l := range Layouts() {
		var got strings.Builder
		for r := rune(0x21); r < 0x7f; r++ {
			if _, ok := strokesFor(l, r); !ok {
				got.WriteRune(r)
			}
		}
		if got.String() != gaps[l] {
			t.Errorf("%s: not typable %q, want %q", l, got.String(), gaps[l])
		}
		if s, ok := strokesFor(l, ' '); !ok || s[0] != k(' ') {
			t.Errorf("%s: space", l)
		}
	}
}

func TestLayoutTablesAreSound(t *testing.T) {
	for _, l := range Layouts() {
		seen := map[Stroke]rune{}
		for r, s := range layoutTable(l) {
			if s.Keysym == 0 || s.Compose {
				t.Errorf("%s %q: bad stroke %v", l, r, s)
			}
			// the same physical stroke must not produce two characters
			if o, dup := seen[s]; dup {
				t.Errorf("%s: %q and %q share stroke %v", l, r, o, s)
			}
			seen[s] = r
			// a stroke names the unshifted US key: letters lowercase, never a shifted symbol
			if s.Keysym >= 'A' && s.Keysym <= 'Z' || strings.ContainsRune("~!@#$%^&*()_+{}|:\"<>?", rune(s.Keysym)) {
				t.Errorf("%s %q: keysym %q is not an unshifted US key", l, r, rune(s.Keysym))
			}
		}
	}
}

// national: characters typed on each layout, from the printed keycaps of the standard keyboards.
var national = map[Layout]map[rune]Stroke{
	LayoutCHDE: {
		'ü': k('['), 'ö': k(';'), 'ä': k('\''), 'è': sh('['), 'é': sh(';'), 'à': sh('\''), 'z': k('y'), 'y': k('z'),
		'§': k('`'), '°': sh('`'), '€': ag('e'), '@': ag('2'), '#': ag('3'), '|': ag('7'), '[': ag('['), ']': ag(']'),
		'{': ag('\''), '}': ag('\\'), '$': k('\\'), '£': sh('\\'), '-': k('/'), '_': sh('/'), '/': sh('7'), '?': sh('-'),
		'\'': k('-'), '^': dk(k('=')), '`': dk(sh('=')), '~': dk(ag('=')), '¨': dk(k(']')), '´': dk(ag('-')),
		'!': sh(']'), '"': sh('2'), '+': sh('1'), '*': sh('3'), 'ç': sh('4'), '%': sh('5'), '&': sh('6'), '(': sh('8'),
	},
	LayoutCHFR: {
		'é': k(';'), 'è': k('['), 'à': k('\''), 'ö': sh(';'), 'ü': sh('['), 'ä': sh('\''), 'z': k('y'), '€': ag('e'),
		'@': ag('2'), '|': ag('7'), 'ç': sh('4'), '+': sh('1'), '*': sh('3'), '!': sh(']'), '^': dk(k('=')), '¨': dk(k(']')),
	},
	LayoutFR: {
		'a': k('q'), 'q': k('a'), 'z': k('w'), 'w': k('z'), 'm': k(';'), 'é': k('2'), 'è': k('7'), 'ç': k('9'), 'à': k('0'),
		'ù': k('\''), '&': k('1'), '1': sh('1'), '€': ag('e'), '@': ag('0'), '#': ag('3'), '~': dk(ag('2')), '{': ag('4'),
		'[': ag('5'), '|': ag('6'), '`': dk(ag('7')), '\\': ag('8'), '^': ag('9'), ']': ag('-'), '}': ag('='), '$': k(']'),
		'£': sh(']'), '*': k('\\'), 'µ': sh('\\'), ',': k('m'), '?': sh('m'), ';': k(','), '.': sh(','), ':': k('.'),
		'/': sh('.'), '!': k('/'), '§': sh('/'), '"': k('3'), '\'': k('4'), '(': k('5'), '-': k('6'), '_': k('8'),
		')': k('-'), '°': sh('-'), '=': k('='), '+': sh('='), '²': k('`'), '¨': dk(sh('[')),
	},
	LayoutBE: {
		'a': k('q'), 'z': k('w'), 'é': k('2'), 'è': k('7'), 'ç': k('9'), 'à': k('0'), 'ù': k('\''), '&': k('1'),
		'€': ag('e'), '@': ag('2'), '#': ag('3'), '|': ag('1'), '{': ag('9'), '}': ag('0'), '[': ag('['), ']': ag(']'),
		'$': k(']'), '*': sh(']'), 'µ': k('\\'), '£': sh('\\'), '=': k('/'), '+': sh('/'), ',': k('m'), ';': k(','),
		':': k('.'), '!': k('8'), ')': k('-'), '-': k('='), '§': k('6'), '^': ag('6'), '°': sh('-'), '²': k('`'),
	},
	LayoutUK: {
		'£': sh('3'), '"': sh('2'), '@': sh('\''), '\'': k('\''), '#': k('\\'), '~': sh('\\'), '¬': sh('`'),
		'€': ag('4'), '`': k('`'), '^': sh('6'), '&': sh('7'), '*': sh('8'),
	},
	LayoutIT: {
		'è': k('['), 'é': sh('['), 'ò': k(';'), 'ç': sh(';'), 'à': k('\''), '°': sh('\''), 'ù': k('\\'), '§': sh('\\'),
		'ì': k('='), '^': sh('='), '\\': k('`'), '|': sh('`'), '@': ag(';'), '#': ag('\''), '[': ag('['),
		'{': shag('['), ']': ag(']'), '}': shag(']'), '€': ag('e'), '+': k(']'), '*': sh(']'), '~': ag('='),
		'£': sh('3'), '"': sh('2'), '\'': k('-'), '?': sh('-'), '`': ag('-'),
	},
	LayoutES: {
		'ñ': k(';'), 'Ñ': sh(';'), '¿': sh('='), '¡': k('='), 'ç': k('\\'), 'Ç': sh('\\'), 'º': k('`'), 'ª': sh('`'),
		'\\': ag('`'), '|': ag('1'), '@': ag('2'), '#': ag('3'), '~': dk(ag('4')), '€': ag('e'), '[': ag('['), ']': ag(']'),
		'{': ag('\''), '}': ag('\\'), '´': dk(k('\'')), '`': dk(k('[')), '^': dk(sh('[')), '¨': dk(sh('\'')),
		'+': k(']'), '*': sh(']'), '-': k('/'), '_': sh('/'), '\'': k('-'), '?': sh('-'), '"': sh('2'),
	},
	LayoutPT: {
		'ç': k(';'), 'Ç': sh(';'), '@': ag('2'), '£': ag('3'), '§': ag('4'), '{': ag('7'), '[': ag('8'), ']': ag('9'),
		'}': ag('0'), '€': ag('e'), '\\': k('`'), '|': sh('`'), '«': k('='), '»': sh('='), '\'': k('-'), '?': sh('-'),
		'+': k('['), '*': sh('['), 'º': k('\''), 'ª': sh('\''), '"': sh('2'), '#': sh('3'), '-': k('/'), '_': sh('/'),
	},
	LayoutSEFI: {
		'å': k('['), 'Å': sh('['), 'ö': k(';'), 'Ö': sh(';'), 'ä': k('\''), 'Ä': sh('\''), '@': ag('2'), '£': ag('3'),
		'$': ag('4'), '€': ag('e'), '{': ag('7'), '[': ag('8'), ']': ag('9'), '}': ag('0'), '\\': ag('-'), '+': k('-'),
		'?': sh('-'), '\'': k('\\'), '*': sh('\\'), '§': k('`'), '½': sh('`'), '-': k('/'), '_': sh('/'), '"': sh('2'),
		'/': sh('7'), '´': dk(k('=')), '`': dk(sh('=')), '¨': dk(k(']')), '^': dk(sh(']')), '~': dk(ag(']')),
	},
	LayoutNO: {
		'å': k('['), 'Å': sh('['), 'ø': k(';'), 'Ø': sh(';'), 'æ': k('\''), 'Æ': sh('\''), '|': k('`'), '§': sh('`'),
		'@': ag('2'), '£': ag('3'), '$': ag('4'), '€': ag('e'), '{': ag('7'), '[': ag('8'), ']': ag('9'), '}': ag('0'),
		'+': k('-'), '?': sh('-'), '\\': k('='), '\'': k('\\'), '*': sh('\\'), '-': k('/'), '_': sh('/'),
		'¨': dk(k(']')), '^': dk(sh(']')), '~': dk(ag(']')), '`': dk(sh('=')), '´': dk(ag('=')),
	},
	LayoutDK: {
		'æ': k(';'), 'Æ': sh(';'), 'ø': k('\''), 'Ø': sh('\''), 'å': k('['), 'Å': sh('['), '½': k('`'), '§': sh('`'),
		'+': k('-'), '?': sh('-'), '|': ag('='), '@': ag('2'), '£': ag('3'), '$': ag('4'), '€': ag('e'), '{': ag('7'),
		'[': ag('8'), ']': ag('9'), '}': ag('0'), '\'': k('\\'), '*': sh('\\'), '-': k('/'), '_': sh('/'),
		'´': dk(k('=')), '`': dk(sh('=')), '¨': dk(k(']')), '^': dk(sh(']')), '~': dk(ag(']')),
	},
	LayoutBR: {
		'ç': k(';'), 'Ç': sh(';'), '/': ag('q'), '?': ag('w'), '[': k(']'), '{': sh(']'), ']': k('\\'), '}': sh('\\'),
		'\'': k('`'), '"': sh('`'), '´': dk(k('[')), '`': dk(sh('[')), '~': dk(k('\'')), '^': dk(sh('\'')),
		'-': k('-'), '_': sh('-'), '=': k('='), '+': sh('='), ',': k(','), '<': sh(','), '.': k('.'), '>': sh('.'),
		';': k('/'), ':': sh('/'), '£': ag('4'), '§': ag('='),
	},
	LayoutJP: {
		'@': k('['), '`': sh('['), '[': k(']'), '{': sh(']'), ']': k('\\'), '}': sh('\\'), ':': k('\''), '*': sh('\''),
		'+': sh(';'), ';': k(';'), '^': k('='), '~': sh('='), '-': k('-'), '=': sh('-'), '"': sh('2'), '\'': sh('7'),
		'&': sh('6'), '(': sh('8'), ')': sh('9'), '/': k('/'), '?': sh('/'), '<': sh(','), '>': sh('.'),
	},
}

func TestNationalCharacters(t *testing.T) {
	for l, chars := range national {
		for r, want := range chars {
			got, ok := strokesFor(l, r)
			if !ok || len(got) != 1 || got[0] != want {
				t.Errorf("%s %q: got %v (%v), want %v", l, r, got, ok, want)
			}
		}
	}
}

func TestRussianAlphabet(t *testing.T) {
	// ЙЦУКЕН: the lower case rows on the US positions q..], a..', z../
	rows := []struct{ keys, letters string }{
		{"qwertyuiop[]", "йцукенгшщзхъ"},
		{"asdfghjkl;'", "фывапролджэ"},
		{"zxcvbnm,.", "ячсмитьбю"},
	}
	for _, row := range rows {
		keys, letters := []rune(row.keys), []rune(row.letters)
		for i := range keys {
			low, up := letters[i], []rune(strings.ToUpper(string(letters[i])))[0]
			if got, _ := strokesFor(LayoutRU, low); len(got) != 1 || got[0] != k(keys[i]) {
				t.Errorf("%q: %v", low, got)
			}
			if got, _ := strokesFor(LayoutRU, up); len(got) != 1 || got[0] != sh(keys[i]) {
				t.Errorf("%q: %v", up, got)
			}
		}
	}
	for r, want := range map[rune]Stroke{'ё': k('`'), 'Ё': sh('`'), '№': sh('3'), '.': k('/'), ',': sh('/'), '"': sh('2'),
		';': sh('4'), ':': sh('6'), '?': sh('7'), '!': sh('1'), '%': sh('5'), '*': sh('8'), '(': sh('9'), ')': sh('0'),
		'-': k('-'), '_': sh('-'), '=': k('='), '+': sh('='), '\\': k('\\'), '/': sh('\\')} {
		if got, ok := strokesFor(LayoutRU, r); !ok || got[0] != want {
			t.Errorf("%q: %v", r, got)
		}
	}
	if _, ok := strokesFor(LayoutRU, 'a'); ok {
		t.Error("Latin letters are not on the Russian layout")
	}
}

func TestComposedCharacters(t *testing.T) {
	cases := []struct {
		l    Layout
		r    rune
		want []Stroke
	}{
		{LayoutPT, 'ã', []Stroke{cp(k('\\')), k('a')}},
		{LayoutPT, 'Õ', []Stroke{cp(k('\\')), sh('o')}},
		{LayoutPT, 'â', []Stroke{cp(sh('\\')), k('a')}},
		{LayoutBR, 'ã', []Stroke{cp(k('\'')), k('a')}},
		{LayoutBR, 'õ', []Stroke{cp(k('\'')), k('o')}},
		{LayoutBR, 'á', []Stroke{cp(k('[')), k('a')}},
		{LayoutBR, 'à', []Stroke{cp(sh('[')), k('a')}},
		{LayoutES, 'é', []Stroke{cp(k('\'')), k('e')}},
		{LayoutES, 'ï', []Stroke{cp(sh('\'')), k('i')}},
		{LayoutDK, 'é', []Stroke{cp(k('=')), k('e')}},
		{LayoutDK, 'ü', []Stroke{cp(k(']')), k('u')}},
		{LayoutFR, 'ô', []Stroke{cp(k('[')), k('o')}},
		{LayoutFR, 'ë', []Stroke{cp(sh('[')), k('e')}},
		{LayoutCHDE, 'ñ', []Stroke{cp(ag('=')), k('n')}},
		{LayoutDE, 'é', []Stroke{cp(k('=')), k('e')}},
		{LayoutDE, 'É', []Stroke{cp(k('=')), sh('e')}},
		{LayoutDE, 'á', []Stroke{cp(k('=')), k('a')}},
		{LayoutDE, 'ý', []Stroke{cp(k('=')), k('z')}}, // QWERTZ: y is on the US z position
		{LayoutDE, 'è', []Stroke{cp(sh('=')), k('e')}},
		{LayoutDE, 'À', []Stroke{cp(sh('=')), sh('a')}},
		{LayoutDE, 'â', []Stroke{cp(k('`')), k('a')}},
		{LayoutDE, 'Ê', []Stroke{cp(k('`')), sh('e')}},
		{LayoutDE, 'û', []Stroke{cp(k('`')), k('u')}},
	}
	for _, c := range cases {
		got, ok := strokesFor(c.l, c.r)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q: got %v (%v), want %v", c.l, c.r, got, ok, c.want)
		}
	}
	// a character with a key of its own is never composed
	if got, _ := strokesFor(LayoutFR, 'é'); len(got) != 1 {
		t.Errorf("é on FR: %v", got)
	}
	// the German layout has no tilde or cedilla dead key
	for _, r := range "ñçãÑÇ" {
		if _, ok := strokesFor(LayoutDE, r); ok {
			t.Errorf("%q must not be typeable on DE", r)
		}
	}
	// no dead key, no composition
	if _, ok := strokesFor(LayoutIT, 'ñ'); ok {
		t.Error("IT has no dead tilde")
	}
}

func TestPrepareComposedCountsOneCharacter(t *testing.T) {
	p, err := Prepare("ã1", LayoutPT)
	if err != nil || !p.Sendable() || p.Chars != 2 || len(p.Strokes) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	f := &fakeSender{}
	var done, total int
	if err := Type(context.Background(), f, p, 0, func(d, n int) { done, total = d, n }); err != nil {
		t.Fatal(err)
	}
	if done != 2 || total != 2 {
		t.Errorf("progress %d of %d, want 2 of 2", done, total)
	}
	// dead ~ then a (no Space between), then 1
	want := []ev{{'\\', true}, {'\\', false}, {'a', true}, {'a', false}, {'1', true}, {'1', false}}
	if !reflect.DeepEqual(f.events, want) {
		t.Errorf("events %v, want %v", f.events, want)
	}
}

func TestCompositionIsNotCancelledBetweenDeadKeyAndLetter(t *testing.T) {
	p, _ := Prepare("ãb", LayoutPT)
	ctx, cancel := context.WithCancel(context.Background())
	f := &cancelSender{cancel: cancel}
	if err := Type(ctx, f, p, 0, nil); err == nil {
		t.Fatal("no error")
	}
	// cancelled while the dead key went down: the letter that completes it is still sent, b is not
	want := []ev{{'\\', true}, {'\\', false}, {'a', true}, {'a', false}}
	if !reflect.DeepEqual(f.events, want) {
		t.Errorf("events %v, want %v", f.events, want)
	}
}

// cancelSender cancels its context at the first key down.
type cancelSender struct {
	fakeSender
	cancel context.CancelFunc
}

func (c *cancelSender) SendKey(k uint32, down bool) error {
	if down {
		c.cancel()
	}
	return c.fakeSender.SendKey(k, down)
}

func TestParseLayoutRoundTrip(t *testing.T) {
	want := []string{"de", "us", "uk", "ch-de", "ch-fr", "fr", "be", "it", "es", "pt", "br", "se-fi", "no", "dk", "ru", "jp"}
	var got []string
	for _, l := range Layouts() {
		got = append(got, string(l))
		for _, in := range []string{string(l), strings.ToUpper(string(l)), " " + l.Label() + " "} {
			if p, ok := ParseLayout(in); !ok || p != l {
				t.Errorf("%q: %v %v", in, p, ok)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codes %v, want %v", got, want)
	}
	if p, ok := ParseLayout("gb"); !ok || p != LayoutUK {
		t.Errorf("gb: %v %v", p, ok)
	}
	for _, bad := range []string{"", "xx", "ch", "se", "fr-ch"} {
		if _, ok := ParseLayout(bad); ok {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestLocaleToLayout(t *testing.T) {
	cases := map[string]Layout{
		"de_DE.UTF-8": LayoutDE, "de_AT": LayoutDE, "de": LayoutDE, "de_CH.UTF-8": LayoutCHDE, "de-CH": LayoutCHDE,
		"de_LI": LayoutCHDE, "fr_CH": LayoutCHFR, "fr-CH": LayoutCHFR, "fr_FR.UTF-8": LayoutFR, "fr": LayoutFR,
		"fr_BE": LayoutBE, "nl_BE.UTF-8": LayoutBE, "nl_NL": LayoutUS, "en_GB.UTF-8": LayoutUK, "en-GB": LayoutUK,
		"en_US": LayoutUS, "en_AU": LayoutUS, "it_IT.UTF-8": LayoutIT, "it": LayoutIT, "es_ES": LayoutES, "es": LayoutES,
		"es_MX": LayoutUS, "pt_PT": LayoutPT, "pt": LayoutPT, "pt_BR.UTF-8": LayoutBR, "pt-BR": LayoutBR,
		"sv_SE": LayoutSEFI, "sv": LayoutSEFI, "fi_FI.UTF-8": LayoutSEFI, "nb_NO": LayoutNO, "nn_NO": LayoutNO,
		"no": LayoutNO, "da_DK.UTF-8": LayoutDK, "da": LayoutDK, "ru_RU.UTF-8": LayoutRU, "ru-RU": LayoutRU,
		"ja_JP.UTF-8": LayoutJP, "ja-JP": LayoutJP, "ja": LayoutJP, "fr-Latn-CH": LayoutCHFR, "": LayoutUS, "C": LayoutUS,
	}
	for in, want := range cases {
		if got := layoutFromLocale(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestNationalLettersAreTypable(t *testing.T) {
	cases := map[Layout]string{
		LayoutCHDE: "üöäéèàÜÖÄç€£", LayoutCHFR: "üöäéèàÜÖÄç€£", LayoutFR: "éèçàù€£µ²°âêîôûëïü",
		LayoutBE: "éèçàù€£µ²³°âêîôûëïü", LayoutUK: "£€¬", LayoutIT: "èéòçàùì°§£€", LayoutES: "ñÑ¿¡çÇºª€áéíóúü",
		LayoutPT: "çÇãõÃÕáéíóúâêôàª º€£§«»", LayoutBR: "çÇãõÃÕáéíóúâêôàüÁÉÍÓÚ§£",
		LayoutSEFI: "åäöÅÄÖ§½€£", LayoutNO: "åæøÅÆØ§€£", LayoutDK: "åæøÅÆØ§½€£",
		LayoutRU: "абвгдеёжзийклмнопрстуфхцчшщъыьэюяАБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯ№",
	}
	for l, chars := range cases {
		for _, r := range chars {
			if r == ' ' {
				continue
			}
			if _, ok := strokesFor(l, r); !ok {
				t.Errorf("%s: %q not typable", l, r)
			}
		}
	}
}

func TestRussianNeedsRussianTargetNotice(t *testing.T) {
	// In a mixed text the Latin letters are unmappable and skipped; the Cyrillic part is typed.
	p, err := Prepare("пароль1 abc", LayoutRU)
	if err != nil || !p.Sendable() || len(p.Unmappable) != 3 || p.Typeable() != 8 {
		t.Fatalf("%+v %v", p, err)
	}
}

// Windows FR and ES make ~ and ` (FR) with dead keys: the character itself needs the dead key
// and Space, and the dead key composes letters (ã, ñ).
func TestWindowsDeadTildeAndGraveOnFRAndES(t *testing.T) {
	for _, c := range []struct {
		l Layout
		r rune
		s Stroke
	}{
		{LayoutFR, '~', dk(ag('2'))}, {LayoutFR, '`', dk(ag('7'))}, {LayoutES, '~', dk(ag('4'))},
	} {
		got, ok := strokesFor(c.l, c.r)
		if !ok || !reflect.DeepEqual(got, []Stroke{c.s}) {
			t.Errorf("%s %q: got %v (%v), want %v", c.l, c.r, got, ok, c.s)
		}
		p, err := Prepare(string(c.r), c.l)
		if err != nil || !p.Sendable() {
			t.Fatalf("%s %q: %+v %v", c.l, c.r, p, err)
		}
		f := &fakeSender{}
		if err := Type(context.Background(), f, p, 0, nil); err != nil {
			t.Fatal(err)
		}
		if n := len(f.events); n < 4 || f.events[n-2].Keysym != KeySpace {
			t.Errorf("%s %q: dead key not completed with Space: %v", c.l, c.r, f.events)
		}
	}
	if got, ok := strokesFor(LayoutFR, 'ã'); !ok || len(got) != 2 || !got[0].Compose {
		t.Errorf("ã on FR: %v %v", got, ok)
	}
}
