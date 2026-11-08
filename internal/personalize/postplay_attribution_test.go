package personalize

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type postplayCaptureCatalog struct {
	core.Catalog
	source        core.Title
	candidates    []core.Title
	gets          []core.RatingKey
	similarInputs []core.RatingKey
}

func (c *postplayCaptureCatalog) Get(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	c.gets = append(c.gets, key)
	if key == c.source.Key {
		cp := c.source
		return &cp, nil
	}
	return c.Catalog.Get(ctx, key)
}
func (c *postplayCaptureCatalog) Similar(_ context.Context, key core.RatingKey, _ int) ([]core.Title, error) {
	c.similarInputs = append(c.similarInputs, key)
	return slices.Clone(c.candidates), nil
}

type postplayCaptureState struct {
	core.State
	batches [][]core.RatingKey
}

func (s *postplayCaptureState) GetMany(ctx context.Context, user int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	s.batches = append(s.batches, slices.Clone(keys))
	return s.State.GetMany(ctx, user, keys)
}

// Capture actual source, candidate and watch-cache keys. Reordering a fixed
// pool must not change the score/key ranking; a session cache must not read
// a second pool. This does not freeze Home's intentional visit sampling.
func TestPostPlayCandidateOrderAndCacheInputs(t *testing.T) {
	h := newHarness(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := waitGenericReady(h.e, ctx); err != nil {
		t.Fatal(err)
	}
	u := core.User{ID: 93, Username: "attribution"}
	source := core.Title{Key: core.MovieKey(8999), Type: core.TypeMovie, Genres: []string{"Drama"}, Runtime: 90 * time.Minute, Released: testNow.AddDate(-1, 0, 0)}
	cat := &postplayCaptureCatalog{Catalog: h.cat, source: source}
	st := &postplayCaptureState{State: h.st}
	h.e.catalog = cat
	h.e.state = st
	h.e.mu.Lock()
	h.e.models[u.ID] = &userModel{user: u, builtAt: testNow, taste: buildTaste(tasteInput{userID: u.ID, now: testNow, loc: time.UTC})}
	h.e.mu.Unlock()
	hc := hubContext(testNow, core.DeviceTV)
	want := []core.RatingKey{core.MovieKey(9001), core.MovieKey(9002), core.MovieKey(9003)}
	for _, order := range [][]int{{1, 2, 3, 4, 5, 6}, {6, 5, 4, 3, 2, 1}, {3, 1, 5, 2, 6, 4}} {
		cat.candidates = nil
		cat.gets = nil
		cat.similarInputs = nil
		st.batches = nil
		for _, n := range order {
			x := source
			x.Key = core.MovieKey(9000 + int64(n))
			x.Title = "Equal score"
			cat.candidates = append(cat.candidates, x)
		}
		hs := h.e.hookState()
		hs.mu.Lock()
		clear(hs.postplay)
		hs.mu.Unlock()
		picks, err := h.e.PostPlay(ctx, &u, source.Key, hc, 3)
		if err != nil {
			t.Fatal(err)
		}
		var got []core.RatingKey
		for _, p := range picks {
			got = append(got, p.Title.Key)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v picks %v want %v", order, got, want)
		}
		if len(st.batches) != 1 || len(st.batches[0]) != 6 || !reflect.DeepEqual(cat.gets, []core.RatingKey{source.Key}) || !reflect.DeepEqual(cat.similarInputs, []core.RatingKey{source.Key}) {
			t.Fatalf("unexpected inputs: get=%v similar=%v batches=%v", cat.gets, cat.similarInputs, st.batches)
		}
		keys := slices.Clone(st.batches[0])
		slices.Sort(keys)
		for i, key := range keys {
			if key != core.MovieKey(9001+int64(i)) {
				t.Fatalf("candidate set %v", keys)
			}
		}
		t.Logf("clock=%s seed=none input_order=%v watch_batch=%v picks=%v", testNow.Format(time.RFC3339), order, st.batches[0], got)
		// Different underlying pool during the same cached session: no source read.
		cat.candidates = nil
		again, err := h.e.PostPlay(ctx, &u, source.Key, hc, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(again, picks) || len(cat.similarInputs) != 1 || len(st.batches) != 1 {
			t.Fatal("cached picks changed or reread candidates")
		}
	}
}
