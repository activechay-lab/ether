package livetv

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// The cross-component contract: a slate occupies a normal programme track on
// both transports, including early requests, mid-slot tunes and rollover.
func TestOffAirPlayout(t *testing.T) {
	for _, dash := range []bool{false, true} {
		for _, join := range []int64{56, 68} {
			name := "hls"
			if dash {
				name = "dash"
			}
			t.Run(name+time.Unix(join, 0).Format("05"), func(t *testing.T) {
				base := time.Date(2026, 11, 5, 5, 0, 0, 0, time.UTC)
				now := base.Add(time.Duration(join) * time.Second)
				pb, tc := &fakePlayback{}, &fakeTranscoder{}
				s := testService(t, Options{Now: func() time.Time { return now }, Playback: pb, Transcoder: tc})
				c := testChannel()
				c.Items = nil
				c.OffAir = true
				c.Timezone = "America/Toronto"
				c.Blocks = []Block{{Start: 0, End: 1, Items: []Item{{Key: core.MovieKey(1), Title: "Before", DurationMS: 60000}}}, {Start: 2, End: 3, Items: []Item{{Key: core.MovieKey(2), Title: "After", DurationMS: 60000}}}}
				if _, err := s.SaveChannel(context.Background(), c); err != nil {
					t.Fatal(err)
				}
				u := &core.User{ID: 1, IsOwner: true}
				info, err := s.Tune(context.Background(), u, c.ID, core.ClientCaps{})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("GET", "/", nil)
				for _, at := range []int64{join, 116, 124} {
					now = base.Add(time.Duration(at) * time.Second)
					rec := httptest.NewRecorder()
					if dash {
						if err := s.ServeDASH(rec, req, info.ID, u); err != nil {
							t.Fatal(err)
						}
					} else {
						s.ServePlaylist(rec, req, info.ID, u)
					}
					if rec.Code != 200 {
						t.Fatalf("at %d: %d %s", at, rec.Code, rec.Body.String())
					}
					ss, err := s.getSession(context.Background(), info.ID, u)
					if err != nil {
						t.Fatal(err)
					}
					if dash {
						v := ss.dash.(*virtualDASH)
						for _, it := range v.snapshot() {
							r := httptest.NewRecorder()
							s.ServeDASHPart(r, req, info.ID, u, 0, int(it.tr.sequence), false)
							if r.Code != 200 {
								t.Fatalf("DASH media %d", r.Code)
							}
						}
					} else {
						for _, tr := range ss.tracks {
							r := httptest.NewRecorder()
							s.ServeSegment(r, req, info.ID, tr.programme.Start.UnixMilli(), 0, u)
							if r.Code != 200 {
								t.Fatalf("HLS media %d", r.Code)
							}
						}
						if at == 116 && !strings.Contains(rec.Body.String(), "#EXT-X-DISCONTINUITY") {
							t.Fatal("missing boundary discontinuity")
						}
					}
				}
				var slate *core.HLSRequest
				for i := range tc.requests {
					r := &tc.requests[i]
					if len(r.SlateText) > 0 && (r.Protocol == core.ProtocolDASH) == dash {
						slate = r
						break
					}
				}
				if slate == nil || slate.Info.Duration != time.Minute || slate.SegmentDuration != segmentDuration || slate.InputURL != "" {
					t.Fatalf("slate contract: %+v", slate)
				}
				if join == 68 && slate.StartOffset != 8*time.Second {
					t.Fatalf("join offset %s", slate.StartOffset)
				}
				if strings.Join(slate.SlateText, "|") != "Off air|Owner channel|Back at 12:02 AM" {
					t.Fatalf("copy %v", slate.SlateText)
				}
				for _, ps := range pb.prepared {
					if ps.Key == 0 {
						t.Fatal("slate prepared a provider")
					}
				}
			})
		}
	}
}

func TestEntirelyOffAirTune(t *testing.T) {
	now := time.Unix(68, 0).UTC()
	pb, tc := &fakePlayback{}, &fakeTranscoder{}
	s := testService(t, Options{Now: func() time.Time { return now }, Playback: pb, Transcoder: tc})
	c := testChannel()
	c.Items = nil
	c.OffAir = true
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	info, err := s.Tune(context.Background(), &core.User{ID: 1, IsOwner: true}, c.ID, core.ClientCaps{})
	if err != nil {
		t.Fatal(err)
	}
	if info.MediaInfo == nil || len(pb.prepared) != 0 || len(tc.requests[0].SlateText) != 2 {
		t.Fatalf("off-air tune %+v", info)
	}
	rec := httptest.NewRecorder()
	if err := s.ServeDASH(rec, httptest.NewRequest("GET", "/", nil), info.ID, &core.User{ID: 1, IsOwner: true}); err != nil || rec.Code != 200 {
		t.Fatalf("DASH tune %v %d", err, rec.Code)
	}
}
