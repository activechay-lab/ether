package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

// The server role keeps protocol routes whose response is inseparable from
// admission or viewer/device authentication. A media split cannot rewrite these
// into delivery routes or lose a track-selection method/query.
func TestServerRolePreservesCoupledProtocolRoutes(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	if err := cfg.Update(func(c *config.Config) error { c.Server.NodeRoles = config.NodeRoles{config.RoleServer}; return nil }); err != nil {
		t.Fatal(err)
	}
	var gotMethod, gotURI string
	a := &App{cfg: cfg, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURI = r.Method, r.URL.RequestURI()
		w.WriteHeader(http.StatusNoContent)
	})}
	h := a.publicRoleHandler()
	for _, q := range []struct{ method, uri string }{
		{"GET", "/video/:/transcode/universal/decision?session=one"},
		{"GET", "/video/:/transcode/universal/start.m3u8?session=one"},
		{"GET", "/video/:/transcode/universal/start.mpd?session=one"},
		{"GET", "/video/:/transcode/universal/start.mkv?session=one"},
		{"GET", "/video/:/transcode/universal/start?session=one"},
		{"GET", "/video/:/transcode/universal/subtitles?session=one"},
		{"GET", "/video/:/transcode/universal/session/one/base/subtitle.ass"},
		{"GET", "/subtitles/:/transcode/universal/start?session=one"},
		{"GET", "/video/:/transcode/universal/ping?session=one"},
		{"GET", "/video/:/transcode/universal/stop?session=one"},
		{"GET", "/library/parts/one/file.mkv"},
		{"HEAD", "/library/parts/one/file.mkv"},
		{"PUT", "/library/parts/one?audioStreamID=2&subtitleStreamID=3"},
		{"GET", "/library/streams/one"},
		{"GET", "/downloadQueue/one"},
		{"GET", "/silo/api/v2/playback/transcode/one/master.m3u8"},
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(q.method, q.uri, nil))
		if response.Code != http.StatusNoContent || gotMethod != q.method || gotURI != q.uri {
			t.Fatalf("coupled route changed: %s %s => %d %s %s", q.method, q.uri, response.Code, gotMethod, gotURI)
		}
	}
	for _, path := range []string{"/sessions/media/one/grant/file", "/jellyfin/sessions/media/one/grant/file", "/emby/sessions/media/one/grant/index.m3u8", "/video/:/transcode/universal/session/one/base/index.m3u8"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("pure representation mounted on server: %s => %d", path, response.Code)
		}
	}
}
