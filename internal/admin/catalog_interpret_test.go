package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestCatalogInterpret(t *testing.T) {
	var output = `{"chips":[{"field":"genre","op":"is","value":"Comedy"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store)})
	if rec := serve(h, req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)); rec.Code != 401 {
		t.Fatalf("unauthenticated: %d", rec.Code)
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
	} {
		output = tt.output
		rec := serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)))
		if rec.Code != tt.status {
			t.Fatalf("status %d want %d: %s", rec.Code, tt.status, rec.Body)
		}
	}
	rec := serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":""}`)))
	if rec.Code != 400 {
		t.Fatalf("empty: %d", rec.Code)
	}
	if err := store.Update(func(c *config.Config) error { c.LLM.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	rec = serve(h, authed(req("POST", "/ether/api/catalog/interpret", `{"text":"comedies"}`)))
	if rec.Code != 501 {
		t.Fatalf("disabled: %d %s", rec.Code, rec.Body)
	}
}
