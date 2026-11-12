package mediabrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// anonState is the mutable state behind a fake session broker: the live
// session set the test can close, and the paths served through it.
type anonState struct {
	live      []frontend.Session
	served    []string
	startedTo []string
}

func newAnonBroker(state *anonState, device frontend.Device) frontendtest.Sessions {
	return frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "directplay"}, DirectPlay: true}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session-1", Item: 5, Device: device}, nil
		},
		SessionsFunc: func(context.Context, frontend.SessionAccess) ([]frontend.Session, error) {
			return state.live, nil
		},
		StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, id string, _ frontend.StreamRequest) (string, error) {
			state.startedTo = append(state.startedTo, id)
			return "/sessions/media/" + id + "/scoped/index.m3u8", nil
		},
		ServeMediaFunc: func(w http.ResponseWriter, r *http.Request) {
			state.served = append(state.served, r.URL.Path)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("bytes"))
		},
	}
}

func anonClientIP(r *http.Request) (netip.Addr, bool) {
	if v := r.Header.Get("X-Test-IP"); v != "" {
		ip, err := netip.ParseAddr(v)
		return ip, err == nil
	}
	return netip.MustParseAddr("192.0.2.1"), true
}

func mountAnonTest(t *testing.T, protocol string, broker frontendtest.Sessions) frontend.Instance {
	t.Helper()
	viewer := &frontend.Viewer{ID: 1}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	var inst frontend.Instance
	var err error
	host := frontend.Host{Identity: ident, Sessions: broker, Capabilities: frontendtest.Capabilities{}, ClientIP: anonClientIP}
	if protocol == "emby" {
		inst, err = (Emby{MachineID: "test"}).Mount(context.Background(), host)
	} else {
		inst, err = (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
	}
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

// openSession opens a playback session through the authenticated PlaybackInfo,
// which is what records the anonymous window.
func openSession(t *testing.T, inst frontend.Instance, protocol string, key frontend.RatingKey, ip string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/"+protocol+"/Items/"+ids.RatingKeyGUID(key)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true}`))
	r.Header.Set("X-Emby-Token", "grant")
	r.Header.Set("X-Emby-Device-Id", "tv")
	r.Header.Set("X-Test-IP", ip)
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("PlaybackInfo = %d: %s", w.Code, w.Body.String())
	}
}

func anonymousStream(inst frontend.Instance, protocol string, key frontend.RatingKey, ip, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/"+protocol+"/Videos/"+ids.RatingKeyGUID(key)+"/stream?static=true"+query, nil)
	if ip != "" {
		r.Header.Set("X-Test-IP", ip)
	}
	inst.Handler().ServeHTTP(w, r)
	return w
}

func TestAnonymousStreamRidesFreshSameIPSession(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "jellyfin", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")

	w := anonymousStream(inst, "jellyfin", 5, "192.0.2.1", "&mediaSourceId="+ids.RatingKeyGUID(5))
	if w.Code != http.StatusPartialContent {
		t.Fatalf("credential-less stream = %d, want 206: %s", w.Code, w.Body.String())
	}
	if len(state.served) != 1 || state.served[0] != "/sessions/media/session-1/scoped/file" {
		t.Fatalf("served paths = %v, want the opened session's file", state.served)
	}
}

func TestAnonymousStreamFromAnotherIPIsRejected(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "jellyfin", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")

	if w := anonymousStream(inst, "jellyfin", 5, "198.51.100.9", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("other-IP stream = %d, want 401", w.Code)
	}
	if len(state.served) != 0 {
		t.Fatalf("served paths = %v, want none", state.served)
	}
}

func TestAnonymousStreamForAnotherItemIsRejected(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "jellyfin", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")

	if w := anonymousStream(inst, "jellyfin", 6, "192.0.2.1", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("other-item stream = %d, want 401", w.Code)
	}
	if len(state.served) != 0 {
		t.Fatalf("served paths = %v, want none", state.served)
	}
}

func TestAnonymousStreamAfterSessionClosesIsRejected(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "jellyfin", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")

	state.live = nil
	if w := anonymousStream(inst, "jellyfin", 5, "192.0.2.1", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("closed-session stream = %d, want 401", w.Code)
	}
	if len(state.served) != 0 {
		t.Fatalf("served paths = %v, want none", state.served)
	}
}

func TestAnonymousStreamDoesNotBypassBadCredential(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	viewer := &frontend.Viewer{ID: 1}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "grant" {
			return viewer, nil
		}
		return nil, frontend.ErrUnauthorized
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Sessions: newAnonBroker(state, frontend.Device{ID: "tv"}), Capabilities: frontendtest.Capabilities{}, ClientIP: anonClientIP})
	if err != nil {
		t.Fatal(err)
	}
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")

	// An invalid token must never fall back to the anonymous path.
	for _, bad := range []string{"X-Emby-Token", "api_key"} {
		url := "/jellyfin/Videos/" + ids.RatingKeyGUID(5) + "/stream?static=true"
		if bad == "api_key" {
			url += "&api_key=bogus"
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("X-Test-IP", "192.0.2.1")
		if bad == "X-Emby-Token" {
			r.Header.Set(bad, "bogus")
		}
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bad-credential stream (%s) = %d, want 401", bad, w.Code)
		}
	}
	if len(state.served) != 0 {
		t.Fatalf("served paths = %v, want none", state.served)
	}
}

func TestAnonymousHLSStreamRidesSession(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "jellyfin", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "jellyfin", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "jellyfin", 5, "192.0.2.1")
	state.startedTo = nil

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/jellyfin/Videos/"+ids.RatingKeyGUID(5)+"/master.m3u8", nil)
	r.Header.Set("X-Test-IP", "192.0.2.1")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/jellyfin/sessions/media/session-1/scoped/index.m3u8" {
		t.Fatalf("anonymous HLS = %d %q", w.Code, w.Header().Get("Location"))
	}
	if len(state.startedTo) != 1 || state.startedTo[0] != "session-1" {
		t.Fatalf("started = %v, want session-1", state.startedTo)
	}
}

func TestAnonymousStreamWorksForEmbyAudioAlias(t *testing.T) {
	state := &anonState{live: []frontend.Session{{ID: "session-1", Item: 5, Protocol: "emby", Device: frontend.Device{ID: "tv"}, MediaPath: "/sessions/media/session-1/scoped/file"}}}
	inst := mountAnonTest(t, "emby", newAnonBroker(state, frontend.Device{ID: "tv"}))
	openSession(t, inst, "emby", 5, "192.0.2.1")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/emby/Audio/"+ids.RatingKeyGUID(5)+"/stream?static=true", nil)
	r.Header.Set("X-Test-IP", "192.0.2.1")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("emby anonymous audio stream = %d, want 206: %s", w.Code, w.Body.String())
	}
	if len(state.served) != 1 || state.served[0] != "/sessions/media/session-1/scoped/file" {
		t.Fatalf("served paths = %v", state.served)
	}
}
