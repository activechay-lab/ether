package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/plex"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// liveEther is a real Live TV service, transcode manager and Plex server in
// this process, listening on an ephemeral loopback port. Nothing persists:
// the database is in memory and the work directory is removed on Close.

const liveOwnerToken = "transcodechaos-live-owner"

var liveOwner = core.User{ID: 1, Username: "owner", Title: "Owner", IsOwner: true}

type liveAuth struct{}

func (liveAuth) Authenticate(_ context.Context, token string) (*core.User, error) {
	if token != liveOwnerToken {
		return nil, core.ErrUnauthorized
	}
	u := liveOwner
	return &u, nil
}
func (liveAuth) Users(context.Context) ([]core.User, error)  { return []core.User{liveOwner}, nil }
func (liveAuth) UpdateUser(context.Context, core.User) error { return nil }
func (liveAuth) Owner(context.Context) (*core.User, error) {
	u := liveOwner
	return &u, nil
}

type liveEther struct {
	manager *transcode.Manager
	db      *sql.DB
	service *livetv.Service
	plex    *plex.Server
	server  *httptest.Server
	work    string
	logFile *os.File
	// Stability is whether the preset reached Ether, and how.
	Stability string
	handled   sync.Map
}

// channelSetup is how a scenario configures its one channel.
type channelSetup struct {
	Mode      string // passthrough | relay-copy | relay-transcode | steady
	Primary   string
	Backups   []string
	Stability string // fastest | balanced | rocksolid ("" = balanced)
}

func startLiveEther(ctx context.Context, ff, fp, work string, setup channelSetup) (*liveEther, livetv.Channel, error) {
	e := &liveEther{work: work}
	var err error
	if e.logFile, err = os.Create(filepath.Join(work, "ether.log")); err != nil {
		return nil, livetv.Channel{}, err
	}
	log := slog.New(slog.NewTextHandler(e.logFile, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fail := func(err error) (*liveEther, livetv.Channel, error) { e.Close(); return nil, livetv.Channel{}, err }
	e.manager, err = transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, HWAccel: "none", TempDir: filepath.Join(work, "transcode")}, log)
	if err != nil {
		return fail(err)
	}
	if e.db, err = store.OpenMemory(ctx); err != nil {
		return fail(err)
	}
	opts := livetv.Options{Transcoder: e.manager, DB: e.db, HTTPClient: &http.Client{}, Logger: log.With("component", "livetv"), HealthSweepMinutes: func() int { return -1 }}
	e.Stability = applyStability(&opts, nil, setup.Stability)
	e.service, err = livetv.New(opts)
	if err != nil {
		return fail(err)
	}
	m3u := fmt.Sprintf("#EXTM3U\n#EXTINF:-1 tvg-id=\"chaos\" tvg-chno=\"7\",Chaos\n%s\n", setup.Primary)
	if _, err = e.service.ImportSource(ctx, livetv.Source{Name: "Chaos provider", Playlist: m3u}); err != nil {
		return fail(fmt.Errorf("import: %w", err))
	}
	channels, err := e.service.Channels(ctx)
	if err != nil || len(channels) != 1 {
		return fail(fmt.Errorf("fixture channel missing: %v", err))
	}
	ch := channels[0]
	switch setup.Mode {
	case "passthrough", "relay-copy":
	case "relay-transcode":
		ch.Transcode = true
		ch.Screens = &livetv.ChannelScreens{NoSignal: "error"}
	case "steady":
		ch.Transcode = true
		ch.Screens = &livetv.ChannelScreens{NoSignal: "card"}
	default:
		return fail(fmt.Errorf("unknown mode %q", setup.Mode))
	}
	if len(setup.Backups) > 0 {
		sources := append([]livetv.StreamSource(nil), ch.StreamSources...)
		if len(sources) == 0 {
			sources = []livetv.StreamSource{{ID: "primary", Name: "Primary", URL: setup.Primary, Enabled: true, Priority: 0}}
		}
		for i, u := range setup.Backups {
			sources = append(sources, livetv.StreamSource{ID: fmt.Sprintf("backup-%d", i+1), Name: fmt.Sprintf("Backup %d", i+1), URL: u, Enabled: true, Priority: i + 1})
		}
		ch.StreamSources = sources
	}
	if e.Stability == "" {
		e.Stability = applyStability(nil, &ch, setup.Stability)
	}
	if ch, err = e.service.SaveChannel(ctx, ch); err != nil {
		return fail(fmt.Errorf("save channel: %w", err))
	}
	cfg := config.Config{}
	cfg.Server.Port = 32400
	cfg.Server.FriendlyName = "Ether chaos"
	cfg.Server.MachineIdentifier = "c4a05c4a05c4a05c4a05c4a05c4a05c4a05c4a05"
	cfg.Plex.Claimed = true
	cfg.Plex.OwnerUsername = "owner"
	e.plex = plex.NewServer(plex.Deps{
		LiveTV: e.service, Auth: liveAuth{}, Transcoder: e.manager, ApplyClientRules: resolver.ApplyClientRulesForMedia,
		Config: func() config.Config { return cfg }, Logger: log.With("component", "plex"),
		Identity: plex.Identity{MachineIdentifier: cfg.Server.MachineIdentifier, FriendlyName: cfg.Server.FriendlyName, Version: "1.43.4.10903-e5521bd8c", Platform: "MacOSX", PlatformVersion: "26", MyPlexSubscription: true},
	})
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Chaos-Judge")
		began := time.Now()
		e.plex.ServeHTTP(w, r)
		if id != "" {
			e.handled.Store(id, time.Since(began).Seconds())
		}
	}))
	return e, ch, nil
}

