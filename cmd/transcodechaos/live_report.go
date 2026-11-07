package main

import (
	"fmt"
	"strings"
)

func liveMarkdown(rep liveReport, base *liveReport, o liveOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Live acceptance: %s\n\n", rep.Label)
	fmt.Fprintf(&b, "- Commit `%s`, subset `%s`, preset `%s`, judge `%s`\n", rep.Commit, rep.Subset, rep.Preset, rep.Player)
	fmt.Fprintf(&b, "- Ether's ffmpeg: %s\n- Host: %s, %s, %.0f s wall clock\n\n", rep.FFmpeg, rep.Host, rep.StartedAt.Format("2006-01-02 15:04 UTC"), rep.Seconds)
	pass := 0
	for _, r := range rep.Results {
		if r.Verdict == "PASS" {
			pass++
		}
	}
	fmt.Fprintf(&b, "**%d of %d rows pass.**\n\n", pass, len(rep.Results))
	byID := map[string]liveResult{}
	if base != nil {
		for _, r := range base.Results {
			byID[r.ID] = r
		}
		fmt.Fprintf(&b, "Compared with baseline `%s` (%s).\n\n", base.Label, base.Commit)
	}
	b.WriteString("| Row | Ether path | Verdict | TTFF s | Screen s (freeze+slate) | Silence s | Longest no-new-segment s | Client errors | Recovered |")
	if base != nil {
		b.WriteString(" Baseline |")
	}
	b.WriteString("\n|---|---|---|---:|---:|---:|---:|---|---|")
	if base != nil {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, r := range rep.Results {
		v := r.Viewer
		errs := strings.Join(v.ClientErrors, "<br>")
		if r.Error != "" {
			errs = strings.TrimPrefix(errs+"<br>"+r.Error, "<br>")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %.1f | %.1f (%.1f+%.1f) | %.1f | %.1f | %s | %v |", r.ID, r.Path, r.Verdict, v.TTFFS, v.ScreenS, v.FreezeS, v.SlateS, v.SilenceS, r.Protocol.LongestNoNewSegmentS, mdEscape(errs), v.Recovered)
		if base != nil {
			if old, ok := byID[r.ID]; ok {
				fmt.Fprintf(&b, " %s, screen %.1f, %d errors |", old.Verdict, old.Viewer.ScreenS, len(old.Viewer.ClientErrors))
			} else {
				b.WriteString(" n/a |")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Failure reasons\n\n")
	for _, r := range rep.Results {
		if r.Verdict == "PASS" {
			continue
		}
		fmt.Fprintf(&b, "- `%s` (%s): %s\n", r.ID, r.Summary, mdEscape(strings.Join(append(r.Reasons, r.Error), "; ")))
	}
	b.WriteString("\n## How each number is measured\n\n")
	b.WriteString("- **Screen** = freeze + slate seconds after the first provider picture. Budgets per preset are in `cmd/transcodechaos/live_scenarios.go`.\n")
	b.WriteString("- **Freeze**: with the AVFoundation judge, wall-clock gaps of at least 0.5 s between frames AVPlayer delivered, plus held (frozen) frames inside the published media (ffmpeg freezedetect). Without it, a player buffer model over observed segment arrivals.\n")
	b.WriteString("- **Slate**: 10 Hz samples of the fixture's green top band in every published segment; samples without it are an Ether screen or black.\n")
	b.WriteString("- **Silence**: ffmpeg silencedetect (-45 dB, 0.25 s) on the published segments. In-player audio is not measured: AVFoundation audio taps do not attach to HLS items.\n")
	b.WriteString("- **Longest no-new-segment**: the protocol judge polls the client playlist every 0.5 s. Apple players stop with -12888 after 1.5x target duration without a new segment.\n")
	b.WriteString("- **Client errors**: AVPlayer item failures and error-log codes, HTTP errors Ether returned to the client, and unmarked codec changes between consecutive segments (Apple -12971).\n")
	b.WriteString("- **TTFF**: tune request to the first frame AVPlayer delivered (or the first downloaded segment without the player).\n")
	return b.String()
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
