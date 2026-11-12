package onair

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

func (s *Service) Reel(ctx context.Context, id string) (Reel, error) {
	var b string
	var r Reel
	var anchor int64
	e := s.db.QueryRowContext(ctx, "SELECT data,updated_at FROM onair_reels WHERE id=?", id).Scan(&b, &anchor)
	if e == sql.ErrNoRows {
		return r, ErrNotFound
	}
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal([]byte(b), &r); e != nil {
		return r, e
	}
	// Shipped reels store updated_at=0 so schedules across any day include pods
	// without anchoring weeklySchedule to 1970-01-01.
	if anchor > 0 {
		r.Anchor = time.Unix(anchor, 0).UTC()
	}
	order := r.Clips
	r.Clips = []ReelClip{}
	rows, e := s.db.QueryContext(ctx, "SELECT data FROM onair_reel_clips WHERE reel_id=? ORDER BY created_at,id", id)
	if e != nil {
		return r, e
	}
	defer rows.Close()
	for rows.Next() {
		var c ReelClip
		if e = rows.Scan(&b); e != nil {
			return r, e
		}
		if e = json.Unmarshal([]byte(b), &c); e != nil {
			return r, e
		}
		r.Clips = append(r.Clips, c)
	}
	if e := rows.Err(); e != nil {
		return r, e
	}
	byID := map[string]ReelClip{}
	for _, c := range r.Clips {
		byID[c.ID] = c
	}
	sorted := []ReelClip{}
	for _, c := range order {
		if v, ok := byID[c.ID]; ok {
			sorted = append(sorted, v)
			delete(byID, c.ID)
		}
	}
	for _, c := range r.Clips {
		if _, ok := byID[c.ID]; ok {
			sorted = append(sorted, c)
			delete(byID, c.ID)
		}
	}
	r.Clips = sorted
	rows.Close()
	s.syncReelClipIndex(r)
	for i := range r.Clips {
		if r.Clips[i].Preview == "" {
			r.Clips[i].Preview = "/ether/api/onair/reels/" + r.ID + "/clips/" + r.Clips[i].ID + "/preview"
		}
		if r.Clips[i].Link {
			_, dur, ok := s.PeekCachedReelClip(r.ID, r.Clips[i].ID)
			r.Clips[i].Cached = ok
			if ok && dur > 0 && r.Clips[i].DurationMS <= 0 {
				r.Clips[i].DurationMS = dur.Milliseconds()
			}
		}
	}
	if s.o.ReelChannels != nil {
		usage, err := s.o.ReelChannels(ctx)
		if err != nil {
			return r, err
		}
		r.ChannelIDs = append([]string{}, usage[r.ID]...)
	}
	return s.importClips(ctx, r)
}
func (s *Service) Reels(ctx context.Context) ([]Reel, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM onair_reels ORDER BY id")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Reel{}
	for _, id := range ids {
		r, e := s.Reel(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Service) SaveReel(ctx context.Context, r Reel) (Reel, error) {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 120 || (r.Order != "shuffle" && r.Order != "sequence") || r.RepeatHours < 0 || r.RepeatHours > 8760 {
		return r, fmt.Errorf("invalid reel name, order or repeat window")
	}
	if r.ID == "" {
		r.ID = newID()
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []string{}
	}
	for i := range r.Clips {
		r.Clips[i] = ReelClip{ID: r.Clips[i].ID}
	}
	b, e := json.Marshal(r)
	if e != nil {
		return r, e
	}
	_, e = s.db.ExecContext(ctx, "INSERT INTO onair_reels(id,data,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at", r.ID, string(b), time.Now().Unix())
	if e != nil {
		return r, e
	}
	saved, e := s.Reel(ctx, r.ID)
	if e == nil {
		s.notifyReelsChanged(ctx)
	}
	return saved, e
}
func (s *Service) DeleteReel(ctx context.Context, id string) error {
	r, e := s.Reel(ctx, id)
	if e != nil {
		return e
	}
	s.cancelReelImports(ctx, id)
	for _, c := range r.Clips {
		if c.Link {
			s.cancelLinkClip(id, c)
		}
	}
	// assetMu before the write transaction, the order DeleteAsset uses.
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM onair_imports WHERE reel_id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM onair_reel_clips WHERE reel_id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM onair_reels WHERE id=?", id); e != nil {
		return e
	}
	digests := make([]string, 0, len(r.Clips))
	for _, c := range r.Clips {
		digests = append(digests, c.SourceDigest)
	}
	if e = s.dropUnusedReelSources(ctx, tx, digests); e != nil {
		return e
	}
	e = tx.Commit()
	if e == nil {
		s.removeUnreferenced(ctx, digests...)
		s.dropReelClipIndex(id)
		for _, c := range r.Clips {
			s.mu.Lock()
			delete(s.reelReady, c.ID)
			s.mu.Unlock()
			os.Remove(filepath.Join(s.o.Dir, "reel-"+c.ID+".mp4"))
		}
		s.notifyReelsChanged(ctx)
	}
	return e
}
func (s *Service) setClip(ctx context.Context, reel string, c ReelClip) error {
	stored := c
	stored.Cached = false
	b, e := json.Marshal(stored)
	if e != nil {
		return e
	}
	res, e := s.db.ExecContext(ctx, "UPDATE onair_reel_clips SET name=?,duration_ms=?,status=?,error=?,data=? WHERE id=? AND reel_id=?", stored.Name, stored.DurationMS, stored.Status, stored.Error, string(b), stored.ID, reel)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e == nil && n == 0 {
		return ErrNotFound
	}
	return e
}

// ClipUploadMetadata preserves provenance when a local file is imported into a reel.
type ClipUploadMetadata struct {
	Source      string
	Attribution string
}

func (s *Service) UploadClip(ctx context.Context, reel, name string, r io.Reader, metadata ...ClipUploadMetadata) (ReelClip, error) {
	if len(metadata) > 1 {
		return ReelClip{}, fmt.Errorf("one clip metadata record is allowed")
	}
	var m ClipUploadMetadata
	if len(metadata) == 1 {
		m = metadata[0]
	}
	return s.uploadClip(ctx, reel, name, m.Source, m.Attribution, r)
}
func (s *Service) uploadClip(ctx context.Context, reel, name, source, attribution string, r io.Reader, importID ...string) (ReelClip, error) {
	if _, e := s.Reel(ctx, reel); e != nil {
		return ReelClip{}, e
	}
	f, e := os.CreateTemp(s.o.Dir, ".upload-*")
	if e != nil {
		return ReelClip{}, e
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, (500<<20)+1))
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return ReelClip{}, e
	}
	if n == 0 || n > 500<<20 {
		return ReelClip{}, fmt.Errorf("clip maximum 500 MB")
	}
	a, e := s.putReelAsset(ctx, name, f.Name(), hex.EncodeToString(h.Sum(nil)), n)
	if e != nil {
		return ReelClip{}, e
	}
	c := ReelClip{Source: source, Attribution: attribution, ID: newID(), Name: name, SourceDigest: a.Digest, Status: "converting", Size: n}
	if len(importID) > 0 {
		c.ID = importID[0]
	}
	c.Preview = "/ether/api/onair/reels/" + reel + "/clips/" + c.ID + "/preview"
	data, _ := json.Marshal(c)
	_, e = s.db.ExecContext(ctx, "INSERT INTO onair_reel_clips(id,reel_id,name,duration_ms,source_digest,status,created_at,data) VALUES(?,?,?,0,?,'converting',?,?) ON CONFLICT(id) DO NOTHING", c.ID, reel, c.Name, a.Digest, time.Now().UnixNano(), string(data))
	if e != nil {
		return c, e
	}
	job := renderJob{Hash: "reel-" + c.ID, convert: func(ctx context.Context) error { return s.convertClip(ctx, reel, c) }}
	if !s.enqueue(job) {
		c.Status = "error"
		c.Error = "render queue full"
		s.setClip(ctx, reel, c)
		return c, fmt.Errorf("render queue full")
	}
	return c, nil
}
func (s *Service) convertClip(ctx context.Context, reel string, c ReelClip) (err error) {
	defer func() {
		if err != nil && ctx.Err() == nil {
			c.Status = "error"
			c.Error = err.Error()
			s.clipProgress(s.ctx, reel, c)
		}
	}()
	c.Status = "converting"
	c.Progress = 0
	s.clipProgress(ctx, reel, c)
	if s.o.Probe == nil || s.o.FFmpeg == nil {
		return fmt.Errorf("media conversion unavailable")
	}
	source, err := s.assetPath(ctx, c.SourceDigest)
	if err != nil {
		return err
	}
	info, err := s.o.Probe(ctx, source)
	if err != nil {
		return err
	}
	if info.Duration <= 0 || info.Duration > 3*time.Minute {
		return fmt.Errorf("reel clip maximum 3 minutes")
	}
	hasVideo, hasAudio := false, false
	for _, st := range info.Streams {
		hasVideo = hasVideo || st.Type == "video"
		hasAudio = hasAudio || st.Type == "audio"
	}
	if !hasVideo {
		return fmt.Errorf("clip has no video")
	}
	r, err := s.Reel(ctx, reel)
	if err != nil {
		return err
	}
	out := filepath.Join(s.o.Dir, "reel-"+c.ID+".mp4")
	dur, err := s.encodeNormalizedClip(ctx, r, source, out, info.Duration, hasAudio, func(pct float64) {
		c.Progress = pct
		s.clipProgress(ctx, reel, c)
	})
	if err != nil {
		return err
	}
	c.DurationMS = dur.Milliseconds()
	s.mu.Lock()
	s.reelReady[c.ID] = true
	s.mu.Unlock()
	c.Status = "ready"
	c.Progress = 100
	err = s.clipProgress(ctx, reel, c)
	if err == nil {
		s.notifyReelsChanged(ctx)
	}
	return err
}

