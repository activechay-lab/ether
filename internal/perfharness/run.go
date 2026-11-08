package perfharness

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// result is one timed request.
type Result struct {
	Phase   string        `json:"phase"`
	Client  string        `json:"client"`
	Surface string        `json:"surface"`
	Step    string        `json:"step"`
	Class   string        `json:"class"`
	Viewer  string        `json:"viewer"`
	Tag     string        `json:"tag"`
	Status  int           `json:"status"`
	Bytes   int           `json:"bytes"`
	BodySHA string        `json:"bodySha256,omitempty"`
	Dur     time.Duration `json:"dur"`
	Err     string        `json:"err,omitempty"`
	Skipped string        `json:"skipped,omitempty"`
	// RenderedItems counts the titles a listing response rendered, by type.
	RenderedItems map[string]int `json:"renderedItems,omitempty"`
	// Set when the runner measures (Measure): the bytes on the wire, the
	// body's size gzipped at the default level, and the server's CPU time
	// and block IO while it answered.
	WireBytes  int           `json:"wireBytes,omitempty"`
	GzipBytes  int           `json:"gzipBytes,omitempty"`
	CPU        time.Duration `json:"cpu,omitempty"`
	ReadBytes  int64         `json:"readBytes,omitempty"`
	WriteBytes int64         `json:"writeBytes,omitempty"`
	Probed     bool          `json:"probed,omitempty"`
	// Items and TotalSize are a Plex MediaContainer's size and totalSize
	// (measure mode), so an empty page is not mistaken for a fast one.
	Items     int `json:"items,omitempty"`
	TotalSize int `json:"totalSize,omitempty"`
}

// requestTimeout caps one replayed request; a path that scales with the
// library fails its budget long before this.
const requestTimeout = 60 * time.Second

type Runner struct {
	host     string
	basePort int
	// shared sends every protocol to the base port under its shared path
	// prefix (/jellyfin, /emby, /silo) instead of its own port.
	shared  bool
	http    *http.Client
	viewers []Viewer
	owner   Viewer

	// before, when set, runs before each request (the gate's quiesce).
	before func(context.Context)

	mu       sync.Mutex
	results  []Result
	sessions map[string]*session // viewer/client
	seq      atomic.Int64
	// bodies, when set, receives the last warm response body of each
	// viewer, client and step (SaveBodies).
	bodies string
	// measure, when set, records wire and gzip sizes, and probe samples the
	// server around each request (Measure).
	measure bool
	probe   Probe
}

// SetHost sends requests to host instead of 127.0.0.1, for a server in a
// container or on another machine.
func (r *Runner) SetHost(host string) { r.host = host }

// Measure records each response's size on the wire and gzipped, and, when
// probe is not nil, the server's CPU time and block IO during each request.
// The probe's counters are server-wide, so it is meaningful only while one
// request runs at a time (Sequential, ColdEach).
func (r *Runner) Measure(probe Probe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.measure, r.probe = true, probe
}

// SaveBodies writes the last warm response of each viewer, client and step
// to dir, so two builds' answers can be compared once personalization has
// settled.
func (r *Runner) SaveBodies(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = dir
}

func (r *Runner) saveBody(res Result, body []byte) {
	if res.Phase != "warm" || res.Err != "" {
		return
	}
	name := res.Viewer + "__" + res.Client + "__" + res.Step
	r.mu.Lock()
	dir := r.bodies
	r.mu.Unlock()
	if dir == "" {
		return
	}
	safe := strings.Map(func(c rune) rune {
		if c == '/' || c == ' ' || c == ':' || c == '?' || c == '&' || c == '=' {
			return '_'
		}
		return c
	}, name)
	_ = os.WriteFile(filepath.Join(dir, safe+".body"), body, 0o600)
}

// NewRunner drives a server whose main port is basePort. With shared set,
// Jellyfin, Emby and Silo are reached under their path prefixes on that
// port; otherwise on basePort+1, +2 and +3.
func NewRunner(basePort int, shared bool, viewers []Viewer) *Runner {
	tr := &http.Transport{MaxIdleConnsPerHost: 64, MaxConnsPerHost: 0, IdleConnTimeout: 90 * time.Second}
	return &Runner{host: "127.0.0.1", basePort: basePort, shared: shared, viewers: viewers, owner: viewers[0], sessions: map[string]*session{},
		http: &http.Client{Transport: tr, Timeout: requestTimeout}}
}

