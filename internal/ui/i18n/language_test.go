package i18n

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/lang"
)

// The language tests run Init in a child process of the test binary: Init registers translations
// with Fyne process-wide, and the other tests of this package need a process without them.
const childLanguage = "OPENLAMT_TEST_LANGUAGE"

// TestUILanguageChild is the child process of TestUILanguageFollowsTheTexts: it runs Init with the
// setting from the environment and prints UILanguage and the text of a translated key.
func TestUILanguageChild(t *testing.T) {
	setting, ok := os.LookupEnv(childLanguage)
	if !ok {
		t.Skip("child process of TestUILanguageFollowsTheTexts")
	}
	if err := Init(setting); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("ui-language=%s connect=%s\n", UILanguage(), lang.L("Connect"))
}

// TestUILanguageFollowsTheTexts: UILanguage names the language the texts are shown in, for a
// forced setting and for System, where Fyne matches every OS locale, not only the first one.
func TestUILanguageFollowsTheTexts(t *testing.T) {
	// The OS locales are set through $LANGUAGE, which Fyne reads on Linux and the BSDs;
	// on Windows and macOS it asks the OS, so only the forced settings are checked there.
	envLocales := runtime.GOOS != "windows" && runtime.GOOS != "darwin"
	for _, c := range []struct {
		name, setting, osLocales, want, connect string
	}{
		{"forced English", English, "de", English, "Connect"},
		{"forced German", German, "en", German, "Verbinden"},
		{"system German", System, "de", German, "Verbinden"},
		{"system English", System, "en", English, "Connect"},
		{"system unsupported, then German", System, "it:de", German, "Verbinden"},
		{"system French", System, "fr", English, "Connect"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.setting == System && !envLocales {
				t.Skip("the OS locales come from the OS, not from $LANGUAGE")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestUILanguageChild$")
			cmd.Env = append(os.Environ(), childLanguage+"="+c.setting,
				"LC_ALL=", "LC_MESSAGES=", "LANG="+strings.Split(c.osLocales, ":")[0]+".UTF-8", "LANGUAGE="+c.osLocales)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child: %v\n%s", err, out)
			}
			if want := fmt.Sprintf("ui-language=%s connect=%s\n", c.want, c.connect); !strings.Contains(string(out), want) {
				t.Fatalf("child printed %q, want %q", out, want)
			}
		})
	}
}
