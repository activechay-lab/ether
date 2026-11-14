package onair

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/encode"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// ErrLiveBelowRealtime indicates that live scene rasterization could not sustain
// >= 1.0x real time and playout must fall back for this moment.
var ErrLiveBelowRealtime = errors.New("onair: live render speed below 1.0x realtime")

// The mid-stream speed gate judges cumulative render speed only after
// liveSpeedGrace seconds of generated video (ffmpeg start-up and first-frame
// costs dip the early average), and falls back below liveSpeedFloor so
// brief load spikes don't cut a transition short. Variables so tests that
// exercise save-while-airing don't depend on the machine's speed.
var (
	liveSpeedGrace = 2.0
	liveSpeedFloor = 0.9
)

// FrameReader matches transcode.Decoder without coupling onair to transcode.
type FrameReader interface {
	ReadFrame(video []byte) (audio []byte, err error)
	Close() error
}

type liveSpeedRecord struct {
	speedX float64
	retry  time.Time
}

// VisibilitySnapshot summarizes on-air master caching and live playout metrics.
type VisibilitySnapshot struct {
	MastersRendered int64
	MastersFailed   int64
	LiveSaved       int64
	CachedAired     int64
	SplitAired      int64
	LiveAired       int64
	Fallbacks       int64
	LastSpeedX      float64
}

type liveOutFrame struct {
	video *[]byte
	audio []byte
	err   error
}

// MomentStream delivers real-time yuv420p + 48kHz stereo s16le frames for an
// On Air moment — decoding from cache, compositing live slot overlays over a
// cached base master, or rendering live while simultaneously saving the reusable
// master to the durable content-addressed cache.
type MomentStream struct {
	mode            string
	loop            bool
	fpsNum          int
	fpsDen          int
	width           int
	height          int
	tick            int64
	baseReader      FrameReader
	overlayDoc      *scene.Document
	overlaySequence *raster.Sequence
	overlayBnd      raster.Bindings
	renderFn        func(scene.Document, raster.Bindings, float64, int, int) *image.RGBA

	outCh                      <-chan liveOutFrame
	videoPool                  *sync.Pool
	stopLive                   func(aborted bool)
	parentCtx                  context.Context
	closeOnce                  sync.Once
	service                    *Service
	lookID                     string
	moment                     string
	ovFrames                   int64
	ovWallDur                  time.Duration
	lastSpeedX                 float64
	saved                      bool
	promote                    func() FrameReader
	animationReady             atomic.Bool
	externalSegmentClock       bool
	settledTitle               bool
	skipOpening                int
	scenePoster, sceneDuration float64
	completeRemaining          int
	completeLast               []byte
}

// UseExternalSegmentClock lets broadcast align the primed frame to its own
// segment clock, which may have repeated title frames during decoder startup.
func (m *MomentStream) UseExternalSegmentClock() { m.externalSegmentClock = true }

// StartCompleteTitle keeps live title text at its authored complete layout
// while the cached base continues animating. Unsplit masters skip their intro.
// Call before the first ReadFrame.
func (m *MomentStream) StartCompleteTitle() {
	m.settledTitle = true
	if m.overlayDoc == nil && m.baseReader != nil && m.promote == nil {
		m.skipOpening = max(1, int(max(1, m.scenePoster)*float64(m.fpsNum)/float64(m.fpsDen)))
		m.completeRemaining = max(1, int(max(0, m.sceneDuration-max(1, m.scenePoster)-0.5)*float64(m.fpsNum)/float64(m.fpsDen)))
	}
}

// AnimationReady reports that the cold reader can splice its primed master.
func (m *MomentStream) AnimationReady() bool { return m.animationReady.Load() }

func (m *MomentStream) Mode() string    { return m.mode }
func (m *MomentStream) Saved() bool     { return m.saved }
func (m *MomentStream) SpeedX() float64 { return m.lastSpeedX }

func (m *MomentStream) ReadFrame(video []byte) ([]byte, error) {
	if m.promote != nil && (m.externalSegmentClock || (m.tick*int64(m.fpsDen))%int64(4*m.fpsNum) == 0) {
		if reader := m.promote(); reader != nil {
			m.baseReader.Close()
			m.baseReader = reader
			m.promote = nil
			m.mode = "cached"
			if m.overlayDoc != nil {
				m.mode = "split-cached"
			}
			if m.settledTitle {
				m.StartCompleteTitle()
			}
			m.service.noteTransition(m.mode, 0)
		}
	}
	if m.baseReader != nil {
		if m.settledTitle && m.overlayDoc == nil && m.completeRemaining == 0 && m.completeLast != nil {
			copy(video, m.completeLast)
			return nil, nil
		}
		for m.skipOpening > 0 {
			if _, err := m.baseReader.ReadFrame(video); err != nil {
				return nil, err
			}
			m.skipOpening--
		}
		audio, err := m.baseReader.ReadFrame(video)
		if err != nil {
			return nil, err
		}
		if m.overlayDoc != nil && m.promote == nil {
			dur := m.overlayDoc.Duration
			if dur <= 0 {
				dur = 8
			}
			t := float64(m.tick) * float64(m.fpsDen) / float64(m.fpsNum)
			if m.loop {
				t = math.Mod(t, dur)
			} else if t >= dur {
				t = dur - 1.0/120.0
			}
			if m.settledTitle {
				t = max(1, m.overlayDoc.Poster)
			}
			started := time.Now()
			if m.overlaySequence == nil {
				m.overlaySequence = raster.NewSequence(*m.overlayDoc, m.overlayBnd, m.width, m.height)
			}
			ov := m.overlaySequence.Frame(max(0, t))
			blendPremulRGBAOverI420(ov, video, m.width, m.height)
			m.ovWallDur += time.Since(started)
			m.ovFrames++
			if m.ovWallDur > 0 {
				genSec := float64(m.ovFrames) * float64(m.fpsDen) / float64(m.fpsNum)
				m.lastSpeedX = genSec / m.ovWallDur.Seconds()
			}
		}
		if m.settledTitle && m.overlayDoc == nil && m.promote == nil {
			if m.completeLast == nil {
				m.completeLast = make([]byte, len(video))
			}
			copy(m.completeLast, video)
			m.completeRemaining--
		}
		m.tick++
		return audio, nil
	}

	fr, ok := <-m.outCh
	if !ok {
		return nil, io.EOF
	}
	if fr.err != nil {
		return nil, fr.err
	}
	copy(video, *fr.video)
	if m.videoPool != nil {
		m.videoPool.Put(fr.video)
	}
	m.tick++
	return fr.audio, nil
}

