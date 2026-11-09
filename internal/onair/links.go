package onair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const defaultLinkCacheBytes = 2 << 30

// LinkClipInput describes a dynamic link-backed reel clip stored without media bytes.
type LinkClipInput struct {
	ID          string
	Name        string
	Source      string
	Attribution string
	DurationMS  int64
	Tags        []string
}

type linkEntry struct {
	Path        string
	Size        int64
	Duration    time.Duration
	LastAired   time.Time
	Pin         time.Time
	Title       string
	Attribution string
}

type linkCacheMeta struct {
	DurationMS    int64  `json:"durationMs"`
	LastAiredUnix int64  `json:"lastAiredUnix"`
	Title         string `json:"title,omitempty"`
	Attribution   string `json:"attribution,omitempty"`
}

type reelClipMeta struct {
	Source        string
	Link          bool
	LoudnessMatch bool
	TrimSilence   bool
	DurationMS    int64
}

func linkAssetDigest(source string) string {
	sum := sha256.Sum256([]byte("link:" + source))
	return hex.EncodeToString(sum[:])
}

func linkCacheKey(source string, w, h int, loudness, trim bool) string {
	raw := fmt.Sprintf("linkclip:v1\x00%s\x00%dx%d:%t:%t", source, w, h, loudness, trim)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) now() time.Time {
	if s.o.Now != nil {
		return s.o.Now()
	}
	return time.Now()
}

func (s *Service) linkDir() string {
	return filepath.Join(s.o.Dir, "links")
}

func (s *Service) linkKeyForMeta(m reelClipMeta) string {
	w, h := s.workingSize()
	return linkCacheKey(m.Source, w, h, m.LoudnessMatch, m.TrimSilence)
}

func (s *Service) notifyReelsChanged(ctx context.Context) {
	if s.o.ReelsChanged != nil {
		s.o.ReelsChanged(ctx)
	}
}

func (s *Service) syncReelClipIndex(r Reel) {
	prefix := r.ID + "\x00"
	s.mu.Lock()
	if s.reelClipIndex == nil {
		s.reelClipIndex = map[string]reelClipMeta{}
	}
	for k := range s.reelClipIndex {
		if strings.HasPrefix(k, prefix) {
			delete(s.reelClipIndex, k)
		}
	}
	for _, c := range r.Clips {
		s.reelClipIndex[prefix+c.ID] = reelClipMeta{
			Source:        c.Source,
			Link:          c.Link,
			LoudnessMatch: r.LoudnessMatch,
			TrimSilence:   r.TrimSilence,
			DurationMS:    c.DurationMS,
		}
	}
	s.mu.Unlock()
}

func (s *Service) dropReelClipIndex(reelID string) {
	prefix := reelID + "\x00"
	s.mu.Lock()
	for k := range s.reelClipIndex {
		if strings.HasPrefix(k, prefix) {
			delete(s.reelClipIndex, k)
		}
	}
	s.mu.Unlock()
}

func (s *Service) lookupReelClipMeta(reelID, clipID string) (reelClipMeta, bool) {
	key := reelID + "\x00" + clipID
	s.mu.Lock()
	m, ok := s.reelClipIndex[key]
	s.mu.Unlock()
	if ok {
		return m, true
	}
	if _, err := s.Reel(s.ctx, reelID); err != nil {
		return reelClipMeta{}, false
	}
	s.mu.Lock()
	m, ok = s.reelClipIndex[key]
	s.mu.Unlock()
	return m, ok
}

