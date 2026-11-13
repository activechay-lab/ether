package sessions

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestScopedSubtitleExtractionAndSidecars(t *testing.T) {
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 1}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Playback: p, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Events: events,
		OpenSubtitle: func(id, format string) (io.ReadCloser, string, error) {
			if id != "ether-subtitle:cached" {
				return nil, "", core.ErrNotFound
			}
			return io.NopCloser(strings.NewReader("sidecar")), format, nil
		},
		ExtractSubtitle: func(_ context.Context, input string, index int, codec string, w io.Writer) error {
			if !strings.HasPrefix(input, "http://loopback/") || index != 3 || codec != "srt" {
				t.Fatal("wrong extraction input")
			}
			_, err := io.WriteString(w, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n")
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 2}
	device := core.Device{ID: "tv"}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	for _, ps := range p.sessions {
		ps.Info.Streams = []core.StreamInfo{{Index: 3, Type: "subtitle", Codec: "srt"}, {Index: 4, Type: "subtitle", Codec: "srt", External: true, ExternalURL: "ether-subtitle:cached"}, {Index: 5, Type: "subtitle", Codec: "pgs"}, {Index: 6, Type: "subtitle", Codec: "srt", External: true, ExternalURL: "https://secret.invalid/?token=credential"}}
	}
	p.mu.Unlock()
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}
	path, err := b.SubtitleURL(ctx, access, session.ID, 3, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		b.ServeMedia(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if got := get(path); got.Code != 200 || !strings.Contains(got.Body.String(), "WEBVTT") {
		t.Fatalf("embedded %d %q", got.Code, got.Body.String())
	}
	srt, err := b.SubtitleURL(ctx, access, session.ID, 3, "srt")
	if err != nil {
		t.Fatal(err)
	}
	if got := get(srt); got.Code != 200 || !strings.Contains(got.Body.String(), "00:00:01,000") {
		t.Fatalf("srt %d %q", got.Code, got.Body.String())
	}
	sidecar, err := b.SubtitleURL(ctx, access, session.ID, 4, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	if got := get(sidecar); got.Code != 200 || got.Body.String() != "sidecar" {
		t.Fatalf("sidecar %d %q", got.Code, got.Body.String())
	}
	for _, index := range []int{5, 6, 99} {
		if _, err := b.SubtitleURL(ctx, access, session.ID, index, "vtt"); !errors.Is(err, core.ErrNotAvailable) {
			t.Fatalf("index %d: %v", index, err)
		}
	}
	if _, err := b.SubtitleURL(ctx, frontend.SessionAccess{Viewer: &core.Viewer{ID: 3}, Device: device, Protocol: "jellyfin"}, session.ID, 3, "vtt"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("foreign viewer: %v", err)
	}
	wrong := httptest.NewRecorder()
	BindSurface(b, "emby").ServeMedia(wrong, httptest.NewRequest(http.MethodGet, path, nil))
	if wrong.Code != 404 {
		t.Fatal("cross-surface subtitle")
	}
	if strings.Contains(path, "credential") || strings.Contains(sidecar, "ether-subtitle") {
		t.Fatal("upstream credential leaked")
	}
	if err := b.Stop(ctx, access, session.ID); err != nil {
		t.Fatal(err)
	}
	if got := get(path); got.Code != 404 {
		t.Fatal("stopped grant remained valid")
	}
}

// A sidecar serves with its stored timing: correction is measured only when
// a viewer picks it (subtitles.StartCorrection), never on serve. A manual
// shift still applies.
func TestExternalSubtitleServesStoredTimingAndManualShift(t *testing.T) {
	ctx := context.Background()
	p := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 1}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	sidecar := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello there friend\n\n00:00:03.000 --> 00:00:04.000\nSecond matching line\n\n00:00:05.000 --> 00:00:06.000\nThird matching line\n"
	reference := "WEBVTT\n\n00:00:03.000 --> 00:00:04.000\nHello there friend\n\n00:00:05.000 --> 00:00:06.000\nSecond matching line\n\n00:00:07.000 --> 00:00:08.000\nThird matching line\n"
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Playback: p, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Events: events,
		OpenSubtitle: func(id, format string) (io.ReadCloser, string, error) {
			if id != "ether-subtitle:cached" {
				return nil, "", core.ErrNotFound
			}
			return io.NopCloser(strings.NewReader(sidecar)), format, nil
		},
		ExtractSubtitle: func(_ context.Context, _ string, index int, _ string, w io.Writer) error {
			if index != 1 {
				t.Fatal(index)
			}
			_, err := io.WriteString(w, reference)
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 2}
	device := core.Device{ID: "tv"}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	for _, ps := range p.sessions {
		ps.Info.Streams = []core.StreamInfo{{Index: 1, Type: "subtitle", Codec: "srt"}, {Index: 2, Type: "subtitle", Codec: "srt", External: true, ExternalURL: "ether-subtitle:cached"}}
	}
	p.mu.Unlock()
	path, err := b.SubtitleURL(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}, session.ID, 2, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	b.ServeMedia(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "00:00:01.000") {
		t.Fatalf("stored timing %d %s", w.Code, w.Body.String())
	}
	shifted := httptest.NewRecorder()
	b.ServeMedia(shifted, httptest.NewRequest(http.MethodGet, path+"?sync_ms=1000", nil))
	if shifted.Code != 200 || !strings.Contains(shifted.Body.String(), "00:00:02.000 --> 00:00:03.000") {
		t.Fatalf("sync %d %s", shifted.Code, shifted.Body.String())
	}
}

type fakeResourcePlayback struct {
	*fakePlayback
	mkvBytes    []byte
	resourceErr error
}

func (f *fakeResourcePlayback) WithSourceResource(ctx context.Context, id string, revision string, fn func(ctx context.Context, src io.ReaderAt, size int64) error) error {
	if f.resourceErr != nil {
		return f.resourceErr
	}
	ps, ok := f.Session(id)
	if !ok {
		return core.ErrNotFound
	}
	if revision != "" && sourceRevision(ps) != revision {
		return core.ErrSourceChanged
	}
	return fn(ctx, bytes.NewReader(f.mkvBytes), int64(len(f.mkvBytes)))
}

func testEBMLVInt(size uint64) []byte {
	for width := 1; width <= 8; width++ {
		marker := uint64(1) << uint(7*width)
		if size < marker-1 {
			val := marker | size
			out := make([]byte, width)
			for i := width - 1; i >= 0; i-- {
				out[i] = byte(val & 0xFF)
				val >>= 8
			}
			return out
		}
	}
	panic("size too large")
}

func testEBMLID(id uint64) []byte {
	switch {
	case id <= 0xFF:
		return []byte{byte(id)}
	case id <= 0xFFFF:
		return []byte{byte(id >> 8), byte(id)}
	case id <= 0xFFFFFF:
		return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	default:
		return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	}
}

func testEBMLNode(id uint64, payload []byte) []byte {
	var out []byte
	out = append(out, testEBMLID(id)...)
	out = append(out, testEBMLVInt(uint64(len(payload)))...)
	out = append(out, payload...)
	return out
}

func testEBMLUint(id uint64, v uint64) []byte {
	return testEBMLNode(id, []byte{byte(v)})
}

func TestSessionSubtitleArtifactsHLSAndAuthoredFidelity(t *testing.T) {
	ctx := context.Background()
	fontPayload := []byte("TTF-FONT-PAYLOAD-0123456789")
	assHeader := "[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Default,CustomFont,48\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"

	var attachedFile []byte
	attachedFile = append(attachedFile, testEBMLNode(0x467E, []byte("CustomFont"))...)
	attachedFile = append(attachedFile, testEBMLNode(0x466E, []byte("../evil/CustomFont.ttf"))...)
	attachedFile = append(attachedFile, testEBMLNode(0x4660, []byte("font/ttf"))...)
	attachedFile = append(attachedFile, testEBMLNode(0x465C, fontPayload)...)
	attachments := testEBMLNode(0x1941A469, testEBMLNode(0x61A7, attachedFile))

	trackEntry := func(num uint64, ttype uint64, codec string, priv []byte) []byte {
		var body []byte
		body = append(body, testEBMLUint(0xD7, num)...)
		body = append(body, testEBMLUint(0x83, ttype)...)
		body = append(body, testEBMLNode(0x86, []byte(codec))...)
		if len(priv) > 0 {
			body = append(body, testEBMLNode(0x63A2, priv)...)
		}
		return testEBMLNode(0xAE, body)
	}
	var tracksPayload []byte
	tracksPayload = append(tracksPayload, trackEntry(1, 1, "V_MPEG4/ISO/AVC", nil)...)
	tracksPayload = append(tracksPayload, trackEntry(2, 2, "A_AAC", nil)...)
	tracksPayload = append(tracksPayload, trackEntry(3, 0x11, "S_TEXT/ASS", []byte(assHeader))...)
	tracks := testEBMLNode(0x1654AE6B, tracksPayload)

	var simpleBlock []byte
	simpleBlock = append(simpleBlock, 0x83, 0x00, 0x00, 0x00)
	simpleBlock = append(simpleBlock, []byte("0,0,Default,,0,0,0,,Hello {\\fnHeaderFont}World")...)
	var clusterPayload []byte
	clusterPayload = append(clusterPayload, testEBMLUint(0xE7, 100)...)
	clusterPayload = append(clusterPayload, testEBMLNode(0xA3, simpleBlock)...)
	cluster := testEBMLNode(0x1F43B675, clusterPayload)

	var segPayload []byte
	segPayload = append(segPayload, attachments...)
	segPayload = append(segPayload, tracks...)
	segPayload = append(segPayload, cluster...)
	ebmlHeader := testEBMLNode(0x1A45DFA3, []byte{0x42, 0x82, 0x88, 'm', 'a', 't', 'r', 'o', 's', 'k', 'a'})
	completeMKV := append(append([]byte(nil), ebmlHeader...), testEBMLNode(0x18538067, segPayload)...)

	fp := &fakeResourcePlayback{
		fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 1},
		mkvBytes:     completeMKV,
	}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()

	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy,
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k}, nil
		},
		Playback:     fp,
		Transcoder:   &fakeLadderTranscoder{},
		Progress:     &fakeProgress{},
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Events:       events,
		ExtractSubtitle: func(_ context.Context, _ string, _ int, _ string, w io.Writer) error {
			_, err := io.WriteString(w, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nFallback VTT line\n")
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	viewer := &core.Viewer{ID: 7}
	device := core.Device{ID: "tv"}
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "silo"}
	surf := BindSurface(b, "silo").(frontend.SessionSubtitleArtifacts)

	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "silo", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "silo", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	for _, ps := range fp.sessions {
		ps.Info = core.MediaInfo{
			Container: "mkv",
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264"},
				{Index: 1, Type: "audio", Codec: "aac"},
				{Index: 2, Type: "subtitle", Codec: "ass", Language: "eng"},
			},
		}
	}
	fp.mu.Unlock()

	// Start stream as HLS (where ServeSessionMedia(..., "file") is not available).
	if _, err := b.StartStream(ctx, access, session.ID, frontend.StreamRequest{Format: frontend.StreamHLS}); err != nil {
		t.Fatal(err)
	}

	if !surf.HasAuthoredASSSubtitle(ctx, access, session.ID, 2) {
		t.Fatal("expected HasAuthoredASSSubtitle=true for complete MKV under StreamHLS")
	}
	artifact, err := surf.ServeASSSubtitle(ctx, access, session.ID, 2, 500, true)
	if err != nil || !artifact.Authored || !strings.Contains(string(artifact.Data), "Dialogue: 0,0:00:00.60,") {
		t.Fatalf("ServeASSSubtitle under StreamHLS failed: err=%v authored=%v body=%q", err, artifact.Authored, string(artifact.Data))
	}
	bundle, err := surf.SubtitleFontBundle(ctx, access, session.ID, 2)
	if err != nil || len(bundle) != 1 || bundle[0].Name != "CustomFont.ttf" || bundle[0].Data != base64.StdEncoding.EncodeToString(fontPayload) {
		t.Fatalf("SubtitleFontBundle under StreamHLS failed: err=%v bundle=%+v", err, bundle)
	}
	att, err := surf.SubtitleFontAttachment(ctx, access, session.ID, 2, 0)
	if err != nil || att.Name != "CustomFont.ttf" || !bytes.Equal(att.Data, fontPayload) {
		t.Fatalf("SubtitleFontAttachment under StreamHLS failed: err=%v att=%+v", err, att)
	}

	// Corrupt/truncate the MKV cluster so embedded ASS is incomplete.
	fp.mkvBytes = completeMKV[:len(completeMKV)-12]
	if surf.HasAuthoredASSSubtitle(ctx, access, session.ID, 2) {
		t.Fatal("expected HasAuthoredASSSubtitle=false when MKV cluster is truncated")
	}
	if _, err := surf.ServeASSSubtitle(ctx, access, session.ID, 2, 0, true); !errors.Is(err, frontend.ErrSubtitleAuthoringUnavailable) {
		t.Fatalf("expected ErrSubtitleAuthoringUnavailable when requireAuthored=true on truncated MKV, got %v", err)
	}
	if _, err := surf.ServeASSSubtitle(ctx, access, session.ID, 2, 0, false); !errors.Is(err, frontend.ErrSubtitleAuthoringUnavailable) {
		t.Fatalf("incomplete authored track must not reopen an unfenced source: %v", err)
	}
	fp.mkvBytes = completeMKV
	for _, denied := range []error{context.Canceled, context.DeadlineExceeded, core.ErrUnauthorized, core.ErrNotAvailable} {
		fp.resourceErr = denied
		if _, err := surf.ServeASSSubtitle(ctx, access, session.ID, 2, 0, false); !errors.Is(err, denied) {
			t.Fatalf("source denial %v replaced by ASS fallback: %v", denied, err)
		}
		if _, err := surf.SubtitleFontBundle(ctx, access, session.ID, 2); !errors.Is(err, denied) {
			t.Fatalf("source denial %v replaced by empty font success: %v", denied, err)
		}
	}
}
