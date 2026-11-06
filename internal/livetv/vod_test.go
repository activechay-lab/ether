package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	var trailing, stall atomic.Bool
	cancelDone := make(chan struct{})
	var active, peak atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != sourceUserAgent {
			t.Errorf("provider player identity: %q", r.UserAgent())
		}
		n := active.Add(1)
		isCancelledRead := false
		defer func() {
			active.Add(-1)
			if isCancelledRead {
				close(cancelDone)
			}
		}()
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
			if trailing.Load() {
				fmt.Fprint(w, `[] {"password":"secret-pass"}`)
				return
			}
			if stall.Load() {
				isCancelledRead = true
				fmt.Fprint(w, `[`)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if fail.Load() {
				fmt.Fprint(w, `{"password":"secret-pass"}`)
				return
			}
			fmt.Fprintf(w, `[{"stream_id":1,"name":"Film.2020.1080p.WEB-DL.H264","tmdb_id":"123","container_extension":"mkv","added":"%d"},{"stream_id":2,"name":"A Different Film (2021)","container_extension":"mp4"},{"stream_id":5,"name":"Undated Film","container_extension":"mkv"}]`, revision.Load())
		case "get_series":
			fmt.Fprint(w, `[{"series_id":"3","name":"A Show (2019)","tmdb":"456"}]`)
		case "get_vod_info":
			infos.Add(1)
			if r.URL.Query().Get("vod_id") == "5" {
				fmt.Fprint(w, `{"info":{"tmdb_id":789,"releasedate":"2022-01-01","size":41943040}}`)
				return
			}
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
	// Exercise the owner PUT path with the returned redacted credentials.
	savedBody, _ := json.Marshal(src)
	var saved Source
	if code := ownerJSON(t, s, "PUT", "/sources/vod", string(savedBody), &saved); code != 200 {
		t.Fatalf("resave: %d", code)
	}
	stored, e := s.storedSourceByID(t.Context(), "vod")
	if e != nil || stored.Auth.Password != "secret-pass" || !stored.VOD || stored.LiveTV == nil || *stored.LiveTV {
		t.Fatal("redacted save changed provider identity/policy")
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
	undatedWrong, _ := s.VODSearch(t.Context(), core.SearchRequest{Type: core.TypeMovie, Title: "Undated Film", Year: 2021})
	if len(undatedWrong) != 0 {
		t.Fatal("detail year mismatch accepted")
	}
	undated, err := s.VODSearch(t.Context(), core.SearchRequest{Type: core.TypeMovie, Title: "Undated Film", Year: 2022})
	if err != nil || len(undated) != 1 || undated[0].TitleIDs.TMDB != 789 {
		t.Fatalf("detail identity: %d %v", len(undated), err)
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
	trailing.Store(true)
	if err = s.RefreshVOD(t.Context(), "vod"); err == nil {
		t.Fatal("trailing list committed")
	}
	trailing.Store(false)
	stall.Store(true)
	cancelCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	err = s.RefreshVOD(cancelCtx, "vod")
	cancel()
	<-cancelDone
	stall.Store(false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("refresh cancellation lost: %v", err)
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
	_ = media.Close()
	_ = eng.Close()
	// Account edits invalidate the prior account's rows; deletion removes
	// both index and refresh status rather than leaving an orphan source.
	stored.Auth.Password = "replacement-pass"
	if _, err = s.ImportSource(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = s.o.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM xtream_vod WHERE provider='vod'`).Scan(&count)
	if count != 0 {
		t.Fatal("old account index survived credential edit")
	}
	if err = s.RefreshVOD(t.Context(), "vod"); err == nil && fail.Load() {
		t.Fatal("failed provider refreshed")
	}
	if err = s.DeleteSource(t.Context(), "vod"); err != nil {
		t.Fatal(err)
	}
	_, err = s.AcquireProvider(t.Context(), "vod")
	if !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatal("deleted provider still admitted")
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

func TestXtreamRefreshRejectsConcurrentSourceChange(t *testing.T) {
	for _, operation := range []string{"edit", "delete"} {
		t.Run(operation, func(t *testing.T) {
			started, resume := make(chan struct{}), make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Query().Get("action") {
				case "get_vod_categories":
					fmt.Fprint(w, `[]`)
				case "get_vod_streams":
					fmt.Fprint(w, `[{"stream_id":1,"name":"Film (2020)","tmdb_id":123,"container_extension":"mkv"}]`)
				case "get_series":
					close(started)
					select {
					case <-resume:
						fmt.Fprint(w, `[]`)
					case <-r.Context().Done():
					}
				default:
					fmt.Fprint(w, `{"user_info":{"auth":1,"status":"Active","max_connections":1}}`)
				}
			}))
			defer up.Close()
			s := testService(t, Options{HTTPClient: up.Client()})
			off := false
			src, err := s.ImportSource(t.Context(), Source{ID: "race", Name: "Provider", VOD: true, LiveTV: &off, Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "u", Password: "p"}})
			if err != nil {
				t.Fatal(err)
			}
			var attemptedSource []byte
			if err = s.o.DB.QueryRowContext(t.Context(), `SELECT data FROM livetv_sources WHERE id=?`, src.ID).Scan(&attemptedSource); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- s.RefreshVOD(t.Context(), src.ID) }()
			<-started
			if operation == "delete" {
				err = s.DeleteSource(t.Context(), src.ID)
			} else {
				src.Auth.Password = "replacement"
				_, err = s.ImportSource(t.Context(), src)
			}
			close(resume)
			refreshErr := <-done
			if err != nil {
				t.Fatal(err)
			}
			if !errors.Is(refreshErr, errVODSourceChanged) {
				t.Fatalf("stale refresh published: %v", refreshErr)
			}
			for _, table := range []string{"xtream_vod", "xtream_vod_refresh"} {
				var count int
				if err = s.o.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE provider=?`, src.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("stale %s count=%d: %v", table, count, err)
				}
			}
			{
				s.recordVODFailure(t.Context(), src.ID, attemptedSource)
				var n int
				_ = s.o.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM xtream_vod_refresh WHERE provider=?`, src.ID).Scan(&n)
				if n != 0 {
					t.Fatal("stale failure modified replacement/deleted provider status")
				}
			}
		})
	}
}

type concurrentVODInner struct{ vodInner }

func (concurrentVODInner) SearchWith(ctx context.Context, r core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if arrived != nil {
				arrived([]core.Release{{ID: "inner"}})
			}
		}()
	}
	wg.Wait()
	return []core.Release{{ID: "inner"}}, nil
}
func (concurrentVODInner) ResolveTorrent(context.Context, core.Release) (core.Release, error) {
	return core.Release{InfoHash: "preserved"}, nil
}
func (concurrentVODInner) SearchRescue(context.Context, core.SearchRequest) ([]core.Release, error) {
	return []core.Release{{ID: "rescue"}}, nil
}
func TestVODWrapperSerializesArrivalsAndPreservesContracts(t *testing.T) {
	v := &VODIndexer{Inner: concurrentVODInner{}, Service: func() *Service { return nil }}
	var active, peak, calls atomic.Int32
	_, err := v.SearchWith(t.Context(), core.SearchRequest{}, func([]core.Release) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		time.Sleep(time.Millisecond)
	})
	if err != nil || peak.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("callback contract: peak=%d calls=%d err=%v", peak.Load(), calls.Load(), err)
	}
	rel, err := v.ResolveTorrent(t.Context(), core.Release{})
	if err != nil || rel.InfoHash != "preserved" {
		t.Fatal("lost torrent capability")
	}
	rescue, err := v.SearchRescue(t.Context(), core.SearchRequest{})
	if err != nil || len(rescue) != 1 || rescue[0].ID != "rescue" {
		t.Fatal("lost rescue capability")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = v.SearchWith(ctx, core.SearchRequest{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled search returned candidates")
	}
}
