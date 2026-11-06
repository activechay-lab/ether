package families

import "testing"

// Every era must expose the complete shared moment contract to both renderers.
func TestDecadeMomentContract(t *testing.T) {
	for _, family := range []string{"retro", "grunge", "aqua", "flat"} {
		for _, moment := range []string{"ident", "upnext", "tonight", "break", "back", "offair", "bug", "lowerthird"} {
			d, err := Get(family, moment)
			if err != nil {
				t.Fatalf("%s/%s: %v", family, moment, err)
			}
			if d.Moment != moment || d.Duration != 8 || d.Poster >= d.Duration {
				t.Fatalf("invalid moment contract: %s/%s", family, moment)
			}
		}
	}
}
