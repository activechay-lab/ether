package onair

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/families"
)

type FamilyRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Look Look   `json:"look"`
}

func familyLook(id, name string) Look {
	server := id
	if id == "lateNight" {
		server = "late-night"
	}
	b := families.Defaults(server)
	l := Look{Name: name, Family: id, Moments: map[string]Moment{}, ChannelIDs: []string{}}
	str := func(k string) string { return fmt.Sprint(b.Tokens[k]) }
	l.Brand.Palette.Background = str("bg")
	l.Brand.Palette.Accent = str("accent")
	l.Brand.Palette.Text = str("text")
	l.Brand.Palette.Glow = str("glow")
	l.Brand.Type.Display = str("type.display")
	l.Brand.Type.Body = str("type.mono")
	l.Brand.Energy = .5
	l.Brand.Textures = []string{"Grain"}
	l.Brand.Volume = .6
	l.Brand.Language = "en"
	l.Brand.Words = map[string]string{}
	l.Brand.Tagline = str("words.tagline")
	for _, m := range families.Names()[server] {
		d, e := families.Get(server, m)
		if e == nil {
			l.Moments[m] = Moment{Duration: d.Duration, Poster: d.Poster}
		}
	}
	return l
}
func (s *Service) FamilyRecords(ctx context.Context) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	for _, v := range [][2]string{{"clean", "Clean"}, {"kids", "Kids"}, {"lateNight", "Late night"}, {"cinema", "Cinema"}, {"retro", "Retro"}, {"personal", "Personal"}} {
		b, _ := json.Marshal(FamilyRecord{v[0], v[1], familyLook(v[0], v[1])})
		out = append(out, b)
	}
	rows, e := s.db.QueryContext(ctx, "SELECT data FROM onair_families ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if !json.Valid([]byte(b)) {
			return nil, fmt.Errorf("invalid family record")
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