func (s *Service) normalizeClipFile(ctx context.Context, r Reel, source, out string, progress func(float64)) (time.Duration, error) {
	if s.o.NormalizeClip != nil {
		return s.o.NormalizeClip(ctx, r, source, out)
	}
	if s.o.Probe == nil || s.o.FFmpeg == nil {
		return 0, fmt.Errorf("media conversion unavailable")
	}
	info, err := s.o.Probe(ctx, source)
	if err != nil {
		return 0, err
	}
	if info.Duration <= 0 || info.Duration > 3*time.Minute {
		return 0, fmt.Errorf("reel clip maximum 3 minutes")
	}
	hasVideo, hasAudio := false, false
	for _, st := range info.Streams {
		hasVideo = hasVideo || st.Type == "video"
		hasAudio = hasAudio || st.Type == "audio"
	}
	if !hasVideo {
		return 0, fmt.Errorf("clip has no video")
	}
	return s.encodeNormalizedClip(ctx, r, source, out, info.Duration, hasAudio, progress)
}

func (s *Service) encodeNormalizedClip(ctx context.Context, r Reel, source, out string, duration time.Duration, hasAudio bool, progress func(float64)) (dur time.Duration, err error) {
	trimStart := time.Duration(0)
	if r.TrimSilence && hasAudio {
		var trimEnd time.Duration
		trimStart, trimEnd, err = s.silenceEdges(ctx, source, duration)
		if err != nil {
			return 0, err
		}
		duration = trimEnd - trimStart
		if duration < time.Second/10 {
			return 0, fmt.Errorf("clip is silent")
		}
	}
	defer func() {
		if err != nil {
			os.Remove(out)
		}
	}()
	w, h := s.workingSize()
	if w < 2 || h < 2 || w > 1920 || h > 1080 || w%2 != 0 || h%2 != 0 {
		return 0, fmt.Errorf("invalid working size")
	}
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,format=yuv420p", w, h, w, h)
	tmp := out + ".tmp.mp4"
	defer os.Remove(tmp)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-ss", strconv.FormatFloat(trimStart.Seconds(), 'f', 6, 64), "-i", source}
	if !hasAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
	}
	args = append(args, "-map", "0:v:0")
	if hasAudio {
		args = append(args, "-map", "0:a:0")
	} else {
		args = append(args, "-map", "1:a:0", "-shortest")
	}
	args = append(args, "-t", strconv.FormatFloat(duration.Seconds(), 'f', 3, 64), "-vf", scale, "-r", "30", "-c:v", "libx264", "-threads", "2", "-profile:v", "high", "-level:v", "4.1", "-preset", "medium", "-crf", "20", "-maxrate", "12M", "-bufsize", "24M", "-g", "120", "-c:a", "aac", "-ac", "2", "-ar", "48000", "-b:a", "192k")
	if r.LoudnessMatch {
		args = append(args, "-af", "loudnorm=I=-23:TP=-2:LRA=7")
	}
	args = append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-movflags", "+faststart", tmp)
	args = append(args[:len(args)-1], "-progress", "pipe:1", tmp)
	cmd := mediaproc.CommandContext(ctx, mediaproc.EncodeTimeout, s.o.FFmpeg(), args...)
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		return 0, e
	}
	if err = cmd.Start(); err != nil {
		return 0, err
	}
	scan := bufio.NewScanner(pipe)
	scan.Buffer(make([]byte, 1024), 4096)
	last := time.Time{}
	for scan.Scan() {
		k, v, ok := strings.Cut(scan.Text(), "=")
		if !ok || k != "out_time_us" {
			continue
		}
		us, e := strconv.ParseFloat(v, 64)
		if e == nil && progress != nil && time.Since(last) > time.Second {
			progress(min(99, 100*us/float64(duration.Microseconds())))
			last = time.Now()
		}
	}
	if scan.Err() != nil {
		_ = pipe.Close()
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		}
		_ = cmd.Wait()
		return 0, scan.Err()
	}
	if err = cmd.Wait(); err != nil {
		return 0, err
	}
	if err = os.Rename(tmp, out); err != nil {
		return 0, err
	}
	return duration, nil
}

