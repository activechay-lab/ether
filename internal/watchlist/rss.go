package watchlist

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	sourcePlexFriendsRSS = "plex-friends-rss"
	sourcePlexOwnerRSS   = "plex-rss"

	metaKeyFriendsRSS = "rss_friends"
	metaKeyOwnerRSS   = "rss_owner"

	rssProblemAuth        = "auth"
	rssProblemRateLimited = "rate_limited"
	rssProblemInvalidFeed = "invalid_feed"
	rssProblemMetadata    = "metadata"
	rssProblemNetwork     = "network"
)

// RSSFeedStatus reports the latest poll state of a configured Plex watchlist RSS feed.
type RSSFeedStatus struct {
	Configured      bool   `json:"configured"`
	LastRead        int64  `json:"lastRead"`
	Imported        int    `json:"imported"`
	SkippedFriends  int    `json:"skippedFriends"`
	UnmatchedTitles int    `json:"unmatchedTitles"`
	Problem         string `json:"problem,omitempty"`
}

type rssMeta struct {
	URLHash         string `json:"urlHash,omitempty"`
	ViewersHash     string `json:"viewersHash,omitempty"`
	ETag            string `json:"etag,omitempty"`
	LastModified    string `json:"lastModified,omitempty"`
	LastRead        int64  `json:"lastRead,omitempty"`
	Imported        int    `json:"imported"`
	SkippedFriends  int    `json:"skippedFriends"`
	UnmatchedTitles int    `json:"unmatchedTitles"`
	Problem         string `json:"problem,omitempty"`
}

type rssFeedXML struct {
	XMLName xml.Name     `xml:""`
	Channel rssChannel   `xml:"channel"`
	Items   []rssXMLItem `xml:"item"`
	Entries []rssAtom    `xml:"entry"`
}

type rssChannel struct {
	Items []rssXMLItem `xml:"item"`
}

type rssXMLItem struct {
	Title      string        `xml:"title"`
	Guids      []rssGUID     `xml:"guid"`
	Links      []rssLink     `xml:"link"`
	Authors    []string      `xml:"author"`
	Creators   []string      `xml:"creator"`
	Categories []rssCategory `xml:"category"`
}

type rssAtom struct {
	Title      string        `xml:"title"`
	ID         string        `xml:"id"`
	Links      []rssLink     `xml:"link"`
	Authors    []rssAtomUser `xml:"author"`
	Categories []rssCategory `xml:"category"`
}

