// Package guide implements the Plex upstream EPG protocol.
package guide

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/livetv/guideprovider"
	"github.com/braedonsaunders/ether/internal/retryafter"
)

var leaseID atomic.Uint64

type Client struct {
	DB         *sql.DB
	HTTPClient *http.Client
	URL        string
	Token      func() string
	plexGate   plexGuideGate
}

const plexGuideURL = "https://epg.provider.plex.tv"

// A database lease coordinates nodes and survives a process restart. A dead
// caller loses its lease after a minute; normal requests time out in 20s.
func (s *Client) acquirePlexGate(ctx context.Context) (func(time.Time, int), error) {
	if s.DB == nil {
		return func(time.Time, int) {}, nil
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO livetv_plex_guide_gate(id,holder,locked_until,next_at) VALUES(1,'',0,0) ON CONFLICT(id) DO NOTHING`); err != nil {
		return nil, err
	}
	holder := fmt.Sprintf("%d-%d", time.Now().UnixNano(), leaseID.Add(1))
	for {
		now := time.Now()
		res, err := s.DB.ExecContext(ctx, `UPDATE livetv_plex_guide_gate SET holder=?, locked_until=? WHERE id=1 AND locked_until<=? AND next_at<=?`, holder, now.Add(time.Minute).UnixMilli(), now.UnixMilli(), now.UnixMilli())
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
				_, _ = s.DB.ExecContext(release, `UPDATE livetv_plex_guide_gate SET holder='', locked_until=0, next_at=?, status=? WHERE id=1 AND holder=?`, next.UnixMilli(), status, holder)
			}, nil
		}
		var nextAt int64
		var status int
		if err := s.DB.QueryRowContext(ctx, `SELECT next_at,status FROM livetv_plex_guide_gate WHERE id=1`).Scan(&nextAt, &status); err != nil {
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

type PlexLineup = guideprovider.Lineup
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

func (s *Client) plexGuideRequest(ctx context.Context, path string, q url.Values, budget *int) (plexGuideResponse, error) {
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
	if s.URL != "" {
		base = strings.TrimRight(s.URL, "/")
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
	if s.Token != nil {
		if token := s.Token(); token != "" {
			req.Header.Set("X-Plex-Token", token)
		}
	}
	// Never forward a server token through redirects.
	client := *s.HTTPClient
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
func (s *Client) Lineups(ctx context.Context, country, postal string) ([]PlexLineup, error) {
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
func (s *Client) Grid(ctx context.Context, key string, date time.Time, budget *int) ([]guideprovider.Programme, error) {
	if !validPlexID(key) {
		return nil, fmt.Errorf("invalid Plex guide channel")
	}
	v, e := s.plexGuideRequest(ctx, "/grid", url.Values{"channelGridKey": {key}, "type": {"1,4"}, "date": {date.Format("2006-01-02")}}, budget)
	if e != nil {
		return nil, e
	}
	var out []guideprovider.Programme
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
			out = append(out, guideprovider.Programme{ChannelID: key, Start: time.Unix(air.Begins, 0).UTC(), End: time.Unix(air.Ends, 0).UTC(), Item: guideprovider.Item{Title: title, EpisodeTitle: episode, ShowTitle: m.Show, Summary: m.Summary, Thumb: thumb, Art: art, Genres: genres, Season: season, Episode: m.Episode, ReleasedMS: released, DurationMS: m.Duration, GUID: m.GUID, ShowGUID: m.ShowGUID, MediaType: m.Type, Premiere: air.Premiere, Live: air.Live}})
		}
	}
	return out, nil
}

func (s *Client) Channels(ctx context.Context, id string, budget *int) ([]guideprovider.Channel, error) {
	if !validPlexID(id) {
		return nil, fmt.Errorf("invalid Plex lineup")
	}
	v, err := s.plexGuideRequest(ctx, "/lineups/"+url.PathEscape(id)+"/channels", nil, budget)
	if err != nil {
		return nil, err
	}
	var out []guideprovider.Channel
	for _, c := range v.Container.Channels {
		if validPlexID(c.GridKey) {
			out = append(out, guideprovider.Channel{ID: c.GridKey, Name: c.Title, Logo: c.Thumb, Aliases: []string{c.CallSign, c.Affiliate, c.Short, c.VCN}})
		}
	}
	return out, nil
}
