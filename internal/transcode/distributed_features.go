package transcode

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
)

// These optional probes run once at Manager startup, never on a request. A
// bounded total deadline/output and no retries keep a broken optional feature
// query from changing the existing standalone startup availability contract.
func probeDistributedFeatures(ctx context.Context, r Runner, ffmpeg string, f *features) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	queries := []struct {
		flag  string
		parse func([]byte) map[string]bool
		dst   *map[string]bool
	}{
		{"-muxers", parseFormats, &f.muxers}, {"-demuxers", parseFormats, &f.demuxers},
		{"-decoders", parseEncoders, &f.decoders}, {"-bsfs", parseBSFs, &f.bsfs},
		{"-protocols", parseInputProtocols, &f.inputProtocols},
	}
	for _, q := range queries {
		queryCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		out := &boundedFeatureOutput{}
		err := r.Run(queryCtx, Command{Path: ffmpeg, Args: []string{"-hide_banner", q.flag}, Stdout: out, Stderr: out})
		stop()
		if err == nil && !out.overflow {
			if parsed := q.parse(out.Bytes()); len(parsed) > 0 {
				*q.dst = parsed
			}
		}
	}
}

type boundedFeatureOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (b *boundedFeatureOutput) Len() int      { return b.data.Len() }
func (b *boundedFeatureOutput) Bytes() []byte { return b.data.Bytes() }
func (b *boundedFeatureOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-b.Len() {
		b.overflow = true
		return 0, errors.New("transcode: optional feature output exceeds bound")
	}
	return b.data.Write(p)
}
func inventoryName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func parseFormats(b []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "D" && fields[0] != "E" && fields[0] != "DE") {
			continue
		}
		for _, name := range strings.Split(fields[1], ",") {
			if inventoryName(name) {
				out[name] = true
			}
		}
	}
	return out
}
func parseBSFs(b []byte) map[string]bool {
	out := map[string]bool{}
	started := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "Bitstream filters:" {
			started = true
			continue
		}
		if started && inventoryName(line) {
			out[line] = true
		}
	}
	return out
}
func parseInputProtocols(b []byte) map[string]bool {
	out := map[string]bool{}
	started := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "Input:" {
			started = true
			continue
		}
		if line == "Output:" {
			break
		}
		if started && inventoryName(line) {
			out[line] = true
		}
	}
	return out
}
