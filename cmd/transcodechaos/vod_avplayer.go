package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// -vod-avplayer plays the corpus client sequences (seek forward/back, scrub,
// pause/resume to the end, track and quality restarts) in AVPlayer through
// Ether's real Plex handlers, next to the same sequences decoded by ffmpeg
// -xerror (TestTranscodeChaosCorpusScenarios), and judges what a viewer saw.
// The ffmpeg-pass/AVPlayer-fail cases are the baseline WS5 exists for.

type vodOptions struct {
	FF, FP, Fixtures, Cache, Report, Markdown, Only, Keep, RepoRoot, Label string
	// Transcode runs only the sustained full-transcode case per asset (the
	// VOD side of the live-priority row).
	Transcode bool
	// PlayerOnly skips the ffmpeg comparison, whose existing results can be reused
	// for focused fix verification. The report records that comparison as n/a.
	PlayerOnly bool
}

const (
	vodTTFFMaxS        = 5.0 // cold start target (owner: 4-5 s)
	vodResumeMaxS      = 5.0 // a seek restarts encoding: same bar as a cold start
	vodResumeWarnS     = 2.0
	vodLandingMaxS     = 0.25 // exact seek, measured on the picture shown (sync fixture)
	vodItemLandingMaxS = 0.5  // exact seek, playhead only (other media)
	vodRestartMaxS     = 1.0  // Plex restart at offset (track/quality change)
	vodOffsetSpreadMax = 0.1  // content time minus item time, within one item
)

type vodPlayer struct {
	Actions []struct {
		Action       string   `json:"action"`
		IssuedAt     float64  `json:"issuedAt"`
		CompletedAt  *float64 `json:"completedAt"`
		Finished     *bool    `json:"finished"`
		FirstFrameAt *float64 `json:"firstFrameAt"`
		Target       *float64 `json:"target"`
		ItemTime     *float64 `json:"itemTimeAtFirstFrame"`
		Content      *float64 `json:"contentAtFirstFrame"`
		Query        string   `json:"query"`
	} `json:"actions"`
	Samples []struct {
		At      float64 `json:"at"`
		Control string  `json:"control"`
	} `json:"samples"`
	Gaps []struct {
		From float64 `json:"from"`
		To   float64 `json:"to"`
	} `json:"gaps"`
	EndedAt  []float64 `json:"endedAt"`
	Failures []struct {
		Domain string `json:"domain"`
		Code   int    `json:"code"`
		Msg    string `json:"message"`
	} `json:"failures"`
	ErrorLog []struct {
		Code    int     `json:"code"`
		Comment string  `json:"comment"`
		At      float64 `json:"at"`
		URI     string  `json:"uri"`
	} `json:"errorLog"`
	ContentJumps []struct {
		At   float64 `json:"at"`
		From int     `json:"from"`
		To   int     `json:"to"`
	} `json:"contentJumps"`
	ContentOffsets []struct {
		Item   int     `json:"item"`
		N      int     `json:"n"`
		Median float64 `json:"median"`
		Min    float64 `json:"min"`
		Max    float64 `json:"max"`
	} `json:"contentOffsets"`
	FinalStatus string `json:"finalStatus"`
	Frames      int    `json:"frames"`
}

type vodLine struct {
	Case      string          `json:"case"`
	Asset     string          `json:"asset"`
	Path      string          `json:"path"`
	Scenario  string          `json:"scenario"`
	Duration  float64         `json:"durationSeconds"`
	Script    string          `json:"script"`
	Decision  []string        `json:"decisions"`
	Player    json.RawMessage `json:"player,omitempty"`
	Error     string          `json:"error,omitempty"`
	Kept      string          `json:"kept,omitempty"`
	Requests  []string        `json:"requests"`
	Playlists []struct {
		At      float64 `json:"at"`
		Seconds float64 `json:"seconds"`
		TD      float64 `json:"targetDuration"`
		Start   bool    `json:"start"`
		Status  int     `json:"status"`
		Body    string  `json:"body"`
	} `json:"playlists"`
}

