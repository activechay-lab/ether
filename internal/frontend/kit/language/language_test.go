package language

import "testing"

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"pt-BR", "pt-BR"},
		{"pt_br", "pt-BR"},
		{"PT-br", "pt-BR"},
		{"pt", "pt"},
		{"por", "pt"},
		{"Portuguese", "pt"},
		{"Português", "pt"},
		{"português", "pt"},
		{"Brazilian Portuguese", "pt-BR"},
		{"portuguese-brazil", "pt-BR"},
		{"Portuguese (Brazil)", "pt-BR"},
		{"pt-PT", "pt-PT"},
		{"portuguese-portugal", "pt-PT"},
		{"es-419", "es-419"},
		{"spanish-latino", "es-419"},
		{"Spanish (Latin America)", "es-419"},
		{"eng", "en"},
		{"English", "en"},
		{"fr-CA", "fr-CA"},
		{"zh-Hant-TW", "zh-Hant-TW"},
		{"", ""},
		{"??", ""},
	}
	for _, tc := range cases {
		if got := Canonical(tc.in); got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pt-BR", "pt"}, {"por", "pt"}, {"Portuguese", "pt"},
		{"Brazilian Portuguese", "pt"}, {"es-419", "es"}, {"pt-PT", "pt"},
		{"", ""}, {"??", ""},
	} {
		if got := Base(tc.in); got != tc.want {
			t.Errorf("Base(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