func (s *Service) workingSize() (int, int) {
	if s.o.WorkingSize != nil {
		return s.o.WorkingSize()
	}
	return 1920, 1080
}

var _ *core.MediaInfo

// resumeConversions repairs jobs interrupted by a process restart.
func (s *Service) resumeConversions() {
	rs, e := s.Reels(s.ctx)
	if e != nil {
		return
	}
	for _, r := range rs {
		for _, c := range r.Clips {
			if c.Link {
				continue
			}
			if c.Status == "converting" {
				s.enqueue(renderJob{Hash: "reel-" + c.ID, convert: func(ctx context.Context) error { return s.convertClip(ctx, r.ID, c) }})
			}
		}
	}
}

func (s *Service) clipProgress(ctx context.Context, reel string, c ReelClip) error {
	if e := s.db.QueryRowContext(ctx, "SELECT name FROM onair_reel_clips WHERE id=? AND reel_id=?", c.ID, reel).Scan(&c.Name); e != nil {
		return e
	}
	return s.setClip(ctx, reel, c)
}

// silenceEdges trims only edge silence, preserving video/audio alignment and
// leaving pauses inside a clip intact.
func (s *Service) silenceEdges(ctx context.Context, path string, duration time.Duration) (time.Duration, time.Duration, error) {
	cmd := mediaproc.CommandContext(ctx, mediaproc.EncodeTimeout, s.o.FFmpeg(), "-nostdin", "-hide_banner", "-i", path, "-vn", "-af", "silencedetect=noise=-45dB:d=0.15", "-f", "null", "-")
	var log bytes.Buffer
	cmd.Stderr = &log
	if e := cmd.Run(); e != nil {
		return 0, 0, e
	}
	re := regexp.MustCompile(`silence_(start|end): ([0-9.]+)`)
	start, end := time.Duration(0), duration
	lastStart, lastEnd := time.Duration(-1), time.Duration(-1)
	for _, m := range re.FindAllStringSubmatch(log.String(), -1) {
		v, e := strconv.ParseFloat(m[2], 64)
		if e != nil {
			continue
		}
		t := time.Duration(v * float64(time.Second))
		if m[1] == "start" {
			lastStart = t
		} else {
			lastEnd = t
			if lastStart >= 0 && lastStart < 50*time.Millisecond {
				start = t
			}
			if t >= duration-50*time.Millisecond {
				end = lastStart
			}
		}
	}
	if lastStart > lastEnd {
		end = lastStart
	}
	return start, end, nil
}

