package diagnostics

import "testing"

// #365: Plex for Mac's Download click is served by the download server
// built into the app, which calls Ether as the same viewer under its own
// client identifier. Its rejected requests were dropped by the confirmed-
// device filter, so the support ZIP showed only the app's 200s and not the
// 400 the user hit. A rejection authenticated as the capture's viewer is
// retained from any of the viewer's clients, unattributed; successes and
// other viewers' traffic are not.
func TestCompanionClientRejectionIsViewerScopedAndUnattributed(t *testing.T) {
	r, clk := newTestRecorder(t)
	r.Observe(Event{Time: clk.t, Kind: KindDevice, DeviceKey: "mac-app", ViewerID: 7})
	c, err := r.Start("owner", StartIn{DeviceID: r.Devices()[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Kind: KindRequest, Method: "POST", Route: "/media/providers", Status: 400, ViewerID: 7, DeviceKey: "embedded-server", Product: "Plex Media Server"},
		{Kind: KindRequest, Method: "GET", Route: "/media/subscriptions/template", Status: 400, ViewerID: 7, DeviceKey: "embedded-server", Product: "Plex Media Server"},
		{Kind: KindRequest, Method: "GET", Route: "/library/metadata/9", Status: 200, ViewerID: 7, DeviceKey: "embedded-server", Product: "Plex Media Server"},
		{Kind: KindRequest, Method: "GET", Route: "/library/metadata/9", Status: 404, ViewerID: 8, DeviceKey: "embedded-server"},
		{Kind: KindRequest, Method: "GET", Route: "/library/metadata/9", Status: 401, DeviceKey: "embedded-server"},
		// A playback completion from another client stays out: it correlates
		// by attempt with a start this capture does not retain.
		{Kind: KindRequest, Method: "GET", Route: "/library/parts/9/0/file.mkv", Status: 404, ViewerID: 7, DeviceKey: "embedded-server", Product: "Plex Media Server", AttemptKey: "attempt-1"},
		{Kind: KindRequest, Method: "GET", Route: "/video/:/transcode/universal/decision", Status: 400, ViewerID: 7, DeviceKey: "embedded-server", Product: "Plex Media Server"},
		{Kind: KindRequest, Method: "GET", Route: "/downloadQueue", Status: 400, ViewerID: 7, DeviceKey: "mac-app", Product: "Plex for Mac"},
	} {
		e.Time = clk.t
		r.Observe(e)
	}
	capture := r.captures[c.ID]
	events := capture.evidenceCopy(capture.started, capture.deadline)
	if len(events) != 3 {
		t.Fatalf("retained %+v", events)
	}
	var companion, own int
	for _, e := range events {
		switch {
		case e.DeviceKey == "mac-app" && !e.Uncorrelated && e.Route == "/downloadQueue":
			own++
		case e.DeviceKey == "" && e.Uncorrelated && e.ViewerID == 7 && e.Product == "Plex Media Server" && e.Status == 400:
			companion++
		default:
			t.Fatalf("unexpected retained event %+v", e)
		}
	}
	if companion != 2 || own != 1 {
		t.Fatalf("companion=%d own=%d in %+v", companion, own, events)
	}
}
