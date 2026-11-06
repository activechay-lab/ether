package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/xtream"
)

func init() {
	store.Register("xtream_vod", 1, `CREATE TABLE xtream_vod(provider TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, tmdb INTEGER NOT NULL, title TEXT NOT NULL, year INTEGER NOT NULL, revision TEXT NOT NULL, data TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', PRIMARY KEY(provider,kind,id));
CREATE INDEX xtream_vod_identity ON xtream_vod(kind,tmdb);
CREATE INDEX xtream_vod_title ON xtream_vod(kind,title,year);
CREATE TABLE xtream_vod_refresh(provider TEXT PRIMARY KEY, refreshed INTEGER NOT NULL DEFAULT 0, next INTEGER NOT NULL DEFAULT 0, failures INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');`)
}

type VODStatus struct {
	Movies  int    `json:"movies"`
	Series  int    `json:"series"`
	Updated int64  `json:"updated"`
	Error   string `json:"error,omitempty"`
}

// AcquireProvider shares the live tap budget with each VOD HTTP request.
// A lease lasts until the response body closes, including cancelled seeks.
func (s *Service) AcquireProvider(ctx context.Context, id string) (func(), error) {
	src, err := s.storedSourceByID(ctx, id)
	if err != nil || !src.VOD || src.Auth == nil || src.Auth.Type != "xtream" {
		return nil, core.ErrSourceUnavailable
	}
	e, err := s.sourceEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	limit := e.limit
	if limit == 0 {
		limit = 1
	}
	return s.reserveVODProvider(id, limit, core.SpeculativeIndexerWork(ctx))
}
func (s *Service) vodClient(src Source, background bool) xtream.Client {
	a := src.Auth
	return xtream.Client{Base: a.Server, Username: a.Username, Password: a.Password, Open: func(ctx context.Context, raw string) (io.ReadCloser, error) {
		limit := 1
		if src.Account != nil && src.Account.MaxConnections > 0 {
			limit = src.Account.MaxConnections
		}
		if src.MaxConnections > 0 {
			limit = min(limit, src.MaxConnections)
		}
		release, err := s.reserveVODProvider(src.ID, limit, background)
		if err != nil {
			return nil, xtream.ErrBusy
		}
		rc, err := s.openURL(ctx, raw, a.header())
		if err != nil {
			release()
			return nil, err
		}
		return &vodBody{ReadCloser: rc, release: release}, nil
	}}
}

type vodBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *vodBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }

func (s *Service) vodLoop() {
	defer s.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}
		sources, err := s.Sources(s.ctx)
		if err == nil {
			for _, v := range sources {
				if !v.VOD || v.Auth == nil || v.Auth.Type != "xtream" {
					continue
				}
				var next int64
				_ = s.o.DB.QueryRowContext(s.ctx, `SELECT next FROM xtream_vod_refresh WHERE provider=?`, v.ID).Scan(&next)
				if next > s.o.Now().Unix() {
					continue
				}
				ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
				err = s.RefreshVOD(ctx, v.ID)
				cancel()
				if err != nil && s.ctx.Err() == nil {
					var failures int
					_ = s.o.DB.QueryRowContext(s.ctx, `SELECT failures FROM xtream_vod_refresh WHERE provider=?`, v.ID).Scan(&failures)
					failures = min(failures+1, 8)
					// Store fixed text only: a provider may echo credentials in any response.
					_, _ = s.o.DB.ExecContext(s.ctx, `INSERT INTO xtream_vod_refresh(provider,next,failures,error) VALUES(?,?,?,?) ON CONFLICT(provider) DO UPDATE SET next=excluded.next,failures=excluded.failures,error=excluded.error`, v.ID, s.o.Now().Add(time.Minute*time.Duration(1<<failures)).Unix(), failures, "Provider unavailable; retry scheduled")
				}
			}
		}
		timer.Reset(time.Minute)
	}
}

