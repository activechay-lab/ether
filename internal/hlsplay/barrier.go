package hlsplay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type SegmentBarrier struct {
	Session           string    `json:"session"`
	Index             int       `json:"index"`
	LastReceivedIndex int       `json:"lastReceivedIndex"`
	PlaylistSHA256    string    `json:"playlistSHA256"`
	ReadyAt           time.Time `json:"readyAt"`
	ReleasedAt        time.Time `json:"releasedAt,omitempty"`
}

// A request barrier controls only when the next HTTP request begins. It never
// freezes the playback clock, changes the forward-buffer target, or publishes
// segment N before the caller proves the owning execution was stopped.
func (p *player) waitForSegment(ctx context.Context, b *Buffer, r *Report, index int) error {
	if p.c.SegmentBarrierDir == "" || index != p.c.PauseBeforeIndex || r.Barrier != nil {
		return nil
	}
	if len(r.Segments) != index {
		return fmt.Errorf("barrier requires contiguous prefix ending at N-1")
	}
	for i, segment := range r.Segments {
		if segment.Index != i {
			return fmt.Errorf("barrier requires contiguous prefix starting at zero")
		}
	}
	if r.InitialPlaylistSHA256 == "" || len(r.InitialTimeline) <= index+1 {
		return fmt.Errorf("barrier requires the initial authored timeline")
	}
	if err := os.Mkdir(p.c.SegmentBarrierDir, 0700); err != nil {
		return fmt.Errorf("fresh barrier directory required: %w", err)
	}
	session := p.playSession
	if session == "" {
		session = r.Session
	}
	r.Barrier = &SegmentBarrier{Session: session, Index: index, LastReceivedIndex: index - 1, PlaylistSHA256: r.InitialPlaylistSHA256, ReadyAt: time.Now().UTC()}
	data, err := json.Marshal(r.Barrier)
	if err != nil {
		return err
	}
	tmp := filepath.Join(p.c.SegmentBarrierDir, "ready.tmp")
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(p.c.SegmentBarrierDir, "ready.json")); err != nil {
		return err
	}
	for {
		now := p.elapsed()
		b.Advance(now)
		sample := Sample{now, b.Level, b.Played}
		r.Buffer = append(r.Buffer, sample)
		p.event(sample)
		data, err := os.ReadFile(filepath.Join(p.c.SegmentBarrierDir, "release.json"))
		if err == nil {
			var release struct {
				Session        string `json:"session"`
				Index          int    `json:"index"`
				FaultVerified  bool   `json:"faultVerified"`
				PlaylistSHA256 string `json:"playlistSHA256"`
				ProofSHA256    string `json:"proofSHA256"`
			}
			if json.Unmarshal(data, &release) != nil || release.Session != session || release.Index != index || !release.FaultVerified || release.PlaylistSHA256 != r.InitialPlaylistSHA256 || len(release.ProofSHA256) != 64 {
				return fmt.Errorf("barrier release binding or fault proof differs")
			}
			proof, err := os.ReadFile(filepath.Join(p.c.SegmentBarrierDir, "fault-proof.json"))
			if err != nil {
				return fmt.Errorf("barrier task-absence evidence unavailable: %w", err)
			}
			hash := sha256.Sum256(proof)
			if hex.EncodeToString(hash[:]) != release.ProofSHA256 {
				return fmt.Errorf("barrier task-absence evidence digest differs")
			}
			r.Barrier.ReleasedAt = time.Now().UTC()
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		if !sleep(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
}
