package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type liveOptions struct {
	FF, FP       string // ffmpeg/ffprobe Ether uses (pinned build)
	FixtureFF    string // encoder for fixtures (any recent ffmpeg)
	Subset, Only string
	Preset       string
	Player       string // auto | on | off
	Parallel     int
	CacheDir     string
	Report       string
	Markdown     string
	Baseline     string
	Keep         string // keep work directories here
	Label        string
	Observe      time.Duration
	Client       string
	RepoRoot     string
}

type viewerSummary struct {
	FreezeS      float64  `json:"freezeSeconds"`
	SlateS       float64  `json:"slateSeconds"`
	ScreenS      float64  `json:"screenSeconds"`
	SilenceS     float64  `json:"silenceSeconds"`
	ClientErrors []string `json:"clientErrors"`
	Warnings     []string `json:"playerWarnings,omitempty"`
	Recovered    bool     `json:"recovered"`
	// TTFF is tune to the first provider picture; FirstFrame may be a card.
	TTFFS         float64 `json:"ttffSeconds"`
	FirstFrameS   float64 `json:"firstFrameSeconds"`
	SlateSource   string  `json:"slateSource"`
	FreezeSource  string  `json:"freezeSource"`
	SilenceSource string  `json:"silenceSource"`
}

type liveResult struct {
	ID          string          `json:"id"`
	Scenario    string          `json:"scenario"`
	Summary     string          `json:"summary"`
	Mode        string          `json:"mode"`
	Path        string          `json:"etherPath"`
	Preset      string          `json:"preset"`
	Applied     string          `json:"presetApplied"`
	Verdict     string          `json:"verdict"`
	Reasons     []string        `json:"reasons,omitempty"`
	Viewer      viewerSummary   `json:"viewer"`
	TuneS       float64         `json:"tuneSeconds"`
	Protocol    protocolReport  `json:"protocol"`
	Player      json.RawMessage `json:"player,omitempty"`
	Provider    providerStats   `json:"provider"`
	Backup      *providerStats  `json:"backupProvider,omitempty"`
	Error       string          `json:"error,omitempty"`
	WallSeconds float64         `json:"wallSeconds"`
	// HostLoad is the 1-minute load average at the row's start and end;
	// timing rows measured on a saturated host are not comparable.
	HostLoad [2]float64 `json:"hostLoad"`
}

type liveReport struct {
	Label     string       `json:"label"`
	Commit    string       `json:"commit"`
	Subset    string       `json:"subset"`
	Preset    string       `json:"preset"`
	Client    string       `json:"client"`
	Player    string       `json:"player"`
	FFmpeg    string       `json:"ffmpeg"`
	Host      string       `json:"host"`
	StartedAt time.Time    `json:"startedAt"`
	Seconds   float64      `json:"seconds"`
	Results   []liveResult `json:"results"`
}

