package main

import (
	"strings"
	"time"
)

// The live acceptance matrix. Each scenario runs once per listed Ether mode.
// Targets are data so every worker is measured against the same bar; a row
// passes only when every rule in its effective criteria holds.

type liveScenario struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Source is the primary provider; Backup is optional.
	Source providerConfig  `json:"source"`
	Backup *providerConfig `json:"backup,omitempty"`
	Modes  []string        `json:"modes"`
	// Quick lists the modes in the < 2 minute subset.
	Quick []string `json:"quick,omitempty"`
	// Budget is the viewer-visible allowance per stability preset.
	Budget map[string]liveBudget `json:"budget"`
	// Recoverable is false when no working input remains: the target is then
	// a calm screen and no client error, not recovery.
	Recoverable bool `json:"recoverable"`
	// MaxConcurrent bounds provider connections (account limit scenarios).
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
}

// liveBudget is what a viewer may see. Screen is freeze + non-content
// (Ether screen or black) seconds after the first picture.
type liveBudget struct {
	ScreenS  float64 `json:"screenSeconds"`
	SilenceS float64 `json:"silenceSeconds"`
}

// Presets are worker D's Stability values.
var livePresets = []string{"fastest", "balanced", "rocksolid"}

const (
	liveFaultAt  = 15 * time.Second // after provider start; tune happens at ~0
	liveObserve  = 60 * time.Second
	liveTTFFMaxS = 5.0
)

func budget(fastest, balanced, rocksolid float64, silence float64) map[string]liveBudget {
	return map[string]liveBudget{
		"fastest":   {ScreenS: fastest, SilenceS: max(silence, fastest)},
		"balanced":  {ScreenS: balanced, SilenceS: max(silence, balanced)},
		"rocksolid": {ScreenS: rocksolid, SilenceS: max(silence, rocksolid)},
	}
}

func hlsSource(faults ...liveFault) providerConfig {
	return providerConfig{Name: "primary", HLS: true, Backlog: 6, Faults: faults}
}
func tsSource(faults ...liveFault) providerConfig {
	return providerConfig{Name: "primary", Faults: faults}
}
func at(kind string, d time.Duration, code int) liveFault {
	return liveFault{Kind: kind, At: liveFaultAt, For: d, Code: code}
}

var (
	allTS  = []string{"relay-copy", "relay-transcode", "steady"}
	allHLS = []string{"passthrough", "relay-transcode", "steady"}
)

