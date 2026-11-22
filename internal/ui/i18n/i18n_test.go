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

// keysOf returns the keys of one language over all its files: `<lang>.json` and
// `<feature>.<lang>.json`. keysOf(t, "en.json") and keysOf(t, "en") both mean English.
func keysOf(t *testing.T, name string) map[string]string {
	t.Helper()
	language := strings.TrimSuffix(name, ".json")
	files, err := languageFiles(language)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{}
	for _, f := range files {
		for k, v := range keysOfFile(t, f) {
			all[k] = v
		}
	}
	return all
}

func keysOfFile(t *testing.T, name string) map[string]string {
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

// featureOf maps a file name to its feature: "" for en.json and de.json.
func featureOf(name string) string {
	for _, l := range Languages {
		if name == l+".json" {
			return ""
		}
		if f, ok := strings.CutSuffix(name, "."+l+".json"); ok {
			return f
		}
	}
	return "?"
}

// TestNoDuplicateKeysAcrossFiles: a key lives in one file per language; the files pair up
// (every `<feature>.en.json` has its `<feature>.de.json`), and each text of a file equals its key.
func TestNoDuplicateKeysAcrossFiles(t *testing.T) {
	entries, err := translations.ReadDir("translations")
	if err != nil {
		t.Fatal(err)
	}
	owner := map[string]string{} // language + key -> file
	features := map[string]map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		f := featureOf(name)
		if f == "?" {
			t.Errorf("%s is not <lang>.json or <feature>.<lang>.json for a shipped language", name)
			continue
		}
		language := name[strings.LastIndex(strings.TrimSuffix(name, ".json"), ".")+1 : len(name)-len(".json")]
		if features[f] == nil {
			features[f] = map[string]bool{}
		}
		features[f][language] = true
		for k := range keysOfFile(t, name) {
			if prev, dup := owner[language+"\x00"+k]; dup {
				t.Errorf("key %q is in %s and %s", k, prev, name)
			}
			owner[language+"\x00"+k] = name
		}
	}
	for f, langs := range features {
		for _, l := range Languages {
			if !langs[l] {
				t.Errorf("feature %q has no %s file", f, l)
			}
		}
	}
}

func TestSameKeysInAllLanguages(t *testing.T) {
	en, de := keysOf(t, "en.json"), keysOf(t, "de.json")
	for k, v := range en {
		if _, ok := de[k]; !ok {
			t.Errorf("German texts lack %q", k)
		}
		if k != v {
			t.Errorf("English texts: text of %q must equal the key, got %q", k, v)
		}
	}
	for k := range de {
		if _, ok := en[k]; !ok {
			t.Errorf("English texts lack %q", k)
		}
	}
}

// TestFeatureFilesPairKeys: every key of a feature file exists in both files of its pair.
func TestFeatureFilesPairKeys(t *testing.T) {
	entries, _ := translations.ReadDir("translations")
	for _, e := range entries {
		f := featureOf(e.Name())
		if f == "" || f == "?" {
			continue
		}
		en, de := keysOfFile(t, f+".en.json"), keysOfFile(t, f+".de.json")
		for k := range en {
			if _, ok := de[k]; !ok {
				t.Errorf("%s.de.json lacks %q", f, k)
			}
		}
		for k := range de {
			if _, ok := en[k]; !ok {
				t.Errorf("%s.en.json lacks %q", f, k)
			}
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

// TestLanguageFilesIncludeFeatureFiles: Init's forced language reads every file of the language,
// not only <lang>.json.
func TestLanguageFilesIncludeFeatureFiles(t *testing.T) {
	got, err := languageFiles("de")
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, f := range got {
		has[f] = true
	}
	if !has["de.json"] || !has["help.de.json"] || has["en.json"] || has["help.en.json"] {
		t.Errorf("de files: %v", got)
	}
}
