// helpers_test.go
package ui

import "testing"

// englishUI marks a test that asserts English texts. Fyne reads the system language itself (NSLocale on
// macOS, LANG elsewhere), and Fyne ships its own translations for some keys ("Cancel", "Save", "OK",
// "Quit", "Advanced" and others), which also apply without i18n.Init. Without registered app translations
// lang.L returns the key for every other text, so those are asserted literally; the Fyne-translated keys
// must be asserted as lang.L("Cancel") etc., never as a literal.
func englishUI(t *testing.T) {
	t.Helper()
}
