package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// variantEditions is a minimal frontend.Editions that serves a fixed ranked
// release list and records how often it was asked.
type variantEditions struct {
	variants []frontend.ReleaseVariant
	calls    int
}

func (e *variantEditions) Editions(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey) ([]frontend.Version, error) {
	return nil, nil
}

func (e *variantEditions) Variants(_ context.Context, _ frontend.ResolveRequest, max int) ([]frontend.ReleaseVariant, error) {
	e.calls++
	if len(e.variants) > max {
		return e.variants[:max], nil
	}
	return e.variants, nil
}

func (e *variantEditions) RefillSearchCache(*frontend.Title) <-chan struct{} { return nil }
func (e *variantEditions) Available(context.Context, frontend.RatingKey) (bool, bool) {
	return false, false
}

// variantHandles issues a deterministic handle per release id and resolves
// only ids it issued.
type variantHandles struct {
	item    frontend.RatingKey
	allowed map[string]bool
}

func newVariantHandles(item frontend.RatingKey, releases ...string) *variantHandles {
	allowed := make(map[string]bool, len(releases))
	for _, r := range releases {
		allowed[r] = true
	}
	return &variantHandles{item: item, allowed: allowed}
}

func handleFor(release string) string {
	// A stable, UUID-shaped id per release, unrelated to the release name.
	sum := 0
	for _, c := range release {
		sum = sum*31 + int(c)
	}
	return ids.RatingKeyGUID(frontend.RatingKey(1<<40 + sum))
}

func (h *variantHandles) Issue(_ context.Context, _ frontend.VersionScope, item frontend.RatingKey, release string) (string, error) {
	if item != h.item || !h.allowed[release] {
		return "", errors.New("not offered")
	}
	return handleFor(release), nil
}

func (h *variantHandles) Resolve(_ context.Context, _ frontend.VersionScope, id string) (frontend.VersionTarget, error) {
	for release := range h.allowed {
		if handleFor(release) == id {
			return frontend.VersionTarget{Item: h.item, Release: release}, nil
		}
	}
	return frontend.VersionTarget{}, errors.New("unknown handle")
}

func (h *variantHandles) MatchSession(context.Context, frontend.VersionScope, string, string) error {
	return errors.New("no session")
}

func releaseVariant(id, name string, height int, size int64) frontend.ReleaseVariant {
	info := frontend.MediaInfo{Estimated: true, Container: "mkv", Size: size, BitrateKbps: 8000, Streams: []frontend.StreamInfo{{Type: "video", Width: height * 16 / 9, Height: height}, {Type: "audio", Codec: "truehd", Channels: 8}}}
	return frontend.ReleaseVariant{
		ID:      id,
		Label:   name,
		Release: frontend.Release{ID: id, Name: name, Size: size, Info: frontend.ReleaseInfo{Height: height, VideoCodec: "hevc", Source: "webdl", AudioCodec: "truehd", Channels: "7.1"}},
		Info:    info,
	}
}