type rssAtomUser struct {
	Name  string `xml:"name"`
	Email string `xml:"email"`
	Text  string `xml:",chardata"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

type rssLink struct {
	Value string `xml:",chardata"`
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
}

type rssCategory struct {
	Value  string `xml:",chardata"`
	Term   string `xml:"term,attr"`
	Domain string `xml:"domain,attr"`
}

type rssItem struct {
	Title      string
	Guids      []string
	Links      []string
	Authors    []string
	Creators   []string
	Categories []rssCategory
}

type traktWriter interface {
	SetWatchlist(ctx context.Context, userID int64, title core.Title, add bool) (bool, error)
}

type typedExternalCatalog interface {
	ByExternalIDForType(ctx context.Context, source, id string, typ core.MediaType) (*core.Title, error)
}

func validateRSSURL(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("RSS URL must be a valid http or https URL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("RSS URL must be a valid http or https URL")
	}
	return nil
}

// SyncRSS polls configured Plex watchlist RSS feeds across active Ether viewers.
func (s *Service) SyncRSS(ctx context.Context) error {
	cfg, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || (cfg.PlexFriendsRSSURL == "" && cfg.PlexOwnerRSSURL == "") {
		return nil
	}
	if s.o.Users == nil {
		return nil
	}
	us, err := s.o.Users(ctx)
	if err != nil {
		return err
	}
	return s.syncRSSUsers(ctx, us)
}

func (s *Service) syncRSSUsers(ctx context.Context, us []core.User) error {
	cfg, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || (cfg.PlexFriendsRSSURL == "" && cfg.PlexOwnerRSSURL == "") {
		return nil
	}
	var errs []error
	if cfg.PlexFriendsRSSURL != "" {
		if e := s.pollRSSFeed(ctx, cfg.PlexFriendsRSSURL, metaKeyFriendsRSS, sourcePlexFriendsRSS, true, us); e != nil {
			errs = append(errs, e)
		}
	}
	if cfg.PlexOwnerRSSURL != "" {
		if e := s.pollRSSFeed(ctx, cfg.PlexOwnerRSSURL, metaKeyOwnerRSS, sourcePlexOwnerRSS, false, us); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) pollRSSFeed(ctx context.Context, feedURL, metaKey, source string, friendsFeed bool, us []core.User) error {
	if s.o.Catalog == nil {
		return errors.New("Plex watchlist RSS requires a catalog")
	}
	urlHash := sha256Hex(feedURL)
	vHash := hashViewers(us)
	meta := s.readRSSMeta(ctx, metaKey)
	if meta.URLHash != urlHash {
		meta = rssMeta{URLHash: urlHash}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		meta.Problem = rssProblemInvalidFeed
		s.writeRSSMeta(ctx, metaKey, meta)
		return errors.New("Plex watchlist RSS URL invalid")
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml;q=0.9, application/json;q=0.5, */*;q=0.1")
	req.Header.Set("User-Agent", "Ether")
	if meta.ViewersHash == vHash {
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}

	resp, err := s.o.Client.Do(req)
	if err != nil {
		// Never return or log *url.Error raw: it embeds the secret feed URL.
		meta.Problem = rssProblemNetwork
		s.writeRSSMeta(ctx, metaKey, meta)
		return errors.New("Plex watchlist RSS request failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	resp.Body.Close()
	if readErr != nil || len(data) > 4<<20 {
		meta.Problem = rssProblemInvalidFeed
		s.writeRSSMeta(ctx, metaKey, meta)
		return errors.New("Plex watchlist RSS response unreadable or too large")
	}

	switch resp.StatusCode {
	case http.StatusNotModified:
		meta.LastRead = s.o.Now().Unix()
		meta.Problem = ""
		if etag := strings.TrimSpace(resp.Header.Get("ETag")); etag != "" {
			meta.ETag = etag
		}
		if lm := strings.TrimSpace(resp.Header.Get("Last-Modified")); lm != "" {
			meta.LastModified = lm
		}
		meta.ViewersHash = vHash
		s.writeRSSMeta(ctx, metaKey, meta)
		return nil
	case http.StatusOK:
		// Handled below.
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		meta.Problem = rssProblemAuth
		s.writeRSSMeta(ctx, metaKey, meta)
		return fmt.Errorf("Plex watchlist RSS: HTTP %d", resp.StatusCode)
	case http.StatusTooManyRequests:
		meta.Problem = rssProblemRateLimited
		s.writeRSSMeta(ctx, metaKey, meta)
		return errors.New("Plex watchlist RSS rate limited")
	default:
		meta.Problem = rssProblemNetwork
		s.writeRSSMeta(ctx, metaKey, meta)
		return fmt.Errorf("Plex watchlist RSS: HTTP %d", resp.StatusCode)
	}

	items, err := parseRSSItems(data)
	if err != nil {
		meta.Problem = rssProblemInvalidFeed
		s.writeRSSMeta(ctx, metaKey, meta)
		return err
	}

	matcher := newViewerMatcher(us)
	byUser := make(map[int64][]core.Title)
	skippedFriends := 0
	unmatchedTitles := 0

	for _, it := range items {
		var target *core.User
		if friendsFeed {
			target = matcher.matchFriend(extractRSSItemFriend(it))
		} else {
			target = matcher.owner
		}
		if target == nil {
			skippedFriends++
			continue
		}
		title, e := s.resolveRSSItem(ctx, it)
		if e != nil {
			// Preserve the last good snapshot and conditional headers on a transient
			// catalogue/TMDB outage so the next pass re-fetches and retries cleanly.
			meta.Problem = rssProblemMetadata
			s.writeRSSMeta(ctx, metaKey, meta)
			return fmt.Errorf("Plex watchlist RSS metadata: %w", e)
		}
		if title == nil {
			unmatchedTitles++
			continue
		}
		byUser[target.ID] = append(byUser[target.ID], *title)
	}

	imported, err := s.replaceFeedSnapshot(ctx, source, us, byUser)
	if err != nil {
		meta.Problem = rssProblemMetadata
		s.writeRSSMeta(ctx, metaKey, meta)
		return err
	}

	meta.URLHash = urlHash
	meta.ViewersHash = vHash
	meta.ETag = strings.TrimSpace(resp.Header.Get("ETag"))
	meta.LastModified = strings.TrimSpace(resp.Header.Get("Last-Modified"))
	meta.LastRead = s.o.Now().Unix()
	meta.Imported = imported
	meta.SkippedFriends = skippedFriends
	meta.UnmatchedTitles = unmatchedTitles
	meta.Problem = ""
	s.writeRSSMeta(ctx, metaKey, meta)
	return nil
}

func parseRSSItems(data []byte) ([]rssItem, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("Plex watchlist RSS response empty")
	}
	if trimmed[0] == '{' {
		return parsePlexRSSJSON(trimmed)
	}
	var doc rssFeedXML
	if err := xml.Unmarshal(trimmed, &doc); err != nil {
		return nil, errors.New("Plex watchlist RSS response invalid")
	}
	local := strings.ToLower(doc.XMLName.Local)
	if local != "rss" && local != "rdf" && local != "feed" {
		return nil, errors.New("Plex watchlist RSS response invalid")
	}
	xmlItems := doc.Channel.Items
	if len(xmlItems) == 0 && len(doc.Items) > 0 {
		xmlItems = doc.Items
	}
	if len(xmlItems)+len(doc.Entries) > 2000 {
		return nil, errors.New("Plex watchlist RSS exceeds 2000-item safety limit; previous snapshot preserved")
	}
	out := make([]rssItem, 0, len(xmlItems)+len(doc.Entries))
	for _, x := range xmlItems {
		it := rssItem{
			Title:      strings.TrimSpace(x.Title),
			Authors:    x.Authors,
			Creators:   x.Creators,
			Categories: x.Categories,
		}
		for _, g := range x.Guids {
			if v := strings.TrimSpace(g.Value); v != "" {
				it.Guids = append(it.Guids, v)
			}
		}
		for _, l := range x.Links {
			if v := strings.TrimSpace(l.Value); v != "" {
				it.Links = append(it.Links, v)
			} else if h := strings.TrimSpace(l.Href); h != "" {
				it.Links = append(it.Links, h)
			}
		}
		out = append(out, it)
	}
	for _, e := range doc.Entries {
		it := rssItem{
			Title:      strings.TrimSpace(e.Title),
			Categories: e.Categories,
		}
		if id := strings.TrimSpace(e.ID); id != "" {
			it.Guids = append(it.Guids, id)
		}
		for _, l := range e.Links {
			if h := strings.TrimSpace(l.Href); h != "" {
				it.Links = append(it.Links, h)
			} else if v := strings.TrimSpace(l.Value); v != "" {
				it.Links = append(it.Links, v)
			}
		}
		for _, a := range e.Authors {
			if n := strings.TrimSpace(a.Name); n != "" {
				it.Authors = append(it.Authors, n)
			}
			if em := strings.TrimSpace(a.Email); em != "" {
				it.Authors = append(it.Authors, em)
			}
			if tx := strings.TrimSpace(a.Text); tx != "" && a.Name == "" && a.Email == "" {
				it.Authors = append(it.Authors, tx)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func parsePlexRSSJSON(data []byte) ([]rssItem, error) {
	var envelope struct {
		Items []struct {
			Title    string   `json:"title"`
			Link     string   `json:"link"`
			Guids    []string `json:"guids"`
			Author   string   `json:"author"`
			Creator  string   `json:"creator"`
			User     string   `json:"user"`
			Category string   `json:"category"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Items == nil {
		return nil, errors.New("Plex watchlist RSS response invalid")
	}
	if len(envelope.Items) > 2000 {
		return nil, errors.New("Plex watchlist RSS exceeds 2000-item safety limit; previous snapshot preserved")
	}
	out := make([]rssItem, 0, len(envelope.Items))
	for _, x := range envelope.Items {
		it := rssItem{
			Title: strings.TrimSpace(x.Title),
			Guids: x.Guids,
		}
		if l := strings.TrimSpace(x.Link); l != "" {
			it.Links = append(it.Links, l)
		}
		for _, c := range []string{x.Creator, x.User} {
			if v := strings.TrimSpace(c); v != "" {
				it.Creators = append(it.Creators, v)
			}
		}
		if a := strings.TrimSpace(x.Author); a != "" {
			it.Authors = append(it.Authors, a)
		}
		if cat := strings.TrimSpace(x.Category); cat != "" {
			it.Categories = append(it.Categories, rssCategory{Value: cat})
		}
		out = append(out, it)
	}
	return out, nil
}

