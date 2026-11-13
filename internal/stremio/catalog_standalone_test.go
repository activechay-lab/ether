package stremio

import "testing"

func TestCatalogStandaloneExtras(t *testing.T) {
	for _, tc := range []struct {
		cat  Catalog
		want bool
	}{
		{Catalog{}, true},
		{Catalog{ExtraSupported: []string{"search"}}, true},
		{Catalog{ExtraRequired: []string{"ids"}}, false},
		{Catalog{Extra: []Extra{{Name: "ids", IsRequired: true}}}, false},
		{Catalog{Extra: []Extra{{Name: "ids"}}, ExtraRequired: []string{"ids"}}, false},
	} {
		if got := tc.cat.Standalone(); got != tc.want {
			t.Errorf("%+v: Standalone() = %v, want %v", tc.cat, got, tc.want)
		}
	}
}
