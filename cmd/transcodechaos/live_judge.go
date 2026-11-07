package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The protocol judge reads the client playlist as an HLS player does and
// checks what Ether published. It runs everywhere; it has no notion of
// rendering, so viewer freeze from it is a buffer model, not an observation.

type segProbe struct {
	Video    string `json:"video,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Audio    string `json:"audio,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Rate     int    `json:"rate,omitempty"`
}

func (p segProbe) codecs() string { return p.Video + "/" + p.Audio }
func (p segProbe) format() string {
	return fmt.Sprintf("%dx%d/%dch/%dHz", p.Width, p.Height, p.Channels, p.Rate)
}

type judgedSegment struct {
	Seq       int64    `json:"seq"`
	Path      string   `json:"path"`
	Duration  float64  `json:"duration"`
	Disc      bool     `json:"discontinuity,omitempty"`
	DiscNum   int64    `json:"discontinuityNumber"`
	SeenAt    float64  `json:"seenAt"`
	FetchedAt float64  `json:"fetchedAt,omitempty"`
	Status    int      `json:"status"`
	Bytes     int      `json:"bytes"`
	Probe     segProbe `json:"probe"`
	// Content analysis of the published media, sampled at 10 Hz.
	MediaS       float64 `json:"mediaSeconds"` // measured, vs the EXTINF Duration
	ContentS     float64 `json:"contentSeconds"`
	NonContentS  float64 `json:"nonContentSeconds"`
	FrozenS      float64 `json:"frozenSeconds"`
	SilenceS     float64 `json:"silenceSeconds"`
	DecodeErrors int     `json:"decodeErrors"`
	file         string
}

type protocolReport struct {
	TargetDuration float64 `json:"targetDuration"`
	FirstPlaylistS float64 `json:"firstPlaylistSeconds"`
	// FirstPlaylistResponseS is how long Ether took to answer the first
	// playlist request that returned one (its own handling time when known).
	// AVFoundation leaves an item stuck for good, with no error, when that
	// response takes a whole target duration (measured on macOS 26).
	FirstPlaylistResponseS float64 `json:"firstPlaylistResponseSeconds"`
	FirstSegmentS          float64 `json:"firstSegmentSeconds"`
	Segments               int     `json:"segments"`
	LongestNoNewSegmentS   float64 `json:"longestNoNewSegmentSeconds"`
	LongestNoNewAt         float64 `json:"longestNoNewSegmentAt"`
	// Adj excludes time the judge itself was blocked (JudgeBlindS).
	LongestNoNewSegmentAdjS float64 `json:"longestNoNewSegmentAdjustedSeconds"`
	JudgeBlindS             float64 `json:"judgeBlindSeconds,omitempty"`
	MaxSegmentS             float64 `json:"maxSegmentSeconds"`
	// MaxMediaS is the longest measured segment; DurationMismatches are
	// segments whose media differs from their EXTINF by over 0.5 s.
	MaxMediaS            float64        `json:"maxMediaSeconds"`
	DurationMismatches   []string       `json:"durationMismatches,omitempty"`
	OverlongSegments     int            `json:"overlongSegments"`
	UnmarkedCodecChanges []string       `json:"unmarkedCodecChanges,omitempty"`
	UnmarkedFormatChange []string       `json:"unmarkedFormatChanges,omitempty"`
	Discontinuities      int            `json:"discontinuities"`
	SequenceErrors       []string       `json:"sequenceErrors,omitempty"`
	HTTPErrors           map[string]int `json:"httpErrors,omitempty"`
	DecodeErrors         int            `json:"decodeErrors"`
	MaxPlaylistEntries   int            `json:"maxPlaylistEntries"`
	Ended                bool           `json:"ended,omitempty"`
	NonContentS          float64        `json:"nonContentSeconds"`
	FrozenContentS       float64        `json:"frozenContentSeconds"`
	SilenceS             float64        `json:"silenceSeconds"`
	ModelStallS          float64        `json:"modelStallSeconds"`
	ModelFatal           string         `json:"modelFatal,omitempty"`
	LastContentAt        float64        `json:"lastContentAt"`
	RunEndS              float64        `json:"runEndSeconds"`
	// SlowPolls are playlist requests where the judge, not Ether, was
	// blocked for over 0.5 s ([at, seconds]); a loaded host causes them.
	SlowPolls       [][2]float64     `json:"slowPolls,omitempty"`
	MaxPollLatencyS float64          `json:"maxPollLatencySeconds"`
	SegmentLog      []*judgedSegment `json:"segmentLog,omitempty"`
}