// AddLinkClip stores a link-backed clip on reelID without downloading media.
func (s *Service) AddLinkClip(ctx context.Context, reelID string, in LinkClipInput) (ReelClip, error) {
	r, err := s.Reel(ctx, reelID)
	if err != nil {
		return ReelClip{}, err
	}
	raw := strings.TrimSpace(in.Source)
	u, err := url.Parse(raw)
	if len(raw) == 0 || len(raw) > 8192 || err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ReelClip{}, fmt.Errorf("invalid HTTP media link")
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 255 {
		return ReelClip{}, fmt.Errorf("name maximum 255 characters")
	}
	autoName := false
	if name == "" {
		name = defaultImportName(u)
		autoName = true
	}
	if in.DurationMS < 0 || in.DurationMS > int64((3*time.Minute)/time.Millisecond) {
		return ReelClip{}, fmt.Errorf("reel clip maximum 3 minutes")
	}
	var tags []string
	for _, tag := range in.Tags {
		if t := strings.TrimSpace(tag); t != "" {
			tags = append(tags, t)
		}
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = newID()
	}
	srcDigest := linkAssetDigest(raw)
	c := ReelClip{
		Source:       raw,
		Attribution:  strings.TrimSpace(in.Attribution),
		ID:           id,
		Name:         name,
		DurationMS:   in.DurationMS,
		SourceDigest: srcDigest,
		Status:       "ready",
		Preview:      "/ether/api/onair/reels/" + reelID + "/clips/" + id + "/preview",
		Link:         true,
		Tags:         tags,
		AutoName:     autoName,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return ReelClip{}, err
	}

	s.assetMu.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.assetMu.Unlock()
		return ReelClip{}, err
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO onair_assets(digest,kind,name,mime,size,license,content,created_at,source,attribution,duration_ms,stored) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(digest) DO NOTHING",
		srcDigest, "reel", name, "text/uri-list", int64(0), strings.Join(tags, ","), []byte{}, s.now().Unix(), raw, c.Attribution, c.DurationMS, "link",
	)
	if err == nil {
		_, err = tx.ExecContext(ctx,
			"INSERT INTO onair_reel_clips(id,reel_id,name,duration_ms,source_digest,status,created_at,data) VALUES(?,?,?,?,?,'ready',?,?)",
			c.ID, reelID, c.Name, c.DurationMS, srcDigest, s.now().UnixNano(), string(data),
		)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	s.assetMu.Unlock()
	if err != nil {
		return ReelClip{}, err
	}

	s.mu.Lock()
	if s.reelClipIndex == nil {
		s.reelClipIndex = map[string]reelClipMeta{}
	}
	s.reelClipIndex[reelID+"\x00"+c.ID] = reelClipMeta{
		Source:        c.Source,
		Link:          true,
		LoudnessMatch: r.LoudnessMatch,
		TrimSilence:   r.TrimSilence,
		DurationMS:    c.DurationMS,
	}
	s.mu.Unlock()
	_, _, c.Cached = s.PeekCachedReelClip(reelID, c.ID)
	s.notifyReelsChanged(ctx)
	return c, nil
}

// PeekCachedReelClip checks the in-memory link cache without touching LastAired,
// the database, disk, or network.
func (s *Service) PeekCachedReelClip(reelID, clipID string) (string, time.Duration, bool) {
	if s == nil || reelID == "" || clipID == "" {
		return "", 0, false
	}
	w, h := s.workingSize()
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.reelClipIndex[reelID+"\x00"+clipID]
	if !ok || !m.Link || m.Source == "" {
		return "", 0, false
	}
	key := linkCacheKey(m.Source, w, h, m.LoudnessMatch, m.TrimSilence)
	ent, ok := s.linkCache[key]
	if !ok {
		return "", 0, false
	}
	return ent.Path, ent.Duration, true
}

// CachedReelClip returns a resolved link clip's local normalized file path from
// the in-memory LRU index and records its airing time. It never blocks or resolves.
func (s *Service) CachedReelClip(reelID, clipID string) (string, bool) {
	if s == nil || reelID == "" || clipID == "" {
		return "", false
	}
	w, h := s.workingSize()
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.reelClipIndex[reelID+"\x00"+clipID]
	if !ok || !m.Link || m.Source == "" {
		return "", false
	}
	key := linkCacheKey(m.Source, w, h, m.LoudnessMatch, m.TrimSilence)
	ent, ok := s.linkCache[key]
	if !ok {
		return "", false
	}
	ent.LastAired = now
	s.linkCache[key] = ent
	return ent.Path, true
}

