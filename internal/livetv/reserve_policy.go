package livetv

import (
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// The stability preset is the owner's one Live TV reliability choice: how
// much provider stream Ether keeps in reserve, behind the live edge, so a
// provider blip plays out of the reserve while Ether reconnects or fails
// over. It is server-wide (config.LiveTV.Stability) with a per-channel
// override (Channel.Stability).
//
// The reserve only ever comes from what costs the viewer nothing visible:
// an HLS provider's backlog (Steady starts that far back), and a provider
// delivering faster than real time (an initial burst, catch-up after a
// reconnect). Ether never slows playback to build one and never delays a
// tune for it.
const (
	stabilityFastest   = config.LiveTVStabilityFastest
	stabilityBalanced  = config.LiveTVStabilityBalanced
	stabilityRockSolid = config.LiveTVStabilityRockSolid
)

// stabilityReserves are the preset targets. Balanced holds enough for the
// common provider blip (a dropped connection and a reconnect, typically a
// few seconds) plus a same-source retry ladder; Rock solid covers a failover
// to a backup source that has to be resolved and probed.
var stabilityReserves = map[string]time.Duration{
	stabilityFastest:   0,
	stabilityBalanced:  12 * time.Second,
	stabilityRockSolid: 30 * time.Second,
}

// stabilityPreset resolves the channel's preset: its own override, else the
// server default, else balanced.
func (s *Service) stabilityPreset(c Channel) string {
	if v := strings.ToLower(strings.TrimSpace(c.Stability)); v != "" && config.ValidLiveTVStability(v) {
		return v
	}
	if s != nil && s.o.Stability != nil {
		if v := strings.ToLower(strings.TrimSpace(s.o.Stability())); v != "" && config.ValidLiveTVStability(v) {
			return v
		}
	}
	return stabilityBalanced
}

// stabilityReserve is the reserve the channel's preset aims for.
func (s *Service) stabilityReserve(c Channel) time.Duration {
	return stabilityReserves[s.stabilityPreset(c)]
}
