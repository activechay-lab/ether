package artwork

import (
	"context"
	"hash/fnv"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"golang.org/x/image/font/sfnt"
)

// TitlePoster describes Ether's generated poster for a movie or show that
// has no artwork anywhere: a typographic poster in a colour picked from the
// title's key, so the same title always gets the same image. Every frontend
// serves it in place of a missing poster.
type TitlePoster struct {
	Key           string // rating key; picks the accent and keys the cache
	Title         string
	OriginalTitle string
	Year          int
	Show          bool
	Width         int
}

// generatedAccents are rich, mid-dark tones white type reads well on.
var generatedAccents = []string{
	"#2b4c7e", "#7a2e3a", "#2f6b5a", "#5b3f7d", "#8a5a1f",
	"#245f73", "#6e3b5c", "#3e5c2a", "#7d3f25", "#38406f",
}

// Request is the collection-poster request that draws p. Its fields are
// everything that affects the pixels, so the renderer's disk cache keys on
// them.
func (p TitlePoster) Request() PosterRequest {
	h := fnv.New32a()
	_, _ = h.Write([]byte(p.Key))
	kind := "FILM"
	if p.Show {
		kind = "SERIES"
	}
	subtitle := kind
	if p.Year > 0 {
		subtitle += " · " + strconv.Itoa(p.Year)
	}
	title := drawableTitle(p.Title, p.OriginalTitle)
	if title == "" {
		// Nothing the bundled fonts can set (a CJK or Thai original name
		// with no localised one): the year carries the poster.
		title, subtitle = kind, ""
		if p.Year > 0 {
			title = strconv.Itoa(p.Year)
			subtitle = kind
		}
	}
	return PosterRequest{
		Title:    title,
		Subtitle: subtitle,
		Mode:     "type",
		Accent:   generatedAccents[h.Sum32()%uint32(len(generatedAccents))],
		Width:    p.Width,
	}
}

// Stamp identifies the generated image for p: it changes when anything
// drawn changes, so a frontend URL built from it is stable while the
// poster is, and a client cache never keeps a generated poster after the
// title gains real artwork (that URL carries the real poster's stamp).
func (p TitlePoster) Stamp() string {
	r := p.Request()
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.Title + "\x00" + r.Subtitle + "\x00" + r.Accent))
	return "g" + strconv.FormatUint(h.Sum64(), 36)
}

// Title renders p (JPEG), disk-cached like collection posters.
func (r *Renderer) Title(ctx context.Context, p TitlePoster) ([]byte, string, error) {
	return r.Poster(ctx, p.Request())
}

// drawableTitle picks the first name whose every letter the bundled fonts
// can draw.
func drawableTitle(names ...string) string {
	bold, _, err := fonts()
	if err != nil {
		return ""
	}
	var buf sfnt.Buffer
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		ok := true
		for _, r := range name {
			if unicode.IsSpace(r) || !unicode.IsGraphic(r) {
				continue
			}
			if i, err := bold.GlyphIndex(&buf, r); err != nil || i == 0 {
				ok = false
				break
			}
		}
		if ok {
			return name
		}
	}
	return ""
}

// ForTitle is Ether's poster for a movie or show with no artwork anywhere.
// Seasons and episodes fall back to their show's poster instead, so they
// have none of their own.
func ForTitle(t *core.Title) (TitlePoster, bool) {
	if !core.NeedsGeneratedPoster(t) {
		return TitlePoster{}, false
	}
	year := t.Year
	if year == 0 && !t.Released.IsZero() {
		year = t.Released.Year()
	}
	return TitlePoster{Key: t.Key.String(), Title: t.Title, OriginalTitle: t.OriginalTitle, Year: year, Show: t.Type == core.TypeShow}, true
}

// maxQueryTitle bounds the names a self-describing poster URL carries.
const maxQueryTitle = 200

// Query encodes p for a URL whose request fully describes the image (the
// owner console's image route): the same title always yields the same URL.
// The key says movie or show.
func (p TitlePoster) Query() url.Values {
	q := url.Values{"k": {p.Key}, "t": {p.Title}}
	if p.OriginalTitle != "" && p.OriginalTitle != p.Title {
		q.Set("o", p.OriginalTitle)
	}
	if p.Year > 0 {
		q.Set("y", strconv.Itoa(p.Year))
	}
	return q
}

// TitlePosterFromQuery is Query's inverse; ok is false for a request no
// Query call could have made.
func TitlePosterFromQuery(q url.Values) (TitlePoster, bool) {
	p := TitlePoster{Key: q.Get("k"), Title: q.Get("t"), OriginalTitle: q.Get("o")}
	if y := q.Get("y"); y != "" {
		n, err := strconv.Atoi(y)
		if err != nil || n < 1800 || n > 2200 {
			return TitlePoster{}, false
		}
		p.Year = n
	}
	key, err := core.ParseRatingKey(p.Key)
	if err != nil || (key.Kind() != core.KindMovie && key.Kind() != core.KindShow) || len(p.Title) > maxQueryTitle || len(p.OriginalTitle) > maxQueryTitle {
		return TitlePoster{}, false
	}
	p.Show = key.Kind() == core.KindShow
	return p, true
}