// ResolveReelClip triggers an on-demand background resolution for console preview.
func (s *Service) ResolveReelClip(ctx context.Context, reelID, clipID string) (ReelClip, error) {
	r, err := s.Reel(ctx, reelID)
	if err != nil {
		return ReelClip{}, err
	}
	var found *ReelClip
	for i := range r.Clips {
		if r.Clips[i].ID == clipID {
			found = &r.Clips[i]
			break
		}
	}
	if found == nil {
		return ReelClip{}, ErrNotFound
	}
	c := *found
	if !c.Link {
		return c, nil
	}
	w, h := s.workingSize()
	key := linkCacheKey(c.Source, w, h, r.LoudnessMatch, r.TrimSilence)
	now := s.now()
	s.mu.Lock()
	if ent, ok := s.linkCache[key]; ok {
		ent.LastAired = now
		s.linkCache[key] = ent
		s.mu.Unlock()
		c.Cached = true
		c.Status = "ready"
		c.Error = ""
		if ent.Duration > 0 && c.DurationMS <= 0 {
			c.DurationMS = ent.Duration.Milliseconds()
		}
		return c, nil
	}
	delete(s.failed, "link-"+key)
	if s.manualLink == nil {
		s.manualLink = map[string]bool{}
	}
	s.manualLink[key] = true
	alreadyPending := s.pending["link-"+key]
	s.mu.Unlock()

	c.Status = "downloading"
	c.Error = ""
	c.Progress = 0
	_ = s.setClip(ctx, reelID, c)
	if alreadyPending {
		return c, nil
	}
	job := renderJob{
		download: true,
		Hash:     "link-" + key,
		prepare: func(jctx context.Context) error {
			return s.runLinkJob(jctx, key, reelID, clipID, true)
		},
	}
	if !s.enqueue(job) {
		c.Status = "error"
		c.Error = "import queue full"
		_ = s.setClip(ctx, reelID, c)
		return c, fmt.Errorf("import queue full")
	}
	return c, nil
}

func (s *Service) ensureReelClip(r core.ClipRequest) {
	m, ok := s.lookupReelClipMeta(r.ReelID, r.ReelClipID)
	if !ok || !m.Link || m.Source == "" {
		return
	}
	key := s.linkKeyForMeta(m)
	now := s.now()
	s.mu.Lock()
	if r.ChannelID != "" {
		delete(s.unwanted, r.ChannelID)
		if s.linkChannels == nil {
			s.linkChannels = map[string]map[string]bool{}
		}
		if s.linkChannels[key] == nil {
			s.linkChannels[key] = map[string]bool{}
		}
		s.linkChannels[key][r.ChannelID] = true
	}
	if ent, cached := s.linkCache[key]; cached {
		ent.Pin = now.Add(3 * time.Hour)
		s.linkCache[key] = ent
		s.mu.Unlock()
		return
	}
	if s.pending["link-"+key] {
		s.mu.Unlock()
		return
	}
	if f, backedOff := s.failed["link-"+key]; backedOff && now.Before(f.retry) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.enqueue(renderJob{
		download: true,
		Boundary: r.Boundary,
		Hash:     "link-" + key,
		Request:  r,
		prepare: func(ctx context.Context) error {
			return s.runLinkJob(ctx, key, r.ReelID, r.ReelClipID, false)
		},
	})
}

func (s *Service) cancelChannelLinksLocked(channelID string) []context.CancelFunc {
	var cancels []context.CancelFunc
	for key, chs := range s.linkChannels {
		delete(chs, channelID)
		if len(chs) > 0 {
			continue
		}
		delete(s.linkChannels, key)
		if ent, ok := s.linkCache[key]; ok {
			ent.Pin = time.Time{}
			s.linkCache[key] = ent
		}
		if s.manualLink[key] {
			continue
		}
		delete(s.pending, "link-"+key)
		if fn := s.linkCancels[key]; fn != nil {
			cancels = append(cancels, fn)
		}
	}
	return cancels
}

