package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHandoffRow exercises real feeds, unlike the package's fake-feed splice
// tests. Fault injection is armed after the previous channel's idle grace has
// released its upstream, so machine speed cannot turn the intended handoff
// into a capacity refusal followed by the two-minute retry cooldown.
func runHandoffRow(parent context.Context, o liveOptions, row liveRow, fixtures map[string]*liveFixture, player, work string) (res liveResult) {
	sc := row.Scenario
	res = liveResult{ID: row.ID(), Scenario: sc.Name, Summary: sc.Summary, Mode: row.Mode, Preset: o.Preset, Verdict: "FAIL"}
	wall := time.Now()
	res.HostLoad[0] = hostLoad()
	defer func() { res.WallSeconds = time.Since(wall).Seconds(); res.HostLoad[1] = hostLoad() }()
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	check := func(ok bool, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		prefix := "ok: "
		if !ok {
			prefix = "FAIL: "
			res.Reasons = append(res.Reasons, msg)
		}
		res.Checks = append(res.Checks, prefix+msg)
	}
	// Three unknown sources: blocker, measured viewer, and a fresh post-teardown
	// admission. The third cannot yield its slot because of a learned clean history.
	var providers []*liveProvider
	defer func() {
		for _, p := range providers {
			p.Close()
		}
	}()
	var setups []channelSetup
	for _, name := range []string{"blocker", "target", "after"} {
		p, err := newLiveProvider(providerConfig{Name: name}, fixtures, wall)
		if err != nil {
			res.Error = err.Error()
			return
		}
		providers = append(providers, p)
		setups = append(setups, channelSetup{Mode: "steady", Primary: p.url, Stability: o.Preset})
	}
	ether, channels, err := startLiveEtherWith(ctx, o.FF, o.FP, work, setups, etherOptions{Budget: historySteadyCores})
	if err != nil {
		res.Error = err.Error()
		return
	}
	defer ether.Close()
	res.Applied = ether.Stability
	mark := ether.logMark()
	blocker, _, err := ether.tunePlexAs(ctx, o.Client, "chaos-blocker", channels[0].ID)
	if err != nil {
		res.Error = "blocker tune: " + err.Error()
		return
	}
	defer func() { _ = ether.stopSession(context.Background(), blocker) }()
	check(firstPath(ether.pathLines(mark), channels[0].ID)["path"] == "steady", "blocker occupies the Steady slot")
	done := make(chan watched, 1)
	go func() {
		done <- watchSession(ctx, o, sc, ether, channels[1].ID, "chaos-handoff", filepath.Join(work, "target"), player, 130*time.Second, false)
	}()
	// Always reap the viewer, including on an early setup failure.
	var w watched
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-done
		}
	}()
	ready := handoffWait(ctx, 15*time.Second, func() bool { return firstPath(ether.pathLines(mark), channels[1].ID)["path"] != "" })
	line := firstPath(ether.pathLines(mark), channels[1].ID)
	check(ready && line["path"] == "direct" && line["reason"] == "capacity", "target starts Direct for capacity: %s", describePath(line))
	if !ready || line["path"] != "direct" {
		return
	}
	err = ether.stopSession(ctx, blocker)
	check(err == nil, "blocker stop: %v", err)
	released := handoffWait(ctx, 80*time.Second, func() bool { return providerConnections(providers[0]) == 0 })
	check(released, "blocker upstream closes after its idle grace")
	if !released {
		return
	}
	// Corrupt only future media, after slot release; published/cached media stays
	// immutable. A two-segment lead excludes the segment currently being emitted.
	target := providers[1]
	target.corruptAfter.Store(int64(time.Since(target.started) + 2*target.segDur()))
	w = <-done
	joined = true
	res.TuneS, res.Protocol, res.Player, res.Viewer, res.Error = w.TuneS, w.Protocol, w.Player, w.Viewer, w.Error
	res.Path = "direct→steady"
	priorReasons := append([]string(nil), res.Reasons...)
	if !w.tuneFailed {
		scoreRow(sc, o.Preset, liveStuckFactor[o.Client], &res, w.av)
	}
	res.Reasons = append(res.Reasons, priorReasons...)
	log, _ := os.ReadFile(filepath.Join(work, "ether.log"))
	handoffs := 0
	if int64(len(log)) >= mark {
		handoffs = strings.Count(string(log[mark:]), "owned relay handed off to Steady")
	}
	check(handoffs == 1, "same running relay hands off exactly once: got %d", handoffs)
	res.Provider = target.Stats()
	check(res.Provider.MaxConcurrent == 1, "handoff shares one provider connection: maximum %d", res.Provider.MaxConcurrent)
	// A stopped handoff session must release its feed/grant without closing Ether.
	released = handoffWait(ctx, 80*time.Second, func() bool { return providerConnections(target) == 0 })
	check(released, "target upstream closes after viewer stop and idle grace")
	admissionMark := ether.logMark()
	next, _, err := ether.tunePlexAs(ctx, o.Client, "chaos-after", channels[2].ID)
	if err == nil {
		defer func() { _ = ether.stopSession(context.Background(), next) }()
	}
	nextLine := firstPath(ether.pathLines(admissionMark), channels[2].ID)
	check(err == nil && nextLine["path"] == "steady", "fresh channel reuses released Steady capacity: %s (tune error %v)", describePath(nextLine), err)
	if len(res.Reasons) > 0 && res.Verdict != "INVALID" {
		res.Verdict = "FAIL"
	}
	return
}

func providerConnections(p *liveProvider) int { p.mu.Lock(); defer p.mu.Unlock(); return p.open }

func handoffWait(ctx context.Context, limit time.Duration, ready func() bool) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-tick.C:
		}
	}
}
