package catalogue

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/httpjson"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

// DefaultRefreshInterval is how often the background loop refreshes the signed index.
const DefaultRefreshInterval = 24 * time.Hour

// Options configures the signed plugin catalogue service.
type Options struct {
	DB        *sql.DB
	Manager   *plugins.Manager
	IndexURL  string
	PublicKey string
	Client    *http.Client
	Log       *slog.Logger
	Now       func() time.Time
}

// Service fetches, verifies, caches, and serves the signed plugin catalogue,
// and reconciles installed plugins for revocations and auto-updates.
type Service struct {
	cache    cacheStore
	mgr      *plugins.Manager
	indexURL string
	pubKey   ed25519.PublicKey
	client   *http.Client
	log      *slog.Logger
	now      func() time.Time

	ingestMu  sync.Mutex
	mu        sync.RWMutex
	idx       Index
	fetchedAt time.Time
	lastErr   string
}

// New constructs a catalogue service and loads any previously cached index
// from the database.
func New(opts Options) (*Service, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Client == nil {
		opts.Client = (&safefetch.Guard{}).Client(egress.Client(egress.CapPlugins, 30*time.Second))
	}
	var pub ed25519.PublicKey
	if strings.TrimSpace(opts.PublicKey) != "" {
		k, err := ParsePublicKey(opts.PublicKey)
		if err != nil {
			return nil, err
		}
		pub = k
	}
	s := &Service{
		cache:    cacheStore{db: opts.DB},
		mgr:      opts.Manager,
		indexURL: strings.TrimSpace(opts.IndexURL),
		pubKey:   pub,
		client:   opts.Client,
		log:      opts.Log,
		now:      opts.Now,
	}
	if idx, t, ok := s.cache.load(context.Background(), pub, s.indexURL); ok && s.Configured() {
		s.idx = idx
		s.fetchedAt = t
	}
	return s, nil
}

// Configured reports whether both an index URL and a public key are set.
func (s *Service) Configured() bool {
	return s.indexURL != "" && len(s.pubKey) == ed25519.PublicKeySize
}

// EnforceCachedRevocations applies the last verified index before plugins start.
// This also closes the gap if Ether stopped after caching an index but before
// its revocations were applied.
func (s *Service) EnforceCachedRevocations(ctx context.Context) error {
	if s.mgr == nil {
		return nil
	}
	s.mu.RLock()
	entries := append([]Entry(nil), s.idx.Entries...)
	s.mu.RUnlock()
	for _, entry := range entries {
		if entry.Revoked {
			if _, err := s.mgr.Revoke(ctx, entry.ID, entry.Reason); err != nil {
				return fmt.Errorf("catalogue: apply cached revocation for %s: %w", entry.ID, err)
			}
		}
	}
	return nil
}

// Run refreshes the catalogue immediately and then on every tick of interval
// (default 24h) until ctx is cancelled.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	if s.Configured() {
		if _, err := s.Refresh(ctx); err != nil {
			s.log.Warn("catalogue initial refresh", "err", err)
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.Configured() {
				continue
			}
			if _, err := s.Refresh(ctx); err != nil {
				s.log.Warn("catalogue daily refresh", "err", err)
			}
		}
	}
}

// Refresh fetches index.json and index.json.sig, verifies the detached
// ed25519 signature, caches the index in the database, and reconciles
// installed plugins (revocations and auto-updates).
func (s *Service) Refresh(ctx context.Context) (Index, error) {
	if !s.Configured() {
		return Index{}, fmt.Errorf("catalogue: index URL or public key is not configured")
	}
	indexBytes, err := s.fetch(ctx, s.indexURL, 8<<20)
	if err != nil {
		s.setErr(err)
		return Index{}, err
	}
	sigBytes, err := s.fetch(ctx, s.sigURL(), 4<<10)
	if err != nil {
		s.setErr(err)
		return Index{}, err
	}
	return s.Ingest(ctx, indexBytes, sigBytes)
}

// Ingest verifies and stores an index and signature directly, then reconciles
// installed plugins.
func (s *Service) Ingest(ctx context.Context, indexBytes, sigBytes []byte) (Index, error) {
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	if len(s.pubKey) != ed25519.PublicKeySize {
		err := fmt.Errorf("catalogue: public key is not configured")
		s.setErr(err)
		return Index{}, err
	}
	idx, err := ParseAndVerify(indexBytes, sigBytes, s.pubKey)
	if err != nil {
		s.setErr(err)
		return Index{}, err
	}
	if _, err := time.Parse(time.RFC3339Nano, idx.GeneratedAt); err != nil {
		err = fmt.Errorf("catalogue: valid generation time required")
		s.setErr(err)
		return Index{}, err
	}
	s.mu.RLock()
	previous := s.idx
	s.mu.RUnlock()
	if err := rejectRollback(previous, idx); err != nil {
		s.setErr(err)
		return Index{}, err
	}
	now := s.now()
	if err := s.cache.save(ctx, idx, indexBytes, strings.TrimSpace(string(sigBytes)), now, s.indexURL, s.pubKey); err != nil {
		s.setErr(err)
		return Index{}, err
	}
	s.mu.Lock()
	s.idx = idx
	s.fetchedAt = now
	s.lastErr = ""
	s.mu.Unlock()

	s.reconcile(ctx, idx)
	return idx, nil
}

