package onair

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

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
	return nil
}
