package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestEstimateVariantInfoDolbyVisionProfiles(t *testing.T) {
	cases := []struct {
		name         string
		release      string
		wantDV       bool
		wantProfile  int
		wantCompatID int
	}{
		{
			name:         "UHD BluRay Remux DV carries Profile 7 compat 6",
			release:      "The.Signal.1999.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.7.1-GRP",
			wantDV:       true,
			wantProfile:  7,
			wantCompatID: 6,
		},
		{
			name:         "WEB-DL DV.HDR hybrid carries Profile 8 compat 1",
			release:      "The.Signal.1999.2160p.WEB-DL.DV.HDR.DDP5.1.Atmos.H.265-GRP",
			wantDV:       true,
			wantProfile:  8,
			wantCompatID: 1,
		},
		{
			name:         "WEB-DL DV without HDR10 fallback carries Profile 5",
			release:      "The.Signal.1999.2160p.ATVP.WEB-DL.DV.DDP5.1.Atmos.H.265-GRP",
			wantDV:       true,
			wantProfile:  5,
			wantCompatID: 0,
		},
		{
			name:         "1080p SDR carries no DV profile",
			release:      "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP",
			wantDV:       false,
			wantProfile:  0,
			wantCompatID: 0,
		},
	}

	title := matrixTitle()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := withParsed(rel("id", tc.release, 18000))
			info := estimateVariantInfo(title, r)
			if len(info.Streams) == 0 || info.Streams[0].Type != "video" {
				t.Fatalf("missing video stream: %+v", info)
			}
			v := info.Streams[0]
			if v.DolbyVision != tc.wantDV || v.DolbyVisionProfile != tc.wantProfile || v.DolbyVisionCompatibilityID != tc.wantCompatID {
				t.Fatalf("got dv=%v profile=%d compat=%d, want dv=%v profile=%d compat=%d (parsed=%+v)",
					v.DolbyVision, v.DolbyVisionProfile, v.DolbyVisionCompatibilityID,
					tc.wantDV, tc.wantProfile, tc.wantCompatID, r.Info)
			}
			if tc.wantCompatID > 0 && v.ColorTransfer != "smpte2084" {
				t.Fatalf("expected HDR10 base transfer smpte2084, got %q", v.ColorTransfer)
			}
			_ = core.MediaInfo(info)
		})
	}
}
