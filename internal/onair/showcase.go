package onair

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

//go:embed showcase_reels.json
var showcaseReelsData []byte

// showcaseLook is a decade look shipped with a Live TV showcase channel. The
// channel refers to it by ID. Each look is added once; a later version
// replaces it only while the owner has never edited it, and a deleted look
// stays deleted.
type showcaseLook struct {
	id, name, family    string
	bg, accent, text    string
	glow, display, body string
	tagline             string
	textures            []string
	controls            map[string]any
	version             int
}

type shippedShowcaseReel struct {
	ID            string                `json:"id"`
	Version       int                   `json:"version"`
	Era           string                `json:"era"`
	Name          string                `json:"name"`
	Order         string                `json:"order"`
	RepeatHours   float64               `json:"repeatHours"`
	LoudnessMatch bool                  `json:"loudnessMatch"`
	TrimSilence   bool                  `json:"trimSilence"`
	Clips         []shippedShowcaseClip `json:"clips"`
}

type shippedShowcaseClip struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	DurationMS  int64    `json:"durationMs"`
	Attribution string   `json:"attribution,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func shippedShowcaseReels() ([]shippedShowcaseReel, error) {
	var reels []shippedShowcaseReel
	if err := json.Unmarshal(showcaseReelsData, &reels); err != nil {
		return nil, fmt.Errorf("decode showcase_reels.json: %w", err)
	}
	return reels, nil
}

func (sr shippedShowcaseReel) reel() Reel {
	clips := make([]ReelClip, 0, len(sr.Clips))
	for _, sc := range sr.Clips {
		clips = append(clips, ReelClip{
			ID:          sc.ID,
			Name:        sc.Name,
			Source:      sc.Source,
			DurationMS:  sc.DurationMS,
			Attribution: sc.Attribution,
			Link:        true,
			Tags:        append([]string(nil), sc.Tags...),
		})
	}
	return Reel{
		ID:            sr.ID,
		Name:          sr.Name,
		Order:         sr.Order,
		RepeatHours:   sr.RepeatHours,
		LoudnessMatch: sr.LoudnessMatch,
		TrimSilence:   sr.TrimSilence,
		Clips:         clips,
		ChannelIDs:    []string{},
	}
}

func showcaseReelSignature(r Reel) string {
	type clipSig struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
		Link   bool   `json:"link"`
	}
	sig := struct {
		ID            string    `json:"id"`
		Name          string    `json:"name"`
		Order         string    `json:"order"`
		RepeatHours   float64   `json:"repeatHours"`
		LoudnessMatch bool      `json:"loudnessMatch"`
		TrimSilence   bool      `json:"trimSilence"`
		Clips         []clipSig `json:"clips"`
	}{
		ID:            r.ID,
		Name:          r.Name,
		Order:         r.Order,
		RepeatHours:   r.RepeatHours,
		LoudnessMatch: r.LoudnessMatch,
		TrimSilence:   r.TrimSilence,
		Clips:         make([]clipSig, 0, len(r.Clips)),
	}
	for _, c := range r.Clips {
		sig.Clips = append(sig.Clips, clipSig{
			ID:     c.ID,
			Name:   c.Name,
			Source: c.Source,
			Link:   c.Link,
		})
	}
	b, _ := json.Marshal(sig)
	return digest(string(b))
}

func showcaseLooks() []showcaseLook {
	return []showcaseLook{
		{id: "showcase-80", name: "CHROMA 80", family: "retro", bg: "#0a0b2e", accent: "#ff3d6e", text: "#ffffff", glow: "#33d6ff", display: "archivoblack", body: "inter", tagline: "Something special every night", textures: []string{"grain", "vignette"}, version: 1},
		{id: "showcase-90", name: "RIOT 90", family: "grunge", bg: "#141021", accent: "#edff38", text: "#ffffff", glow: "#ed4c99", display: "archivoblack", body: "specialelite", tagline: "Television with a pulse", textures: []string{"grain"}, version: 1},
		{id: "showcase-00", name: "ORBIT 00", family: "aqua", bg: "#031d37", accent: "#009edb", text: "#ffffff", glow: "#8cf5ff", display: "spacegrotesk", body: "inter", tagline: "Your world, in orbit.", textures: []string{"grain", "vignette"}, version: 1},
		{id: "showcase-10", name: "SHIFT 10", family: "flat", bg: "#102a35", accent: "#f15440", text: "#ffffff", glow: "#ffd66e", display: "archivoblack", body: "inter", tagline: "Good stories. Every day.", version: 1},
	}
}

func (v showcaseLook) look() Look {
	l := familyLook(v.family, v.name)
	l.ID = v.id
	l.Brand.Palette.Background = v.bg
	l.Brand.Palette.Accent = v.accent
	l.Brand.Palette.Text = v.text
	l.Brand.Palette.Glow = v.glow
	l.Brand.Type.Display = v.display
	l.Brand.Type.Body = v.body
	l.Brand.Tagline = v.tagline
	l.Brand.Energy = .5
	l.Brand.Language = "en"
	l.Brand.Textures = append([]string{}, v.textures...)
	for i, c := range l.Controls {
		if value, ok := v.controls[c.ID]; ok {
			l.Controls[i].Value = value
		}
	}
	// A translucent wordmark bug in the channel's own colour.
	opacity := .4
	l.Bug = &core.BugSettings{Source: "wordmark", Style: "original", Opacity: &opacity}
	return l
}

func digest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func (s *Service) seedShowcase(ctx context.Context) error {
	for _, v := range showcaseLooks() {
		data, err := json.Marshal(v.look())
		if err != nil {
			return err
		}
		key := "showcase:" + v.id
		var record string
		err = s.db.QueryRowContext(ctx, "SELECT v FROM onair_settings WHERE k=?", key).Scan(&record)
		if errors.Is(err, sql.ErrNoRows) {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO onair_settings(k,v) VALUES(?,?) ON CONFLICT(k) DO NOTHING", key, fmt.Sprintf("%d:%s", v.version, digest(string(data))))
			if err == nil {
				if n, _ := res.RowsAffected(); n == 1 {
					_, err = tx.ExecContext(ctx, "INSERT INTO onair_looks(id,data,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", v.id, string(data), time.Now().Unix())
				}
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		version, hash, _ := strings.Cut(record, ":")
		if n, _ := strconv.Atoi(version); n >= v.version {
			continue
		}
		// A newer shipped version replaces only the look exactly as shipped.
		var stored string
		err = s.db.QueryRowContext(ctx, "SELECT data FROM onair_looks WHERE id=?", v.id).Scan(&stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		next := fmt.Sprintf("%d:%s", v.version, hash)
		if err == nil && digest(stored) == hash {
			if _, err = s.db.ExecContext(ctx, "UPDATE onair_looks SET data=?,updated_at=? WHERE id=? AND data=?", string(data), time.Now().Unix(), v.id, stored); err != nil {
				return err
			}
			next = fmt.Sprintf("%d:%s", v.version, digest(string(data)))
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE onair_settings SET v=? WHERE k=?", next, key); err != nil {
			return err
		}
	}
	reels, err := shippedShowcaseReels()
	if err != nil {
		return err
	}
	return s.seedShowcaseReels(ctx, reels)
}

func (s *Service) seedShowcaseReels(ctx context.Context, reels []shippedShowcaseReel) error {
	for _, sr := range reels {
		if sr.ID == "" {
			continue
		}
		target := sr.reel()
		sig := showcaseReelSignature(target)
		key := "showcase:" + sr.ID
		var record string
		err := s.db.QueryRowContext(ctx, "SELECT v FROM onair_settings WHERE k=?", key).Scan(&record)
		if errors.Is(err, sql.ErrNoRows) {
			if err := s.insertShippedReel(ctx, key, sr, sig, false, nil); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		version, hash, _ := strings.Cut(record, ":")
		if n, _ := strconv.Atoi(version); n >= sr.Version {
			continue
		}
		existing, err := s.Reel(ctx, sr.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if errors.Is(err, ErrNotFound) || showcaseReelSignature(existing) != hash {
			// Deleted or edited by the owner: advance version so we never undo it.
			if _, err = s.db.ExecContext(ctx, "UPDATE onair_settings SET v=? WHERE k=?", fmt.Sprintf("%d:%s", sr.Version, hash), key); err != nil {
				return err
			}
			continue
		}
		oldDigests := make([]string, 0, len(existing.Clips))
		for _, c := range existing.Clips {
			oldDigests = append(oldDigests, c.SourceDigest)
		}
		if err := s.insertShippedReel(ctx, key, sr, sig, true, oldDigests); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) insertShippedReel(ctx context.Context, key string, sr shippedShowcaseReel, sig string, replace bool, oldDigests []string) error {
	reelRow := Reel{
		ID:            sr.ID,
		Name:          sr.Name,
		Order:         sr.Order,
		RepeatHours:   sr.RepeatHours,
		LoudnessMatch: sr.LoudnessMatch,
		TrimSilence:   sr.TrimSilence,
		Clips:         make([]ReelClip, 0, len(sr.Clips)),
		ChannelIDs:    []string{},
	}
	for _, sc := range sr.Clips {
		reelRow.Clips = append(reelRow.Clips, ReelClip{ID: sc.ID})
	}
	reelBytes, err := json.Marshal(reelRow)
	if err != nil {
		return err
	}

	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	settingVal := fmt.Sprintf("%d:%s", sr.Version, sig)
	if !replace {
		res, err := tx.ExecContext(ctx, "INSERT INTO onair_settings(k,v) VALUES(?,?) ON CONFLICT(k) DO NOTHING", key, settingVal)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return tx.Commit()
		}
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM onair_reels WHERE id=?", sr.ID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return tx.Commit()
		}
		// Anchor shipped reels at Unix epoch 0 so schedules across any day include pods.
		if _, err := tx.ExecContext(ctx, "INSERT INTO onair_reels(id,data,updated_at) VALUES(?,?,0)", sr.ID, string(reelBytes)); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, "DELETE FROM onair_reel_clips WHERE reel_id=?", sr.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE onair_reels SET data=?,updated_at=0 WHERE id=?", string(reelBytes), sr.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE onair_settings SET v=? WHERE k=?", settingVal, key); err != nil {
			return err
		}
	}

	for i, sc := range sr.Clips {
		srcDigest := linkAssetDigest(sc.Source)
		c := ReelClip{
			Source:       sc.Source,
			Attribution:  sc.Attribution,
			ID:           sc.ID,
			Name:         sc.Name,
			DurationMS:   sc.DurationMS,
			SourceDigest: srcDigest,
			Status:       "ready",
			Preview:      "/ether/api/onair/reels/" + sr.ID + "/clips/" + sc.ID + "/preview",
			Link:         true,
			Tags:         append([]string(nil), sc.Tags...),
		}
		clipBytes, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO onair_assets(digest,kind,name,mime,size,license,content,created_at,source,attribution,duration_ms,stored) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(digest) DO NOTHING",
			srcDigest, "reel", sc.Name, "text/uri-list", int64(0), strings.Join(sc.Tags, ","), []byte{}, int64(0), sc.Source, sc.Attribution, sc.DurationMS, "link",
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO onair_reel_clips(id,reel_id,name,duration_ms,source_digest,status,created_at,data) VALUES(?,?,?,?,?,'ready',?,?) ON CONFLICT(id) DO NOTHING",
			c.ID, sr.ID, c.Name, c.DurationMS, srcDigest, int64(i+1), string(clipBytes),
		); err != nil {
			return err
		}
	}
	if replace && len(oldDigests) > 0 {
		if err := s.dropUnusedReelSources(ctx, tx, oldDigests); err != nil {
			return err
		}
	}
	return tx.Commit()
}
