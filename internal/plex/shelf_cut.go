package plex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// The refresh interval selects a NEW generation only when building a screen.
// Continuations name the immutable generation, never the current wall clock.
const shelfGenerationParam = "etherShelf"
const shelfMaxBytes = 32 << 20

type shelfCut struct {
	at         time.Time
	generation string
	revision   string
}
type shelfGeneration struct {
	scope  string
	paths  map[string]bool
	items  []core.Title
	bytes  int
	serial uint64
}

func (s *Server) shelfScope(ctx context.Context, pr *Request) (string, string, bool) {
	hard := fmt.Sprintf("%+v", pr.User)
	viewerRevision := ""
	if src := s.d.HomeCache; src != nil && src.Revision != nil {
		rev, err := src.Revision(ctx, pr.User)
		if err != nil {
			return "", "", false
		}
		// Watch state and owner layout may rebuild a screen, but do not
		// revoke a continuation. Only authorization/settings belong here.
		viewerRevision = rev.Viewer
		var settings uint64
		if src.Config != nil {
			settings = src.Config()
		}
		hard += fmt.Sprintf("|%d", settings)
		if s.viewerRestricted(pr.User) {
			hard += fmt.Sprintf("|%d", rev.Policy)
		}
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%t", hard, pr.ClientID, pr.Language, pr.Product, pr.Version, pr.Platform, pr.PlatformVersion, pr.Device, pr.Model, pr.Remote), viewerRevision, true
}

func (s *Server) shelfSnapshot(ctx context.Context, pr *Request, h core.Hub, hc hubCtx, build func() ([]core.Title, []model.Metadata)) ([]core.Title, string) {
	scope, viewerRevision, ok := s.shelfScope(ctx, pr)
	if !ok {
		// Without the authorization revision a retained row cannot be served.
		return nil, ""
	}
	identity := fmt.Sprintf("%s|%d|%d|%s", scope, hc.section, hc.related, h.ID)
	s.shelfMu.Lock()
	now := s.now()
	if cut, ok := s.shelfCuts[identity]; ok && cut.revision == viewerRevision && now.Sub(cut.at) < 5*time.Minute && !now.Before(cut.at) {
		if gen, ok := s.shelfGenerations[cut.generation]; ok {
			s.shelfMu.Unlock()
			return gen.items, cut.generation
		}
	}
	s.shelfMu.Unlock()
	titles, items := build()
	// Wire identities, including duplicates from a producer, are canonicalized
	// BEFORE calculating counts or cutting any page.
	seen := map[string]bool{}
	byKey := map[string]core.Title{}
	for _, title := range titles {
		if _, ok := byKey[title.Key.String()]; !ok {
			byKey[title.Key.String()] = title
		}
	}
	unique := make([]core.Title, 0, len(items))
	for _, item := range items {
		if item.RatingKey != "" && !seen[item.RatingKey] {
			seen[item.RatingKey] = true
			unique = append(unique, byKey[item.RatingKey])
		}
	}
	data, _ := json.Marshal(unique)
	generation := fmt.Sprintf("%x", sha256.Sum256(append([]byte(identity+"|"), data...)))
	s.shelfMu.Lock()
	defer s.shelfMu.Unlock()
	// Concurrent screen builds must publish the same winning generation.
	if cut, ok := s.shelfCuts[identity]; ok && cut.revision == viewerRevision && now.Sub(cut.at) < 5*time.Minute && !now.Before(cut.at) {
		if gen, ok := s.shelfGenerations[cut.generation]; ok {
			return gen.items, cut.generation
		}
	}
	if s.shelfCuts == nil {
		s.shelfCuts = map[string]shelfCut{}
	}
	if s.shelfGenerations == nil {
		s.shelfGenerations = map[string]shelfGeneration{}
	}
	if _, ok := s.shelfGenerations[generation]; !ok {
		s.shelfSerial++
		s.shelfGenerations[generation] = shelfGeneration{scope: scope, paths: map[string]bool{}, items: unique, bytes: len(data), serial: s.shelfSerial}
		s.shelfBytes += len(data)
	}
	s.shelfCuts[identity] = shelfCut{now, generation, viewerRevision}
	// Bound retained generations by memory, not idle time. Evicted URLs fail
	// explicitly (410); they must NEVER fall through to a rebuilt listing.
	for (s.shelfBytes > shelfMaxBytes || len(s.shelfGenerations) > 512) && len(s.shelfGenerations) > 1 {
		oldest := generation
		for k, g := range s.shelfGenerations {
			if g.serial < s.shelfGenerations[oldest].serial {
				oldest = k
			}
		}
		s.shelfBytes -= s.shelfGenerations[oldest].bytes
		delete(s.shelfGenerations, oldest)
		for k, cut := range s.shelfCuts {
			if cut.generation == oldest {
				delete(s.shelfCuts, k)
			}
		}
	}
	return s.shelfGenerations[generation].items, generation
}

func (s *Server) shelfURL(raw, generation string) string {
	u, err := url.Parse(raw)
	if err != nil || generation == "" {
		return raw
	}
	s.shelfMu.Lock()
	g, ok := s.shelfGenerations[generation]
	if ok {
		g.paths[u.Path] = true
	}
	s.shelfMu.Unlock()
	q := u.Query()
	q.Set(shelfGenerationParam, generation)
	u.RawQuery = q.Encode()
	return u.String()
}

// Registered on the existing PMS listing routes, after authentication. The
// opaque key is scoped to viewer policy, client and language, not a credential.
func (s *Server) serveShelfGeneration(w http.ResponseWriter, r *http.Request) bool {
	generation := r.URL.Query().Get(shelfGenerationParam)
	if generation == "" {
		return false
	}
	pr := FromContext(r.Context())
	scope, _, valid := s.shelfScope(r.Context(), pr)
	s.shelfMu.Lock()
	g, ok := s.shelfGenerations[generation]
	pathOK := ok && g.paths[r.URL.Path]
	s.shelfMu.Unlock()
	if !ok {
		writeHTTPError(w, http.StatusGone)
		return true
	}
	if !valid || g.scope != scope || !pathOK {
		writeHTTPError(w, http.StatusNotFound)
		return true
	}
	items := g.items
	// PMS hubKey explicitly names just the displayed metadata, not the rest
	// of the row. Its response must preserve that subset.
	if strings.HasPrefix(r.URL.Path, "/library/metadata/") {
		n := len(strings.Split(strings.TrimPrefix(r.URL.Path, "/library/metadata/"), ","))
		items = items[:min(n, len(items))]
	}
	total := len(items)
	start := min(max(pr.Start, 0), total)
	size := total - start
	if pr.HasPage {
		size = min(max(pr.Size, 0), size)
	}
	mc := hubContainer()
	mc.Metadata = s.renderTitles(r.Context(), pr, items[start:start+size], renderOpts{media: true, availabilityInSQL: true})
	mc.Size = len(mc.Metadata)
	mc.SetPage(start, total)
	s.write(w, r, mc)
	return true
}