// handledSeconds is how long Ether spent serving a judge request, so the
// judge can tell its own blocked time from a slow response.
func (e *liveEther) handledSeconds(id string) (float64, bool) {
	v, ok := e.handled.LoadAndDelete(id)
	if !ok {
		return 0, false
	}
	return v.(float64), true
}

// applyStability sets the stability preset through whichever hook the
// checked-out Ether has (worker D's config.LiveTV.Stability ->
// livetv.Options.Stability and livetv.Channel.Stability). It uses reflection
// so the harness also builds against the v0.3.5 baseline, which has none.
// The result describes what was applied ("" when nothing was).
func applyStability(opts *livetv.Options, ch *livetv.Channel, preset string) string {
	if preset == "" {
		return ""
	}
	if opts != nil {
		f := reflect.ValueOf(opts).Elem().FieldByName("Stability")
		if f.IsValid() && f.Kind() == reflect.Func && f.Type().NumIn() == 0 && f.Type().NumOut() == 1 && f.Type().Out(0).Kind() == reflect.String {
			value := preset
			f.Set(reflect.MakeFunc(f.Type(), func([]reflect.Value) []reflect.Value {
				return []reflect.Value{reflect.ValueOf(value).Convert(f.Type().Out(0))}
			}))
			return "server:" + preset
		}
	}
	if ch != nil {
		f := reflect.ValueOf(ch).Elem().FieldByName("Stability")
		if f.IsValid() && f.Kind() == reflect.String {
			f.SetString(preset)
			return "channel:" + preset
		}
	}
	return ""
}

func (e *liveEther) Close() {
	if e.server != nil {
		e.server.CloseClientConnections()
		e.server.Close()
	}
	if e.plex != nil {
		e.plex.Close()
	}
	if e.service != nil {
		_ = e.service.Close()
	}
	if e.manager != nil {
		_ = e.manager.Close()
	}
	if e.db != nil {
		_ = e.db.Close()
	}
	if e.logFile != nil {
		_ = e.logFile.Close()
	}
}

// Client identities whose headers the official apps send. Each must match
// the registry quirk plex-native-live-tune so the native tune path runs.
var liveClients = map[string]map[string]string{
	"plex-appletv": {"X-Plex-Product": "Plex for Apple TV", "X-Plex-Platform": "tvOS", "X-Plex-Platform-Version": "26.0", "X-Plex-Device": "Apple TV", "X-Plex-Model": "AppleTV14,1", "X-Plex-Version": "8.45", "X-Plex-Client-Identifier": "chaos-appletv"},
}

// tunePlex performs the native live tune and returns the client playlist URL
// the app plays, plus the session id.
func (e *liveEther) tunePlex(ctx context.Context, client string, channelID string) (string, string, error) {
	hdr := liveClients[client]
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.server.URL+"/livetv/dvrs/1/channels/"+url.PathEscape(channelID)+"/tune", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", liveOwnerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("tune HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc struct {
		MediaContainer struct {
			MediaSubscription []struct {
				MediaGrabOperation []struct {
					ID       string `json:"id"`
					Metadata struct {
						Media []struct {
							Part []struct {
								Key string `json:"key"`
							} `json:"Part"`
						} `json:"Media"`
					} `json:"Metadata"`
				} `json:"MediaGrabOperation"`
			} `json:"MediaSubscription"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", "", fmt.Errorf("tune response: %w", err)
	}
	subs := doc.MediaContainer.MediaSubscription
	if len(subs) == 0 || len(subs[0].MediaGrabOperation) == 0 || len(subs[0].MediaGrabOperation[0].Metadata.Media) == 0 || len(subs[0].MediaGrabOperation[0].Metadata.Media[0].Part) == 0 {
		return "", "", fmt.Errorf("native tune response has no media part: %.300s", body)
	}
	grab := subs[0].MediaGrabOperation[0]
	key := grab.Metadata.Media[0].Part[0].Key
	q := url.Values{"X-Plex-Token": {liveOwnerToken}}
	for k, v := range hdr {
		q.Set(k, v)
	}
	return e.server.URL + key + "?" + q.Encode(), grab.ID, nil
}

// activeSessionPath is the live playback path Ether chose, from its log.
func (e *liveEther) selectedPath() string {
	b, err := os.ReadFile(filepath.Join(e.work, "ether.log"))
	if err != nil {
		return ""
	}
	path := ""
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, " path="); i >= 0 && strings.Contains(line, "playback path selected") {
			path = strings.Fields(line[i+6:])[0]
		}
	}
	return path
}
