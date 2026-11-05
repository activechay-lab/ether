package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/retryafter"
	"github.com/braedonsaunders/ether/internal/store"
)

const plexGuideURL = "https://epg.provider.plex.tv"
const plexGuideBudget = 80

func init() {
	store.Register("livetv", 5, `CREATE TABLE livetv_plex_guide_gate (id INTEGER PRIMARY KEY, holder TEXT NOT NULL, locked_until BIGINT NOT NULL, next_at BIGINT NOT NULL, status INTEGER NOT NULL DEFAULT 0);`)
}

// A database lease coordinates nodes and survives a process restart. A dead
// caller loses its lease after a minute; normal requests time out in 20s.
func (s *Service) acquirePlexGate(ctx context.Context) (func(time.Time, int), error) {
	if s.o.DB == nil {
		return func(time.Time, int) {}, nil
	}
	if _, err := s.o.DB.ExecContext(ctx, `INSERT INTO livetv_plex_guide_gate(id,holder,locked_until,next_at) VALUES(1,'',0,0) ON CONFLICT(id) DO NOTHING`); err != nil {
		return nil, err
	}
	holder := randomID()
	for {
		now := time.Now()
		res, err := s.o.DB.ExecContext(ctx, `UPDATE livetv_plex_guide_gate SET holder=?, locked_until=? WHERE id=1 AND locked_until<=? AND next_at<=?`, holder, now.Add(time.Minute).UnixMilli(), now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 1 {
			return func(next time.Time, status int) {
				release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = s.o.DB.ExecContext(release, `UPDATE livetv_plex_guide_gate SET holder='', locked_until=0, next_at=?, status=? WHERE id=1 AND holder=?`, next.UnixMilli(), status, holder)
			}, nil
		}
		var nextAt int64
		var status int
		if err := s.o.DB.QueryRowContext(ctx, `SELECT next_at,status FROM livetv_plex_guide_gate WHERE id=1`).Scan(&nextAt, &status); err != nil {
			return nil, err
		}
		if nextAt > time.Now().UnixMilli() && status == 429 {
			return nil, fmt.Errorf("Plex guide rate-limited; retry later")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// plexGuideGate serializes and spaces all requests, including previews and
// setup. A Retry-After applies to all callers using this service.
type plexGuideGate struct {
	mu       sync.Mutex
	next     time.Time
	failures int
	status   int
}

type PlexLineup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}
type plexGuideChannel struct {
	GridKey   string `json:"gridKey"`
	Title     string `json:"title"`
	Thumb     string `json:"thumb"`
	CallSign  string `json:"callSign"`
	Affiliate string `json:"affiliateCallSign"`
	Short     string `json:"shortTitle"`
	VCN       string `json:"vcn"`
}
type plexGuideMetadata struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Show      string `json:"grandparentTitle"`
	Summary   string `json:"summary"`
	Thumb     string `json:"thumb"`
	ShowThumb string `json:"grandparentThumb"`
	Art       string `json:"art"`
	ShowArt   string `json:"grandparentArt"`
	GUID      string `json:"guid"`
	ShowGUID  string `json:"grandparentGuid"`
	Season    int    `json:"parentIndex"`
	Episode   int    `json:"index"`
	Date      string `json:"originallyAvailableAt"`
	Duration  int64  `json:"duration"`
	Genres    []struct {
		Tag string `json:"tag"`
	} `json:"Genre"`
	Media []struct {
		Begins   int64 `json:"beginsAt"`
		Ends     int64 `json:"endsAt"`
		Premiere bool  `json:"premiere"`
		Live     bool  `json:"live"`
	} `json:"Media"`
}
type plexGuideResponse struct {
	Container struct {
		Lineups  []PlexLineup        `json:"Lineup"`
		Channels []plexGuideChannel  `json:"Channel"`
		Metadata []plexGuideMetadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (s *Service) plexGuideRequest(ctx context.Context, path string, q url.Values, budget *int) (plexGuideResponse, error) {
	var result plexGuideResponse
	s.plexGate.mu.Lock()
	defer s.plexGate.mu.Unlock()
	if budget != nil {
		if *budget <= 0 {
			return result, fmt.Errorf("Plex guide request budget exhausted")
		}
		*budget--
	}
	if delay := time.Until(s.plexGate.next); delay > 0 {
		if s.plexGate.status == 429 {
			return result, fmt.Errorf("Plex guide rate-limited; retry later")
		}
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-t.C:
		}
	}
	release, err := s.acquirePlexGate(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err.Error() == "Plex guide rate-limited; retry later" {
			return result, err
		}
		return result, fmt.Errorf("Plex guide is unavailable")
	}
	defer func() { release(s.plexGate.next, s.plexGate.status) }()
	base := plexGuideURL
	if s.o.PlexGuideURL != "" {
		base = strings.TrimRight(s.o.PlexGuideURL, "/")
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return result, fmt.Errorf("invalid Plex guide request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "Ether")
	req.Header.Set("X-Plex-Client-Identifier", "ether-guide")
	if s.o.PlexGuideToken != nil {
		if token := s.o.PlexGuideToken(); token != "" {
			req.Header.Set("X-Plex-Token", token)
		}
	}
	// Never forward a server token through redirects.
	client := *s.o.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	s.plexGate.next = time.Now().Add(time.Second)
	s.plexGate.status = 0
	if err != nil {
		s.plexGate.failures++
		s.plexGate.next = time.Now().Add(plexBackoff(s.plexGate.failures))
		return result, fmt.Errorf("Plex guide is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.plexGate.failures++
		s.plexGate.status = resp.StatusCode
		wait := plexBackoff(s.plexGate.failures)
		if d, ok := retryafter.Parse(resp.Header.Get("Retry-After"), time.Now(), 24*time.Hour); ok {
			wait = d
		}
		s.plexGate.next = time.Now().Add(wait)
		switch resp.StatusCode {
		case 401:
			return result, fmt.Errorf("Plex guide sign-in expired")
		case 403:
			return result, fmt.Errorf("Plex guide access refused; Plex Pass may be required")
		case 429:
			return result, fmt.Errorf("Plex guide rate-limited; retry later")
		default:
			return result, fmt.Errorf("Plex guide request failed (HTTP %d)", resp.StatusCode)
		}
	}
	s.plexGate.failures = 0
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&result); err != nil {
		return result, fmt.Errorf("invalid Plex guide response")
	}
	return result, nil
}
func plexBackoff(n int) time.Duration {
	if n > 8 {
		n = 8
	}
	return time.Duration(1<<uint(n-1)) * time.Second
}
func (s *Service) PlexLineups(ctx context.Context, country, postal string) ([]PlexLineup, error) {
	country = strings.TrimSpace(country)
	postal = strings.TrimSpace(postal)
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' || postal == "" || len(postal) > 32 {
		return nil, fmt.Errorf("enter an uppercase two-letter country and postal code")
	}
	v, e := s.plexGuideRequest(ctx, "/lineups", url.Values{"country": {country}, "postalCode": {postal}}, nil)
	return v.Container.Lineups, e
}
func validPlexID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (s *Service) plexGrid(ctx context.Context, key string, date time.Time, budget *int) ([]Programme, error) {
	if !validPlexID(key) {
		return nil, fmt.Errorf("invalid Plex guide channel")
	}
	v, e := s.plexGuideRequest(ctx, "/grid", url.Values{"channelGridKey": {key}, "type": {"1,4"}, "date": {date.Format("2006-01-02")}}, budget)
	if e != nil {
		return nil, e
	}
	var out []Programme
	for _, m := range v.Container.Metadata {
		for _, air := range m.Media {
			if air.Begins <= 0 || air.Ends <= air.Begins {
				continue
			}
			title := m.Title
			episode := ""
			if m.Show != "" {
				title = m.Show
				episode = m.Title
			}
			thumb := m.Thumb
			if thumb == "" {
				thumb = m.ShowThumb
			}
			art := m.Art
			if art == "" {
				art = m.ShowArt
			}
			var genres []string
			for _, g := range m.Genres {
				if g.Tag != "" {
					genres = append(genres, g.Tag)
				}
			}
			released := int64(0)
			if t, e := time.Parse(time.RFC3339, m.Date); e == nil {
				released = t.UnixMilli()
			} else if t, e := time.Parse("2006-01-02", m.Date); e == nil {
				released = t.UnixMilli()
			}
			season := m.Season
			if m.Episode == 0 && season >= 1900 && season <= 2100 {
				season = 0
			}
			out = append(out, Programme{ChannelID: key, Start: time.Unix(air.Begins, 0).UTC(), End: time.Unix(air.Ends, 0).UTC(), Item: Item{Title: title, EpisodeTitle: episode, ShowTitle: m.Show, Summary: m.Summary, Thumb: thumb, Art: art, Genres: genres, Season: season, Episode: m.Episode, ReleasedMS: released, DurationMS: m.Duration, GUID: m.GUID, ShowGUID: m.ShowGUID, MediaType: m.Type, Premiere: air.Premiere, Live: air.Live}})
		}
	}
	return out, nil
}

// plexGridDates covers the requested UTC window, tomorrow's UTC grid, and
// the server-local date when it differs. Plex grids overlap across dates.
func plexGridDates(now, from, until time.Time) []time.Time {
	dates := map[string]time.Time{}
	add := func(t time.Time) {
		date := t.Format("2006-01-02")
		if _, ok := dates[date]; !ok {
			dates[date], _ = time.Parse("2006-01-02", date)
		}
	}
	utc := now.UTC()
	add(utc)
	add(utc.AddDate(0, 0, 1))
	add(now)
	first := from.UTC()
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
	for d := first; d.Before(until.UTC()); d = d.AddDate(0, 0, 1) {
		add(d)
	}
	out := make([]time.Time, 0, len(dates))
	for _, d := range dates {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// Later entries replace earlier ones for the same channel and start. Sort
// before presentation as upstream groups airings by metadata, not by time.
func uniquePlexProgrammes(ps []Programme) []Programme {
	type key struct {
		channel string
		start   int64
	}
	seen := make(map[key]Programme, len(ps))
	for _, p := range ps {
		seen[key{p.ChannelID, p.Start.UnixMilli()}] = p
	}
	out := make([]Programme, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out
}

func (s *Service) plexGrids(ctx context.Context, key string, dates []time.Time, budget *int) ([]Programme, error) {
	var programmes []Programme
	for _, date := range dates {
		ps, err := s.plexGrid(ctx, key, date, budget)
		if err != nil {
			return nil, err
		}
		programmes = append(programmes, ps...)
	}
	return uniquePlexProgrammes(programmes), nil
}

type plexGuideKind struct{}

func (plexGuideKind) ID() string              { return "plex" }
func (plexGuideKind) Matches(src Source) bool { return src.Kind == "plex" }
func (plexGuideKind) Describe() SourceDescribe {
	return SourceDescribe{Label: extend.T("Plex guide"), Icon: "calendar-range", Editor: "guide", YieldsGuide: true, Order: 31}
}
func (plexGuideKind) Fetch(ctx context.Context, s *Service, src *Source) (Fetched, error) {
	if !validPlexID(src.LineupID) {
		return Fetched{}, fmt.Errorf("invalid Plex lineup")
	}
	budget := plexGuideBudget
	v, err := s.plexGuideRequest(ctx, "/lineups/"+url.PathEscape(src.LineupID)+"/channels", nil, &budget)
	if err != nil {
		return Fetched{}, err
	}
	g := XMLGuide{Channels: map[string]Channel{}, Names: map[string][]string{}}
	for _, c := range v.Container.Channels {
		if !validPlexID(c.GridKey) {
			continue
		}
		g.Channels[c.GridKey] = Channel{Name: c.Title, Logo: c.Thumb}
		g.Names[c.GridKey] = []string{c.CallSign, c.Affiliate, c.Short, c.VCN}
	}
	cs, err := s.Channels(ctx)
	if err != nil {
		return Fetched{}, err
	}
	used := map[string]bool{}
	for _, c := range cs {
		if c.Kind == "iptv" && guideSource(c) == src.ID && c.GuideID != "" {
			if _, ok := g.Channels[c.GuideID]; ok {
				used[c.GuideID] = true
			}
		}
	}
	keys := make([]string, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Retain existing listings while rotating through bounded batches; a large
	// lineup must not be starved by the budget or erased on each refresh.
	rows, err := s.o.DB.QueryContext(ctx, `SELECT data FROM livetv_programmes WHERE source_id=? AND end_ms>?`, src.ID, s.o.Now().UnixMilli())
	if err != nil {
		return Fetched{}, err
	}
	for rows.Next() {
		var b []byte
		var p Programme
		if err = rows.Scan(&b); err != nil {
			break
		}
		if json.Unmarshal(b, &p) == nil && used[p.ChannelID] {
			g.Programmes = append(g.Programmes, p)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return Fetched{}, err
	}
	if len(keys) > 0 {
		start := src.PlexCursor % len(keys)
		now := s.o.Now()
		dates := plexGridDates(now, now, now.UTC().AddDate(0, 0, 1))
		n := min(len(keys), budget/len(dates))
		for i := 0; i < n; i++ {
			k := keys[(start+i)%len(keys)]
			ps, e := s.plexGrids(ctx, k, dates, &budget)
			if e != nil {
				return Fetched{}, e
			}
			g.Programmes = append(g.Programmes, ps...)
		}
		src.PlexCursor = (start + n) % len(keys)
	}
	g.Programmes = uniquePlexProgrammes(g.Programmes)
	return Fetched{Guide: g}, nil
}

type plexPreviewEntry struct {
	at         time.Time
	programmes []Programme
}

func (s *Service) previewProgrammes(ctx context.Context, c Channel, from, until time.Time) ([]Programme, error) {
	src, err := s.storedSourceByID(ctx, guideSource(c))
	if err != nil {
		return s.programmes(ctx, c, from, until)
	}
	if src.Kind != "plex" || c.GuideID == "" {
		return s.programmes(ctx, c, from, until)
	}
	var exists int
	err = s.o.DB.QueryRowContext(ctx, `SELECT 1 FROM livetv_guide_channels WHERE source_id=? AND guide_id=?`, src.ID, c.GuideID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("unknown Plex guide channel")
	}
	dates := plexGridDates(s.o.Now(), from, until)
	dateKey := make([]string, 0, len(dates))
	for _, date := range dates {
		dateKey = append(dateKey, date.Format("2006-01-02"))
	}
	key := src.ID + "/" + c.GuideID + "/" + strings.Join(dateKey, ",")
	s.plexPreviewMu.Lock()
	defer s.plexPreviewMu.Unlock()
	entry, ok := s.plexPreview[key]
	if !ok || time.Since(entry.at) > 10*time.Minute {
		budget := len(dates)
		ps, e := s.plexGrids(ctx, c.GuideID, dates, &budget)
		if e != nil {
			return nil, e
		}
		if s.plexPreview == nil || len(s.plexPreview) >= 1000 {
			s.plexPreview = map[string]plexPreviewEntry{}
		}
		entry = plexPreviewEntry{at: time.Now(), programmes: ps}
		s.plexPreview[key] = entry
	}
	out := []Programme{}
	for _, p := range entry.programmes {
		if p.End.After(from) && p.Start.Before(until) {
			p.ChannelID = c.ID
			p.ID = fmt.Sprintf("%s-%d", c.ID, p.Start.UnixMilli())
			out = append(out, p)
		}
	}
	return out, nil
}
