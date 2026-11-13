package app

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"net/http"
	"strings"
)

type catalogueSource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Owner      string             `json:"owner"`
	Group      string             `json:"group"`
	MediaTypes []string           `json:"mediaTypes"`
	ItemCount  *int               `json:"itemCount,omitempty"`
	Health     string             `json:"health"`
	PluginID   string             `json:"pluginId,omitempty"`
	KindKey    string             `json:"kindKey,omitempty"`
	Libraries  []catalogueLibrary `json:"libraries"`
}
type catalogueLibrary struct {
	ID   core.LibraryID `json:"id"`
	Name string         `json:"name"`
}

// Registry enumeration reads library and plugin declarations only. It never
// fetches remote catalogues or counts title rows on the owner request path.
func (c *librariesConsole) handleCatalogueSources(w http.ResponseWriter, r *http.Request) {
	out := []catalogueSource{
		{ID: "tmdb-movies", Name: "TMDB Movies", Owner: "Built in", Group: "builtin", MediaTypes: []string{"movie"}, Health: "ok", Libraries: []catalogueLibrary{}},
		{ID: "tmdb-tv", Name: "TMDB TV shows", Owner: "Built in", Group: "builtin", MediaTypes: []string{"show"}, Health: "ok", Libraries: []catalogueLibrary{}},
	}
	if c.app.catalog != nil {
		if stats, err := c.app.catalog.Stats(r.Context()); err == nil {
			out[0].ItemCount = &stats.BrowsableMovies
			out[1].ItemCount = &stats.BrowsableShows
		}
	}
	var libs []core.Library
	if c.app.libraries != nil {
		var err error
		libs, err = c.app.libraries.List(r.Context())
		if err != nil {
			libError(w, 500, "Could not list libraries")
			return
		}
	}
	if c.app.pluginsRT != nil && c.app.pluginsRT.mgr != nil {
		recs, err := c.app.pluginsRT.mgr.List(r.Context())
		if err != nil {
			libError(w, 500, "Could not list plugins")
			return
		}
		for _, rec := range recs {
			health := "unknown"
			if h, err := c.app.pluginsRT.mgr.HealthOf(r.Context(), rec.ID); err == nil && h.Status != "" {
				health = h.Status
			}
			if !rec.Enabled {
				continue
			}
			if e := rec.Manifest.EffectiveEther(); e != nil {
				for _, kind := range e.Contributes.MediaKinds {
					src := catalogueSource{ID: "plugin:" + rec.ID + ":" + kind.Key, Name: kind.Label, Owner: rec.Name, Group: "plugin", PluginID: rec.ID, KindKey: kind.Key, MediaTypes: []string{kind.Key}, Health: health, Libraries: []catalogueLibrary{}}
					if kind.Library.Name != "" {
						src.Name = kind.Library.Name
					}
					if c.app.pluginsRT.pluginitems != nil {
						for _, lib := range libs {
							if lib.PluginID == rec.ID && lib.KindKey == kind.Key {
								if tally, err := c.app.pluginsRT.pluginitems.LibraryTally(r.Context(), lib.ID); err == nil && len(kind.Levels) > 0 {
									count := tally[kind.Levels[0].Key]
									src.ItemCount = &count
								}
								break
							}
						}
					}
					out = append(out, src)
				}
			}
			if !rec.Manifest.HasCapability(plugins.CapListSource) {
				continue
			}
			for _, cat := range rec.Manifest.Stremio().Catalogs {
				typ := "movie"
				if cat.Type == "series" {
					typ = "show"
				} else if cat.Type != "movie" {
					continue
				}
				name := cat.Name
				if name == "" {
					name = cat.ID
				}
				out = append(out, catalogueSource{ID: "stremio:" + rec.ID + ":" + plugins.StremioCatalogKey(cat), Name: name, Owner: rec.Name, Group: "stremio", PluginID: rec.ID, MediaTypes: []string{typ}, Health: health, Libraries: []catalogueLibrary{}})
			}
		}
	}
	for i := range out {
		if out[i].Group == "stremio" {
			var count int
			if err := c.app.db.QueryRowContext(r.Context(), `SELECT item_count FROM libraries_catalogue_state WHERE source_id=?`, out[i].ID).Scan(&count); err == nil {
				out[i].ItemCount = &count
			}
		}
		for _, lib := range libs {
			for _, id := range lib.Sources {
				if id == out[i].ID {
					out[i].Libraries = append(out[i].Libraries, catalogueLibrary{lib.ID, lib.Name})
					break
				}
			}
		}
	}
	libJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func (c *librariesConsole) handleCatalogueSourceSync(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("source")
	if !strings.HasPrefix(id, "stremio:") {
		libError(w, 400, "Only Stremio catalogues use this refresh")
		return
	}
	if _, err := c.app.catalogueSourceType(r.Context(), id); err != nil {
		libError(w, 400, err.Error())
		return
	}
	if c.app.catalog == nil {
		libError(w, 503, "Catalogue unavailable")
		return
	}
	if _, err := c.app.db.ExecContext(r.Context(), `DELETE FROM libraries_catalogue_state WHERE source_id=?`, id); err != nil {
		libError(w, 500, "Could not refresh source")
		return
	}
	if err := c.app.catalog.BrowseSourceLibrary(r.Context(), []string{id}); err != nil {
		libError(w, 502, err.Error())
		return
	}
	libJSON(w, 200, map[string]bool{"ok": true})
}
