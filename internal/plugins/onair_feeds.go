package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/faillog"
	"github.com/braedonsaunders/ether/internal/onair"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// PluginSegment is a memory-only lookup shared by the render queue and scheduler.
func (m *Manager) PluginSegment(id string) (onair.PluginSegment, bool) {
	return m.deps.OnAirContent.Lookup(id)
}

func plainFeedText(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 4096 || strings.ContainsAny(s, "<>") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func feedImage(value any, files func(string) ([]byte, bool)) (image.Image, error) {
	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("must be packaged image path or base64 bytes")
	}
	var b []byte
	if !unsafePath(s) && files != nil {
		b, _ = files(s)
	}
	if b == nil {
		if strings.HasPrefix(s, "data:image/") {
			_, s, _ = strings.Cut(s, ",")
		}
		if len(s) > base64.StdEncoding.EncodedLen(2<<20) {
			return nil, fmt.Errorf("image exceeds 2 MB")
		}
		var err error
		b, err = base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("invalid image reference or bytes")
		}
	}
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("image exceeds 2 MB")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 2160 || cfg.Width*cfg.Height > 4096*2160 {
		return nil, fmt.Errorf("image dimensions exceed bounds")
	}
	im, _, err := image.Decode(bytes.NewReader(b))
	return im, err
}
func boundedList(v any, depth int) bool {
	if depth > 4 {
		return false
	}
	switch x := v.(type) {
	case string:
		return plainFeedText(x)
	case float64:
		return !math.IsInf(x, 0) && !math.IsNaN(x)
	case nil, bool:
		return true
	case []any:
		if len(x) > 50 {
			return false
		}
		for _, v := range x {
			if !boundedList(v, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		if len(x) > 64 {
			return false
		}
		for k, v := range x {
			if !keyRe.MatchString(k) || !boundedList(v, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// ValidateFeed rejects the complete update on any bad field, preserving the last good value.
func ValidateFeed(f OnAirFeed, raw []byte, files func(string) ([]byte, bool)) (map[string]any, map[string]image.Image, error) {
	if len(raw) > 8<<20 {
		return nil, nil, fmt.Errorf("feed exceeds 8 MB")
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return nil, nil, fmt.Errorf("feed must be an object")
	}
	var errs []FieldError
	images := map[string]image.Image{}
	for k, t := range f.Fields {
		v, exists := data[k]
		valid := exists
		switch t {
		case "text":
			s, ok := v.(string)
			valid = valid && ok && plainFeedText(s)
		case "number":
			n, ok := v.(float64)
			valid = valid && ok && !math.IsNaN(n) && !math.IsInf(n, 0)
		case "list":
			a, ok := v.([]any)
			valid = valid && ok && len(a) <= 50 && boundedList(a, 0)
		case "image":
			im, err := feedImage(v, files)
			valid = valid && err == nil
			if valid {
				images[k] = im
			}
		default:
			valid = false
		}
		if !valid {
			errs = append(errs, FieldError{k, "invalid " + t + " field"})
		}
	}
	for k := range data {
		if _, ok := f.Fields[k]; !ok {
			errs = append(errs, FieldError{k, "undeclared field"})
		}
	}
	if len(errs) > 0 {
		return nil, nil, &ValidationError{errs}
	}
	return data, images, nil
}
func flattenFeed(out map[string]string, path string, value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			flattenFeed(out, path+"."+k, x)
		}
	case []any:
		for i, x := range v {
			flattenFeed(out, path+"["+strconv.Itoa(i)+"]", x)
		}
	case string:
		out[path] = v
	default:
		out[path] = fmt.Sprint(v)
	}
}

// refreshOnAir runs only from the host background loop. Followers reload shared values;
// leadership is checked for each fetch, independently of every_node placement.
func (m *Manager) refreshOnAir(ctx context.Context, recs []Record, now time.Time) {
	snapshot := map[string]onair.PluginSegment{}
	for _, r := range recs {
		if !r.Enabled || !r.Manifest.HasCapability(CapOnAirContent) {
			continue
		}
		e := r.Manifest.Ether
		files := func(p string) ([]byte, bool) { return m.store.file(ctx, r.ID, r.Version, p) }
		values := map[string]map[string]any{}
		ims := map[string]map[string]image.Image{}
		exp := map[string]time.Time{}
		versions := map[string]string{}
		every := 15 * time.Minute
		if len(e.Schedule) > 0 {
			if d, err := time.ParseDuration(e.Schedule[0].Every); err == nil && d >= every {
				every = d
			}
		}
		for _, f := range e.Contributes.OnAirFeeds {
			var raw string
			var fetched int64
			_ = m.store.db.QueryRowContext(ctx, "SELECT data,fetched_at FROM onair_feed_values WHERE plugin=? AND feed=?", r.ID, f.ID).Scan(&raw, &fetched)
			key := r.ID + "|onair:" + f.ID
			m.mu.Lock()
			next := m.nextRun[key]
			m.mu.Unlock()
			if m.isLeader() && !now.Before(next) && (fetched == 0 || !now.Before(time.UnixMilli(fetched).Add(every))) {
				m.mu.Lock()
				m.nextRun[key] = now.Add(every)
				m.mu.Unlock()
				resp, err := m.Call(ctx, r.ID, Request{Op: "feed", ID: f.ID, Method: "POST", Path: "/ether/feed/" + f.ID})
				if err == nil && resp.Status >= 200 && resp.Status < 300 {
					if _, _, err = ValidateFeed(f, resp.Body, files); err == nil {
						newFetched := now.UnixMilli()
						_, err = m.store.db.ExecContext(ctx, "INSERT INTO onair_feed_values(plugin,feed,data,fetched_at) VALUES(?,?,?,?) ON CONFLICT(plugin,feed) DO UPDATE SET data=excluded.data,fetched_at=excluded.fetched_at", r.ID, f.ID, string(resp.Body), newFetched)
						if err == nil {
							raw = string(resp.Body)
							fetched = newFetched
						}
					}
				} else if err == nil {
					err = fmt.Errorf("feed returned status %d", resp.Status)
				}
				tracker, _ := m.feedLogs.LoadOrStore(key, faillog.New(m.log, "plugin feed"))
				if err != nil {
					tracker.(*faillog.Tracker).Fail(err, "plugin", r.ID, "feed", f.ID)
					m.RecordFailure(ctx, r.ID, "feed "+f.ID+": "+err.Error())
				} else {
					tracker.(*faillog.Tracker).OK()
				}
			}
			data, images, err := ValidateFeed(f, []byte(raw), files)
			if err != nil {
				continue
			}
			age, _ := time.ParseDuration(f.MaxAge)
			values[f.ID] = data
			ims[f.ID] = images
			exp[f.ID] = time.UnixMilli(fetched).Add(age)
			h := sha256.Sum256([]byte(raw))
			versions[f.ID] = hex.EncodeToString(h[:]) + fmt.Sprint(fetched)
		}
		for _, seg := range e.Contributes.OnAirSegments {
			raw, ok := files(seg.File)
			if !ok {
				continue
			}
			doc, err := scene.Decode(raw)
			if err != nil {
				continue
			}
			s := onair.PluginSegment{Document: doc, Name: seg.Name, Rating: seg.Rating, Slots: map[string]string{}, Images: map[string]image.Image{}, Expires: now.Add(24 * time.Hour)}
			version := r.Version
			for _, id := range seg.Feeds {
				if exp[id].Before(s.Expires) {
					s.Expires = exp[id]
				}
				version += id + versions[id]
				flattenFeed(s.Slots, r.ID+"/"+id, values[id])
				// Both fully qualified bindings and package-local bindings resolve.
				flattenFeed(s.Slots, id, values[id])
				for k, im := range ims[id] {
					ref := "plugin-image:" + r.ID + "/" + id + "." + k
					s.Images[ref] = im
					s.Slots[r.ID+"/"+id+"."+k] = ref
					s.Slots[id+"."+k] = ref
				}
			}
			h := sha256.Sum256([]byte(version))
			s.Version = hex.EncodeToString(h[:])
			snapshot[extend.PluginID(r.ID, seg.ID)] = s
		}
	}
	m.deps.OnAirContent.Publish(snapshot)
}
