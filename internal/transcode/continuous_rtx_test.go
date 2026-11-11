package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type simRTXRunner struct {
	ExecRunner
	ffmpeg    string
	mu        sync.Mutex
	rtxCalls  [][]string
	failStart atomic.Bool
	crashTick atomic.Int64 // >0: close process after Nms
}

func (r *simRTXRunner) StartRTX(cmd Command) (Process, error) {
	r.mu.Lock()
	r.rtxCalls = append(r.rtxCalls, append([]string(nil), cmd.Args...))
	r.mu.Unlock()

	if r.failStart.Load() {
		return nil, errors.New("simulated NVEncC start failure")
	}

	var videoURL, outRes, gopLen, fps, keyfile string
	for i := 0; i < len(cmd.Args); i++ {
		switch cmd.Args[i] {
		case "-i":
			if i+1 < len(cmd.Args) {
				videoURL = cmd.Args[i+1]
			}
		case "--output-res":
			if i+1 < len(cmd.Args) {
				outRes = cmd.Args[i+1]
			}
		case "--gop-len":
			if i+1 < len(cmd.Args) {
				gopLen = cmd.Args[i+1]
			}
		case "--fps":
			if i+1 < len(cmd.Args) {
				fps = cmd.Args[i+1]
			}
		case "--keyfile":
			if i+1 < len(cmd.Args) {
				keyfile = cmd.Args[i+1]
			}
		}
	}
	w, h, _ := strings.Cut(outRes, "x")
	g, _ := strconv.Atoi(gopLen)
	if g <= 0 {
		g = 60
	}
	keyExpr := fmt.Sprintf("expr:gte(n,n_forced*%d)", g)
	if keyfile != "" {
		b, err := os.ReadFile(keyfile)
		if err == nil {
			var parts []string
			parts = append(parts, "eq(n,0)")
			var steady int
			for _, f := range strings.Fields(string(b)) {
				n, convErr := strconv.Atoi(f)
				if convErr == nil && n > 0 {
					parts = append(parts, fmt.Sprintf("eq(n,%d)", n))
					steady = n
				}
			}
			if steady > 0 {
				keyExpr = fmt.Sprintf("expr:if(lt(n,%d),%s,gte(n,(n_forced-%d)*%d))", steady, strings.Join(parts, "+"), len(parts)-steady/g, g)
			}
		}
	}

	ffArgs := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-probesize", "32", "-analyzeduration", "0", "-f", "matroska", "-i", videoURL,
		"-vf", fmt.Sprintf("scale=%s:%s,setsar=1,format=yuv420p,fps=%s", w, h, fps),
		"-c:v", "libx264", "-preset", "ultrafast", "-profile:v", "high", "-pix_fmt", "yuv420p",
		"-bf", "0", "-g", strconv.Itoa(g), "-sc_threshold", "0",
		"-force_key_frames", keyExpr,
		"-flush_packets", "1", "-f", "mpegts", "pipe:1",
	}
	proc, err := r.ExecRunner.Start(Command{
		Path:   r.ffmpeg,
		Args:   ffArgs,
		Dir:    cmd.Dir,
		Stdout: cmd.Stdout,
		Stderr: cmd.Stderr,
	})
	if err != nil {
		return proc, err
	}
	if ms := r.crashTick.Load(); ms > 0 {
		go func() {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			proc.Stop()
		}()
	}
	return proc, nil
}

func (r *simRTXRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rtxCalls)
}

func (r *simRTXRunner) lastArgs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rtxCalls) == 0 {
		return nil
	}
	return r.rtxCalls[len(r.rtxCalls)-1]
}

