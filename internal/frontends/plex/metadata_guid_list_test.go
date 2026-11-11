package plex

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/store"
)

// Infuse resolves its Plex Watchlist row against the server with one
// /library/metadata request naming plex.tv identities, slashes and all.
func TestMetadataByPlexGUIDList(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const movie, show = "plex://movie/5d7768265af944001f1f6976", "plex://show/5d9c086c46115600200aa2fe"
	if err = storeVerifiedGUID(db, fightClub.Key, movie, 550, "Fjord Harbor", 1999); err != nil {
		t.Fatal(err)
	}
	if err = storeVerifiedGUID(db, breakingBad.Key, show, 1396, "Boundary Bay", 2008); err != nil {
		t.Fatal(err)
	}
	pg := plexguid.New(db, plexguid.Options{})
	defer pg.Close()
	f := newFixture()
	f.srv.setPlexGUID(pg)
	defer f.srv.Close()
	f.srv.setCatalog(guidCatalog{f.catalog})

	rec := f.get(t, "/library/metadata/"+movie+",plex://movie/000000000000000000000000,"+show, jsonAccept...)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"ratingKey":"`+fightClub.Key.String()+`"`) || !strings.Contains(body, `"ratingKey":"`+breakingBad.Key.String()+`"`) {
		t.Fatalf("guid list: %d %s", rec.Code, body)
	}
	if rec := f.get(t, "/library/metadata/plex://movie/000000000000000000000000", jsonAccept...); rec.Code != 404 {
		t.Fatalf("unknown identity: %d", rec.Code)
	}
}
