package perfharness

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// A real catalogue is a long tail. Most TMDB titles are obscure (popularity
// near 1, a handful of votes, a few credits and no keywords); a few are
// famous (thousands of votes, a hundred credits, dozens of keywords and
// alternative titles); a long-running series credits up to 1,400 people.
// The shapes below follow the lab mirror (2026-11-07: 50k detailed titles,
// 1.08M credits, 389k people of whom 61% have one credit, 7 keywords per
// popular title from a 30k pool, 3.1 studios, 3.8 alternative titles, 2.0
// genres and 1.2 countries per title, 10% of movies in a franchise).

// titleShape is one title's detail: how popular it is and how many detail
// rows of each kind it carries.
type titleShape struct {
	popularity float64
	votes      int
	rating     float64
	credits    int
	keywords   int
	genres     int
	countries  int
	studios    int
	altTitles  int
	crowd      int
	collection int // a franchise id, 0 for none
}

// tier is how famous a title is: 2 for the head (about 1% of titles), 1 for
// the middle (about 11%), 0 for the tail.
func (s titleShape) tier() int {
	switch {
	case s.popularity >= 20:
		return 2
	case s.popularity >= 3:
		return 1
	}
	return 0
}

// popularity draws TMDB's long tail: median 1, one title in ten above 3.4,
// one in a hundred above 19, one in a thousand above 107.
func (g *generator) popularity() (float64, int, float64) {
	pop := min(3000, 0.6*math.Pow(1-g.rng.Float64(), -0.75))
	votes := int(math.Pow(pop, 1.6) * (0.5 + g.rng.Float64()) * 2)
	rating := 0.0
	if votes > 0 {
		rating = math.Round((3+g.rng.Float64()*6)*10) / 10
	}
	return pop, votes, rating
}

// between draws an int in [lo, hi].
func (g *generator) between(lo, hi int) int { return lo + g.rng.IntN(hi-lo+1) }

func (g *generator) titleShape(typ string) titleShape {
	var s titleShape
	s.popularity, s.votes, s.rating = g.popularity()
	t := s.tier()
	pick := func(tail, mid, head [2]int) int {
		r := [3][2]int{tail, mid, head}[t]
		return g.between(r[0], r[1])
	}
	s.genres = 1 + g.rng.IntN(2)
	if g.rng.IntN(4) == 0 {
		s.genres++
	}
	s.countries = 1
	if g.rng.IntN(5) == 0 {
		s.countries = 2
	}
	s.keywords = pick([2]int{0, 3}, [2]int{3, 14}, [2]int{10, 50})
	s.studios = pick([2]int{0, 2}, [2]int{1, 4}, [2]int{3, 10})
	s.altTitles = pick([2]int{0, 1}, [2]int{1, 6}, [2]int{5, 35})
	s.crowd = pick([2]int{0, 20}, [2]int{20, 20}, [2]int{20, 20})
	switch typ {
	case "movie":
		s.credits = pick([2]int{0, 10}, [2]int{10, 50}, [2]int{20, 130})
		if g.rng.IntN(20) == 0 {
			s.collection = 1 + g.rng.IntN(max(1, g.franchises))
		}
	case "show":
		s.credits = pick([2]int{0, 8}, [2]int{8, 38}, [2]int{20, 220})
		if t == 2 && g.rng.IntN(25) == 0 {
			// A soap or a long-running anthology.
			s.credits = g.between(600, 1400)
		}
	case "episode":
		s = titleShape{credits: g.rng.IntN(9)}
	}
	return s
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

var crewRoles = []string{"Composer", "Director of Photography", "Editor", "Producer", "Executive Producer", "Casting", "Production Design", "Costume Design"}

// details queues key's detail rows.
func (g *generator) details(key core.RatingKey, typ string, s titleShape, title string) error {
	k := int64(key)
	if err := g.facets(key, s); err != nil {
		return err
	}
	clear(g.used)
	ords := map[string]int{}
	for i := 0; i < s.credits; i++ {
		kind, role := "cast", ""
		switch {
		case i == 1 && typ == "movie":
			kind, role = "director", "Director"
		case i == 1 && typ == "show":
			kind, role = "writer", "Creator"
		case i%10 == 4:
			kind, role = "writer", []string{"Screenplay", "Writer", "Story", "Novel"}[g.rng.IntN(4)]
		case i%10 == 7:
			kind, role = "crew", crewRoles[g.rng.IntN(len(crewRoles))]
		}
		p := g.people.next(g.rng, g.used)
		g.used[p] = true
		if kind == "cast" {
			role = madeUpWord(splitmix(uint64(p) ^ uint64(k)))
			if g.rng.IntN(8) == 0 {
				role += " (voice)"
			}
		}
		thumb := ""
		if p%2 == 0 {
			thumb = "/person" + strconv.Itoa(int(p)) + ".jpg"
		}
		if err := g.insert("catalog_credits", "key,person_tmdb,name,role,kind,ord,thumb", k, int64(p), personName(p), role, kind, ords[kind], thumb); err != nil {
			return err
		}
		ords[kind]++
	}
	if typ == "episode" {
		return nil
	}
	seen := map[string]bool{}
	for range s.keywords {
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
	for range s.studios {
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
	for range s.altTitles {
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
	return g.crowd(key, typ, s)
}

// crowd stores the title's TMDB recommendations: keys of titles of the same
// type, as catalog/crowd.go keeps them.
func (g *generator) crowd(key core.RatingKey, typ string, s titleShape) error {
	pool := g.movies
	if typ == "show" {
		pool = g.shows
	}
	var recs []string
	for range min(s.crowd, len(pool)) {
		recs = append(recs, strconv.FormatInt(int64(pool[g.rng.IntN(len(pool))]), 10))
	}
	return g.insert("catalog_crowd", "key,fetched_at,pages,recs", int64(key), g.now.Unix(), 1, strings.Join(recs, ","))
}

func (g *generator) facets(key core.RatingKey, s titleShape) error {
	first := g.rng.IntN(len(fixtureGenres))
	for i := range s.genres {
		genre := fixtureGenres[(first+i*(1+first%5))%len(fixtureGenres)]
		if err := g.insert("catalog_genres", "key,genre", int64(key), genre); err != nil {
			return err
		}
	}
	countries := []string{"US", "GB", "CA", "FR", "JP", "KR", "DE"}
	c := g.rng.IntN(len(countries))
	for i := range s.countries {
		if err := g.insert("catalog_countries", "key,country", int64(key), countries[(c+i)%len(countries)]); err != nil {
			return err
		}
	}
	return nil
}
