// Package i18n embeds the UI translations and registers them with Fyne.
package i18n

import (
	"embed"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
)

//go:embed translations
var translations embed.FS

// The languages of the setting "Language": System follows the OS, the others force the app's texts.
const (
	System  = ""
	German  = "de"
	English = "en"
)

// Languages are the shipped translations, in the order of their files.
var Languages = []string{German, English}

// Valid reports whether l is System or one of Languages.
func Valid(l string) bool {
	if l == System {
		return true
	}
	for _, s := range Languages {
		if s == l {
			return true
		}
	}
	return false
}

// Feature translations: next to en.json and de.json a feature may add its own pair
// `<feature>.en.json` and `<feature>.de.json` in translations/ (Fyne's `[prefix.]lang.json`
// naming), so feature work does not grow the main files. Every file is loaded together with the
// main ones; a key lives in exactly one file (TestNoDuplicateKeysAcrossFiles).

// Init registers every translation file with Fyne; with System the closest system locale wins,
// fallback English. A forced language is loaded over every shipped language: Fyne has no public
// way to pick its localizer's language (it always matches the OS locales), so whichever of them
// the OS resolves to, and English as the fallback of every other one, yields the chosen texts.
// Fyne's own widget texts (such as an entry's context menu) still follow the OS. Fyne builds
// texts once, so Init runs at start, before the first window.
func Init(language string) error {
	if err := lang.AddTranslationsFS(translations, "translations"); err != nil {
		return err
	}
	if language == System || !Valid(language) {
		return nil
	}
	files, err := languageFiles(language)
	if err != nil {
		return err
	}
	for _, name := range files {
		data, err := translations.ReadFile("translations/" + name)
		if err != nil {
			return err
		}
		for _, l := range Languages {
			if err := lang.AddTranslationsForLocale(data, fyne.Locale(l)); err != nil {
				return err
			}
		}
	}
	return nil
}

// languageFiles lists the translation files of one language: `<language>.json` and every
// `<feature>.<language>.json`, sorted by name.
func languageFiles(language string) ([]string, error) {
	entries, err := translations.ReadDir("translations")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if n := e.Name(); n == language+".json" || strings.HasSuffix(n, "."+language+".json") {
			files = append(files, n)
		}
	}
	return files, nil
}
