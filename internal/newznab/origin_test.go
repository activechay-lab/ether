package newznab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/braedonsaunders/ether/internal/store"
)

// The label, feed GUID and NZB directory deliberately collide. Only the
// configured originating client may supply a private trust context or cache.
func TestOriginatingIndexerIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var grabsA, grabsB atomic.Int32
	bodyA := strings.Replace(fakeNZB, `subject="x"`, `subject="origin-a"`, 1)
	bodyB := strings.Replace(fakeNZB, `subject="x"`, `subject="origin-b"`, 1)
	server := func(label, body string, grabs *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("t") {
			case "caps":
				fmt.Fprint(w, loadFixture(t, "caps.xml", "http://"+r.Host))
			case "get":
				grabs.Add(1)
				fmt.Fprint(w, body)
			default:
				fmt.Fprintf(w, `<rss><channel><item><title>Two.Origins.%s.2020.1080p.WEB-DL.H264.AAC</title><guid>shared-guid</guid><link>http://%s/api?t=get&amp;id=shared-guid</link><enclosure url="http://%s/api?t=get&amp;id=shared-guid" length="1000" type="application/x-nzb"/></item></channel></rss>`, label, r.Host, r.Host)
			}
		}))
	}
	a, b := server("A", bodyA, &grabsA), server("B", bodyB, &grabsB)
	defer a.Close()
	defer b.Close()
	cfgs := []config.Indexer{
		{ID: "origin-a", Name: "Duplicate label", URL: a.URL, Enabled: true},
		{ID: "origin-b", Name: "Duplicate label", URL: b.URL, Enabled: true},
	}
	dir := t.TempDir()
	m := NewManager(db, cfgs, Options{NZBDir: dir})
	request := core.SearchRequest{Type: core.TypeMovie, Title: "Two Origins", Year: 2020}
	releases, err := m.Search(ctx, request)
	if err != nil || len(releases) != 2 {
		t.Fatalf("two origins lost: releases=%+v err=%v", releases, err)
	}
	if releases[0].ID == releases[1].ID {
		t.Fatal("same GUID and label collided across configured origins")
	}
	byID := map[string]core.Release{}
	bodies := map[string]string{"origin-a": bodyA, "origin-b": bodyB}
	for _, r := range releases {
		if r.Indexer != "Duplicate label" || bodies[r.IndexerID] == "" {
			t.Fatalf("origin or display label lost: %+v", r)
		}
		byID[r.IndexerID] = r
		for range 2 {
			got, err := m.FetchNZB(ctx, r)
			if err != nil || string(got) != bodies[r.IndexerID] {
				t.Fatalf("crossed origin/cache: %s body=%s err=%v", r.IndexerID, got, err)
			}
		}
	}
	if grabsA.Load() != 1 || grabsB.Load() != 1 {
		t.Fatalf("shared disk cache crossed origins: A=%d B=%d", grabsA.Load(), grabsB.Load())
	}
	// Backend/cached releases can retain the same opaque ID even when their
	// originating identity is already bound. The disk namespace must bind both.
	for _, id := range []string{"origin-a", "origin-b"} {
		r := byID[id]
		r.ID = "shared-opaque-release"
		got, err := m.FetchNZB(ctx, r)
		if err != nil || string(got) != bodies[id] {
			t.Fatalf("opaque ID crossed client disk cache: origin=%s got=%s err=%v", id, got, err)
		}
	}
	for _, c := range m.Clients() {
		var arrived []core.Release
		req := request
		req.Title = "Streamed Origins"
		streamed, err := c.SearchWith(ctx, req, func(rs []core.Release) { arrived = append(arrived, rs...) })
		if err != nil || len(arrived) != 1 || len(streamed) != 1 || arrived[0].IndexerID != c.indexerID() || arrived[0].ID != byID[c.indexerID()].ID || streamed[0].ID != arrived[0].ID {
			t.Fatalf("streamed item exposed without binding: arrived=%+v final=%+v err=%v", arrived, streamed, err)
		}
	}
	// Simulate a persisted pre-binding row, still scoped by indexer_id and the
	// exact configured search identity. Restart/rename must recover that owner.
	rows, err := db.Query(`SELECT cache_key,results FROM newznab_cache WHERE indexer_id='origin-a'`)
	if err != nil {
		t.Fatal(err)
	}
	oldRows := map[string]string{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		oldRows[key] = raw
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(oldRows) == 0 {
		t.Fatal("no persisted origin-scoped cache")
	}
	for key, raw := range oldRows {
		var doc cachedResults
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		for i := range doc.Releases {
			doc.Releases[i].IndexerID = ""
			doc.Releases[i].ID = releaseID(doc.Releases[i].Indexer, doc.Releases[i].GUID, doc.Releases[i].NZBURL+doc.Releases[i].Name)
		}
		rawBytes, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE newznab_cache SET results=? WHERE cache_key=?`, string(rawBytes), key); err != nil {
			t.Fatal(err)
		}
	}
	renamed := append([]config.Indexer(nil), cfgs...)
	renamed[0].Name = "Renamed A"
	renamed[1].Name = "Renamed B"
	restarted := NewManager(db, []config.Indexer{renamed[1], renamed[0]}, Options{NZBDir: t.TempDir()})
	cached, err := restarted.Search(ctx, request)
	if err != nil || len(cached) != 2 {
		t.Fatalf("restart cache binding failed: %+v %v", cached, err)
	}
	for _, r := range cached {
		got, err := restarted.FetchNZB(ctx, r)
		if err != nil || string(got) != bodies[r.IndexerID] {
			t.Fatalf("rename/cache rebound to label: %+v %v", r, err)
		}
	}
	discovered, err := restarted.CachedReleases(ctx)
	if err != nil || len(discovered) != 2 {
		t.Fatalf("bulk cache lost origin scope: %+v %v", discovered, err)
	}
	// Unbound or unknown records cannot pick the first duplicate, even when a
	// shared on-disk descriptor is already available. Explicit IDs also fail closed.
	ambiguous := NewManager(db, cfgs, Options{NZBDir: dir})
	beforeA, beforeB := grabsA.Load(), grabsB.Load()
	for _, id := range []string{"", "removed-indexer"} {
		r := byID["origin-a"]
		r.IndexerID = id
		if _, err := ambiguous.FetchNZB(ctx, r); err == nil {
			t.Fatalf("ambiguous/unknown origin %q accepted", id)
		}
	}
	r := byID["origin-a"]
	r.IndexerID = ""
	r.Indexer = "unconfigured label"
	if _, err := ambiguous.FetchNZB(ctx, r); err == nil {
		t.Fatal("unbound release probed arbitrary clients")
	}
	if _, err := ambiguous.Clients()[1].FetchNZB(ctx, byID["origin-a"]); err == nil {
		t.Fatal("direct foreign-client cache read accepted")
	}
	if grabsA.Load() != beforeA || grabsB.Load() != beforeB {
		t.Fatal("unbound/foreign release made an HTTP request")
	}
	r = byID["origin-a"]
	r.ID = "cross-origin-negative-control"
	r.NZBURL = b.URL + "/api?t=get&id=off-origin"
	if _, err := ambiguous.FetchNZB(ctx, r); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("bound client trusted another private origin: %v", err)
	}
	if grabsB.Load() != beforeB {
		t.Fatal("private cross-origin target was reached")
	}
}
