package onair

import (
	"context"
	"fmt"
	"os"
	"time"
)

// ImportChannelCommercials is called only from an approved channel proposal.
// Each download is sequential and bounded by the existing shared downloader.
func (s *Service) ImportChannelCommercials(ctx context.Context, name string, links []string) (Reel, error) {
	if len(links) == 0 || len(links) > 30 {
		return Reel{}, fmt.Errorf("commercial reel requires 1 to 30 clips")
	}
	r, err := s.SaveReel(ctx, Reel{Name: name, Order: "shuffle", LoudnessMatch: true})
	if err != nil {
		return r, err
	}
	client, closeClient := s.importHTTPClient()
	defer closeClient()
	for _, link := range links {
		clipCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		dl, e := s.downloadViaExtractor(clipCtx, client, link, "video", true, 64<<20, 2*time.Minute, nil)
		if e == nil {
			if s.o.Probe == nil {
				e = fmt.Errorf("media probe unavailable")
			} else {
				info, err := s.o.Probe(clipCtx, dl.path)
				if err != nil {
					e = err
				} else if info.Duration <= 0 || info.Duration > 2*time.Minute {
					e = fmt.Errorf("commercial clips must be at most two minutes")
				}
			}
			var f *os.File
			if e == nil {
				f, e = os.Open(dl.path)
			}
			if e == nil {
				_, e = s.UploadClip(clipCtx, r.ID, dl.title, f, ClipUploadMetadata{Source: link, Attribution: dl.attribution})
				f.Close()
			}
			dl.cleanup()
		}
		cancel()
		if e != nil {
			return r, fmt.Errorf("commercial import stopped (reel %s): %w", r.ID, e)
		}
	}
	return s.Reel(ctx, r.ID)
}
