// Package adapters bridges runtime plugin contributions (WS9a list sources
// and WS9c Live TV channel sources) into Ether's collection, personalization
// and Live TV registries.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// MaxPluginListItems bounds one list source refresh.
const MaxPluginListItems = stremio.MaxCatalogItems

// Bridge tracks the source kinds registered for each plugin so enable,
// disable, update and uninstall cleanly replace or remove them.
type Bridge struct {
	mgr  *plugins.Manager
	cols *collections.Service
	pers *personalize.Engine
	tv   *livetv.Service

	mu       sync.Mutex
	listIDs  map[string][]string
	livetvID map[string]string
}

// Attach wires plugin list sources (collections + Home rows) and Live TV
// channel sources to the plugin manager's lifecycle.
func Attach(mgr *plugins.Manager, cols *collections.Service, pers *personalize.Engine, tv *livetv.Service) *Bridge {
	b := &Bridge{
		mgr:      mgr,
		cols:     cols,
		pers:     pers,
		tv:       tv,
		listIDs:  map[string][]string{},
		livetvID: map[string]string{},
	}
	if tv != nil && mgr != nil {
		tv.SetStreamOpener(b.openPluginStream)
	}
	if mgr != nil {
		mgr.OnSync(b.Sync)
	}
	return b
}

// Sync updates all adapter registrations for one plugin record.
func (b *Bridge) Sync(_ context.Context, r plugins.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, id := range b.listIDs[r.ID] {
		if b.cols != nil {
			b.cols.UnregisterSourceKind(id)
		}
		if b.pers != nil {
			b.pers.UnregisterRowSourceKind(id)
		}
	}
	delete(b.listIDs, r.ID)

	if id, ok := b.livetvID[r.ID]; ok {
		if b.tv != nil {
			b.tv.UnregisterSourceKind(id)
		}
		delete(b.livetvID, r.ID)
	}

	if !r.Enabled {
		return
	}

	c := r.Manifest.EffectiveEther().Contributes
	for _, ls := range c.ListSources {
		if !r.Manifest.HasCapability(plugins.CapListSource) || ls.Key == "" {
			continue
		}
		kind := &listSourceKind{
			mgr:        b.mgr,
			pluginID:   r.ID,
			pluginName: r.Name,
			meta:       ls,
			fields:     plugins.ParseListParamFields(ls.Params),
		}
		kind.catalog, kind.declared = listCatalog(r.Manifest.Stremio(), ls)
		if b.cols != nil {
			_ = b.cols.RegisterSourceKind(kind)
		}
		if b.pers != nil {
			_ = b.pers.RegisterRowSourceKind(&rowSourceKind{list: kind})
		}
		b.listIDs[r.ID] = append(b.listIDs[r.ID], kind.ID())
	}

	if r.Manifest.HasCapability(plugins.CapLiveTV) && c.Channels != nil {
		chKind := &channelSourceKind{
			mgr:        b.mgr,
			pluginID:   r.ID,
			pluginName: r.Name,
			meta:       *c.Channels,
			mode:       modeNative,
		}
		if r.Manifest.SpeaksStremio() {
			chKind.mode = modeAddon
		}
		if b.tv != nil {
			_ = b.tv.RegisterSourceKind(chKind)
		}
		b.livetvID[r.ID] = chKind.ID()
	}
}

func pluginText(pluginID, raw string) extend.Text {
	if strings.HasPrefix(raw, "$t:") {
		return extend.PluginText(pluginID, strings.TrimPrefix(raw, "$t:"))
	}
	return extend.Text{Resolved: raw}
}

func resolvePlainText(mgr *plugins.Manager, pluginID, raw, fallback string) string {
	if raw == "" {
		return fallback
	}
	if strings.HasPrefix(raw, "$t:") {
		key := strings.TrimPrefix(raw, "$t:")
		if mgr != nil {
			if s, ok := mgr.ResolveText(context.Background(), pluginID, key, "en"); ok && s != "" {
				return s
			}
		}
		return fallback
	}
	return raw
}

type listSourceKind struct {
	mgr        *plugins.Manager
	pluginID   string
	pluginName string
	meta       plugins.ListSourceMeta
	fields     []plugins.ListParamField
	// catalog is the catalog the list reads. Its Type is empty for a
	// plugin list that serves whichever of movies and series a collection
	// asks for. declared marks a catalog the manifest declares (every
	// Stremio add-on catalog): its extras are checked against the
	// declaration and it pages only when it declares skip.
	catalog  stremio.Catalog
	declared bool
}

