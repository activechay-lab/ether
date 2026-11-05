package onair

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type LibraryUse struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scene string `json:"scene,omitempty"`
}
type LibraryAsset struct {
	Asset
	MIME       string       `json:"mime"`
	UsedBy     []LibraryUse `json:"usedBy"`
	CreatedAt  int64        `json:"createdAt"`
	DurationMs int64        `json:"durationMs,omitempty"`
	Width      int          `json:"width,omitempty"`
	Height     int          `json:"height,omitempty"`
}

func (s *Service) Library(ctx context.Context) ([]LibraryAsset, error) {
	assets, e := s.Assets(ctx)
	if e != nil {
		return nil, e
	}
	out := []LibraryAsset{}
	for _, a := range assets {
		if a.Kind == "reel" {
			continue
		}
		v, e := s.libraryInfo(ctx, a)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) libraryInfo(ctx context.Context, a Asset) (LibraryAsset, error) {
	v := LibraryAsset{Asset: a, MIME: a.MIME, UsedBy: []LibraryUse{}}
	if a.Status != "" && a.Status != "ready" {
		return v, nil
	}
	if e := s.db.QueryRowContext(ctx, "SELECT created_at FROM onair_assets WHERE digest=?", a.Digest).Scan(&v.CreatedAt); e != nil {
		return v, e
	}
	// Facts are content-addressed, so replacement cannot leave stale metadata.
	if b, e := os.ReadFile(filepath.Join(s.o.Dir, "library-"+a.Digest+".json")); e == nil {
		_ = json.Unmarshal(b, &v)
		v.Asset = a
		v.MIME = a.MIME
		v.UsedBy = []LibraryUse{}
	}
	for _, entry := range []struct{ table, kind string }{{"onair_looks", "style"}, {"onair_reels", "reel"}, {"livetv_channels", "channel"}} {
		if entry.kind == "channel" {
			ok, e := s.hasChannelAssets(ctx)
			if e != nil {
				return v, e
			}
			if !ok {
				continue
			}
		}
		query := "SELECT id,data FROM " + entry.table + " WHERE data LIKE ?"
		if entry.kind == "reel" {
			query = "SELECT id,data FROM onair_reels WHERE id IN (SELECT reel_id FROM onair_reel_clips WHERE source_digest=?)"
		}
		arg := "%" + a.Digest + "%"
		if entry.kind == "reel" {
			arg = a.Digest
		}
		rows, e := s.db.QueryContext(ctx, query, arg)
		if e != nil {
			return v, e
		}
		for rows.Next() {
			var id, data string
			if e = rows.Scan(&id, &data); e != nil {
				rows.Close()
				return v, e
			}
			var record struct {
				Name   string `json:"name"`
				Scenes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"scenes"`
			}
			_ = json.Unmarshal([]byte(data), &record)
			name := record.Name
			if name == "" {
				name = id
			}
			v.UsedBy = append(v.UsedBy, LibraryUse{Kind: entry.kind, ID: id, Name: name})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return v, e
		}
	}
	return v, nil
}
func (s *Service) libraryAsset(ctx context.Context, d string) (LibraryAsset, error) {
	var a Asset
	a.Digest = d
	e := s.db.QueryRowContext(ctx, "SELECT name,kind,size,license,mime,source,attribution FROM onair_assets WHERE digest=?", d).Scan(&a.Name, &a.Kind, &a.Size, &a.License, &a.MIME, &a.Source, &a.Attribution)
	if e != nil {
		return LibraryAsset{}, ErrNotFound
	}
	return s.libraryInfo(ctx, a)
}
func (s *Service) recordFacts(ctx context.Context, a Asset, b []byte) {
	v := LibraryAsset{DurationMs: 0}
	if a.Kind == "image" {
		if c, _, e := image.DecodeConfig(bytes.NewReader(b)); e == nil {
			v.Width = c.Width
			v.Height = c.Height
		}
	}
	if (a.Kind == "audio" || a.Kind == "video") && s.o.Probe != nil {
		f, e := os.CreateTemp(s.o.Dir, "library-probe-*")
		if e == nil {
			defer os.Remove(f.Name())
			_, _ = f.Write(b)
			_ = f.Close()
			if info, e := s.o.Probe(ctx, f.Name()); e == nil {
				v.DurationMs = info.Duration.Milliseconds()
				if st := info.Video(); st != nil {
					v.Width = st.Width
					v.Height = st.Height
				}
			}
		}
	}
	data, _ := json.Marshal(v)
	_ = os.WriteFile(filepath.Join(s.o.Dir, "library-"+a.Digest+".json"), data, 0600)
}
func (s *Service) libraryDerived(ctx context.Context, a LibraryAsset, wave bool) ([]byte, error) {
	suffix := ".jpg"
	if wave {
		suffix = ".wave.json"
	}
	path := filepath.Join(s.o.Dir, "library-"+a.Digest+suffix)
	if b, e := os.ReadFile(path); e == nil {
		return b, nil
	}
	var data []byte
	if e := s.db.QueryRowContext(ctx, "SELECT content FROM onair_assets WHERE digest=?", a.Digest).Scan(&data); e != nil {
		return nil, e
	}
	var out bytes.Buffer
	if !wave && (a.Kind == "image" || a.Kind == "font") {
		var src image.Image
		if a.Kind == "image" {
			var e error
			src, _, e = image.Decode(bytes.NewReader(data))
			if e != nil {
				return nil, e
			}
		} else {
			f, e := opentype.Parse(data)
			if e != nil {
				return nil, e
			}
			face, e := opentype.NewFace(f, &opentype.FaceOptions{Size: 150, DPI: 72})
			if e != nil {
				return nil, e
			}
			defer face.Close()
			im := image.NewRGBA(image.Rect(0, 0, 480, 270))
			draw.Draw(im, im.Bounds(), image.NewUniform(color.RGBA{24, 28, 42, 255}), image.Point{}, draw.Src)
			d := font.Drawer{Dst: im, Src: image.White, Face: face, Dot: fixed.P(110, 190)}
			d.DrawString("Aa")
			src = im
		}
		h := max(1, 480*src.Bounds().Dy()/max(1, src.Bounds().Dx()))
		h = min(h, 4096)
		im := image.NewRGBA(image.Rect(0, 0, 480, h))
		draw.CatmullRom.Scale(im, im.Bounds(), src, src.Bounds(), draw.Src, nil)
		if e := jpeg.Encode(&out, im, &jpeg.Options{Quality: 85}); e != nil {
			return nil, e
		}
	} else {
		if s.o.FFmpeg == nil {
			return nil, fmt.Errorf("media tools unavailable")
		}
		f, e := os.CreateTemp(s.o.Dir, "library-media-*")
		if e != nil {
			return nil, e
		}
		defer os.Remove(f.Name())
		_, e = f.Write(data)
		_ = f.Close()
		if e != nil {
			return nil, e
		}
		cctx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-i", f.Name()}
		if wave {
			args = append(args, "-t", "30", "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1")
		} else {
			args = append(args, "-ss", "1", "-frames:v", "1", "-vf", "scale=480:-2", "-threads", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
		}
		cmd := exec.CommandContext(cctx, s.o.FFmpeg(), args...)
		cmd.Stdout = &out
		if e = cmd.Run(); e != nil {
			return nil, e
		}
		if wave {
			pcm := out.Bytes()
			peaks := make([]float64, 1000)
			samples := len(pcm) / 2
			for i := 0; i < samples; i++ {
				p := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))) / 32768
				bucket := min(999, i*1000/max(1, samples))
				peaks[bucket] = max(peaks[bucket], p)
			}
			b, _ := json.Marshal(map[string]any{"peaks": peaks, "durationMs": a.DurationMs})
			out.Reset()
			out.Write(b)
		}
	}
	b := out.Bytes()
	if e := os.WriteFile(path, b, 0600); e != nil {
		return nil, e
	}
	return b, nil
}
func (s *Service) LibraryRoutes(r chi.Router) {
	r.Get("/assets/{id}/info", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.libraryAsset(r.Context(), chi.URLParam(r, "id"))
		result(w, v, e)
	})
	for _, part := range []string{"thumb", "waveform"} {
		part := part
		r.Get("/assets/{id}/"+part, func(w http.ResponseWriter, r *http.Request) {
			a, e := s.libraryAsset(r.Context(), chi.URLParam(r, "id"))
			if e != nil {
				result(w, nil, e)
				return
			}
			b, e := s.libraryDerived(r.Context(), a, part == "waveform")
			if e != nil {
				result(w, nil, e)
				return
			}
			mime := "image/jpeg"
			if part == "waveform" {
				mime = "application/json"
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("ETag", `"`+a.Digest+"-"+part+`"`)
			http.ServeContent(w, r, part, time.Time{}, bytes.NewReader(b))
		})
	}
	r.Patch("/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name        string  `json:"name"`
			License     *string `json:"license"`
			Attribution *string `json:"attribution"`
		}
		if !body(w, r, &p) {
			return
		}
		d := chi.URLParam(r, "id")
		a, e := s.libraryAsset(r.Context(), d)
		if e != nil {
			result(w, nil, e)
			return
		}
		if strings.TrimSpace(p.Name) == "" {
			p.Name = a.Name
		}
		license, attribution := a.License, a.Attribution
		if p.License != nil {
			license = *p.License
		}
		if p.Attribution != nil {
			attribution = *p.Attribution
		}
		_, e = s.db.ExecContext(r.Context(), "UPDATE onair_assets SET name=?,license=?,attribution=? WHERE digest=?", p.Name, license, attribution, d)
		if e != nil {
			result(w, nil, e)
			return
		}
		v, e := s.libraryAsset(r.Context(), d)
		result(w, v, e)
	})
	r.Post("/assets/{id}/replace", s.replaceLibraryAsset)
	r.Post("/assets/{id}/trim", s.trimLibraryAsset)
}
func (s *Service) replaceLibraryAsset(w http.ResponseWriter, r *http.Request) {
	old, e := s.libraryAsset(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		result(w, nil, e)
		return
	}
	if !multipart(w, r, assetLimit) {
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, e := r.FormFile("file")
	if e != nil {
		result(w, nil, e)
		return
	}
	defer f.Close()
	a, e := s.UploadAsset(r.Context(), h.Filename, old.Kind, old.License, f)
	if e != nil {
		result(w, nil, e)
		return
	}
	if a.Digest == old.Digest {
		result(w, old, nil)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		result(w, nil, e)
		return
	}
	defer tx.Rollback()
	tables := []string{"onair_looks", "onair_families", "onair_reels"}
	if ok, _ := s.hasChannelAssets(r.Context()); ok {
		tables = append(tables, "livetv_channels")
	}
	for _, table := range tables {
		if _, e = tx.ExecContext(r.Context(), "UPDATE "+table+" SET data=REPLACE(data,?,?) WHERE data LIKE ?", old.Digest, a.Digest, "%"+old.Digest+"%"); e != nil {
			result(w, nil, e)
			return
		}
	}
	for _, q := range []string{"INSERT INTO onair_look_assets(look_id,digest) SELECT look_id,? FROM onair_look_assets WHERE digest=? ON CONFLICT(look_id,digest) DO NOTHING", "UPDATE onair_reel_clips SET source_digest=? WHERE source_digest=?"} {
		if _, e = tx.ExecContext(r.Context(), q, a.Digest, old.Digest); e != nil {
			result(w, nil, e)
			return
		}
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM onair_look_assets WHERE digest=?", old.Digest); e == nil {
		_, e = tx.ExecContext(r.Context(), "DELETE FROM onair_assets WHERE digest=?", old.Digest)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		result(w, nil, e)
		return
	}
	if s.o.Changed != nil {
		_ = s.o.Changed(r.Context())
	}
	v, e := s.libraryAsset(r.Context(), a.Digest)
	result(w, v, e)
}
func (s *Service) trimLibraryAsset(w http.ResponseWriter, r *http.Request) {
	a, e := s.libraryAsset(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		result(w, nil, e)
		return
	}
	var p struct {
		Start, End float64
		Name       string
	}
	if !body(w, r, &p) {
		return
	}
	if s.o.FFmpeg == nil || (a.Kind != "audio" && a.Kind != "video") || math.IsNaN(p.Start) || math.IsNaN(p.End) || p.Start < 0 || p.End <= p.Start || p.End-p.Start > 30 || p.End > float64(a.DurationMs)/1000 {
		result(w, nil, fmt.Errorf("invalid clip interval"))
		return
	}
	var b []byte
	if e = s.db.QueryRowContext(r.Context(), "SELECT content FROM onair_assets WHERE digest=?", a.Digest).Scan(&b); e != nil {
		result(w, nil, e)
		return
	}
	f, e := os.CreateTemp(s.o.Dir, "library-trim-*")
	if e != nil {
		result(w, nil, e)
		return
	}
	defer os.Remove(f.Name())
	_, _ = f.Write(b)
	_ = f.Close()
	ext := ".mp4"
	if a.Kind == "audio" {
		ext = ".m4a"
	}
	out := f.Name() + ext
	defer os.Remove(out)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	args := []string{"-nostdin", "-y", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-i", f.Name(), "-ss", fmt.Sprint(p.Start), "-t", fmt.Sprint(p.End - p.Start), "-threads", "1"}
	if a.Kind == "audio" {
		args = append(args, "-vn", "-c:a", "aac")
	} else {
		args = append(args, "-c:v", "libx264", "-c:a", "aac")
	}
	e = exec.CommandContext(ctx, s.o.FFmpeg(), append(args, out)...).Run()
	if e != nil {
		result(w, nil, e)
		return
	}
	file, e := os.Open(out)
	if e != nil {
		result(w, nil, e)
		return
	}
	defer file.Close()
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = a.Name
	}
	asset, e := s.UploadAsset(r.Context(), name, a.Kind, a.License, io.LimitReader(file, assetLimit+1))
	if e != nil {
		result(w, nil, e)
		return
	}
	v, e := s.libraryAsset(r.Context(), asset.Digest)
	result(w, v, e)
}
