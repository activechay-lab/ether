//go:build upgradebench

package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
	"os"
	"testing"
	"time"
)

func TestUpgradeFixtureManual(t *testing.T) {
	dir := os.Getenv("ETHER_UPGRADE_FIXTURE")
	if dir == "" {
		t.Skip("manual")
	}
	os.MkdirAll(dir, 0700)
	_, e := config.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(context.Background(), dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	queries := []string{
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<600000) INSERT INTO catalog_titles(key,type,tmdb_id,title,original_title,summary) SELECT i,'movie',i,'Film '||i,'Original '||i,printf('%01000d',i) FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000000) INSERT INTO state_watch(user_id,rating_key,updated_at,last_viewed_at) SELECT 1+i/600000,1+i%600000,100,90 FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000000) INSERT INTO state_history(user_id,rating_key,viewed_at) SELECT 1+i/600000,1+i%600000,i FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000000) INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended) SELECT 'play'||i,1,1+i%600000,'viewer','Film '||i,'Windows','PC','directplay',i,i,i FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO livetv_sources(id,data) SELECT 'source'||i,json_object('id','source'||i,'playlistUrl','https://iptv.example/list?token=fake-'||i,'guideUrl','https://iptv.example/guide?token=fake-'||i) FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO livetv_channels(id,data) SELECT 'channel'||i,json_object('id','channel'||i,'name','Channel '||i,'streamUrl','https://iptv.example/stream?token=fake-'||i) FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO trakt_tokens(user_id,access_token,refresh_token,expires_at,username,updated_at) SELECT i,'fake-access-'||i,'fake-refresh-'||i,0,'viewer',0 FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO availability_feed_items(id,source_id,generation,match_title,payload,seen_at) SELECT 'release'||i,'fake','1','film',json_object('title','Film '||i,'url','https://indexer.example/'||i),0 FROM n`,
		`ANALYZE`,
	}
	for i, q := range queries {
		start := time.Now()
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e, q)
		}
		t.Log(i, time.Since(start))
	}
	if os.Getenv("ETHER_UPGRADE_MIXED") != "" {
		if _, e = db.Exec(`UPDATE catalog_titles SET type=CASE WHEN key<=42000 THEN 'show' WHEN key<=142000 THEN 'movie' ELSE 'episode' END,show_key=CASE WHEN key>142000 THEN 1+key%42000 ELSE 0 END,released_ts=1600000000; ANALYZE`); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
		t.Fatal(e)
	}
}