// listCatalog reads a list source's catalog ("movie/top", or an id alone)
// and its declaration in the manifest. A plugin's own list that the
// manifest does not declare pages with skip, as the plugin contract says.
func listCatalog(m stremio.Manifest, ls plugins.ListSourceMeta) (stremio.Catalog, bool) {
	spec := strings.TrimSuffix(strings.TrimPrefix(strings.Trim(strings.TrimSpace(ls.Catalog), "/"), "catalog/"), ".json")
	typ, id, ok := strings.Cut(spec, "/")
	if !ok {
		typ, id = "", spec
	}
	typ, id = strings.ToLower(strings.TrimSpace(typ)), strings.TrimSpace(id)
	if id == "" {
		id = ls.Key
	}
	switch typ {
	case "show", "tv":
		typ = stremio.TypeSeries
	}
	if c, ok := m.CatalogNamed(typ, id); ok {
		return c, true
	}
	return stremio.Catalog{Type: typ, ID: id, Extra: []stremio.Extra{{Name: "skip"}}}, false
}

func (k *listSourceKind) MinRefresh() time.Duration {
	d, err := time.ParseDuration(k.meta.Refresh)
	if err != nil || d < time.Hour {
		return time.Hour
	}
	return d
}

func (k *listSourceKind) ID() string {
	return extend.PluginID(k.pluginID, k.meta.Key)
}

func (k *listSourceKind) label() string {
	return resolvePlainText(k.mgr, k.pluginID, k.meta.Label, firstNonEmpty(k.pluginName, k.meta.Key))
}

func (k *listSourceKind) mediaTypes() []string {
	switch k.catalog.Type {
	case stremio.TypeMovie:
		return []string{"movie"}
	case stremio.TypeSeries:
		return []string{"show"}
	default:
		return []string{"movie", "show"}
	}
}

func (k *listSourceKind) Describe() collections.KindDescribe {
	params, defaults := plugins.ParamFieldsFromList(k.pluginID, k.fields)
	if defaults != "{}" {
		defaults = `{"params":` + defaults + `}`
	}
	return collections.KindDescribe{
		Label:      pluginText(k.pluginID, firstNonEmpty(k.meta.Label, k.meta.Key)),
		Hint:       pluginText(k.pluginID, k.meta.Hint),
		Icon:       "list-video",
		Order:      500,
		Params:     params,
		Defaults:   defaults,
		MediaTypes: k.mediaTypes(),
	}
}

func (k *listSourceKind) Validate(ve *collections.ValidationError, path string, src core.CollectionSource) {
	if src.MaxItems < 0 || src.MaxItems > MaxPluginListItems {
		ve.Add(path+".maxItems", fmt.Sprintf("must be between 1 and %d", MaxPluginListItems))
	}
	if k.declared {
		if err := k.catalog.ValidateExtras(catalogExtras(src.Params)); err != nil {
			name, _, _ := strings.Cut(err.Error(), " ")
			ve.Add(path+".params."+name, err.Error())
		}
		return
	}
	for _, f := range k.fields {
		key := firstNonEmpty(f.Key, f.Name)
		if f.Required && key != "" && strings.TrimSpace(src.Params[key]) == "" {
			ve.Add(path+".params."+key, key+" is required")
		}
	}
}

// catalogExtras are a source's params as catalog extras: set values only,
// and never skip, which the reader adds while paging.
func catalogExtras(params map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range params {
		if v = strings.TrimSpace(v); k != "" && k != "skip" && v != "" {
			out[k] = v
		}
	}
	return out
}

// maxItems is how many items one refresh reads: the source's own limit,
// within the list's, within MaxPluginListItems.
func (k *listSourceKind) maxItems(src core.CollectionSource) int {
	n := MaxPluginListItems
	if k.meta.MaxItems > 0 && k.meta.MaxItems < n {
		n = k.meta.MaxItems
	}
	if src.MaxItems > 0 && src.MaxItems < n {
		n = src.MaxItems
	}
	return n
}

// catalogs are the catalogs one gather reads: the list's own, or for a
// list without a type, one per media type the collection asks for.
func (k *listSourceKind) catalogs(kinds []string) []stremio.Catalog {
	if k.catalog.Type != "" {
		return []stremio.Catalog{k.catalog}
	}
	var out []stremio.Catalog
	for _, typ := range []string{stremio.TypeMovie, stremio.TypeSeries} {
		for _, kind := range kinds {
			if (kind == "tv") == (typ == stremio.TypeSeries) {
				c := k.catalog
				c.Type = typ
				out = append(out, c)
				break
			}
		}
	}
	if len(out) == 0 {
		c := k.catalog
		c.Type = stremio.TypeMovie
		out = append(out, c)
	}
	return out
}

