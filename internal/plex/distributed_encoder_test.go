package plex

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type recordedPlexEncoder struct {
	sessions                         map[string]core.HLSSession
	starts, stops, lookups, forwards int
	viewer                           int64
	playID, publicID                 string
	request                          core.HLSRequest
	startErr                         error
}

func (e *recordedPlexEncoder) StartPlexHLS(_ context.Context, u *core.User, playID, id string, q core.HLSRequest) (core.HLSSession, error) {
	e.starts++
	if u == nil || playID == "" || id == "" || q.SessionID != id {
		return nil, core.ErrUnauthorized
	}
	e.viewer, e.playID, e.publicID, e.request = u.ID, playID, id, q
	if e.startErr != nil {
		return nil, e.startErr
	}
	hs := &remotePlexFixtureSession{fakeDASH: fakeDASH{fakeHLS{id: id, req: q}}}
	e.sessions[id] = hs
	return hs, nil
}
func (e *recordedPlexEncoder) PlexHLSSession(id string) (core.HLSSession, bool) {
	e.lookups++
	hs, ok := e.sessions[id]
	return hs, ok
}
func (e *recordedPlexEncoder) StopPlexHLS(id string) bool {
	if _, ok := e.sessions[id]; !ok {
		return false
	}
	e.stops++
	delete(e.sessions, id)
	return true
}
func (e *recordedPlexEncoder) ForwardPlexMedia(http.ResponseWriter, *http.Request, string) bool {
	e.forwards++
	return false
}

type remotePlexFixtureSession struct{ fakeDASH }

func (s *remotePlexFixtureSession) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 0-2/3")
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte("abc"))
		}
		return
	}
	s.fakeHLS.ServeSegment(w, r, n)
}

func TestPlexEncoderUniversalRoutesPreserveAuthorization(t *testing.T) {
	remote := &recordedPlexEncoder{sessions: map[string]core.HLSSession{}}
	f := newFixture(func(d *Deps) { d.PlexEncoder = remote })
	defer f.srv.Close()
	f.trans.decision = core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts"}
	startPath := "/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000000550&session=remote-plex"
	master := f.get(t, startPath)
	if master.Code != 200 || remote.starts != 1 || remote.viewer != ownerUser.ID || remote.playID == "" || remote.publicID != "remote-plex" || len(f.trans.sessions) != 0 {
		t.Fatalf("universal start bypassed broker: %d %s", master.Code, master.Body.String())
	}
	if master.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || !strings.Contains(master.Body.String(), "session/remote-plex/base/index.m3u8?") || strings.Contains(master.Body.String(), "/cluster/") {
		t.Fatal("public master contract changed")
	}
	entry, _ := f.srv.transcodes.get("remote-plex")
	path := "/video/:/transcode/universal/session/remote-plex/base/00042.ts"
	for _, tc := range []struct {
		url, token string
		status     int
		served     bool
	}{
		{path, "", 401, false}, {streamGrantURL(path, "wrong"), "", 401, false}, {path, friendToken, 403, false}, {streamGrantURL(path, entry.streamToken), "", 200, true}, {path, ownerToken, 200, true},
	} {
		before := remote.forwards
		out := f.request(t, "GET", tc.url, map[string]string{"X-Plex-Token": tc.token})
		if out.Code != tc.status || (remote.forwards > before) != tc.served {
			t.Fatalf("public credential reached private seam incorrectly: %d want %d", out.Code, tc.status)
		}
	}
	ranged := f.request(t, "GET", path, map[string]string{"X-Plex-Token": ownerToken, "Range": "bytes=0-2"})
	if ranged.Code != 206 || ranged.Body.String() != "abc" || ranged.Header().Get("Content-Range") != "bytes 0-2/3" {
		t.Fatal("range representation changed")
	}
	head := f.request(t, "HEAD", path, map[string]string{"X-Plex-Token": ownerToken, "Range": "bytes=0-2"})
	if head.Code != 206 || head.Body.Len() != 0 || head.Header().Get("Content-Range") != "bytes 0-2/3" {
		t.Fatal("HEAD representation contract changed")
	}
	before := remote.starts
	_ = f.get(t, startPath, "X-Plex-Token", friendToken)
	if remote.starts != before {
		t.Fatal("foreign viewer reached start seam")
	}
	_ = f.get(t, "/video/:/transcode/universal/stop?session=remote-plex", "X-Plex-Token", friendToken)
	if remote.stops != 0 {
		t.Fatal("foreign viewer reached stop seam")
	}
	_ = f.get(t, "/video/:/transcode/universal/stop?session=remote-plex")
	if remote.stops != 1 {
		t.Fatal("owner stop bypassed remote ownership")
	}
}

func TestPlexEncoderFailureAndMountCleanupRetainOwnership(t *testing.T) {
	remote := &recordedPlexEncoder{sessions: map[string]core.HLSSession{}, startErr: errors.New("uncertain owned remote start")}
	f := newFixture(func(d *Deps) { d.PlexEncoder = remote })
	defer f.srv.Close()
	_ = f.get(t, "/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000000550&session=pending-remote")
	if remote.starts != 1 || len(f.trans.sessions) != 0 {
		t.Fatal("remote error fell back to local encoder")
	}
	remote.sessions["pending-remote"] = nil
	// Ownership, even a nil pending session, prevents local lookup/stop fallback.
	if hs, owned := f.srv.plexHLSSession("pending-remote"); !owned || hs != nil {
		t.Fatal("pending execution lost ownership")
	}
	f.srv.releaseMountedPlayback()
	if remote.stops != 1 || f.srv.currentPlexEncoder() != remote {
		t.Fatal("clearing mounted broker lost remote cleanup dependency")
	}
}

func TestPlexEncoderDASHAndHTTPStartsUseSameSeam(t *testing.T) {
	for _, tc := range []struct{ format, protocol, content string }{{"start.mpd", "dash", "application/dash+xml"}, {"start", "http", "video/x-matroska"}} {
		t.Run(tc.protocol, func(t *testing.T) {
			remote := &recordedPlexEncoder{sessions: map[string]core.HLSSession{}}
			f := newFixture(func(d *Deps) { d.PlexEncoder = remote })
			defer f.srv.Close()
			f.trans.decision = core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "fmp4"}
			out := f.get(t, "/video/:/transcode/universal/"+tc.format+"?path=%2Flibrary%2Fmetadata%2F1000000000000550&session=remote-"+tc.protocol+"&protocol="+tc.protocol)
			if out.Code != 200 || remote.starts != 1 || remote.request.Protocol != tc.protocol || out.Header().Get("Content-Type") != tc.content || len(f.trans.sessions) != 0 {
				t.Fatalf("%s start contract changed: %d %s", tc.protocol, out.Code, out.Body.String())
			}
		})
	}
}
