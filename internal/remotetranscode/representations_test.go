package remotetranscode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func (s *fakeSession) Rungs() []core.HLSRung {
	return []core.HLSRung{{ID: "base", Bandwidth: 4000000}, {ID: "720p", Bandwidth: 2000000}}
}
func (s *fakeSession) ServeRungPlaylist(w http.ResponseWriter, r *http.Request, id string) {
	fmt.Fprint(w, "playlist:"+id)
}
func (s *fakeSession) ServeRungSegment(w http.ResponseWriter, r *http.Request, id string, n int) {
	fmt.Fprintf(w, "rung:%s:%d", id, n)
}
func (s *fakeSession) ServeManifest(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "manifest")
}
func (s *fakeSession) ServeInit(w http.ResponseWriter, r *http.Request, rep int) {
	fmt.Fprintf(w, "init:%d", rep)
}
func (s *fakeSession) ServeMedia(w http.ResponseWriter, r *http.Request, rep, n int) {
	fmt.Fprintf(w, "media:%d:%d", rep, n)
}
func (s *fakeSession) ServeHTTP(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "matroska") }

func TestCapabilityPreflight(t *testing.T) {
	_, _, start := fixture(t)
	r := core.HLSRequest{Decision: start.Decision}
	c := Supported()
	c.ABR = true
	c.DASH = true
	c.HTTP = true
	for _, p := range []string{"", core.ProtocolHLS, core.ProtocolDASH, core.ProtocolHTTP} {
		r.Protocol = p
		if !SupportsRequest(r, c) {
			t.Fatalf("supported protocol %q rejected", p)
		}
	}
	r.Protocol = core.ProtocolDASH
	if supported(r) {
		t.Fatal("default advertised DASH")
	}
	r.InputHeaders = map[string]string{"secret": "value"}
	if SupportsRequest(r, c) {
		t.Fatal("caller credentials admitted")
	}
	r.InputHeaders = nil
	r.Decision.BurnSubtitles = true
	if SupportsRequest(r, c) {
		t.Fatal("subtitle transport not implemented")
	}
}

// Exercise the actual adapter methods through the existing real-mTLS fixture.
func checkRepresentations(t *testing.T, hs core.HLSSession, protocol string) {
	t.Helper()
	get := func(want string, serve func(http.ResponseWriter, *http.Request)) {
		t.Helper()
		w := httptest.NewRecorder()
		serve(w, httptest.NewRequest("GET", "https://frontend/media", nil))
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("representation got %d %q want %q", w.Code, w.Body.String(), want)
		}
	}
	switch protocol {
	case core.ProtocolHLS:
		l := hs.(core.HLSLadderSession)
		r := l.Rungs()
		if len(r) != 2 || r[1].ID != "720p" {
			t.Fatalf("lost ladder: %+v", r)
		}
		r[0].ID = "mutated"
		if l.Rungs()[0].ID != "base" {
			t.Fatal("mutable ladder snapshot")
		}
		get("playlist:720p", func(w http.ResponseWriter, r *http.Request) { l.ServeRungPlaylist(w, r, "720p") })
		get("rung:720p:-1", func(w http.ResponseWriter, r *http.Request) { l.ServeRungSegment(w, r, "720p", -1) })
	case core.ProtocolDASH:
		d := hs.(core.DASHSession)
		get("manifest", d.ServeManifest)
		get("init:1", func(w http.ResponseWriter, r *http.Request) { d.ServeInit(w, r, 1) })
		get("media:1:42", func(w http.ResponseWriter, r *http.Request) { d.ServeMedia(w, r, 1, 42) })
	case core.ProtocolHTTP:
		get("matroska", hs.(core.HTTPSession).ServeHTTP)
	}
}

func (s *fakeSession) Touch()                     { s.touched = true }
func (s *fakeSession) PlaybackState(state string) { s.playback = state }
func (s *fakeSession) ServeSoftSubtitle(w http.ResponseWriter, r *http.Request, index int) bool {
	if s.soft == nil || *s.soft != index {
		return false
	}
	fmt.Fprint(w, "[Script Info]\n[Events]\n")
	return true
}