func runLive(o liveOptions) error {
	if o.Observe == 0 {
		o.Observe = liveObserve
	}
	if o.Preset == "" {
		o.Preset = "balanced"
	}
	if o.Client == "" {
		o.Client = "plex-appletv"
	}
	if _, ok := liveClients[o.Client]; !ok {
		return fmt.Errorf("unknown -live-client %q", o.Client)
	}
	if o.Client != "plex-appletv" && o.Player == "auto" {
		o.Player = "off" // AVPlayer only stands in for Apple clients
	}
	rows := selectRows(o.Subset, o.Only)
	if len(rows) == 0 {
		return fmt.Errorf("no live rows selected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	started := time.Now()
	root, err := os.MkdirTemp("", "ether-live-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	fixtures := map[string]*liveFixture{}
	for name, v := range liveVariants {
		f, err := loadLiveFixture(ctx, o.FixtureFF, o.CacheDir, v)
		if err != nil {
			return fmt.Errorf("fixture %s: %w", name, err)
		}
		fixtures[name] = f
	}
	player := ""
	if o.Player == "on" || (o.Player == "auto" && runtime.GOOS == "darwin") {
		if player, err = buildAVProbe(ctx, o.RepoRoot, o.CacheDir); err != nil {
			if o.Player == "on" {
				return err
			}
			fmt.Fprintln(os.Stderr, "avprobe unavailable, protocol judge only:", err)
		}
	}
	version, _ := mediaprocOutput(ctx, o.FF, "-version")
	rep := liveReport{Label: o.Label, Commit: gitCommit(o.RepoRoot), Subset: o.Subset, Preset: o.Preset, Client: o.Client, Player: map[bool]string{true: "avfoundation", false: "protocol-only"}[player != ""], FFmpeg: firstLine(version), Host: runtime.GOOS + "/" + runtime.GOARCH, StartedAt: started.UTC()}
	fmt.Printf("live: %d rows, preset %s, player %s, parallel %d\n", len(rows), o.Preset, rep.Player, o.Parallel)
	results := make([]liveResult, len(rows))
	sem := make(chan struct{}, max(1, o.Parallel))
	var wg sync.WaitGroup
	var printMu sync.Mutex
	if player != "" {
		// The first AVPlayer on a host pays a one-time media stack start-up;
		// keep it out of every row's TTFF.
		warmAVFoundation(ctx, player, fixtures)
	}
	var startMu sync.Mutex
	lastStart := time.Time{}
	for i, row := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Stagger starts so parallel players do not initialise together.
			startMu.Lock()
			if wait := time.Until(lastStart.Add(1500 * time.Millisecond)); wait > 0 {
				time.Sleep(wait)
			}
			lastStart = time.Now()
			startMu.Unlock()
			work := filepath.Join(root, strings.ReplaceAll(row.ID(), "/", "_"))
			_ = os.MkdirAll(work, 0o700)
			res := runLiveRow(ctx, o, row, fixtures, player, work)
			if o.Keep != "" {
				_ = os.MkdirAll(o.Keep, 0o700)
				_ = os.Rename(work, filepath.Join(o.Keep, filepath.Base(work)))
			}
			results[i] = res
			printMu.Lock()
			printLiveResult(res)
			printMu.Unlock()
		}()
	}
	wg.Wait()
	rep.Results = results
	rep.Seconds = time.Since(started).Seconds()
	var baseline *liveReport
	if o.Baseline != "" {
		baseline = &liveReport{}
		if b, err := os.ReadFile(o.Baseline); err != nil || json.Unmarshal(b, baseline) != nil {
			return fmt.Errorf("baseline %s unreadable", o.Baseline)
		}
	}
	passed, failed, invalid := 0, 0, 0
	for _, r := range results {
		switch r.Verdict {
		case "PASS":
			passed++
		case "INVALID":
			invalid++
		default:
			failed++
		}
	}
	fmt.Printf("live score: %d passed, %d failed, %d invalid (host stall, rerun) of %d (%.0fs)\n", passed, failed, invalid, len(results), rep.Seconds)
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(o.Report, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if o.Markdown != "" {
		if err := os.WriteFile(o.Markdown, []byte(liveMarkdown(rep, baseline, o)), 0o644); err != nil {
			return err
		}
	}
	if failed+invalid > 0 {
		return fmt.Errorf("%d live rows failed, %d invalid; report: %s", failed, invalid, o.Report)
	}
	return nil
}

// renderLive rewrites the Markdown summary of an existing JSON report.
func renderLive(report, markdown, baselinePath string) error {
	var rep liveReport
	b, err := os.ReadFile(report)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return err
	}
	var base *liveReport
	if baselinePath != "" {
		base = &liveReport{}
		if b, err := os.ReadFile(baselinePath); err != nil || json.Unmarshal(b, base) != nil {
			return fmt.Errorf("baseline %s unreadable", baselinePath)
		}
	}
	return os.WriteFile(markdown, []byte(liveMarkdown(rep, base, liveOptions{})), 0o644)
}

func hostLoad() float64 {
	var out []byte
	if runtime.GOOS == "linux" {
		out, _ = os.ReadFile("/proc/loadavg")
	} else {
		out, _ = exec.Command("sysctl", "-n", "vm.loadavg").Output()
	}
	for _, f := range strings.Fields(strings.Trim(string(out), "{} \n")) {
		var v float64
		if _, err := fmt.Sscan(f, &v); err == nil {
			return v
		}
	}
	return 0
}

func gitCommit(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short=9", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func printLiveResult(r liveResult) {
	v := r.Viewer
	detail := strings.Join(r.Reasons, "; ")
	if r.Error != "" {
		detail = r.Error
	}
	fmt.Printf("%-40s %-15s %-4s first=%4.1fs ttff=%4.1fs screen=%5.1fs (freeze %4.1f slate %4.1f) silence=%5.1fs gap=%4.1fs errors=%d recovered=%v %s\n",
		r.ID, r.Path, r.Verdict, v.FirstFrameS, v.TTFFS, v.ScreenS, v.FreezeS, v.SlateS, v.SilenceS, r.Protocol.LongestNoNewSegmentAdjS, len(v.ClientErrors), v.Recovered, detail)
}

func runLiveRow(parent context.Context, o liveOptions, row liveRow, fixtures map[string]*liveFixture, player, work string) (res liveResult) {
	sc := row.Scenario
	res = liveResult{ID: row.ID(), Scenario: sc.Name, Summary: sc.Summary, Mode: row.Mode, Preset: o.Preset, Verdict: "FAIL"}
	wall := time.Now()
	res.HostLoad[0] = hostLoad()
	defer func() { res.WallSeconds = time.Since(wall).Seconds(); res.HostLoad[1] = hostLoad() }()
	ctx, cancel := context.WithTimeout(parent, o.Observe+90*time.Second)
	defer cancel()
	start := time.Now()
	primary, err := newLiveProvider(sc.Source, fixtures, start)
	if err != nil {
		res.Error = err.Error()
		return
	}
	defer primary.Close()
	setup := channelSetup{Mode: row.Mode, Primary: primary.url, Stability: o.Preset}
	var backup *liveProvider
	if sc.Backup != nil {
		if backup, err = newLiveProvider(*sc.Backup, fixtures, start); err != nil {
			res.Error = err.Error()
			return
		}
		defer backup.Close()
		setup.Backups = []string{backup.url}
	}
	ether, ch, err := startLiveEther(ctx, o.FF, o.FP, work, setup)
	if err != nil {
		res.Error = "ether: " + err.Error()
		return
	}
	defer ether.Close()
	res.Applied = ether.Stability
	tuneStart := time.Now()
	playlist, _, err := ether.tunePlex(ctx, o.Client, ch.ID)
	res.TuneS = time.Since(tuneStart).Seconds()
	res.Path = ether.selectedPath()
	if res.Path == "" && row.Mode == "steady" {
		res.Path = "broadcast"
	}
	if err != nil {
		res.Error = "tune: " + err.Error()
		res.Viewer.ClientErrors = append(res.Viewer.ClientErrors, "tune failed")
		res.Provider = primary.Stats()
		return
	}
	segDir := filepath.Join(work, "segments")
	_ = os.MkdirAll(segDir, 0o700)
	judge := newProtocolJudge(ctx, o.FF, o.FP, playlist, segDir, tuneStart, ether.handledSeconds)
	judge.sync = sc.Sync
	observeCtx, stop := context.WithTimeout(ctx, o.Observe)
	defer stop()
	var playerOut []byte
	var playerErr error
	var pwg sync.WaitGroup
	if player != "" {
		playerURL := playlist
		if tap, u, err := newPlayerTap(playlist, tuneStart); err == nil {
			playerURL = u
			defer tap.Close(filepath.Join(work, "player-http.log"))
		}
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			cmd := exec.CommandContext(ctx, player, playerURL, fmt.Sprint(int(o.Observe/time.Second)))
			playerOut, playerErr = cmd.Output()
		}()
	}
	judge.Run(observeCtx)
	pwg.Wait()
	res.Protocol = judge.Finish(time.Since(tuneStart).Seconds())
	res.Provider = primary.Stats()
	if backup != nil {
		s := backup.Stats()
		res.Backup = &s
	}
	if res.Path == "" {
		res.Path = ether.selectedPath()
	}
	var av *avResult
	if player != "" {
		if playerErr != nil {
			res.Error = "avprobe: " + playerErr.Error()
		} else {
			av = &avResult{}
			if err := json.Unmarshal(playerOut, av); err != nil {
				res.Error = "avprobe output: " + err.Error()
				av = nil
			} else {
				res.Player = json.RawMessage(compactJSON(playerOut))
			}
		}
	}
	res.Viewer = summarise(res.Protocol, av, res.TuneS)
	res.Verdict, res.Reasons = judgeRow(sc, o.Preset, liveStuckFactor[o.Client], res)
	if res.Error != "" && res.Verdict == "PASS" {
		res.Verdict = "FAIL"
		res.Reasons = append(res.Reasons, res.Error)
	}
	if av != nil {
		if n := probeStall(av); n >= 3 {
			res.Verdict = "INVALID"
			res.Reasons = append([]string{fmt.Sprintf("avprobe itself was not scheduled for %d s (host stall); the player verdict is void, rerun this row", n)}, res.Reasons...)
		}
	}
	return
}

