package transcode

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func distributedFixture() (*Manager, core.HLSRequest) {
	f := features{encoders: map[string]bool{"libx264": true, "libx265": true, "aac": true, "ass": true}, filters: map[string]bool{"scale": true, "format": true, "setsar": true}, muxers: map[string]bool{"hls": true, "mpegts": true, "mp4": true, "dash": true, "matroska": true, "ssegment": true, "ass": true}, demuxers: map[string]bool{"matroska": true}, decoders: map[string]bool{"h264": true, "aac": true, "subrip": true}, bsfs: map[string]bool{"noise": true, "dump_extra": true, "h264_mp4toannexb": true}, inputProtocols: map[string]bool{"http": true, "https": true, "tcp": true, "tls": true}}
	m := &Manager{feat: f, cfg: config.Transcode{}}
	req := core.HLSRequest{Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac", Channels: 2}}}, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: -1}}
	return m, req
}
func TestDistributedRequestUsesExactProbedPlan(t *testing.T) {
	for _, protocol := range []string{core.ProtocolHLS, core.ProtocolDASH, core.ProtocolHTTP} {
		t.Run(protocol, func(t *testing.T) {
			m, req := distributedFixture()
			req.Protocol = protocol
			if !m.SupportsDistributedRequest(req) {
				t.Fatal("probed copy plan rejected")
			}
			delete(m.feat.muxers, map[string]string{core.ProtocolHLS: "mpegts", core.ProtocolDASH: "mp4", core.ProtocolHTTP: "matroska"}[protocol])
			if m.SupportsDistributedRequest(req) {
				t.Fatal("missing actual mux dependency admitted")
			}
		})
	}
	m, req := distributedFixture()
	req.Protocol = core.ProtocolHTTP
	delete(m.feat.encoders, "aac")
	if m.SupportsDistributedRequest(req) {
		t.Fatal("HTTP hidden AAC conversion ignored")
	}
	m, req = distributedFixture()
	req.Decision.VideoCopy = false
	req.Decision.VideoCodec = "hevc"
	delete(m.feat.encoders, "libx265")
	if m.SupportsDistributedRequest(req) {
		t.Fatal("silent HEVC-to-H264 degradation admitted")
	}
	m, req = distributedFixture()
	req.Decision.VideoCopy = false
	delete(m.feat.encoders, "libx264")
	if m.SupportsDistributedRequest(req) {
		t.Fatal("missing actual CPU encoder admitted")
	}
	m, req = distributedFixture()
	delete(m.feat.bsfs, "noise")
	if m.SupportsDistributedRequest(req) {
		t.Fatal("advertised seek lacks bitstream dependency")
	}
	m, req = distributedFixture()
	req.Decision.StripDolbyVision = true
	if m.SupportsDistributedRequest(req) {
		t.Fatal("unprobed Dolby Vision strip admitted")
	}
	m, req = distributedFixture()
	req.Decision.VideoCopy = false
	req.Decision.ToneMap = true
	req.Info.Streams[0].ColorTransfer = "smpte2084"
	if m.SupportsDistributedRequest(req) {
		t.Fatal("missing tone-map graph admitted")
	}
	m, req = distributedFixture()
	m.feat.muxers = nil
	if m.SupportsDistributedRequest(req) {
		t.Fatal("unknown inventory assumed available")
	}
	m, req = distributedFixture()
	m.cfg.FastStart = true
	if m.SupportsDistributedRequest(req) {
		t.Fatal("mutable dedicated-manager policy admitted")
	}
	m, req = distributedFixture()
	req.Decision.VideoCopy = false
	m.feat.decoders = nil
	if m.SupportsDistributedRequest(req) {
		t.Fatal("unknown decoder inventory admitted")
	}
}

func TestDistributedStartupInventoryIsBoundedAndOptional(t *testing.T) {
	responses := map[string]string{"-muxers": "Muxers:\n E hls HLS\n E mp4 MP4\n", "-demuxers": "Demuxers:\n D matroska,webm Matroska\n", "-decoders": "Decoders:\n ------\n VFS..D h264 H264\n", "-bsfs": "Bitstream filters:\nnoise\ndovi_rpu\n", "-protocols": "Supported file protocols:\nInput:\n http\n tcp\nOutput:\n fake_output_only\n"}
	runner := retirementRunner{run: func(ctx context.Context, c Command) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("startup inventory lacks deadline")
		}
		_, err := io.WriteString(c.Stdout, responses[c.Args[1]])
		return err
	}}
	var f features
	probeDistributedFeatures(context.Background(), runner, "ffmpeg", &f)
	if !f.muxers["hls"] || !f.demuxers["webm"] || !f.decoders["h264"] || !f.bsfs["dovi_rpu"] || !f.inputProtocols["http"] || f.inputProtocols["fake_output_only"] {
		t.Fatal("inventory parsing lost actual feature direction")
	}
	runner.run = func(context.Context, Command) error { return errors.New("optional unavailable") }
	old := features{encoders: map[string]bool{"libx264": true}}
	probeDistributedFeatures(context.Background(), runner, "ffmpeg", &old)
	if !old.hasEncoder("libx264") || old.muxers != nil {
		t.Fatal("optional failure changed standalone feature contract")
	}
	b := &boundedFeatureOutput{}
	if _, err := b.Write([]byte(strings.Repeat("x", (1<<20)+1))); err == nil || !b.overflow {
		t.Fatal("inventory output unbounded")
	}
}

func TestDistributedFilterDependencyParser(t *testing.T) {
	inventory := map[string]bool{"scale": true, "format": true, "noise": true}
	for _, graph := range []string{"[0:0]scale=1920:1080,format=yuv420p[vout]", "noise=amount=0:drop='lt(pts*tb,1)'"} {
		if !distributedFilterList(graph, inventory) {
			t.Fatal("generated graph rejected", graph)
		}
	}
	if distributedFilterList("scale=1920:1080,unprobed=x", inventory) || distributedFilterList("format='unterminated", inventory) {
		t.Fatal("unknown/malformed graph admitted")
	}
}