func (s *Service) setErr(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
}

func (s *Service) sigURL() string {
	u, err := url.Parse(s.indexURL)
	if err != nil {
		return s.indexURL + ".sig"
	}
	u.Path += ".sig"
	u.RawPath = ""
	return u.String()
}

func (s *Service) fetch(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("catalogue: invalid request URL")
	}
	req.Header.Set("User-Agent", "Ether plugin catalogue")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalogue: request failed")
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalogue: request returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("catalogue: response exceeds %d bytes", max)
	}
	return body, nil
}

// reconcile applies revocations and checks updates for installed plugins.
func (s *Service) reconcile(ctx context.Context, idx Index) {
	if s.mgr == nil {
		return
	}
	byID := make(map[string]Entry, len(idx.Entries))
	for _, e := range idx.Entries {
		byID[e.ID] = e
	}
	recs, err := s.mgr.List(ctx)
	if err != nil {
		return
	}
	for _, rec := range recs {
		entry, inCat := byID[rec.ID]
		if inCat && entry.Revoked {
			if _, err := s.mgr.Revoke(ctx, rec.ID, entry.Reason); err != nil {
				s.log.Warn("catalogue revoke failed", "plugin", rec.ID, "err", err)
			}
			continue
		}
		if !inCat || !rec.Verified {
			continue
		}
		latest, ok := entry.Latest()
		if !ok || plugins.CompareSemVer(latest.Version, rec.Version) <= 0 {
			continue
		}
		if rec.UpdateVersion == latest.Version && !rec.AutoUpdate {
			continue
		}
		if _, _, err := s.mgr.CheckUpdate(ctx, rec.ID); err != nil {
			s.log.Warn("catalogue update check failed", "plugin", rec.ID, "err", err)
		}
	}
}

// Resolve implements plugins.Catalogue.
func (s *Service) Resolve(ctx context.Context, idOrURL string, version string) (plugins.CatalogueArtifact, error) {
	s.mu.RLock()
	idx := s.idx
	fetched := s.fetchedAt
	s.mu.RUnlock()

	if len(idx.Entries) == 0 && fetched.IsZero() && s.Configured() {
		if refreshed, err := s.Refresh(ctx); err == nil {
			idx = refreshed
		}
	}

	key := strings.TrimSpace(idOrURL)
	for _, e := range idx.Entries {
		match := e.ID == key || (e.Repo != "" && strings.EqualFold(e.Repo, key))
		if !match {
			for _, v := range e.Versions {
				if v.URL == key {
					match = true
					if version == "" {
						version = v.Version
					}
					break
				}
			}
		}
		if !match {
			continue
		}
		if e.Revoked {
			reason := strings.TrimSpace(e.Reason)
			if reason == "" {
				reason = "This plugin has been revoked in the catalogue."
			}
			return plugins.CatalogueArtifact{}, fmt.Errorf("%w: %s", plugins.ErrRevoked, reason)
		}
		v, ok := e.FindVersion(version)
		if !ok {
			if version == "" {
				return plugins.CatalogueArtifact{}, fmt.Errorf("catalogue: %s has no published versions", e.ID)
			}
			return plugins.CatalogueArtifact{}, fmt.Errorf("catalogue: %s version %s not found", e.ID, version)
		}
		permissions := e.Permissions
		if v.Permissions != nil {
			permissions = *v.Permissions
		}
		capabilities := make([]plugins.Capability, len(e.Capabilities))
		for i, capability := range e.Capabilities {
			capabilities[i] = plugins.Capability(capability)
		}
		return plugins.CatalogueArtifact{
			ID:           e.ID,
			Version:      v.Version,
			URL:          v.URL,
			SHA256:       v.SHA256,
			Kind:         plugins.Kind(e.Kind),
			Permissions:  permissions,
			Capabilities: capabilities,
		}, nil
	}
	return plugins.CatalogueArtifact{}, plugins.ErrNotFound
}

// Filter controls search and filtering over catalogue entries.
type Filter struct {
	Query          string
	Kind           string
	Category       string
	Capability     string
	IncludeRevoked bool
}

