package plex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// All item surfaces must retain the ordered version identities first issued
// to this viewer/device, even before admission, through a refill, expiry,
// probe and start. PMS decisions are projections of the selected Media,
// rather than replacement version lists (see the recorded decision corpus).
func TestVersionIdentityAcrossPlayFlow(t *testing.T) {
	for _, product := range []string{"Plex for LG", "Plex for Samsung", "Plex for Android (TV)", "Plex Web"} {
		for _, count := range []int{0, 1, 3} {
			t.Run(product+"/"+string(rune('0'+count)), func(t *testing.T) {
				f, pb := newVariantPlaybackFixture(t)
				full := testVariantLists()[fightClub.Key]
				f.resolver.byKey[fightClub.Key] = full[:count]
				path := "/library/metadata/" + fightClub.Key.String()
				headers := []string{"X-Plex-Product", product, "Accept", "application/json"}
				media := func(stage string, r *httptest.ResponseRecorder) []wireMedia {
					t.Helper()
					if r.Code != http.StatusOK {
						t.Fatalf("%s: %d %s", stage, r.Code, r.Body.String())
					}
					// Hubs and queues can contain several titles. Locate this item's
					// Media recursively without depending on a particular shelf layout.
					var root any
					if err := json.Unmarshal(r.Body.Bytes(), &root); err != nil {
						t.Fatal(err)
					}
					var found []wireMedia
					var visit func(any)
					visit = func(v any) {
						switch x := v.(type) {
						case map[string]any:
							if x["ratingKey"] == fightClub.Key.String() {
								b, _ := json.Marshal(x["Media"])
								if err := json.Unmarshal(b, &found); err != nil {
									t.Fatal(err)
								}
								return
							}
							for _, child := range x {
								visit(child)
							}
						case []any:
							for _, child := range x {
								visit(child)
							}
						}
					}
					visit(root)
					if len(found) == 0 {
						t.Fatalf("%s: no media: %s", stage, r.Body.String())
					}
					return found
				}
				ids := func(ms []wireMedia) []flexID {
					out := make([]flexID, len(ms))
					for i, m := range ms {
						out[i] = m.ID
						if len(m.Part) != 1 || m.Part[0].ID != m.ID {
							t.Fatalf("invalid part: %+v", m)
						}
					}
					return out
				}
				before := ids(media("hub", f.get(t, "/hubs/home/recentlyAdded", headers...)))
				wantCount := count
				if wantCount < 2 {
					wantCount = 1
				}
				if len(before) != wantCount {
					t.Fatalf("hub ids %v, want %d versions", before, wantCount)
				}
				check := func(stage, target string) {
					t.Helper()
					got := ids(media(stage, f.get(t, target, headers...)))
					if !reflect.DeepEqual(got, before) {
						t.Fatalf("%s ids %v, client holds %v", stage, got, before)
					}
				}
				// Search completes after the hub, before pre-play and queue creation.
				f.resolver.mu.Lock()
				f.resolver.byKey[fightClub.Key] = full
				f.resolver.mu.Unlock()
				check("pre-play", path+"?checkFiles=1&includeChapters=1&includeMarkers=1")
				req := httptest.NewRequest(http.MethodPost, "/playQueues?type=video&uri="+urlEsc(path), nil)
				req.Header.Set("X-Plex-Token", ownerToken)
				req.Header.Set("X-Plex-Client-Identifier", "client-1")
				req.Header.Set("X-Plex-Product", product)
				req.Header.Set("Accept", "application/json")
				qr := httptest.NewRecorder()
				f.srv.ServeHTTP(qr, req)
				if got := ids(media("queue", qr)); !reflect.DeepEqual(got, before) {
					t.Fatalf("queue ids %v, client holds %v", got, before)
				}
				// A stale stored choice must stay Auto for a singleton; it must never
				// pick a candidate that arrived after that singleton was rendered.
				query := "path=" + urlEsc(path) + "&mediaIndex=1&partIndex=9&session=flow339&hasMDE=1&directPlay=1&directStream=1&protocol=hls"
				dec := ids(media("decision", f.get(t, "/video/:/transcode/universal/decision?"+query, headers...)))
				selected := before[0]
				if count > 1 {
					selected = before[1]
				}
				if !reflect.DeepEqual(dec, []flexID{selected}) {
					t.Fatalf("decision ids %v, selected %d", dec, selected)
				}
				f.resolver.mu.Lock()
				f.resolver.byKey[fightClub.Key] = nil
				f.resolver.mu.Unlock()
				check("after decision and cache expiry", path)
				if r := f.get(t, "/video/:/transcode/universal/start.m3u8?"+query, headers...); r.Code != 200 {
					t.Fatalf("start: %d %s", r.Code, r.Body.String())
				}
				check("after start and probe", path+"?checkFiles=1&includeChapters=1")
				pb.mu.Lock()
				defer pb.mu.Unlock()
				if count < 2 && len(pb.variantCalls) != 0 {
					t.Fatalf("stale mediaIndex selected unissued release: %v", pb.variantCalls)
				}
				if count > 1 && (len(pb.variantCalls) == 0 || pb.variantCalls[0] != full[1].Release.ID) {
					t.Fatalf("wrong variant: %v", pb.variantCalls)
				}
			})
		}
	}
}
