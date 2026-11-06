package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type vodPattern struct{ off int64 }

const vodTestSize = 40 << 20

func (p *vodPattern) Read(b []byte) (int, error) {
	n := int(min(int64(len(b)), vodTestSize-p.off))
	if n <= 0 {
		return 0, io.EOF
	}
	for i := range b[:n] {
		b[i] = byte((p.off + int64(i)) % 251)
	}
	p.off += int64(n)
	return n, nil
}
func (p *vodPattern) Seek(n int64, w int) (int64, error) {
	switch w {
	case io.SeekStart:
		p.off = n
	case io.SeekCurrent:
		p.off += n
	case io.SeekEnd:
		p.off = vodTestSize + n
	}
	return p.off, nil
}

type vodInner struct{}

func (vodInner) Name() string { return "other" }
func (vodInner) Search(context.Context, core.SearchRequest) ([]core.Release, error) {
	n := "Film.2020.720p.WEB-DL.H264"
	return []core.Release{{ID: "other", Name: n, Info: releaseparse.Parse(n), Protocol: core.ProtocolHTTP, StreamURL: "http://unused.invalid/file.mkv"}}, nil
}
func (vodInner) FetchNZB(context.Context, core.Release) ([]byte, error) {
	return nil, errors.New("unused")
}

func TestXtreamVODIndexResolutionAndRange(t *testing.T) {
	var revision atomic.Int64
	var infos atomic.Int64
	var fail atomic.Bool
	var active, peak atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if strings.HasPrefix(r.URL.Path, "/movie/") || strings.HasPrefix(r.URL.Path, "/series/") {
			w.Header().Set("Content-Type", "video/x-matroska")
			http.ServeContent(w, r, "file.mkv", time.Time{}, &vodPattern{})
			return
		}
		switch r.URL.Query().Get("action") {
		case "get_vod_categories":
			fmt.Fprint(w, `[{"category_id":"1","category_name":"Films"}]`)
		case "get_vod_streams":
			if fail.Load() {
				fmt.Fprint(w, `{"password":"secret-pass"}`)
				return
			}
			fmt.Fprintf(w, `[{"stream_id":1,"name":"Film.2020.1080p.WEB-DL.H264","tmdb_id":"123","container_extension":"mkv","added":"%d"},{"stream_id":2,"name":"A Different Film (2021)","container_extension":"mp4"}]`, revision.Load())
		case "get_series":
			fmt.Fprint(w, `[{"series_id":"3","name":"A Show (2019)","tmdb":"456"}]`)
		case "get_vod_info":
			infos.Add(1)
			fmt.Fprint(w, `{"info":{"size":"41943040","video":{"codec_name":"h264","height":1080}}}`)
		case "get_series_info":
			fmt.Fprint(w, `{"episodes":{"2":[{"id":"4","season":"2","episode_num":3,"container_extension":"mkv","info":{"size":41943040,"video":{"height":"1080","codec_name":"h264"}}}]}}`)
		default:
			fmt.Fprint(w, `{"user_info":{"auth":1,"status":"Active","max_connections":"1"}}`)
		}
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	off := false
	src, err := s.ImportSource(t.Context(), Source{ID: "vod", Name: "Provider", VOD: true, LiveTV: &off, Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "secret-user", Password: "secret-pass"}})
	if err != nil {
		t.Fatal(err)
	}
	if src.Auth.Password != "••••••••" && src.Auth.Password != redactedSecret {
		t.Fatal("secret returned")
	}
	if err = s.RefreshVOD(t.Context(), "vod"); err != nil {
		t.Fatal(err)
	}
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: 123}}
	found, err := s.VODSearch(t.Context(), req)
	if err != nil || len(found) != 1 {
		t.Fatalf("TMDB match: %d %v", len(found), err)
	}
	fuzzy, err := s.VODSearch(t.Context(), core.SearchRequest{Type: core.TypeMovie, Title: "A: Different Film", Year: 2021})
	if err != nil || len(fuzzy) != 1 {
		t.Fatalf("normalized match: %d %v", len(fuzzy), err)
	}
	wrong, _ := s.VODSearch(t.Context(), core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: 999}})
	if len(wrong) != 0 {
		t.Fatal("conflicting ID accepted")
	}
	eps, err := s.VODSearch(t.Context(), core.SearchRequest{Type: core.TypeEpisode, Title: "A Show", Year: 2019, IDs: core.IDs{TMDB: 456}, Season: 2, Episode: 3})
	if err != nil || len(eps) != 1 || !strings.Contains(eps[0].StreamURL, "/series/secret-user/secret-pass/4.mkv") {
		t.Fatalf("episode match: %d %v", len(eps), err)
	}
	before := infos.Load()
	if err = s.RefreshVOD(t.Context(), "vod"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.VODSearch(t.Context(), req)
	if infos.Load() != before {
		t.Fatal("unchanged details refetched")
	}
	revision.Add(1)
	if err = s.RefreshVOD(t.Context(), "vod"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.VODSearch(t.Context(), req)
	if infos.Load() != before+1 {
		t.Fatal("changed details not refreshed")
	}
	fail.Store(true)
	if err = s.RefreshVOD(t.Context(), "vod"); err == nil || strings.Contains(err.Error(), "secret-pass") {
		t.Fatalf("unsafe refresh error: %v", err)
	}
	found, err = s.VODSearch(t.Context(), req)
	if err != nil || len(found) != 1 {
		t.Fatal("failed refresh lost index")
	}
	// The normal resolver chooses the provider's 1080p file above a 720p source.
	cfg := config.Defaults(t.TempDir())
	svc := debrid.NewService(cfg.Debrid, debrid.Options{AcquireStreamProvider: s.AcquireProvider})
	ix := debrid.NewIndexer(&VODIndexer{Inner: vodInner{}, Service: func() *Service { return s }}, svc)
	res := resolver.New(ix, nil, nil, cfg, resolver.Options{})
	defer res.Close()
	result, err := res.Resolve(t.Context(), core.ResolveRequest{Title: &core.Title{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: 123}}, Client: core.ClientCaps{MaxHeight: 1080}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.ID != "xtream:vod:movie:1" {
		t.Fatalf("ranking chose %s", result.Release.ID)
	}
	release, err := s.AcquireProvider(t.Context(), "vod")
	if err != nil {
		t.Fatal(err)
	}
	blocked := debrid.NewService(cfg.Debrid, debrid.Options{AcquireStreamProvider: s.AcquireProvider})
	busyResolver := resolver.New(debrid.NewIndexer(&VODIndexer{Inner: vodInner{}, Service: func() *Service { return s }}, blocked), nil, nil, cfg, resolver.Options{})
	_, busyErr := busyResolver.Resolve(t.Context(), core.ResolveRequest{Title: &core.Title{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: 123}}, Client: core.ClientCaps{MaxHeight: 1080}})
	busyResolver.Close()
	release()
	if !errors.Is(busyErr, core.ErrStreamLimit) {
		t.Fatalf("busy resolution lost classification: %v", busyErr)
	}
	eng := debrid.NewEngine(svc, debrid.EngineOptions{})
	defer eng.Close()
	media, err := eng.Open(t.Context(), result.Release.ID, result.NZB)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []int64{0, 10 << 20, 1024} {
		b := make([]byte, 32)
		n, e := media.ReadAt(b, off)
		if e != nil || n != len(b) {
			t.Fatalf("range read: %d %v", n, e)
		}
		for i, v := range b {
			if v != byte((off+int64(i))%251) {
				t.Fatal("seek bytes mismatch")
			}
		}
	}
	if peak.Load() > 1 {
		t.Fatalf("oversubscribed provider: %d", peak.Load())
	}
}

func TestXtreamVODSharesLiveBudget(t *testing.T) {
	var r tapRegistry
	release, err := r.reserve("p", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.acquire("live", "p", 1, &inputGrant{}); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("live overlaps VOD: %v", err)
	}
	release()
	release()
	tap, _, err := r.acquire("live", "p", 1, &inputGrant{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.reserve("p", 1, false); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("VOD overlaps live: %v", err)
	}
	if _, err = r.reserve("p", 5, true); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("refresh competes with live: %v", err)
	}
	r.forget(tap)
	release, err = r.reserve("p", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