func (m *MomentStream) Close() error {
	var err error
	m.closeOnce.Do(func() {
		if m.baseReader != nil {
			err = m.baseReader.Close()
			if m.overlayDoc != nil && m.service != nil && m.ovFrames > 0 {
				m.service.recordMomentSpeed(m.lookID, m.moment+":overlay", m.lastSpeedX)
			}
		}
		if m.stopLive != nil {
			aborted := m.parentCtx != nil && m.parentCtx.Err() != nil
			m.stopLive(aborted)
		}
	})
	return err
}

// LiveRenderAllowed reports whether live real-time rendering is permitted for r
// based on measured render speed (>= 1.0x realtime required).
func (s *Service) LiveRenderAllowed(r core.ClipRequest) (bool, float64, string) {
	if s != nil && s.o.Enabled != nil && !s.o.Enabled() {
		return false, 0, "Live TV disabled"
	}
	if s == nil {
		return false, 0, "graphics unavailable"
	}
	if r.ReelClipID != "" {
		return false, 0, "external reel clip"
	}
	rawJob, ok := s.planRaw(r)
	if !ok {
		return false, 0, "scene unavailable"
	}
	if rawJob.AssetDigest != "" {
		return false, 0, "video asset bumper"
	}
	key := rawJob.Look + "/" + r.Moment
	s.mu.Lock()
	rec, found := s.liveSpeed[key]
	now := s.now()
	s.mu.Unlock()
	if found && rec.speedX > 0 && rec.speedX < 1.0 && now.Before(rec.retry) {
		return false, rec.speedX, fmt.Sprintf("measured render speed %.2fx < 1.00x realtime", rec.speedX)
	}
	if found && rec.speedX > 0 {
		return true, rec.speedX, ""
	}
	return true, 0, ""
}

// StandbyMoment picks the best animated hold moment for a look when content is
// warming: an explicit standby scene, else break, else ident.
func (s *Service) StandbyMoment(lookID string) (string, bool) {
	if s == nil || lookID == "" {
		return "", false
	}
	s.mu.Lock()
	l, ok := s.looks[lookID]
	s.mu.Unlock()
	if !ok {
		return "", false
	}
	for _, role := range []string{"standby", "break", "ident"} {
		for _, sc := range l.Scenes {
			if sc.Enabled && sc.Role == role && sc.Weight > 0 {
				if role == "standby" {
					return "standby", true
				}
				if _, ok := scene.StandbyBase(sc.Document); ok {
					return "standby", true
				}
				return role, true
			}
		}
	}
	return "", false
}

// CachedStandby returns a durable animated look master suitable for looping
// during content holds (standby, break, or ident) without triggering rendering.
func (s *Service) CachedStandby(ctx context.Context, lookID string) (core.ClipRef, bool) {
	if s == nil || lookID == "" {
		return core.ClipRef{}, false
	}
	for _, role := range []string{"standby", "break", "ident"} {
		if ref, ok := s.Clip(ctx, core.ClipRequest{LookID: lookID, Moment: role}); ok && ref.Path != "" {
			return ref, true
		}
	}
	return core.ClipRef{}, false
}

