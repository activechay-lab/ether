package plex

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// flexID decodes an id rendered as a JSON number or string (decision items
// carry string-typed ids, as PMS does).
type flexID int64

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	*f = flexID(n)
	return err
}

type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*f = flexBool(s == "1" || s == "true")
	return nil
}

type wireStream struct {
	ID         flexID   `json:"id"`
	StreamType flexID   `json:"streamType"`
	Selected   flexBool `json:"selected"`
}

type wirePart struct {
	ID     flexID       `json:"id"`
	Key    string       `json:"key"`
	Stream []wireStream `json:"Stream"`
}

type wireMedia struct {
	ID       flexID     `json:"id"`
	Selected flexBool   `json:"selected"`
	Part     []wirePart `json:"Part"`
}

func decodeWireMedia(t *testing.T, stage string, rec interface{ Bytes() []byte }) []wireMedia {
	t.Helper()
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				Media []wireMedia `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Bytes(), &body); err != nil || len(body.MediaContainer.Metadata) != 1 {
		t.Fatalf("%s: %v %s", stage, err, rec.Bytes())
	}
	return body.MediaContainer.Metadata[0].Media
}

// Plex Web and Plex for macOS build the player settings panel from the
// Media, Part and Stream ids they hold (#267). Within one playback session
// those ids must keep naming the same things whatever happens behind them:
// a background refill or cache expiry changing the version list, the
// playing release being probed, and a failover to another release. The
// metadata, the decision and /status/sessions must agree on them.
func TestPlayIdentityStableAcrossRefillProbeFailover(t *testing.T) {
	f, pb := newVariantPlaybackFixture(t)
	vr := f.resolver
	// The session opens on the 4K release's estimate; its probe lands later.
	estimate := testVariantLists()[fightClub.Key][1].Info
	pb.mu.Lock()
	pb.info = estimate
	pb.mu.Unlock()

	path := "/library/metadata/" + fightClub.Key.String()
	web := []string{"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome", "Accept", "application/json"}
	fetch := func(stage string) []wireMedia {
		t.Helper()
		rec := f.get(t, path, web...)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", stage, rec.Code)
		}
		return decodeWireMedia(t, stage, rec.Body)
	}
	decision := func(stage string) wireMedia {
		t.Helper()
		rec := f.get(t, "/video/:/transcode/universal/decision?path="+urlEsc(path)+"&mediaIndex=1&partIndex=0&session=web267b&hasMDE=1&directPlay=1&directStream=1&protocol=dash", web...)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: decision status %d %s", stage, rec.Code, rec.Body.String())
		}
		ms := decodeWireMedia(t, stage+" decision", rec.Body)
		if len(ms) != 1 || len(ms[0].Part) != 1 {
			t.Fatalf("%s: decision media %+v", stage, ms)
		}
		return ms[0]
	}
	sessionMedia := func(stage string) wireMedia {
		t.Helper()
		rec := f.get(t, "/status/sessions", "Accept", "application/json")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: sessions status %d", stage, rec.Code)
		}
		ms := decodeWireMedia(t, stage+" sessions", rec.Body)
		if len(ms) != 1 || len(ms[0].Part) != 1 {
			t.Fatalf("%s: session media %+v", stage, ms)
		}
		return ms[0]
	}

	before := fetch("before playback")
	if len(before) != 3 {
		t.Fatalf("before playback: %d media, want 3", len(before))
	}
	// Everything the client holds for the version it plays, by type.
	played := before[1]
	held := map[flexID]flexID{}
	heldAudio := flexID(0)
	for _, st := range played.Part[0].Stream {
		held[st.ID] = st.StreamType
		if st.StreamType == 2 && st.Selected {
			heldAudio = st.ID
		}
	}
	dec := decision("admission")
	// A streamed decision has no part key, as on PMS; a direct-play one
	// names the key the client already holds.
	if dec.ID != played.ID || dec.Part[0].ID != played.Part[0].ID || (dec.Part[0].Key != "" && dec.Part[0].Key != played.Part[0].Key) {
		t.Fatalf("decision names Media %d part %d %q, the client plays %d part %d %q",
			dec.ID, dec.Part[0].ID, dec.Part[0].Key, played.ID, played.Part[0].ID, played.Part[0].Key)
	}
	for _, st := range dec.Part[0].Stream {
		held[st.ID] = st.StreamType
	}
	if rec := f.get(t, "/:/timeline?ratingKey="+fightClub.Key.String()+"&key="+urlEsc(path)+"&state=playing&time=120000&duration=8340000", web[:4]...); rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d", rec.Code)
	}

	check := func(stage string) {
		t.Helper()
		during := fetch(stage)
		if len(during) != len(before) {
			t.Fatalf("%s: %d media, the client holds %d", stage, len(during), len(before))
		}
		var selected []int
		for i := range before {
			if during[i].ID != before[i].ID || during[i].Part[0].ID != before[i].Part[0].ID || during[i].Part[0].Key != before[i].Part[0].Key {
				t.Errorf("%s: Media[%d] %d/%d %q -> %d/%d %q", stage, i, before[i].ID, before[i].Part[0].ID, before[i].Part[0].Key,
					during[i].ID, during[i].Part[0].ID, during[i].Part[0].Key)
			}
			if during[i].Selected {
				selected = append(selected, i)
			}
		}
		if len(selected) != 1 || selected[0] != 1 {
			t.Errorf("%s: selected Media %v, want only the playing version [1]", stage, selected)
		}
		listed := map[flexID]flexID{}
		var selectedAudio []flexID
		for _, st := range during[1].Part[0].Stream {
			listed[st.ID] = st.StreamType
			if st.StreamType == 2 && st.Selected {
				selectedAudio = append(selectedAudio, st.ID)
			}
		}
		for id, typ := range held {
			if got, ok := listed[id]; !ok || got != typ {
				t.Errorf("%s: held stream %d (type %d) missing from the playing Part: %+v", stage, id, typ, during[1].Part[0].Stream)
			}
		}
		if len(selectedAudio) != 1 || selectedAudio[0] != heldAudio {
			t.Errorf("%s: selected audio %v, the client holds %d", stage, selectedAudio, heldAudio)
		}
		for _, st := range during[1].Part[0].Stream {
			held[st.ID] = st.StreamType
		}
		for _, src := range []struct {
			name string
			m    wireMedia
		}{{"decision", decision(stage)}, {"sessions", sessionMedia(stage)}} {
			if src.m.ID != played.ID || src.m.Part[0].ID != played.Part[0].ID {
				t.Errorf("%s: %s names Media %d part %d, the client plays %d", stage, src.name, src.m.ID, src.m.Part[0].ID, played.ID)
			}
			if src.name == "decision" && src.m.Part[0].Key != "" && src.m.Part[0].Key != played.Part[0].Key {
				t.Errorf("%s: decision part key %q, the client holds %q", stage, src.m.Part[0].Key, played.Part[0].Key)
			}
			for _, st := range src.m.Part[0].Stream {
				if _, ok := listed[st.ID]; !ok {
					t.Errorf("%s: %s stream %d not in the metadata's playing Part", stage, src.name, st.ID)
				}
			}
		}
	}
	check("during playback")

	// A background refill lands: a new same-quality version slots in
	// ahead of the playing one, so the resolver's list grows and reorders.
	vr.mu.Lock()
	list := vr.byKey[fightClub.Key]
	refill := mkVariant("rel-refill", "1080p WEB-DL", "", false, 1080, "h264", "eac3", false, 7000)
	vr.byKey[fightClub.Key] = []core.ReleaseVariant{list[0], refill, list[1], list[2]}
	vr.mu.Unlock()
	check("after refill")

	// The playing release is probed: its estimated tracks become real ones.
	setSession := func(release string, info core.MediaInfo) {
		pb.mu.Lock()
		defer pb.mu.Unlock()
		for _, ps := range pb.sessions {
			ps.Info = info
			if release != "" {
				ps.Resolution = &core.Resolution{Title: fightClub.Key, Release: core.Release{ID: release}}
			}
		}
	}
	setSession("", sampleInfo)
	check("after probe")

	// The cached search expires: the resolver has nothing for the title.
	vr.mu.Lock()
	vr.byKey[fightClub.Key] = nil
	vr.mu.Unlock()
	check("after cache expiry")

	// The session fails over to a release outside the list, with another
	// container and differently described tracks.
	failover := core.MediaInfo{
		Container: "mp4", Duration: 139 * time.Minute, Size: 6_000_000_000, BitrateKbps: 9000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "audio", Codec: "aac", Language: "fr", Channels: 2, SampleRate: 48000},
			{Index: 1, Type: "video", Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FrameRate: 23.976, BitDepth: 10},
			{Index: 2, Type: "audio", Codec: "ac3", Language: "en", Channels: 6, SampleRate: 48000, Title: "Surround", Default: true},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "en", Title: "SDH"},
			sampleInfo.Streams[4], // the viewer's sidecar follows the title
		},
	}
	setSession("rel-failover", failover)
	check("after failover")
}

// Issue #389: once a client has been issued a version list, no later
// bookkeeping may shrink or erase it while the identity lives — an empty
// shared-flow snapshot (row expired or never offered), a thaw after the
// pre-play freeze lapses, or a re-resolve against an expired search
// cache. The client addresses Parts and mediaIndex positions from the
// list it holds; a shorter list strands both.
func TestIssuedVersionListNeverShrinks(t *testing.T) {
	p := newPlayIdentities()
	k := playIdentityKey{userID: 7, clientID: "tv", key: fightClub.Key}
	full := testVariantLists()[fightClub.Key]
	now := fixedNow
	p.issueList(k, full, false, now)
	p.installVersionFlow(k, nil, now.Add(time.Minute), now)
	p.installVersionFlow(k, full[:1], now.Add(time.Minute), now)
	// Thaw after the freeze lapses, then a re-resolve that finds only Auto.
	pin := p.pinned(k, false, now.Add(2*time.Minute))
	if pin.frozen {
		t.Fatal("flow did not thaw")
	}
	p.issueList(k, full[:1], false, now.Add(2*time.Minute))
	pin = p.pinned(k, false, now.Add(2*time.Minute))
	if len(pin.variants) != len(full) {
		t.Fatalf("issued list shrank to %d, want %d", len(pin.variants), len(full))
	}
	for i, v := range pin.variants {
		if v.Release.ID != full[i].Release.ID {
			t.Fatalf("position %d became %q, want %q", i, v.Release.ID, full[i].Release.ID)
		}
	}
	if ids, ok := p.releases(k, now.Add(2*time.Minute)); !ok || len(ids) != len(full) || ids[1] != full[1].Release.ID {
		t.Fatalf("releases %v issued=%v", ids, ok)
	}
	// An empty snapshot on a fresh identity issues nothing: a mediaIndex
	// with no issued list still refuses (TestMediaIndexWithoutIssuedList).
	fresh := playIdentityKey{userID: 8, clientID: "tv", key: fightClub.Key}
	p.installVersionFlow(fresh, nil, now.Add(time.Minute), now)
	if _, ok := p.releases(fresh, now); ok {
		t.Fatal("empty snapshot must not issue a list")
	}
}
