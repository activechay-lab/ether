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
		ttff := "none"
		if v.TTFFS > 0 {
			ttff = fmt.Sprintf("%.1f", v.TTFFS)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %.1f (%.1f+%.1f) | %.1f | %.1f | %s | %v |", r.ID, r.Path, r.Verdict, ttff, v.ScreenS, v.FreezeS, v.SlateS, v.SilenceS, r.Protocol.LongestNoNewSegmentAdjS, mdEscape(errs), v.Recovered)
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
		why := r.Reasons
		if r.Error != "" {
			why = append(why, r.Error)
		}
		fmt.Fprintf(&b, "- `%s` (%s): %s\n", r.ID, r.Summary, mdEscape(strings.Join(why, "; ")))
	}
	b.WriteString("\n## How each number is measured\n\n")
	b.WriteString("- **Screen** = freeze + slate seconds after the first provider picture. Budgets per preset are in `cmd/transcodechaos/live_scenarios.go`.\n")
	b.WriteString("- **Freeze**: with the AVFoundation judge, gaps of at least 0.5 s between frames AVPlayer delivered plus seconds of repeated (held) provider frames. Without it, a player buffer model over observed segment arrivals plus held provider frames in the published media (ffmpeg freezedetect).\n")
	b.WriteString("- **Slate**: the fixture carries a green band on top; frames without it are an Ether screen or black. Counted from AVPlayer's frames (or 10 Hz samples of the published segments without the player).\n")
	b.WriteString("- Everything before the first provider picture is startup and is judged by **TTFF** (tune to first provider picture); an Ether card shown at tune counts against TTFF, not screen.\n")
	b.WriteString("- **Silence**: ffmpeg silencedetect (-45 dB, 0.25 s) on the published segments. In-player audio is not measured: AVFoundation audio taps do not attach to HLS items.\n")
	b.WriteString("- **Longest no-new-segment**: the protocol judge polls the client playlist every 0.5 s. Apple players stop with -12888 after 1.5x target duration without a new segment.\n")
	b.WriteString("- **Client errors**: AVPlayer item failures and error-log codes, HTTP errors Ether returned to the client, and unmarked codec changes between consecutive segments (Apple -12971).\n")
	b.WriteString("- **TTFF**: tune request to the first frame AVPlayer delivered (or the first downloaded segment without the player).\n")
	return b.String()
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