// extractRSSItemFriend is the SINGLE POINT OF ADJUSTMENT for extracting which
// Plex friend watchlisted an RSS <item>.
//
// Note on Plex's public rss.plex.tv format: Plex's combined "friendsWatchlist"
// RSS 2.0 XML omits <author> and <dc:creator> for privacy and uses <category>
// only for the media type ("movie" / "show"). When per-item attribution is
// present (via <dc:creator>, <author>, <category domain="friend|user">, or a
// non-media <category>), candidates are returned in priority order; when absent,
// an empty slice is returned so the item is counted as skipped rather than
// attributed to the wrong viewer.
func extractRSSItemFriend(it rssItem) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return
		}
		k := strings.ToLower(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	addAuthorLike := func(raw string) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return
		}
		// Handle RFC 822 RSS <author> forms like "alice@example.com (alice)"
		// and "alice <alice@example.com>" as well as plain usernames or IDs.
		if i := strings.IndexByte(v, '('); i >= 0 {
			if j := strings.LastIndexByte(v, ')'); j > i+1 {
				add(v[i+1 : j])
			}
			add(v[:i])
			return
		}
		if i := strings.IndexByte(v, '<'); i >= 0 {
			if j := strings.LastIndexByte(v, '>'); j > i+1 {
				add(v[:i])
				add(v[i+1 : j])
				return
			}
		}
		add(v)
	}

	for _, c := range it.Creators {
		addAuthorLike(c)
	}
	for _, a := range it.Authors {
		addAuthorLike(a)
	}
	for _, cat := range it.Categories {
		val := strings.TrimSpace(cat.Value)
		if val == "" {
			val = strings.TrimSpace(cat.Term)
		}
		if val == "" {
			continue
		}
		dom := strings.ToLower(strings.TrimSpace(cat.Domain))
		if dom == "friend" || dom == "user" || dom == "viewer" || dom == "account" || dom == "plex" || dom == "author" || dom == "creator" {
			add(val)
		}
	}
	for _, cat := range it.Categories {
		val := strings.TrimSpace(cat.Value)
		if val == "" {
			val = strings.TrimSpace(cat.Term)
		}
		if val == "" || isMediaOrFeedCategory(val) {
			continue
		}
		add(val)
	}
	return out
}