func (s *Service) cancelLinkClip(reelID string, c ReelClip) {
	m, ok := s.lookupReelClipMeta(reelID, c.ID)
	if !ok || m.Source == "" {
		return
	}
	key := s.linkKeyForMeta(m)
	s.mu.Lock()
	cancel := s.linkCancels[key]
	delete(s.linkCancels, key)
	delete(s.manualLink, key)
	delete(s.linkChannels, key)
	delete(s.pending, "link-"+key)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) runLinkJob(parent context.Context, key, reelID, clipID string, manual bool) error {
	now := s.now()
	s.mu.Lock()
	if ent, ok := s.linkCache[key]; ok {
		if len(s.linkChannels[key]) > 0 {
			ent.Pin = now.Add(3 * time.Hour)
			s.linkCache[key] = ent
		}
		delete(s.manualLink, key)
		s.mu.Unlock()
		return nil
	}
	if !manual && !s.manualLink[key] && len(s.linkChannels[key]) == 0 {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	if s.linkCancels == nil {
		s.linkCancels = map[string]context.CancelFunc{}
	}
	s.linkCancels[key] = cancel
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.linkCancels, key)
		delete(s.manualLink, key)
		s.mu.Unlock()
		cancel()
	}()

	r, err := s.Reel(ctx, reelID)
	if err != nil {
		return nil
	}
	var clip *ReelClip
	for i := range r.Clips {
		if r.Clips[i].ID == clipID {
			clip = &r.Clips[i]
			break
		}
	}
	if clip == nil || !clip.Link || clip.Source == "" {
		return nil
	}
	c := *clip
	c.Status = "downloading"
	c.Error = ""
	c.Progress = 0
	_ = s.clipProgress(ctx, reelID, c)

	recordFail := func(e error) {
		if errors.Is(e, context.Canceled) && (parent.Err() != nil || s.linkAbandoned(key)) {
			c.Status = "ready"
			c.Progress = 0
			_ = s.clipProgress(s.ctx, reelID, c)
			return
		}
		s.mu.Lock()
		if s.failed == nil {
			s.failed = map[string]failure{}
		}
		f := s.failed["link-"+key]
		f.delay = min(max(2*f.delay, 5*time.Second), 5*time.Minute)
		f.retry = s.now().Add(f.delay)
		s.failed["link-"+key] = f
		s.mu.Unlock()

		s.linkFailLog.Fail(e, "reel", reelID, "clip", clipID, "source", c.Source)
		c.Status = "error"
		c.Error = e.Error()
		c.Progress = 0
		_ = s.clipProgress(s.ctx, reelID, c)
	}

	dl, err := s.downloadLinkMedia(ctx, c.Source, func(pct float64) {
		c.Progress = min(49, pct*0.5)
		_ = s.clipProgress(ctx, reelID, c)
	})
	if err != nil {
		recordFail(err)
		return nil
	}
	defer dl.cleanup()

	if err := os.MkdirAll(s.linkDir(), 0o750); err != nil {
		recordFail(err)
		return nil
	}
	c.Status = "converting"
	c.Progress = 50
	_ = s.clipProgress(ctx, reelID, c)

	convCtx := ctx
	release := func() {}
	if s.o.Acquire != nil {
		var acqErr error
		convCtx, release, acqErr = s.o.Acquire(ctx)
		if acqErr != nil {
			recordFail(acqErr)
			return nil
		}
	}
	outPath := filepath.Join(s.linkDir(), key+".mp4")
	dur, err := s.normalizeClipFile(convCtx, r, dl.path, outPath, func(pct float64) {
		c.Progress = 50 + min(49, pct*0.5)
		_ = s.clipProgress(ctx, reelID, c)
	})
	release()
	if err != nil {
		recordFail(err)
		return nil
	}
	st, err := os.Stat(outPath)
	if err != nil || st.Size() <= 0 {
		_ = os.Remove(outPath)
		if err == nil {
			err = fmt.Errorf("normalized link clip is empty")
		}
		recordFail(err)
		return nil
	}

	airedAt := s.now()
	metaBytes, _ := json.Marshal(linkCacheMeta{
		DurationMS:    dur.Milliseconds(),
		LastAiredUnix: airedAt.Unix(),
		Title:         dl.title,
		Attribution:   dl.attribution,
	})
	_ = os.WriteFile(filepath.Join(s.linkDir(), key+".json"), metaBytes, 0o640)

	s.mu.Lock()
	delete(s.failed, "link-"+key)
	pin := time.Time{}
	if len(s.linkChannels[key]) > 0 {
		pin = airedAt.Add(3 * time.Hour)
	}
	s.linkCache[key] = linkEntry{
		Path:        outPath,
		Size:        st.Size(),
		Duration:    dur,
		LastAired:   airedAt,
		Pin:         pin,
		Title:       dl.title,
		Attribution: dl.attribution,
	}
	s.evictLinksLocked(key)
	s.mu.Unlock()

	s.linkFailLog.OK()

	if c.AutoName && dl.title != "" {
		c.Name = dl.title
		c.AutoName = false
	} else {
		_ = s.db.QueryRowContext(s.ctx, "SELECT name FROM onair_reel_clips WHERE id=? AND reel_id=?", c.ID, reelID).Scan(&c.Name)
	}
	if c.Attribution == "" && dl.attribution != "" {
		c.Attribution = dl.attribution
	}
	if c.DurationMS <= 0 {
		c.DurationMS = dur.Milliseconds()
	}
	c.Status = "ready"
	c.Error = ""
	c.Progress = 100
	if err := s.setClip(s.ctx, reelID, c); err == nil {
		s.mu.Lock()
		if m, ok := s.reelClipIndex[reelID+"\x00"+c.ID]; ok {
			m.DurationMS = c.DurationMS
			s.reelClipIndex[reelID+"\x00"+c.ID] = m
		}
		s.mu.Unlock()
		s.notifyReelsChanged(s.ctx)
	}
	return nil
}

