package typing

import "testing"

func TestDetectFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Layout
	}{
		{"empty", nil, LayoutUS},
		{"de_DE.UTF-8", map[string]string{"LANG": "de_DE.UTF-8"}, LayoutDE},
		{"de_AT", map[string]string{"LANG": "de_AT"}, LayoutDE},
		{"de-AT", map[string]string{"LANG": "de-AT"}, LayoutDE},
		{"de", map[string]string{"LANG": "de"}, LayoutDE},
		{"DE upper", map[string]string{"LANG": "DE_de.utf8"}, LayoutDE},
		{"modifier", map[string]string{"LANG": "de_DE@euro"}, LayoutDE},
		{"de_CH", map[string]string{"LANG": "de_CH.UTF-8"}, LayoutCHDE},
		{"en_US", map[string]string{"LANG": "en_US.UTF-8"}, LayoutUS},
		{"C", map[string]string{"LANG": "C"}, LayoutUS},
		{"deu", map[string]string{"LANG": "deu"}, LayoutUS},
		{"LC_ALL wins", map[string]string{"LC_ALL": "en_US", "LC_CTYPE": "de_DE", "LANG": "de_DE"}, LayoutUS},
		{"LC_CTYPE over LANG", map[string]string{"LC_CTYPE": "de_AT", "LANG": "en_US"}, LayoutDE},
		{"empty LC_ALL skipped", map[string]string{"LC_ALL": "", "LANG": "de_DE"}, LayoutDE},
	}
	for _, c := range cases {
		got := detectLayout(func(k string) string { return c.env[k] }, "")
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestDetectLayoutSystemLocale(t *testing.T) {
	cases := []struct {
		name   string
		env    map[string]string
		system string
		want   Layout
	}{
		{"de", nil, "de", LayoutDE},
		{"de-DE", nil, "de-DE", LayoutDE},
		{"de-AT", nil, "de-AT", LayoutDE},
		{"de-AT-Latn", nil, "de-AT-Latn", LayoutDE},
		{"de-Latn-AT", nil, "de-Latn-AT", LayoutDE},
		{"de-CH", nil, "de-CH", LayoutCHDE},
		{"en-AT", nil, "en-AT", LayoutUS},
		{"en", nil, "en", LayoutUS},
		{"empty", nil, "", LayoutUS},
		{"env US wins over system de", map[string]string{"LANG": "en_US.UTF-8"}, "de-AT", LayoutUS},
		{"env DE wins over system en", map[string]string{"LANG": "de_DE.UTF-8"}, "en", LayoutDE},
	}
	for _, c := range cases {
		got := detectLayout(func(k string) string { return c.env[k] }, c.system)
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Regression for #25: apps started from the Finder have no locale variables; with system
// language de-AT the result used to be US.
func TestDetectLayoutWithoutEnvUsesSystemLanguage(t *testing.T) {
	if got := detectLayout(func(string) string { return "" }, "de-AT"); got != LayoutDE {
		t.Errorf("got %v want DE", got)
	}
}
