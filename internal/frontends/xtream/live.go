package xtream

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) channels(r *http.Request, l login) ([]frontend.LiveChannel, error) {
	cs, err := i.host.LiveTV.Channels(r.Context(), l.viewer)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(cs); start += 250 {
		refs := []string{}
		groups := []string{}
		seen := map[string]bool{}
		for _, c := range cs[start:min(start+250, len(cs))] {
			refs = append(refs, c.ID)
			if group := strings.TrimSpace(c.Group); group != "" && !seen[group] {
				groups = append(groups, group)
				seen[group] = true
			}
		}
		allocated, err := i.host.IDs.CompactBatch(r.Context(), "xtream", "channel", refs)
		if err != nil {
			return nil, err
		}
		for ref, id := range allocated {
			l.channels[ref] = id
		}
		if len(groups) > 0 {
			allocated, err := i.host.IDs.CompactBatch(r.Context(), "xtream", "live-group", groups)
			if err != nil {
				return nil, err
			}
			for ref, id := range allocated {
				if id <= 0 || id > 2147483645 {
					return nil, fmt.Errorf("invalid live group identity")
				}
				l.groups[ref] = id + 2 // preserve the existing ungrouped categories 1 and 2
			}
		}
	}
	return cs, nil
}
func liveCategory(l login, c frontend.LiveChannel) string {
	if id := l.groups[strings.TrimSpace(c.Group)]; id > 0 {
		return strconv.FormatInt(int64(id), 10)
	}
	if c.Kind == "iptv" {
		return "2"
	}
	return "1"
}
func (i *instance) live(w http.ResponseWriter, r *http.Request, l login, action string) {
	channels, err := i.channels(r, l)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	if action == "get_live_categories" {
		out := []map[string]any{}
		seen := map[string]bool{}
		for _, c := range channels {
			id := liveCategory(l, c)
			if !seen[id] {
				seen[id] = true
				name := "On Air"
				if id == "2" {
					name = "IPTV"
				}
				if group := strings.TrimSpace(c.Group); group != "" {
					name = group
				}
				out = append(out, map[string]any{"category_id": id, "category_name": name, "parent_id": 0})
			}
		}
		jsonResponse(w, out)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte("["))
	first := true
	num := 0
	for _, c := range channels {
		if cat := r.URL.Query().Get("category_id"); cat != "" && cat != "0" && cat != liveCategory(l, c) {
			continue
		}
		if !first {
			_, _ = w.Write([]byte(","))
		}
		first = false
		num++
		_ = jsonEncoder(w, map[string]any{"num": num, "name": c.Name, "stream_type": "live", "stream_id": l.channels[c.ID], "stream_icon": i.image(r, l, 0, c.ID, c.Images), "epg_channel_id": c.ID, "added": "0", "category_id": liveCategory(l, c), "custom_sid": "", "tv_archive": 0, "tv_archive_duration": 0, "direct_source": ""})
	}
	_, _ = w.Write([]byte("]"))
}
func (i *instance) findChannel(r *http.Request, l login, id string) (frontend.LiveChannel, bool) {
	cs, err := i.channels(r, l)
	if err != nil {
		return frontend.LiveChannel{}, false
	}
	for _, c := range cs {
		if strconv.FormatInt(int64(l.channels[c.ID]), 10) == id {
			return c, true
		}
	}
	return frontend.LiveChannel{}, false
}
func (i *instance) programmes(r *http.Request, l login, c frontend.LiveChannel, limit int) ([]frontend.LiveProgramme, error) {
	now := time.Now().UTC()
	return i.host.LiveTV.Guide(r.Context(), l.viewer, frontend.LiveGuideQuery{Device: l.device, Channels: []string{c.ID}, From: now.Add(-6 * time.Hour), Until: now.Add(48 * time.Hour), Limit: limit})
}
func (i *instance) epg(w http.ResponseWriter, r *http.Request, l login, action string) {
	c, ok := i.findChannel(r, l, r.URL.Query().Get("stream_id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ps, err := i.programmes(r, l, c, 10000)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	limit := 10000
	if action == "get_short_epg" {
		limit = 4
		if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 {
			limit = min(n, 1000)
		}
	}
	out := []map[string]any{}
	now := time.Now()
	for _, p := range ps {
		if p.ChannelID != c.ID || !p.End.After(p.Start) || action == "get_short_epg" && !p.End.After(now) {
			continue
		}
		playing := 0
		if !p.Start.After(now) && p.End.After(now) {
			playing = 1
		}
		out = append(out, map[string]any{"id": p.ID, "epg_id": strconv.FormatInt(int64(l.channels[c.ID]), 10), "channel_id": c.ID, "title": base64.StdEncoding.EncodeToString([]byte(p.Title)), "description": base64.StdEncoding.EncodeToString([]byte(p.Summary)), "lang": "", "start": p.Start.UTC().Format("2006-01-02 15:04:05"), "end": p.End.UTC().Format("2006-01-02 15:04:05"), "start_timestamp": strconv.FormatInt(p.Start.Unix(), 10), "stop_timestamp": strconv.FormatInt(p.End.Unix(), 10), "now_playing": playing, "has_archive": 0})
		if len(out) >= limit {
			break
		}
	}
	jsonResponse(w, map[string]any{"epg_listings": out})
}
func (i *instance) xmltv(w http.ResponseWriter, r *http.Request, l login) {
	cs, err := i.channels(r, l)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header + "<tv>"))
	e := xml.NewEncoder(w)
	for _, c := range cs {
		_ = e.Encode(struct {
			XMLName xml.Name `xml:"channel"`
			ID      string   `xml:"id,attr"`
			Name    string   `xml:"display-name"`
		}{ID: c.ID, Name: c.Name})
	}
	for _, c := range cs {
		ps, err := i.programmes(r, l, c, 10000)
		if err != nil {
			return
		}
		for _, p := range ps {
			if p.ChannelID != c.ID || !p.End.After(p.Start) {
				continue
			}
			_ = e.Encode(struct {
				XMLName xml.Name `xml:"programme"`
				Channel string   `xml:"channel,attr"`
				Start   string   `xml:"start,attr"`
				Stop    string   `xml:"stop,attr"`
				Title   string   `xml:"title"`
				Desc    string   `xml:"desc"`
			}{Channel: c.ID, Start: p.Start.UTC().Format("20060102150405 -0700"), Stop: p.End.UTC().Format("20060102150405 -0700"), Title: p.Title, Desc: p.Summary})
		}
	}
	_ = e.Flush()
	_, _ = w.Write([]byte("</tv>"))
}
func cleanM3U(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\"", "'").Replace(s)
}
func (i *instance) playlist(w http.ResponseWriter, r *http.Request, l login) {
	base := i.base(r)
	if base == "" {
		http.Error(w, "Unavailable", 503)
		return
	}
	cs, err := i.channels(r, l)
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = fmt.Fprintln(w, "#EXTM3U")
	for _, c := range cs {
		group := strings.TrimSpace(c.Group)
		if group == "" {
			group = "Live TV"
		}
		_, _ = fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n%s/live/%s/%s/%d.m3u8\n", cleanM3U(c.ID), cleanM3U(i.image(r, l, 0, c.ID, c.Images)), cleanM3U(group), cleanM3U(c.Name), base, l.username, l.password, l.channels[c.ID])
	}
	// Movies are emitted a page at a time; exporting never accumulates the catalogue.
	if err := i.walk(r, l, "movie", func(l login, t frontend.Title, cat string) error {
		_, err := fmt.Fprintf(w, "#EXTINF:-1 group-title=\"Movies\",%s\n%s/movie/%s/%s/%d.mkv\n", cleanM3U(t.Title), base, l.username, l.password, l.ids[t.Key])
		return err
	}); err != nil {
		return
	}
	_ = i.walk(r, l, "show", func(_ login, show frontend.Title, _ string) error {
		return i.children(r, l, show.Key, func(season frontend.Title) error {
			if string(season.Type) != "season" {
				return nil
			}
			return i.children(r, l, season.Key, func(ep frontend.Title) error {
				if string(ep.Type) != "episode" {
					return nil
				}
				_, err := fmt.Fprintf(w, "#EXTINF:-1 group-title=\"Series\",%s S%02dE%02d %s\n%s/series/%s/%s/%d.mkv\n", cleanM3U(show.Title), ep.Season, ep.Episode, cleanM3U(ep.Title), base, l.username, l.password, l.ids[ep.Key])
				return err
			})
		})
	})
}
