package catalog

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// Evidence/credit key sets should not fetch wide title rows merely to test
	// visibility or sort a narrow page. SQLite needs explicit covering keys;
	// PostgreSQL keeps the same payload in leaf INCLUDE columns.
	store.Register("catalog", 41, `CREATE INDEX catalog_titles_console_candidate_page ON catalog_titles(key,type,browsable,adult,released_ts,popularity,year,rating,runtime_sec,language,vote_count,sort_title,show_added_ts,content_rating_rank);`)
	store.RegisterDialect("catalog", 41, store.Postgres, `CREATE INDEX catalog_titles_console_candidate_page ON catalog_titles(key) INCLUDE(type,browsable,adult,release_known,released_ts,popularity,year,rating,runtime_sec,language,vote_count,sort_title,show_added_ts,content_rating_rank);`)
}

func consolePositiveEvidence(f core.Filter) bool {
	// Unknown evidence is a broad complement, so it must retain the planner's
	// ordered path. Positive evidence supplies an indexed, selective key set.
	return f.Playable == "yes" || f.DebridCached == "yes" || len(f.Qualities) > 0
}
