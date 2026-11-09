package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WS3 rows: Ether learns from a source's history and must start the next
// session right. Each row primes the history with one or more real sessions,
// then judges a measured session with the usual rules plus the decision Ether
// logged for it ("live playback path selected"):
//
//   - retune / restart (C, D): an owner no-screen (Direct) channel whose
//     source was damaged or slow to key in the prime session must start the
//     measured session re-encoded (path=transcode reason=history evidence=...),
//     never copy, with 0 s screen. restart closes Ether entirely between the
//     sessions and reopens the same store.
//   - capacity-* (B, D): two Steady channels, a Steady budget of exactly one
//     output. Flaky (F, a reset in its prime) and clean (H, a prime of 60 s+
//     without events) sources: flaky or unknown sources get Steady when they
//     fit, a clean one yields the last slot, and a running viewer is never
//     pre-empted.

const (
	historyPrime       = 45 * time.Second
	historyCleanPrime  = 65 * time.Second // B: clean needs >= 60 s of observed media
	historyMeasure     = 40 * time.Second
	historySteadyCores = 0.85 // one 576p30 Steady output (0.57 cores) fits, two do not
)

func runHistoryRow(parent context.Context, o liveOptions, row liveRow, fixtures map[string]*liveFixture, player, work string) (res liveResult) {
	sc := row.Scenario
	if sc.WS3 == "handoff" {
		return runHandoffRow(parent, o, row, fixtures, player, work)
	}
	res = liveResult{ID: row.ID(), Scenario: sc.Name, Summary: sc.Summary, Mode: row.Mode, Preset: o.Preset, Verdict: "FAIL"}
	wall := time.Now()
	res.HostLoad[0] = hostLoad()
	defer func() { res.WallSeconds = time.Since(wall).Seconds(); res.HostLoad[1] = hostLoad() }()
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	start := time.Now()
	// The providers live for the whole row: history is keyed by the source
	// URL, so the sessions must see the same one.
	primary, err := newLiveProvider(sc.Source, fixtures, start)
	if err != nil {
		res.Error = err.Error()
		return
	}
	defer primary.Close()
	setups := []channelSetup{{Mode: row.Mode, Primary: primary.url, Stability: o.Preset}}
	var second *liveProvider
	if sc.Second != nil {
		if second, err = newLiveProvider(*sc.Second, fixtures, start); err != nil {
			res.Error = err.Error()
			return
		}
		defer second.Close()
		setups = append(setups, channelSetup{Mode: row.Mode, Primary: second.url, Stability: o.Preset})
	}
	eo := etherOptions{DBPath: filepath.Join(work, "ether.db")}
	capacity := strings.HasPrefix(sc.WS3, "capacity-")
	if capacity {
		eo.Budget = historySteadyCores
	}
	ether, chs, err := startLiveEtherWith(ctx, o.FF, o.FP, work, setups, eo)
	if err != nil {
		res.Error = "ether: " + err.Error()
		return
	}
	defer func() { ether.Close() }()
	res.Applied = ether.Stability
	check := func(ok bool, f string, a ...any) {
		mark := "ok: "
		if !ok {
			mark = "FAIL: "
		}
		res.Checks = append(res.Checks, mark+fmt.Sprintf(f, a...))
	}

	// Prime.
	prime := func(chID, client string, d time.Duration, dir string) {
		_ = os.MkdirAll(dir, 0o700)
		w := watchSession(ctx, o, sc, ether, chID, client, dir, "", d, false)
		if w.Error != "" {
			res.Checks = append(res.Checks, "prime: "+w.Error)
		}
	}
	switch sc.WS3 {
	case "retune", "restart":
		prime(chs[0].ID, "", historyPrime, filepath.Join(work, "prime"))
	case "capacity-flaky-first", "capacity-clean-first":
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			prime(chs[0].ID, "chaos-prime-f", historyCleanPrime, filepath.Join(work, "prime-f"))
		}()
		go func() {
			defer wg.Done()
			prime(chs[1].ID, "chaos-prime-h", historyCleanPrime, filepath.Join(work, "prime-h"))
		}()
		wg.Wait()
	case "capacity-unknown-first":
		prime(chs[0].ID, "chaos-prime-f", historyCleanPrime, filepath.Join(work, "prime-f"))
	}
	if b, err := json.Marshal(ether.service.SourceHistories()); err == nil {
		res.History = b
	}
	// The measured sessions start on an idle Ether: restart rows by design,
	// capacity rows so no prime output still holds the budget.
	if sc.WS3 == "restart" || capacity {
		ether.Close()
		eo.Reuse = true
		if ether, chs, err = startLiveEtherWith(ctx, o.FF, o.FP, work, setups, eo); err != nil {
			res.Error = "ether restart: " + err.Error()
			return
		}
		if b, err := json.Marshal(ether.service.SourceHistories()); err == nil {
			res.History = b // what the restarted Ether loaded
		}
	}
	mark := ether.logMark()

	// Measure.
	if !capacity {
		w := watchSession(ctx, o, sc, ether, chs[0].ID, "", work, player, historyMeasure, false)
		res.TuneS, res.Path, res.Protocol, res.Player, res.Viewer, res.Error = w.TuneS, w.Path, w.Protocol, w.Player, w.Viewer, w.Error
		res.Provider = primary.Stats()
		if !w.tuneFailed {
			scoreRow(sc, o.Preset, liveStuckFactor[o.Client], &res, w.av)
		}
		line := firstPath(ether.pathLines(mark), chs[0].ID)
		check(line["path"] == "transcode" && line["reason"] == "history" && (line["evidence"] == "damage" || line["evidence"] == "keyframe"),
			"measured session starts re-encoded from history: want path=transcode reason=history evidence=damage|keyframe, got %s", describePath(line))
		res.Path = line["path"]
	} else {
		first, later := chs[0], chs[1] // F, H
		if sc.WS3 != "capacity-flaky-first" {
			first, later = chs[1], chs[0]
		}
		var a, b watched
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			a = watchSession(ctx, o, sc, ether, first.ID, "chaos-viewer-1", filepath.Join(work, "s1"), player, historyMeasure+5*time.Second, false)
		}()
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Second)
			b = watchSession(ctx, o, sc, ether, later.ID, "chaos-viewer-2", filepath.Join(work, "s2"), player, historyMeasure, false)
		}()
		wg.Wait()
		res.TuneS, res.Protocol, res.Player, res.Viewer, res.Error = a.TuneS, a.Protocol, a.Player, a.Viewer, a.Error
		res.Provider = primary.Stats()
		if !a.tuneFailed {
			scoreRow(sc, o.Preset, liveStuckFactor[o.Client], &res, a.av)
		}
		other := liveResult{ID: res.ID + "#2", Scenario: sc.Name, Mode: row.Mode, Preset: o.Preset, Verdict: "FAIL", TuneS: b.TuneS, Protocol: b.Protocol, Player: b.Player, Viewer: b.Viewer, Error: b.Error}
		if !b.tuneFailed {
			scoreRow(sc, o.Preset, liveStuckFactor[o.Client], &other, b.av)
		}
		lines := ether.pathLines(mark)
		fl, hl := firstPath(lines, chs[0].ID), firstPath(lines, chs[1].ID)
		switch sc.WS3 {
		case "capacity-flaky-first":
			check(fl["path"] == "steady" && fl["allocation"] == "flaky", "flaky channel tuned first gets Steady (allocation=flaky): got %s", describePath(fl))
			check(hl["path"] == "direct" && hl["reason"] == "capacity", "clean channel tuned second plays Direct for capacity: got %s", describePath(hl))
		case "capacity-clean-first":
			check(hl["path"] == "direct" && hl["reason"] == "clean-yields-last-slot", "clean channel tuned first yields the last slot: got %s", describePath(hl))
			check(fl["path"] == "steady" && fl["allocation"] == "flaky", "flaky channel tuned second gets Steady: got %s", describePath(fl))
		case "capacity-unknown-first":
			check(hl["path"] == "steady" && hl["allocation"] == "unknown", "channel without history tuned first gets Steady (allocation=unknown): got %s", describePath(hl))
			check(fl["path"] == "direct" && fl["reason"] == "capacity", "flaky channel is not given a running viewer's slot (direct, capacity): got %s", describePath(fl))
		}
		res.Path = fmt.Sprintf("F=%s H=%s", fl["path"], hl["path"])
		other.Path = hl["path"]
		res.Others = []liveResult{other}
		if other.Verdict == "INVALID" && res.Verdict != "FAIL" {
			res.Verdict = "INVALID"
		} else if other.Verdict == "FAIL" && res.Verdict != "INVALID" {
			res.Verdict = "FAIL"
			res.Reasons = append(res.Reasons, "second viewer: "+strings.Join(other.Reasons, "; "))
		}
	}
	for _, c := range res.Checks {
		if strings.HasPrefix(c, "FAIL: ") && res.Verdict != "INVALID" {
			res.Verdict = "FAIL"
			res.Reasons = append(res.Reasons, strings.TrimPrefix(c, "FAIL: "))
		}
	}
	return
}

// firstPath is the first path decision logged for a channel: Steady's own
// (steady|direct) when Steady was considered, else the relay's.
func firstPath(lines []pathLine, channelID string) pathLine {
	for _, l := range lines {
		if l["channel"] == channelID {
			return l
		}
	}
	return pathLine{}
}

func describePath(l pathLine) string {
	if len(l) == 0 {
		return "no path decision logged"
	}
	var parts []string
	for _, k := range []string{"path", "reason", "evidence", "allocation", "detail"} {
		if v := l[k]; v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}