func (r *Runner) session(v Viewer, c Client) *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := v.Username + "/" + c.Name
	s := r.sessions[key]
	if s == nil {
		s = &session{Viewer: v, vars: map[string]string{
			"username": v.Username, "password": Password, "stremio": queryEscape(v.Stremio),
			"ownerToken": r.owner.PlexToken, "viewerId": strconv.FormatInt(v.ID, 10),
			"xtreamUser": frontend.XtreamUsername(v.Xtream), "xtreamPass": url.QueryEscape(v.Xtream), "mcp": v.MCP,
		}}
		r.sessions[key] = s
	}
	return s
}

// runClient drives one client's whole sequence for one viewer.
func (r *Runner) runClient(ctx context.Context, phase string, c Client, v Viewer) {
	s := r.session(v, c)
	// Clients sign in once and keep their token, as real apps do; repeated
	// sign-ins would also trip the sign-in rate limiter.
	r.mu.Lock()
	signedIn := s.vars["token"] != ""
	r.mu.Unlock()
	if c.SignIn != nil && !signedIn {
		r.do(ctx, phase, c, s, *c.SignIn)
	}
	for _, st := range c.Steps {
		if ctx.Err() != nil {
			return
		}
		r.do(ctx, phase, c, s, st)
	}
}

func (r *Runner) do(ctx context.Context, phase string, c Client, s *session, st Step) {
	if r.before != nil {
		r.before(ctx)
	}
	now := time.Now()
	r.mu.Lock()
	s.vars["now"] = strconv.FormatInt(now.Unix(), 10)
	s.vars["now3h"] = strconv.FormatInt(now.Add(3*time.Hour).Unix(), 10)
	vars := make(map[string]string, len(s.vars))
	for k, v := range s.vars {
		vars[k] = v
	}
	r.mu.Unlock()
	tag := phase + "|" + c.Name + "|" + st.Name + "|" + strconv.FormatInt(r.seq.Add(1), 10)
	res := Result{Phase: phase, Client: c.Name, Surface: c.Surface, Step: st.Name, Class: st.Class, Viewer: s.Viewer.Username, Tag: tag}
	path, missing, ok := expand(c.Prefix+st.Path, vars)
	body, missingBody, okBody := expand(st.Body, vars)
	if !ok || !okBody {
		if missing == "" {
			missing = missingBody
		}
		res.Skipped = "missing {" + missing + "}"
		r.record(res)
		return
	}
	method := st.Method
	if method == "" {
		method = http.MethodGet
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	port := r.basePort + c.Port
	if r.shared {
		port, path = r.basePort, c.SharedPrefix+path
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+r.host+":"+strconv.Itoa(port)+path, reader)
	if err != nil {
		res.Err = err.Error()
		r.record(res)
		return
	}
	req.Header = c.Headers(&session{Viewer: s.Viewer, vars: vars})
	req.Header.Set("X-Ether-Perf-Tag", tag)
	r.mu.Lock()
	measure, probe := r.measure, r.probe
	r.mu.Unlock()
	if measure {
		// Asked for explicitly, so the transport leaves the body compressed
		// and the wire size can be counted; real clients ask for gzip too.
		req.Header.Set("Accept-Encoding", "gzip")
	}
	var before Usage
	var probeErr error
	if probe != nil {
		before, probeErr = probe.Sample()
	}
	start := time.Now()
	resp, err := r.http.Do(req)
	if err != nil {
		res.Dur, res.Err = time.Since(start), err.Error()
		r.record(res)
		return
	}
	buf, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	res.Dur = time.Since(start)
	if probe != nil && probeErr == nil {
		if after, err := probe.Sample(); err == nil {
			d := after.Sub(before)
			res.CPU, res.ReadBytes, res.WriteBytes, res.Probed = d.CPU, d.ReadBytes, d.WriteBytes, true
		}
	}
	if measure && err == nil {
		res.WireBytes = len(buf)
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			if zr, zerr := gzip.NewReader(bytes.NewReader(buf)); zerr == nil {
				buf, err = io.ReadAll(zr)
			}
		}
		res.GzipBytes = gzipSize(buf)
		var mc struct {
			MediaContainer struct {
				Size      int `json:"size"`
				TotalSize int `json:"totalSize"`
			} `json:"MediaContainer"`
		}
		if json.Unmarshal(buf, &mc) == nil {
			res.Items, res.TotalSize = mc.MediaContainer.Size, mc.MediaContainer.TotalSize
		}
	}
	res.Status, res.Bytes = resp.StatusCode, len(buf)
	if err == nil {
		res.BodySHA = fmt.Sprintf("%x", sha256.Sum256(buf))
	}
	if err != nil {
		res.Err = err.Error()
	} else if resp.StatusCode >= 400 && st.Class != classPlayback {
		res.Err = strings.TrimSpace(string(bytes.ToValidUTF8(buf[:min(len(buf), 160)], nil)))
	}
	if st.Class == classBrowse && resp.StatusCode < 300 {
		res.RenderedItems = renderedTitles(buf)
	}
	if st.Discover != nil && resp.StatusCode < 300 {
		r.mu.Lock()
		st.Discover(s.vars, buf)
		r.mu.Unlock()
	}
	r.saveBody(res, buf)
	r.record(res)
}

