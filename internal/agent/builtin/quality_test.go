package builtin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
)

type qualityProfiles struct {
	*fakeEnv
	order []core.QualityItem
	media string
}

func (f *qualityProfiles) Profiles(context.Context) ([]builtin.ProfileInfo, error) {
	return []builtin.ProfileInfo{
		{Name: "4K", Media: "movie", Scores: map[string]int{"ether:viewer-language": 20000}},
		{Name: "4K", Media: "tv", Scores: map[string]int{"ether:viewer-language": 20000},
			QualityOrder: []core.QualityItem{{Name: "UHD", Qualities: []string{"webdl-2160"}}}, Language: "original", PreferredLanguages: []string{"es"}, MaxHeight: 2160, MinHeight: 720, AllowedQualities: []string{"webdl-2160", "webdl-1080"}},
	}, nil
}

func (f *qualityProfiles) SetQualityOrder(_ context.Context, name, media string, order []core.QualityItem) error {
	f.writes++
	f.order, f.media = order, media
	return nil
}

func qualityTools(t *testing.T) (*qualityProfiles, *tools.Catalogue, tools.Principal) {
	t.Helper()
	f, d := buildDeps()
	profiles := &qualityProfiles{fakeEnv: f}
	d.Profiles = profiles
	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}
	return profiles, cat, tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}
}

func TestQualityProfilesRead(t *testing.T) {
	f, cat, p := qualityTools(t)
	res, err := callTool(t, cat, context.Background(), p, "quality_profiles", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Data)
	var data struct {
		Profiles []struct {
			Name, Media, Language string
			Scores                map[string]int
			QualityOrder          []core.QualityItem
			PreferredLanguages    []string
			MaxHeight, MinHeight  int
			AllowedQualities      []string
		}
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Profiles) != 2 || data.Profiles[1].Media != "tv" || data.Profiles[1].Scores["ether:viewer-language"] != 20000 {
		t.Fatalf("profiles: %s", b)
	}
	row := data.Profiles[1]
	if row.Language != "original" || row.MaxHeight != 2160 || row.MinHeight != 720 || len(row.QualityOrder) != 1 || row.QualityOrder[0].Qualities[0] != "webdl-2160" || !reflect.DeepEqual(row.PreferredLanguages, []string{"es"}) || !reflect.DeepEqual(row.AllowedQualities, []string{"webdl-2160", "webdl-1080"}) {
		t.Fatalf("profile settings: %s", b)
	}
	if f.writes != 0 {
		t.Fatal("read mutated profiles")
	}
}

func TestQualityOrderSetApprovalAndReplay(t *testing.T) {
	f, cat, p := qualityTools(t)
	input := map[string]any{"profile": "4K", "media": "tv", "qualityOrder": []any{map[string]any{"name": "HD and UHD", "qualities": []any{"webdl-2160", "webdl-1080"}}}}
	res, err := callTool(t, cat, context.Background(), p, "quality_order_set", input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposal == nil || f.writes != 0 {
		t.Fatal("must propose without writing")
	}
	// Approval persistence replays JSON, including the exact media target.
	b, _ := json.Marshal(res.Proposal.Input)
	var replay map[string]any
	if err := json.Unmarshal(b, &replay); err != nil {
		t.Fatal(err)
	}
	res, err = callTool(t, cat, context.Background(), p, "quality_order_set", replay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = res.Proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []core.QualityItem{{Name: "HD and UHD", Qualities: []string{"webdl-2160", "webdl-1080"}}}
	if f.writes != 1 || f.media != "tv" || !reflect.DeepEqual(f.order, want) {
		t.Fatalf("applied: %+v", f)
	}
}

func TestQualityOrderSetRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		order any
		media string
		want  string
	}{
		{"missing", nil, "tv", "qualityOrder"},
		{"null", nil, "tv", "qualityOrder"},
		{"empty group", []any{map[string]any{"qualities": []any{}}}, "tv", "qualities"},
		{"unknown", []any{map[string]any{"qualities": []any{"vhs-1080"}}}, "tv", "not a quality"},
		{"too many groups", make([]any, 65), "tv", "64 quality entries"},
		{"wrong type", "webdl-1080", "tv", "qualityOrder"},
		{"unknown field", []any{map[string]any{"qualities": []any{"webdl-1080"}, "extra": true}}, "tv", "qualityOrder"},
		{"long name", []any{map[string]any{"name": strings.Repeat("x", 81), "qualities": []any{"webdl-1080"}}}, "tv", "too long"},
		{"duplicate", []any{map[string]any{"qualities": []any{"webdl-1080", "webdl-1080"}}}, "tv", "twice"},
		{"ambiguous", []any{}, "", "movies and for TV"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, cat, p := qualityTools(t)
			in := map[string]any{"profile": "4K", "qualityOrder": tc.order}
			if tc.name == "missing" {
				delete(in, "qualityOrder")
			}
			if tc.media != "" {
				in["media"] = tc.media
			}
			res, err := callTool(t, cat, context.Background(), p, "quality_order_set", in)
			if err == nil || !strings.Contains(err.Error(), tc.want) || res.Proposal != nil || f.writes != 0 {
				t.Fatalf("result=%+v err=%v writes=%d", res, err, f.writes)
			}
		})
	}
}

func TestQualityOrderSetEmptyOrder(t *testing.T) {
	f, cat, p := qualityTools(t)
	res, err := callTool(t, cat, context.Background(), p, "quality_order_set", map[string]any{"profile": "4K", "media": "tv", "qualityOrder": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = res.Proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.order) != 0 || f.writes != 1 {
		t.Fatal("empty order must rank all qualities equally")
	}
}
