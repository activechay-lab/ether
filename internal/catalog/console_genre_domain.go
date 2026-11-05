package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
)

// This metadata-only projection is a safe upper bound for every viewer/type/
// date policy. A missing pair proves an AND impossible; a present pair never
// bypasses the ordinary policy query. Writes touch only a title's own genres.
func init() {
	schema := `CREATE TABLE catalog_genre_pairs(a TEXT NOT NULL,b TEXT NOT NULL,title_count INTEGER NOT NULL,PRIMARY KEY(a,b));
CREATE INDEX catalog_genre_pairs_reverse ON catalog_genre_pairs(b,a);
INSERT INTO catalog_genre_pairs(a,b,title_count) SELECT a.genre,b.genre,COUNT(*) FROM catalog_genres a JOIN catalog_genres b ON b.key=a.key GROUP BY a.genre,b.genre;`
	add := `INSERT INTO catalog_genre_pairs(a,b,title_count) SELECT NEW.genre,genre,1 FROM catalog_genres WHERE key=NEW.key ON CONFLICT(a,b) DO UPDATE SET title_count=catalog_genre_pairs.title_count+1;
INSERT INTO catalog_genre_pairs(a,b,title_count) SELECT genre,NEW.genre,1 FROM catalog_genres WHERE key=NEW.key AND genre<>NEW.genre ON CONFLICT(a,b) DO UPDATE SET title_count=catalog_genre_pairs.title_count+1;`
	remove := `UPDATE catalog_genre_pairs SET title_count=title_count-1 WHERE a=OLD.genre AND b IN(SELECT genre FROM catalog_genres WHERE key=OLD.key);
UPDATE catalog_genre_pairs SET title_count=title_count-1 WHERE b=OLD.genre AND a<>OLD.genre AND a IN(SELECT genre FROM catalog_genres WHERE key=OLD.key);
DELETE FROM catalog_genre_pairs WHERE title_count<=0 AND (a=OLD.genre OR b=OLD.genre);`
	store.Register("catalog", 40, schema+`
CREATE TRIGGER catalog_genre_pairs_insert AFTER INSERT ON catalog_genres BEGIN `+add+` END;
CREATE TRIGGER catalog_genre_pairs_delete BEFORE DELETE ON catalog_genres BEGIN `+remove+` END;
CREATE TRIGGER catalog_genre_pairs_update_before BEFORE UPDATE ON catalog_genres BEGIN `+remove+` END;
CREATE TRIGGER catalog_genre_pairs_update_after AFTER UPDATE ON catalog_genres BEGIN `+add+` END;`)
	store.RegisterDialect("catalog", 40, store.Postgres, schema+`
CREATE FUNCTION catalog_genre_pairs_add() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+add+` RETURN NEW; END $$;
CREATE FUNCTION catalog_genre_pairs_remove() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN `+remove+` IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$;
CREATE TRIGGER catalog_genre_pairs_insert AFTER INSERT OR UPDATE ON catalog_genres FOR EACH ROW EXECUTE FUNCTION catalog_genre_pairs_add();
CREATE TRIGGER catalog_genre_pairs_delete BEFORE DELETE OR UPDATE ON catalog_genres FOR EACH ROW EXECUTE FUNCTION catalog_genre_pairs_remove();`)
}

// mandatoryConsoleFilters visits only predicates that must be true. OR branches
// and viewer-state nodes are left to SQL, so no alternative can be pruned.
func mandatoryConsoleFilters(f core.Filter) []core.Filter {
	out := []core.Filter{f}
	var walk func(*core.FilterGroup)
	walk = func(g *core.FilterGroup) {
		if g == nil || groupHasOwnState(g) {
			return
		}
		if g.Filter != nil {
			out = append(out, *g.Filter)
			walk(g.Filter.Group)
			return
		}
		if g.Operator == "and" {
			for i := range g.Children {
				walk(&g.Children[i])
			}
		}
	}
	walk(f.Group)
	return out
}

func (c *Catalog) consoleDomainEmpty(ctx context.Context, f core.Filter) (bool, error) {
	var genres [][]string
	exists := func(query string, args ...any) (bool, error) {
		var one int
		err := c.db.QueryRowContext(ctx, query+" LIMIT 1", args...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	for _, leaf := range mandatoryConsoleFilters(f) {
		if len(leaf.Genres) > 0 {
			genres = append(genres, leaf.Genres)
		}
		if leaf.Kids {
			genres = append(genres, []string{"Kids"})
		}
		keywords := leaf.Keywords
		if leaf.Anime {
			keywords = append(append([]string{}, keywords...), "anime")
		}
		for _, keyword := range keywords {
			keyword = strings.ToLower(strings.TrimSpace(keyword))
			if keyword == "" {
				continue
			}
			ok, err := exists(`SELECT 1 FROM catalog_keywords WHERE LOWER(keyword)=?`, keyword)
			if err != nil || !ok {
				return !ok, err
			}
		}
		credits := leaf.Credits
		if leaf.Person > 0 && len(credits) == 0 {
			credits = []core.CreditFilter{{People: []int64{leaf.Person}}}
		}
		for _, credit := range credits {
			if credit.Exclude {
				continue
			}
			var parts []string
			var args []any
			if len(credit.People) > 0 {
				parts = append(parts, "person_tmdb IN("+placeholders(len(credit.People))+")")
				for _, id := range credit.People {
					args = append(args, id)
				}
			}
			if len(credit.Names) > 0 {
				parts = append(parts, "person_tmdb IN(SELECT person_tmdb FROM catalog_person_names WHERE "+personNameExpression(store.DialectOf(c.db))+" IN("+placeholders(len(credit.Names))+"))")
				for _, name := range credit.Names {
					args = append(args, asciiPersonName(name))
				}
			}
			if len(parts) == 0 {
				continue
			}
			predicate := "(" + strings.Join(parts, " OR ") + ")"
			if credit.Kind == "composer" {
				predicate += " AND kind='crew' AND role='Composer'"
			} else if credit.Kind != "" {
				predicate += " AND kind=?"
				args = append(args, credit.Kind)
			}
			ok, err := exists("SELECT 1 FROM catalog_credits WHERE "+predicate, args...)
			if err != nil || !ok {
				return !ok, err
			}
		}
	}
	// Diagonal pairs also provide an indexed, exact known-genre domain.
	for i, left := range genres {
		args := make([]any, 0, len(left))
		for _, v := range left {
			args = append(args, v)
		}
		ok, err := exists("SELECT 1 FROM catalog_genre_pairs WHERE a=b AND a IN("+placeholders(len(left))+")", args...)
		if err != nil || !ok {
			return !ok, err
		}
		for _, right := range genres[:i] {
			values := append([]any{}, args...)
			for _, v := range right {
				values = append(values, v)
			}
			ok, err := exists("SELECT 1 FROM catalog_genre_pairs WHERE a IN("+placeholders(len(left))+") AND b IN("+placeholders(len(right))+")", values...)
			if err != nil || !ok {
				return !ok, err
			}
		}
	}
	return c.consoleYearEmpty(ctx, f)
}

func consoleHasSparseCandidates(f core.Filter) bool {
	for _, v := range mandatoryConsoleFilters(f) {
		if v.Person > 0 || len(v.Credits) > 0 || len(v.Keywords) > 0 || len(v.Tags) > 0 || v.Anime || v.Collection > 0 || len(v.Collections) > 0 {
			return true
		}
	}
	return false
}
