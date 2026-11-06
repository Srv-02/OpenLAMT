package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/Srv-02/openlamt/internal/ui/style"
)

// Every form label (NewFormRow in start.go) must fit the label column in English and German;
// a longer translation would be cut off with an ellipsis.
func TestFormLabelsFitLabelColumn(t *testing.T) {
	src, err := os.ReadFile("start.go")
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`NewFormRow\(lang\.L\("([^"]+)"\)`).FindAllStringSubmatch(string(src), -1)
	if len(matches) < 8 {
		t.Fatalf("found only %d form labels in start.go", len(matches))
	}
	// The texts come straight from the JSON files: Fyne picks its language from the OS (NSLocale on
	// macOS), so switching it through LANG would make this test depend on the machine.
	for _, name := range []string{"en", "de"} {
		t.Run(name, func(t *testing.T) {
			texts := translationsOf(t, name)
			for _, m := range matches {
				label, ok := texts[m[1]]
				if !ok || (name == "de" && label == m[1] && !germanSame[m[1]]) {
					t.Errorf("%q has no %s translation", m[1], name)
				}
				w := fyne.MeasureText(label, style.Size("text"), fyne.TextStyle{}).Width
				if w > style.FormLabelWidth {
					t.Errorf("%s label %q is %.1f px wide, column is %v", name, label, w, style.FormLabelWidth)
				}
			}
		})
	}
}

func translationsOf(t *testing.T, name string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("i18n", "translations", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s.json: %v", name, err)
	}
	return m
}

// germanSame lists labels that are the same word in German.
var germanSame = map[string]bool{"Name": true, "Host/IP": true, "Status": true}
