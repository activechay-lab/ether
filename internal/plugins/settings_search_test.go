package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchOptionsForwardQueryAndPreserveValue(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	env := newTestEnv(t)
	rec := env.installHello(t, "1.0.0")
	env.fake.routes["/ether/options/location"] = func(req Request) (Response, error) {
		if req.Locale != "en-US" {
			t.Fatalf("server locale: %q", req.Locale)
		}
		if req.Query["q"] != "Paris, France" || req.Query["locale"] != "fr" {
			t.Fatalf("query: %#v", req.Query)
		}
		return Response{Status: 200, Body: json.RawMessage(`[{"value":"{\"name\":\"Paris\",\"latitude\":48.85,\"longitude\":2.35}","label":"Paris"}]`)}, nil
	}
	out, err := env.m.Options(context.Background(), rec.ID, "location", map[string]string{"q": "Paris, France", "locale": "fr"})
	if err != nil {
		t.Fatal(err)
	}
	var choices []struct{ Value string }
	if err = json.Unmarshal(out, &choices); err != nil {
		t.Fatal(err)
	}
	if _, err = env.m.Configure(context.Background(), rec.ID, map[string]any{"location": choices[0].Value}); err != nil {
		t.Fatal(err)
	}
	settings, _, err := env.m.GetSettings(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settings["location"] != choices[0].Value {
		t.Fatal("selected coordinates changed")
	}
}
