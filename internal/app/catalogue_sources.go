package app

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"net/http"
)

type catalogueSource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Owner      string             `json:"owner"`
	Group      string             `json:"group"`
	MediaTypes []string           `json:"mediaTypes"`
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
