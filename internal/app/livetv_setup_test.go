package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestLiveTVSetupSourceIdentityAdmission(t *testing.T) {
	a := pinTestApp(t, 42)
	tunes := 0
	h := admin.NewHandler(admin.Deps{Config: a.cfg, Extensions: map[string]http.Handler{"livetv": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.liveTVInputUser(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		tunes++
		if !u.IsOwner || u.ID != core.SetupOwnerID {
			t.Errorf("setup source identity: %+v", u)
		}
		w.WriteHeader(200)
	})}})
	for _, tc := range []struct {
		remote, host string
		code         int
	}{
		{"192.168.68.10:45000", "192.168.68.65:32552", 200},
		{"8.8.8.8:45000", "192.168.68.65:32552", 403},
		{"192.168.68.10:45000", "server.plex.direct", 404},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/ether/api/livetv/channels/example/play", strings.NewReader("{}"))
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Ether-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.remote, tc.host, rec.Code, rec.Body.String())
		}
	}
	if tunes != 1 {
		t.Fatalf("unauthorized request reached tune: %d", tunes)
	}
	if _, err := a.liveTVInputUser(context.Background()); err == nil {
		t.Fatal("unadmitted background context got stand-in")
	}
	if err := a.users.SetOwner(context.Background(), plextv.Account{ID: 42, Username: "owner"}, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	u, err := a.liveTVInputUser(context.Background())
	if err != nil || u.ID == 0 || u.ID == core.SetupOwnerID || !u.IsOwner {
		t.Fatalf("claimed source identity %+v %v", u, err)
	}
}

func TestOpenSetupLANTunesVirtualChannel(t *testing.T) {
	a := pinTestApp(t, 42)
	pb := &setupSourcePlayback{prepared: make(chan int64, 4)}
	service, err := livetv.New(livetv.Options{DB: store.OpenTest(t), InputUser: a.liveTVInputUser, Playback: pb, Transcoder: &liveTestTranscoder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	channel, err := service.SaveChannel(context.Background(), livetv.Channel{Name: "Setup fixture", Number: "1", Kind: "virtual", Enabled: true, Epoch: time.Now(), Items: []livetv.Item{{Key: core.MovieKey(1), Title: "Fixture", DurationMS: 600000}}})
	if err != nil {
		t.Fatal(err)
	}
	h := admin.NewHandler(admin.Deps{Config: a.cfg, Extensions: map[string]http.Handler{"livetv": service.Handler()}})
	request := func(remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://192.168.68.65:32552/ether/api/livetv/channels/"+channel.ID+"/play", strings.NewReader("{}"))
		r.RemoteAddr = remote
		r.Header.Set("X-Ether-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if r := request("8.8.8.8:45000"); r.Code != 403 || !strings.Contains(r.Body.String(), "setup_local_only") {
		t.Fatalf("remote: %d %s", r.Code, r.Body.String())
	}
	select {
	case <-pb.prepared:
		t.Fatal("remote request opened a source")
	default:
	}
	rec := request("192.168.68.10:45000")
	if rec.Code != 200 {
		t.Fatalf("LAN tune: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case id := <-pb.prepared:
		if id != core.SetupOwnerID {
			t.Fatalf("source principal %d", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("virtual source did not open")
	}
}

type setupSourcePlayback struct {
	liveTestPlayback
	prepared chan int64
}

func (p *setupSourcePlayback) PrepareChannel(ctx context.Context, u *core.User, k core.RatingKey, c core.ClientCaps, g string) (*core.PlaySession, error) {
	select {
	case p.prepared <- u.ID:
	default:
	}
	return &core.PlaySession{ID: "setup-fixture", UserID: u.ID, Key: k, Info: core.MediaInfo{Container: "mp4", Duration: time.Hour, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720}, {Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000}}}}, nil
}
