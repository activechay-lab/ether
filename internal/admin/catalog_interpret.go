package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/llm"
)

type catalogChip struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
	Group int    `json:"group,omitempty"`
}
type catalogInterpretation struct {
	Chips     []catalogChip `json:"chips"`
	Mode      string        `json:"mode"`
	GroupMode string        `json:"groupMode"`
}

func (h *Handler) handleCatalogInterpretCapability(w http.ResponseWriter, r *http.Request) {
	client := llm.NewClient(llm.ClientOptions{Config: h.deps.Config.Get().LLM})
	if client.Enabled() {
		// Cached reads schedule deduplicated background refreshes before the first
		// AI click. This cannot count titles on the capability request.
		_, _ = h.catalogInterpretVocabulary(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": client.Enabled()})
}

// AI interpretation is independent of browsing. It uses generation-stamped facet
// snapshots: it never counts or scans the catalogue in the request path.
func (h *Handler) handleCatalogInterpret(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4097)).Decode(&input); err != nil || len(input.Text) > 1000 || strings.TrimSpace(input.Text) == "" {
		writeError(w, 400, "invalid_prompt", "Describe the titles you want in at most 1,000 characters")
		return
	}
	client := llm.NewClient(llm.ClientOptions{Config: h.deps.Config.Get().LLM})
	if !client.Enabled() {
		writeError(w, 501, "not_configured", "AI filters are unavailable; use title search or filter chips")
		return
	}
	vocabulary, err := h.catalogInterpretVocabulary(r.Context())
	if err != nil {
		writeError(w, 503, "vocabulary_unavailable", "AI catalogue vocabulary is warming up or unavailable; try again shortly")
		return
	}
	encoded, _ := json.Marshal(vocabulary)
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: catalogInterpretGuide + "\nAvailable catalogue vocabulary (canonical values): " + string(encoded)}, {Role: "user", Content: input.Text}}, true)
	if err != nil {
		writeError(w, 502, "interpretation_unavailable", "AI filters are unavailable; use title search or filter chips")
		return
	}
	var result catalogInterpretation
	if len(out) > 16384 || json.Unmarshal([]byte(out), &result) != nil || validateCatalogInterpretation(&result, vocabulary) != nil {
		writeError(w, 502, "invalid_interpretation", "AI could not produce valid catalogue filters; try rephrasing or use filter chips")
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) catalogInterpretVocabulary(ctx context.Context) (map[string][]string, error) {
	c, ok := h.deps.CatalogHelper.(interface {
		ConsoleFacets(context.Context, core.MediaType, string) ([]core.FacetValue, bool, error)
	})
	if !ok {
		return nil, fmt.Errorf("cached vocabulary unavailable")
	}
	vocabulary := map[string][]string{}
	warming := false
	for _, pair := range [][2]string{{"genre", "genre"}, {"language", "language"}, {"country", "country"}, {"certification", "contentRating"}, {"studio", "studio"}, {"keyword", "keyword"}, {"tag", "tag"}} {
		values, pending, err := c.ConsoleFacets(ctx, "", pair[1])
		if err != nil {
			return nil, err
		}
		if pending && len(values) == 0 {
			warming = true
		}
		keys := []string{}
		for _, v := range values {
			if v.Key != "" && len(v.Key) <= 256 {
				keys = append(keys, v.Key)
			}
			if len(keys) >= 200 {
				break
			}
		}
		vocabulary[pair[0]] = keys
	}
	if warming {
		return nil, fmt.Errorf("vocabulary warming")
	}
	return vocabulary, nil
}

