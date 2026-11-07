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
	RepoRoot     string
}

type viewerSummary struct {
	FreezeS       float64  `json:"freezeSeconds"`
	SlateS        float64  `json:"slateSeconds"`
	ScreenS       float64  `json:"screenSeconds"`
	SilenceS      float64  `json:"silenceSeconds"`
	ClientErrors  []string `json:"clientErrors"`
	Recovered     bool     `json:"recovered"`
	TTFFS         float64  `json:"ttffSeconds"`
	FreezeSource  string   `json:"freezeSource"`
	SilenceSource string   `json:"silenceSource"`
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
}

type liveReport struct {
	Label     string       `json:"label"`
	Commit    string       `json:"commit"`
	Subset    string       `json:"subset"`
	Preset    string       `json:"preset"`
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
	host, _ := os.Hostname()
	rep := liveReport{Label: o.Label, Commit: gitCommit(o.RepoRoot), Subset: o.Subset, Preset: o.Preset, Player: map[bool]string{true: "avfoundation", false: "protocol-only"}[player != ""], FFmpeg: firstLine(version), Host: runtime.GOOS + "/" + runtime.GOARCH + " " + host, StartedAt: started.UTC()}
	fmt.Printf("live: %d rows, preset %s, player %s, parallel %d\n", len(rows), o.Preset, rep.Player, o.Parallel)
	results := make([]liveResult, len(rows))
	sem := make(chan struct{}, max(1, o.Parallel))
	var wg sync.WaitGroup
	var printMu sync.Mutex
	for i, row := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
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
	passed, failed := 0, 0
	for _, r := range results {
		if r.Verdict == "PASS" {
			passed++
		} else {
			failed++
		}
	}
	fmt.Printf("live score: %d passed, %d failed of %d (%.0fs)\n", passed, failed, len(results), rep.Seconds)
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
	if failed > 0 {
		return fmt.Errorf("%d live rows failed; report: %s", failed, o.Report)
	}
	return nil
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
	fmt.Printf("%-40s %-15s %-4s ttff=%4.1fs screen=%5.1fs (freeze %4.1f slate %4.1f) silence=%5.1fs gap=%4.1fs errors=%d recovered=%v %s\n",
		r.ID, r.Path, r.Verdict, v.TTFFS, v.ScreenS, v.FreezeS, v.SlateS, v.SilenceS, r.Protocol.LongestNoNewSegmentS, len(v.ClientErrors), v.Recovered, detail)
}

func runLiveRow(parent context.Context, o liveOptions, row liveRow, fixtures map[string]*liveFixture, player, work string) (res liveResult) {
	sc := row.Scenario
	res = liveResult{ID: row.ID(), Scenario: sc.Name, Summary: sc.Summary, Mode: row.Mode, Preset: o.Preset, Verdict: "FAIL"}
	wall := time.Now()
	defer func() { res.WallSeconds = time.Since(wall).Seconds() }()
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
	playlist, _, err := ether.tunePlex(ctx, "plex-appletv", ch.ID)
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
	judge := newProtocolJudge(o.FF, o.FP, playlist, segDir, tuneStart)
	observeCtx, stop := context.WithTimeout(ctx, o.Observe)
	defer stop()
	var playerOut []byte
	var playerErr error
	var pwg sync.WaitGroup
	if player != "" {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			cmd := exec.CommandContext(ctx, player, playlist, fmt.Sprint(int(o.Observe/time.Second)))
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
	res.Verdict, res.Reasons = judgeRow(sc, o.Preset, res)
	if res.Error != "" && res.Verdict == "PASS" {
		res.Verdict = "FAIL"
		res.Reasons = append(res.Reasons, res.Error)
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
	FirstFrameS  *float64 `json:"firstFrameSeconds"`
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
}

func summarise(p protocolReport, av *avResult, tune float64) viewerSummary {
	v := viewerSummary{SilenceSource: "ffmpeg silencedetect on the published segments (in-player audio taps do not attach to HLS)"}
	// Content measures exclude anything before the first provider picture:
	// a tune-time card is startup, not a fault.
	started := false
	lastSeq := len(p.SegmentLog) - 1
	for i, s := range p.SegmentLog {
		if !started && s.ContentS == 0 {
			continue
		}
		started = true
		v.SlateS += s.NonContentS
		v.SilenceS += s.SilenceS
		_ = i
	}
	contentFreeze := 0.0
	for _, s := range p.SegmentLog {
		contentFreeze += s.FrozenS
	}
	if av != nil {
		v.FreezeSource = "avfoundation frame delivery + held frames in the published media"
		if av.FrozenS != nil {
			v.FreezeS = *av.FrozenS
		} else {
			v.FreezeS = av.DurationS
			v.ClientErrors = append(v.ClientErrors, "player never showed a frame")
		}
		if av.FirstFrameS != nil {
			v.TTFFS = tune + *av.FirstFrameS
		}
		for _, f := range av.Failures {
			v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("failed %s %d", f.Domain, f.Code))
		}
		if av.ItemError != nil {
			v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("item %s %d", av.ItemError.Domain, av.ItemError.Code))
		}
		seen := map[int]bool{}
		for _, e := range av.ErrorLog {
			if !seen[e.Code] {
				seen[e.Code] = true
				v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("errorlog %d @%.0fs", e.Code, e.At))
			}
		}
		n := len(av.FramesPerSec)
		recent := 0
		for i := max(0, n-5); i < n; i++ {
			recent += av.FramesPerSec[i]
		}
		v.Recovered = recent > 0 && av.FinalStatus != "failed"
	} else {
		v.FreezeSource = "player buffer model over observed segment arrivals + held frames in the published media"
		v.FreezeS = p.ModelStallS
		v.TTFFS = p.FirstSegmentS
		if p.ModelFatal != "" {
			v.ClientErrors = append(v.ClientErrors, "model "+p.ModelFatal)
		}
		// Content in the newest segment, published within two target durations of the end.
		v.Recovered = lastSeq >= 0 && p.SegmentLog[lastSeq].ContentS > 0 && p.ModelFatal == "" && p.RunEndS-p.SegmentLog[lastSeq].SeenAt <= 2*math.Max(p.TargetDuration, 1)
	}
	v.FreezeS += contentFreeze
	for k, n := range p.HTTPErrors {
		v.ClientErrors = append(v.ClientErrors, fmt.Sprintf("http %s x%d", k, n))
	}
	if len(p.UnmarkedCodecChanges) > 0 {
		v.ClientErrors = append(v.ClientErrors, "unmarked codec change (Apple -12971)")
	}
	sort.Strings(v.ClientErrors)
	v.ScreenS = v.FreezeS + v.SlateS
	return v
}

// judgeRow applies the common rules plus the scenario's preset budget.
func judgeRow(sc liveScenario, preset string, r liveResult) (string, []string) {
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
	if td := p.TargetDuration; td > 0 && p.LongestNoNewSegmentS > 1.5*td {
		why = append(why, fmt.Sprintf("no new segment for %.1fs (> 1.5x TD %.0fs)", p.LongestNoNewSegmentS, td))
	}
	if p.OverlongSegments > 0 {
		why = append(why, fmt.Sprintf("%d segments exceed TD+0.5s (max %.1fs)", p.OverlongSegments, p.MaxSegmentS))
	}
	if len(p.SequenceErrors) > 0 {
		why = append(why, "sequence: "+p.SequenceErrors[0])
	}
	if v.TTFFS == 0 || v.TTFFS > liveTTFFMaxS {
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
