package livetv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/extend"
)

func TestSourceKindClassification(t *testing.T) {
	reg := NewKindRegistry()
	cases := []struct {
		name string
		src  Source
		want string
	}{
		{"m3u url", Source{PlaylistURL: "https://x.test/p.m3u"}, "m3u"},
		{"legacy stremio waits for its migration", Source{Kind: "stremio", PlaylistURL: "https://x.test/manifest.json"}, ""},
		{"plugin source whose plugin is off", Source{Kind: "plugin:addon.x/channels", PlaylistURL: "plugin:addon.x/channels"}, ""},
		{"m3u upload", Source{Playlist: "#EXTM3U"}, "m3u"},
		{"guide only url", Source{GuideURL: "https://x.test/g.xml"}, "guideOnly"},
		{"guide only upload", Source{Guide: "<tv/>"}, "guideOnly"},
		{"xtream beats derived urls", Source{PlaylistURL: "https://x.test/get.php", GuideURL: "https://x.test/xmltv.php",
			Auth: &SourceAuth{Type: "xtream", Server: "https://x.test"}}, "xtream"},
		{"empty", Source{}, ""},
	}
	for _, c := range cases {
		k, ok := reg.Classify(c.src)
		got := ""
		if ok {
			got = k.ID()
		}
		if got != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLiveTVSourceDescriptors(t *testing.T) {
	ds := BuiltinDescriptors()
	if len(ds) != 5 {
		t.Fatalf("descriptors = %d, want 5", len(ds))
	}
	byID := map[string]extend.Descriptor{}
	for _, d := range ds {
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.ID, err)
		}
		byID[d.ID] = d
	}
	meta, err := extend.MetaOf[extend.LiveTVSourceMeta](byID["guideOnly"])
	if err != nil {
		t.Fatal(err)
	}
	if meta.YieldsChannels || !meta.YieldsGuide {
		t.Errorf("guideOnly meta = %+v", meta)
	}
	meta, _ = extend.MetaOf[extend.LiveTVSourceMeta](byID["xtream"])
	if !meta.YieldsChannels || meta.Editor != "xtream" {
		t.Errorf("xtream meta = %+v", meta)
	}
	// Password fields are secret-kind so the schema form never echoes them.
	found := false
	for _, p := range meta.Params {
		if p.Name == "password" {
			found = p.Kind == "secret"
		}
	}
	if !found {
		t.Error("xtream password param is not a secret field")
	}
}

type deadlineSource struct{ deadline time.Time }

func (*deadlineSource) ID() string               { return "plugin:test/deadline" }
func (*deadlineSource) Describe() SourceDescribe { return SourceDescribe{} }
func (*deadlineSource) Matches(s Source) bool    { return s.Kind == "plugin:test/deadline" }
func (d *deadlineSource) Fetch(ctx context.Context, _ *Service, _ *Source) (Fetched, error) {
	d.deadline, _ = ctx.Deadline()
	return Fetched{}, context.Canceled
}
func TestManualImportHasTotalBudget(t *testing.T) {
	s := testService(t, Options{})
	kind := &deadlineSource{}
	if err := s.RegisterSourceKind(kind); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []time.Duration{0, time.Second} {
		ctx := context.Background()
		cancel := func() {}
		if budget > 0 {
			ctx, cancel = context.WithTimeout(ctx, budget)
		}
		started := time.Now()
		_, err := s.ImportSource(ctx, Source{Name: "Deadline", Kind: kind.ID()})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		max := refreshBudget
		if budget > 0 {
			max = budget
		}
		if kind.deadline.IsZero() || kind.deadline.After(started.Add(max+time.Second)) || kind.deadline.Before(started.Add(max-time.Second)) {
			t.Fatalf("deadline %s, budget %s", kind.deadline, max)
		}
	}
}
