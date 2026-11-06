// Package i18n embeds the UI translations and registers them with Fyne.
package i18n

import (
	"embed"

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

// Init registers en.json and de.json with Fyne; with System the closest system locale wins,
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
	data, err := translations.ReadFile("translations/" + language + ".json")
	if err != nil {
		return err
	}
	for _, l := range Languages {
		if err := lang.AddTranslationsForLocale(data, fyne.Locale(l)); err != nil {
			return err
		}
	}
	return nil
}
