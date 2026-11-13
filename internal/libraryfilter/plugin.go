package libraryfilter

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// PluginSQLHidden compiles the same ordered policy against plugin_items AS i.
// fields maps the rule vocabulary onto declared, indexed plugin field keys.
func (f *Filter) PluginSQLHidden(typ core.MediaType, fields map[string]string) (string, []any) {
	q, args := f.SQLHiddenForType(typ)
	return pluginSQL(q, typ, fields), args
}

func pluginSQL(q string, typ core.MediaType, fields map[string]string) string {
	q = strings.ReplaceAll(q, "t.type", "'"+string(typ)+"'")
	for _, pair := range [][2]string{{"year", "year"}, {"rating", "rating"}} {
		q = strings.ReplaceAll(q, "t."+pair[0], "i."+pair[1])
	}
	for _, pair := range [][2]string{{"votes", "vote_count"}, {"popularity", "popularity"}, {"runtime", "runtime_sec"}, {"language", "language"}, {"contentRating", "content_rating_rank"}} {
		expr := "0"
		column := "v_int + pf.v_real"
		if pair[0] == "language" {
			expr = "''"
			column = "v_text"
		}
		if key := fields[pair[0]]; key != "" {
			expr = "COALESCE((SELECT pf." + column + " FROM plugin_item_fields pf WHERE pf.item_ref=i.item_ref AND pf.field='" + strings.ReplaceAll(key, "'", "''") + "' LIMIT 1)," + expr + ")"
		}
		q = strings.ReplaceAll(q, "t."+pair[1], expr)
	}
	for _, pair := range [][2]string{{"genre", "genres"}, {"studio", "studios"}, {"country", "countries"}, {"keyword", "keywords"}} {
		key := strings.ReplaceAll(fields[pair[0]], "'", "''")
		for _, alias := range []string{"lf", "co"} {
			q = strings.ReplaceAll(q, "FROM catalog_"+pair[1]+" "+alias+" WHERE "+alias+".key=t.key", "FROM plugin_item_fields "+alias+" WHERE "+alias+".item_ref=i.item_ref AND "+alias+".field='"+key+"'")
			q = strings.ReplaceAll(q, alias+"."+pair[0], alias+".v_text")
		}
	}
	return q
}

// PluginCatchCountsSQL counts the first matching rule for each plugin item.
func (f *Filter) PluginCatchCountsSQL(typ core.MediaType, fields map[string]string) (string, []any) {
	q, args := f.CatchCountsSQL()
	return pluginSQL(q, typ, fields), args
}
