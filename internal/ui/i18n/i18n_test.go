package i18n

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"fyne.io/fyne/v2/lang"
)

func keysOf(t *testing.T, name string) map[string]string {
	t.Helper()
	b, err := translations.ReadFile("translations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func TestSameKeysInAllLanguages(t *testing.T) {
	en, de := keysOf(t, "en.json"), keysOf(t, "de.json")
	for k, v := range en {
		if _, ok := de[k]; !ok {
			t.Errorf("de.json lacks %q", k)
		}
		if k != v {
			t.Errorf("en.json: text of %q must equal the key, got %q", k, v)
		}
	}
	for k := range de {
		if _, ok := en[k]; !ok {
			t.Errorf("en.json lacks %q", k)
		}
	}
}

var reserved = map[string]bool{"id": true, "description": true, "hash": true, "leftdelim": true, "rightdelim": true,
	"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true, "translation": true}

func TestNoReservedKeys(t *testing.T) {
	for k := range keysOf(t, "en.json") {
		if reserved[strings.ToLower(k)] {
			t.Errorf("key %q is reserved by go-i18n", k)
		}
	}
}

var useRE = regexp.MustCompile(`lang\.[LX]\("((?:[^"\\]|\\.)*)"`)

// TestUsedKeysExist scans every non-test Go file under internal/ui for lang.L("…") and lang.X("…", …).
func TestUsedKeysExist(t *testing.T) {
	en := keysOf(t, "en.json")
	used := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range useRE.FindAllStringSubmatch(string(src), -1) {
			used++
			if _, ok := en[m[1]]; !ok {
				t.Errorf("%s uses unknown key %q", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d uses checked", used)
}

// Fyne picks its language from the OS (NSLocale on macOS, LANG elsewhere) and registers translations
// process-wide, so no test calls Init or switches the language: the texts are read from the JSON files.
func TestGermanTranslations(t *testing.T) {
	de := keysOf(t, "de.json")
	if got := de["Connect"]; got != "Verbinden" {
		t.Fatalf("de: %q", got)
	}
	if got := de["Connecting to {{.Name}}"]; got != "Verbinde mit {{.Name}}" {
		t.Fatalf("de template: %q", got)
	}
	// color blind mode and the status row of the detail card
	for k, want := range map[string]string{
		"Color blind mode": "Farbenblind-Modus",
		"Status colors that stay distinct with red-green color blindness.": "Statusfarben, die bei Rot-Grün-Schwäche unterscheidbar bleiben.",
		"Status": "Status", "Powered on": "Eingeschaltet", "Standby": "Standby", "Powered off": "Ausgeschaltet", "Unknown": "Unbekannt",
	} {
		if got := de[k]; got != want {
			t.Errorf("de %q = %q, want %q", k, got, want)
		}
	}
}

// Without registered translations lang.L returns the key, which is the English text; this is also
// what an unknown key or an unsupported system language yields. Init must not be called in this package.
func TestFallbackReturnsKey(t *testing.T) {
	if got := lang.L("Connect"); got != "Connect" {
		t.Fatalf("fallback: %q", got)
	}
	if got := lang.L("No such key"); got != "No such key" {
		t.Fatalf("unknown key: %q", got)
	}
	if got := lang.L("Connecting to {{.Name}}", map[string]any{"Name": "device.example.com"}); got != "Connecting to device.example.com" {
		t.Fatalf("template fallback: %q", got)
	}
}
