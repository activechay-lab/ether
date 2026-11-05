package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// provider serves a playlist and guide only to requests that pass check.
func provider(t *testing.T, check func(r *http.Request) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !check(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/player_api.php" {
			writeXtreamDocuments(w, r)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u"), r.URL.Path == "/get.php":
			w.Write([]byte(m3uFixture("news.us=News Network")))
		default:
			w.Write([]byte(xmltvFixture(6, "news.us=News Network")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// JSON live API fixtures keep document timeout/progress coverage on the same
// account/category/channel path used by release Xtream imports.
func writeXtreamDocuments(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("action") {
	case "get_live_categories":
		w.Write([]byte(`[{"category_id":"1","category_name":"News"}]`))
	case "get_live_streams":
		w.Write([]byte(`[{"num":1,"stream_id":1,"name":"News Network","epg_channel_id":"news.us","category_id":"1"}]`))
	default:
		w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","allowed_output_formats":["ts"]}}`))
	}
}

func TestBasicAuthSourceKeepsItsSecretRedacted(t *testing.T) {
	ctx := context.Background()
	up := provider(t, func(r *http.Request) bool {
		u, p, ok := r.BasicAuth()
		return ok && u == "owner" && p == "s3cret" && r.UserAgent() == "VLC/3.0"
	})
	s := testService(t, Options{HTTPClient: up.Client()})
	src := Source{ID: "p", Name: "Provider", PlaylistURL: up.URL + "/list.m3u", GuideURL: up.URL + "/guide.xml",
		Auth: &SourceAuth{Type: "basic", Username: "owner", Password: "s3cret", UserAgent: "VLC/3.0"}}
	got, err := s.ImportSource(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.Password != redactedSecret || got.ChannelCount != 1 {
		t.Fatalf("import response: %+v", got)
	}
	listed := sourceByID(t, s, "p")
	if listed.Auth == nil || listed.Auth.Password != redactedSecret || listed.Auth.Username != "owner" {
		t.Fatalf("listed auth: %+v", listed.Auth)
	}
	// The owner API sends the placeholder back: the stored secret is kept.
	var resaved Source
	body := `{"id":"p","name":"Provider","playlistUrl":"` + up.URL + `/list.m3u","auth":{"type":"basic","username":"owner","password":"` + redactedSecret + `","userAgent":"VLC/3.0"}}`
	if code := ownerJSON(t, s, "PUT", "/sources/p", body, &resaved); code != 200 {
		t.Fatalf("re-save with placeholder: %d", code)
	}
	if _, err := s.RefreshSource(ctx, "p"); err != nil {
		t.Fatalf("refresh with stored secret: %v", err)
	}
	src.Auth.Password = "wrong"
	if _, err := s.ImportSource(ctx, src); err == nil || !strings.Contains(err.Error(), "refused the credentials") {
		t.Fatalf("wrong password: %v", err)
	}
}

// xtreamPanel is a fake Xtream Codes panel. streams is get_live_streams.
func xtreamPanel(t *testing.T, user, pass string, paths *[]string, streams *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path+"?"+r.URL.Query().Get("action"))
		q := r.URL.Query()
		if q.Get("username") != user || q.Get("password") != pass || r.UserAgent() == "" || strings.HasPrefix(r.UserAgent(), "Go-http-client") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch {
		case r.URL.Path == "/xmltv.php":
			w.Write([]byte(xmltvFixture(6, "news.us=News Network", "sport.uk=Sport One")))
		case r.URL.Path != "/player_api.php":
			w.WriteHeader(http.StatusNotFound)
		case q.Get("action") == "get_live_categories":
			w.Write([]byte(`[{"category_id":"7","category_name":"Events","parent_id":0},{"category_id":3,"category_name":"News","parent_id":0},{"category_id":"9","category_name":"24/7 Adult"}]`))
		case q.Get("action") == "get_live_streams":
			w.Write([]byte(*streams))
		case q.Get("action") == "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1793913001","is_trial":"1","active_cons":"0","max_connections":"1","allowed_output_formats":["m3u8","ts"]},"server_info":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestXtreamSourceImportsTheLiveLineupThroughTheAPI(t *testing.T) {
	ctx := context.Background()
	var paths []string
	streams := `[{"num":2,"name":"News Network","stream_type":"live","stream_id":101,"epg_channel_id":"news.us","category_id":"3","is_adult":0},
		{"num":"1","name":"Big Event","stream_type":"live","stream_id":"55","epg_channel_id":null,"category_id":"7"},
		{"num":3,"name":"After Dark","stream_type":"live","stream_id":77,"category_id":"9","is_adult":"1"}]`
	up := xtreamPanel(t, "u", "p", &paths, &streams)
	s := testService(t, Options{HTTPClient: up.Client()})
	got, err := s.ImportSource(ctx, Source{ID: "x", Name: "Panel", HideAdult: true, NewGroups: "exclude", Auth: &SourceAuth{Type: "xtream", Server: up.URL + "/", Username: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.PlaylistURL != "" || got.GuideURL != "" || got.Auth.Password != redactedSecret || !got.HasPlaylist || !got.HasGuide {
		t.Fatalf("xtream source leaked or lost URLs: %+v", got)
	}
	if strings.Join(paths, " ") != "/player_api.php? /player_api.php?get_live_categories /player_api.php?get_live_streams /xmltv.php?" {
		t.Fatalf("panel requests (never get.php): %v", paths)
	}
	if got.Account == nil || got.Account.MaxConnections != 1 || !got.Account.Trial || got.Account.ExpiresAt == nil || got.Account.ExpiresAt.Unix() != 1793913001 {
		t.Fatalf("account: %+v", got.Account)
	}
	if len(got.Groups) != 3 || got.Groups[0].Name != "Events" || got.Groups[1].Name != "News" || !got.Groups[2].Adult || !got.Groups[2].Excluded || got.ChannelCount != 2 || got.GuideChannelCount != 1 {
		t.Fatalf("groups or counts: %+v", got)
	}
	cs, _ := s.Channels(ctx)
	if len(cs) != 2 || cs[0].Name != "Big Event" || cs[0].Number != "1" || cs[1].Number != "2" || cs[1].GuideID != "news.us" ||
		cs[1].StreamSources[0].URL != up.URL+"/live/u/p/101.ts" {
		t.Fatalf("channels: %+v", cs)
	}
	// The owner renumbers News; the provider renumbers Big Event. A group
	// that first appears on refresh stays out (NewGroups "exclude").
	news := cs[1]
	news.Number = "50"
	if _, err := s.SaveChannel(ctx, news); err != nil {
		t.Fatal(err)
	}
	streams = `[{"num":2,"name":"News Network","stream_id":101,"epg_channel_id":"news.us","category_id":"3"},
		{"num":9,"name":"Big Event","stream_id":55,"category_id":"7"},
		{"num":4,"name":"New Thing","stream_id":88,"category_id":"12"}]`
	if got, err = s.RefreshSource(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	cs, _ = s.Channels(ctx)
	if len(cs) != 2 || cs[0].Name != "Big Event" || cs[0].Number != "9" || cs[1].Number != "50" || len(got.ExcludeGroups) != 1 || got.ExcludeGroups[0] != "" {
		t.Fatalf("refresh: %+v excluded=%q", cs, got.ExcludeGroups)
	}
	if err := (&SourceAuth{Type: "xtream", Server: "ftp://x", Username: "u", Password: "p"}).validate(); err == nil {
		t.Fatal("non-HTTP panel accepted")
	}
}

func TestXtreamImportKeepsChannelsFirstImportedFromTheM3UExport(t *testing.T) {
	ctx := context.Background()
	var paths []string
	streams := `[{"num":1,"name":"News Network","stream_id":101,"category_id":"3"}]`
	up := xtreamPanel(t, "u", "p", &paths, &streams)
	s := testService(t, Options{HTTPClient: up.Client()})
	legacy := Channel{ID: "legacy-id", Name: "News", Number: "1", Kind: "iptv", SourceID: "x", Enabled: false, Favorite: true,
		StreamSources: []StreamSource{{ID: stableID("legacy-id", "playlist"), Name: "Imported stream", URL: "http://panel.invalid:80/u/p/101", Enabled: true}}}
	b, _ := json.Marshal(legacy)
	if _, err := s.o.DB.Exec(`INSERT INTO livetv_channels(id,data) VALUES(?,?)`, legacy.ID, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSource(ctx, Source{ID: "x", Name: "Panel", Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "u", Password: "p", Output: "m3u8"}}); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Channels(ctx)
	if len(cs) != 1 || cs[0].ID != "legacy-id" || cs[0].Enabled || !cs[0].Favorite || cs[0].StreamSources[0].URL != up.URL+"/live/u/p/101.m3u8" {
		t.Fatalf("carried channel: %+v", cs)
	}
}

func TestXtreamFetchFailureNamesItsCauseWithoutTheURL(t *testing.T) {
	raw := "http://panel.example:8080/get.php?username=panel-user&password=panel-pass"
	refused := sourceFetchFailure(&url.Error{Op: "Get", URL: raw, Err: errors.New("dial tcp 203.0.113.5:8080: connect: connection refused")})
	if !strings.Contains(refused.Error(), "source fetch failed") || !strings.Contains(refused.Error(), "connection refused") {
		t.Fatalf("cause lost: %v", refused)
	}
	if strings.Contains(refused.Error(), "panel-user") || strings.Contains(refused.Error(), "panel-pass") {
		t.Fatalf("error leaks credentials: %v", refused)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(80 * time.Millisecond)
	}))
	defer slow.Close()
	_, doErr := (&http.Client{Timeout: 10 * time.Millisecond}).Get(slow.URL + "/get.php?username=panel-user&password=panel-pass")
	if doErr == nil {
		t.Fatal("expected a client timeout")
	}
	timedOut := sourceFetchFailure(doErr)
	if !strings.Contains(timedOut.Error(), "did not answer") {
		t.Fatalf("timeout not named: %v", timedOut)
	}
	if strings.Contains(timedOut.Error(), "panel-user") || strings.Contains(timedOut.Error(), "panel-pass") {
		t.Fatalf("timeout error leaks credentials: %v", timedOut)
	}
	if strings.Contains(timedOut.Error(), "panel") {
		t.Fatalf("a plain source is called a panel: %v", timedOut)
	}
}

func TestSourceReadTimeoutNamesItsCause(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(80 * time.Millisecond)
	}))
	defer slow.Close()
	res, err := (&http.Client{Timeout: 10 * time.Millisecond}).Get(slow.URL + "/list.m3u")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, readErr := io.ReadAll(res.Body)
	if readErr == nil {
		t.Fatal("expected a body read timeout")
	}
	got := sourceReadFailure(readErr, "playlist")
	if !strings.Contains(got.Error(), "did not finish sending the playlist") {
		t.Fatalf("read timeout not named: %v", got)
	}
	if stalled := sourceReadFailure(errSourceStalled, "guide"); !strings.Contains(stalled.Error(), "stopped sending the guide") {
		t.Fatalf("stall not named: %v", stalled)
	}
	// A size limit already names itself and must not become a timeout.
	if limit := (&limitError{msg: "playlist exceeds the 64 MB limit"}); sourceReadFailure(limit, "playlist") != limit {
		t.Fatal("size-limit error rewritten")
	}
}

func TestXtreamPanelHasTimeToComposeItsDocuments(t *testing.T) {
	// The service client bounds stream work to 20 s; a panel that composes a
	// large playlist or guide gets the refresh budget instead.
	up := provider(t, func(*http.Request) bool {
		time.Sleep(60 * time.Millisecond)
		return true
	})
	s := testService(t, Options{HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	got, err := s.ImportSource(context.Background(), Source{ID: "x", Name: "Slow panel", Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "u", Password: "p"}})
	if err != nil {
		t.Fatalf("slow panel: %v", err)
	}
	if got.ChannelCount != 1 {
		t.Fatalf("import: %+v", got)
	}
	if s.o.HTTPClient.Timeout != 20*time.Millisecond {
		t.Fatalf("service client changed: %s", s.o.HTTPClient.Timeout)
	}
}

func TestSourceRequestsUseAnIPTVUserAgent(t *testing.T) {
	var got []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.UserAgent())
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	for _, ua := range []string{"", "Custom/1.0"} {
		req, err := http.NewRequest(http.MethodGet, up.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		res, err := s.o.HTTPClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if len(got) != 2 || got[0] != sourceUserAgent || got[1] != "Custom/1.0" {
		t.Fatalf("user agents: %v", got)
	}
}

func TestImportFailureIsLoggedWithoutCredentials(t *testing.T) {
	var logs bytes.Buffer
	s := testService(t, Options{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		HTTPClient: &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp 203.0.113.5:8080: connect: connection refused")
		})},
	})
	_, err := s.ImportSource(context.Background(), Source{ID: "x", Name: "Panel", Auth: &SourceAuth{Type: "xtream", Server: "http://panel.example:8080", Username: "panel-user", Password: "panel-pass"}})
	if err == nil {
		t.Fatal("import succeeded")
	}
	if !strings.Contains(err.Error(), "Xtream account:") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error lacks the step or cause: %v", err)
	}
	got := logs.String()
	for _, want := range []string{"Live TV source import failed", "sourceId=x", "source=Panel", "panel.example", "Xtream account:", "connection refused"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "panel-user") || strings.Contains(got, "panel-pass") {
		t.Fatalf("log leaks credentials: %s", got)
	}
}

func TestCustomHeadersAreValidated(t *testing.T) {
	ctx := context.Background()
	up := provider(t, func(r *http.Request) bool { return r.Header.Get("X-Api-Key") == "k" })
	s := testService(t, Options{HTTPClient: up.Client()})
	if _, err := s.ImportSource(ctx, Source{ID: "h", Name: "Keyed", PlaylistURL: up.URL + "/a.m3u", Auth: &SourceAuth{Type: "headers", Headers: map[string]string{"X-Api-Key": "k"}}}); err != nil {
		t.Fatal(err)
	}
	if h := sourceByID(t, s, "h").Auth.Headers["X-Api-Key"]; h != redactedSecret {
		t.Fatalf("header value not redacted: %q", h)
	}
	for _, bad := range []map[string]string{{"Host": "x"}, {"X-Plex-Token": "x"}, {"Bad Name": "x"}, {"X-A": "a\r\nb"}} {
		if err := (&SourceAuth{Type: "headers", Headers: bad}).validate(); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestUpstreamHeadersUseOnlyTheInputsProvider(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	for _, src := range []Source{
		{ID: "a", Name: "A", Playlist: m3uFixture("a=One"), Auth: &SourceAuth{Type: "bearer", Token: "token-a", UserAgent: "UA-A"}},
		{ID: "b", Name: "B", Playlist: m3uFixture("b=Two"), Auth: &SourceAuth{Type: "basic", Username: "bob", Password: "pw"}},
		{ID: "c", Name: "C", Playlist: m3uFixture("c=Three")},
	} {
		if _, err := s.ImportSource(ctx, src); err != nil {
			t.Fatal(err)
		}
	}
	imported := StreamSource{ID: stableID("x", "playlist")}
	ch := Channel{ID: "x", SourceID: "a", StreamSources: []StreamSource{imported, {ID: "added"}}}
	own, _ := s.upstreamHeaders(ctx, ch, imported)
	if own.Get("Authorization") != "Bearer token-a" || own.Get("User-Agent") != "UA-A" {
		t.Fatalf("own provider headers: %v", own)
	}
	backup, _ := s.upstreamHeaders(ctx, ch, StreamSource{ID: "2", AuthSourceID: "b"})
	if !strings.HasPrefix(backup.Get("Authorization"), "Basic ") || backup.Get("User-Agent") != "" {
		t.Fatalf("backup provider headers: %v", backup)
	}
	plain, _ := s.upstreamHeaders(ctx, ch, StreamSource{ID: "3", AuthSourceID: "c"})
	if len(plain) != 0 {
		t.Fatalf("unauthenticated provider received credentials: %v", plain)
	}
	added, _ := s.upstreamHeaders(ctx, ch, StreamSource{ID: "added"})
	if len(added) != 0 {
		t.Fatalf("owner-added backup inherited the playlist login: %v", added)
	}
	legacy, _ := s.upstreamHeaders(ctx, Channel{ID: "y", SourceID: "a"}, StreamSource{ID: "only"})
	if legacy.Get("Authorization") != "Bearer token-a" {
		t.Fatalf("legacy single input lost its login: %v", legacy)
	}
	manual, _ := s.upstreamHeaders(ctx, Channel{ID: "v"}, StreamSource{ID: "4"})
	if len(manual) != 0 {
		t.Fatalf("manual channel received credentials: %v", manual)
	}
}
