package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/llm"
)

type catalogChip struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Interpretation is an explicit owner action, independent of the SQL results
// request. The model receives only the owner's sentence, never library data.
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
		writeError(w, 501, "not_configured", "Use the built-in filter interpreter")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: catalogInterpretGuide}, {Role: "user", Content: input.Text}}, true)
	if err != nil {
		writeError(w, 502, "interpretation_unavailable", "Use the built-in filter interpreter")
		return
	}
	var result struct {
		Chips []catalogChip `json:"chips"`
	}
	if len(out) > 16384 || json.Unmarshal([]byte(out), &result) != nil || len(result.Chips) == 0 || len(result.Chips) > 24 {
		writeError(w, 502, "invalid_interpretation", "Use the built-in filter interpreter")
		return
	}
	allowed := map[string]bool{}
	for _, field := range strings.Fields("type genre decade year rating votes popularity runtime language country certification studio keyword cast director writer composer collection released title watched progress") {
		allowed[field] = true
	}
	for _, c := range result.Chips {
		if !allowed[c.Field] || (c.Op != "is" && c.Op != "min" && c.Op != "max") || strings.TrimSpace(c.Value) == "" || len(c.Value) > 256 {
			writeError(w, 502, "invalid_interpretation", "Use the built-in filter interpreter")
			return
		}
	}
	writeJSON(w, 200, result)
}

const catalogInterpretGuide = `Convert the user's film/TV request to editable filter chips. Return JSON only: {"chips":[{"field":"genre","op":"is","value":"Comedy"}]}. All values are strings. Fields: type (movie/show), genre (canonical English TMDB genre), decade (1980), year, rating (0-10), votes, popularity, runtime (minutes), language (ISO 639-1), country (ISO alpha-2), certification, studio, keyword, cast/director/writer/composer (exact person name), collection (numeric TMDB collection ID only if explicitly given), released (YYYY-MM-DD), title (unparsed title terms), watched (watched/unwatched), progress (true/false). Operators: is, min (at least), max (at most); min/max only for numeric fields or released dates. Use two chips for a range. Do not invent availability, quality, audio, tags, or viewer data. Keep unsupported terms in a title chip for owner review. Maximum 24 chips. The response is a draft the owner will inspect before running.`