type playlistEntry struct {
	seq      int64
	uri      string
	dur      float64
	disc     bool
	discNum  int64
	hasMap   string
	gapTag   bool
	rawIndex int
}

type parsedPlaylist struct {
	td      float64
	msn     int64
	dsn     int64
	entries []playlistEntry
	ended   bool
	hasDSN  bool
}

func parseMediaPlaylist(body string) (parsedPlaylist, error) {
	var p parsedPlaylist
	if !strings.HasPrefix(strings.TrimSpace(body), "#EXTM3U") {
		return p, fmt.Errorf("not a playlist")
	}
	var dur float64
	disc, gap := false, false
	mapURI := ""
	seq := int64(-1)
	discNum := int64(0)
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			p.td, _ = strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64)
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			p.msn, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			p.dsn, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
			p.hasDSN = true
		case line == "#EXT-X-DISCONTINUITY":
			disc = true
		case line == "#EXT-X-GAP":
			gap = true
		case line == "#EXT-X-ENDLIST":
			p.ended = true
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			if m := regexp.MustCompile(`URI="([^"]+)"`).FindStringSubmatch(line); m != nil {
				mapURI = m[1]
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			dur, _ = strconv.ParseFloat(v, 64)
		case line != "" && !strings.HasPrefix(line, "#"):
			if seq < 0 {
				seq = p.msn
				discNum = p.dsn
			}
			if disc {
				discNum++
			}
			p.entries = append(p.entries, playlistEntry{seq: seq, uri: line, dur: dur, disc: disc, discNum: discNum, hasMap: mapURI, gapTag: gap, rawIndex: len(p.entries)})
			seq++
			disc, gap = false, false
		}
	}
	return p, nil
}

type protocolJudge struct {
	ff, fp   string
	playlist string
	dir      string
	started  time.Time
	client   *http.Client
	mu       sync.Mutex
	report   protocolReport
	known    map[int64]*judgedSegment
	order    []*judgedSegment
	lastMSN  int64
	lastDSN  int64
	queue    chan *judgedSegment
	wg       sync.WaitGroup
	polls    int
	// handled reports Ether's own handling time for a tagged request.
	handled func(string) (float64, bool)
	// work bounds fetch and analysis; it outlives the polling window so
	// segments seen before the end are analysed completely.
	work context.Context
}

func newProtocolJudge(work context.Context, ff, fp, playlist, dir string, started time.Time, handled func(string) (float64, bool)) *protocolJudge {
	return &protocolJudge{work: work, handled: handled, ff: ff, fp: fp, playlist: playlist, dir: dir, started: started, client: &http.Client{Timeout: 30 * time.Second}, known: map[int64]*judgedSegment{}, lastMSN: -1, lastDSN: -1, queue: make(chan *judgedSegment, 256), report: protocolReport{HTTPErrors: map[string]int{}}}
}

func (j *protocolJudge) since() float64 { return time.Since(j.started).Seconds() }

func (j *protocolJudge) httpError(kind string, code int, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	key := fmt.Sprintf("%s %d", kind, code)
	if err != nil {
		key = kind + " " + shortNetError(err)
	}
	j.report.HTTPErrors[key]++
}

