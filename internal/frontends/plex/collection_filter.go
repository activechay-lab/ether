package plex

import (
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Collection lookups use a word-prefix search for title=, but substring
// exclusion for title!=. This only visits the collection list already loaded
// by the endpoint; it never expands collection members or the title catalogue.
func filterCollectionTitles(cs []frontend.Collection, q url.Values) []frontend.Collection {
	keys := []string{"title", "title!", "title<", "title>"}
	if !q.Has("title") && !q.Has("title!") && !q.Has("title<") && !q.Has("title>") {
		return cs
	}
	out := make([]frontend.Collection, 0, len(cs))
	for _, c := range cs {
		keep := true
		for _, key := range keys {
			for _, raw := range q[key] {
				if raw == "" {
					continue
				}
				matched := false
				for _, value := range strings.Split(raw, ",") {
					title, value := strings.ToLower(c.Title), strings.ToLower(value)
					switch key {
					case "title":
						title, value = collectionSearchFold(title), collectionSearchFold(value)
						matched = strings.HasPrefix(title, value) || strings.Contains(title, " "+value)
					case "title!":
						matched = strings.Contains(title, value)
					case "title<":
						matched = strings.HasPrefix(title, value)
					case "title>":
						matched = strings.HasSuffix(title, value)
					}
					if matched {
						break
					}
				}
				if key == "title!" {
					matched = !matched
				}
				keep = keep && matched
			}
		}
		if keep {
			out = append(out, c)
		}
	}
	return out
}

func collectionSearchFold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(s))
}