func TestContinuousRTXEligibilityAndArgs(t *testing.T) {
	m := rtxManager()
	req := EncoderRequest{
		Source:          RawFormat{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"},
		SegmentDuration: 4 * time.Second,
		Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 720, BitrateKbps: 3000},
		ClientCaps:      core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	}

	plan, _, ok := m.continuousRTXPlanFor(req, false)
	if !ok || plan.width != 1920 || plan.height != 1080 || plan.quality != 2 {
		t.Fatalf("expected 1920x1080 RTX plan, got ok=%v plan=%+v", ok, plan)
	}
	args := strings.Join(buildContinuousRTXArgs(req, plan, "tcp://127.0.0.1:12345", ""), " ")
	for _, want := range []string{"--vpp-resize algo=ngx-vsr,vsr-quality=2", "--output-res 1920x1080", "--bframes 0", "--strict-gop", "-f mpegts -o -"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}

	// Off mode -> ineligible.
	m.cfg.RTXVideo.Mode = "off"
	if _, _, ok := m.continuousRTXPlanFor(req, false); ok {
		t.Fatal("off mode must not select RTX")
	}

	// Auto mode: LAN eligible, Remote ineligible.
	m.cfg.RTXVideo.Mode = "auto"
	if _, _, ok := m.continuousRTXPlanFor(req, false); !ok {
		t.Fatal("auto LAN must select RTX")
	}
	remoteReq := req
	remoteReq.ClientCaps.Remote = true
	if _, _, ok := m.continuousRTXPlanFor(remoteReq, false); ok {
		t.Fatal("auto Remote must not select RTX")
	}

	// Client cap without upscale headroom -> ineligible.
	m.cfg.RTXVideo.Mode = "on"
	cappedReq := req
	cappedReq.ClientCaps.MaxHeight = 720
	if _, _, ok := m.continuousRTXPlanFor(cappedReq, false); ok {
		t.Fatal("720p client cap on 720p source leaves no upscale headroom")
	}

	// Source above SourceMaxHeight -> ineligible.
	m.cfg.RTXVideo.SourceMaxHeight = 480
	if _, _, ok := m.continuousRTXPlanFor(req, false); ok {
		t.Fatal("720p source above 480p SourceMaxHeight must not select RTX")
	}
	m.cfg.RTXVideo.SourceMaxHeight = 720

	// Concurrency cap: blocks new entrant, permits holder already holding its slot.
	m.rtx.active = 1
	if _, _, ok := m.continuousRTXPlanFor(req, false); ok {
		t.Fatal("new entrant must respect MaxConcurrent=1")
	}
	if _, _, ok := m.continuousRTXPlanFor(req, true); !ok {
		t.Fatal("existing permit holder must remain eligible across boundaries")
	}
}