// SetBefore runs fn before each request: the gate waits there for the
// server's background work to finish, so every request starts from the
// same state.
func (r *Runner) SetBefore(fn func(context.Context)) { r.before = fn }

// gzipSize is the size of b gzipped at the default level, so two servers'
// bodies compare the same whether or not they compress.
func gzipSize(b []byte) int {
	var n countWriter
	zw := gzip.NewWriter(&n)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return int(n)
}

type countWriter int

func (c *countWriter) Write(p []byte) (int, error) { *c += countWriter(len(p)); return len(p), nil }

// SetTimeout caps each request (60 s by default).
func (r *Runner) SetTimeout(d time.Duration) { r.http.Timeout = d }

// Results returns every timed request so far.
func (r *Runner) Results() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.results...)
}

func (r *Runner) record(res Result) {
	r.mu.Lock()
	r.results = append(r.results, res)
	r.mu.Unlock()
}

// sequential runs every client for each viewer, one request at a time.
func (r *Runner) Sequential(ctx context.Context, phase string, viewers []Viewer, cs []Client) {
	for _, v := range viewers {
		for _, c := range cs {
			r.runClient(ctx, phase, c, v)
		}
	}
}

// ColdEach replays each client's steps for viewer v one at a time, calling
// restart before every step, so each step's request is the first a freshly
// started server answers. The steps' variables must already be discovered
// (a prior pass under another phase); a step that discovers variables still
// records them.
func (r *Runner) ColdEach(ctx context.Context, phase string, v Viewer, cs []Client, restart func(ctx context.Context, c Client, st Step) error) error {
	for _, c := range cs {
		s := r.session(v, c)
		for _, st := range c.Steps {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := restart(ctx, c, st); err != nil {
				return fmt.Errorf("restart before %s %s: %w", c.Name, st.Name, err)
			}
			r.do(ctx, phase, c, s, st)
		}
	}
	return nil
}

// concurrent runs n workers, each a viewer working through every client in
// its own order, rounds times.
func (r *Runner) Concurrent(ctx context.Context, phase string, n, rounds int, cs []Client) {
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := r.viewers[i%len(r.viewers)]
			order := rand.New(rand.NewPCG(uint64(i), 7)).Perm(len(cs))
			for range rounds {
				for _, j := range order {
					r.runClient(ctx, phase, cs[j], v)
				}
			}
		}(i)
	}
	wg.Wait()
}

// backgroundWriter imitates a catalogue refresh against the same database:
// write transactions of 500 title rows, back to back with a short pause, so
// request paths meet a busy writer the way they do while the mirror syncs.
type BackgroundWriter struct {
	db     *sql.DB
	stop   chan struct{}
	done   chan struct{}
	txs    atomic.Int64
	maxTx  atomic.Int64
	errors atomic.Int64
}

func StartBackgroundWriter(dbPath string) (*BackgroundWriter, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var maxRowID int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM catalog_titles`).Scan(&maxRowID); err != nil {
		db.Close()
		return nil, err
	}
	w := &BackgroundWriter{db: db, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		rng := rand.New(rand.NewPCG(1, 2))
		for {
			select {
			case <-w.stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
			from := rng.Int64N(max(maxRowID-500, 1))
			start := time.Now()
			tx, err := db.Begin()
			if err == nil {
				_, err = tx.Exec(`UPDATE catalog_titles SET popularity=popularity WHERE rowid BETWEEN ? AND ?`, from, from+499)
				if err == nil {
					err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
			}
			if err != nil {
				w.errors.Add(1)
				continue
			}
			w.txs.Add(1)
			if d := int64(time.Since(start)); d > w.maxTx.Load() {
				w.maxTx.Store(d)
			}
		}
	}()
	return w, nil
}

func (w *BackgroundWriter) Close() string {
	close(w.stop)
	<-w.done
	w.db.Close()
	return fmt.Sprintf("%d write transactions of 500 rows (longest %s, %d failed)", w.txs.Load(), time.Duration(w.maxTx.Load()).Round(time.Millisecond), w.errors.Load())
}