type vodResult struct {
	Case     string   `json:"case"`
	Asset    string   `json:"asset"`
	Path     string   `json:"path"`
	Scenario string   `json:"scenario"`
	Verdict  string   `json:"verdict"` // PASS | FAIL | INVALID
	FFmpeg   string   `json:"ffmpeg"`  // the same sequence decoded with ffmpeg -xerror: PASS | FAIL | SKIP | n/a
	Class    string   `json:"class,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	TTFF     float64  `json:"ttffSeconds"`
	// The slowest first media playlist after a start (cold start, restart),
	// against its target duration, and the slowest start.m3u8.
	FirstPlaylistS  float64 `json:"firstPlaylistSeconds"`
	FirstPlaylistTD float64 `json:"firstPlaylistTargetDuration"`
	StartS          float64 `json:"startSeconds"`
	// Advertised BANDWIDTH of each start's variant against the highest
	// measured segment bit rate it served (bytes over EXTINF), kbit/s.
	Bandwidth []string      `json:"bandwidth,omitempty"`
	Seeks     []vodSeek     `json:"seeks,omitempty"`
	Sync      *avSyncReport `json:"avSync,omitempty"`
	Decisions []string      `json:"decisions"`
	Line      vodLine       `json:"raw"`
}

type vodSeek struct {
	Action  string  `json:"action"`
	Target  float64 `json:"target"`
	Resume  float64 `json:"resumeSeconds"`
	Landing float64 `json:"landingSeconds"`
	By      string  `json:"measuredBy"` // content | playhead
}

func runVODAVPlayer(o vodOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("-vod-avplayer needs macOS (AVFoundation)")
	}
	if o.Fixtures == "" {
		return fmt.Errorf("-vod-avplayer needs -fixture-dir with a staged corpus (run once with -corpus -fixture-dir DIR)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	if err := stageVODSyncFixture(ctx, o.FF, o.Fixtures); err != nil {
		return fmt.Errorf("sync fixture: %w", err)
	}
	probe, err := buildSwift(ctx, o.RepoRoot, o.Cache, filepath.Join("tools", "avprobe", "vod", "main.swift"), "avvod")
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ether-vod-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	jsonl := filepath.Join(work, "avplayer.jsonl")
	keep := o.Keep
	if keep == "" {
		keep = filepath.Join(work, "keep")
	}
	started := time.Now()
	pattern := "^TestTranscodeChaos(CorpusScenarios|AVPlayer)$"
	if o.Transcode || o.PlayerOnly {
		pattern = "^TestTranscodeChaosAVPlayer$"
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-p", "1", "-tags", "transcodechaos", "./internal/frontends/plex", "-run", pattern, "-count=1", "-timeout=170m", "-json")
	cmd.Dir = o.RepoRoot
	cmd.Env = append(os.Environ(), "CHAOS_FFMPEG="+o.FF, "CHAOS_FFPROBE="+o.FP, "CHAOS_CORPUS="+o.Fixtures, "CHAOS_AVVOD="+probe, "CHAOS_AVREPORT="+jsonl, "CHAOS_AVKEEP="+keep, "CHAOS_AVONLY="+o.Only, "GOMAXPROCS=2")
	if o.Transcode {
		cmd.Env = append(cmd.Env, "CHAOS_AVTRANSCODE=1")
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Keep the raw go test events beside the report.
	raw, err := os.Create(strings.TrimSuffix(o.Report, ".json") + ".gotest.jsonl")
	if err != nil {
		return err
	}
	defer raw.Close()
	stdout := io.TeeReader(pipe, raw)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	ffmpeg := map[string]string{}
	ffOut := map[string][]string{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var e struct{ Action, Test, Output string }
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		const prefix = "TestTranscodeChaosCorpusScenarios/"
		if strings.HasPrefix(e.Test, prefix) && strings.Count(e.Test, "/") == 3 {
			key := strings.TrimPrefix(e.Test, prefix)
			switch e.Action {
			case "pass", "fail", "skip":
				ffmpeg[key] = strings.ToUpper(e.Action)
			case "output":
				if len(ffOut[key]) < 6 && strings.Contains(e.Output, ".go:") {
					ffOut[key] = append(ffOut[key], strings.TrimSpace(e.Output))
				}
			}
		}
		if strings.HasPrefix(e.Test, "TestTranscodeChaosAVPlayer/") && strings.Count(e.Test, "/") == 3 && (e.Action == "pass" || e.Action == "fail") {
			fmt.Printf("%-60s %s\n", strings.TrimPrefix(e.Test, "TestTranscodeChaosAVPlayer/"), e.Action)
		}
	}
	waitErr := cmd.Wait()
	f, err := os.Open(jsonl)
	if err != nil {
		return fmt.Errorf("no AVPlayer results (go test: %v)", waitErr)
	}
	defer f.Close()
	var results []vodResult
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 64<<10), 32<<20)
	for lines.Scan() {
		var l vodLine
		if json.Unmarshal(lines.Bytes(), &l) != nil {
			continue
		}
		r := judgeVOD(ctx, o.FF, l)
		r.FFmpeg = "n/a"
		if v, ok := ffmpeg[l.Case]; ok {
			r.FFmpeg = v
		}
		if r.Verdict == "FAIL" && r.FFmpeg == "PASS" {
			r.Class = "avplayer-only"
		}
		if l.Scenario == "damaged-copy-play" {
			r.Class = strings.TrimSpace("damaged-copy " + r.Class)
		}
		if r.Verdict == "FAIL" && r.FFmpeg == "FAIL" {
			r.Class = "both"
			r.Warnings = append(r.Warnings, "ffmpeg: "+strings.Join(ffOut[l.Case], " | "))
		}
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Case < results[j].Case })
	rep := struct {
		Label   string      `json:"label"`
		Commit  string      `json:"commit"`
		Started time.Time   `json:"startedAt"`
		Seconds float64     `json:"seconds"`
		FFmpeg  string      `json:"ffmpeg"`
		Results []vodResult `json:"results"`
	}{o.Label, gitCommit(o.RepoRoot), started.UTC(), time.Since(started).Seconds(), "", results}
	version, _ := mediaprocOutput(ctx, o.FF, "-version")
	rep.FFmpeg = firstLine(version)
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(o.Report, append(b, '\n'), 0o644); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
		if r.Class == "avplayer-only" {
			counts["avplayer-only"]++
		}
	}
	fmt.Printf("vod avplayer: %d passed, %d failed (%d where ffmpeg -xerror passes), %d invalid of %d (%.0fs)\n", counts["PASS"], counts["FAIL"], counts["avplayer-only"], counts["INVALID"], len(results), rep.Seconds)
	if o.Markdown != "" {
		if err := os.WriteFile(o.Markdown, []byte(vodMarkdown(rep.Label, rep.Commit, rep.FFmpeg, results)), 0o644); err != nil {
			return err
		}
	}
	if waitErr != nil && len(results) == 0 {
		return fmt.Errorf("go test: %w", waitErr)
	}
	return nil
}

func judgeVOD(ctx context.Context, ff string, l vodLine) vodResult {
	r := vodResult{Case: l.Case, Asset: l.Asset, Path: l.Path, Scenario: l.Scenario, Verdict: "FAIL", Decisions: l.Decision, Line: l}
	fail := func(f string, a ...any) { r.Reasons = append(r.Reasons, fmt.Sprintf(f, a...)) }
	if l.Error != "" || len(l.Player) == 0 {
		fail("%s", l.Error)
		return r
	}
	var p vodPlayer
	if err := json.Unmarshal(l.Player, &p); err != nil {
		fail("player output: %v", err)
		return r
	}
	// A probe that was not scheduled cannot be judged.
	for i := 1; i < len(p.Samples); i++ {
		if d := p.Samples[i].At - p.Samples[i-1].At; d > 3 {
			r.Verdict = "INVALID"
			r.Reasons = []string{fmt.Sprintf("avvod itself was not scheduled for %.0f s (host stall); rerun", d)}
			return r
		}
	}
	sync := l.Asset == vodSyncName
	r.Bandwidth = bandwidthCheck(l)
	for _, b := range r.Bandwidth {
		if strings.HasPrefix(b, "UNDER: ") {
			r.Warnings = append(r.Warnings, "BANDWIDTH below a served segment's rate: "+strings.TrimPrefix(b, "UNDER: "))
		}
	}
	// First-playlist rule, as for live: AVPlayer leaves an item stuck for good
	// when its first media playlist takes a whole target duration.
	expectFirst := false
	for _, pl := range l.Playlists {
		if pl.Start {
			expectFirst = true
			r.StartS = math.Max(r.StartS, pl.Seconds)
			continue
		}
		if !expectFirst {
			continue
		}
		expectFirst = false
		if pl.TD > 0 && pl.Seconds/pl.TD > r.FirstPlaylistS/math.Max(r.FirstPlaylistTD, 1e-9) {
			r.FirstPlaylistS, r.FirstPlaylistTD = pl.Seconds, pl.TD
		}
		if pl.TD > 0 && pl.Seconds >= pl.TD {
			fail("first media playlist took %.2fs >= TD %.0fs (AVFoundation never starts the item)", pl.Seconds, pl.TD)
		} else if pl.TD > 0 && pl.Seconds >= pl.TD/2 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("first media playlist took %.2fs, over half TD %.0fs", pl.Seconds, pl.TD))
		}
	}
	for _, e := range p.Failures {
		fail("item failed %d %s", e.Code, e.Msg)
	}
	seen := map[int]bool{}
	for _, e := range p.ErrorLog {
		if strings.HasPrefix(e.Comment, "Warning:") || seen[e.Code] {
			continue
		}
		seen[e.Code] = true
		fail("errorlog %d @%.0fs %s", e.Code, e.At, e.Comment)
	}
	// windows in which a pause or a seek/restart legitimately stops frames
	type span struct{ from, to float64 }
	var windows []span
	paused := -1.0
	lastSeek := -1
	for i, a := range p.Actions {
		switch a.Action {
		case "seek":
			lastSeek = i
		case "pause":
			paused = a.IssuedAt
		case "play":
			if paused >= 0 {
				windows = append(windows, span{paused, a.IssuedAt + 0.5})
			}
			paused = -1
		}
	}
	if paused >= 0 {
		windows = append(windows, span{paused, math.Inf(1)})
	}
	// Nothing plays after the end of the item.
	for _, e := range p.EndedAt {
		windows = append(windows, span{e - 0.5, math.Inf(1)})
	}
	for i, a := range p.Actions {
		end := a.IssuedAt + vodResumeMaxS
		if a.FirstFrameAt != nil {
			end = *a.FirstFrameAt + 0.5
		}
		switch a.Action {
		case "start":
			if a.FirstFrameAt == nil {
				fail("no first frame")
				return r
			}
			r.TTFF = *a.FirstFrameAt
			if r.TTFF > vodTTFFMaxS {
				fail("ttff %.1fs > %.0fs", r.TTFF, vodTTFFMaxS)
			}
			windows = append(windows, span{0, end})
		case "seek", "replace":
			windows = append(windows, span{a.IssuedAt, end})
			superseded := i < len(p.Actions)-1 && p.Actions[i+1].Action == a.Action && p.Actions[i+1].IssuedAt-a.IssuedAt < 3 && i != lastSeek
			if a.FirstFrameAt == nil {
				if superseded {
					continue
				}
				fail("%s to %.1fs never showed a frame", a.Action, deref(a.Target))
				continue
			}
			s := vodSeek{Action: a.Action, Target: deref(a.Target), Resume: *a.FirstFrameAt - a.IssuedAt}
			limit := vodItemLandingMaxS
			switch {
			case a.Content != nil:
				s.Landing, s.By = *a.Content-s.Target, "content"
				limit = vodLandingMaxS
			case a.ItemTime != nil && a.Action == "seek":
				// A Plex restart's playlist starts at the offset, so only an
				// in-item seek can be judged on the playhead.
				s.Landing, s.By = *a.ItemTime-s.Target, "playhead"
			}
			if a.Action == "replace" {
				limit = vodRestartMaxS
			}
			r.Seeks = append(r.Seeks, s)
			if a.Action == "seek" && a.Finished != nil && !*a.Finished && !superseded {
				fail("seek to %.1fs did not finish", s.Target)
			}
			if s.Resume > vodResumeMaxS {
				fail("%s to %.1fs resumed after %.1fs > %.0fs", a.Action, s.Target, s.Resume, vodResumeMaxS)
			} else if s.Resume > vodResumeWarnS {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s to %.1fs resumed after %.1fs", a.Action, s.Target, s.Resume))
			}
			if s.By != "" && math.Abs(s.Landing) > limit && !superseded {
				fail("%s to %.2fs landed %+.2fs away (%s)", a.Action, s.Target, s.Landing, s.By)
			}
		}
	}
	in := func(t float64) bool {
		for _, w := range windows {
			if t >= w.from && t <= w.to {
				return true
			}
		}
		return false
	}
	stalled := 0.0
	for _, g := range p.Gaps {
		if !in(g.From) && !in(g.To) {
			stalled += g.To - g.From
		}
	}
	if stalled > 1 {
		fail("playback stalled %.1fs outside seeks and pauses", stalled)
	}
	for _, j := range p.ContentJumps {
		if in(j.At) {
			continue
		}
		if j.To > j.From && j.To-j.From <= 3 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("dropped %d frame(s) at %.1fs", j.To-j.From-1, j.At))
			continue
		}
		fail("picture jumped from %.2fs to %.2fs of content at %.1fs while playing", float64(j.From)/liveFPS, float64(j.To)/liveFPS, j.At)
	}
	for _, o := range p.ContentOffsets {
		if o.N > 30 && o.Max-o.Min > vodOffsetSpreadMax {
			fail("item %d: picture vs timestamp drifted %.2fs (offset %+.2f..%+.2f)", o.Item, o.Max-o.Min, o.Min, o.Max)
		}
	}
	if l.Scenario == "pause-resume-eos" && len(p.EndedAt) == 0 {
		fail("never reached the end after seeking near it")
	}
	if sync && l.Kept != "" {
		r.Sync = keptSync(ctx, ff, l.Kept)
		if why := syncVerdict(r.Sync); why != "" {
			fail("%s", why)
		}
	}
	if sync && len(p.ContentOffsets) == 0 && p.Frames > 0 {
		fail("sync fixture frames shown but no frame counter decoded")
	}
	if len(r.Reasons) == 0 {
		r.Verdict = "PASS"
	}
	return r
}

var (
	reBandwidth = regexp.MustCompile(`BANDWIDTH=(\d+)`)
	reExtinf    = regexp.MustCompile(`#EXTINF:([0-9.]+),?[^\n]*\n([^#\s][^\n]*)`)
	reReqSize   = regexp.MustCompile(`GET (\S+) -> 200 (\d+)B`)
)

