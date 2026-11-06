package typing

import (
	"os"
	"strings"
)

// DetectLocalLayout guesses the layout of the local system: see layoutFromLocale; US
// if the language has no layout of its own.
//
// The locale environment variables (LC_ALL, LC_CTYPE, LANG) take precedence. If none is set,
// as for apps started from the Finder or the Windows start menu, systemLocale (a BCP 47 or
// POSIX style language tag, e.g. "de-AT") is used instead. The caller supplies it so that
// this package stays free of any UI dependency. The system language is only an approximation
// of the keyboard layout; real layout queries per OS are not part of v1.
func DetectLocalLayout(systemLocale string) Layout {
	return detectLayout(os.Getenv, systemLocale)
}

func detectLayout(getenv func(string) string, systemLocale string) Layout {
	if l, ok := detectFromEnv(getenv); ok {
		return l
	}
	return layoutFromLocale(systemLocale)
}

// detectFromEnv checks LC_ALL, LC_CTYPE and LANG; the first non-empty value wins. The bool
// reports whether any of them was set.
func detectFromEnv(getenv func(string) string) (Layout, bool) {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return layoutFromLocale(v), true
		}
	}
	return LayoutUS, false
}

// layoutFromLocale maps a locale string to a layout. The language and the region decide, with
// "-" or "_", an encoding, modifier or script (de-AT-Latn) ignored: de, de_DE and de_AT give DE,
// de_CH and fr_CH their Swiss layouts, fr_FR FR, fr_BE and nl_BE BE, en_GB UK, it IT, es ES, pt_PT
// PT, pt_BR BR, sv and fi SE-FI, nb, nn and no NO, da DK, ru RU, ja JP; everything else gives US.
func layoutFromLocale(v string) Layout {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, ".@"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(v, "-", "_")), "_")
	lang, region := parts[0], ""
	// Skip an optional 4-letter script subtag (de-Latn-AT).
	rest := parts[1:]
	if len(rest) > 0 && len(rest[0]) == 4 {
		rest = rest[1:]
	}
	if len(rest) > 0 {
		region = rest[0]
	}
	switch lang {
	case "de":
		switch region {
		case "", "de", "at":
			return LayoutDE
		case "ch", "li":
			return LayoutCHDE
		}
	case "fr":
		switch region {
		case "", "fr", "lu", "mc":
			return LayoutFR
		case "ch":
			return LayoutCHFR
		case "be":
			return LayoutBE
		}
	case "nl":
		if region == "be" {
			return LayoutBE
		}
	case "en":
		if region == "gb" || region == "uk" {
			return LayoutUK
		}
	case "it":
		if region == "" || region == "it" || region == "sm" {
			return LayoutIT
		}
	case "es":
		if region == "" || region == "es" {
			return LayoutES
		}
	case "pt":
		switch region {
		case "", "pt":
			return LayoutPT
		case "br":
			return LayoutBR
		}
	case "sv", "fi":
		return LayoutSEFI
	case "nb", "nn", "no":
		return LayoutNO
	case "da":
		return LayoutDK
	case "ru":
		return LayoutRU
	case "ja":
		return LayoutJP
	}
	return LayoutUS
}
