package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestLayoutDocumentEndpoints(t *testing.T) {
	e := &fakeEngine{}
	h := personalizationHandler(t, e)
	rec := serve(h, req(http.MethodGet, "/ether/api/layout", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"layout":{"global":[]`) || !strings.Contains(rec.Body.String(), `"defaults":{"maxRows"`) {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	draft := `{"viewerId":2,"layout":{"global":[{"section":"home","rows":[{"row":"a","status":"always","slot":1}]}],"viewers":[]},"device":"mobile","at":"2026-10-17T20:30:00Z","section":"movies","count":20}`
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", draft))
	if rec.Code != 200 {
		t.Fatalf("preview %d %s", rec.Code, rec.Body)
	}
	if e.who != 2 || e.hc.Device != "mobile" || e.section != "movies" || e.limit != 20 || e.writes != 0 || len(e.doc.Global) != 0 {
		t.Fatalf("preview inputs/persistence %+v", e)
	}
	for _, v := range []string{`"layer":"global"`, `"slot":1`, `"anchorSlot":1`, `"placement":"global anchor at slot 1"`, `"policyLayer":"global"`, `"viewer":{"id":2,"title":"bob"}`} {
		if !strings.Contains(rec.Body.String(), v) {
			t.Fatalf("missing %s in %s", v, rec.Body)
		}
	}
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/impact", draft))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"changed":2`) || !strings.Contains(rec.Body.String(), `"added":["New"]`) {
		t.Fatalf("impact %d %s", rec.Code, rec.Body)
	}
	if e.writes != 0 {
		t.Fatal("impact wrote a layout")
	}
	rec = serve(h, req(http.MethodPut, "/ether/api/layout", `{"layout":{"global":[],"viewers":[]}}`))
	if rec.Code != 200 || e.writes != 1 {
		t.Fatalf("save %d %s writes %d", rec.Code, rec.Body, e.writes)
	}
	for _, body := range []string{`{"device":"fridge"}`, `{"at":"yesterday"}`, `{"count":101}`, `{"viewerId":99}`} {
		rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", body))
		if rec.Code != 400 && rec.Code != 404 {
			t.Fatalf("bad preview %s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestLayoutPreviewCarriesDialsAndHomeDeduped(t *testing.T) {
	e := &fakeEngine{}
	h := personalizationHandler(t, e)
	draft := `{"layout":{"global":[{"section":"movies","dedupeHome":false,"tuning":{"discovery":0.8,"variety":0.2},"rows":[]}],"viewers":[]},"section":"movies"}`
	rec := serve(h, req(http.MethodPost, "/ether/api/layout/preview", draft))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"homeDeduped":12`) {
		t.Fatalf("preview %d %s", rec.Code, rec.Body)
	}
	p := e.draft.Global[0]
	if p.DedupeHome == nil || *p.DedupeHome || p.Tuning == nil || *p.Tuning.Discovery != 0.8 || p.Tuning.Freshness != nil || *p.Tuning.Variety != 0.2 {
		t.Fatalf("draft dials not passed through: %+v", p)
	}
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", `{"section":"home"}`))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "homeDeduped") {
		t.Fatalf("home preview %d %s", rec.Code, rec.Body)
	}
}

// schedulingStudio is a CollectionStudio that owns collection row schedules.
type schedulingStudio struct {
	*fakeStudio
	schedules map[string]*core.CollectionSchedule
	sets      []string
}

func (s *schedulingStudio) CollectionSchedules(context.Context) (map[string]*core.CollectionSchedule, error) {
	return s.schedules, nil
}
func (s *schedulingStudio) ValidateSchedule(sc *core.CollectionSchedule) error {
	if sc != nil {
		for _, m := range sc.Months {
			if m < 1 || m > 12 {
				return fieldErr{"months": "months are 1 to 12"}
			}
		}
	}
	return nil
}
func (s *schedulingStudio) SetSchedule(_ context.Context, id string, sc *core.CollectionSchedule) error {
	if id == "gone" {
		return core.ErrNotFound
	}
	s.sets = append(s.sets, id)
	if sc == nil {
		delete(s.schedules, id)
	} else {
		s.schedules[id] = sc
	}
	return nil
}

func TestLayoutCarriesCollectionSchedules(t *testing.T) {
	e := &fakeEngine{}
	studio := &schedulingStudio{fakeStudio: newFakeStudio(), schedules: map[string]*core.CollectionSchedule{"1": {Days: []int{5}}, "2": {Months: []int{12}}}}
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, IsOwnerToken: storedOwner(cfg), Personalization: e, Collections: studio})
	rec := serve(h, req(http.MethodGet, "/ether/api/layout", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"collectionSchedules":{"1":{"days":[5]},"2":{"months":[12]}}`) {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	rec = serve(h, req(http.MethodPut, "/ether/api/layout", `{"layout":{"global":[],"viewers":[],"collectionSchedules":{"1":{"months":[13]}}}}`))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"layout.collectionSchedules.1.months"`) || e.writes != 0 || len(studio.sets) != 0 {
		t.Fatalf("invalid schedule %d %s writes %d sets %v", rec.Code, rec.Body, e.writes, studio.sets)
	}
	rec = serve(h, req(http.MethodPut, "/ether/api/layout", `{"layout":{"global":[],"viewers":[],"collectionSchedules":{"1":{"days":[0,6]},"2":null,"gone":null}}}`))
	if rec.Code != 200 || e.writes != 1 || len(studio.sets) != 2 {
		t.Fatalf("save %d %s writes %d sets %v", rec.Code, rec.Body, e.writes, studio.sets)
	}
	if e.doc.CollectionSchedules != nil {
		t.Fatalf("schedules reached the layout store: %+v", e.doc.CollectionSchedules)
	}
	if sc := studio.schedules["1"]; sc == nil || len(sc.Days) != 2 || studio.schedules["2"] != nil {
		t.Fatalf("schedules: %+v", studio.schedules)
	}
	if !strings.Contains(rec.Body.String(), `"collectionSchedules":{"1":{"days":[0,6]}}`) {
		t.Fatalf("saved doc %s", rec.Body)
	}
	// Previews pass a draft's schedules to the engine.
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", `{"layout":{"global":[],"viewers":[],"collectionSchedules":{"1":null}}}`))
	if rec.Code != 200 {
		t.Fatalf("preview %d %s", rec.Code, rec.Body)
	}
	if sc, ok := e.draft.CollectionSchedules["1"]; !ok || sc != nil {
		t.Fatalf("draft schedules: %+v", e.draft.CollectionSchedules)
	}
}