// bandwidthCheck pairs each start's advertised BANDWIDTH with the peak
// segment rate of the media it then served.
func bandwidthCheck(l vodLine) []string {
	sizes := map[string]int{}
	for _, q := range l.Requests {
		if m := reReqSize.FindStringSubmatch(q); m != nil {
			n, _ := strconv.Atoi(m[2])
			base := filepath.Base(m[1])
			sizes[base] = max(sizes[base], n)
		}
	}
	var out []string
	adv := 0
	for _, pl := range l.Playlists {
		if pl.Start {
			adv = 0
			if m := reBandwidth.FindStringSubmatch(pl.Body); m != nil {
				adv, _ = strconv.Atoi(m[1])
			}
			continue
		}
		if adv == 0 {
			continue
		}
		peak, at := 0.0, ""
		for _, m := range reExtinf.FindAllStringSubmatch(pl.Body, -1) {
			d, _ := strconv.ParseFloat(m[1], 64)
			if n := sizes[filepath.Base(strings.TrimSpace(m[2]))]; n > 0 && d > 0 {
				if k := float64(n) * 8 / d / 1000; k > peak {
					peak, at = k, filepath.Base(strings.TrimSpace(m[2]))
				}
			}
		}
		if peak > 0 {
			mark := ""
			if peak > float64(adv)/1000 {
				mark = "UNDER: "
			}
			out = append(out, fmt.Sprintf("%sadvertised %d, peak %.0f kbit/s (%s)", mark, adv/1000, peak, at))
		}
		adv = 0
	}
	return out
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// keptSync measures lip sync on the media segments the player fetched, in
// fetch order; fMP4 fragments get the latest init segment prepended.
func keptSync(ctx context.Context, ff, dir string) *avSyncReport {
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	sort.Strings(files)
	var init []byte
	var segs []*judgedSegment
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.HasSuffix(f, "init.mp4") {
			init = b
			continue
		}
		path := f
		if strings.HasSuffix(f, ".m4s") && init != nil {
			path = f + ".mp4"
			if os.WriteFile(path, append(bytes.Clone(init), b...), 0o600) != nil {
				continue
			}
		}
		v, a := analyseSync(ctx, ff, path)
		segs = append(segs, &judgedSegment{Seq: int64(i), syncVideo: v, syncAudio: a})
	}
	return avSync(segs)
}

