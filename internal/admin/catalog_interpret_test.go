package admin_test

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestCatalogInterpret(t *testing.T) {
	status := http.StatusOK
	var output = `{"chips":[{"field":"genre","op":"is","value":"Comedy"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		// Encode model output as the provider's JSON string.
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": output}}}})
	}))
	defer upstream.Close()
	store := newStore(t, func(c *config.Config) {
		seededConfig(c)
		c.SetupComplete = true
		c.LLM.Enabled = true
		c.LLM.Provider = "custom"
		c.LLM.BaseURL = upstream.URL
		c.LLM.Model = "test"
	})
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: interpretHelper{}})
	if rec := serve(h, req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)); rec.Code != 401 {
		t.Fatalf("unauthenticated: %d", rec.Code)
	}
	if rec := serve(h, req("GET", "/ether/api/catalog/interpret", "")); rec.Code != 401 {
		t.Fatalf("capability unauthenticated: %d", rec.Code)
	}
	if rec := serve(h, authed(req("GET", "/ether/api/catalog/interpret", ""))); rec.Code != 200 || rec.Body.String() != "{\"enabled\":true}\n" {
		t.Fatalf("enabled capability: %d %s", rec.Code, rec.Body)
	}
	for _, tt := range []struct {
		output string
		status int
	}{
		{output, 200},
		{`{"chips":[{"field":"sql","op":"is","value":"DROP"}]}`, 502},
		{`{"chips":[{"field":"rating","op":"inject","value":"7"}]}`, 502},
		{`{"chips":[]}`, 502},
		{`not json`, 502},
		{`{"chips":[{"field":"type","op":"is","value":"movie"},{"field":"kids","op":"is","value":"true"}]}`, 502},
		{`{"chips":[{"field":"title","op":"is","value":"cute whimsical girls night"}]}`, 502},
		{`{"chips":[{"field":"genre","op":"is","value":"Whimsical"}]}`, 502},
		{`{"chips":[{"field":"rating","op":"min","value":"eleven"}]}`, 502},
		{`{"chips":[{"field":"rating","op":"min","value":"11"}]}`, 502},
		{`{"chips":[{"field":"genre","op":"min","value":"Comedy"}]}`, 502},
		{`{"chips":[{"field":"genre","op":"is","value":"Comedy","group":4}]}`, 502},
	} {
		output = tt.output
		rec := serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)))
		if rec.Code != tt.status {
			t.Fatalf("status %d want %d: %s", rec.Code, tt.status, rec.Body)
		}
	}
	status = 503
	unavailable := serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":"feel-good movies"}`)))
	if unavailable.Code != 502 || strings.Contains(unavailable.Body.String(), `"chips"`) {
		t.Fatalf("upstream unavailable: %d %s", unavailable.Code, unavailable.Body)
	}
	status = 200
	rec := serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":""}`)))
	if rec.Code != 400 {
		t.Fatalf("empty: %d", rec.Code)
	}
	if err := store.Update(func(c *config.Config) error { c.LLM.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if cap := serve(h, authed(req("GET", "/ether/api/catalog/interpret", ""))); cap.Code != 200 || cap.Body.String() != "{\"enabled\":false}\n" {
		t.Fatalf("disabled capability: %d %s", cap.Code, cap.Body)
	}
	rec = serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)))
	if rec.Code != 501 {
		t.Fatalf("disabled: %d %s", rec.Code, rec.Body)
	}
}

type interpretHelper struct {
	fakeHelper
	pending bool
}

func (h interpretHelper) ConsoleFacets(_ context.Context, _ core.MediaType, facet string) ([]core.FacetValue, bool, error) {
	values := map[string][]string{"genre": {"Comedy", "Romance", "Family", "Animation", "Thriller"}, "language": {"fr", "en"}, "country": {"US", "FR"}, "contentRating": {"G", "PG", "PG-13"}, "studio": {"Pixar"}, "keyword": {"female friendship", "whimsical", "feel-good", "friendship", "magical realism"}, "tag": {"favorites"}}
	var out []core.FacetValue
	if !h.pending {
		for _, v := range values[facet] {
			out = append(out, core.FacetValue{Key: v, Title: v, Count: 1})
		}
	}
	return out, h.pending, nil
}

func TestCatalogInterpretNaturalLanguage(t *testing.T) {
	examples := []struct{ text, output string }{
		{"cute, whimsical girls' night in movies", `{"mode":"and","groupMode":"or","chips":[{"field":"type","op":"is","value":"movie"},{"field":"genre","op":"is","value":"Comedy","group":1},{"field":"genre","op":"is","value":"Romance","group":1},{"field":"keyword","op":"is","value":"female friendship","group":2},{"field":"keyword","op":"is","value":"whimsical","group":2}]}`},
		{"90s thrillers rated over seven under two hours", `{"chips":[{"field":"type","op":"is","value":"movie"},{"field":"decade","op":"is","value":"1990"},{"field":"genre","op":"is","value":"thriller"},{"field":"rating","op":"min","value":"7"},{"field":"runtime","op":"max","value":"120"}]}`},
		{"French romantic movies", `{"chips":[{"field":"type","op":"is","value":"movie"},{"field":"language","op":"is","value":"fr"},{"field":"genre","op":"is","value":"Romance"}]}`},
		{"family-friendly animated movies", `{"chips":[{"field":"type","op":"is","value":"movie"},{"field":"genre","op":"is","value":"Animation"},{"field":"certification","op":"is","value":"G","group":1},{"field":"certification","op":"is","value":"PG","group":1}]}`},
		{"quietly surreal moonlit adventures", `{"chips":[{"field":"type","op":"is","value":"movie"},{"field":"keyword","op":"is","value":"surrealism"}]}`},
	}
	for _, example := range examples {
		t.Run(example.text, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Messages []struct{ Role, Content string }
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 2 || body.Messages[1].Content != example.text {
					t.Error("model did not receive actual user query")
				}
				if !strings.Contains(body.Messages[0].Content, `female friendship`) || !strings.Contains(body.Messages[0].Content, `There is NO title field`) {
					t.Error("missing actual vocabulary or no-title instruction")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": example.output}}}})
			}))
			defer upstream.Close()
			store := newStore(t, func(c *config.Config) {
				seededConfig(c)
				c.SetupComplete = true
				c.LLM.Enabled = true
				c.LLM.Provider = "custom"
				c.LLM.BaseURL = upstream.URL
				c.LLM.Model = "test"
			})
			h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: interpretHelper{}})
			input, _ := json.Marshal(map[string]string{"text": example.text})
			rec := serve(h, authed(req("POST", "/ether/api/catalog/interpret", string(input))))
			if rec.Code != 200 || calls != 1 {
				t.Fatalf("response %d calls%d: %s", rec.Code, calls, rec.Body)
			}
			var result struct {
				Chips []struct {
					Field, Op, Value string
					Group            int
				}
				Mode, GroupMode string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Chips) == 0 || result.Mode != "and" || result.GroupMode != "or" {
				t.Fatalf("invalid draft: %s", rec.Body)
			}
			for _, c := range result.Chips {
				if c.Field == "title" || c.Value == example.text {
					t.Fatal("silently degraded to literal search")
				}
			}
			cold := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: interpretHelper{pending: true}})
			rec = serve(cold, authed(req("POST", "/ether/api/catalog/interpret", string(input))))
			if rec.Code != 503 || calls != 1 {
				t.Fatalf("cold vocabulary must be explicit and must not invoke model: %d calls%d", rec.Code, calls)
			}
		})
	}
}