// Gather reads the list through the add-on protocol and matches its items
// to the catalogue by id. It runs only in background builds and owner
// previews (collections never gathers a plugin list on a viewer request);
// a preview reads the first page only. Items without an id Ether can
// match are counted with the unmatched ones, never guessed by title.
func (k *listSourceKind) Gather(g *collections.Gather) {
	if k.mgr == nil {
		if g.Strict {
			g.Fail(fmt.Errorf("catalogue source unavailable"))
			return
		}
		g.Warn("%s is unavailable", k.label())
		return
	}
	rec, err := k.mgr.Get(g.Ctx, k.pluginID)
	if err != nil {
		if g.Strict {
			g.Fail(err)
			return
		}
		g.Warn("%s: %v", k.label(), err)
		return
	}
	preview := collections.IsPreview(g.Ctx)
	limit := k.maxItems(g.Src)
	if preview && limit > collections.PreviewItems {
		limit = collections.PreviewItems
	}
	extra := catalogExtras(g.Src.Params)
	metadata := rec.Manifest.HasCapability(plugins.CapMetadata)
	var refs []collections.ExternalRef
	read, total, unidentified := false, 0, 0
	for _, cat := range k.catalogs(g.Kinds) {
		if total >= limit {
			break
		}
		fetcher := &healthFetcher{inner: k.mgr.Fetcher(rec.ID)}
		addon := k.mgr.Addon(rec)
		addon.Fetcher = fetcher
		var metas []stremio.MetaPreview
		if preview {
			metas, _, err = addon.CatalogPage(g.Ctx, cat.Type, cat.ID, extra)
		} else {
			metas, err = addon.Catalog(g.Ctx, cat, extra, limit-total)
		}
		if err != nil {
			if g.Strict {
				g.Fail(err)
				return
			}
			if preview && errors.Is(err, context.DeadlineExceeded) {
				g.Fail(fmt.Errorf("plugin list preview timed out: %w", err))
				return
			}
			if !fetcher.recorded {
				k.mgr.RecordFailure(g.Ctx, rec.ID, fmt.Sprintf("catalog %s/%s: %v", cat.Type, cat.ID, err))
			}
			g.Warn("%s: %v", k.label(), err)
		}
		if err == nil || len(metas) > 0 {
			read = true
		}
		for _, m := range metas {
			if total >= limit {
				break
			}
			total++
			if ref, ok := catalogRef(m, cat.Type, metadata, rec.ID); ok {
				refs = append(refs, ref)
			} else {
				unidentified++
			}
		}
	}
	if !read {
		return
	}
	if total == 0 {
		g.Warn("%s is empty", k.label())
		return
	}
	g.ResolveExternal(k.label(), refs, unidentified)
}

// catalogRef is a catalog item's identity: its IMDb, TMDB or TVDB id, or,
// when the add-on also provides title details, the add-on's own id.
func catalogRef(m stremio.MetaPreview, catType string, metadata bool, pluginID string) (collections.ExternalRef, bool) {
	typ := strings.ToLower(m.Type)
	if typ != stremio.TypeMovie && typ != stremio.TypeSeries {
		typ = catType
	}
	ref := collections.ExternalRef{Type: "movie"}
	if typ == stremio.TypeSeries {
		ref.Type = "show"
	}
	ref.IMDB, ref.TMDB, ref.TVDB = m.Ref()
	if metadata && m.ID != "" {
		ref.MetadataProvider = extend.PluginID(pluginID, "metadata")
		ref.ProviderID = m.ID
	}
	return ref, ref.MetadataProvider != "" || ref.IMDB != "" || ref.TMDB > 0 || ref.TVDB > 0
}

// healthFetcher reads through the plugin runtime, which records transport
// failures in the plugin's health itself; recorded tells the caller not
// to record that failure again.
type healthFetcher struct {
	inner    stremio.Fetcher
	recorded bool
}

func (f *healthFetcher) Fetch(ctx context.Context, route string, limit int64) ([]byte, error) {
	body, err := f.inner.Fetch(ctx, route, limit)
	var status *stremio.HTTPError
	if err != nil && !errors.As(err, &status) {
		f.recorded = true
	}
	return body, err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// rowSourceKind adapts a plugin list source to a Home Designer row kind.
type rowSourceKind struct {
	list *listSourceKind
}

func (r *rowSourceKind) Kind() string { return r.list.ID() }

func (r *rowSourceKind) Descriptor() core.RowDescriptor {
	id := r.list.ID()
	title := r.list.label()
	desc := resolvePlainText(r.list.mgr, r.list.pluginID, r.list.meta.Hint, "")
	sections := []string{"home"}
	for _, mt := range r.list.mediaTypes() {
		switch mt {
		case "movie":
			sections = append(sections, "movies")
		case "show":
			sections = append(sections, "shows")
		}
	}
	return core.RowDescriptor{
		HubID:       id,
		Title:       title,
		Kind:        id,
		Description: desc,
		Sections:    sections,
		Group:       "collections",
	}
}
