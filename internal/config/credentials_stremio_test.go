package config

import (
	"strings"
	"testing"
)

func TestStremioIndexerURLSealedAtRest(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	doc := []byte(`{"indexers":[
{"id":"s1","kind":"stremio","url":"https://addon.example.com/SECRETKEYSEGMENT/manifest.json"},
{"id":"n1","url":"https://idx.example.com/api","apiKey":"news-key"}
]}`)
	sealed, err := sealDocument(doc, key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "SECRETKEYSEGMENT") {
		t.Fatalf("stremio URL stored in plaintext: %s", sealed)
	}
	if strings.Contains(string(sealed), "news-key") {
		t.Fatalf("apiKey stored in plaintext: %s", sealed)
	}
	if !strings.Contains(string(sealed), "https://idx.example.com/api") {
		t.Fatalf("newznab URL should stay plaintext: %s", sealed)
	}
	opened, _, err := openDocument(sealed, key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(opened), "SECRETKEYSEGMENT") || !strings.Contains(string(opened), "news-key") {
		t.Fatalf("round trip lost values: %s", opened)
	}
	// Destination binding: moving the sealed URL to another indexer id
	// (path change) must not open.
	moved := strings.Replace(string(sealed), `"id": "s1"`, `"id": "s2"`, 1)
	if moved == string(sealed) {
		moved = strings.Replace(string(sealed), `"id":"s1"`, `"id":"s2"`, 1)
	}
	if _, _, err := openDocument([]byte(moved), key); err == nil {
		t.Fatal("sealed URL opened under a different indexer id")
	}
}

func TestProtectedValuesIncludeStremioURL(t *testing.T) {
	cfg := Config{Indexers: []Indexer{{ID: "s1", Kind: IndexerKindStremio, URL: "https://a.example/SECRETSEG/manifest.json"}}}
	found := false
	for _, v := range ProtectedValues(cfg) {
		if strings.Contains(v, "SECRETSEG") {
			found = true
		}
	}
	if !found {
		t.Fatal("ProtectedValues missed the Stremio install URL")
	}
}
