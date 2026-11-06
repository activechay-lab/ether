package clusterplayback

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

type scoredFixtureEncoder struct {
	*fixtureEncoder
	hardware  string
	incapable bool
}

func (e *scoredFixtureEncoder) SupportsDistributedRequest(core.HLSRequest) bool   { return !e.incapable }
func (e *scoredFixtureEncoder) DistributedHardwareEncoder(core.HLSRequest) string { return e.hardware }

func TestTranscodePlacementScoresVerifiedHardwareAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		hardware                           string
		full, incapable, ineligible, local bool
		cpuSlots                           int64
		want                               string
	}{
		{name: "hardware beats spare CPU and lexical order", hardware: "h264_nvenc", cpuSlots: 8, want: "z-gpu"},
		{name: "full GPU falls back to CPU", hardware: "h264_nvenc", full: true, cpuSlots: 8, want: "a-cpu"},
		{name: "incapable GPU falls back to CPU", hardware: "h264_nvenc", incapable: true, cpuSlots: 8, want: "a-cpu"},
		{name: "ineligible GPU excluded", hardware: "h264_nvenc", ineligible: true, cpuSlots: 8, want: "a-cpu"},
		{name: "software fallback ranks by spare slots", cpuSlots: 8, want: "a-cpu"},
		{name: "equal scores have stable tie break", cpuSlots: 1, want: "a-cpu"},
		{name: "equal scores prefer source locality", cpuSlots: 1, local: true, want: "z-gpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixtureStarted := time.Now()
			db := store.OpenTest(t)
			ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 10*time.Second))
			defer cancel()
			ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: time.Minute, LockTimeout: time.Second}, NewVerifier())
			if err != nil {
				t.Fatal(err)
			}
			frontID := nodewire.Identity{Cluster: "offline", NodeID: "controller", Boot: "front-boot"}
			cpuID := nodewire.Identity{Cluster: "offline", NodeID: "a-cpu", Boot: "cpu-boot"}
			gpuID := nodewire.Identity{Cluster: "offline", NodeID: "z-gpu", Boot: "gpu-boot"}
			guards := fixtureGuards(t, frontID, cpuID, gpuID)
			endpoints := map[nodewire.Identity]string{}
			eligible := func(_ context.Context, id nodewire.Identity, _ config.NodeRole) error {
				if tc.ineligible && id == gpuID {
					return ErrDenied
				}
				return nil
			}
			base := Options{DB: db, Placement: ledger, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, Eligible: eligible, MemberRole: func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) {
				if ep, ok := endpoints[id]; ok {
					return ep, nil
				}
				return "", errors.New("missing fixture endpoint")
			}}
			for i, id := range []nodewire.Identity{frontID, cpuID, gpuID} {
				opts := base
				opts.Self = id
				opts.Guard = guards[i]
				if i > 0 {
					enc := &scoredFixtureEncoder{fixtureEncoder: &fixtureEncoder{}}
					if i == 2 {
						enc.hardware = tc.hardware
						enc.incapable = tc.incapable
					}
					opts.Encoder = enc
				}
				runtime, err := NewRuntime(opts)
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				if i == 0 {
					continue
				}
				server := httptest.NewUnstartedServer(runtime.Handler())
				server.TLS = guards[i].ServerTLS()
				server.StartTLS()
				defer server.Close()
				endpoints[id] = server.URL
				slots := int64(1)
				if i == 1 {
					slots = tc.cpuSlots
				}
				scope := id.NodeID + "/streams"
				if err = ledger.SetPool(ctx, scope, config.RoleTranscode, slots); err != nil {
					t.Fatal(err)
				}
				target := placement.Target{NodeID: id.NodeID, Boot: id.Boot}
				if err = ledger.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {scope}}); err != nil {
					t.Fatal(err)
				}
				if i == 2 && tc.full {
					_, err = ledger.Reserve(ctx, placement.Request{Session: "occupied", Operation: "occupy", Controller: placement.Controller{ID: frontID.NodeID, Boot: frontID.Boot}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: scope, Units: 1}}, Lease: time.Minute})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			opts := base
			opts.Self = frontID
			opts.Guard = guards[0]
			front, err := NewRuntime(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer front.Close()
			req := core.HLSRequest{SessionID: "scored-playback", Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}, Decision: core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", Container: "mpegts", SubtitleStream: -1}}
			source := nodewire.Identity{}
			if tc.local {
				source = gpuID
			}
			target, claims, _, err := front.selectTarget(ctx, req, source)
			if err != nil || target.NodeID != tc.want || len(claims) != 1 || claims[0].Scope != tc.want+"/streams" {
				t.Fatalf("target=%+v claims=%+v err=%v; want %s", target, claims, err, tc.want)
			}
		})
	}
}
