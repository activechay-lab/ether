package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// runPlex executes the opt-in real-handler fixtures against this checkout's
// code. Go's test timeout is shorter than the parent deadline; encoder
// guardians own their child processes even when the test executable exits.
func runPlex(ctx context.Context, ff, fp, fixtures, mode string) ([]result, error) {
	pattern := "^TestTranscodeChaos(Plex|Clients|Matrix)$"
	if mode == "full" {
		pattern = "^TestTranscodeChaos(Plex|Clients|Matrix|PlexFaults|LiveRelay|LiveHardware|AppleRelay|DASHIFRelay|HTTPHardware|PCELongSeek|CorpusScenarios|DV5Graphs)$"
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-p", "1", "-tags", "transcodechaos", "./internal/frontends/plex", "./internal/transcode", "-run", pattern, "-count=1", "-timeout=12m", "-json")
	cmd.Env = append(os.Environ(), "CHAOS_FFMPEG="+ff, "CHAOS_FFPROBE="+fp, "CHAOS_CORPUS="+fixtures, "GOMAXPROCS=2")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	// Keep raw offline test diagnostics beside the fixtures, outside reports.
	diagnostics, logErr := os.CreateTemp(fixtures, "test-events-*.jsonl")
	if logErr == nil {
		defer diagnostics.Close()
	}
	var results []result
	failedGroups := map[string]bool{}
	hasChildren := map[string]bool{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		if diagnostics != nil {
			_, _ = diagnostics.Write(append(append([]byte(nil), scanner.Bytes()...), '\n'))
		}
		var e struct {
			Action  string
			Test    string
			Elapsed float64
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Action == "run" {
			if i := strings.LastIndex(e.Test, "/"); i > 0 {
				hasChildren[e.Test[:i]] = true
			}
			continue
		}
		if hasChildren[e.Test] {
			continue
		}
		if e.Action != "pass" && e.Action != "fail" && e.Action != "skip" {
			continue
		}
		parts := strings.Split(e.Test, "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		if len(parts) == 1 && (parts[0] != "TestTranscodeChaosLiveRelay" && parts[0] != "TestTranscodeChaosLiveHardware" && parts[0] != "TestTranscodeChaosAppleRelay" && parts[0] != "TestTranscodeChaosDASHIFRelay" && parts[0] != "TestTranscodeChaosHTTPHardware" && parts[0] != "TestTranscodeChaosPCELongSeek") {
			continue
		}
		if len(parts) > 1 && e.Test == parts[0] {
			continue
		}
		if len(parts) == 2 && (parts[0] == "TestTranscodeChaosMatrix" || parts[0] == "TestTranscodeChaosPlexFaults") {
			if e.Action != "fail" || failedGroups[e.Test] {
				continue
			}
			item := result{parts[1], "plex/setup", "FAIL", "fixture setup failed; rerun this test with -v for details", int64(e.Elapsed * 1000)}
			results = append(results, item)
			fmt.Printf("%-32s %-20s %-4s %6dms %s\n", item.Scenario, item.Path, item.Status, item.Milliseconds, item.Detail)
			continue
		}
		if e.Action == "fail" && len(parts) > 2 {
			failedGroups[strings.Join(parts[:2], "/")] = true
		}
		name, path := parts[0], "plex"
		if len(parts) > 1 {
			name = strings.Join(parts[1:len(parts)-1], "/")
			if name == "" {
				name = parts[1]
			}
			path = "plex/" + parts[len(parts)-1]
		}
		item := result{name, path, strings.ToUpper(e.Action), "", int64(e.Elapsed * 1000)}
		if parts[0] == "TestTranscodeChaosDV5Graphs" {
			item.Scenario, item.Path = "dv5-filter-graph", "graph/"+parts[len(parts)-1]
		}
		if item.Status == "FAIL" {
			item.Detail = "handler scenario failed; rerun this test with -v for details"
		}
		results = append(results, item)
		fmt.Printf("%-32s %-20s %-4s %6dms %s\n", item.Scenario, item.Path, item.Status, item.Milliseconds, item.Detail)
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return results, scanErr
	}
	if len(results) == 0 && waitErr != nil {
		return results, fmt.Errorf("Plex harness could not run: %w", waitErr)
	}
	if waitErr != nil {
		failed := false
		for _, item := range results {
			failed = failed || item.Status == "FAIL"
		}
		if !failed {
			item := result{"Plex harness", "plex/runner", "FAIL", "test process failed or timed out", 0}
			results = append(results, item)
			fmt.Printf("%-32s %-20s %-4s %6dms %s\n", item.Scenario, item.Path, item.Status, item.Milliseconds, item.Detail)
		}
	}
	return results, nil
}
