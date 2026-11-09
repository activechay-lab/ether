package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
	"strings"
	"testing"
)

func TestChannelBatchAtomicAndPreservesFields(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	ids := make([]string, 701)
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ids {
		ids[i] = fmt.Sprintf("channel-%04d", i)
		c := testChannel()
		c.ID = ids[i]
		c.StreamURL = "https://provider.invalid/private"
		c.Private = true
		b, _ := json.Marshal(c)
		if _, err := tx.ExecContext(ctx, "INSERT INTO livetv_channels(id,data) VALUES(?,?)", c.ID, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fav := true
	// The missing ID is in the final SQL chunk. Earlier chunks must roll back.
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: append(append([]string{}, ids...), "zzz-missing"), Favorite: &fav}); err == nil {
		t.Fatal("missing channel accepted")
	}
	c, err := s.Channel(ctx, ids[0])
	if err != nil || c.Favorite {
		t.Fatalf("partial update: %+v %v", c, err)
	}
	bumps := 0
	s.SetOnChange(func() { bumps++ })
	n, err := s.UpdateChannels(ctx, ChannelBatch{IDs: append(append([]string{}, ids...), ids[0]), Favorite: &fav})
	if err != nil || n != len(ids) || bumps != 1 {
		t.Fatalf("batch: n=%d bumps=%d err=%v", n, bumps, err)
	}
	for _, id := range []string{ids[0], ids[700]} {
		c, err := s.Channel(ctx, id)
		if err != nil || !c.Favorite || !c.Enabled || !c.Private || c.StreamURL != "https://provider.invalid/private" || len(c.Items) != 2 {
			t.Fatalf("lost fields: %+v %v", c, err)
		}
	}
	off := false
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: ids[:2], Enabled: &off, Favorite: &off}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Channel(ctx, ids[0])
	if c.Enabled || c.Favorite {
		t.Fatal("false patch ignored")
	}
	c, _ = s.Channel(ctx, ids[2])
	if !c.Enabled || !c.Favorite {
		t.Fatal("unselected channel changed")
	}
}

func TestChannelBatchGroupsSurviveImport(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	src := Source{ID: "provider", Name: "Provider", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" group-title=\"Provider news\",News\nhttps://provider.invalid/news.ts\n"}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Channels(ctx)
	if len(cs) != 1 {
		t.Fatal("fixture channels", len(cs))
	}
	group := "  My news  "
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{cs[0].ID}, Group: &group}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Channel(ctx, cs[0].ID)
	if c.Group != "My news" || !c.CustomGroup {
		t.Fatalf("custom group lost: %+v", c)
	}
}

func TestChannelBatchRejectsInvalidAndUnknownFields(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	c := testChannel()
	s.SaveChannel(ctx, c)
	fav := true
	long := strings.Repeat("x", 201)
	for _, body := range []ChannelBatch{{IDs: []string{c.ID}}, {Favorite: &fav}, {IDs: []string{""}, Favorite: &fav}, {IDs: []string{c.ID}, Group: &long}} {
		if _, err := s.UpdateChannels(ctx, body); err == nil {
			t.Fatal("accepted invalid batch")
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/channels/bulk", strings.NewReader(`{"ids":["test"],"streamUrl":"https://wrong.invalid"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatal("unexpected status", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/channels/bulk", strings.NewReader(`{"ids":["test"],"favorite":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatal("patch route", rec.Code, rec.Body.String())
	}
}

// Pause a real refresh after its override snapshot but before it writes any
// channel row. An uncoordinated bulk write would commit in that exact gap.
// Imports diff against stored rows, so the refresh carries a renamed channel:
// an unchanged playlist would write nothing and never reach the barrier.
func TestChannelBatchRefreshInterleavingPostgres(t *testing.T) {
	if os.Getenv(store.TestPostgresEnv) == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	s := testService(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	src := Source{ID: "panel", Name: "Panel", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" group-title=\"Provider\",News\nhttps://provider.invalid/news.ts\n"}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Channels(ctx)
	id := cs[0].ID
	src.Playlist = strings.Replace(src.Playlist, ",News\n", ",News HD\n", 1)
	_, err := s.o.DB.ExecContext(ctx, `CREATE FUNCTION bulk_pause_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(987654321); RETURN NULL; END $$;
 CREATE TRIGGER bulk_pause_refresh BEFORE INSERT OR UPDATE OR DELETE ON livetv_channels FOR EACH STATEMENT EXECUTE FUNCTION bulk_pause_refresh();`)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := s.o.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	if _, err := blocker.ExecContext(ctx, "SELECT pg_advisory_lock(987654321)"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	unlock := func() {
		once.Do(func() { blocker.ExecContext(context.Background(), "SELECT pg_advisory_unlock(987654321)") })
	}
	importDone := make(chan error, 1)
	bulkDone := make(chan error, 1)
	importRead, bulkRead, bulkStarted := false, false, false
	defer func() {
		unlock()
		if !importRead {
			<-importDone
		}
		if bulkStarted && !bulkRead {
			<-bulkDone
		}
	}()
	go func() { _, err := s.ImportSource(ctx, src); importDone <- err }()
	for {
		var paused bool
		if err := s.o.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=987654321 AND NOT granted)").Scan(&paused); err != nil {
			t.Fatal(err)
		}
		if paused {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	off, fav, group := false, true, "Owner group"
	bulkStarted = true
	go func() {
		_, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{id}, Enabled: &off, Favorite: &fav, Group: &group})
		bulkDone <- err
	}()
	for {
		select {
		case err := <-bulkDone:
			bulkRead = true
			t.Fatalf("bulk bypassed refresh lock while old snapshot was pending: %v", err)
		default:
		}
		var waiting int
		if err := s.o.DB.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		} // refresh waits at the test barrier; bulk waits on its source.
		time.Sleep(5 * time.Millisecond)
	}
	unlock()
	err = <-importDone
	importRead = true
	if err != nil {
		t.Fatal(err)
	}
	err = <-bulkDone
	bulkRead = true
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Channel(ctx, id)
	if err != nil || c.Name != "News HD" || c.Enabled || !c.Favorite || c.Group != group || !c.CustomGroup {
		t.Fatalf("refresh lost bulk edits: %+v %v", c, err)
	}
}

func TestChannelBatchMultiSourceLockOrderPostgres(t *testing.T) {
	if os.Getenv(store.TestPostgresEnv) == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	s := testService(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range []string{"z-panel", "a-panel"} {
		src := Source{ID: id, Name: id, Playlist: "#EXTM3U\n#EXTINF:-1,News\nhttps://provider.invalid/" + id + ".ts\n"}
		if _, err := s.ImportSource(ctx, src); err != nil {
			t.Fatal(err)
		}
	}
	cs, _ := s.Channels(ctx)
	if len(cs) != 2 {
		t.Fatal(len(cs))
	}
	done := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func(i int) {
			ids := []string{cs[i%2].ID, cs[(i+1)%2].ID}
			group := fmt.Sprint("Group ", i)
			_, err := s.UpdateChannels(ctx, ChannelBatch{IDs: ids, Group: &group})
			done <- err
		}(i)
	}
	for i := 0; i < 12; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	cs, _ = s.Channels(ctx)
	if cs[0].Group != cs[1].Group {
		t.Fatal("batch split across sources")
	}
}
