package subtitles

// Add-on subtitle providers beside OpenSubtitles.
//
// An add-on whose manifest resources include "subtitles" answers
// GET /subtitles/{type}/{id}[/videoHash=…&videoSize=…].json with
// {url, lang} entries. Listing is a search: Ether calls it only from
// the viewer-driven SearchOnDemand, and a file is downloaded only when
// the viewer picks it (DownloadOnDemand), through the same normalise
// and cache pipeline as OpenSubtitles. The moviehash is sent only when
// a caller already has it from cached ranges; nothing here ever fetches
// a release to compute one (see MovieHash).

import (
	"context"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
)

type videoHashEntry struct {
	release   string
	movieHash string
	videoSize int64
}

// RememberVideoHash records an already-computed moviehash and byte size
// (for example from local file enrichment) so a later viewer-driven
// subtitle search can pass videoHash and videoSize without fetching the
// release on the search path.
func (s *Service) RememberVideoHash(key core.RatingKey, releaseName, movieHash string, videoSize int64) {
	if key == 0 || len(movieHash) != 16 {
		return
	}
	s.mu.Lock()
	if s.videoHashes == nil {
		s.videoHashes = map[core.RatingKey]videoHashEntry{}
	}
	s.videoHashes[key] = videoHashEntry{
		release:   releaseName,
		movieHash: strings.ToLower(movieHash),
		videoSize: videoSize,
	}
	s.mu.Unlock()
}

func (s *Service) lookupVideoHash(title *core.Title, releaseName string) (string, int64) {
	if title == nil || title.Key == 0 {
		return "", 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.videoHashes[title.Key]
	if !ok {
		return "", 0
	}
	if releaseName != "" && e.release != "" && !strings.EqualFold(releaseName, e.release) {
		return "", 0
	}
	return e.movieHash, e.videoSize
}

// providerList is the effective provider order: the owner's configured
// order, with OpenSubtitles and SubDL included when enabled and not
// explicitly listed in s.providers.
func (s *Service) providerList() []config.SubtitleProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []config.SubtitleProvider
	hasOpenSubtitles := false
	hasSubDL := false
	for _, p := range s.providers {
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			hasOpenSubtitles = true
		case config.SubtitleProviderSubDL:
			hasSubDL = true
		}
		if p.Enabled {
			out = append(out, p)
		}
	}
	if !hasSubDL && s.subdl.Enabled && s.subdl.APIKey != "" {
		out = append(out, config.SubtitleProvider{ID: "subdl", Kind: config.SubtitleProviderSubDL, Enabled: true})
	}
	if !hasOpenSubtitles && s.cfg.Enabled && s.cfg.APIKey != "" {
		out = append([]config.SubtitleProvider{{ID: "opensubtitles", Kind: config.SubtitleProviderOpenSubtitles, Enabled: true}}, out...)
	}
	return out
}

// searchEnabled reports whether any provider can search.
func (s *Service) searchEnabled() bool {
	if s.dir == "" {
		return false
	}
	s.mu.Lock()
	hasPlugins := len(s.pluginProviders) > 0
	s.mu.Unlock()
	if hasPlugins {
		return true
	}
	for _, p := range s.providerList() {
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			if s.openSubtitlesEnabled() {
				return true
			}
		case config.SubtitleProviderSubDL:
			if s.subdlEnabled() {
				return true
			}
		}
	}
	return false
}

// UpdateProviders swaps the subtitle provider order (config changes).
func (s *Service) UpdateProviders(ps []config.SubtitleProvider) {
	s.mu.Lock()
	s.providers = append([]config.SubtitleProvider(nil), ps...)
	s.mu.Unlock()
}

// searchProviders runs every enabled provider in owner order and merges
// the candidates into one ranked list (language, hash match and
// release-name match via Rank; provider order breaks score ties). One
// provider failing does not sink the others; the error surfaces only
// when every provider failed.
func (s *Service) searchProviders(ctx context.Context, title *core.Title, lang, releaseName, movieHash string, videoSize int64) ([]Candidate, error) {
	var all []Candidate
	var firstErr error
	for i, p := range s.providerList() {
		var cands []Candidate
		var err error
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			if !s.openSubtitlesEnabled() {
				continue
			}
			cands, err = s.SearchWithHash(ctx, title, lang, releaseName, movieHash)
			for j := range cands {
				if cands[j].Provider == "" {
					cands[j].Provider = "OpenSubtitles"
					cands[j].ProviderID = "opensubtitles"
				}
			}
		case config.SubtitleProviderSubDL:
			if !s.subdlEnabled() {
				continue
			}
			cands, err = s.searchSubDL(ctx, title, lang)
		default:
			pluginID := strings.TrimSuffix(strings.TrimPrefix(p.Kind, "plugin:"), "/subtitles")
			s.mu.Lock()
			provider := s.pluginProviders[pluginID]
			s.mu.Unlock()
			if !strings.HasPrefix(p.Kind, "plugin:") || provider == nil {
				continue
			}
			cands, err = provider.Search(ctx, title, lang, movieHash, videoSize)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.log.Debug("subtitle provider search failed", "provider", p.ID, "err", err)
			continue
		}
		for j := range cands {
			cands[j].providerOrder = i
		}
		all = append(all, cands...)
	}
	s.mu.Lock()
	pluginProviders := make([]PluginProvider, 0, len(s.pluginProviders))
	for _, p := range s.pluginProviders {
		pluginProviders = append(pluginProviders, p)
	}
	s.mu.Unlock()
	sort.Slice(pluginProviders, func(i, j int) bool { return pluginProviders[i].ID() < pluginProviders[j].ID() })
	for i, p := range pluginProviders {
		configured := false
		s.mu.Lock()
		entries := append([]config.SubtitleProvider(nil), s.providers...)
		s.mu.Unlock()
		for _, entry := range entries {
			if entry.Kind == "plugin:"+p.ID()+"/subtitles" {
				configured = true
				break
			}
		}
		if configured {
			continue
		}
		cands, err := p.Search(ctx, title, lang, movieHash, videoSize)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for j := range cands {
			cands[j].providerOrder = len(s.providerList()) + i
		}
		all = append(all, cands...)
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	Rank(all, releaseName)
	sort.SliceStable(all, func(i, j int) bool {
		if lang != "" {
			ti := langtag.MatchTier(lang, all[i].Language)
			tj := langtag.MatchTier(lang, all[j].Language)
			if ti != tj {
				return ti > tj
			}
		}
		if all[i].HashMatch != all[j].HashMatch {
			return all[i].HashMatch
		}
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		if all[i].providerOrder != all[j].providerOrder {
			return all[i].providerOrder < all[j].providerOrder
		}
		return all[i].FileID < all[j].FileID
	})
	return all, nil
}
