package plex

import (
	"bytes"
	"compress/gzip"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/diagnostics"
)

func TestSupportJournalPrivacyAndProjection(t *testing.T) {
	q := diagnostics.SafeQuery(url.Values{"contentDirectoryID": {"1,2,playlists"}, "count": {"20"}, "password": {"secret"}, "extension": {"192.0.2.1"}, "X-Plex-Token": {"unknown-token"}})
	if !strings.Contains(q, "contentDirectoryID=1%2C2%2Cplaylists") || !strings.Contains(q, "count=20") {
		t.Fatalf("missing safe parameters: %s", q)
	}
	for _, bad := range []string{"secret", "192.0.2.1", "unknown-token"} {
		if strings.Contains(q, bad) {
			t.Fatalf("leaked %q", bad)
		}
	}
	body := []byte(`{"MediaContainer":{"size":1,"Hub":[{"hubIdentifier":"continueWatching","style":"shelf","size":1,"more":false,"Metadata":[{"title":"Example"}]}]}}`)
	s := summarizePlexResponse(body, false, "")
	if s == nil || len(s.Hubs) != 1 || s.Hubs[0].ItemCount != 1 || s.Hubs[0].Identifier != "continueWatching" || s.Hubs[0].Items[0] != "Example" {
		t.Fatalf("summary: %+v", s)
	}
	if s := summarizePlexResponse(body, true, ""); s == nil || !s.Truncated || len(s.Hubs) != 0 {
		t.Fatalf("partial data must be marked: %+v", s)
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if s := summarizePlexResponse(compressed.Bytes(), false, "gzip"); s == nil || len(s.Hubs) != 1 || s.Hubs[0].ItemCount != 1 {
		t.Fatalf("gzip summary: %+v", s)
	}
	decision := summarizePlexResponse([]byte(`{"MediaContainer":{"size":1,"generalDecisionCode":2000,"generalDecisionText":"No sources"}}`), false, "")
	if decision == nil || decision.Decision != "failed" || decision.ErrorCode != "2000" || decision.Error != "No sources" {
		t.Fatalf("decision summary: %+v", decision)
	}
}