// Visibility returns a snapshot of master render and live playout counters.
func (s *Service) Visibility() VisibilitySnapshot {
	if s == nil {
		return VisibilitySnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vis
}

// OpenMoment opens a graphics moment for continuous broadcast playout:
//   - Cached static master: decodes <hash>.mp4 directly ("cached")
//   - Split live-slot moment with cached base: decodes <baseHash>.mp4 and
//     composites slot layers live on top ("split-cached")
//   - Cold static or split base master: renders live in real time into the
//     broadcast AND simultaneously writes <hash>.mp4 to the content-addressed
//     cache ("live-save" / "split-live-save")
//   - Unsplittable live-slot moment: renders live in real time with no disk save
//     ("live-nosave")
func (s *Service) OpenMoment(
	ctx context.Context,
	r core.ClipRequest,
	width, height, fpsNum, fpsDen int,
	loop bool,
	openClip func(ctx context.Context, path string, loop bool) (FrameReader, error),
) (*MomentStream, string, error) {
	if s == nil {
		return nil, "graphics unavailable", fmt.Errorf("onair: nil service")
	}
	if width <= 0 || height <= 0 {
		width, height = 1920, 1080
	}
	width &^= 1
	height &^= 1
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 30, 1
	}
	renderFn := s.o.RenderFrame
	if renderFn == nil {
		renderFn = raster.Render
	}

	if r.ReelClipID != "" {
		ref, ok := s.Clip(ctx, r)
		if !ok || openClip == nil {
			return nil, "reel clip not cached", fmt.Errorf("onair: reel clip %s not cached", r.ReelClipID)
		}
		dec, err := openClip(ctx, ref.Path, loop)
		if err != nil {
			return nil, "reel clip decode failed", err
		}
		s.noteTransition("cached", 0)
		return &MomentStream{mode: "cached", loop: loop, fpsNum: fpsNum, fpsDen: fpsDen, width: width, height: height, baseReader: dec, service: s, moment: r.Moment}, "cached", nil
	}

	rawJob, ok := s.planRaw(r)
	if !ok {
		return nil, "scene not configured", fmt.Errorf("onair: moment %s unavailable", r.Moment)
	}
	if assetLoop(r.Moment) {
		loop = true
	}
	cacheJob, cacheable := s.plan(r)

	hasLive := !strings.HasPrefix(r.Moment, "plugin:") && !strings.HasPrefix(r.Moment, "screen.") && rawJob.AssetDigest == "" && scene.HasLiveSlots(rawJob.Doc)
	var overlayDoc *scene.Document
	var overlayBind raster.Bindings
	if hasLive {
		_, ov, splittable := scene.SplitSlotLayers(rawJob.Doc)
		if splittable && cacheable {
			scene.RewriteLineupHeading(&ov, r.Bind["lineup.heading"])
			ovJob := rawJob
			ovJob.Doc = ov
			if err := s.loadAssets(ctx, &ovJob); err != nil {
				return nil, "overlay assets failed", err
			}
			overlayDoc = &ovJob.Doc
			overlayBind = ovJob.Bind
		}
	}

	// 1. Check durable content-addressed master cache first.
	if cacheable && openClip != nil {
		if ref, hit := s.Clip(ctx, r); hit {
			dec, err := openClip(ctx, ref.Path, loop)
			if err == nil {
				mode := "cached"
				if overlayDoc != nil {
					mode = "split-cached"
				}
				s.noteTransition(mode, 0)
				return &MomentStream{
					mode:        mode,
					loop:        loop,
					fpsNum:      fpsNum,
					fpsDen:      fpsDen,
					width:       width,
					height:      height,
					baseReader:  dec,
					scenePoster: rawJob.Doc.Poster, sceneDuration: rawJob.Doc.Duration,
					overlayDoc: overlayDoc,
					overlayBnd: overlayBind,
					renderFn:   renderFn,
					service:    s,
					lookID:     rawJob.Look,
					moment:     r.Moment,
				}, mode, nil
			}
		}
	}

	// A cold scene airs immediately. The queued master becomes eligible at the
	// next segment boundary; the reader never blocks on the render slot.
	if cacheable && rawJob.AssetDigest == "" {
		// Keep the master requested by this airing through budget eviction.
		s.mu.Lock()
		s.pins[cacheJob.Hash] = time.Now().Add(3 * time.Hour)
		s.mu.Unlock()
		s.enqueue(cacheJob)
		stillJob := rawJob
		if err := s.loadAssets(ctx, &stillJob); err != nil {
			return nil, "plate assets failed", err
		}
		seq := raster.NewSequence(stillJob.Doc, stillJob.Bind, 1280, 720)
		plate := seq.Frame(max(1, stillJob.Doc.Poster))
		pixels := make([]byte, width*height*3/2)
		rgbaScaleToI420(plate, pixels, width, height)
		reader := &plateReader{remaining: plateFrames(rawJob.Doc.Duration, fpsNum, fpsDen, loop), pixels: pixels, audio: make([]byte, (48000*fpsDen/fpsNum)*4)}
		// Prepare and prime the cached decoder off the frame clock. Only exchange
		// readers at a segment boundary; the plate remains available throughout.
		promoteCtx, cancel := context.WithCancel(ctx)
		ready := make(chan FrameReader)
		promoteDone := make(chan struct{})
		stream := &MomentStream{scenePoster: rawJob.Doc.Poster, sceneDuration: rawJob.Doc.Duration, mode: "static-plate", loop: loop, fpsNum: fpsNum, fpsDen: fpsDen, width: width, height: height, baseReader: reader, renderFn: renderFn, service: s, lookID: rawJob.Look, moment: r.Moment,
			overlayDoc: overlayDoc, overlayBnd: overlayBind,
			stopLive: func(bool) { cancel(); <-promoteDone },
			promote: func() FrameReader {
				select {
				case dec := <-ready:
					return dec
				default:
					return nil
				}
			},
		}
		go func() {
			defer close(promoteDone)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				if ref, hit := s.Clip(promoteCtx, r); hit && openClip != nil {
					dec, err := openClip(promoteCtx, ref.Path, loop)
					if err == nil {
						pixels := make([]byte, width*height*3/2)
						audio, err := dec.ReadFrame(pixels)
						if err == nil {
							primed := &primedReader{FrameReader: dec, video: pixels, audio: audio}
							stream.animationReady.Store(true)
							select {
							case ready <- primed:
								return
							case <-promoteCtx.Done():
							}
						}
						dec.Close()
					}
				}
				select {
				case <-promoteCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		return stream, "static-plate", nil
	}

	// 2. Cold cache miss or unsplittable slot scene: check >= 1.0x speed gate.
	allowed, speedX, reason := s.LiveRenderAllowed(r)
	if !allowed {
		s.noteTransition("fallback", speedX)
		s.o.Logger.Info("onair live moment fallback", "look", rawJob.Look, "moment", r.Moment, "speed_x", round2(speedX), "reason", reason)
		// Even an unsplittable custom scene must remain visible when its
		// animation cannot sustain real time. It is rendered per airing and
		// never persisted with programme bindings.
		if rawJob.AssetDigest == "" {
			if err := s.loadAssets(ctx, &rawJob); err != nil {
				return nil, "plate assets failed", err
			}
			plate := renderFn(rawJob.Doc, rawJob.Bind, max(1, rawJob.Doc.Poster), 1280, 720)
			pixels := make([]byte, width*height*3/2)
			rgbaScaleToI420(plate, pixels, width, height)
			reader := &plateReader{remaining: plateFrames(rawJob.Doc.Duration, fpsNum, fpsDen, loop), pixels: pixels, audio: make([]byte, (48000*fpsDen/fpsNum)*4)}
			return &MomentStream{mode: "static-plate", loop: loop, fpsNum: fpsNum, fpsDen: fpsDen, width: width, height: height, baseReader: reader, service: s, lookID: rawJob.Look, moment: r.Moment}, "static-plate", nil
		}
		return nil, reason, fmt.Errorf("%w: %s", ErrLiveBelowRealtime, reason)
	}

	// Determine what scene document renders in the base pass and whether to save.
	var baseJob renderJob
	saveMaster := false
	mode := "live-nosave"
	if cacheable {
		baseJob = cacheJob
		if err := s.loadAssets(ctx, &baseJob); err != nil {
			return nil, "base assets failed", err
		}
		saveMaster = baseJob.Hash != "" && baseJob.AssetDigest == ""
		if overlayDoc != nil {
			mode = "split-live-save"
		} else {
			mode = "live-save"
		}
	} else {
		baseJob = rawJob
		if hasLive && r.Moment == "tonight" {
			scene.RewriteLineupHeading(&baseJob.Doc, r.Bind["lineup.heading"])
		}
		if err := s.loadAssets(ctx, &baseJob); err != nil {
			return nil, "live assets failed", err
		}
	}

	stream, err := s.startLiveMomentStream(ctx, baseJob, overlayDoc, overlayBind, width, height, fpsNum, fpsDen, loop, saveMaster, mode, renderFn)
	if err != nil {
		return nil, err.Error(), err
	}
	return stream, mode, nil
}

func (s *Service) startLiveMomentStream(
	parentCtx context.Context,
	baseJob renderJob,
	overlayDoc *scene.Document,
	overlayBind raster.Bindings,
	width, height, fpsNum, fpsDen int,
	loop, saveMaster bool,
	mode string,
	renderFn func(scene.Document, raster.Bindings, float64, int, int) *image.RGBA,
) (*MomentStream, error) {
	durSec := baseJob.Doc.Duration
	if durSec <= 0 {
		durSec = 8
	}
	masterFPS := 30
	masterFrames := max(1, int(math.Round(durSec*float64(masterFPS))))
	totalOutTicks := max(1, int(math.Round(durSec*float64(fpsNum)/float64(fpsDen))))

	var pcmSamples []int16
	if baseJob.Doc.Audio != nil {
		secs := max(1, int(math.Round(durSec)))
		if samples, err := encode.PCM(*baseJob.Doc.Audio, secs); err == nil {
			pcmSamples = samples
		}
	}

	claimedSave := saveMaster && s.claimLiveSave(baseJob.Hash)
	var writer *encode.StreamWriter
	outPath := filepath.Join(s.o.Dir, baseJob.Hash+".mp4")
	if claimedSave && s.o.Render == nil && s.o.FFmpeg != nil && s.o.FFmpeg() != "" && masterFrames%120 == 0 {
		opts, err := s.masterEncodeOptions(parentCtx, baseJob)
		if err == nil {
			w, wErr := encode.StartStream(s.ctx, s.o.FFmpeg(), masterFrames, masterFPS, "", outPath, opts)
			if wErr == nil {
				writer = w
			} else {
				s.releaseLiveSave(baseJob.Hash)
				claimedSave = false
			}
		} else {
			s.releaseLiveSave(baseJob.Hash)
			claimedSave = false
		}
	} else if claimedSave && s.o.Render == nil {
		s.releaseLiveSave(baseJob.Hash)
		claimedSave = false
	}

	outBytes := width * height * 3 / 2
	videoPool := &sync.Pool{New: func() any { b := make([]byte, outBytes); return &b }}
	outCh := make(chan liveOutFrame, 6)
	runCtx, cancelRun := context.WithCancel(s.ctx)

	ms := &MomentStream{
		mode:       mode,
		loop:       loop,
		fpsNum:     fpsNum,
		fpsDen:     fpsDen,
		width:      width,
		height:     height,
		outCh:      outCh,
		videoPool:  videoPool,
		parentCtx:  parentCtx,
		service:    s,
		lookID:     baseJob.Look,
		moment:     baseJob.Request.Moment,
		renderFn:   renderFn,
		overlayDoc: overlayDoc,
		overlayBnd: overlayBind,
	}
	if ms.moment == "" {
		ms.moment = baseJob.Doc.Moment
	}

	var stopMu sync.Mutex
	consumerStopped := false
	consumerAborted := false
	ms.stopLive = func(aborted bool) {
		stopMu.Lock()
		consumerStopped = true
		consumerAborted = aborted
		stopMu.Unlock()
		if aborted || !claimedSave {
			cancelRun()
		}
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(outCh)
		defer func() {
			if writer != nil {
				writer.Abort()
			}
			if claimedSave {
				s.releaseLiveSave(baseJob.Hash)
			}
		}()

		renderW, renderH := width, height
		if claimedSave {
			renderW, renderH = 1920, 1080
		}

		// Use 2 bounded workers to stay comfortably ahead of real-time playout.
		type masterResult struct {
			idx   int
			frame *image.RGBA
		}
		startedAt := time.Now()
		var backpressureWait time.Duration
		var renderWall time.Duration
		renderedCount := 0
		savedOnce := false

		commitMaster := func() {
			if !claimedSave || savedOnce {
				return
			}
			var saveErr error
			if s.o.Render != nil {
				saveErr = s.o.Render(runCtx, baseJob.Doc, baseJob.Bind, outPath)
			} else if writer != nil {
				saveErr = writer.Close()
				writer = nil
			}
			if saveErr == nil {
				if st, stErr := os.Stat(outPath); stErr == nil {
					s.mu.Lock()
					pin := s.pins[baseJob.Hash]
					if pin.IsZero() {
						pin = s.now().Add(3 * time.Hour)
					}
					s.cache[baseJob.Hash] = entry{
						Ref:  core.ClipRef{Hash: baseJob.Hash, Duration: time.Duration(durSec * float64(time.Second)), Path: outPath},
						Size: st.Size(),
						Used: s.now(),
						Pin:  pin,
					}
					delete(s.pending, baseJob.Hash)
					s.vis.MastersRendered++
					s.vis.LiveSaved++
					s.refreshProgress(baseJob.Look)
					s.evict()
					s.mu.Unlock()
					meta, _ := json.Marshal(baseJob.Doc.Duration)
					_ = os.WriteFile(filepath.Join(s.o.Dir, baseJob.Hash+".json"), meta, 0640)
					savedOnce = true
					ms.saved = true
				}
			} else if s.ctx.Err() == nil && !errors.Is(saveErr, context.Canceled) {
				s.mu.Lock()
				s.vis.MastersFailed++
				s.mu.Unlock()
				s.o.Logger.Warn("onair live master save failed", "look", baseJob.Look, "moment", ms.moment, "hash", baseJob.Hash, "error", saveErr)
			}
			s.releaseLiveSave(baseJob.Hash)
			claimedSave = false
		}

		// Render pass 0 via 2-worker sliding window so multi-core sustains >= 1.0x.
		pass := 0
		globalOutTick := 0
		for {
			if runCtx.Err() != nil {
				return
			}
			stopMu.Lock()
			stopped, aborted := consumerStopped, consumerAborted
			stopMu.Unlock()
			if stopped && (aborted || !claimedSave || savedOnce) {
				return
			}

			// Run one full scene pass (masterFrames at 30fps when saving, or totalOutTicks directly).
			passFrames := totalOutTicks
			passFPSNum, passFPSDen := fpsNum, fpsDen
			if claimedSave && pass == 0 {
				passFrames = masterFrames
				passFPSNum, passFPSDen = masterFPS, 1
			}

			jobs := make(chan int)
			results := make(chan masterResult, 2)
			var wwg sync.WaitGroup
			passCtx, cancelPass := context.WithCancel(runCtx)
			for range 2 {
				wwg.Add(1)
				go func() {
					defer wwg.Done()
					for idx := range jobs {
						if passCtx.Err() != nil {
							return
						}
						t := float64(idx) * float64(passFPSDen) / float64(passFPSNum)
						im := renderFn(baseJob.Doc, baseJob.Bind, t, renderW, renderH)
						select {
						case results <- masterResult{idx: idx, frame: im}:
						case <-passCtx.Done():
							return
						}
					}
				}()
			}
			go func() { wwg.Wait(); close(results) }()

			nextIdx, submitted := 0, 0
			pending := map[int]*image.RGBA{}
			outCursorInPass := 0
			abortedSpeed := false

			for nextIdx < passFrames && !abortedSpeed {
				var send chan int
				if submitted < passFrames && submitted-nextIdx < 2 {
					send = jobs
				}
				select {
				case <-passCtx.Done():
					nextIdx = passFrames
				case send <- submitted:
					submitted++
				case res, ok := <-results:
					if !ok {
						nextIdx = passFrames
						break
					}
					pending[res.idx] = res.frame
					for {
						im, ready := pending[nextIdx]
						if !ready {
							break
						}
						delete(pending, nextIdx)

						if writer != nil && pass == 0 {
							if err := writer.WriteFrame(im); err != nil {
								writer.Abort()
								writer = nil
								s.releaseLiveSave(baseJob.Hash)
								claimedSave = false
							}
						}

						renderedCount++
						renderWall = max(time.Nanosecond, time.Since(startedAt)-backpressureWait)
						genSec := float64(renderedCount) * float64(passFPSDen) / float64(passFPSNum)
						ms.lastSpeedX = genSec / renderWall.Seconds()

						// Speed gate: if live rendering cannot sustain >= 1.0x realtime
						// after warming up initial worker pipeline, fall back cleanly.
						if genSec >= liveSpeedGrace && ms.lastSpeedX > 0 && ms.lastSpeedX < liveSpeedFloor {
							s.recordSlowMoment(baseJob.Look, ms.moment, ms.lastSpeedX)
							s.noteTransition("fallback", ms.lastSpeedX)
							s.o.Logger.Info("onair live render below realtime; falling back",
								"look", baseJob.Look,
								"moment", ms.moment,
								"speed_x", round2(ms.lastSpeedX),
								"reason", "render_speed_below_1x",
							)
							select {
							case outCh <- liveOutFrame{err: ErrLiveBelowRealtime}:
							case <-passCtx.Done():
							}
							abortedSpeed = true
							break
						}

						// Emit 0 or more output ticks mapped to nextIdx.
						stopMu.Lock()
						cStopped, cAborted := consumerStopped, consumerAborted
						stopMu.Unlock()
						if cStopped {
							// Drain remaining frames only if we're >= 75% done with saving master.
							if cAborted || !claimedSave || nextIdx < passFrames*3/4 {
								abortedSpeed = true
								break
							}
						} else {
							for outCursorInPass < totalOutTicks {
								mapped := outCursorInPass * passFrames / totalOutTicks
								if mapped > nextIdx && nextIdx < passFrames-1 {
									break
								}
								yuvBuf := videoPool.Get().(*[]byte)
								yuv := *yuvBuf
								if renderW == width && renderH == height {
									rgbaToI420(im, yuv, width, height)
								} else {
									rgbaScaleToI420(im, yuv, width, height)
								}
								if overlayDoc != nil {
									ovT := float64(outCursorInPass) * float64(fpsDen) / float64(fpsNum)
									ov := renderFn(*overlayDoc, overlayBind, ovT, width, height)
									blendPremulRGBAOverI420(ov, yuv, width, height)
								}
								audioBytes := sliceTickAudio(pcmSamples, globalOutTick, fpsNum, fpsDen)
								globalOutTick++
								outCursorInPass++

								frameOut := liveOutFrame{video: yuvBuf, audio: audioBytes}
								select {
								case outCh <- frameOut:
								default:
									waitBegan := time.Now()
									sent := false
									for !sent {
										stopMu.Lock()
										cs := consumerStopped
										stopMu.Unlock()
										if cs {
											videoPool.Put(yuvBuf)
											break
										}
										select {
										case outCh <- frameOut:
											sent = true
										case <-time.After(10 * time.Millisecond):
										case <-passCtx.Done():
											videoPool.Put(yuvBuf)
											sent = true
										}
									}
									backpressureWait += time.Since(waitBegan)
								}
							}
						}
						nextIdx++
					}
				}
			}
			close(jobs)
			cancelPass()
			wwg.Wait()

			if abortedSpeed {
				return
			}
			if pass == 0 {
				commitMaster()
				s.recordMomentSpeed(baseJob.Look, ms.moment, ms.lastSpeedX)
				s.noteTransition(mode, ms.lastSpeedX)
			}
			pass++
			if !loop {
				return
			}
			stopMu.Lock()
			if consumerStopped {
				stopMu.Unlock()
				return
			}
			stopMu.Unlock()
		}
	}()

	return ms, nil
}

func (s *Service) masterEncodeOptions(ctx context.Context, j renderJob) (encode.Options, error) {
	var sounds []encode.SoundInput
	prepare := func(digest, kind string) (string, error) {
		var stored string
		if err := s.db.QueryRowContext(ctx, "SELECT kind FROM onair_assets WHERE digest=?", digest).Scan(&stored); err != nil {
			return "", err
		}
		if stored != kind {
			return "", sql.ErrNoRows
		}
		return s.assetPath(ctx, digest)
	}
	for _, sound := range j.Scene.Sounds {
		path, err := prepare(sound.AssetDigest, "audio")
		if err != nil {
			return encode.Options{}, err
		}
		sounds = append(sounds, encode.SoundInput{Path: path, At: sound.At, Start: sound.Start, End: sound.End, Volume: sound.Volume, FadeIn: sound.FadeIn, FadeOut: sound.FadeOut})
	}
	var background *encode.VideoInput
	if v := j.Scene.Video; v != nil && v.Mode == "background" && j.Request.AssetDigest == "" {
		path, err := prepare(v.AssetDigest, "video")
		if err != nil {
			return encode.Options{}, err
		}
		background = &encode.VideoInput{Path: path, Fit: v.Fit, Start: v.Start}
	}
	opts := encode.Options{Workers: 2, Audio: j.Doc.Audio, Sounds: sounds, Background: background}
	if strings.HasPrefix(j.Doc.Moment, "plugin:") {
		opts.MaxSeconds = 600
	}
	return opts, nil
}

func (s *Service) claimLiveSave(hash string) bool {
	if hash == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, cached := s.cache[hash]; cached {
		return false
	}
	if s.liveSaving == nil {
		s.liveSaving = map[string]bool{}
	}
	if s.liveSaving[hash] {
		return false
	}
	s.liveSaving[hash] = true
	return true
}

func (s *Service) releaseLiveSave(hash string) {
	if hash == "" {
		return
	}
	s.mu.Lock()
	delete(s.liveSaving, hash)
	s.mu.Unlock()
}

func (s *Service) recordMomentSpeed(look, moment string, speedX float64) {
	if s == nil || speedX <= 0 {
		return
	}
	key := look + "/" + moment
	s.mu.Lock()
	if s.liveSpeed == nil {
		s.liveSpeed = map[string]liveSpeedRecord{}
	}
	prev := s.liveSpeed[key]
	if prev.speedX > 0 {
		speedX = (prev.speedX*2 + speedX) / 3
	}
	s.liveSpeed[key] = liveSpeedRecord{speedX: speedX}
	s.vis.LastSpeedX = speedX
	s.mu.Unlock()
}

func (s *Service) recordSlowMoment(look, moment string, speedX float64) {
	if s == nil {
		return
	}
	key := look + "/" + moment
	s.mu.Lock()
	if s.liveSpeed == nil {
		s.liveSpeed = map[string]liveSpeedRecord{}
	}
	s.liveSpeed[key] = liveSpeedRecord{
		speedX: speedX,
		retry:  s.now().Add(10 * time.Minute),
	}
	s.vis.LastSpeedX = speedX
	s.mu.Unlock()
}

func (s *Service) noteTransition(mode string, speedX float64) {
	if s == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	switch mode {
	case "cached":
		s.vis.CachedAired++
	case "split-cached":
		s.vis.SplitAired++
	case "live-save", "split-live-save", "live-nosave":
		s.vis.LiveAired++
	case "fallback":
		s.vis.Fallbacks++
	}
	if speedX > 0 {
		s.vis.LastSpeedX = speedX
	}
	shouldLog := s.lastVisLog.IsZero() || now.Sub(s.lastVisLog) >= time.Hour || (mode != s.lastVisMode && (mode == "live-save" || mode == "split-live-save" || mode == "fallback"))
	snap := s.vis
	if shouldLog {
		s.lastVisLog = now
		s.lastVisMode = mode
	}
	s.mu.Unlock()
	if shouldLog {
		s.o.Logger.Info("onair visibility summary",
			"mode", mode,
			"masters_rendered", snap.MastersRendered,
			"masters_failed", snap.MastersFailed,
			"live_saved", snap.LiveSaved,
			"cached_aired", snap.CachedAired,
			"split_aired", snap.SplitAired,
			"live_aired", snap.LiveAired,
			"fallbacks", snap.Fallbacks,
			"render_speed_x", round2(snap.LastSpeedX),
		)
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func sliceTickAudio(pcm []int16, tick, fpsNum, fpsDen int) []byte {
	startSample := int((int64(tick) * 48000 * int64(fpsDen)) / int64(fpsNum))
	endSample := int((int64(tick+1) * 48000 * int64(fpsDen)) / int64(fpsNum))
	count := max(0, endSample-startSample)
	out := make([]byte, count*4)
	if len(pcm) < 2 || count == 0 {
		return out
	}
	totalFrames := len(pcm) / 2
	for i := 0; i < count; i++ {
		idx := (startSample + i) % totalFrames
		l := uint16(pcm[idx*2])
		r := uint16(pcm[idx*2+1])
		binary.LittleEndian.PutUint16(out[i*4:], l)
		binary.LittleEndian.PutUint16(out[i*4+2:], r)
	}
	return out
}

// rgbaToI420 converts premultiplied RGBA pixels to studio-swing BT.709 I420.
func rgbaToI420(img *image.RGBA, dst []byte, w, h int) {
	pix := img.Pix
	stride := img.Stride
	yPlane := dst[:w*h]
	uPlane := dst[w*h : w*h+w*h/4]
	vPlane := dst[w*h+w*h/4:]

	for y := 0; y < h; y += 2 {
		row0 := y * stride
		row1 := (y + 1) * stride
		yOut0 := y * w
		yOut1 := (y + 1) * w
		uvOut := (y / 2) * (w / 2)
		for x := 0; x < w; x += 2 {
			p00 := row0 + x*4
			p01 := p00 + 4
			p10 := row1 + x*4
			p11 := p10 + 4

			r00, g00, b00 := int(pix[p00]), int(pix[p00+1]), int(pix[p00+2])
			r01, g01, b01 := int(pix[p01]), int(pix[p01+1]), int(pix[p01+2])
			r10, g10, b10 := int(pix[p10]), int(pix[p10+1]), int(pix[p10+2])
			r11, g11, b11 := int(pix[p11]), int(pix[p11+1]), int(pix[p11+2])

			yPlane[yOut0+x] = byte(16 + ((47*r00 + 157*g00 + 16*b00 + 128) >> 8))
			yPlane[yOut0+x+1] = byte(16 + ((47*r01 + 157*g01 + 16*b01 + 128) >> 8))
			yPlane[yOut1+x] = byte(16 + ((47*r10 + 157*g10 + 16*b10 + 128) >> 8))
			yPlane[yOut1+x+1] = byte(16 + ((47*r11 + 157*g11 + 16*b11 + 128) >> 8))

			rS := r00 + r01 + r10 + r11
			gS := g00 + g01 + g10 + g11
			bS := b00 + b01 + b10 + b11
			uPlane[uvOut+x/2] = byte(128 + ((-26*rS - 87*gS + 112*bS + 512) >> 10))
			vPlane[uvOut+x/2] = byte(128 + ((112*rS - 102*gS - 10*bS + 512) >> 10))
		}
	}
}

func rgbaScaleToI420(img *image.RGBA, dst []byte, w, h int) {
	srcW, srcH := img.Rect.Dx(), img.Rect.Dy()
	pix := img.Pix
	stride := img.Stride
	yPlane := dst[:w*h]
	uPlane := dst[w*h : w*h+w*h/4]
	vPlane := dst[w*h+w*h/4:]

	for y := 0; y < h; y += 2 {
		sy0 := (y * srcH) / h
		sy1 := ((y + 1) * srcH) / h
		row0 := sy0 * stride
		row1 := sy1 * stride
		yOut0 := y * w
		yOut1 := (y + 1) * w
		uvOut := (y / 2) * (w / 2)
		for x := 0; x < w; x += 2 {
			sx0 := (x * srcW) / w
			sx1 := ((x + 1) * srcW) / w
			p00 := row0 + sx0*4
			p01 := row0 + sx1*4
			p10 := row1 + sx0*4
			p11 := row1 + sx1*4

			r00, g00, b00 := int(pix[p00]), int(pix[p00+1]), int(pix[p00+2])
			r01, g01, b01 := int(pix[p01]), int(pix[p01+1]), int(pix[p01+2])
			r10, g10, b10 := int(pix[p10]), int(pix[p10+1]), int(pix[p10+2])
			r11, g11, b11 := int(pix[p11]), int(pix[p11+1]), int(pix[p11+2])

			yPlane[yOut0+x] = byte(16 + ((47*r00 + 157*g00 + 16*b00 + 128) >> 8))
			yPlane[yOut0+x+1] = byte(16 + ((47*r01 + 157*g01 + 16*b01 + 128) >> 8))
			yPlane[yOut1+x] = byte(16 + ((47*r10 + 157*g10 + 16*b10 + 128) >> 8))
			yPlane[yOut1+x+1] = byte(16 + ((47*r11 + 157*g11 + 16*b11 + 128) >> 8))

			rS := r00 + r01 + r10 + r11
			gS := g00 + g01 + g10 + g11
			bS := b00 + b01 + b10 + b11
			uPlane[uvOut+x/2] = byte(128 + ((-26*rS - 87*gS + 112*bS + 512) >> 10))
			vPlane[uvOut+x/2] = byte(128 + ((112*rS - 102*gS - 10*bS + 512) >> 10))
		}
	}
}

// blendPremulRGBAOverI420 composites a premultiplied RGBA foreground onto an I420 frame.
func blendPremulRGBAOverI420(img *image.RGBA, dst []byte, w, h int) {
	if img == nil || img.Rect.Dx() < w || img.Rect.Dy() < h {
		return
	}
	pix := img.Pix
	stride := img.Stride
	yPlane := dst[:w*h]
	uPlane := dst[w*h : w*h+w*h/4]
	vPlane := dst[w*h+w*h/4:]

	for y := 0; y < h; y += 2 {
		row0 := y * stride
		row1 := (y + 1) * stride
		yOut0 := y * w
		yOut1 := (y + 1) * w
		uvOut := (y / 2) * (w / 2)
		for x := 0; x < w; x += 2 {
			p00 := row0 + x*4
			p01 := p00 + 4
			p10 := row1 + x*4
			p11 := p10 + 4

			a00, a01 := int(pix[p00+3]), int(pix[p01+3])
			a10, a11 := int(pix[p10+3]), int(pix[p11+3])
			sumA := a00 + a01 + a10 + a11
			if sumA == 0 {
				continue
			}

			r00, g00, b00 := int(pix[p00]), int(pix[p00+1]), int(pix[p00+2])
			r01, g01, b01 := int(pix[p01]), int(pix[p01+1]), int(pix[p01+2])
			r10, g10, b10 := int(pix[p10]), int(pix[p10+1]), int(pix[p10+2])
			r11, g11, b11 := int(pix[p11]), int(pix[p11+1]), int(pix[p11+2])

			if a00 > 0 {
				yPremul := 16*a00 + (((47*r00 + 157*g00 + 16*b00) * 255) >> 8)
				yPlane[yOut0+x] = byte((yPremul + int(yPlane[yOut0+x])*(255-a00) + 127) / 255)
			}
			if a01 > 0 {
				yPremul := 16*a01 + (((47*r01 + 157*g01 + 16*b01) * 255) >> 8)
				yPlane[yOut0+x+1] = byte((yPremul + int(yPlane[yOut0+x+1])*(255-a01) + 127) / 255)
			}
			if a10 > 0 {
				yPremul := 16*a10 + (((47*r10 + 157*g10 + 16*b10) * 255) >> 8)
				yPlane[yOut1+x] = byte((yPremul + int(yPlane[yOut1+x])*(255-a10) + 127) / 255)
			}
			if a11 > 0 {
				yPremul := 16*a11 + (((47*r11 + 157*g11 + 16*b11) * 255) >> 8)
				yPlane[yOut1+x+1] = byte((yPremul + int(yPlane[yOut1+x+1])*(255-a11) + 127) / 255)
			}

			rS := r00 + r01 + r10 + r11
			gS := g00 + g01 + g10 + g11
			bS := b00 + b01 + b10 + b11
			uPremul := 128*sumA + (((-26*rS - 87*gS + 112*bS) * 255) >> 8)
			vPremul := 128*sumA + (((112*rS - 102*gS - 10*bS) * 255) >> 8)
			uvi := uvOut + x/2
			uVal := (uPremul + int(uPlane[uvi])*(1020-sumA) + 510) / 1020
			vVal := (vPremul + int(vPlane[uvi])*(1020-sumA) + 510) / 1020
			uPlane[uvi] = byte(max(16, min(240, uVal)))
			vPlane[uvi] = byte(max(16, min(240, vVal)))
		}
	}
}

// plateReader holds a single decoded image, equivalent to a looped image input.
// Audio remains silent while the reusable master is being prepared.
type plateReader struct {
	pixels, audio []byte
	remaining     int64 // -1 for looping moments
}

func plateFrames(duration float64, num, den int, loop bool) int64 {
	if loop {
		return -1
	}
	return max(1, int64(math.Ceil(duration*float64(num)/float64(den))))
}

func (p *plateReader) ReadFrame(video []byte) ([]byte, error) {
	if p.remaining == 0 {
		return nil, io.EOF
	}
	if p.remaining > 0 {
		p.remaining--
	}
	copy(video, p.pixels)
	return p.audio, nil
}
func (p *plateReader) Close() error { return nil }

// primedReader transfers the first decoded frame without blocking the splice.
type primedReader struct {
	FrameReader
	video, audio []byte
}

func (p *primedReader) ReadFrame(video []byte) ([]byte, error) {
	if p.video != nil {
		copy(video, p.video)
		p.video = nil
		return p.audio, nil
	}
	return p.FrameReader.ReadFrame(video)
}
