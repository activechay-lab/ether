package raster_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGoldenAndDeterminism(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		type entry struct {
			Family   string `json:"family"`
			Moment   string `json:"moment"`
			Bindings any    `json:"bindings"`
		}
		var entries []entry
		for family, moments := range families.Names() {
			for _, moment := range moments {
				d, err := families.Get(family, moment)
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, entry{family, d.Moment, families.Defaults(family)})
			}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Family != entries[j].Family {
				return entries[i].Family < entries[j].Family
			}
			return entries[i].Moment < entries[j].Moment
		})
		data, _ := json.MarshalIndent(entries, "", "  ")
		if err := os.WriteFile("testdata/manifest.json", data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	for family, moments := range families.Names() {
		for _, moment := range moments {
			times := []float64{.5, 3, 5.5}
			if family == "signal" {
				times = []float64{1.5}
			}
			for _, at := range times {
				t.Run(fmt.Sprintf("%s-%s-%.1f", family, moment, at), func(t *testing.T) {
					d, e := families.Get(family, moment)
					if e != nil {
						t.Fatal(e)
					}
					b := raster.Bindings{Bindings: families.Defaults(family)}
					im := raster.Render(d, b, at, 480, 270)
					other := raster.Render(d, b, at, 480, 270)
					if !bytes.Equal(im.Pix, other.Pix) {
						t.Fatal("non-deterministic")
					}
					name := filepath.Join("testdata", fmt.Sprintf("%s-%s-%.1f.png", family, moment, at))
					if os.Getenv("UPDATE_GOLDEN") == "1" {
						os.MkdirAll("testdata", 0755)
						f, e := os.Create(name)
						if e != nil {
							t.Fatal(e)
						}
						e = png.Encode(f, im)
						f.Close()
						if e != nil {
							t.Fatal(e)
						}
					}
					f, e := os.Open(name)
					if e != nil {
						t.Fatal(e)
					}
					gold, e := png.Decode(f)
					f.Close()
					if e != nil {
						t.Fatal(e)
					}
					sum := 0.0
					for y := 0; y < 270; y++ {
						for x := 0; x < 480; x++ {
							a, b, c, _ := im.At(x, y).RGBA()
							aa, bb, cc, _ := gold.At(x, y).RGBA()
							delta := .2126*math.Abs(float64(a)-float64(aa)) + .7152*math.Abs(float64(b)-float64(bb)) + .0722*math.Abs(float64(c)-float64(cc))
							sum += delta / 65535
						}
					}
					if sum/(480*270) > .005 {
						t.Fatalf("perceptual mean error %.6f", sum/(480*270))
					}
				})
			}
		}
	}
}
func BenchmarkRender(b *testing.B) {
	for family, moments := range families.Names() {
		for _, moment := range moments {
			for _, w := range []int{1920, 960} {
				b.Run(fmt.Sprintf("%s/%s/%dx%d", family, moment, w, w*9/16), func(b *testing.B) {
					d, e := families.Get(family, moment)
					if e != nil {
						b.Fatal(e)
					}
					bindings := raster.Bindings{Bindings: families.Defaults(family)}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						raster.Render(d, bindings, 4.8, w, w*9/16)
					}
				})
			}
		}
	}
}

// TestParity runs unchanged natively and under GOOS=js GOARCH=wasm, using the
// same scene/bindings/t/dimensions as the Chrome loader smoke measurement.
func TestParity(t *testing.T) {
	d, e := families.Get("late-night", "ident")
	if e != nil {
		t.Fatal(e)
	}
	im := raster.Render(d, raster.Bindings{Bindings: families.Defaults("late-night")}, 3, 960, 540)
	got := fmt.Sprintf("%x", sha256.Sum256(im.Pix))
	if got != "79a1ac849149cd8c5290d3f5be1cfd4e6aac3fe6a95b30f751873e3af2789c5f" {
		t.Fatalf("parity hash: %s", got)
	}
}