func (s *Service) linkAbandoned(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.manualLink[key] && len(s.linkChannels[key]) == 0
}

func (s *Service) loadLinkCache() {
	dir := s.linkDir()
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, v := range es {
		if !strings.HasSuffix(v.Name(), ".mp4") {
			continue
		}
		key := strings.TrimSuffix(v.Name(), ".mp4")
		if len(key) != 64 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, key+".json"))
		var meta linkCacheMeta
		if err != nil || json.Unmarshal(b, &meta) != nil || meta.DurationMS <= 0 {
			continue
		}
		st, err := v.Info()
		if err != nil || st.Size() <= 0 {
			continue
		}
		lastAired := st.ModTime()
		if meta.LastAiredUnix > 0 {
			lastAired = time.Unix(meta.LastAiredUnix, 0)
		}
		s.linkCache[key] = linkEntry{
			Path:        filepath.Join(dir, v.Name()),
			Size:        st.Size(),
			Duration:    time.Duration(meta.DurationMS) * time.Millisecond,
			LastAired:   lastAired,
			Title:       meta.Title,
			Attribution: meta.Attribution,
		}
	}
	s.mu.Lock()
	s.evictLinksLocked("")
	s.mu.Unlock()
}

func (s *Service) evictLinksLocked(keepKey string) {
	now := s.now()
	var total int64
	var unpinned []string
	var pinned []string
	for k, ent := range s.linkCache {
		if !ent.Pin.IsZero() && ent.Pin.Before(now) {
			ent.Pin = time.Time{}
			s.linkCache[k] = ent
		}
		total += ent.Size
		if k == keepKey {
			continue
		}
		if ent.Pin.IsZero() {
			unpinned = append(unpinned, k)
		} else {
			pinned = append(pinned, k)
		}
	}
	if total <= s.o.LinkCacheBytes {
		return
	}
	byOldest := func(keys []string) {
		sort.Slice(keys, func(i, j int) bool {
			return s.linkCache[keys[i]].LastAired.Before(s.linkCache[keys[j]].LastAired)
		})
	}
	drop := func(keys []string) {
		byOldest(keys)
		for _, k := range keys {
			if total <= s.o.LinkCacheBytes {
				return
			}
			ent := s.linkCache[k]
			if err := os.Remove(ent.Path); err == nil || errors.Is(err, os.ErrNotExist) {
				_ = os.Remove(filepath.Join(s.linkDir(), k+".json"))
				delete(s.linkCache, k)
				total -= ent.Size
			}
		}
	}
	drop(unpinned)
	if total > s.o.LinkCacheBytes {
		drop(pinned)
	}
}
