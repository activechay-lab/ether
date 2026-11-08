package livetv

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// A source refresh writes only what changed. Re-importing an unchanged guide
// rewrites no row, so it costs no WAL or write-gate time and does not move the
// viewer-policy epoch that every row write on these tables advances. A
// changed rating changes a programme's stored data, so it is written (and
// moves the epoch) like any other change.

// importWrites counts what one import wrote.
type importWrites struct {
	ProgrammesWritten, ProgrammesDeleted, ProgrammesKept int
	ChannelsWritten, ChannelsDeleted                     int
	GuideChannelsWritten, GuideChannelsDeleted           int
}

func (w importWrites) changed() bool {
	return w.ProgrammesWritten+w.ProgrammesDeleted+w.ChannelsWritten+w.ChannelsDeleted+w.GuideChannelsWritten+w.GuideChannelsDeleted > 0
}

type programmeKey struct {
	channel string
	start   int64
}

// writeProgrammes makes the source's stored programmes equal ps. Stored rows
// are streamed once and compared with the incoming guide (already in memory
// from the fetch), so the database side never holds the source's schedule
// in RAM. Identical rows stay untouched; removed ones are deleted, new or
// changed ones (end time or data, which carries the rating) are upserted, in
// bounded batches.
func writeProgrammes(ctx context.Context, tx *sql.Tx, batch int, sourceID string, ps []Programme, saved func(int)) (importWrites, error) {
	var w importWrites
	type incoming struct {
		end  int64
		data []byte
		seen bool
	}
	in := make(map[programmeKey]*incoming, len(ps))
	order := make([]programmeKey, 0, len(ps))
	for _, p := range ps {
		b, err := json.Marshal(p)
		if err != nil {
			return w, err
		}
		k := programmeKey{p.ChannelID, p.Start.UnixMilli()}
		if _, dup := in[k]; !dup {
			order = append(order, k)
		}
		in[k] = &incoming{end: p.End.UnixMilli(), data: b}
	}
	rows, err := tx.QueryContext(ctx, `SELECT channel_id, start_ms, end_ms, data FROM livetv_programmes WHERE source_id=?`, sourceID)
	if err != nil {
		return w, err
	}
	var gone []programmeKey
	for rows.Next() {
		var k programmeKey
		var end int64
		var data []byte
		if err := rows.Scan(&k.channel, &k.start, &end, &data); err != nil {
			rows.Close()
			return w, err
		}
		// Byte-exact against json.Marshal(Programme) by design: a later change
		// to Programme's encoding costs one full rewrite, never correctness.
		cur, ok := in[k]
		switch {
		case !ok:
			gone = append(gone, k)
		case cur.end == end && bytes.Equal(cur.data, data):
			cur.seen = true // identical: nothing to write
			w.ProgrammesKept++
		}
	}
	if err := rows.Close(); err != nil {
		return w, err
	}
	if err := rows.Err(); err != nil {
		return w, err
	}
	for start := 0; start < len(gone); start += batch {
		end := min(start+batch, len(gone))
		marks := make([]string, 0, end-start)
		args := []any{sourceID}
		for _, k := range gone[start:end] {
			marks = append(marks, "(?,?)")
			args = append(args, k.channel, k.start)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_programmes WHERE source_id=? AND (channel_id,start_ms) IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
			return w, err
		}
		w.ProgrammesDeleted += end - start
	}
	var write []programmeKey
	for _, k := range order {
		if !in[k].seen {
			write = append(write, k)
		}
	}
	for start := 0; start < len(write); start += batch {
		end := min(start+batch, len(write))
		marks := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*5)
		for _, k := range write[start:end] {
			p := in[k]
			marks = append(marks, "(?,?,?,?,?)")
			args = append(args, sourceID, k.channel, k.start, p.end, p.data)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_programmes(source_id,channel_id,start_ms,end_ms,data) VALUES `+strings.Join(marks, ",")+`
			ON CONFLICT(source_id,channel_id,start_ms) DO UPDATE SET end_ms=excluded.end_ms, data=excluded.data`, args...); err != nil {
			return w, err
		}
		w.ProgrammesWritten += end - start
		if saved != nil {
			saved(w.ProgrammesKept + w.ProgrammesWritten)
		}
	}
	return w, nil
}

// writeSourceChannels makes the source's stored channel rows equal cs (each
// already encoded): unchanged rows stay untouched.
func writeSourceChannels(ctx context.Context, tx *sql.Tx, d store.Dialect, batch int, sourceID string, ids []string, data [][]byte, saved func(int)) (importWrites, error) {
	var w importWrites
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM livetv_channels WHERE `+store.JSONText(d, "data", "sourceId")+`=?`, sourceID)
	if err != nil {
		return w, err
	}
	stored := map[string][]byte{}
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return w, err
		}
		stored[id] = b
	}
	if err := rows.Close(); err != nil {
		return w, err
	}
	if err := rows.Err(); err != nil {
		return w, err
	}
	keep := make(map[string]bool, len(ids))
	var write []int
	for i, id := range ids {
		keep[id] = true
		if old, ok := stored[id]; !ok || !bytes.Equal(old, data[i]) {
			write = append(write, i)
		}
	}
	var gone []string
	for id := range stored {
		if !keep[id] {
			gone = append(gone, id)
		}
	}
	for start := 0; start < len(gone); start += batch {
		end := min(start+batch, len(gone))
		marks := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, 0, end-start)
		for _, id := range gone[start:end] {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_channels WHERE id IN (`+marks+`)`, args...); err != nil {
			return w, err
		}
		w.ChannelsDeleted += end - start
	}
	for start := 0; start < len(write); start += batch {
		end := min(start+batch, len(write))
		marks := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*2)
		for _, i := range write[start:end] {
			marks = append(marks, "(?,?)")
			args = append(args, ids[i], data[i])
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES `+strings.Join(marks, ",")+` ON CONFLICT(id) DO UPDATE SET data=excluded.data`, args...); err != nil {
			return w, err
		}
		w.ChannelsWritten += end - start
	}
	if saved != nil {
		saved(len(ids))
	}
	return w, nil
}

