// Package feedparse turns RSS, Atom and JSON Feed documents into
// kind items: one feed is usually one root item with entries as leaves.
package feedparse

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	etherplugin "github.com/braedonsaunders/ether/sdk/go"
)

// Entry is one normalised feed entry.
type Entry struct {
	ID      string
	Title   string
	Summary string
	Link    string
	Audio   string // enclosure/media URL, when the entry carries one
	Image   string
	Date    time.Time
	Authors []string
}

// Feed is a normalised feed document.
type Feed struct {
	Title   string
	Summary string
	Link    string
	Image   string
	Entries []Entry
}

// Parse detects and parses RSS 2.0, Atom or JSON Feed.
func Parse(data []byte) (Feed, error) {
	trim := strings.TrimSpace(string(data))
	if strings.HasPrefix(trim, "{") {
		return parseJSONFeed(data)
	}
	var probe struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(data, &probe); err != nil {
		return Feed{}, err
	}
	switch probe.XMLName.Local {
	case "rss", "RDF":
		return parseRSS(data)
	case "feed":
		return parseAtom(data)
	}
	return Feed{}, fmt.Errorf("feedparse: unrecognised feed document %q", probe.XMLName.Local)
}

func parseRSS(data []byte) (Feed, error) {
	var doc struct {
		Channel struct {
			Title string `xml:"title"`
			Desc  string `xml:"description"`
			Link  string `xml:"link"`
			Image struct {
				URL string `xml:"url"`
			} `xml:"image"`
			Items []struct {
				Title string `xml:"title"`
				Desc  string `xml:"description"`
				Link  string `xml:"link"`
				GUID  string `xml:"guid"`
				Date  string `xml:"pubDate"`
				Enc   struct {
					URL string `xml:"url,attr"`
				} `xml:"enclosure"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return Feed{}, err
	}
	feed := Feed{Title: doc.Channel.Title, Summary: doc.Channel.Desc, Link: doc.Channel.Link, Image: doc.Channel.Image.URL}
	for _, it := range doc.Channel.Items {
		feed.Entries = append(feed.Entries, Entry{
			ID: firstNonEmpty(it.GUID, it.Link), Title: it.Title, Summary: it.Desc,
			Link: it.Link, Audio: it.Enc.URL, Date: parseDate(it.Date),
		})
	}
	return feed, nil
}

func parseAtom(data []byte) (Feed, error) {
	type link struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
		Type string `xml:"type,attr"`
	}
	var doc struct {
		Title   string `xml:"title"`
		Summary string `xml:"subtitle"`
		Links   []link `xml:"link"`
		Entries []struct {
			ID      string `xml:"id"`
			Title   string `xml:"title"`
			Summary string `xml:"summary"`
			Updated string `xml:"updated"`
			Links   []link `xml:"link"`
			Author  struct {
				Name string `xml:"name"`
			} `xml:"author"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return Feed{}, err
	}
	pick := func(ls []link) (page, media string) {
		for _, l := range ls {
			switch {
			case l.Rel == "enclosure":
				media = l.Href
			case l.Rel == "alternate" || l.Rel == "":
				page = firstNonEmpty(page, l.Href)
			}
		}
		return page, media
	}
	page, _ := pick(doc.Links)
	feed := Feed{Title: doc.Title, Summary: doc.Summary, Link: page}
	for _, e := range doc.Entries {
		p, m := pick(e.Links)
		ent := Entry{ID: firstNonEmpty(e.ID, p), Title: e.Title, Summary: e.Summary, Link: p, Audio: m, Date: parseDate(e.Updated)}
		if e.Author.Name != "" {
			ent.Authors = []string{e.Author.Name}
		}
		feed.Entries = append(feed.Entries, ent)
	}
	return feed, nil
}

func parseJSONFeed(data []byte) (Feed, error) {
	var doc struct {
		Title string `json:"title"`
		Desc  string `json:"description"`
		Home  string `json:"home_page_url"`
		Icon  string `json:"icon"`
		Items []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Summary string `json:"summary"`
			URL     string `json:"url"`
			Date    string `json:"date_published"`
			Image   string `json:"image"`
			Attach  []struct {
				URL string `json:"url"`
			} `json:"attachments"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Feed{}, err
	}
	feed := Feed{Title: doc.Title, Summary: doc.Desc, Link: doc.Home, Image: doc.Icon}
	for _, it := range doc.Items {
		ent := Entry{ID: firstNonEmpty(it.ID, it.URL), Title: it.Title, Summary: it.Summary, Link: it.URL, Image: it.Image, Date: parseDate(it.Date)}
		if len(it.Attach) > 0 {
			ent.Audio = it.Attach[0].URL
		}
		feed.Entries = append(feed.Entries, ent)
	}
	return feed, nil
}

// StableID turns a feed URL/id into a legal plugin external id.
func StableID(s string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("f%x", sha256.Sum256([]byte(s)))[:24]
}

// Items maps a feed onto a two-level kind: the feed as root, entries as
// playable leaves whose hints carry the link/media URLs for the resolver.
func (f Feed) Items(rootLevel, leafLevel, feedURL string) ([]etherplugin.KindItem, error) {
	rootID := StableID(firstNonEmpty(feedURL, f.Link, f.Title))
	root := etherplugin.KindItem{
		ID: rootID, Level: rootLevel, Title: f.Title, Summary: f.Summary,
		ChildCount: len(f.Entries), LeafCount: len(f.Entries),
	}
	if f.Image != "" {
		root.Art = []etherplugin.KindArtRef{{Role: "poster", URL: f.Image}}
	}
	out := []etherplugin.KindItem{root}
	for i, e := range f.Entries {
		hints, err := json.Marshal(map[string]string{"link": e.Link, "mediaUrl": e.Audio})
		if err != nil {
			return nil, err
		}
		item := etherplugin.KindItem{
			ID: StableID(firstNonEmpty(e.ID, e.Link, e.Title)), Level: leafLevel,
			ParentID: rootID, Index: i, Title: e.Title, Summary: e.Summary,
			Playable: e.Audio != "" || e.Link != "", Hints: hints,
		}
		if !e.Date.IsZero() {
			item.Released = e.Date.Unix()
			item.Year = e.Date.Year()
		}
		if e.Image != "" {
			item.Art = []etherplugin.KindArtRef{{Role: "poster", URL: e.Image}}
		}
		out = append(out, item)
	}
	return out, nil
}

func parseDate(s string) time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "2006-01-02 15:04:05 -0700"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
