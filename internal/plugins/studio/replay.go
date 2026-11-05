// Package studio provides the plugin Studio replay runner (WS11c) and the
// Studio and extensibility MCP and agent tools (WS11h).
package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

// ReplayKind names which replay mode to run.
type ReplayKind string

const (
	ReplayRanking  ReplayKind = "ranking"
	ReplayEvents   ReplayKind = "events"
	ReplayList     ReplayKind = "list"
	ReplayChannels ReplayKind = "channels"
)

// Replay bounds keep every replay background-safe with no request-path impact.
const (
	DefaultReplayTimeout = 15 * time.Second
	MaxReplayTimeout     = 30 * time.Second

	DefaultRankingTraces = 1000
	MaxRankingTraces     = 5000
	MaxChangedPreview    = 200

	DefaultEventDays  = 7
	MaxEventDays      = 7
	DefaultEventLimit = 500
	MaxEventLimit     = 2000

	DefaultListPages = 2
	MaxListPages     = 5
	MaxListPreview   = 25

	DefaultChannelProbes = 5
	MaxChannelProbes     = 5

	maxRequestBytes  = 2 << 20
	maxManifestBytes = 256 << 10
	maxFileBytes     = 512 << 10
	maxFilesCount    = 16
)

// DevInstance is an unsaved or in-progress plugin from the Studio editor.
type DevInstance struct {
	Manifest json.RawMessage   `json:"manifest"`
	Entry    string            `json:"entry,omitempty"`
	Files    map[string]string `json:"files,omitempty"`
	Settings map[string]any    `json:"settings,omitempty"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	Grants   plugins.Grants    `json:"grants,omitempty"`
}

// ReplayParams carries mode-specific parameters for a replay run.
type ReplayParams struct {
	// Ranking and rule-condition parameters.
	StepKey      string               `json:"stepKey,omitempty"`
	ConditionKey string               `json:"conditionKey,omitempty"`
	Operator     string               `json:"operator,omitempty"`
	Value        json.RawMessage      `json:"value,omitempty"`
	Action       string               `json:"action,omitempty"`
	Position     *int                 `json:"position,omitempty"`
	AfterStep    string               `json:"afterStep,omitempty"`
	Config       json.RawMessage      `json:"config,omitempty"`
	Limit        int                  `json:"limit,omitempty"`
	Profile      *core.QualityProfile `json:"profile,omitempty"`

	// Event replay parameters.
	Days   int      `json:"days,omitempty"`
	Events []string `json:"events,omitempty"`

	// List source replay parameters.
	SourceKey string `json:"sourceKey,omitempty"`
	Catalog   string `json:"catalog,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Pages     int    `json:"pages,omitempty"`

	// Channel replay parameters.
	ProbeLimit int `json:"probeLimit,omitempty"`
}

// ReplayRequest is the input to POST /api/plugins/studio/replay and Runner.Replay.
type ReplayRequest struct {
	Plugin      string       `json:"plugin,omitempty"`
	DevInstance *DevInstance `json:"devInstance,omitempty"`
	Kind        ReplayKind   `json:"kind"`
	Params      ReplayParams `json:"params,omitempty"`
}

// ReplayReport is the structured output of one Studio replay run.
type ReplayReport struct {
	PluginID   string               `json:"pluginId"`
	Kind       ReplayKind           `json:"kind"`
	DurationMS int64                `json:"durationMs"`
	TimedOut   bool                 `json:"timedOut,omitempty"`
	Ranking    *RankingReplayReport `json:"ranking,omitempty"`
	Events     *EventReplayReport   `json:"events,omitempty"`
	List       *ListReplayReport    `json:"list,omitempty"`
	Channels   *ChannelReplayReport `json:"channels,omitempty"`
}

// ReplayDeps configures a Studio replay Runner.
type ReplayDeps struct {
	DB      *sql.DB
	Manager *plugins.Manager
	Engine  *wasm.Engine
	Logger  *slog.Logger
	Now     func() time.Time
	Timeout time.Duration

	// Profiles returns configured quality profiles so ranking replay can
	// look up a trace's profile by name. When nil, a default profile is used.
	Profiles func() []core.QualityProfile

	// CatalogMatch checks whether a list item matches a title in Ether's
	// catalogue. When nil, Runner falls back to searching the database if
	// a catalogue table is present.
	CatalogMatch func(ctx context.Context, item ListItem) (MatchedTitle, bool)

	// ProbeStream probes one Live TV channel stream URL. When nil, Runner
	// uses a bounded HTTP/URL probe.
	ProbeStream func(ctx context.Context, rawURL string, headers map[string]string) StreamProbeResult

	// HTTPClient is used by the default stream prober when ProbeStream is nil.
	HTTPClient *http.Client
}

