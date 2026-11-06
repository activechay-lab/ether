package splicecheck

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAuthoredTimelineBoundaries(t *testing.T) {
	// No external tools: synthetic observations cover ramp spacing, a terminal
	// end boundary, and a subset beginning in the steady part of the title.
	bounds := []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second, 12 * time.Second}
	observationAt := func(start, end float64) observation {
		x := observation{first: start, last: end - .04}
		for n := start; n < end-.001; n += .04 {
			x.frames = append(x.frames, frame{PTS: fmt.Sprint(n)})
		}
		return x
	}
	for _, tc := range []struct {
		name    string
		indices []int
		spans   [][2]float64
		pass    bool
	}{
		{"ramp", []int{0, 1, 2}, [][2]float64{{0, 2}, {2, 4}, {4, 8}}, true},
		{"steady subset", []int{2, 3}, [][2]float64{{4, 8}, {8, 12}}, true},
		{"wrong grid", []int{0, 1, 2}, [][2]float64{{0, 4}, {4, 8}, {8, 12}}, false},
		{"truncated terminal", []int{3}, [][2]float64{{8, 10}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var obs []observation
			var segs []Segment
			for i, n := range tc.indices {
				obs = append(obs, observationAt(tc.spans[i][0], tc.spans[i][1]))
				segs = append(segs, Segment{Index: n})
			}
			r := Report{GridDurations: Result{Pass: true}}
			timing(obs, segs, &r, Options{Timeline: bounds})
			if r.GridDurations.Pass != tc.pass {
				t.Fatal(r.GridDurations)
			}
		})
	}
	for _, bounds := range [][]time.Duration{{0}, {0, 0}, {-1, time.Second}, {time.Second, 0}} {
		_, err := Check(context.Background(), []Segment{{Index: 0}}, Options{Timeline: bounds})
		if err == nil || !strings.Contains(err.Error(), "timeline") {
			t.Fatalf("invalid timeline %v: %v", bounds, err)
		}
	}
}