func TestContinuousRTXLiveBoundarySwapWithoutRestart(t *testing.T) {
	sim := &simRTXRunner{}
	e := newRealEnv(t, WithoutHWDetection(), WithRunner(sim))
	sim.ffmpeg = e.ffmpeg

	// Seed simulated Windows RTX backend availability on the live manager.
	e.m.rtx.mu.Lock()
	e.m.rtx.path = "NVEncC64.exe"
	e.m.rtx.configuredPath = ""
	e.m.rtx.support = core.VideoEnhancementSupport{
		Backend:   rtxBackend,
		Available: true,
		Protocols: []string{core.ProtocolHLS, core.ProtocolDASH},
	}
	e.m.rtx.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	format := RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
	enc, err := e.m.StartEncoder(ctx, EncoderRequest{
		Source:          format,
		SegmentDuration: 2 * time.Second,
		DVRWindow:       2 * time.Minute,
		Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 180, BitrateKbps: 1500},
		ClientCaps:      core.ClientCaps{MaxHeight: 360, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	// Attach a live MPEG-TS viewer before any boundary swap; it must stay open across swaps.
	tsReader, err := enc.Segments().AttachTS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tsReader.Close()
	var tsBytes atomic.Int64
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := tsReader.Read(buf)
			if n > 0 {
				tsBytes.Add(int64(n))
			}
			if rerr != nil {
				return
			}
		}
	}()

	frame := RawFrame{Video: make([]byte, format.Width*format.Height*3/2), PCM: make([]byte, tickPCM(format, 0))}
	for i := format.Width * format.Height; i < len(frame.Video); i++ {
		frame.Video[i] = 128
	}
	writeTicks := func(startTick, count int) {
		t.Helper()
		for n := range count {
			tick := startTick + n
			for i := range format.Width * format.Height {
				frame.Video[i] = byte(16 + (i+tick)%200)
			}
			if werr := enc.Write(ctx, frame); werr != nil {
				t.Fatalf("Write tick %d: %v", tick, werr)
			}
		}
	}

	// Phase 1 (RTX off): startup (60 ticks = segs 0,1,2) + 2 steady segments (120 ticks = segs 3,4).
	writeTicks(0, 180)
	if _, err := enc.Segments().Wait(ctx, 2); err != nil {
		t.Fatalf("wait seg 3: %v", err)
	}
	_, w0, h0, _ := enc.Segments().Format()
	if w0 != 320 || h0 != 180 {
		t.Fatalf("expected initial ordinary format 320x180, got %dx%d", w0, h0)
	}
	if sim.count() != 0 {
		t.Fatalf("expected 0 RTX invocations while off, got %d", sim.count())
	}

	// Enable RTX Video while the channel is actively running!
	cfg := config.Transcode{
		FFmpegPath:  e.ffmpeg,
		FFprobePath: e.ffprobe,
		HWAccel:     AccelNone,
		RTXVideo: config.RTXVideo{
			Mode:            "on",
			SourceMaxHeight: 720,
			TargetMaxHeight: 360,
			Quality:         2,
			MaxConcurrent:   1,
		},
	}
	e.m.UpdateConfig(cfg)

	// Phase 2 (RTX on): feed 3 steady segments (180 ticks = segs 5,6,7).
	// Tick 180 is an exact steady segment boundary (seq 5), so it swaps immediately at seq 5!
	writeTicks(180, 180)
	for want := int64(3); want <= 6; want++ {
		seg, werr := enc.Segments().Wait(ctx, want-1)
		if werr != nil {
			t.Fatalf("wait seg %d: %v", want, werr)
		}
		if seg.Seq != want {
			t.Fatalf("non-contiguous segment sequence: got %d, want %d", seg.Seq, want)
		}
	}
	if sim.count() != 1 {
		t.Fatalf("expected 1 RTX launch after boundary swap, got %d", sim.count())
	}
	joinedArgs := strings.Join(sim.lastArgs(), " ")
	if !strings.Contains(joinedArgs, "algo=ngx-vsr,vsr-quality=2") || !strings.Contains(joinedArgs, "--output-res 640x360") {
		t.Fatalf("unexpected RTX args: %s", joinedArgs)
	}
	_, w1, h1, _ := enc.Segments().Format()
	if w1 != 640 || h1 != 360 {
		t.Fatalf("expected live-upgraded format 640x360, got %dx%d", w1, h1)
	}
	if active := e.m.EnhancementStatus().Active; active != 1 {
		t.Fatalf("expected EnhancementStatus().Active == 1 while RTX active, got %d", active)
	}

	// Toggle RTX Video back off while still running!
	cfg.RTXVideo.Mode = "off"
	e.m.UpdateConfig(cfg)

	// Phase 3 (RTX off again): feed 3 more steady segments (180 ticks = segs 8,9,10).
	writeTicks(360, 180)
	for want := int64(7); want <= 9; want++ {
		seg, werr := enc.Segments().Wait(ctx, want-1)
		if werr != nil {
			t.Fatalf("wait seg %d: %v", want, werr)
		}
		if seg.Seq != want {
			t.Fatalf("non-contiguous segment sequence after disabling RTX: got %d, want %d", seg.Seq, want)
		}
	}
	if active := e.m.EnhancementStatus().Active; active != 0 {
		t.Fatalf("expected EnhancementStatus().Active == 0 after swapping back to ordinary, got %d", active)
	}
	_, w2, h2, _ := enc.Segments().Format()
	if w2 != 320 || h2 != 180 {
		t.Fatalf("expected restored ordinary format 320x180, got %dx%d", w2, h2)
	}
	if tsBytes.Load() == 0 {
		t.Fatal("expected continuous MPEG-TS bytes across live swaps")
	}
}

