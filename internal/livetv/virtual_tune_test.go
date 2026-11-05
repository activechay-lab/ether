package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestGuideSkipsTitlesKnownToBeUnplayable(t *testing.T) {
	now := time.Unix(7, 0).UTC()
	first := core.MovieKey(1)
	second := core.MovieKey(2)
	s := testService(t, Options{
		Now: func() time.Time { return now },
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == first {
				return true, false
			}
			return false, false
		}),
	})
	c := testChannel()
	c.Items = []Item{
		{Key: first, Title: "Cats in the Museum 2", DurationMS: 12000},
		{Key: second, Title: "Second", DurationMS: 20000},
	}
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	got, err := s.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(got) != 1 || got[0].Item.Key != second || got[0].Item.Title != "Second" {
		t.Fatalf("guide kept an unplayable title: %+v %v", got, err)
	}
	unknown := testService(t, Options{Now: func() time.Time { return now }})
	plain, err := unknown.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(plain) != 1 || plain[0].Item.Key != first {
		t.Fatalf("unknown availability changed the guide: %+v %v", plain, err)
	}
}

type availFunc func(core.RatingKey) (bool, bool)

func (f availFunc) Available(_ context.Context, key core.RatingKey) (bool, bool) { return f(key) }
