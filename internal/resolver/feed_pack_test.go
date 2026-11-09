package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestPackEvidenceReason(t *testing.T) {
	sreq := core.SearchRequest{Type: core.TypeEpisode, Season: 1, Episode: 1}
	for _, tc := range []struct {
		name, release, protocol, want string
	}{
		{"bare usenet pack", "Series.S01.1080p.WEB-DL", core.ProtocolUsenet, "season pack does not list the episode"},
		{"usenet pack listing the episode", "Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", core.ProtocolUsenet, ""},
		{"usenet pack listing other episodes", "Доктор Краснов / Сезон: 1 / Серии: 3-16 из 16 [2023 WEBRip]", core.ProtocolUsenet, "season pack does not list the episode"},
		{"single episode", "Series.S01E01.1080p.WEB-DL", core.ProtocolUsenet, ""},
		{"bare torrent pack unchanged", "Series.S01.1080p.WEB-DL", core.ProtocolTorrent, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := core.Release{Name: tc.release, Info: releaseparse.Parse(tc.release), Protocol: tc.protocol}
			if got := packEvidenceReason(r, sreq); got != tc.want {
				t.Fatalf("%q = %q, want %q (parsed %+v)", tc.release, got, tc.want, r.Info)
			}
		})
	}
	if got := packEvidenceReason(core.Release{Info: core.ReleaseInfo{FullSeason: true}}, core.SearchRequest{Type: core.TypeMovie}); got != "" {
		t.Fatalf("movie request: %q", got)
	}
}
