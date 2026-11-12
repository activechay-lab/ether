package plex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// fakePalette answers the palettes a test preloads, keyed by title key and
// image path, and records every lookup so the test can prove the route asked
// for the image the url named.
type fakePalette struct {
	colors  map[string]frontend.ArtworkPalette
	lookups []string
}

func (p *fakePalette) Lookup(key frontend.RatingKey, path string) *frontend.ArtworkPalette {
	p.lookups = append(p.lookups, key.String()+"|"+path)
	c, ok := p.colors[key.String()+"|"+path]
	if !ok {
		return nil
	}
	return &c
}

func (p *fakePalette) Preload(context.Context, []frontend.ArtworkPaletteWant) {}
func (p *fakePalette) PersistFor(func(context.Context, frontend.RatingKey) bool) {
}

func TestUltraBlurColorsRoute(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	pal := &fakePalette{colors: map[string]frontend.ArtworkPalette{
		fightClub.Key.String() + "|/poster550.jpg": {
			TopLeft: "112233", TopRight: "445566", BottomLeft: "778899", BottomRight: "aabbcc",
		},
		bbE1.Key.String() + "|/pilot.jpg": {
			TopLeft: "010203", TopRight: "040506", BottomLeft: "070809", BottomRight: "0a0b0c",
		},
	}}
	f.srv.setUltra(pal)

	// A movie thumb resolves its poster path and answers the cached colours.
	for _, prefix := range []string{"/service", "/services"} {
		rec := f.get(t, prefix+"/ultrablur/colors?url=/library/metadata/"+fightClub.Key.String()+"/thumb/1700000000", jsonAccept...)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", prefix, rec.Code, rec.Body.String())
		}
		var v struct {
			MediaContainer struct {
				Size            int `json:"size"`
				UltraBlurColors *struct {
					TopLeft, TopRight, BottomLeft, BottomRight string
				} `json:"UltraBlurColors"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v", prefix, err)
		}
		u := v.MediaContainer.UltraBlurColors
		if v.MediaContainer.Size != 1 || u == nil || u.TopLeft != "112233" || u.BottomRight != "aabbcc" {
			t.Fatalf("%s: unexpected body: %s", prefix, rec.Body.String())
		}
	}

	// An episode thumb uses its still, not its (absent) poster.
	rec := f.get(t, "/services/ultrablur/colors?url=/library/metadata/"+bbE1.Key.String()+"/thumb/1700000000", jsonAccept...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"topLeft":"010203"`) {
		t.Fatalf("episode still colours: %d %s", rec.Code, rec.Body.String())
	}
	// XML clients get the same palette as a single child element.
	xmlRec := f.get(t, "/services/ultrablur/colors?url=/library/metadata/"+bbE1.Key.String()+"/thumb/1700000000")
	if xmlRec.Code != 200 || !strings.Contains(xmlRec.Body.String(), `<UltraBlurColors topLeft="010203"`) {
		t.Fatalf("episode still colours (xml): %d %s", xmlRec.Code, xmlRec.Body.String())
	}
	found := false
	for _, l := range pal.lookups {
		if l == bbE1.Key.String()+"|/pilot.jpg" {
			found = true
		}
	}
	if !found {
		t.Fatalf("episode still was not looked up: %v", pal.lookups)
	}

	// Unknown, off-server, and non-image urls are all 404.
	unknown := core.MovieKey(999999)
	for _, url := range []string{
		"/library/metadata/" + unknown.String() + "/thumb/1",
		"https://evil.example/library/metadata/" + fightClub.Key.String() + "/thumb/1",
		"/library/sections/1/all",
		"/library/metadata/" + fightClub.Key.String() + "/children",
		"",
	} {
		rec := f.get(t, "/service/ultrablur/colors?url="+url, jsonAccept...)
		if rec.Code != 404 {
			t.Fatalf("url %q: status %d, want 404", url, rec.Code)
		}
	}
}