func isMediaOrFeedCategory(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "movie", "movies", "film", "films", "show", "shows", "tv", "series", "episode", "episodes", "season", "seasons",
		"watchlist", "my watchlist", "friends watchlist", "friends' watchlist", "your watchlist", "your friends' watchlist":
		return true
	default:
		return false
	}
}

type viewerMatcher struct {
	owner      *core.User
	byPlexID   map[int64]*core.User
	byUUID     map[string][]*core.User
	byUsername map[string][]*core.User
	byEmail    map[string][]*core.User
	byTitle    map[string][]*core.User
}

func newViewerMatcher(us []core.User) viewerMatcher {
	m := viewerMatcher{
		byPlexID:   make(map[int64]*core.User, len(us)),
		byUUID:     make(map[string][]*core.User, len(us)),
		byUsername: make(map[string][]*core.User, len(us)),
		byEmail:    make(map[string][]*core.User, len(us)),
		byTitle:    make(map[string][]*core.User, len(us)),
	}
	for i := range us {
		u := &us[i]
		if u.IsOwner && m.owner == nil {
			m.owner = u
		}
		if u.PlexID > 0 {
			m.byPlexID[u.PlexID] = u
		}
		if k := strings.ToLower(strings.TrimSpace(u.UUID)); k != "" {
			m.byUUID[k] = append(m.byUUID[k], u)
		}
		if k := strings.ToLower(strings.TrimSpace(u.Username)); k != "" {
			m.byUsername[k] = append(m.byUsername[k], u)
		}
		if k := strings.ToLower(strings.TrimSpace(u.Email)); k != "" {
			m.byEmail[k] = append(m.byEmail[k], u)
		}
		if k := strings.ToLower(strings.TrimSpace(u.Title)); k != "" {
			m.byTitle[k] = append(m.byTitle[k], u)
		}
	}
	return m
}

