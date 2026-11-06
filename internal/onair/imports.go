package onair

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/go-chi/chi/v5"
)

// Import jobs share the bounded background queue with look preparation. No
// network request or media probe runs on the console request goroutine.
type ImportRequest struct {
	URL  string   `json:"url"`
	URLs []string `json:"urls"`
	Name string   `json:"name"`
	Kind string   `json:"kind"`
}
type ImportJob struct {
	UseSourceTitle bool    `json:"useSourceTitle,omitempty"`
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	Attribution    string  `json:"attribution,omitempty"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	Status         string  `json:"status"`
	Progress       float64 `json:"progress"`
	Error          string  `json:"error,omitempty"`
}

func publicImportURL(ctx context.Context, raw string) error {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("a public HTTP media link is required")
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve media host")
	}
	for _, a := range ips {
		ip := a.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("private media hosts are not allowed")
		}
	}
	return nil
}
func importClient() *http.Client {
	c := egress.Client(egress.CapPlugins, 10*time.Minute)
	tr := c.Transport.(*http.Transport)
	dial := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, e := dial(ctx, network, addr)
		if e != nil {
			return nil, e
		}
		// RoundTrip records the real request's outbound proxy decision.
		proxied, _ := ctx.Value(importProxyKey{}).(bool)
		if !proxied {
			if a, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || !a.IP.IsGlobalUnicast() || a.IP.IsPrivate() || a.IP.IsLoopback() {
				conn.Close()
				return nil, fmt.Errorf("private media peer refused")
			}
		}
		return conn, nil
	}
	c.Transport = importTransport{tr}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return publicImportURL(r.Context(), r.URL.String())
	}
	return c
}

type importProxyKey struct{}
type importTransport struct{ base *http.Transport }

func (t importTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := publicImportURL(r.Context(), r.URL.String()); e != nil {
		return nil, e
	}
	proxy, e := t.base.Proxy(r)
	if e != nil {
		return nil, e
	}
	return t.base.RoundTrip(r.WithContext(context.WithValue(r.Context(), importProxyKey{}, proxy != nil)))
}
func (t importTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
func importGET(ctx context.Context, c *http.Client, raw string) (*http.Response, error) {
	if e := publicImportURL(ctx, raw); e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, e
	}
	resp, e := c.Do(r)
	if e != nil {
		return nil, fmt.Errorf("media request failed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("media returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// resolveArchive accepts an explicit metadata endpoint for offline fake-server
// tests. Production always uses archive.org over the same guarded HTTP client.
func resolveArchive(ctx context.Context, c *http.Client, endpoint, item string) (string, string, string, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", endpoint+"/metadata/"+url.PathEscape(item), nil)
	if e != nil {
		return "", "", "", e
	}
	resp, e := c.Do(r)
	if e != nil {
		return "", "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", fmt.Errorf("archive metadata unavailable")
	}
	var v struct {
		Metadata struct {
			Title   string `json:"title"`
			License string `json:"licenseurl"`
		} `json:"metadata"`
		Files []struct {
			Name   string `json:"name"`
			Format string `json:"format"`
			Size   string `json:"size"`
		} `json:"files"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v); e != nil {
		return "", "", "", e
	}
	sort.SliceStable(v.Files, func(i, j int) bool {
		score := func(name, format string) int {
			if strings.HasSuffix(strings.ToLower(name), ".mp4") {
				if strings.Contains(strings.ToLower(format), "h.264") {
					return 3
				}
				return 2
			}
			return 0
		}
		return score(v.Files[i].Name, v.Files[i].Format) > score(v.Files[j].Name, v.Files[j].Format)
	})
	for _, f := range v.Files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".mp4") {
			return endpoint + "/download/" + url.PathEscape(item) + "/" + url.PathEscape(f.Name), v.Metadata.Title, strings.TrimSpace(v.Metadata.Title + " · " + v.Metadata.License), nil
		}
	}
	return "", "", "", fmt.Errorf("archive item has no MP4 video")
}
func (s *Service) saveImport(ctx context.Context, reel string, j ImportJob) error {
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, "UPDATE onair_imports SET data=? WHERE id=? AND reel_id=?", string(b), j.ID, reel)
	return e
}
func (s *Service) imports(ctx context.Context, reel string) ([]ImportJob, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT data FROM onair_imports WHERE reel_id=? ORDER BY id", reel)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ImportJob{}
	for rows.Next() {
		var b string
		var j ImportJob
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &j); e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Service) importClips(ctx context.Context, r Reel) (Reel, error) {
	jobs, e := s.imports(ctx, r.ID)
	for _, j := range jobs {
		found := false
		for _, c := range r.Clips {
			if c.ID == j.ID {
				found = true
			}
		}
		if found {
			continue
		}
		r.Clips = append(r.Clips, ReelClip{ID: j.ID, Name: j.Name, Source: j.Source, Attribution: j.Attribution, Status: j.Status, Progress: j.Progress, Error: j.Error})
	}
	return r, e
}
func (s *Service) importAssets(ctx context.Context, out []Asset) ([]Asset, error) {
	jobs, e := s.imports(ctx, "")
	for _, j := range jobs {
		out = append(out, Asset{Digest: j.ID, Name: j.Name, Kind: j.Kind, Source: j.Source, Attribution: j.Attribution, Status: j.Status, Progress: j.Progress, Error: j.Error, UsedBy: []string{}})
	}
	return out, e
}
func (s *Service) Import(ctx context.Context, reel string, in ImportRequest) ([]ImportJob, error) {
	urls := append([]string(nil), in.URLs...)
	if in.URL != "" {
		urls = append(urls, in.URL)
	}
	if len(in.Name) > 255 {
		return nil, fmt.Errorf("name maximum 255 characters")
	}
	if len(urls) == 0 || len(urls) > 50 {
		return nil, fmt.Errorf("import between 1 and 50 links")
	}
	if reel != "" {
		if _, e := s.Reel(ctx, reel); e != nil {
			return nil, e
		}
	} else if in.Kind != "image" && in.Kind != "audio" && in.Kind != "video" {
		return nil, fmt.Errorf("choose image, audio or video")
	}
	// Parse only here; DNS resolution belongs to the background job.
	for _, raw := range urls {
		u, e := url.Parse(raw)
		if len(raw) > 8192 || e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("invalid HTTP media link")
		}
	}
	out := []ImportJob{}
	for _, raw := range urls {
		j := ImportJob{UseSourceTitle: in.Name == "", ID: newID(), Source: raw, Name: in.Name, Kind: in.Kind, Status: "queued"}
		if j.Name == "" {
			u, _ := url.Parse(raw)
			j.Name = path.Base(u.Path)
		}
		b, _ := json.Marshal(j)
		if _, e := s.db.ExecContext(ctx, "INSERT INTO onair_imports(id,reel_id,data) VALUES(?,?,?)", j.ID, reel, string(b)); e != nil {
			return nil, e
		}
		job := j
		if !s.enqueue(renderJob{download: true, Hash: "import-" + j.ID, prepare: func(ctx context.Context) error { return s.runImport(ctx, reel, job) }}) {
			j.Status = "error"
			j.Error = "import queue full"
			s.saveImport(ctx, reel, j)
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *Service) runImport(ctx context.Context, reel string, j ImportJob) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	defer func() {
		if err != nil {
			j.Status = "error"
			j.Error = err.Error()
			s.saveImport(context.WithoutCancel(ctx), reel, j)
		}
	}()
	j.Status = "downloading"
	s.saveImport(ctx, reel, j)
	c := importClient()
	defer c.CloseIdleConnections()
	raw := j.Source
	u, _ := url.Parse(raw)
	if strings.EqualFold(u.Hostname(), "archive.org") && strings.HasPrefix(u.Path, "/details/") {
		item := strings.Split(strings.TrimPrefix(u.Path, "/details/"), "/")[0]
		var title string
		raw, title, j.Attribution, err = resolveArchive(ctx, c, "https://archive.org", item)
		if err != nil {
			return err
		}
		if j.UseSourceTitle {
			j.Name = title
		}
	}
	resp, e := importGET(ctx, c, raw)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	mt, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil {
		return fmt.Errorf("invalid media content type")
	}
	kind := j.Kind
	if reel != "" {
		kind = "video"
	}
	if !strings.HasPrefix(mt, kind+"/") && mt != "application/octet-stream" {
		return fmt.Errorf("link is not %s media", kind)
	}
	limit := int64(assetLimit)
	if reel != "" {
		limit = 500 << 20
	}
	if resp.ContentLength > limit {
		return fmt.Errorf("media exceeds size cap")
	}
	f, e := os.CreateTemp(s.o.Dir, ".import-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, e := copyImport(f, resp.Body, limit, resp.ContentLength, func(progress float64) { j.Progress = progress; s.saveImport(ctx, reel, j) })
	if e != nil {
		return e
	}
	if n == 0 {
		return fmt.Errorf("empty media")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	var exists int
	if e = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onair_imports WHERE id=?", j.ID).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	if reel != "" {
		_, e = s.uploadClip(ctx, reel, j.Name, j.Source, j.Attribution, f, j.ID)
	} else {
		var a Asset
		a, e = s.UploadAsset(ctx, j.Name, j.Kind, j.Attribution, f)
		if e == nil {
			_, e = s.db.ExecContext(ctx, "UPDATE onair_assets SET source=?,attribution=? WHERE digest=?", j.Source, j.Attribution, a.Digest)
		}
	}
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, "DELETE FROM onair_imports WHERE id=?", j.ID)
	return e
}
func (s *Service) importHandler(reel bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in ImportRequest
		if !body(w, r, &in) {
			return
		}
		id := ""
		if reel {
			id = chi.URLParam(r, "id")
		}
		jobs, e := s.Import(r.Context(), id, in)
		if e != nil {
			result(w, nil, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(jobs)
	}
}

func copyImport(dst io.Writer, src io.Reader, limit, total int64, progress func(float64)) (int64, error) {
	if total > limit {
		return 0, fmt.Errorf("media exceeds size cap")
	}
	var n int64
	buf := make([]byte, 64<<10)
	last := time.Now()
	for {
		nr, re := src.Read(buf)
		if nr > 0 {
			n += int64(nr)
			if n > limit {
				return n, fmt.Errorf("media exceeds size cap")
			}
			if _, e := dst.Write(buf[:nr]); e != nil {
				return n, e
			}
			if total > 0 && time.Since(last) > time.Second {
				progress(99 * float64(n) / float64(total))
				last = time.Now()
			}
		}
		if re == io.EOF {
			return n, nil
		}
		if re != nil {
			return n, re
		}
	}
}

func (s *Service) resumeImports() {
	rows, e := s.db.QueryContext(s.ctx, "SELECT reel_id,data FROM onair_imports")
	if e != nil {
		return
	}
	type saved struct {
		reel string
		j    ImportJob
	}
	var jobs []saved
	for rows.Next() {
		var v saved
		var b string
		if rows.Scan(&v.reel, &b) == nil && json.Unmarshal([]byte(b), &v.j) == nil && v.j.Status != "error" {
			jobs = append(jobs, v)
		}
	}
	rows.Close()
	for _, v := range jobs {
		if !s.enqueue(renderJob{download: true, Hash: "import-" + v.j.ID, prepare: func(ctx context.Context) error { return s.runImport(ctx, v.reel, v.j) }}) {
			v.j.Status = "error"
			v.j.Error = "import queue full"
			s.saveImport(s.ctx, v.reel, v.j)
		}
	}
}