func shortNetError(err error) string {
	s := err.Error()
	for _, k := range []string{"connection reset", "connection refused", "EOF", "timeout", "deadline"} {
		if strings.Contains(s, k) {
			return k
		}
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// Run polls until ctx ends. Polling every 0.2 s measures the no-new-segment
// interval to +0.2 s: at a 2 s target duration the Apple limit is 3 s, so a
// coarser grid (0.5 s before) could report a 2.6 s gap as 3.0 s and fail it.
// The real player (avprobe) polls at its own cadence alongside.
func (j *protocolJudge) Run(ctx context.Context) {
	j.wg.Add(1)
	go j.fetcher(j.work)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		j.poll(ctx)
		select {
		case <-ctx.Done():
			close(j.queue)
			j.wg.Wait()
			return
		case <-tick.C:
		}
	}
}

func (j *protocolJudge) poll(ctx context.Context) {
	began := j.since()
	j.polls++
	id := strconv.Itoa(j.polls)
	served, servedOK := 0.0, false
	defer func() {
		lat := j.since() - began
		// Only the part Ether did not spend serving is the judge's blind time.
		if !servedOK {
			served, servedOK = j.handled(id)
		}
		if servedOK {
			lat -= served
		}
		j.mu.Lock()
		j.report.MaxPollLatencyS = math.Max(j.report.MaxPollLatencyS, lat)
		if lat > 0.5 && ctx.Err() == nil {
			j.report.SlowPolls = append(j.report.SlowPolls, [2]float64{math.Round(began*10) / 10, math.Round(lat*100) / 100})
		}
		j.mu.Unlock()
	}()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, j.playlist, nil)
	req.Header.Set("X-Chaos-Judge", id)
	resp, err := j.client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			j.httpError("playlist", 0, err)
		}
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		j.httpError("playlist", resp.StatusCode, nil)
		return
	}
	p, err := parseMediaPlaylist(string(body))
	if err != nil {
		j.httpError("playlist-parse", 0, err)
		return
	}
	now := j.since()
	if served, servedOK = j.handled(id); !servedOK {
		served = now - began
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.report.FirstPlaylistS == 0 {
		j.report.FirstPlaylistS = now
		j.report.FirstPlaylistResponseS = math.Round(served*1000) / 1000
	}
	if p.td > 0 {
		j.report.TargetDuration = p.td
	}
	j.report.Ended = j.report.Ended || p.ended
	j.report.MaxPlaylistEntries = max(j.report.MaxPlaylistEntries, len(p.entries))
	if j.lastMSN >= 0 && p.msn < j.lastMSN {
		j.report.SequenceErrors = append(j.report.SequenceErrors, fmt.Sprintf("%.1fs media sequence went back %d -> %d", now, j.lastMSN, p.msn))
	}
	if j.lastDSN >= 0 && p.dsn < j.lastDSN {
		j.report.SequenceErrors = append(j.report.SequenceErrors, fmt.Sprintf("%.1fs discontinuity sequence went back %d -> %d", now, j.lastDSN, p.dsn))
	}
	j.lastMSN, j.lastDSN = p.msn, p.dsn
	base, _ := url.Parse(j.playlist)
	for _, e := range p.entries {
		ref, err := url.Parse(e.uri)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if old, ok := j.known[e.seq]; ok {
			if old.Path != abs.Path || old.DiscNum != e.discNum {
				j.report.SequenceErrors = append(j.report.SequenceErrors, fmt.Sprintf("%.1fs seq %d changed: %s disc %d -> %s disc %d", now, e.seq, old.Path, old.DiscNum, abs.Path, e.discNum))
				old.Path, old.DiscNum = abs.Path, e.discNum
			}
			continue
		}
		if len(j.order) > 0 && e.seq < j.order[len(j.order)-1].Seq {
			continue // slid out before we saw it; not new content
		}
		seg := &judgedSegment{Seq: e.seq, Path: abs.Path, Duration: e.dur, Disc: e.disc, DiscNum: e.discNum, SeenAt: now}
		j.known[e.seq] = seg
		j.order = append(j.order, seg)
		if e.dur > j.report.MaxSegmentS {
			j.report.MaxSegmentS = e.dur
		}
		if p.td > 0 && e.dur > p.td+0.5 {
			j.report.OverlongSegments++
		}
		if e.disc {
			j.report.Discontinuities++
		}
		u := abs.String()
		if e.hasMap != "" {
			if mref, err := url.Parse(e.hasMap); err == nil {
				u += "\x00" + base.ResolveReference(mref).String()
			}
		}
		seg.file = u
		select {
		case j.queue <- seg:
		default:
		}
	}
}