// Runner executes bounded dry-run replays for installed plugins and Studio
// dev instances. It is safe for concurrent use; replays are serialized by a
// small semaphore so background runs cannot starve request paths.
type Runner struct {
	deps    ReplayDeps
	log     *slog.Logger
	now     func() time.Time
	timeout time.Duration
	sem     chan struct{}

	engMu     sync.Mutex
	ownEngine *wasm.Engine
}

// NewRunner builds a Studio replay runner.
func NewRunner(deps ReplayDeps) *Runner {
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	timeout := deps.Timeout
	if timeout <= 0 {
		timeout = DefaultReplayTimeout
	}
	if timeout > MaxReplayTimeout {
		timeout = MaxReplayTimeout
	}
	return &Runner{
		deps:    deps,
		log:     log,
		now:     now,
		timeout: timeout,
		sem:     make(chan struct{}, 2),
	}
}

// Close releases any engine the Runner created internally.
func (r *Runner) Close() error {
	r.engMu.Lock()
	defer r.engMu.Unlock()
	if r.ownEngine != nil {
		err := r.ownEngine.Close()
		r.ownEngine = nil
		return err
	}
	return nil
}

func (r *Runner) engine() *wasm.Engine {
	if r.deps.Engine != nil {
		return r.deps.Engine
	}
	r.engMu.Lock()
	defer r.engMu.Unlock()
	if r.ownEngine == nil {
		r.ownEngine = wasm.NewEngine(r.log)
	}
	return r.ownEngine
}

func normalizeKind(k ReplayKind) (ReplayKind, error) {
	switch strings.ToLower(strings.TrimSpace(string(k))) {
	case "ranking", "ranking_step", "rule_condition":
		return ReplayRanking, nil
	case "events", "event", "notify":
		return ReplayEvents, nil
	case "list", "list_source", "catalog":
		return ReplayList, nil
	case "channels", "channel", "livetv", "livetv_channels":
		return ReplayChannels, nil
	default:
		return "", fmt.Errorf("unsupported replay kind %q", k)
	}
}

// Replay runs a bounded replay and returns its structured report.
func (r *Runner) Replay(ctx context.Context, req ReplayRequest) (ReplayReport, error) {
	kind, err := normalizeKind(req.Kind)
	if err != nil {
		return ReplayReport{}, err
	}
	if strings.TrimSpace(req.Plugin) == "" && req.DevInstance == nil {
		return ReplayReport{}, errors.New("choose an installed plugin or provide a devInstance")
	}
	if strings.TrimSpace(req.Plugin) != "" && req.DevInstance != nil {
		return ReplayReport{}, errors.New("provide either plugin or devInstance, not both")
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	select {
	case r.sem <- struct{}{}:
		defer func(sem chan struct{}) { <-sem }(r.sem)
	case <-runCtx.Done():
		return ReplayReport{}, errors.New("another replay is already running; try again in a moment")
	}

	// Each replay owns its lazily created engine. Caller-supplied engines
	// remain caller-owned; the shared runner retains admission control.
	worker := NewRunner(r.deps)
	defer worker.Close()
	r = worker
	start := time.Now()
	target, err := r.resolveTarget(runCtx, req)
	if err != nil {
		return ReplayReport{}, err
	}
	defer target.close()

	report := ReplayReport{
		PluginID: target.manifest.ID,
		Kind:     kind,
	}

	switch kind {
	case ReplayRanking:
		rk, err := r.replayRanking(runCtx, target, req.Params)
		if err != nil {
			return ReplayReport{}, err
		}
		report.Ranking = rk
	case ReplayEvents:
		ev, err := r.replayEvents(runCtx, target, req.Params)
		if err != nil {
			return ReplayReport{}, err
		}
		report.Events = ev
	case ReplayList:
		ls, err := r.replayList(runCtx, target, req.Params)
		if err != nil {
			return ReplayReport{}, err
		}
		report.List = ls
	case ReplayChannels:
		ch, err := r.replayChannels(runCtx, target, req.Params)
		if err != nil {
			return ReplayReport{}, err
		}
		report.Channels = ch
	}

	report.DurationMS = time.Since(start).Milliseconds()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		report.TimedOut = true
	}
	return report, nil
}

