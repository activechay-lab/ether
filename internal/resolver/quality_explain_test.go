package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func explainQuality() config.Quality {
	return config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "At home", MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true},
			{Name: "Away", MaxHeight: 1080},
			{Name: "Small", MaxHeight: 720},
		},
		Default: "At home",
		Remote:  "Away",
	}
}

func appleTV(remote bool) core.ClientCaps {
	return core.ClientCaps{Protocol: "plex", Product: "Plex for Apple TV", Platform: "tvOS", MaxHeight: 2160, HDR10: true, DolbyVision: true, Remote: remote}
}

func androidPhone(remote bool) core.ClientCaps {
	return core.ClientCaps{Protocol: "plex", Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 8", MaxHeight: 1080, Remote: remote}
}

func layer(t *testing.T, a QualityAnswer, kind string) QualityLayer {
	t.Helper()
	for _, l := range a.Layers {
		if l.Kind == kind {
			return l
		}
	}
	t.Fatalf("no %s layer in %+v", kind, a.Layers)
	return QualityLayer{}
}

func decisive(a QualityAnswer) []string {
	var out []string
	for _, l := range a.Layers {
		if l.Decisive {
			out = append(out, l.Kind)
		}
	}
	return out
}

func sameKinds(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// questionMedia is the media scope the question asks about: the title's,
// else the explicit hint, else unknown.
func questionMedia(in QualityQuestion) string {
	if media := titleMedia(in.Title); media != core.MediaAny {
		return media
	}
	return normalizeMedia(in.MediaType)
}

// The checker must agree with SelectProfile, the function playback uses.
func assertMatchesPlayback(t *testing.T, in QualityQuestion, a QualityAnswer) {
	t.Helper()
	p := SelectProfileForMedia(in.Quality, in.Caps, in.User, questionMedia(in))
	if a.Profile != p.Name || a.MaxHeight != p.MaxHeight || a.HDR != p.AllowHDR || a.DolbyVision != p.AllowDolbyVision {
		t.Fatalf("explain %+v disagrees with SelectProfile %s %d hdr=%v dv=%v", a, p.Name, p.MaxHeight, p.AllowHDR, p.AllowDolbyVision)
	}
}

func TestExplainQualityAtHomeUsesDefaultProfile(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: appleTV(false), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 2160 || !a.HDR || !a.DolbyVision || a.BitrateKbps != 0 || a.Blocked {
		t.Fatalf("%+v", a)
	}
	if p := layer(t, a, LayerProfile); p.Source != ProfileFromDefault || p.Reason != "No rule matches, so the At home profile applies." {
		t.Fatalf("%+v", p)
	}
	if !sameKinds(decisive(a), LayerProfile) {
		t.Fatalf("decisive %v", decisive(a))
	}
	for _, l := range a.Layers {
		if l.Kind == LayerRemoteCap {
			t.Fatal("remote cap listed at home")
		}
	}
}

// PR #234: no server bitrate cap by default, and the Away profile decides
// for a remote client no rule covers.
func TestExplainQualityAwayProfileWithoutServerCap(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: appleTV(true), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "Away" || a.MaxHeight != 1080 || a.HDR || a.BitrateKbps != 0 {
		t.Fatalf("%+v", a)
	}
	if p := layer(t, a, LayerProfile); p.Source != ProfileFromRemote || p.MaxHeight != 1080 {
		t.Fatalf("%+v", p)
	}
	if r := layer(t, a, LayerRemoteCap); r.BitrateKbps != 0 || r.Decisive || r.Reason != "Away from home has no bitrate limit." {
		t.Fatalf("%+v", r)
	}
	if !sameKinds(decisive(a), LayerProfile) {
		t.Fatalf("decisive %v", decisive(a))
	}
}

// A viewer's Max quality rule is more specific than "away from home".
func TestExplainQualityViewerRuleBeatsAwayProfile(t *testing.T) {
	q := explainQuality()
	q.Rules = []config.QualityRule{{UserID: 9, Profile: "At home", MaxHeight: 720}}
	in := QualityQuestion{Quality: q, Caps: appleTV(true), User: &core.User{ID: 9}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 720 || !a.HDR {
		t.Fatalf("%+v", a)
	}
	p := layer(t, a, LayerProfile)
	if p.Source != ProfileFromRule || p.Rule != 1 || p.RuleUserID != 9 || !p.BeatsRemote {
		t.Fatalf("%+v", p)
	}
	if h := layer(t, a, LayerRuleHeight); h.MaxHeight != 720 || h.Ignored || !h.Decisive {
		t.Fatalf("%+v", h)
	}
	if !sameKinds(decisive(a), LayerRuleHeight) {
		t.Fatalf("decisive %v", decisive(a))
	}
	// Another viewer on the same app still gets the Away profile.
	other := ExplainQuality(QualityQuestion{Quality: q, Caps: appleTV(true), User: &core.User{ID: 10}})
	if other.Profile != "Away" || other.MaxHeight != 1080 {
		t.Fatalf("%+v", other)
	}
}

func TestExplainQualityDeviceCeilingDecides(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: androidPhone(false), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 1080 || a.HDR {
		t.Fatalf("%+v", a)
	}
	d := layer(t, a, LayerDevice)
	if d.MaxHeight != 1080 || *d.HDR || !d.Decisive || d.Reason != "This app plays up to 1080p, without HDR." {
		t.Fatalf("%+v", d)
	}
	if !sameKinds(decisive(a), LayerDevice) {
		t.Fatalf("decisive %v", decisive(a))
	}
}

func TestExplainQualityOwnerEditedDevice(t *testing.T) {
	builtin := appleTV(false)
	edited := appleTV(false)
	edited.MaxHeight, edited.ExplicitMaxHeight = 1080, true
	q := explainQuality()
	q.Rules = []config.QualityRule{{Product: "Apple TV", Profile: "At home", MaxHeight: 2160}}
	in := QualityQuestion{Quality: q, Caps: edited, Builtin: &builtin, User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.MaxHeight != 1080 {
		t.Fatalf("%+v", a)
	}
	if d := layer(t, a, LayerDevice); !d.Edited || !d.Decisive {
		t.Fatalf("%+v", d)
	}
	if h := layer(t, a, LayerRuleHeight); !h.Ignored || h.Decisive || h.MaxHeight != 2160 {
		t.Fatalf("%+v", h)
	}
}

func TestExplainQualityRuleLiftsAndroidCeiling(t *testing.T) {
	q := explainQuality()
	q.Rules = []config.QualityRule{{Product: "Plex for Android", Profile: "At home"}}
	in := QualityQuestion{Quality: q, Caps: androidPhone(true), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 2160 || !a.HDR {
		t.Fatalf("%+v", a)
	}
	lift := layer(t, a, LayerAndroidLift)
	if lift.MaxHeight != 2160 || lift.HDR == nil || !*lift.HDR {
		t.Fatalf("%+v", lift)
	}
	if p := layer(t, a, LayerProfile); !p.BeatsRemote || !p.Decisive {
		t.Fatalf("%+v", p)
	}
}

func TestExplainQualityBitrateCaps(t *testing.T) {
	q := explainQuality()
	caps := appleTV(true)
	caps.MaxBitrateKbps, caps.DefaultRemoteBitrate = 8000, true // as plex.Request.ClientCapsWith declares it
	a := ExplainQuality(QualityQuestion{Quality: q, Caps: caps, User: &core.User{ID: 7}, RemoteMaxBitrateKbps: 8000})
	if a.BitrateKbps != 8000 {
		t.Fatalf("%+v", a)
	}
	if r := layer(t, a, LayerRemoteCap); !r.Decisive || r.Reason != "Away from home is limited to 8 Mbps." {
		t.Fatalf("%+v", r)
	}
	if !sameKinds(decisive(a), LayerProfile, LayerRemoteCap) {
		t.Fatalf("decisive %v", decisive(a))
	}

	// The viewer's own cap applies when it is lower.
	a = ExplainQuality(QualityQuestion{Quality: q, Caps: caps, User: &core.User{ID: 7}, RemoteMaxBitrateKbps: 8000, ViewerMaxBitrateKbps: 4500})
	if a.BitrateKbps != 4500 || layer(t, a, LayerRemoteCap).Decisive || !layer(t, a, LayerViewerCap).Decisive {
		t.Fatalf("%+v", a)
	}
	if r := layer(t, a, LayerViewerCap).Reason; r != "This viewer is limited to 4.5 Mbps." {
		t.Fatal(r)
	}
	// At home only the viewer's cap counts.
	a = ExplainQuality(QualityQuestion{Quality: q, Caps: appleTV(false), User: &core.User{ID: 7}, RemoteMaxBitrateKbps: 8000, ViewerMaxBitrateKbps: 20000})
	if a.BitrateKbps != 20000 || !layer(t, a, LayerViewerCap).Decisive {
		t.Fatalf("%+v", a)
	}
}

func TestExplainQualityRemoteBlocked(t *testing.T) {
	a := ExplainQuality(QualityQuestion{Quality: explainQuality(), Caps: appleTV(true), User: &core.User{ID: 7}, RemoteDisabled: true})
	if !a.Blocked || !layer(t, a, LayerRemoteBlocked).Decisive {
		t.Fatalf("%+v", a)
	}
	a = ExplainQuality(QualityQuestion{Quality: explainQuality(), Caps: appleTV(false), User: &core.User{ID: 7}, RemoteDisabled: true})
	if a.Blocked {
		t.Fatalf("blocked at home: %+v", a)
	}
}

func TestExplainQualityNoProfilesUsesBuiltin(t *testing.T) {
	in := QualityQuestion{Caps: appleTV(false)}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if p := layer(t, a, LayerProfile); p.Source != ProfileBuiltin || a.MaxHeight != 1080 || !p.Decisive {
		t.Fatalf("%+v", a)
	}
}

func TestExplainQualityAnyResolutionHasNoHeightDecider(t *testing.T) {
	q := config.Quality{Profiles: []core.QualityProfile{{Name: "Any"}}, Default: "Any"}
	caps := appleTV(false)
	caps.MaxHeight = 0
	a := ExplainQuality(QualityQuestion{Quality: q, Caps: caps})
	if a.MaxHeight != 0 || len(decisive(a)) != 0 {
		t.Fatalf("%+v", a)
	}
	if d := layer(t, a, LayerDevice); d.Reason != "This app plays any resolution, with HDR and Dolby Vision." {
		t.Fatal(d.Reason)
	}
}