// RefreshVOD streams large lists into a transaction. Unchanged revisions keep
// cached details; a failed refresh never removes the previous index.
func (s *Service) RefreshVOD(ctx context.Context, id string) error {
	if !s.vodRefreshMu.TryLock() {
		return ErrProviderBusy
	}
	defer s.vodRefreshMu.Unlock()
	src, err := s.storedSourceByID(ctx, id)
	if err != nil {
		return err
	}
	if !src.VOD || src.Auth == nil || src.Auth.Type != "xtream" {
		return nil
	}
	c := s.vodClient(src, true)
	var cats []xtream.Category
	if err = c.Get(ctx, "get_vod_categories", nil, &cats); err != nil {
		return err
	}
	// Fetch into a temporary staging table in the same transaction. It stores
	// only the current provider, and is discarded on rollback.
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS xtream_vod_seen(kind TEXT,id TEXT,PRIMARY KEY(kind,id))`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM xtream_vod_seen`); err != nil {
		return err
	}
	up, err := tx.PrepareContext(ctx, `INSERT INTO xtream_vod(provider,kind,id,tmdb,title,year,revision,data) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider,kind,id) DO UPDATE SET tmdb=excluded.tmdb,title=excluded.title,year=excluded.year,revision=excluded.revision,data=excluded.data,detail=CASE WHEN xtream_vod.revision=excluded.revision THEN xtream_vod.detail ELSE '' END WHERE xtream_vod.data<>excluded.data`)
	if err != nil {
		return err
	}
	defer up.Close()
	seen, err := tx.PrepareContext(ctx, `INSERT INTO xtream_vod_seen(kind,id) VALUES(?,?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer seen.Close()
	for _, kind := range []string{"movie", "series"} {
		action := "get_vod_streams"
		if kind == "series" {
			action = "get_series"
		}
		err = c.Items(ctx, action, func(v xtream.Item) error {
			sid := string(v.ID)
			if kind == "series" {
				sid = string(v.SeriesID)
			}
			if sid == "" {
				return nil
			}
			info := releaseparse.Parse(v.Name)
			year := int(v.Year.Int())
			if year == 0 {
				year = info.Year
			}
			title := releaseparse.NormalizeTitle(info.Title)
			if title == "" {
				title = releaseparse.NormalizeTitle(v.Name)
			}
			tmdb := v.TMDB.Int()
			if tmdb == 0 {
				tmdb = v.TMDBAlt.Int()
			}
			b, _ := json.Marshal(v)
			if _, e := up.ExecContext(ctx, id, kind, sid, tmdb, title, year, string(v.Modified)+":"+string(v.Added), string(b)); e != nil {
				return e
			}
			_, e := seen.ExecContext(ctx, kind, sid)
			return e
		})
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM xtream_vod WHERE provider=? AND NOT EXISTS(SELECT 1 FROM xtream_vod_seen s WHERE s.kind=xtream_vod.kind AND s.id=xtream_vod.id)`, id); err != nil {
		return err
	}
	hours := src.RefreshHours
	if hours == 0 {
		hours = 6
	}
	next := s.o.Now().Add(time.Duration(hours) * time.Hour).Unix()
	if hours < 0 {
		next = 1 << 62
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO xtream_vod_refresh(provider,refreshed,next) VALUES(?,?,?) ON CONFLICT(provider) DO UPDATE SET refreshed=excluded.refreshed,next=excluded.next,failures=0,error=''`, id, s.o.Now().Unix(), next); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.o.VODChanged != nil {
		s.o.VODChanged()
	}
	return nil
}

func (s *Service) fillVODStatus(ctx context.Context, src *Source) {
	if !src.VOD {
		return
	}
	v := &VODStatus{}
	_ = s.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM xtream_vod WHERE provider=? AND kind='movie'`, src.ID).Scan(&v.Movies)
	_ = s.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM xtream_vod WHERE provider=? AND kind='series'`, src.ID).Scan(&v.Series)
	_ = s.o.DB.QueryRowContext(ctx, `SELECT refreshed,error FROM xtream_vod_refresh WHERE provider=?`, src.ID).Scan(&v.Updated, &v.Error)
	src.VODStatus = v
}

type vodMatch struct {
	provider, kind, id, detail, revision string
	item                                 xtream.Item
	tmdb                                 int64
	year                                 int
}

func (s *Service) VODSearch(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	kind := "movie"
	if req.Type == core.TypeEpisode {
		kind = "series"
	}
	// Identity lookups use SQL indexes. Name fallback requires a known matching
	// year and no conflicting provider ID; punctuation/case are normalized.
	rows, err := s.o.DB.QueryContext(ctx, `SELECT provider,id,tmdb,year,data,detail,revision FROM xtream_vod WHERE kind=? AND ((tmdb>0 AND tmdb=?) OR (title=? AND (year=? OR year=0) AND ?>0 AND (tmdb=0 OR ?=0))) LIMIT 64`, kind, req.IDs.TMDB, releaseparse.NormalizeTitle(req.Title), req.Year, req.Year, req.IDs.TMDB)
	if err != nil {
		return nil, err
	}
	var matches []vodMatch
	for rows.Next() {
		m := vodMatch{kind: kind}
		var b string
		if err = rows.Scan(&m.provider, &m.id, &m.tmdb, &m.year, &b, &m.detail, &m.revision); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &m.item); err != nil {
			continue
		}
		matches = append(matches, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []core.Release
	for _, m := range matches {
		src, e := s.storedSourceByID(ctx, m.provider)
		if e != nil || !src.VOD || src.Auth == nil {
			continue
		}
		c := s.vodClient(src, core.SpeculativeIndexerWork(ctx))
		detail := m.detail
		if detail == "" {
			var doc json.RawMessage
			action, param := "get_vod_info", "vod_id"
			if kind == "series" {
				action, param = "get_series_info", "series_id"
			}
			if e = c.Get(ctx, action, url.Values{param: {m.id}}, &doc); e != nil {
				if errors.Is(e, xtream.ErrBusy) {
					return out, ErrProviderBusy
				}
				continue
			}
			detail = string(doc)
			_, _ = s.o.DB.ExecContext(ctx, `UPDATE xtream_vod SET detail=? WHERE provider=? AND kind=? AND id=? AND revision=?`, detail, m.provider, kind, m.id, m.revision)
		}
		if kind == "movie" {
			var info xtream.VODInfo
			if json.Unmarshal([]byte(detail), &info) != nil {
				continue
			}
			s.enrichVODMatch(ctx, &m, info.Info)
			ext := m.item.Extension
			if info.MovieData.Extension != "" {
				ext = info.MovieData.Extension
			}
			out = appendVODRelease(out, c, src, m, m.id, ext, m.item.Name, info.Info, req)
		} else {
			var info xtream.SeriesInfo
			if json.Unmarshal([]byte(detail), &info) != nil {
				continue
			}
			s.enrichVODMatch(ctx, &m, info.Info)
			if m.tmdb == 0 {
				m.tmdb = info.Info.TMDB.Int()
				if m.tmdb == 0 {
					m.tmdb = info.Info.TMDBAlt.Int()
				}
			}
			for season, eps := range info.Episodes {
				for _, ep := range eps {
					sn := int(ep.Season.Int())
					if sn == 0 {
						sn, _ = strconv.Atoi(season)
					}
					if sn == req.Season && int(ep.Number.Int()) == req.Episode {
						name := fmt.Sprintf("%s S%02dE%02d", m.item.Name, sn, req.Episode)
						out = appendVODRelease(out, c, src, m, string(ep.ID), ep.Extension, name, ep.Info, req)
					}
				}
			}
		}
	}
	return out, nil
}
func appendVODRelease(out []core.Release, c xtream.Client, src Source, m vodMatch, id, ext, name string, info xtream.MediaInfo, req core.SearchRequest) []core.Release {
	raw, err := c.StreamURL(m.kind, id, ext)
	if err != nil {
		return out
	}
	parsed := releaseparse.Parse(name)
	parsed.Container = strings.ToLower(ext)
	if m.kind == "series" {
		parsed.Seasons = []int{req.Season}
		parsed.Episodes = []int{req.Episode}
	}
	if m.tmdb == 0 && m.kind == "movie" {
		m.tmdb = info.TMDB.Int()
		if m.tmdb == 0 {
			m.tmdb = info.TMDBAlt.Int()
		}
	}
	if !(req.IDs.TMDB > 0 && req.IDs.TMDB == m.tmdb) && (req.Year <= 0 || m.year <= 0 || req.Year != m.year) {
		return out
	}
	if req.IDs.TMDB > 0 && m.tmdb > 0 && req.IDs.TMDB != m.tmdb {
		return out
	}
	if parsed.Year == 0 {
		parsed.Year = m.year
	}
	if h := int(info.Video.Height.Int()); h > 0 {
		parsed.Height = h
	}
	if info.Video.Codec != "" {
		parsed.VideoCodec = strings.ToLower(info.Video.Codec)
		switch parsed.VideoCodec {
		case "h265":
			parsed.VideoCodec = "hevc"
		case "avc":
			parsed.VideoCodec = "h264"
		case "mpeg2video":
			parsed.VideoCodec = "mpeg2"
		}
	}
	if info.Audio.Codec != "" {
		parsed.AudioCodec = info.Audio.Codec
	}
	rid := "xtream:" + src.ID + ":" + m.kind + ":" + id
	headers := map[string]string{}
	for k, vs := range src.Auth.header() {
		headers[k] = strings.Join(vs, ", ")
	}
	return append(out, core.Release{ID: rid, GUID: rid, Indexer: "Xtream", Name: name + "." + ext, Size: info.Size.Int(), Info: parsed, TitleIDs: core.IDs{TMDB: m.tmdb}, Protocol: core.ProtocolHTTP, StreamURL: raw, StreamOrigin: c.Base, StreamHeaders: headers, StreamProvider: src.ID})
}

// VODIndexer adds indexed provider files to the aggregate source contract.
type VODIndexer struct {
	Inner   core.Indexer
	Service func() *Service
}

func (v *VODIndexer) Name() string { return v.Inner.Name() }
func (v *VODIndexer) Search(ctx context.Context, r core.SearchRequest) ([]core.Release, error) {
	return v.SearchWith(ctx, r, nil)
}
func (v *VODIndexer) SearchWith(ctx context.Context, r core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	type result struct {
		releases []core.Release
		err      error
	}
	done := make(chan result, 1)
	go func() {
		s := v.Service()
		if s == nil {
			done <- result{}
			return
		}
		b, e := s.VODSearch(ctx, r)
		if arrived != nil && len(b) > 0 {
			arrived(b)
		}
		done <- result{b, e}
	}()
	var a []core.Release
	var err error
	if streaming, ok := v.Inner.(core.StreamingIndexer); ok {
		a, err = streaming.SearchWith(ctx, r, arrived)
	} else {
		a, err = v.Inner.Search(ctx, r)
		if arrived != nil && len(a) > 0 {
			arrived(a)
		}
	}
	b := <-done
	if len(a) > 0 || len(b.releases) > 0 {
		return append(a, b.releases...), nil
	}
	if b.err != nil {
		return nil, b.err
	}
	if (errors.Is(err, core.ErrNoSources) || errors.Is(err, core.ErrNoTorrentSources)) && v.hasVOD() {
		return nil, nil
	}
	return nil, err
}
func (v *VODIndexer) ResolveTorrent(ctx context.Context, r core.Release) (core.Release, error) {
	if inner, ok := v.Inner.(interface {
		ResolveTorrent(context.Context, core.Release) (core.Release, error)
	}); ok {
		return inner.ResolveTorrent(ctx, r)
	}
	return r, core.ErrSourceUnavailable
}
func (v *VODIndexer) SearchRescue(ctx context.Context, r core.SearchRequest) ([]core.Release, error) {
	if inner, ok := v.Inner.(core.RescueIndexer); ok {
		return inner.SearchRescue(ctx, r)
	}
	return nil, nil
}
func (v *VODIndexer) SearchRescueWith(ctx context.Context, r core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	if inner, ok := v.Inner.(core.StreamingRescueIndexer); ok {
		return inner.SearchRescueWith(ctx, r, arrived)
	}
	a, e := v.SearchRescue(ctx, r)
	if arrived != nil && len(a) > 0 {
		arrived(a)
	}
	return a, e
}
func (v *VODIndexer) hasVOD() bool {
	s := v.Service()
	if s == nil {
		return false
	}
	sources, err := s.Sources(s.ctx)
	if err != nil {
		return false
	}
	for _, src := range sources {
		if src.VOD && src.Auth != nil && src.Auth.Type == "xtream" {
			return true
		}
	}
	return false
}
func (v *VODIndexer) MissingSources() error {
	if v.hasVOD() {
		return nil
	}
	if inner, ok := v.Inner.(interface{ MissingSources() error }); ok {
		return inner.MissingSources()
	}
	return nil
}
func (v *VODIndexer) HasTorrentIndexer() bool {
	if inner, ok := v.Inner.(interface{ HasTorrentIndexer() bool }); ok {
		return inner.HasTorrentIndexer()
	}
	return false
}
func (v *VODIndexer) FetchNZB(ctx context.Context, r core.Release) ([]byte, error) {
	return v.Inner.FetchNZB(ctx, r)
}

func (v *VODIndexer) HasDirectSources() bool { return v.hasVOD() }

// The service mutex also covers live sessions and broadcast reservations.
func (s *Service) reserveVODProvider(id string, limit int, background bool) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := map[string]bool{}
	for _, ss := range s.sessions {
		source := ss.info.Channel.SourceID
		if ss.inputSource.AuthSourceID != "" {
			source = ss.inputSource.AuthSourceID
		}
		if source == id {
			channels[ss.info.Channel.ID] = true
		}
	}
	for _, ch := range s.broadcastProviders[id] {
		channels[ch] = true
	}
	if background && len(channels) > 0 {
		return nil, ErrProviderBusy
	}

	return s.inputRelay.taps.reserveWithLive(id, limit, background, len(channels))
}

func (s *Service) enrichVODMatch(ctx context.Context, m *vodMatch, info xtream.MediaInfo) {
	oldTMDB, oldYear := m.tmdb, m.year
	if m.tmdb == 0 {
		m.tmdb = info.TMDB.Int()
		if m.tmdb == 0 {
			m.tmdb = info.TMDBAlt.Int()
		}
	}
	if m.year == 0 && len(info.ReleaseDate) >= 4 {
		m.year, _ = strconv.Atoi(info.ReleaseDate[:4])
	}
	if m.year == 0 {
		m.year = releaseparse.Parse(info.Name).Year
	}
	if m.tmdb != oldTMDB || m.year != oldYear {
		_, _ = s.o.DB.ExecContext(ctx, `UPDATE xtream_vod SET tmdb=?,year=? WHERE provider=? AND kind=? AND id=? AND revision=?`, m.tmdb, m.year, m.provider, m.kind, m.id, m.revision)
	}
}
