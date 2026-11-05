package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/container/ebml"
)

const indexedHeaderLimit = 8 << 20

// IndexedInput prepares a bounded Matroska projection: original codec headers,
// then the indexed keyframe cluster. All provider I/O happens in the caller's
// background prepare. The returned loopback URL cannot address arbitrary bytes:
// its cluster is verified against this generation's published container index.
func (m *Manager) IndexedInput(ctx context.Context, input string, at time.Duration) (string, time.Duration, bool) {
	g := m.generationFor(input)
	if g == nil || at <= 0 || !g.acquire() {
		return "", 0, false
	}
	defer g.release()
	info := g.infoSnapshot()
	if info.Container != "mkv" && info.Container != "matroska" && info.Container != "webm" {
		return "", 0, false
	}
	tm := g.timeMapSnapshot()
	if len(tm.Points) == 0 {
		return "", 0, false
	}
	point := tm.Points[0]
	for _, p := range tm.Points {
		if p.Time > at {
			break
		}
		point = p
	}
	if point.Time > at || point.Offset <= 0 || point.Offset >= tm.Size {
		return "", 0, false
	}
	m.WarmInput(input, at, at+8*time.Second)
	g.indexedMu.Lock()
	defer g.indexedMu.Unlock()
	if len(g.indexedHeader) == 0 {
		first := tm.Points[0].Offset
		if first <= 0 || first > indexedHeaderLimit {
			return "", 0, false
		}
		header := make([]byte, int(first))
		if n, e := readMetadata(ctx, g, header, 0); e != nil || n != len(header) {
			return "", 0, false
		}
		if !indexedMatroskaHeader(header) {
			return "", 0, false
		}
		g.indexedHeader = header
	}
	u, e := url.Parse(input)
	if e != nil {
		return "", 0, false
	}
	q := u.Query()
	q.Set("cluster", strconv.FormatInt(point.Offset, 10))
	u.RawQuery = q.Encode()
	m.log.Debug("indexed decoder input", "session", g.sess.id, "target", at, "cluster", point.Offset, "keyframe", point.Time, "forwardTrim", at-point.Time, "headerBytes", len(g.indexedHeader))
	return u.String(), at - point.Time, true
}

// Preserve header byte coordinates while disabling original-file seeks. A
// projection's segment is unknown-sized; SeekHead/Cues become bounded Void.
func indexedMatroskaHeader(header []byte) bool {
	segment := false
	for at, steps := 0, 0; at < len(header) && steps < 4096; steps++ {
		id, idLen := ebml.Vint(header[at:], true)
		if idLen < 1 || idLen > 4 {
			return false
		}
		size, sizeLen := ebml.Vint(header[at+idLen:], false)
		if sizeLen < 1 {
			return false
		}
		head := idLen + sizeLen
		if id == 0x18538067 {
			for n := 0; n < sizeLen; n++ {
				header[at+idLen+n] = 255
			}
			header[at+idLen] = byte((1 << uint(9-sizeLen)) - 1)
			segment = true
			at += head
			continue
		}
		if ebml.Unknown(size, sizeLen) || size > uint64(len(header)-at-head) {
			return false
		}
		total := head + int(size)
		if segment && (id == 0x114d9b74 || id == 0x1c53bb6b) {
			if !fillIndexedVoid(header[at : at+total]) {
				return false
			}
		}
		at += total
		if at == len(header) {
			return segment
		}
	}
	return false
}
func fillIndexedVoid(b []byte) bool {
	for width := 1; width <= 8; width++ {
		payload := len(b) - 1 - width
		if payload < 0 {
			continue
		}
		if uint64(payload) >= (uint64(1)<<uint(width*7))-1 {
			continue
		}
		clear(b)
		b[0] = 0xec
		value := uint64(payload)
		for n := width; n > 0; n-- {
			b[n] = byte(value)
			value >>= 8
		}
		b[1] |= byte(1 << uint(8-width))
		return true
	}
	return false
}
func (m *Manager) serveIndexedLoopback(w http.ResponseWriter, r *http.Request, g *generation, raw string) {
	cluster, e := strconv.ParseInt(raw, 10, 64)
	valid := false
	if e == nil {
		for _, point := range g.timeMapSnapshot().Points {
			if point.Offset == cluster {
				valid = true
				break
			}
		}
	}
	g.indexedMu.Lock()
	header := g.indexedHeader
	g.indexedMu.Unlock()
	if !valid || len(header) == 0 || cluster < int64(len(header)) || !g.acquire() {
		http.Error(w, "invalid indexed input", http.StatusBadRequest)
		return
	}
	defer g.release()
	size := int64(len(header)) + g.src.Size() - cluster
	rep := representation{size: size, etag: fmt.Sprintf(`"%s-index-%d"`, g.releaseID(), cluster), contentType: "video/x-matroska"}
	rep.body = func(w io.Writer, start, length int64) error {
		if start < int64(len(header)) {
			n := min(length, int64(len(header))-start)
			if _, e := w.Write(header[start : start+n]); e != nil {
				return e
			}
			start += n
			length -= n
		}
		if length <= 0 {
			return nil
		}
		_, readErr, writeErr := m.copyFrom(r.Context(), w, g, cluster+start-int64(len(header)), length, func(n int) { g.sess.addBytes(n, m.now()) }, false)
		if writeErr != nil {
			return writeErr
		}
		return readErr
	}
	if e := serveRepresentation(w, r, rep); e != nil && r.Context().Err() == nil {
		m.log.Debug("indexed loopback response ended early", "session", g.sess.id, "err", e)
	}
}