func (m viewerMatcher) matchFriend(candidates []string) *core.User {
	for _, raw := range candidates {
		cand := strings.TrimSpace(raw)
		if cand == "" {
			continue
		}
		idRaw := cand
		for _, prefix := range []string{"plex://user/", "plex://", "plex:", "id:", "user:"} {
			if strings.HasPrefix(strings.ToLower(idRaw), prefix) {
				idRaw = strings.TrimSpace(idRaw[len(prefix):])
				break
			}
		}
		if id, err := strconv.ParseInt(idRaw, 10, 64); err == nil && id > 0 {
			if u, ok := m.byPlexID[id]; ok {
				return u
			}
		}
		key := strings.ToLower(cand)
		if list := m.byUUID[key]; len(list) == 1 {
			return list[0]
		}
		if list := m.byUsername[key]; len(list) == 1 {
			return list[0]
		}
		if list := m.byEmail[key]; len(list) == 1 {
			return list[0]
		}
		if list := m.byTitle[key]; len(list) == 1 {
			return list[0]
		}
	}
	return nil
}

type externalRef struct {
	source string         // "tmdb", "imdb", "tvdb"
	id     string         // numeric id or "tt..."
	typ    core.MediaType // optional TypeMovie / TypeShow
}

func (s *Service) resolveRSSItem(ctx context.Context, it rssItem) (*core.Title, error) {
	itemType := rssItemMediaType(it)
	refs := extractRSSExternalRefs(it, itemType)
	if len(refs) == 0 {
		return nil, nil
	}
	typedCat, hasTyped := s.o.Catalog.(typedExternalCatalog)
	for _, ref := range refs {
		typ := ref.typ
		if typ == "" {
			typ = itemType
		}
		switch ref.source {
		case "tmdb":
			id, err := strconv.ParseInt(ref.id, 10, 64)
			if err != nil || id <= 0 {
				continue
			}
			var keys []core.RatingKey
			switch typ {
			case core.TypeMovie:
				keys = []core.RatingKey{core.MovieKey(id)}
			case core.TypeShow:
				keys = []core.RatingKey{core.ShowKey(id)}
			default:
				keys = []core.RatingKey{core.MovieKey(id), core.ShowKey(id)}
			}
			for _, k := range keys {
				t, e := s.o.Catalog.Get(ctx, k)
				if e != nil && !errors.Is(e, core.ErrNotFound) {
					return nil, e
				}
				if e == nil && t != nil && (t.Type == core.TypeMovie || t.Type == core.TypeShow) {
					return t, nil
				}
			}
		case "imdb", "tvdb":
			var t *core.Title
			var e error
			if hasTyped && (typ == core.TypeMovie || typ == core.TypeShow) {
				t, e = typedCat.ByExternalIDForType(ctx, ref.source, ref.id, typ)
				if errors.Is(e, core.ErrNotFound) && ref.source == "imdb" {
					t, e = s.o.Catalog.ByExternalID(ctx, ref.source, ref.id)
				}
			} else {
				t, e = s.o.Catalog.ByExternalID(ctx, ref.source, ref.id)
			}
			if e != nil && !errors.Is(e, core.ErrNotFound) {
				return nil, e
			}
			if e == nil && t != nil && (t.Type == core.TypeMovie || t.Type == core.TypeShow) {
				return t, nil
			}
		}
	}
	return nil, nil
}