func compactJSON(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

type avResult struct {
	FirstFrameS   *float64 `json:"firstFrameSeconds"`
	FirstContentS *float64 `json:"firstContentSeconds"`
	ContentFrames []int    `json:"contentFramesPerSecond"`
	OtherFrames   []int    `json:"otherFramesPerSecond"`
	RepeatFrames  []int    `json:"repeatFramesPerSecond"`
	Gaps          []struct {
		From float64 `json:"from"`
		To   float64 `json:"to"`
	} `json:"gaps"`
	LastFrameS   *float64 `json:"lastFrameSeconds"`
	FrozenS      *float64 `json:"frozenSeconds"`
	DurationS    float64  `json:"durationSeconds"`
	FinalStatus  string   `json:"finalStatus"`
	FramesPerSec []int    `json:"framesPerSecond"`
	Failures     []struct {
		Domain string `json:"domain"`
		Code   int    `json:"code"`
		Msg    string `json:"message"`
	} `json:"failures"`
	ItemError *struct {
		Domain string `json:"domain"`
		Code   int    `json:"code"`
		Msg    string `json:"message"`
	} `json:"itemError"`
	ErrorLog []struct {
		Code    int     `json:"code"`
		Domain  string  `json:"domain"`
		Comment string  `json:"comment"`
		At      float64 `json:"at"`
	} `json:"errorLog"`
	Stalls []float64 `json:"stallNotifications"`
	// Control is the player's state sampled once per second; null where the
	// probe's own timer did not run that second.
	Control []*string `json:"controlPerSecond"`
}

// probeStall is the longest run of seconds in which avprobe's own timer did
// not run (the process was not scheduled), after its first second. A player
// that was not running cannot be judged: frames and requests stop with it.
func probeStall(av *avResult) int {
	longest, run := 0, 0
	for i, c := range av.Control {
		if i > 0 && c == nil {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

func summarise(p protocolReport, av *avResult, tune float64) viewerSummary {
	v := viewerSummary{SilenceSource: "ffmpeg silencedetect on the published segments after the first provider picture (in-player audio taps do not attach to HLS items)"}
	// Everything before the first provider picture is startup, judged by
	// TTFF; screens, freezes and silence count from that picture on.
	started := false
	for _, s := range p.SegmentLog {
		if !started && s.ContentS == 0 {
			continue
		}
		started = true
		v.SilenceS += s.SilenceS
	}
	if av != nil {
		v.FreezeSource = "AVPlayer frame delivery: gaps >= 0.5 s and held (identical) frames"
		v.SlateSource = "AVPlayer frames without the fixture's green band"
		if av.FirstFrameS != nil {
			v.FirstFrameS = tune + *av.FirstFrameS
		}
		if av.FirstContentS == nil {
			v.ClientErrors = append(v.ClientErrors, "player never showed the channel")
		} else {
			first := *av.FirstContentS
			v.TTFFS = tune + first
			for _, g := range av.Gaps {
				if g.To > first {
					v.FreezeS += g.To - math.Max(g.From, first)
				}
			}
			for i := range av.FramesPerSec {
				n := av.FramesPerSec[i]
				if float64(i) < math.Ceil(first) || n == 0 {
					continue
				}
				if i < len(av.OtherFrames) {
					v.SlateS += float64(av.OtherFrames[i]) / float64(n)
				}
				if i < len(av.RepeatFrames) && i < len(av.ContentFrames) && float64(av.RepeatFrames[i]) > 0.8*float64(n) && av.ContentFrames[i] > n/2 {
					v.FreezeS += float64(av.RepeatFrames[i]) / float64(n)
				}
			}
		}
		for _, f := range av.Failures {
			v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("failed %s %d", f.Domain, f.Code))
		}
		if av.ItemError != nil {
			v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("item %s %d", av.ItemError.Domain, av.ItemError.Code))
		}
		seen := map[int]bool{}
		for _, e := range av.ErrorLog {
			warning := strings.HasPrefix(e.Comment, "Warning:")
			if !seen[e.Code] {
				if warning {
					v.Warnings = append(v.Warnings, fmt.Sprintf("%d @%.0fs %s", e.Code, e.At, e.Comment))
				} else {
					v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("errorlog %d @%.0fs", e.Code, e.At))
				}
			}
			seen[e.Code] = true
		}
		n := len(av.ContentFrames)
		recent := 0
		for i := max(0, n-5); i < n; i++ {
			recent += av.ContentFrames[i]
		}
		v.Recovered = recent > 0 && av.FinalStatus != "failed"
	} else {
		v.FreezeSource = "player buffer model over observed segment arrivals + held content frames in the published media"
		v.SlateSource = "published segments sampled at 10 Hz without the fixture's green band"
		v.FreezeS = p.ModelStallS
		started = false
		for _, s := range p.SegmentLog {
			if !started && s.ContentS == 0 {
				continue
			}
			if !started {
				started = true
				v.TTFFS = s.FetchedAt
			}
			v.SlateS += s.NonContentS
			v.FreezeS += s.FrozenS
		}
		v.FirstFrameS = p.FirstSegmentS
		if p.ModelFatal != "" {
			v.ClientErrors = append(v.ClientErrors, "model "+p.ModelFatal)
		}
		// Content in the newest segment, published within two target durations of the end.
		last := len(p.SegmentLog) - 1
		v.Recovered = last >= 0 && p.SegmentLog[last].ContentS > 0 && p.ModelFatal == "" && p.RunEndS-p.SegmentLog[last].SeenAt <= 2*math.Max(p.TargetDuration, 1)
	}
	if td, first := p.TargetDuration, p.FirstPlaylistResponseS; td > 0 && first >= td/2 && first < td {
		v.Warnings = append(v.Warnings, fmt.Sprintf("first playlist took %.1fs, over half TD %.0fs (AVFoundation hangs at 1x TD)", first, td))
	}
	if a := p.AVSync; a != nil {
		for _, c := range a.Cuts {
			v.Warnings = append(v.Warnings, "content time cut: "+c)
		}
	}
	for k, n := range p.HTTPErrors {
		v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("http %s x%d", k, n))
	}
	if len(p.UnmarkedCodecChanges) > 0 {
		v.ClientErrors = append(v.ClientErrors, "unmarked codec change between segments (Apple players fail or freeze)")
	}
	sort.Strings(v.ClientErrors)
	v.FreezeS = math.Round(v.FreezeS*10) / 10
	v.SlateS = math.Round(v.SlateS*10) / 10
	v.SilenceS = math.Round(v.SilenceS*10) / 10
	v.ScreenS = v.FreezeS + v.SlateS
	return v
}

