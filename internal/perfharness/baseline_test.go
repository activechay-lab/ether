package perfharness

import (
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCgroupProbeSample(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("cpu.stat", "usage_usec 1500\nuser_usec 1000\nsystem_usec 500\n")
	write("io.stat", "8:16 rbytes=100 wbytes=10 rios=1 wios=1 dbytes=0 dios=0\n8:48 rbytes=4096 wbytes=0 rios=1 wios=0 dbytes=0 dios=0\n")
	u, err := CgroupProbe{Dir: dir}.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if u.CPU != 1500*time.Microsecond || u.ReadBytes != 4196 || u.WriteBytes != 10 {
		t.Fatalf("sample = %+v", u)
	}
	// Without the io controller, CPU alone is still measured.
	os.Remove(filepath.Join(dir, "io.stat"))
	if u, err = (CgroupProbe{Dir: dir}).Sample(); err != nil || u.CPU != 1500*time.Microsecond || u.ReadBytes != 0 {
		t.Fatalf("sample without io.stat = %+v, %v", u, err)
	}
}

type fakeProbe struct{ n time.Duration }

func (p *fakeProbe) Sample() (Usage, error) {
	p.n += time.Millisecond
	return Usage{CPU: p.n, ReadBytes: int64(p.n / time.Millisecond)}, nil
}

// A measured replay reaches the configured host, records the decompressed
// body, its size on the wire and gzipped, the container's item counts and
// the probe's delta, and the per-step cold pass restarts before each step.
func TestRunnerMeasureAndColdEach(t *testing.T) {
	body := `{"MediaContainer":{"size":2,"totalSize":40,"Metadata":[{"ratingKey":"7","type":"movie"},{"ratingKey":"8","type":"movie"}]}}`
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("X-Plex-Token"))
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(body))
			_ = zw.Close()
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	r := NewRunner(port, false, []Viewer{{ID: 1, Username: "admin", Owner: true}})
	r.SetHost(host)
	p := &fakeProbe{}
	r.Measure(p)
	c := Client{Name: "plex-test", Surface: "plex", Headers: plexHeaders("Plex Web", "Chrome", "Windows"), Steps: []Step{
		{Name: "list", Class: classBrowse, Path: "/list", Discover: discoverPlexItem("movieKey", "movie")},
		{Name: "detail", Class: classDetail, Path: "/library/metadata/{movieKey}"},
	}}
	r.Sequential(context.Background(), "discover", r.viewers, []Client{c})
	restarts := 0
	if err := r.ColdEach(context.Background(), "cold", r.viewers[0], []Client{c}, func(context.Context, Client, Step) error { restarts++; return nil }); err != nil {
		t.Fatal(err)
	}
	r.Sequential(context.Background(), "warm", r.viewers, []Client{c})
	if restarts != 2 {
		t.Fatalf("restarts = %d, want one per step", restarts)
	}
	for _, tok := range tokens {
		if tok != "" {
			t.Fatalf("a viewer without a token sent X-Plex-Token %q", tok)
		}
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte(body))
	_ = zw.Close()
	for _, res := range r.Results() {
		if res.Err != "" || res.Skipped != "" {
			t.Fatalf("%s %s: %s%s", res.Phase, res.Step, res.Err, res.Skipped)
		}
		if res.Bytes != len(body) || res.WireBytes != zipped.Len() || res.GzipBytes != zipped.Len() {
			t.Fatalf("%s %s: bytes %d wire %d gzip %d, want %d %d %d", res.Phase, res.Step, res.Bytes, res.WireBytes, res.GzipBytes, len(body), zipped.Len(), zipped.Len())
		}
		if !res.Probed || res.CPU != time.Millisecond || res.ReadBytes != 1 || res.Items != 2 || res.TotalSize != 40 {
			t.Fatalf("%s %s: %+v", res.Phase, res.Step, res)
		}
	}
	rows := SummarizeBaseline(r.Results(), nil, []Client{c})
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, row := range rows {
		if row.Status != 200 || row.WarmN != 1 || row.WarmCPU != time.Millisecond || row.ColdCPU != time.Millisecond || row.Items != 2 {
			t.Fatalf("row %+v", row)
		}
	}
	var out bytes.Buffer
	WriteComparison(&out, []Target{{Label: "A", Rows: rows}, {Label: "B", Rows: rows[:1]}}, "plex-test")
	if !strings.Contains(out.String(), "| detail |") || !strings.Contains(out.String(), " – |") {
		t.Fatalf("comparison:\n%s", out.String())
	}
}