func rssItemMediaType(it rssItem) core.MediaType {
	for _, c := range it.Categories {
		switch strings.ToLower(strings.TrimSpace(c.Value)) {
		case "movie", "movies", "film":
			return core.TypeMovie
		case "show", "shows", "tv", "series":
			return core.TypeShow
		}
	}
	for _, raw := range append(append([]string(nil), it.Links...), it.Guids...) {
		low := strings.ToLower(raw)
		switch {
		case strings.Contains(low, "/movie/") || strings.HasPrefix(low, "plex://movie/"):
			return core.TypeMovie
		case strings.Contains(low, "/show/") || strings.Contains(low, "/tv/") || strings.Contains(low, "/series/") || strings.HasPrefix(low, "plex://show/"):
			return core.TypeShow
		}
	}
	return ""
}

func extractRSSExternalRefs(it rssItem, fallbackType core.MediaType) []externalRef {
	var out []externalRef
	seen := map[string]bool{}
	add := func(source, id string, typ core.MediaType) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if source == "imdb" {
			id = strings.ToLower(id)
			if !strings.HasPrefix(id, "tt") || len(id) < 3 {
				return
			}
			for _, ch := range id[2:] {
				if ch < '0' || ch > '9' {
					return
				}
			}
		} else {
			for _, ch := range id {
				if ch < '0' || ch > '9' {
					return
				}
			}
		}
		key := source + ":" + id + ":" + string(typ)
		if !seen[key] {
			seen[key] = true
			out = append(out, externalRef{source: source, id: id, typ: typ})
		}
	}

	parseOne := func(raw string) {
		s := strings.TrimSpace(raw)
		if s == "" {
			return
		}
		low := strings.ToLower(s)
		switch {
		case strings.HasPrefix(low, "imdb://"), strings.HasPrefix(low, "imdb:"):
			id := strings.TrimPrefix(strings.TrimPrefix(low, "imdb://"), "imdb:")
			add("imdb", strings.Trim(id, "/"), fallbackType)
			return
		case strings.HasPrefix(low, "tmdb://"), strings.HasPrefix(low, "tmdb:"), strings.HasPrefix(low, "themoviedb://"):
			rest := low
			for _, p := range []string{"themoviedb://", "tmdb://", "tmdb:"} {
				rest = strings.TrimPrefix(rest, p)
			}
			rest = strings.Trim(rest, "/")
			switch {
			case strings.HasPrefix(rest, "movie/"):
				add("tmdb", strings.TrimPrefix(rest, "movie/"), core.TypeMovie)
			case strings.HasPrefix(rest, "show/"):
				add("tmdb", strings.TrimPrefix(rest, "show/"), core.TypeShow)
			case strings.HasPrefix(rest, "tv/"):
				add("tmdb", strings.TrimPrefix(rest, "tv/"), core.TypeShow)
			default:
				add("tmdb", rest, fallbackType)
			}
			return
		case strings.HasPrefix(low, "tvdb://"), strings.HasPrefix(low, "tvdb:"), strings.HasPrefix(low, "thetvdb://"):
			rest := low
			for _, p := range []string{"thetvdb://", "tvdb://", "tvdb:"} {
				rest = strings.TrimPrefix(rest, p)
			}
			rest = strings.Trim(rest, "/")
			switch {
			case strings.HasPrefix(rest, "movie/"), strings.HasPrefix(rest, "movies/"):
				add("tvdb", rest[strings.IndexByte(rest, '/')+1:], core.TypeMovie)
			case strings.HasPrefix(rest, "show/"), strings.HasPrefix(rest, "series/"):
				add("tvdb", rest[strings.IndexByte(rest, '/')+1:], core.TypeShow)
			default:
				add("tvdb", rest, fallbackType)
			}
			return
		}

		u, err := url.Parse(s)
		if err != nil {
			return
		}
		host := strings.ToLower(u.Hostname())
		segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		switch {
		case host == "imdb.com" || strings.HasSuffix(host, ".imdb.com"):
			for i := 0; i+1 < len(segs); i++ {
				if strings.EqualFold(segs[i], "title") {
					add("imdb", segs[i+1], fallbackType)
				}
			}
		case host == "themoviedb.org" || strings.HasSuffix(host, ".themoviedb.org"):
			for i := 0; i+1 < len(segs); i++ {
				num := leadingDigits(segs[i+1])
				switch strings.ToLower(segs[i]) {
				case "movie":
					add("tmdb", num, core.TypeMovie)
				case "tv":
					add("tmdb", num, core.TypeShow)
				}
			}
		case host == "thetvdb.com" || strings.HasSuffix(host, ".thetvdb.com"):
			for i := 0; i+1 < len(segs); i++ {
				num := leadingDigits(segs[i+1])
				switch strings.ToLower(segs[i]) {
				case "series", "shows":
					add("tvdb", num, core.TypeShow)
				case "movies", "movie":
					add("tvdb", num, core.TypeMovie)
				}
			}
			if qid := u.Query().Get("id"); qid != "" {
				add("tvdb", qid, fallbackType)
			}
		}
		q := u.Query()
		if v := q.Get("imdb"); v != "" {
			add("imdb", v, fallbackType)
		}
		if v := q.Get("tmdb"); v != "" {
			add("tmdb", v, fallbackType)
		}
		if v := q.Get("tvdb"); v != "" {
			add("tvdb", v, fallbackType)
		}
	}

	for _, g := range it.Guids {
		parseOne(g)
	}
	for _, l := range it.Links {
		parseOne(l)
	}
	return out
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// replaceFeedSnapshot atomically reconciles a feed's watchlist_items rows for
// every viewer, removing items only when they came from this feed source, and
// pushes newly added titles to a viewer's linked Trakt account when available.
func (s *Service) replaceFeedSnapshot(ctx context.Context, source string, us []core.User, byUser map[int64][]core.Title) (int, error) {
	usersByID := make(map[int64]*core.User, len(us))
	userIDSet := make(map[int64]bool, len(us))
	for i := range us {
		u := &us[i]
		usersByID[u.ID] = u
		if len(byUser[u.ID]) > 0 {
			userIDSet[u.ID] = true
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	existingRows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id FROM watchlist_items WHERE source=?`, source)
	if err != nil {
		return 0, err
	}
	for existingRows.Next() {
		var uid int64
		if err := existingRows.Scan(&uid); err != nil {
			existingRows.Close()
			return 0, err
		}
		userIDSet[uid] = true
	}
	if err := existingRows.Err(); err != nil {
		existingRows.Close()
		return 0, err
	}
	existingRows.Close()

	uids := make([]int64, 0, len(userIDSet))
	for uid := range userIDSet {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })

	type traktPush struct {
		uid   int64
		title core.Title
	}
	var toPush []traktPush
	imported := 0
	changed := false
	now := s.o.Now().Unix()

	for _, uid := range uids {
		u := usersByID[uid]
		keys := map[core.RatingKey]bool{}
		if u != nil {
			for _, t := range byUser[uid] {
				if keys[t.Key] || !s.allowed(u, &t) {
					continue
				}
				keys[t.Key] = true
				imported++

				var existed bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM watchlist_items WHERE user_id=? AND item_key=?)`, uid, t.Key).Scan(&existed); err != nil {
					return 0, err
				}
				raw, err := json.Marshal(t)
				if err != nil {
					return 0, err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES(?,?,?,?,?) ON CONFLICT(user_id,item_key,source) DO UPDATE SET title=excluded.title`, uid, t.Key, source, string(raw), now); err != nil {
					return 0, err
				}
				changed = true
				if !existed {
					toPush = append(toPush, traktPush{uid: uid, title: t})
				}
			}
		}

		rows, err := tx.QueryContext(ctx, `SELECT item_key FROM watchlist_items WHERE user_id=? AND source=?`, uid, source)
		if err != nil {
			return 0, err
		}
		var removed []core.RatingKey
		for rows.Next() {
			var k core.RatingKey
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return 0, err
			}
			if !keys[k] {
				removed = append(removed, k)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()

		for _, k := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist_items WHERE user_id=? AND source=? AND item_key=?`, uid, source, k); err != nil {
				return 0, err
			}
			if err := forgetEpisodes(ctx, tx, uid, k); err != nil {
				return 0, err
			}
			changed = true
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if changed {
		s.gen.Add(1)
	}

	if writer, ok := s.o.Trakt.(traktWriter); ok && len(toPush) > 0 {
		for _, p := range toPush {
			if ctx.Err() != nil {
				break
			}
			pushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, pushErr := writer.SetWatchlist(pushCtx, p.uid, p.title, true)
			cancel()
			if pushErr != nil {
				s.o.Logger.Warn("watchlist rss trakt push failed", "user", p.uid, "item", p.title.Key, "err", pushErr)
			}
		}
	}
	return imported, nil
}

func (s *Service) clearFeedSource(ctx context.Context, source, metaKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT user_id,item_key FROM watchlist_items WHERE source=?`, source)
	if err != nil {
		return err
	}
	type pair struct {
		uid int64
		key core.RatingKey
	}
	var removed []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.uid, &p.key); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist_items WHERE source=?`, source); err != nil {
		return err
	}
	for _, p := range removed {
		if err := forgetEpisodes(ctx, tx, p.uid, p.key); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist_meta WHERE k=?`, metaKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(removed) > 0 {
		s.gen.Add(1)
	}
	return nil
}

func (s *Service) resetFeedMeta(ctx context.Context, metaKey string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM watchlist_meta WHERE k=?`, metaKey)
}