func (s *Service) DeleteClip(ctx context.Context, reel, id string) error {
	s.cancelImport(id)
	res, e := s.db.ExecContext(ctx, "DELETE FROM onair_imports WHERE id=? AND reel_id=?", id, reel)
	if e != nil {
		return e
	}
	removed, _ := res.RowsAffected()
	r, e := s.Reel(ctx, reel)
	if e != nil {
		return e
	}
	for _, c := range r.Clips {
		if c.ID == id {
			if c.Link {
				s.cancelLinkClip(reel, c)
			}
			e = s.deleteClipRow(ctx, reel, c)
			if e == nil {
				s.mu.Lock()
				delete(s.reelReady, c.ID)
				delete(s.reelClipIndex, reel+"\x00"+c.ID)
				s.mu.Unlock()
				os.Remove(filepath.Join(s.o.Dir, "reel-"+c.ID+".mp4"))
				s.notifyReelsChanged(ctx)
			}
			return e
		}
	}
	if removed > 0 {
		return nil
	}
	return ErrNotFound
}

// deleteClipRow deletes one clip and, in the same transaction, its source
// upload once nothing else uses it.
func (s *Service) deleteClipRow(ctx context.Context, reel string, c ReelClip) error {
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM onair_reel_clips WHERE reel_id=? AND id=?", reel, c.ID); e != nil {
		return e
	}
	if e = s.dropUnusedReelSources(ctx, tx, []string{c.SourceDigest}); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.removeUnreferenced(ctx, c.SourceDigest)
	return nil
}