// buildSwift compiles a Swift tool on demand into the cache, keyed by its
// source hash.
func buildSwift(ctx context.Context, root, cache, rel, name string) (string, error) {
	src := filepath.Join(root, rel)
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	out := filepath.Join(cache, name+"-"+hex.EncodeToString(sum[:6]))
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	if o, err := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", out).CombinedOutput(); err != nil {
		return "", fmt.Errorf("swiftc %s: %v: %s", rel, err, o)
	}
	return out, nil
}

func vodMarkdown(label, commit, ff string, results []vodResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# VOD in AVPlayer: %s\n\n- Commit `%s`; Ether's ffmpeg: %s\n", label, commit, ff)
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
		if r.Class == "avplayer-only" {
			counts["only"]++
		}
	}
	fmt.Fprintf(&b, "- **%d of %d cases pass**; %d fail, %d of them where ffmpeg -xerror passes the same sequence; %d invalid (host stall).\n\n", counts["PASS"], len(results), counts["FAIL"], counts["only"], counts["INVALID"])
	b.WriteString("| Case | AVPlayer | ffmpeg -xerror | TTFF s | First playlist s / TD | Seeks: resume s / landing s | Decision | Reasons |\n|---|---|---|---:|---|---|---|---|\n")
	for _, r := range results {
		var seeks []string
		for _, s := range r.Seeks {
			seeks = append(seeks, fmt.Sprintf("%s %.1f: %.2f / %+.2f%s", s.Action, s.Target, s.Resume, s.Landing, map[string]string{"content": "", "playhead": "p", "": "?"}[s.By]))
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %.1f | %.2f / %.0f | %s | %s | %s |\n", r.Case, r.Verdict, r.FFmpeg, r.TTFF, r.FirstPlaylistS, r.FirstPlaylistTD, mdEscape(strings.Join(seeks, "<br>")), mdEscape(strings.Join(r.Decisions, "<br>")), mdEscape(strings.Join(r.Reasons, "; ")))
	}
	b.WriteString("\n## How it is judged\n\n")
	b.WriteString("- AVPlayer (tools/avprobe/vod) plays Ether's start.m3u8 after a /decision, as a Plex client does, and runs the scripted client sequence: exact seeks inside the item, pause and play, seeking near the end, and Plex-style restarts at an offset for track and quality changes.\n")
	fmt.Fprintf(&b, "- Fails: any item failure or CoreMedia error-log entry (warnings excluded); first frame after %.0f s; a seek or restart resuming after %.0f s (warning after %.0f s); a seek landing over %.2f s from its target on the picture actually shown (sync fixture: frame-counter barcode), or %.1f s on the playhead (other media); a restart landing over %.1f s from its offset; over 1 s of stalls outside seeks and pauses; the picture jumping while playing (more than 3 frames, or backwards); the picture drifting against its timestamps by over %.1f s within an item; not reaching the end after seeking near it; and, on the sync fixture, lip sync outside ITU-R BT.1359 (audio 45 ms early / 125 ms late) in the segments the player fetched.\n", vodTTFFMaxS, vodResumeMaxS, vodResumeWarnS, vodLandingMaxS, vodItemLandingMaxS, vodRestartMaxS, vodOffsetSpreadMax)
	b.WriteString("- First playlist: the slowest first media playlist after each start or restart, against its TARGETDURATION; 1x TD fails (AVPlayer then never starts the item), 0.5x warns. damaged-copy rows play a damaged library title (the generated input-* fixtures) through the copy path and record what AVPlayer shows.\n")
	b.WriteString("- ffmpeg -xerror is TestTranscodeChaosCorpusScenarios on the same asset, path and sequence (as Plex Web; the AVPlayer cases tune as Plex for Apple TV); `avplayer-only` marks cases where it passes and AVPlayer fails.\n")
	b.WriteString("- Audio inside the player is not measured; lip sync is measured on the published segments.\n")
	return b.String()
}
