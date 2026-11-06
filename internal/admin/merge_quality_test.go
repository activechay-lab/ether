package admin

import (
	"encoding/json"
	"testing"
)

func TestMergeQualitySyncPreservesServerRunState(t *testing.T) {
	current := json.RawMessage(`{"profiles":[],"sync":{"enabled":false,"interval":"24h0m0s","lastRun":"2026-11-05T10:00:00Z","lastError":"old"}}`)
	patch := json.RawMessage(`{"sync":{"enabled":true,"interval":"6h","lastRun":"1999-01-01T00:00:00Z","lastError":"forged"}}`)
	merged, err := mergeSection(current, patch)
	if err != nil {
		t.Fatal(err)
	}
	got := mergeQualitySync(current, merged)
	var out struct {
		Profiles []any `json:"profiles"`
		Sync     struct {
			Enabled   bool   `json:"enabled"`
			Interval  string `json:"interval"`
			LastRun   string `json:"lastRun"`
			LastError string `json:"lastError"`
		} `json:"sync"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Sync.Enabled || out.Sync.Interval != "6h" || out.Sync.LastRun != "2026-11-05T10:00:00Z" || out.Sync.LastError != "old" {
		t.Fatalf("merged sync %+v", out.Sync)
	}
	if out.Profiles == nil {
		t.Fatal("unrelated quality fields were dropped")
	}
}