// dropUnusedReelSources deletes the reel source uploads among digests that
// no clip, look, family or channel uses any more, under the same rules as
// DeleteAsset. Without it a deleted reel's sources stayed in the database
// and kept counting against the reel budget. The caller holds assetMu,
// taken before its transaction.
func (s *Service) dropUnusedReelSources(ctx context.Context, tx *sql.Tx, digests []string) error {
	channels, e := s.hasChannelAssets(ctx)
	if e != nil {
		return e
	}
	query := `DELETE FROM onair_assets WHERE digest=? AND kind='reel' AND NOT EXISTS(SELECT 1 FROM onair_reel_clips WHERE source_digest=?) AND NOT EXISTS(SELECT 1 FROM onair_looks WHERE data LIKE ?) AND NOT EXISTS(SELECT 1 FROM onair_families WHERE data LIKE ?)`
	if channels {
		query += ` AND NOT EXISTS(SELECT 1 FROM livetv_channels WHERE data LIKE ?)`
	}
	seen := map[string]bool{}
	for _, d := range digests {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		like := "%" + d + "%"
		args := []any{d, d, like, like}
		if channels {
			args = append(args, like)
		}
		res, e := tx.ExecContext(ctx, query, args...)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if _, e = tx.ExecContext(ctx, "DELETE FROM onair_asset_replicas WHERE digest=?", d); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Service) loadReels() {
	rs, e := s.Reels(s.ctx)
	if e != nil {
		return
	}
	for _, r := range rs {
		for _, c := range r.Clips {
			if c.Link {
				if c.Status == "downloading" || c.Status == "converting" || c.Status == "queued" {
					c.Status = "ready"
					c.Progress = 0
					_ = s.setClip(s.ctx, r.ID, c)
				}
				continue
			}
			if c.Status == "ready" {
				if st, e := os.Stat(filepath.Join(s.o.Dir, "reel-"+c.ID+".mp4")); e == nil && st.Size() > 0 {
					s.mu.Lock()
					s.reelReady[c.ID] = true
					s.mu.Unlock()
				}
			}
		}
	}
}