func mountVariants(t *testing.T, front frontend.Frontend, editions *variantEditions, handles *variantHandles, sessions frontendtest.Sessions, detail frontend.ItemDetail) frontend.Instance {
	t.Helper()
	viewer := &frontend.Viewer{ID: 1}
	identity := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	experience := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return detail, nil
	}}
	inst, err := front.Mount(context.Background(), frontend.Host{
		Identity:       identity,
		Experience:     experience,
		Sessions:       sessions,
		Capabilities:   frontendtest.Capabilities{},
		Editions:       editions,
		VersionHandles: handles,
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func TestDetailListsAutoAndReleaseVariants(t *testing.T) {
	variants := []frontend.ReleaseVariant{
		{Auto: true, Release: frontend.Release{ID: "auto-rel", Info: frontend.ReleaseInfo{Height: 1080, Source: "bluray"}}, Info: frontend.MediaInfo{Estimated: true}},
		releaseVariant("rel-2160", "2160p WEB-DL HEVC DV", 2160, 18_400_000_000),
		releaseVariant("rel-1080", "1080p BluRay HEVC", 1080, 4_200_000_000),
	}
	editions := &variantEditions{variants: variants}
	for _, tc := range []struct {
		name  string
		front frontend.Frontend
		path  string
		token string
	}{
		{"jellyfin", Jellyfin{MachineID: "test"}, "/Items/" + ids.RatingKeyGUID(5), "X-Emby-Token"},
		{"emby", Emby{MachineID: "test"}, "/Items/" + ids.RatingKeyGUID(5), "X-Emby-Token"},
	} {
		handles := newVariantHandles(5, "rel-2160", "rel-1080")
		inst := mountVariants(t, tc.front, editions, handles, frontendtest.Sessions{}, frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie", Title: "Movie"}})
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set(tc.token, "grant")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
		body := w.Body.String()
		sources := mediaSourcesFrom(t, w.Body.Bytes())
		if len(sources) != 3 {
			t.Fatalf("%s: want Auto + 2 variants, got %d: %s", tc.name, len(sources), body)
		}
		if sources[0]["Name"] != "Auto" {
			t.Fatalf("%s: first source is not Auto: %v", tc.name, sources[0])
		}
		if sources[0]["Id"] != ids.RatingKeyGUID(5) {
			t.Fatalf("%s: Auto Id changed: %v", tc.name, sources[0]["Id"])
		}
		if sources[1]["Id"] != handleFor("rel-2160") || sources[2]["Id"] != handleFor("rel-1080") {
			t.Fatalf("%s: variant handles missing: %v", tc.name, sources)
		}
		name, _ := sources[1]["Name"].(string)
		if !strings.Contains(name, "2160p") || !strings.Contains(name, "HEVC") || !strings.Contains(name, "TrueHD") || !strings.Contains(name, "GB") {
			t.Fatalf("%s: variant name not built from facts: %q", tc.name, name)
		}
		if strings.Contains(body, "rel-2160") || strings.Contains(body, "rel-1080") {
			t.Fatalf("%s: release ids leaked: %s", tc.name, body)
		}
	}
}

func TestPlaybackInfoListsAutoAndReleaseVariants(t *testing.T) {
	variants := []frontend.ReleaseVariant{
		{Auto: true, Release: frontend.Release{ID: "auto-rel", Info: frontend.ReleaseInfo{Height: 1080}}, Info: frontend.MediaInfo{Estimated: true}},
		releaseVariant("rel-2160", "2160p", 2160, 18_000_000_000),
	}
	editions := &variantEditions{variants: variants}
	handles := newVariantHandles(5, "rel-2160")
	sessions := frontendtest.Sessions{PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
		return frontend.PlayPlan{ID: "plan", Media: frontend.MediaInfo{Container: "mkv"}, Decision: frontend.Decision{Kind: "directplay", VideoCopy: true}, DirectPlay: true}, nil
	}}
	inst := mountVariants(t, Jellyfin{MachineID: "test"}, editions, handles, sessions, frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie", Title: "Movie"}})
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":false}`))
	r.Header.Set("X-Emby-Token", "grant")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	sources := mediaSourcesFrom(t, w.Body.Bytes())
	if len(sources) != 2 || sources[0]["Name"] != "Auto" || sources[1]["Id"] != handleFor("rel-2160") {
		t.Fatalf("expected Auto + variant: %v", sources)
	}
}

func TestPlaybackInfoVariantIdPlansThatRelease(t *testing.T) {
	handles := newVariantHandles(5, "rel-2160")
	var planned frontend.PlanRequest
	sessions := frontendtest.Sessions{PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
		planned = req
		return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "directplay", VideoCopy: true}}, nil
	}}
	inst := mountVariants(t, Jellyfin{MachineID: "test"}, &variantEditions{}, handles, sessions, frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie", Title: "Movie"}})
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo?MediaSourceId="+handleFor("rel-2160"), strings.NewReader(`{"IsPlayback":false}`))
	r.Header.Set("X-Emby-Token", "grant")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || planned.Version != handleFor("rel-2160") || !planned.Intent.ExactVersion {
		t.Fatal(w.Code, planned, w.Body.String())
	}
}

func TestAutoSkipsVideoTranscodeVariant(t *testing.T) {
	variants := []frontend.ReleaseVariant{
		{Auto: true, Release: frontend.Release{ID: "auto-rel", Info: frontend.ReleaseInfo{Height: 2160}}, Info: frontend.MediaInfo{Estimated: true, Streams: []frontend.StreamInfo{{Type: "video", Height: 2160}}}},
		releaseVariant("rel-vt", "transcode", 2160, 18_000_000_000),
		releaseVariant("rel-dp", "direct play", 1080, 4_000_000_000),
	}
	editions := &variantEditions{variants: variants}
	handles := newVariantHandles(5, "rel-vt", "rel-dp")
	var versions []string
	var final frontend.PlanRequest
	sessions := frontendtest.Sessions{PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
		versions = append(versions, req.Version)
		version := req.Version
		if version == handleFor("rel-dp") {
			final, version = req, "rel-dp"
		}
		switch version {
		case "rel-vt":
			return frontend.PlayPlan{Estimated: true, Media: frontend.MediaInfo{Estimated: true, Streams: []frontend.StreamInfo{{Type: "video", Height: 2160}}}, Decision: frontend.Decision{Kind: "transcode", VideoCopy: false}}, nil
		case "rel-dp":
			if req.Intent.Estimate {
				return frontend.PlayPlan{Estimated: true, Media: frontend.MediaInfo{Estimated: true, Streams: []frontend.StreamInfo{{Type: "video", Height: 1080}}}, Decision: frontend.Decision{Kind: "directplay", VideoCopy: true}}, nil
			}
			return frontend.PlayPlan{ID: "plan", Media: frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "video", Height: 1080}}}, Decision: frontend.Decision{Kind: "directplay", VideoCopy: true}}, nil
		default:
			// Auto: a video transcode.
			return frontend.PlayPlan{ID: "plan", Media: frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "video", Height: 2160}}}, Decision: frontend.Decision{Kind: "transcode", VideoCopy: false}}, nil
		}
	}}
	inst := mountVariants(t, Jellyfin{MachineID: "test"}, editions, handles, sessions, frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie", Title: "Movie"}})
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true}`))
	r.Header.Set("X-Emby-Token", "grant")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Auto estimated (never opened), rel-vt and rel-dp trialled as
	// estimates, then rel-dp planned for real through its handle, exactly
	// like a client pick, so the stream the client opens with that
	// MediaSource Id matches the session.
	want := []string{"", "rel-vt", "rel-dp", handleFor("rel-dp")}
	if strings.Join(versions, ",") != strings.Join(want, ",") {
		t.Fatalf("trial order wrong: %v", versions)
	}
	if final.ResolvedRelease != "rel-dp" || !final.Intent.ExactVersion || final.Intent.Estimate {
		t.Fatalf("switched plan not an exact handle pick: %+v", final)
	}
	// The switched source is identified by its scoped handle, not the raw id.
	sources := mediaSourcesFrom(t, w.Body.Bytes())
	if len(sources) != 1 || sources[0]["Id"] != handleFor("rel-dp") {
		t.Fatalf("switched source id wrong: %v", sources)
	}
	if strings.Contains(w.Body.String(), "rel-dp") || strings.Contains(w.Body.String(), "auto-rel") {
		t.Fatalf("release id leaked: %s", w.Body.String())
	}
}

func TestAutoGivesUpAfterThreeTrials(t *testing.T) {
	variants := []frontend.ReleaseVariant{
		{Auto: true, Release: frontend.Release{ID: "auto-rel"}, Info: frontend.MediaInfo{Estimated: true, Streams: []frontend.StreamInfo{{Type: "video", Height: 2160}}}},
		releaseVariant("r1", "r1", 2160, 1),
		releaseVariant("r2", "r2", 2160, 2),
		releaseVariant("r3", "r3", 2160, 3),
		releaseVariant("r4", "r4", 2160, 4),
	}
	editions := &variantEditions{variants: variants}
	handles := newVariantHandles(5, "r1", "r2", "r3", "r4")
	trials := 0
	sessions := frontendtest.Sessions{PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
		if req.Intent.Estimate && req.Version != "" {
			trials++
		}
		return frontend.PlayPlan{ID: "plan", Media: frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "video", Height: 2160}}}, Decision: frontend.Decision{Kind: "transcode", VideoCopy: false}}, nil
	}}
	inst := mountVariants(t, Jellyfin{MachineID: "test"}, editions, handles, sessions, frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie", Title: "Movie"}})
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true}`))
	r.Header.Set("X-Emby-Token", "grant")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || trials != maxAutoTranscodeTrials {
		t.Fatalf("want %d trials, got %d (%d %s)", maxAutoTranscodeTrials, trials, w.Code, w.Body.String())
	}
}

func mediaSourcesFrom(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var parsed struct {
		MediaSources []map[string]any
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.MediaSources
}