// resolvedTarget holds the manifest, dry-run host, and loaded runtime for one
// replay execution.
type resolvedTarget struct {
	id        string
	manifest  plugins.Manifest
	files     map[string][]byte
	settings  map[string]any
	secrets   map[string]string
	grants    plugins.Grants
	dryHost   *dryRunHost
	wasmPlug  *wasm.Plugin
	installed bool
	mgr       *plugins.Manager
}

func (t *resolvedTarget) close() {
	if t.wasmPlug != nil {
		_ = t.wasmPlug.Close()
	}
}

func (r *Runner) resolveTarget(ctx context.Context, req ReplayRequest) (*resolvedTarget, error) {
	if req.DevInstance != nil {
		return r.resolveDevInstance(req.DevInstance)
	}
	id := strings.TrimSpace(req.Plugin)
	if r.deps.Manager == nil && r.deps.DB == nil {
		return nil, errors.New("plugin manager is not configured")
	}
	var manifest plugins.Manifest
	var version string
	if r.deps.Manager != nil {
		rec, err := r.deps.Manager.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		manifest = rec.Manifest
		version = rec.Version
	} else {
		var raw string
		err := r.deps.DB.QueryRowContext(ctx, `SELECT version, manifest FROM plugins_installed WHERE id = ?`, id).Scan(&version, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, plugins.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		m, err := plugins.ParseManifest([]byte(raw))
		if err != nil {
			return nil, err
		}
		manifest = m
	}

	files := r.loadArtifactFiles(ctx, id, version)
	kvSeed := r.loadKVSeed(ctx, id)
	dryHost := newDryRunHost(manifest, plugins.Grants{}, nil, kvSeed, r.deps)

	target := &resolvedTarget{
		id:        id,
		manifest:  manifest,
		files:     files,
		dryHost:   dryHost,
		installed: true,
		mgr:       r.deps.Manager,
	}

	if manifest.Ether != nil && manifest.Ether.Runtime.Kind == "hosted" && len(files) > 0 {
		wp, err := r.openWASMPlugin(manifest, files, dryHost)
		if err != nil {
			return nil, err
		}
		target.wasmPlug = wp
	}
	return target, nil
}

func (r *Runner) resolveDevInstance(dev *DevInstance) (*resolvedTarget, error) {
	if len(dev.Manifest) == 0 {
		return nil, errors.New("devInstance.manifest is required")
	}
	if len(dev.Manifest) > maxManifestBytes {
		return nil, errors.New("devInstance.manifest exceeds maximum size")
	}
	manifest, err := plugins.ParseManifest(dev.Manifest)
	if err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if len(dev.Files) > maxFilesCount {
		return nil, fmt.Errorf("at most %d files are allowed in a devInstance", maxFilesCount)
	}
	files := make(map[string][]byte, len(dev.Files)+1)
	totalBytes := 0
	for k, v := range dev.Files {
		totalBytes += len(v)
		if totalBytes > maxFileBytes {
			return nil, errors.New("devInstance files exceed maximum size")
		}
		files[k] = []byte(v)
	}
	if dev.Entry != "" && manifest.Ether != nil {
		entryPath := manifest.Ether.Runtime.Entry
		if entryPath == "" {
			entryPath = "plugin.ts"
			manifest.Ether.Runtime.Entry = entryPath
		}
		if _, exists := files[entryPath]; !exists {
			if len(dev.Entry) > maxFileBytes {
				return nil, errors.New("devInstance.entry exceeds maximum size")
			}
			files[entryPath] = []byte(dev.Entry)
		}
	}

	dryHost := newDryRunHost(manifest, dev.Grants, dev.Secrets, nil, r.deps)
	target := &resolvedTarget{
		id:       manifest.ID,
		manifest: manifest,
		files:    files,
		settings: dev.Settings,
		secrets:  dev.Secrets,
		grants:   dev.Grants,
		dryHost:  dryHost,
	}

	if manifest.Ether != nil && manifest.Ether.Runtime.Kind == "hosted" {
		wp, err := r.openWASMPlugin(manifest, files, dryHost)
		if err != nil {
			return nil, err
		}
		target.wasmPlug = wp
	}
	return target, nil
}

func (r *Runner) openWASMPlugin(manifest plugins.Manifest, files map[string][]byte, host *dryRunHost) (*wasm.Plugin, error) {
	if manifest.Ether == nil {
		return nil, errors.New("hosted plugins need an ether block")
	}
	decl := manifest.Ether.Runtime
	eng := r.engine()
	lim := plugins.HotLimits(manifest)
	switch {
	case decl.Module != "":
		mod, ok := files[decl.Module]
		if !ok {
			return nil, fmt.Errorf("module %q is missing", decl.Module)
		}
		return eng.LoadWASM(manifest.ID, manifest.Version, mod, host, lim)
	case decl.Entry != "":
		src, ok := files[decl.Entry]
		if !ok {
			return nil, fmt.Errorf("entry %q is missing", decl.Entry)
		}
		js := string(src)
		if ext := strings.ToLower(path.Ext(decl.Entry)); ext == ".ts" || ext == ".tsx" || ext == ".mts" {
			compileFiles := make(map[string]string, len(files))
			for k, v := range files {
				compileFiles[k] = string(v)
			}
			out, _, diags := wasm.Compile(compileFiles, decl.Entry)
			var errs []string
			for _, d := range diags {
				if d.Severity == "error" {
					errs = append(errs, d.Text)
				}
			}
			if len(errs) > 0 {
				return nil, fmt.Errorf("compile: %s", strings.Join(errs, "; "))
			}
			js = string(out)
		}
		return eng.LoadJS(manifest.ID, manifest.Version, js, host, lim)
	default:
		return nil, errors.New("hosted runtime needs an entry or a module")
	}
}

func (r *Runner) loadArtifactFiles(ctx context.Context, pluginID, version string) map[string][]byte {
	if r.deps.DB == nil {
		return nil
	}
	rows, err := r.deps.DB.QueryContext(ctx, `SELECT path, content FROM plugins_artifacts WHERE plugin = ? AND version = ?`, pluginID, version)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck
	out := map[string][]byte{}
	for rows.Next() {
		var p string
		var b []byte
		if rows.Scan(&p, &b) == nil {
			out[p] = b
		}
	}
	return out
}

func (r *Runner) loadKVSeed(ctx context.Context, pluginID string) map[string][]byte {
	if r.deps.DB == nil {
		return nil
	}
	rows, err := r.deps.DB.QueryContext(ctx, `SELECT kkey, value FROM plugins_kv WHERE plugin = ?`, pluginID)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck
	out := map[string][]byte{}
	for rows.Next() {
		var k string
		var v []byte
		if rows.Scan(&k, &v) == nil {
			out[k] = append([]byte(nil), v...)
		}
	}
	return out
}

// call handles a request against either the isolated wasm.Plugin or, for
// non-hosted installed plugins, Manager.Call.
func (t *resolvedTarget) call(ctx context.Context, req plugins.Request) (plugins.Response, wasm.Outcome, error) {
	if t.wasmPlug != nil {
		q := make(map[string][]string, len(req.Query))
		for k, v := range req.Query {
			q[k] = []string{v}
		}
		wresp, out, err := t.wasmPlug.CallDetailed(ctx, wasm.Request{
			Method:  req.Method,
			Path:    req.Path,
			Query:   q,
			Headers: req.Headers,
			Body:    req.Body,
		})
		if err != nil {
			return plugins.Response{}, out, err
		}
		return plugins.Response{
			Status:  wresp.Status,
			Headers: wresp.Headers,
			Body:    unwrapResponseBody(wresp.Body),
		}, out, nil
	}
	if t.installed && t.mgr != nil {
		start := time.Now()
		resp, err := t.mgr.Call(ctx, t.id, req)
		out := wasm.Outcome{Status: wasm.StatusOK, Duration: time.Since(start)}
		if err != nil {
			out.Status = wasm.StatusError
			return plugins.Response{}, out, err
		}
		resp.Body = unwrapResponseBody(resp.Body)
		return resp, out, nil
	}
	return plugins.Response{}, wasm.Outcome{Status: wasm.StatusError}, errors.New("no runtime available for plugin")
}

// unwrapResponseBody normalizes a response body that may be either a raw JSON
// object/array or a JSON-encoded string containing JSON.
func unwrapResponseBody(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		trimmed := strings.TrimSpace(str)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			return json.RawMessage(trimmed)
		}
	}
	return raw
}