func (s *Service) readRSSMeta(ctx context.Context, metaKey string) rssMeta {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT v FROM watchlist_meta WHERE k=?`, metaKey).Scan(&raw); err != nil || raw == "" {
		return rssMeta{}
	}
	var m rssMeta
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func (s *Service) writeRSSMeta(ctx context.Context, metaKey string, m rssMeta) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO watchlist_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, metaKey, string(raw)); err != nil {
		s.o.Logger.Warn("watchlist: record rss status failed", "key", metaKey, "err", err)
	}
}

func (s *Service) feedStatus(ctx context.Context, feedURL, metaKey string) *RSSFeedStatus {
	if feedURL == "" {
		return nil
	}
	m := s.readRSSMeta(ctx, metaKey)
	if m.URLHash != "" && m.URLHash != sha256Hex(feedURL) {
		m = rssMeta{}
	}
	return &RSSFeedStatus{
		Configured:      true,
		LastRead:        m.LastRead,
		Imported:        m.Imported,
		SkippedFriends:  m.SkippedFriends,
		UnmatchedTitles: m.UnmatchedTitles,
		Problem:         m.Problem,
	}
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hashViewers(us []core.User) string {
	parts := make([]string, len(us))
	for i, u := range us {
		parts[i] = fmt.Sprintf("%d:%d:%s:%s:%s:%s:%t", u.ID, u.PlexID, strings.ToLower(u.UUID), strings.ToLower(u.Username), strings.ToLower(u.Email), strings.ToLower(u.Title), u.IsOwner)
	}
	sort.Strings(parts)
	return sha256Hex(strings.Join(parts, "|"))
}