func TestContinuousRTXSilentFallbackOnFailureAndUnavailable(t *testing.T) {
	sim := &simRTXRunner{}
	e := newRealEnv(t, WithoutHWDetection(), WithRunner(sim))
	sim.ffmpeg = e.ffmpeg

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	format := RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
	frame := RawFrame{Video: make([]byte, format.Width*format.Height*3/2), PCM: make([]byte, tickPCM(format, 0))}
	for i := format.Width * format.Height; i < len(frame.Video); i++ {
		frame.Video[i] = 128
	}

	// 1. Mode="on" when NVEncC is unavailable (e.g. macOS/Linux): silent fallback to ordinary encoder.
	e.m.UpdateConfig(config.Transcode{
		FFmpegPath:  e.ffmpeg,
		FFprobePath: e.ffprobe,
		HWAccel:     AccelNone,
		RTXVideo:    config.RTXVideo{Mode: "on", SourceMaxHeight: 720, TargetMaxHeight: 360, Quality: 2, MaxConcurrent: 1},
	})
	encUnavailable, err := e.m.StartEncoder(ctx, EncoderRequest{
		Source:          format,
		SegmentDuration: 2 * time.Second,
		Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 180, BitrateKbps: 1500},
		ClientCaps:      core.ClientCaps{MaxHeight: 360, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	if err != nil {
		t.Fatalf("unavailable RTX must silently fall back, got err: %v", err)
	}
	for range 120 {
		if werr := encUnavailable.Write(ctx, frame); werr != nil {
			t.Fatalf("write with unavailable RTX: %v", werr)
		}
	}
	if _, err := encUnavailable.Segments().Wait(ctx, 1); err != nil {
		t.Fatalf("wait segment on unavailable RTX fallback: %v", err)
	}
	_ = encUnavailable.Close()

	// 2. Mode="on" when NVEncC is marked available, fails on start, then crashes mid-stream on retry.
	e.m.rtx.mu.Lock()
	e.m.rtx.path = "NVEncC64.exe"
	e.m.rtx.support = core.VideoEnhancementSupport{Backend: rtxBackend, Available: true, Protocols: []string{core.ProtocolHLS, core.ProtocolDASH}}
	e.m.rtx.mu.Unlock()

	sim.failStart.Store(true)
	encStartFail, err := e.m.StartEncoder(ctx, EncoderRequest{
		Source:          format,
		SegmentDuration: 2 * time.Second,
		Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 180, BitrateKbps: 1500},
		ClientCaps:      core.ClientCaps{MaxHeight: 360, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	if err != nil {
		t.Fatalf("start failure must silently fall back, got: %v", err)
	}
	for range 120 {
		if werr := encStartFail.Write(ctx, frame); werr != nil {
			t.Fatalf("write after start fallback: %v", werr)
		}
	}
	if _, err := encStartFail.Segments().Wait(ctx, 1); err != nil {
		t.Fatalf("wait segment after start fallback: %v", err)
	}
	_ = encStartFail.Close()
	if active := e.m.EnhancementStatus().Active; active != 0 {
		t.Fatalf("permit leaked on start failure: active=%d", active)
	}

	// 3. Mid-stream crash recovery without dropping the encoder.
	sim.failStart.Store(false)
	sim.crashTick.Store(150)
	encCrash, err := e.m.StartEncoder(ctx, EncoderRequest{
		Source:          format,
		SegmentDuration: 2 * time.Second,
		Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 180, BitrateKbps: 1500},
		ClientCaps:      core.ClientCaps{MaxHeight: 360, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
	})
	if err != nil {
		t.Fatalf("start encoder before crash: %v", err)
	}
	defer encCrash.Close()
	for n := range 240 {
		for i := range format.Width * format.Height {
			frame.Video[i] = byte(16 + (i+n)%200)
		}
		if werr := encCrash.Write(ctx, frame); werr != nil {
			t.Fatalf("write tick %d during mid-stream RTX recovery: %v", n, werr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := encCrash.Segments().Wait(ctx, 1); err != nil {
		t.Fatalf("wait segment after mid-stream RTX crash recovery: %v", err)
	}
	if active := e.m.EnhancementStatus().Active; active != 0 {
		t.Fatalf("permit leaked after mid-stream crash: active=%d", active)
	}
}