type guideChannelRow struct{ name, logo, names string }

// writeGuideChannels makes the source's guide channel rows match g: named
// guide channels, plus programme-only channel IDs (named after themselves).
// Unchanged rows stay untouched.
func writeGuideChannels(ctx context.Context, tx *sql.Tx, sourceID string, g XMLGuide) (importWrites, error) {
	var w importWrites
	want := map[string]guideChannelRow{}
	for id, c := range g.Channels {
		if id == "" {
			continue
		}
		names := g.Names[id]
		if names == nil {
			names = []string{}
		}
		b, err := json.Marshal(names)
		if err != nil {
			return w, err
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = id
		}
		want[id] = guideChannelRow{name, c.Logo, string(b)}
	}
	for _, p := range g.Programmes {
		if _, ok := want[p.ChannelID]; !ok && p.ChannelID != "" {
			want[p.ChannelID] = guideChannelRow{p.ChannelID, "", "[]"}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT guide_id, name, logo, names FROM livetv_guide_channels WHERE source_id=?`, sourceID)
	if err != nil {
		return w, err
	}
	var gone []string
	same := map[string]bool{}
	for rows.Next() {
		var id string
		var r guideChannelRow
		if err := rows.Scan(&id, &r.name, &r.logo, &r.names); err != nil {
			rows.Close()
			return w, err
		}
		if cur, ok := want[id]; !ok {
			gone = append(gone, id)
		} else if cur == r {
			same[id] = true
		}
	}
	if err := rows.Close(); err != nil {
		return w, err
	}
	if err := rows.Err(); err != nil {
		return w, err
	}
	for _, id := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_guide_channels WHERE source_id=? AND guide_id=?`, sourceID, id); err != nil {
			return w, err
		}
		w.GuideChannelsDeleted++
	}
	for id, r := range want {
		if same[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_guide_channels(source_id,guide_id,name,logo,names) VALUES(?,?,?,?,?)
			ON CONFLICT(source_id,guide_id) DO UPDATE SET name=excluded.name, logo=excluded.logo, names=excluded.names`, sourceID, id, r.name, r.logo, r.names); err != nil {
			return w, err
		}
		w.GuideChannelsWritten++
	}
	return w, nil
}

func init() {
	// A refresh's time is the schedule anchor, not policy: it lives outside
	// livetv_sources (whose writes advance the viewer-policy epoch), so an
	// unchanged refresh records it without rewriting the source.
	store.Register("livetv", 14, `CREATE TABLE livetv_source_refresh(id TEXT PRIMARY KEY, updated_ms BIGINT NOT NULL);`)
}

// writeImportedSource stores an imported source. The row (and its row lock)
// is written only when the source itself changed or a refresh error is to be
// cleared, and it reports whether it was; every import records its time in
// livetv_source_refresh. Callers hold lockSourceEdits for the source.
func (s *Service) writeImportedSource(ctx context.Context, tx *sql.Tx, src Source) (bool, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM livetv_sources WHERE id=?`, src.ID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	write := err != nil
	if !write {
		old, _, derr := s.decodeSource(raw)
		write = derr != nil || old.LastError != "" || old.LastErrorAt != nil || old.RefreshFailures != 0 || !sameStoredSource(old, src)
	}
	if write {
		b, err := s.encodeSource(src)
		if err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO livetv_sources(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, src.ID, b); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO livetv_source_refresh(id,updated_ms) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET updated_ms=excluded.updated_ms`, src.ID, src.UpdatedAt.UnixMilli())
	return write, err
}

// sameStoredSource compares what a source row holds apart from its refresh
// time. Secrets are compared opened: sealing them is randomised.
func sameStoredSource(a, b Source) bool {
	a, b = storedSource(a), storedSource(b)
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

// overlayRefreshStamps gives each source the time of its latest refresh,
// which an unchanged import records only in livetv_source_refresh.
func (s *Service) overlayRefreshStamps(ctx context.Context, srcs []Source) error {
	if len(srcs) == 0 {
		return nil
	}
	rows, err := s.o.DB.QueryContext(ctx, `SELECT id, updated_ms FROM livetv_source_refresh`)
	if err != nil {
		return err
	}
	defer rows.Close()
	at := map[string]int64{}
	for rows.Next() {
		var id string
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return err
		}
		at[id] = ms
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range srcs {
		if ms, ok := at[srcs[i].ID]; ok && ms > srcs[i].UpdatedAt.UnixMilli() {
			srcs[i].UpdatedAt = time.UnixMilli(ms).UTC()
		}
	}
	return nil
}
