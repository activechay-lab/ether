package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Xtream Codes panels are read through their JSON API (player_api.php), the
// way dedicated IPTV apps sign in: the account check, the live categories
// and the live streams. The panel's get.php M3U export also carries every
// movie and series episode (often ten times the live lineup) and is the
// endpoint strict panels throttle first, so Ether never downloads it.
//
// Panels disagree on JSON types (numbers arrive as strings, ints, bools or
// null), so every scalar is read through xtValue.

// maxXtreamBytes caps one API document; get_live_streams for a very large
// lineup is a few megabytes.
const maxXtreamBytes = 64 << 20

type xtValue string

func (v *xtValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		*v = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = xtValue(strings.TrimSpace(s))
	case string(b) == "true":
		*v = "1"
	case string(b) == "false":
		*v = "0"
	case b[0] == '[' || b[0] == '{':
		*v = ""
	default:
		*v = xtValue(b)
	}
	return nil
}

func (v xtValue) int() int {
	n, err := strconv.ParseFloat(string(v), 64)
	if err != nil {
		return 0
	}
	return int(n)
}

func (v xtValue) truthy() bool { return v != "" && v != "0" && !strings.EqualFold(string(v), "false") }

type xtreamLogin struct {
	UserInfo *struct {
		Auth           xtValue  `json:"auth"`
		Status         string   `json:"status"`
		Message        string   `json:"message"`
		ExpDate        xtValue  `json:"exp_date"`
		IsTrial        xtValue  `json:"is_trial"`
		ActiveCons     xtValue  `json:"active_cons"`
		MaxConnections xtValue  `json:"max_connections"`
		Formats        []string `json:"allowed_output_formats"`
	} `json:"user_info"`
}

type xtreamCategory struct {
	ID   xtValue `json:"category_id"`
	Name string  `json:"category_name"`
}

type xtreamStream struct {
	Num        xtValue `json:"num"`
	Name       string  `json:"name"`
	Type       string  `json:"stream_type"`
	StreamID   xtValue `json:"stream_id"`
	Icon       string  `json:"stream_icon"`
	EPGID      string  `json:"epg_channel_id"`
	Adult      xtValue `json:"is_adult"`
	CategoryID xtValue `json:"category_id"`
}

// xtreamAPI is one panel login.
type xtreamAPI struct {
	s    *Service
	auth *SourceAuth
	base string
}

func newXtreamAPI(s *Service, a *SourceAuth) xtreamAPI {
	return xtreamAPI{s: s, auth: a, base: strings.TrimRight(strings.TrimSpace(a.Server), "/")}
}

func (x xtreamAPI) url(action string) string {
	q := url.Values{"username": {x.auth.Username}, "password": {x.auth.Password}}
	if action != "" {
		q.Set("action", action)
	}
	return x.base + "/player_api.php?" + q.Encode()
}

func (x xtreamAPI) get(ctx context.Context, action string, into any) error {
	if progress := importProgress(ctx); progress != nil {
		progress.setPhase("playlist")
	}
	rc, err := x.s.openURL(ctx, x.url(action), x.auth.header())
	if err != nil {
		return fmt.Errorf("Xtream %s: %w", xtreamWhat(action), err)
	}
	defer rc.Close()
	b, err := readLimited(rc, maxXtreamBytes, "playlist")
	if err != nil {
		return sourceReadFailure(err, "channel list")
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("the Xtream panel sent an unreadable %s reply", xtreamWhat(action))
	}
	return nil
}

func xtreamWhat(action string) string {
	switch action {
	case "get_live_categories":
		return "category list"
	case "get_live_streams":
		return "channel list"
	}
	return "account"
}

// account signs in and reports the login's state. A login the panel
// refuses, or one that is no longer active, stops the import so the stored
// lineup is kept.
func (x xtreamAPI) account(ctx context.Context, now time.Time) (*ProviderAccount, error) {
	var login xtreamLogin
	if err := x.get(ctx, "", &login); err != nil {
		return nil, err
	}
	u := login.UserInfo
	if u == nil || !u.Auth.truthy() {
		return nil, fmt.Errorf("the Xtream panel refused this username and password")
	}
	acct := &ProviderAccount{
		Status: strings.TrimSpace(u.Status), Trial: u.IsTrial.truthy(),
		MaxConnections: max(u.MaxConnections.int(), 0), ActiveConnections: max(u.ActiveCons.int(), 0),
		CheckedAt: now.UTC(),
	}
	for _, f := range u.Formats {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			acct.Formats = append(acct.Formats, f)
		}
	}
	if exp := u.ExpDate.int(); exp > 0 {
		t := time.Unix(int64(exp), 0).UTC()
		acct.ExpiresAt = &t
	}
	if acct.Status != "" && !strings.EqualFold(acct.Status, "active") {
		msg := strings.TrimSpace(u.Message)
		if msg != "" {
			return acct, fmt.Errorf("the Xtream account is %s: %s", strings.ToLower(acct.Status), msg)
		}
		return acct, fmt.Errorf("the Xtream account is %s", strings.ToLower(acct.Status))
	}
	return acct, nil
}

// xtreamOutput is the stream container Ether asks the panel for: the
// owner's choice when the panel allows it, otherwise MPEG-TS, otherwise
// whatever the panel offers.
func xtreamOutput(want string, allowed []string) string {
	if want == "" {
		want = "ts"
	}
	if len(allowed) == 0 || slices.Contains(allowed, want) {
		return want
	}
	for _, f := range []string{"ts", "m3u8"} {
		if slices.Contains(allowed, f) {
			return f
		}
	}
	return want
}