func liveMatrix() []liveScenario {
	// A blip a reserve should hide: 0 s with Balanced/Rock solid, <= 3 s on Fastest.
	blip := budget(3, 0, 0, 0)
	// Format changes: no error, at most a 1 s hiccup.
	format := budget(1, 1, 1, 1)
	damage := budget(2, 2, 2, 2)
	return []liveScenario{
		{Name: "healthy-ts", Summary: "Healthy raw TS control", Source: tsSource(), Modes: allTS, Budget: budget(0, 0, 0, 0), Recoverable: true},
		{Name: "healthy-hls", Summary: "Healthy provider HLS control", Source: hlsSource(), Modes: allHLS, Budget: budget(0, 0, 0, 0), Recoverable: true},
		{Name: "hls-playlist-stall-8s", Summary: "Provider playlist stops advancing for 8 s, then catches up", Source: hlsSource(at(faultPlaylistStall, 8*time.Second, 0)), Modes: allHLS, Quick: []string{"passthrough"}, Budget: blip, Recoverable: true},
		{Name: "ts-stall-8s", Summary: "Raw TS body sends nothing for 8 s on an open connection", Source: tsSource(at(faultTSStall, 8*time.Second, 0)), Modes: allTS, Quick: []string{"steady"}, Budget: blip, Recoverable: true},
		{Name: "hls-segment-404", Summary: "One provider segment returns 404", Source: hlsSource(at(faultSegmentError, 4*time.Second, 404)), Modes: []string{"passthrough", "steady"}, Budget: blip, Recoverable: true},
		{Name: "hls-segment-503", Summary: "Two provider segments return 503", Source: hlsSource(at(faultSegmentError, 8*time.Second, 503)), Modes: []string{"passthrough", "steady"}, Budget: blip, Recoverable: true},
		{Name: "hls-segment-slow-8s", Summary: "Provider segment responses wait 8 s", Source: hlsSource(at(faultSegmentSlow, 8*time.Second, 0)), Modes: []string{"passthrough", "steady"}, Budget: blip, Recoverable: true},
		{Name: "hls-rst", Summary: "Provider resets HLS requests for 3 s", Source: hlsSource(at(faultRST, 3*time.Second, 0)), Modes: []string{"passthrough", "steady"}, Budget: blip, Recoverable: true},
		{Name: "ts-rst", Summary: "Raw TS connection reset mid-stream", Source: tsSource(at(faultRST, 0, 0)), Modes: allTS, Quick: []string{"relay-copy"}, Budget: blip, Recoverable: true},
		{Name: "ts-eof", Summary: "Raw TS body ends cleanly mid-stream", Source: tsSource(at(faultEOF, 0, 0)), Modes: []string{"relay-copy", "steady"}, Budget: blip, Recoverable: true},
		{Name: "ts-refuse-458", Summary: "Reset, then reconnects refused 458 for 10 s", Source: tsSource(at(faultRST, 0, 0), at(faultRefuse, 10*time.Second, 458)), Modes: []string{"relay-copy", "steady"}, Budget: budget(3, 0, 0, 0), Recoverable: true},
		{Name: "ts-refuse-403", Summary: "Reset, then reconnects refused 403 for 10 s", Source: tsSource(at(faultRST, 0, 0), at(faultRefuse, 10*time.Second, 403)), Modes: []string{"relay-copy"}, Budget: budget(3, 0, 0, 0), Recoverable: true},
		{Name: "ts-refuse-509", Summary: "Reset, then reconnects refused 509 for 10 s", Source: tsSource(at(faultRST, 0, 0), at(faultRefuse, 10*time.Second, 509)), Modes: []string{"relay-copy"}, Budget: budget(3, 0, 0, 0), Recoverable: true},
		{Name: "ts-conn-limit-1", Summary: "Account limit 1; reset mid-stream; reconnect must not overlap", Source: providerConfig{Name: "primary", ConnLimit: 1, LimitCode: 458, Faults: []liveFault{at(faultRST, 0, 0)}}, Modes: []string{"relay-copy", "steady"}, Budget: blip, Recoverable: true, MaxConcurrent: 1},
		{Name: "primary-dies-backup-healthy", Summary: "Primary resets and stays down; healthy backup", Source: tsSource(at(faultDown, 0, 0)), Backup: &providerConfig{Name: "backup"}, Modes: []string{"relay-copy", "steady"}, Quick: []string{"steady"}, Budget: budget(3, 3, 3, 3), Recoverable: true},
		{Name: "hls-primary-dies-backup-healthy", Summary: "HLS primary down for good; healthy HLS backup", Source: hlsSource(at(faultDown, 0, 0)), Backup: &providerConfig{Name: "backup", HLS: true, Backlog: 6}, Modes: []string{"passthrough", "steady"}, Budget: budget(3, 3, 3, 3), Recoverable: true},
		{Name: "primary-dies-backup-absent", Summary: "Primary down for good; backup URL refuses connections", Source: tsSource(at(faultDown, 0, 0)), Backup: &providerConfig{Name: "backup", Absent: true}, Modes: []string{"relay-copy", "steady"}, Budget: budget(1e9, 1e9, 1e9, 1e9), Recoverable: false},
		{Name: "primary-dies-backup-failing", Summary: "Primary and backup both down for good", Source: tsSource(at(faultDown, 0, 0)), Backup: &providerConfig{Name: "backup", Faults: []liveFault{at(faultDown, 0, 0)}}, Modes: []string{"steady"}, Budget: budget(1e9, 1e9, 1e9, 1e9), Recoverable: false},
		{Name: "ts-codec-ac3-pmt", Summary: "Audio AAC->AC3, same PID, PMT version bumped", Source: tsSource(at(faultCodecAC3, 0, 0)), Modes: allTS, Quick: []string{"relay-copy"}, Budget: format, Recoverable: true},
		{Name: "ts-codec-ac3-nobump", Summary: "Audio AAC->AC3, same PID, no PMT version bump", Source: tsSource(at(faultCodecAC3Same, 0, 0)), Modes: []string{"relay-copy", "steady"}, Budget: format, Recoverable: true},
		{Name: "hls-codec-ac3-untagged", Summary: "Provider HLS switches AAC->AC3 without EXT-X-DISCONTINUITY", Source: hlsSource(at(faultCodecAC3, 0, 0)), Modes: []string{"passthrough", "steady"}, Budget: format, Recoverable: true},
		{Name: "hls-codec-ac3-tagged", Summary: "Provider HLS switches AAC->AC3 with EXT-X-DISCONTINUITY", Source: withDisc(hlsSource(at(faultCodecAC3, 0, 0))), Modes: []string{"passthrough"}, Budget: format, Recoverable: true},
		{Name: "ts-resolution", Summary: "Video 640x360 -> 1280x720 mid-stream", Source: tsSource(at(faultResolution, 0, 0)), Modes: []string{"relay-copy", "steady"}, Budget: format, Recoverable: true},
		{Name: "hls-resolution-untagged", Summary: "Provider HLS 640x360 -> 1280x720 without a tag", Source: hlsSource(at(faultResolution, 0, 0)), Modes: []string{"passthrough"}, Budget: format, Recoverable: true},
		{Name: "ts-clock-jump", Summary: "Timestamps jump +5000 s", Source: tsSource(at(faultClockJump, 0, 0)), Modes: []string{"relay-copy", "steady"}, Budget: format, Recoverable: true},
		{Name: "ts-clock-reset", Summary: "Timestamps restart near zero", Source: tsSource(at(faultClockReset, 0, 0)), Modes: []string{"relay-copy", "steady"}, Budget: format, Recoverable: true},
		{Name: "hls-clock-jump-untagged", Summary: "Provider HLS timestamps jump without a tag", Source: hlsSource(at(faultClockJump, 0, 0)), Modes: []string{"passthrough"}, Budget: format, Recoverable: true},
		{Name: "ts-long-gop-10s", Summary: "10 s GOP raw TS", Source: providerConfig{Name: "primary", Fixture: "longgop"}, Modes: []string{"relay-copy", "steady"}, Quick: []string{"relay-copy"}, Budget: budget(0, 0, 0, 0), Recoverable: true},
		{Name: "ts-keyframe-loss", Summary: "Keyframes lost for 8 s", Source: tsSource(at(faultKeyframeLoss, 8*time.Second, 0)), Modes: []string{"relay-copy", "steady"}, Budget: damage, Recoverable: true},
		{Name: "ts-corrupt", Summary: "1% of packets carry garbage for 8 s", Source: tsSource(at(faultCorrupt, 8*time.Second, 0)), Modes: []string{"relay-copy", "steady"}, Budget: damage, Recoverable: true},
		{Name: "ts-packet-loss", Summary: "2% packet loss for 8 s", Source: tsSource(at(faultPacketLoss, 8*time.Second, 0)), Modes: []string{"relay-copy", "steady"}, Budget: damage, Recoverable: true},
	}
}

func withDisc(p providerConfig) providerConfig { p.Discontinuities = true; return p }

type liveRow struct {
	Scenario liveScenario
	Mode     string
}

func (r liveRow) ID() string { return r.Scenario.Name + "/" + r.Mode }

// selectRows expands the matrix. subset is "quick" or "full"; only filters
// row IDs by substring (comma-separated).
func selectRows(subset, only string) []liveRow {
	var rows []liveRow
	for _, sc := range liveMatrix() {
		modes := sc.Modes
		if subset == "quick" {
			modes = sc.Quick
		}
		for _, m := range modes {
			row := liveRow{sc, m}
			if only != "" {
				match := false
				for _, o := range strings.Split(only, ",") {
					match = match || (o != "" && strings.Contains(row.ID(), o))
				}
				if !match {
					continue
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}
