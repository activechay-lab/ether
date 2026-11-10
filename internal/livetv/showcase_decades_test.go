package livetv

import "testing"

// Each premiere year belongs to one decade network, so no series or film can
// air on two of them.
func TestShowcaseDecadesDoNotOverlap(t *testing.T) {
	owner := map[int]string{}
	for _, r := range showcaseRecipes() {
		years := map[int]bool{}
		for _, b := range r.blocks {
			for _, src := range b.Sources {
				if f := src.Filter; f != nil {
					if f.YearFrom > f.YearTo {
						t.Fatalf("%s %s: empty window %d-%d", r.id, b.Name, f.YearFrom, f.YearTo)
					}
					for y := f.YearFrom; y <= f.YearTo; y++ {
						years[y] = true
					}
				}
			}
		}
		for y := r.filler.Filter.YearFrom; y <= r.filler.Filter.YearTo; y++ {
			years[y] = true
		}
		for y := range years {
			if other, ok := owner[y]; ok && other != r.id {
				t.Fatalf("%d is on both %s and %s", y, other, r.id)
			}
			owner[y] = r.id
		}
	}
}