func (j *protocolJudge) fetcher(ctx context.Context) {
	defer j.wg.Done()
	for seg := range j.queue {
		if ctx.Err() != nil {
			continue
		}
		j.fetch(ctx, seg)
	}
}

func (j *protocolJudge) get(ctx context.Context, u string) (int, []byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := j.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, b, err
}

func (j *protocolJudge) fetch(ctx context.Context, seg *judgedSegment) {
	parts := strings.SplitN(seg.file, "\x00", 2)
	var init []byte
	if len(parts) == 2 {
		code, b, err := j.get(ctx, parts[1])
		if err != nil || code != 200 {
			j.httpError("init", code, err)
		}
		init = b
	}
	code, body, err := j.get(ctx, parts[0])
	if ctx.Err() != nil {
		return
	}
	j.mu.Lock()
	seg.Status, seg.Bytes, seg.FetchedAt = code, len(body), j.since()
	if code == 200 && err == nil && j.report.FirstSegmentS == 0 {
		j.report.FirstSegmentS = seg.FetchedAt
	}
	j.mu.Unlock()
	if err != nil || code != 200 {
		j.httpError("segment", code, err)
		return
	}
	ext := ".ts"
	if init != nil {
		ext = ".mp4"
	}
	file := filepath.Join(j.dir, fmt.Sprintf("seg-%06d%s", seg.Seq, ext))
	if err := os.WriteFile(file, append(init, body...), 0o600); err != nil {
		return
	}
	probe := probeSegment(ctx, j.fp, file)
	content, noncontent, frozen, silence, decodeErrs := analyseSegment(ctx, j.ff, file, seg.Duration)
	j.mu.Lock()
	seg.Probe, seg.ContentS, seg.NonContentS, seg.FrozenS, seg.SilenceS, seg.DecodeErrors = probe, content, noncontent, frozen, silence, decodeErrs
	seg.MediaS = math.Round((content+noncontent)*10) / 10
	seg.file = file
	j.mu.Unlock()
}