// CatalogueItem is one entry decorated with its latest version and install status
// for the console's GET /api/plugins/catalogue response.
type CatalogueItem struct {
	Entry
	LatestVersion    string   `json:"latestVersion,omitempty"`
	ArtifactURL      string   `json:"artifactUrl,omitempty"`
	SHA256           string   `json:"sha256,omitempty"`
	PermissionLines  []string `json:"permissionLines,omitempty"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	UpdateAvailable  bool     `json:"updateAvailable,omitempty"`
}

// Search returns entries matching Filter.
func (s *Service) Search(ctx context.Context, f Filter) []CatalogueItem {
	s.mu.RLock()
	entries := append([]Entry(nil), s.idx.Entries...)
	s.mu.RUnlock()

	installed := map[string]plugins.Record{}
	if s.mgr != nil {
		if recs, err := s.mgr.List(ctx); err == nil {
			for _, r := range recs {
				installed[r.ID] = r
			}
		}
	}

	q := strings.ToLower(strings.TrimSpace(f.Query))
	kind := strings.ToLower(strings.TrimSpace(f.Kind))
	cat := strings.ToLower(strings.TrimSpace(f.Category))
	capFilter := strings.ToLower(strings.TrimSpace(f.Capability))

	out := make([]CatalogueItem, 0, len(entries))
	for _, e := range entries {
		if e.Revoked && !f.IncludeRevoked {
			if _, isInst := installed[e.ID]; !isInst {
				continue
			}
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		if cat != "" && !strings.EqualFold(e.Category, cat) {
			continue
		}
		if capFilter != "" && !hasStringFold(e.Capabilities, capFilter) {
			continue
		}
		if q != "" && !matchesQuery(e, q) {
			continue
		}
		item := CatalogueItem{Entry: e}
		permissions := e.Permissions
		if latest, ok := e.Latest(); ok {
			item.LatestVersion = latest.Version
			item.ArtifactURL = latest.URL
			item.SHA256 = latest.SHA256
			if latest.Permissions != nil {
				permissions = *latest.Permissions
			}
		}
		lines, _, _ := plugins.PermissionSheet(plugins.Manifest{
			ID:   e.ID,
			Name: e.Name,
			Ether: &plugins.EtherBlock{
				Kind:        plugins.Kind(e.Kind),
				Permissions: permissions,
			},
		})
		item.PermissionLines = lines
		if rec, ok := installed[e.ID]; ok {
			item.Installed = true
			item.InstalledVersion = rec.Version
			if item.LatestVersion != "" && !e.Revoked && plugins.CompareSemVer(item.LatestVersion, rec.Version) > 0 {
				item.UpdateAvailable = true
			}
		}
		out = append(out, item)
	}
	return out
}

func hasStringFold(list []string, target string) bool {
	for _, s := range list {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}

func matchesQuery(e Entry, q string) bool {
	for _, field := range []string{e.ID, e.Name, e.Summary, e.Description, e.Category, e.Kind, e.Author} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	for _, c := range e.Capabilities {
		if strings.Contains(strings.ToLower(c), q) {
			return true
		}
	}
	return false
}

// ServeHTTP serves GET /api/plugins/catalogue and POST /api/plugins/catalogue/refresh.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !s.Configured() {
			writeJSON(w, http.StatusOK, map[string]any{
				"entries":    []CatalogueItem{},
				"configured": false,
			})
			return
		}
		if _, err := s.Refresh(r.Context()); err != nil {
			code := "refresh_failed"
			status := http.StatusBadGateway
			msg := "Could not refresh the plugin catalogue."
			if errors.Is(err, ErrSignatureInvalid) {
				code = "signature_invalid"
				msg = "The plugin catalogue signature did not verify."
			}
			writeJSON(w, status, map[string]any{
				"error": map[string]any{
					"code":    code,
					"message": msg,
				},
			})
			return
		}
	} else {
		s.mu.RLock()
		empty := len(s.idx.Entries) == 0 && s.fetchedAt.IsZero()
		s.mu.RUnlock()
		if empty && s.Configured() {
			_, _ = s.Refresh(r.Context())
		}
	}

	q := r.URL.Query()
	items := s.Search(r.Context(), Filter{
		Query:          firstNonEmpty(q.Get("q"), q.Get("query"), q.Get("search")),
		Kind:           q.Get("kind"),
		Category:       q.Get("category"),
		Capability:     q.Get("capability"),
		IncludeRevoked: q.Get("includeRevoked") == "1" || q.Get("revoked") == "1",
	})

	s.mu.RLock()
	fetched := s.fetchedAt
	lastErr := s.lastErr
	s.mu.RUnlock()

	var fetchedUnix int64
	if !fetched.IsZero() {
		fetchedUnix = fetched.Unix()
	}
	resp := map[string]any{
		"entries":    items,
		"configured": s.Configured(),
		"fetchedAt":  fetchedUnix,
	}
	if lastErr != "" {
		resp["lastError"] = lastErr
	}
	writeJSON(w, http.StatusOK, resp)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	httpjson.Write(w, status, v)
}