var trailingStreamID = regexp.MustCompile(`/(\d+)(?:\.[A-Za-z0-9]{1,8})?$`)

// streamIDOf extracts the panel stream id from an Xtream live URL, in both
// the /live/u/p/<id>.<ext> and the M3U export's /u/p/<id> forms.
func streamIDOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if m := trailingStreamID.FindStringSubmatch(u.Path); m != nil {
		return m[1]
	}
	return ""
}

// fetchXtream reads the live lineup and the panel's XMLTV guide.
func fetchXtream(ctx context.Context, s *Service, src *Source) (Fetched, error) {
	if src.Auth == nil || src.Auth.Type != "xtream" {
		return Fetched{}, fmt.Errorf("source is not an Xtream login")
	}
	x := newXtreamAPI(s, src.Auth)
	acct, err := x.account(ctx, s.o.Now())
	if err != nil {
		return Fetched{}, err
	}
	var cats []xtreamCategory
	if err := x.get(ctx, "get_live_categories", &cats); err != nil {
		return Fetched{}, err
	}
	var streams []xtreamStream
	if err := x.get(ctx, "get_live_streams", &streams); err != nil {
		return Fetched{}, err
	}
	old, err := s.sourceStreamIDs(ctx, src.ID)
	if err != nil {
		return Fetched{}, err
	}
	cs := xtreamChannels(src.ID, x.base, src.Auth, xtreamOutput(src.Auth.Output, acct.Formats), cats, streams, old)
	src.Account = acct
	src.Playlist = ""
	cs = s.applyGroupRules(ctx, src, cs)
	g, err := fetchSourceGuide(ctx, s, src, cs, true)
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{Channels: cs, Guide: g, ProviderNumbers: true}, nil
}

// xtreamChannels builds live channels in the panel's category order, each
// numbered by the panel. old maps a stream id to the channel id an earlier
// import gave it, so owner settings follow the stream (including channels
// first imported from the panel's M3U export).
func xtreamChannels(sourceID, base string, a *SourceAuth, output string, cats []xtreamCategory, streams []xtreamStream, old map[string]string) []Channel {
	names := make(map[string]string, len(cats))
	rank := make(map[string]int, len(cats))
	for i, c := range cats {
		id := string(c.ID)
		names[id] = strings.TrimSpace(c.Name)
		if _, ok := rank[id]; !ok {
			rank[id] = i
		}
	}
	type entry struct {
		c    Channel
		rank int
		num  int
		pos  int
	}
	entries := make([]entry, 0, len(streams))
	seen := make(map[string]bool, len(streams))
	for i, st := range streams {
		sid := string(st.StreamID)
		if st.Type != "" && st.Type != "live" && st.Type != "created_live" && st.Type != "radio_streams" || sid == "" || seen[sid] {
			continue
		}
		seen[sid] = true
		id := old[sid]
		if id == "" {
			id = stableID(sourceID, "xtream", sid)
		}
		streamURL := base + "/live/" + url.PathEscape(a.Username) + "/" + url.PathEscape(a.Password) + "/" + url.PathEscape(sid) + "." + output
		c := Channel{
			ID: id, Name: strings.TrimSpace(st.Name), Logo: strings.TrimSpace(st.Icon), GuideID: strings.TrimSpace(st.EPGID),
			Group: names[string(st.CategoryID)], SourceID: sourceID, Kind: "iptv", Enabled: true, StreamURL: streamURL,
			adult: st.Adult.truthy(),
		}
		if c.Name == "" {
			c.Name = "Channel " + sid
		}
		c.StreamSources = []StreamSource{{ID: stableID(c.ID, "playlist"), Name: "Imported stream", URL: streamURL, Enabled: true}}
		r, ok := rank[string(st.CategoryID)]
		if !ok {
			r = len(cats)
		}
		entries = append(entries, entry{c: c, rank: r, num: st.Num.int(), pos: i})
	}
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if a.num != b.num {
			return a.num - b.num
		}
		return a.pos - b.pos
	})
	out := make([]Channel, len(entries))
	used := make(map[int]bool, len(entries))
	for _, e := range entries {
		if e.num > 0 {
			used[e.num] = true
		}
	}
	// A missing or repeated panel number takes the lowest number no stream
	// claims.
	assigned := make(map[int]bool, len(entries))
	next := 1
	for i, e := range entries {
		n := e.num
		if n <= 0 || assigned[n] {
			for used[next] || assigned[next] {
				next++
			}
			n = next
		}
		assigned[n] = true
		e.c.Number = strconv.Itoa(n)
		e.c.ProviderNumber = e.c.Number
		out[i] = e.c
	}
	return out
}

// sourceStreamIDs maps each stored channel of a source to its stream's
// panel id.
func (s *Service) sourceStreamIDs(ctx context.Context, sourceID string) (map[string]string, error) {
	cs, err := s.sourceChannels(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cs))
	for _, c := range cs {
		raw := c.StreamURL
		for _, in := range c.StreamSources {
			if in.ID == stableID(c.ID, "playlist") {
				raw = in.URL
			}
		}
		if sid := streamIDOf(raw); sid != "" {
			if _, dup := out[sid]; !dup {
				out[sid] = c.ID
			}
		}
	}
	return out, nil
}