func probeSegment(ctx context.Context, fp, file string) segProbe {
	out, err := exec.CommandContext(ctx, fp, "-v", "error", "-show_entries", "stream=codec_type,codec_name,width,height,channels,sample_rate", "-of", "json", file).Output()
	var p segProbe
	if err != nil {
		return p
	}
	var doc struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Name     string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Channels int    `json:"channels"`
			Rate     string `json:"sample_rate"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return p
	}
	for _, s := range doc.Streams {
		switch {
		case s.Type == "video" && p.Video == "":
			p.Video, p.Width, p.Height = s.Name, s.Width, s.Height
		case s.Type == "audio" && p.Audio == "":
			p.Audio, p.Channels = s.Name, s.Channels
			p.Rate, _ = strconv.Atoi(s.Rate)
		}
	}
	return p
}

var (
	reSilence = regexp.MustCompile(`silence_duration: ([0-9.]+)`)
	reSilOpen = regexp.MustCompile(`silence_start: ([0-9.\-]+)`)
	reFreeze  = regexp.MustCompile(`freeze_duration: ([0-9.]+)`)
	reFrzOpen = regexp.MustCompile(`freeze_start: ([0-9.\-]+)`)
)

// analyseSegment classifies 10 Hz samples of the fixture's green top band as
// provider content or not (Ether screens, black), and measures frozen video,
// silent audio and decoder errors of the media actually published.
func analyseSegment(ctx context.Context, ff, file string, dur float64) (content, noncontent, frozen, silence float64, decodeErrors int) {
	args := []string{"-hide_banner", "-nostats", "-loglevel", "level+info", "-i", file,
		"-filter_complex", "[0:v:0]setpts=PTS-STARTPTS,split[a][b];[a]fps=10,crop=iw/2:ih/40:iw/4:ih/100,scale=1:1:flags=area,format=rgb24[m];[b]freezedetect=n=-60dB:d=0.5[f]",
		"-map", "[m]", "-f", "rawvideo", "pipe:1", "-map", "[f]", "-f", "null", "-",
		"-map", "0:a:0?", "-af", "asetpts=PTS-STARTPTS,silencedetect=n=-45dB:d=0.25", "-f", "null", "-"}
	cmd := exec.CommandContext(ctx, ff, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	px := stdout.Bytes()
	var samples []bool // true = provider content
	for i := 0; i+3 <= len(px); i += 3 {
		r, g, b := px[i], px[i+1], px[i+2]
		samples = append(samples, g > 150 && r < 110 && b < 110)
	}
	end := float64(len(samples)) / 10
	if end == 0 {
		end = dur
	}
	hasAudio := strings.Contains(stderr.String(), "Audio:")
	var freezes [][2]float64
	silOpen, frzOpen := -1.0, -1.0
	sc := bufio.NewScanner(&stderr)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "[error]") || strings.Contains(line, "[fatal]") {
			decodeErrors++
		}
		if m := reSilence.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			silence += v
			silOpen = -1
		} else if m := reSilOpen.FindStringSubmatch(line); m != nil {
			silOpen, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := reFreeze.FindStringSubmatch(line); m != nil && frzOpen >= 0 {
			v, _ := strconv.ParseFloat(m[1], 64)
			freezes = append(freezes, [2]float64{frzOpen, frzOpen + v})
			frzOpen = -1
		} else if m := reFrzOpen.FindStringSubmatch(line); m != nil {
			frzOpen, _ = strconv.ParseFloat(m[1], 64)
		}
	}
	// Spans still open at the end of the segment run to its end.
	if silOpen >= 0 {
		silence += math.Max(0, end-silOpen)
	}
	if frzOpen >= 0 {
		freezes = append(freezes, [2]float64{frzOpen, end})
	}
	for i, isContent := range samples {
		t := float64(i) / 10
		if !isContent {
			noncontent += 0.1
			continue
		}
		content += 0.1
		for _, f := range freezes {
			if t >= f[0] && t < f[1] {
				frozen += 0.1
				break
			}
		}
	}
	if !hasAudio {
		silence = end
	}
	return
}

// Finish computes derived metrics after polling stops.
func (j *protocolJudge) Finish(runEnd float64) protocolReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := j.report
	r.RunEndS = runEnd
	sort.Slice(j.order, func(a, b int) bool { return j.order[a].Seq < j.order[b].Seq })
	// Gaps between newly listed segments, and to the end of the run. Time
	// the judge itself was blocked (slow playlist polls on a loaded host) is
	// reported but not charged to Ether.
	for i := range j.order {
		from := j.order[i].SeenAt
		to := runEnd
		if i+1 < len(j.order) {
			to = j.order[i+1].SeenAt
		}
		blind := 0.0
		for _, sp := range r.SlowPolls {
			lo, hi := math.Max(from, sp[0]), math.Min(to, sp[0]+sp[1])
			if hi > lo {
				blind += hi - lo
			}
		}
		if to-from > r.LongestNoNewSegmentS {
			r.LongestNoNewSegmentS, r.LongestNoNewAt = to-from, from
		}
		if to-from-blind > r.LongestNoNewSegmentAdjS {
			r.LongestNoNewSegmentAdjS, r.JudgeBlindS = to-from-blind, blind
		}
	}
	var prev *judgedSegment
	for _, s := range j.order {
		r.Segments++
		r.NonContentS += s.NonContentS
		r.FrozenContentS += s.FrozenS
		r.SilenceS += s.SilenceS
		r.DecodeErrors += s.DecodeErrors
		if s.ContentS > 0 {
			r.LastContentAt = s.SeenAt
		}
		if s.MediaS > 0 {
			r.MaxMediaS = math.Max(r.MaxMediaS, s.MediaS)
			if math.Abs(s.MediaS-s.Duration) > 0.5 {
				r.DurationMismatches = append(r.DurationMismatches, fmt.Sprintf("seq %d: EXTINF %.1fs, media %.1fs", s.Seq, s.Duration, s.MediaS))
			}
			if r.TargetDuration > 0 && s.MediaS > r.TargetDuration+0.5 && s.Duration <= r.TargetDuration+0.5 {
				r.OverlongSegments++
			}
		}
		if s.Status == 200 && s.Probe.Video != "" {
			if prev != nil && !s.Disc {
				if prev.Probe.codecs() != s.Probe.codecs() {
					r.UnmarkedCodecChanges = append(r.UnmarkedCodecChanges, fmt.Sprintf("seq %d: %s -> %s", s.Seq, prev.Probe.codecs(), s.Probe.codecs()))
				} else if prev.Probe.format() != s.Probe.format() {
					r.UnmarkedFormatChange = append(r.UnmarkedFormatChange, fmt.Sprintf("seq %d: %s -> %s", s.Seq, prev.Probe.format(), s.Probe.format()))
				}
			}
			prev = s
		}
		r.SegmentLog = append(r.SegmentLog, s)
	}
	r.ModelStallS, r.ModelFatal = playerModel(j.order, r.TargetDuration, runEnd)
	return r
}

// playerModel replays segment arrivals through a simple Apple-style live
// player: start three segments behind the newest one once the first is
// fetched, play in real time, stall when the buffer runs dry, resume after a
// full segment. A playlist that gains nothing for 1.5x target duration while
// the buffer is empty is the -12888 failure.
func playerModel(segs []*judgedSegment, td, end float64) (stall float64, fatal string) {
	if len(segs) == 0 || td <= 0 {
		return 0, "no segments"
	}
	start := 0
	if len(segs) > 3 {
		// The first playlist's newest three segments.
		first := segs[0].SeenAt
		n := 0
		for n < len(segs) && segs[n].SeenAt <= first+0.01 {
			n++
		}
		start = max(0, n-3)
	}
	t := segs[start].FetchedAt
	if t == 0 {
		return 0, "first segment never fetched"
	}
	buffer, i := 0.0, start
	stalled := false
	lastNew := segs[start].SeenAt
	const step = 0.1
	for ; t < end; t += step {
		for i < len(segs) && segs[i].FetchedAt > 0 && segs[i].FetchedAt <= t {
			if segs[i].Status == 200 {
				buffer += segs[i].Duration
			}
			lastNew = math.Max(lastNew, segs[i].SeenAt)
			i++
		}
		if i < len(segs) && segs[i].SeenAt <= t {
			lastNew = math.Max(lastNew, segs[i].SeenAt)
		}
		if t-lastNew > 1.5*td && fatal == "" {
			fatal = fmt.Sprintf("-12888 predicted at %.1fs (playlist gained no segment for %.1fs)", t, t-lastNew)
		}
		if stalled {
			stall += step
			if buffer >= td {
				stalled = false
			}
			continue
		}
		if buffer <= 0 {
			stalled = true
			stall += step
			continue
		}
		buffer -= step
	}
	return stall, fatal
}