func validateCatalogInterpretation(result *catalogInterpretation, vocab map[string][]string) error {
	if len(result.Chips) == 0 || len(result.Chips) > 24 {
		return fmt.Errorf("invalid chips")
	}
	if result.Mode == "" {
		result.Mode = "and"
	}
	if result.GroupMode == "" {
		result.GroupMode = "or"
	}
	if result.Mode != "and" || result.GroupMode != "or" {
		return fmt.Errorf("invalid grouping")
	}
	enums := map[string]string{
		"type": "movie|show", "watched": "watched|unwatched", "progress": "true|false", "rated": "true|false", "watchlist": "true|false",
		"playable": "yes|unknown", "cached": "yes|unknown", "quality": "4k|hdr|dv|atmos", "anime": "true", "kids": "true",
		"status": "Returning Series|Ended|Canceled|In Production", "gate": "admitted|held|unknown",
	}
	bounds := map[string][2]float64{"year": {1870, 2200}, "decade": {1870, 2200}, "rating": {0, 10}, "votes": {0, 1e12}, "popularity": {0, 1e12}, "runtime": {0, 100000}, "seasons": {0, 100000}, "episodes": {0, 1e7}, "collection": {1, 1e12}}
	movie, kids := false, false
	for _, chip := range result.Chips {
		movie = movie || (chip.Field == "type" && strings.EqualFold(strings.TrimSpace(chip.Value), "movie") && chip.Op == "is")
		kids = kids || (chip.Field == "kids" && strings.EqualFold(strings.TrimSpace(chip.Value), "true") && chip.Op == "is")
	}
	// The Kids genre is a TV taxonomy facet, not an audience classification.
	// Returning it for movies would turn a family request into an empty result.
	if movie && kids {
		return fmt.Errorf("kids TV genre is incompatible with movies")
	}
	for i := range result.Chips {
		c := &result.Chips[i]
		c.Value = strings.TrimSpace(c.Value)
		if c.Value == "" || len(c.Value) > 256 || c.Group < 0 || c.Group > 3 {
			return fmt.Errorf("invalid value/group")
		}
		if c.Op != "is" && c.Op != "min" && c.Op != "max" && c.Op != "not" {
			return fmt.Errorf("invalid operator")
		}
		if limits, ok := bounds[c.Field]; ok {
			n, err := strconv.ParseFloat(c.Value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < limits[0] || n > limits[1] || c.Op == "not" {
				return fmt.Errorf("invalid numeric value")
			}
			if c.Field == "decade" && (math.Mod(n, 10) != 0 || c.Op != "is") {
				return fmt.Errorf("invalid decade")
			}
			if c.Field == "collection" && (math.Trunc(n) != n || c.Op != "is") {
				return fmt.Errorf("invalid collection")
			}
			continue
		}
		if c.Field == "released" || c.Field == "dateAdded" || c.Field == "etherAdded" {
			if _, err := time.Parse("2006-01-02", c.Value); err != nil || c.Op == "not" {
				return fmt.Errorf("invalid date")
			}
			continue
		}
		if c.Op != "is" && c.Op != "not" {
			return fmt.Errorf("range on category")
		}
		if values, ok := vocab[c.Field]; ok {
			matched := false
			for _, v := range values {
				if strings.EqualFold(v, c.Value) {
					c.Value = v
					matched = true
					break
				}
			}
			// Unknown moods are explicit keyword matches, not title searches. Keywords
			// use the existing indexed exact-match path and may legitimately find none.
			if !matched && c.Field != "keyword" {
				return fmt.Errorf("unknown catalogue facet value")
			}
			continue
		}
		if values, ok := enums[c.Field]; ok {
			matched := false
			for _, v := range strings.Split(values, "|") {
				if strings.EqualFold(v, c.Value) {
					c.Value = v
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("unknown enum value")
			}
			continue
		}
		switch c.Field {
		case "cast", "director", "writer", "composer":
		default:
			return fmt.Errorf("unsupported field")
		}
	}
	return nil
}

const catalogInterpretGuide = `Translate the user's natural-language film/TV wishes into REAL editable catalogue filters. Return JSON only: {"mode":"and","groupMode":"or","chips":[{"field":"type","op":"is","value":"movie"},{"field":"genre","op":"is","value":"Comedy","group":1},{"field":"genre","op":"is","value":"Romance","group":1}]}.
Values are strings. Ungrouped chips are mandatory AND constraints. Chips sharing group 1, 2 or 3 are OR alternatives; each group is ANDed with mandatory constraints. Use OR genre alternatives for moods, never require every suggested genre simultaneously. Use actual values from the supplied vocabulary for genre, language, country, certification, studio and tag. Map intent and tone, not literal title words. Cute/whimsical/girls' night movies suggests movie + Comedy/Romance/Family/Animation alternatives as appropriate, plus relevant catalogue keyword alternatives such as female friendship, friendship, whimsical, feel-good when present. Kids/family-friendly MOVIES require actual Family/Animation genre values and available age-appropriate certifications such as G/PG, using OR alternatives where appropriate. NEVER emit kids=true for movies: that flag means the canonical TV Kids genre, NOT a generic child audience. The combination type=movie and kids=true is invalid. Only use kids=true for an explicit TV Kids genre request. Do not assume all films are for kids. Dark/gritty thrillers suggests Thriller/Crime and matching keywords. Feel-good does not mean a title called feel-good.
For mood/tone or concepts without an existing facet, use concise keyword concepts, preferentially matching supplied TMDB keyword vocabulary (e.g. female friendship, magical realism). Unknown concepts MUST remain keyword matches, never title queries. There is NO title field. Never return the whole sentence as a keyword either: extract the concepts. Do not invent categorical vocabulary values. Do not infer numerical ratings/runtime/era unless requested.
Fields: type (movie/show), genre, decade (1980), year, rating (0-10), votes, popularity, runtime (minutes), language, country, certification, studio, keyword, tag, cast/director/writer/composer (exact person name), collection (numeric TMDB collection ID only when explicitly supplied), released/dateAdded/etherAdded (YYYY-MM-DD), watched (watched/unwatched), progress/rated/watchlist (true/false), playable (yes/unknown), cached (yes/unknown), quality (4k/hdr/dv/atmos), anime (true), kids (true; explicit TV Kids genre ONLY, never movies or generic family audience), status (Returning Series/Ended/Canceled/In Production), seasons, episodes, gate (admitted/held/unknown).
Operators: is, not (categorical only), min/max (numeric and dates only). Use two chips for a range; decades use is. At most 24 chips. Examples: "90s thrillers above 7 under two hours" -> type movie, decade1990, genre Thriller, rating min7, runtime max120; "80s comedies with Tom Hanks" -> type movie, decade1980, genre Comedy, cast Tom Hanks; "French romantic movies" -> type movie, language fr, genre Romance (only if present). Subjective adjectives are keywords or guide genre choices, never titles.`
