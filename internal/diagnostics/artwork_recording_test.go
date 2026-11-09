package diagnostics

import (
	"strings"
	"testing"
)

func TestArtworkWithoutDeviceIsViewerScopedAndUnattributed(t *testing.T) {
	r, clk := newTestRecorder(t)
	r.Observe(Event{Time: clk.t, Kind: KindDevice, DeviceKey: "phone", ViewerID: 7})
	c, err := r.Start("owner", StartIn{DeviceID: r.Devices()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Kind: KindRequest, Route: "/photo/:/transcode", Status: 403, ViewerID: 7},
		{Kind: KindRequest, Route: "/library/metadata/1/thumb/1", Status: 404, ViewerID: 7},
		{Kind: KindRequest, Route: "/photo/:/transcode", Status: 500, ViewerID: 8},
		{Kind: KindRequest, Route: "/photo/:/transcode", Status: 401},
		{Kind: KindRequest, Route: "/photo/:/transcode", Status: 502, ViewerID: 7, DeviceKey: "other"},
		{Kind: KindRequest, Route: "/library/metadata/1", Status: 503, ViewerID: 7},
	} {
		e.Time = clk.t
		r.Observe(e)
	}
	capture := r.captures[c.ID]
	events := capture.evidenceCopy(capture.started, capture.deadline)
	// The two viewer artwork rejections, plus — since #365 — every other
	// rejection authenticated as this viewer (the 502 from another client
	// and the 503 metadata request), all unattributed. Another viewer's
	// and unauthenticated rejections stay out.
	if len(events) != 4 {
		t.Fatalf("retained %+v", events)
	}
	for _, e := range events {
		if !e.Uncorrelated || e.DeviceKey != "" || e.ViewerID != 7 || e.Status < 400 {
			t.Fatal(e)
		}
	}
	body, skipped, _ := eventsJSONL(events, [3]bool{}, c.ID, nil)
	if skipped != 0 || strings.Count(body, `"uncorrelated":true`) != 4 || strings.Contains(body, `"device":`) {
		t.Fatalf("export: %s skipped=%d", body, skipped)
	}
}

func TestArtworkRequestPaths(t *testing.T) {
	for _, path := range []string{"/photo/:/transcode", "/library/metadata/1/thumb/2", "/library/metadata/1/art/2", "/library/metadata/1/discoveryPoster/v", "/playlists/1/composite", "/library/collections/1/thumb/2"} {
		if !ArtworkRequestPath(path) {
			t.Errorf("missed %s", path)
		}
	}
	for _, path := range []string{"/library/metadata/1", "/hubs/sections/1", "/library/metadata/1/children", "/arbitrary/thumb/1"} {
		if ArtworkRequestPath(path) {
			t.Errorf("accepted %s", path)
		}
	}
}
