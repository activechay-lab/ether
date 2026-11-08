package perfharness

import (
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Detail rows follow LongTailShape (longtail.go), the counts model fitted
// to the lab mirror that the long-tail generator also mirrors through the
// real catalogue; the fixture writes the rows it describes directly. A
// long-running series credits up to 1,400 people.

// shape draws one title's detail counts.
func (g *generator) shape(typ core.MediaType) Shape {
	if typ == core.TypeEpisode {
		// An episode's guest cast.
		return Shape{Type: typ, Cast: poisson(g.rng, 4)}
	}
	return LongTailShape(g.rng, typ)
}

// People are dealt to credits by preferential attachment (a Yule-Simon
// process): a credit names a new person with probability 0.36, otherwise
// the person of an earlier credit picked at random, so people are chosen in
// proportion to the credits they already have. That gives the lab's 61% of
// people with one credit and 2.8 credits per person; maxPersonCredits stands
// in for the length of a career.
const (
	newPersonChance  = 0.36
	maxPersonCredits = 400
)

type personDeck struct {
	dealt   []int32  // every credit's person, for the attachment draw
	credits []uint16 // credits per person; person ids are indexes from 1
}

func (d *personDeck) next(rng *rand.Rand, used map[int32]bool) int32 {
	if len(d.dealt) > 0 && rng.Float64() >= newPersonChance {
		for range 4 {
			p := d.dealt[rng.IntN(len(d.dealt))]
			if !used[p] && d.credits[p] < maxPersonCredits {
				d.credits[p]++
				d.dealt = append(d.dealt, p)
				return p
			}
		}
	}
	if len(d.credits) == 0 {
		d.credits = append(d.credits, 0) // id 0 is no one
	}
	p := int32(len(d.credits))
	d.credits = append(d.credits, 1)
	d.dealt = append(d.dealt, p)
	return p
}

// splitmix scrambles an id, so names derived from it look random but cost
// no memory.
func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// madeUpWord is a word of two or three syllables drawn from h.
func madeUpWord(h uint64) string {
	var b strings.Builder
	n := 2 + int(h%2)
	h /= 2
	for range n {
		b.WriteString(fixtureSyllables[h%uint64(len(fixtureSyllables))])
		h /= uint64(len(fixtureSyllables))
	}
	w := b.String()
	return strings.ToUpper(w[:1]) + w[1:]
}

// personName is person p's name. One person in two hundred carries a
// common English first name, so a people search matches sparsely.
func personName(p int32) string {
	h := splitmix(uint64(p))
	first := madeUpWord(h)
	if h%200 == 0 {
		first = fixtureWords[(h>>8)%uint64(len(fixtureWords))]
	}
	return first + " " + madeUpWord(splitmix(h))
}

// poolWord is entry i of a made-up vocabulary (keywords, studios), lower
// case for keywords as TMDB sends them.
func poolWord(salt string, i int) string {
	h := splitmix(uint64(i)*0x100000001b3 ^ uint64(len(salt)))
	return madeUpWord(h) + " " + strings.ToLower(madeUpWord(splitmix(h)))
}


// details queues key's detail rows.
func (g *generator) details(key core.RatingKey, s Shape, title string) error {
	k := int64(key)
	if err := g.facets(key, s); err != nil {
		return err
	}
	clear(g.used)
	writers := []string{"Screenplay", "Writer", "Story", "Novel"}
	crew := []string{"Original Music Composer", "Director of Photography"}
	if s.Type == core.TypeShow {
		writers, crew = []string{"Writer", "Teleplay", "Story"}, []string{"Creator", "Original Music Composer", "Director of Photography"}
	}
	for _, group := range []struct {
		kind  string
		n     int
		roles []string
	}{{"cast", min(s.Cast, 30), nil}, {"director", s.Directors, []string{"Director"}}, {"writer", s.Writers, writers}, {"crew", s.Crew, crew}} {
		for ord := range group.n {
			p := g.people.next(g.rng, g.used)
			g.used[p] = true
			var role string
			if group.roles == nil {
				role = madeUpWord(splitmix(uint64(p) ^ uint64(k)))
				if g.rng.IntN(8) == 0 {
					role += " (voice)"
				}
			} else {
				role = group.roles[g.rng.IntN(len(group.roles))]
			}
			thumb := ""
			if p%2 == 0 {
				thumb = "/person" + strconv.Itoa(int(p)) + ".jpg"
			}
			if err := g.insert("catalog_credits", "key,person_tmdb,name,role,kind,ord,thumb", k, int64(p), personName(p), role, group.kind, ord, thumb); err != nil {
				return err
			}
		}
	}
	if s.Type == core.TypeEpisode {
		return nil
	}
	seen := map[string]bool{}
	for range s.Keywords {
		kw := strings.ToLower(poolWord("keyword", int(g.keywordPool.Uint64())))
		if seen[kw] {
			continue
		}
		seen[kw] = true
		if err := g.insert("catalog_keywords", "key,keyword", k, kw); err != nil {
			return err
		}
	}
	clear(seen)
	for range s.Studios {
		st := poolWord("studio", int(g.studioPool.Uint64()))
		if seen[st] {
			continue
		}
		seen[st] = true
		if err := g.insert("catalog_studios", "key,studio", k, st); err != nil {
			return err
		}
	}
	clear(seen)
	for range s.AltTitles {
		alt := g.word() + " " + g.word()
		if g.rng.IntN(3) == 0 {
			alt = title + ": " + g.word()
		}
		if seen[alt] {
			continue
		}
		seen[alt] = true
		if err := g.insert("catalog_alt_titles", "key,title", k, alt); err != nil {
			return err
		}
	}
	return g.crowd(key, s)
}

// crowd stores the title's TMDB recommendations: keys of titles of the same
// type, as catalog/crowd.go keeps them.
func (g *generator) crowd(key core.RatingKey, s Shape) error {
	pool := g.movies
	if s.Type == core.TypeShow {
		pool = g.shows
	}
	if s.CrowdRecs == 0 {
		return nil
	}
	var recs []string
	for range min(s.CrowdRecs, len(pool)) {
		recs = append(recs, strconv.FormatInt(int64(pool[g.rng.IntN(len(pool))]), 10))
	}
	return g.insert("catalog_crowd", "key,fetched_at,pages,recs", int64(key), g.now.Unix(), 1, strings.Join(recs, ","))
}

func (g *generator) facets(key core.RatingKey, s Shape) error {
	first := g.rng.IntN(len(fixtureGenres))
	for i := range min(s.Genres, 3) {
		genre := fixtureGenres[(first+i*(1+first%5))%len(fixtureGenres)]
		if err := g.insert("catalog_genres", "key,genre", int64(key), genre); err != nil {
			return err
		}
	}
	countries := []string{"US", "GB", "CA", "FR", "JP", "KR", "DE"}
	c := g.rng.IntN(len(countries))
	for i := range min(s.Countries, len(countries)) {
		if err := g.insert("catalog_countries", "key,country", int64(key), countries[(c+i)%len(countries)]); err != nil {
			return err
		}
	}
	return nil
}