// judgeRow applies the common rules plus the scenario's preset budget.
func judgeRow(sc liveScenario, preset string, stuck float64, r liveResult) (string, []string) {
	var why []string
	v, p := r.Viewer, r.Protocol
	b, ok := sc.Budget[preset]
	if !ok {
		b = sc.Budget["balanced"]
	}
	if len(v.ClientErrors) > 0 {
		why = append(why, "client errors: "+strings.Join(v.ClientErrors, ", "))
	}
	if sc.Recoverable && !v.Recovered {
		why = append(why, "did not recover")
	}
	if v.ScreenS > b.ScreenS+0.05 {
		why = append(why, fmt.Sprintf("screen %.1fs > %.1fs", v.ScreenS, b.ScreenS))
	}
	if v.SilenceS > b.SilenceS+0.05 {
		why = append(why, fmt.Sprintf("silence %.1fs > %.1fs", v.SilenceS, b.SilenceS))
	}
	if td := p.TargetDuration; td > 0 && p.LongestNoNewSegmentAdjS > stuck*td {
		why = append(why, fmt.Sprintf("no new segment for %.1fs (> %.1fx TD %.0fs)", p.LongestNoNewSegmentAdjS, stuck, td))
	}
	if td, first := p.TargetDuration, p.FirstPlaylistResponseS; td > 0 && first >= td {
		why = append(why, fmt.Sprintf("first playlist took %.1fs >= TD %.0fs (AVFoundation never starts the item)", first, td))
	}
	if sc.Sync {
		if why2 := syncVerdict(p.AVSync); why2 != "" {
			why = append(why, why2)
		}
	}
	if p.OverlongSegments > 0 {
		why = append(why, fmt.Sprintf("%d segments exceed TD+0.5s (longest EXTINF %.1fs, media %.1fs)", p.OverlongSegments, p.MaxSegmentS, p.MaxMediaS))
	}
	if len(p.SequenceErrors) > 0 {
		why = append(why, "sequence: "+p.SequenceErrors[0])
	}
	if v.TTFFS == 0 {
		why = append(why, "no provider picture")
	} else if v.TTFFS > liveTTFFMaxS {
		why = append(why, fmt.Sprintf("ttff %.1fs > %.0fs", v.TTFFS, liveTTFFMaxS))
	}
	if sc.MaxConcurrent > 0 && r.Provider.MaxConcurrent > sc.MaxConcurrent {
		why = append(why, fmt.Sprintf("%d concurrent provider connections > %d", r.Provider.MaxConcurrent, sc.MaxConcurrent))
	}
	if len(why) == 0 {
		return "PASS", nil
	}
	return "FAIL", why
}

func warmAVFoundation(ctx context.Context, player string, fixtures map[string]*liveFixture) {
	p, err := newLiveProvider(providerConfig{Name: "warm", HLS: true, Backlog: 3}, fixtures, time.Now())
	if err != nil {
		return
	}
	defer p.Close()
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(wctx, player, p.url, "4").Run()
}

func buildAVProbe(ctx context.Context, root, cache string) (string, error) {
	src := filepath.Join(root, "tools", "avprobe", "main.swift")
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	out := filepath.Join(cache, "avprobe-"+hex.EncodeToString(sum[:6]))
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	if o, err := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", out).CombinedOutput(); err != nil {
		return "", fmt.Errorf("swiftc: %v: %s", err, o)
	}
	return out, nil
}
