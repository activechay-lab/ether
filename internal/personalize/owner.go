package personalize

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// Owner-facing API (internal/admin: viewers, Home Designer, insights).

// previewWait bounds how long an owner preview waits for a viewer's first
// model build.
const previewWait = 45 * time.Second

// PreviewHome returns exactly what the Plex front end would serve the
// viewer in this context (the same assembly as HomeHubsFor and
// SectionHubsFor), plus the rows the front end adds itself (Continue
// Watching and Recently Added in sections, Recent Playlists on Home),
// marked source "plex". Rows hidden by a layout policy or absent despite an
// "always" policy are included last with Hidden set; Plex never receives them.
//
// section is "" or "home", "movies", "shows", or a library section id.
// The daypart and weekend flag are derived from hc.Now in server local
// time (hc.Device is kept as given).
func (e *Engine) PreviewHome(ctx context.Context, user *core.User, hc core.HubContext, sectionArg string, limit int) (*core.HomePreview, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	if user == nil {
		return nil, fmt.Errorf("personalize: no user")
	}
	if e.opts.UserPolicy != nil {
		user = e.opts.UserPolicy(user)
	}
	sid, section, err := e.resolveSection(sectionArg)
	if err != nil {
		return nil, err
	}
	if hc.Now.IsZero() {
		hc.Now = e.now()
	}
	hc.Now = hc.Now.In(e.loc)
	hc.Daypart, hc.Weekend, hc.SectionID = core.DaypartAt(hc.Now), core.IsWeekend(hc.Now), sid
	if !validDevices[hc.Device] {
		return nil, &ValidationError{Problems: []Problem{{"device", "must be tv, mobile, web or desktop"}}}
	}
	wctx, cancel := context.WithTimeout(ctx, previewWait)
	defer cancel()
	p := e.home(wctx, user, hc, section, limit, true)
	if limit <= 0 {
		limit = 12
	}
	if section == "" {
		// The front end puts Continue Watching first when the engine did
		// not place it, and appends Recent Playlists.
		hasCW := false
		for _, r := range p.Rows {
			if r.Kind == KindContinue {
				hasCW = true
			}
		}
		if !hasCW {
			items := e.continueItems(ctx, user, limit)
			p.Rows = append([]core.HomeRow{plexRow("home.continue", "Continue Watching", KindContinue,
				"Titles you're part-way through (Plex's own row).", items)}, p.Rows...)
		}
		if row, ok := e.playlistsRow(ctx, user); ok {
			p.Rows = insertBeforeHidden(p.Rows, row)
		}
	} else {
		prefix := sectionPrefix(section)
		sid := fmt.Sprint(hc.SectionID)
		var cw []candItem
		for _, it := range e.continueItems(ctx, user, 50) {
			if (section == core.TypeMovie) == (it.t.Type == core.TypeMovie) {
				cw = append(cw, it)
			}
		}
		if len(cw) > limit {
			cw = cw[:limit]
		}
		head := []core.HomeRow{plexRow(prefix+".inprogress."+sid, "Continue Watching", KindContinue,
			"Titles you're part-way through in this library (Plex's own row).", cw)}
		ra := e.recentlyAdded(ctx, user, section, limit)
		head = append(head, plexRow(prefix+".recentlyadded."+sid, "Recently Added", KindRecentlyAdded,
			"The newest additions to this library (Plex's own row).", ra))
		p.Rows = append(head, p.Rows...)
	}
	if e.extraHomeRows != nil {
		p.Rows = append(p.Rows, rowsFromHubs(e.extraHomeRows(ctx, user, hc, sectionName(section), limit))...)
	}
	emptiedByRestrictions := map[string]bool{}
	if e.opts.Allowed != nil {
		kept := p.Rows[:0]
		for i := range p.Rows {
			row := p.Rows[i]
			before := len(row.Items)
			items := make([]core.Title, 0, len(row.Items))
			reasons := map[core.RatingKey]string{}
			for _, title := range row.Items {
				if title.Type == "playlist" || e.opts.Allowed(user, &title) {
					items = append(items, title)
					if reason, ok := row.ItemReasons[title.Key]; ok {
						reasons[title.Key] = reason
					}
				}
			}
			row.Items = items
			row.Hub.Items = items
			row.ItemReasons = reasons
			if before > 0 && len(items) == 0 && row.Kind != KindContinue && row.Kind != KindPlaylists {
				if row.Kind != "" {
					emptiedByRestrictions[row.Kind] = true
				}
				if row.HubID != "" {
					emptiedByRestrictions[row.HubID] = true
				}
				if !row.Hidden {
					continue
				}
			}
			kept = append(kept, row)
		}
		p.Rows = kept
	}
	def := e.opts.MaxHomeRows
	if section != "" {
		def = e.opts.MaxSectionRows
	}
	layoutDoc := e.layoutForContext(ctx)
	p.Rows = arrangeLayout(p.Rows, layoutDoc, user.ID, hc, sectionName(section), def)
	p.Rows = e.appendAbsentAlwaysRows(ctx, p.Rows, layoutDoc, user, hc, section, emptiedByRestrictions)
	return &p, nil
}

func isPersonalKind(kind string) bool {
	switch kind {
	case KindFavorites, KindContinue, KindUpNext, KindTop10, KindBecause, KindPerson,
		KindBridge, KindTrendingTaste, KindCrowd, KindCrowdTrending, KindHiddenGems,
		KindNewThisWeek, KindComfort, KindDifferent, KindFranchise, KindLLM,
		KindTraktRecs, KindTraktWatch, KindPlaylists,
		KindTasteCluster, KindEra, KindWorld, KindOccasion, KindAcclaimed, KindTopGenre, KindStudio,
		KindNewSeasons, KindFinishShow, KindStartNew, KindAiringTaste, KindNetwork:
		return true
	}
	return false
}

func (e *Engine) resolvePolicyIdentity(ctx context.Context, raw string, user *core.User, hc core.HubContext, section core.MediaType, d core.HomeLayout) (hubID, kind, title, source string) {
	if collID := collectionRowID(raw); collID != "" {
		kind = KindCollectionPrefix + collID
		hubID = "collection." + collID
		source = core.RowSourceOwner
		title = collID
		if cr, ok := e.cachedOwnerCollection(user, hc, section, d, collID); ok {
			if cr.Hub.Title != "" {
				title = cr.Hub.Title
			}
			if cr.Hub.ID != "" {
				hubID = cr.Hub.ID
			}
			return hubID, kind, title, source
		}
		for _, sp := range e.collectionSpecs(ctx) {
			if sp.ID == collID && sp.Name != "" {
				title = sp.Name
				break
			}
		}
		return hubID, kind, title, source
	}
	if _, ok := kindByName[raw]; ok {
		kind = raw
	} else if k := hubKind(raw); k != raw {
		kind = k
	} else if k := parseHubKind("home." + raw); k != "other" {
		kind = k
	} else {
		kind = raw
	}
	info := infoFor(kind)
	source = info.source
	title = info.title
	switch {
	case strings.HasPrefix(raw, "home."), strings.HasPrefix(raw, "movie."), strings.HasPrefix(raw, "tv."), strings.HasPrefix(raw, "live."):
		hubID = raw
	case kind == KindPlaylists:
		hubID = "home.playlists"
	case kind == KindContinue && section == "":
		hubID = "home.continue"
	default:
		hubID = sectionPrefix(section) + "." + info.slug
	}
	return hubID, kind, title, source
}

func (e *Engine) collectionSpecs(ctx context.Context) []core.CollectionSpec {
	source, ok := e.opts.Collections.(core.CollectionSpecSource)
	if !ok {
		return nil
	}
	if light, ok := e.opts.Collections.(lightCollectionSpecSource); ok {
		specs, _ := light.ListSpecsLight(ctx)
		return specs
	}
	specs, _ := source.ListSpecs(ctx)
	return specs
}

func (e *Engine) appendAbsentAlwaysRows(ctx context.Context, rows []core.HomeRow, d core.HomeLayout, user *core.User, hc core.HubContext, section core.MediaType, emptied map[string]bool) []core.HomeRow {
	if user == nil {
		return rows
	}
	surf := sectionName(section)
	type activePol struct {
		pol   core.RowPolicy
		layer string
	}
	var policies []activePol
	var vPage *core.LayoutPage
	for _, vl := range d.Viewers {
		if vl.UserID == user.ID {
			vPage = pageFor(vl.Pages, surf)
		}
	}
	if vPage != nil {
		for _, pol := range vPage.Rows {
			if pol.Status == "always" && !strings.ContainsAny(pol.Row, "*?[") {
				policies = append(policies, activePol{pol: pol, layer: "override"})
			}
		}
	}
	if gPage := pageFor(d.Global, surf); gPage != nil {
		for _, pol := range gPage.Rows {
			if pol.Status != "always" || strings.ContainsAny(pol.Row, "*?[") {
				continue
			}
			hubID, kind, _, _ := e.resolvePolicyIdentity(ctx, pol.Row, user, hc, section, d)
			eff, effLayer := effectiveHomePolicy(d, user.ID, surf, core.HomeRow{HubID: hubID, Kind: kind})
			if eff == nil || eff.Status != "always" || effLayer != "global" || eff.Row != pol.Row {
				continue
			}
			policies = append(policies, activePol{pol: pol, layer: "global"})
		}
	}
	if len(policies) == 0 {
		return rows
	}
	var model *userModel
	modelLoaded := false
	getModel := func() *userModel {
		if !modelLoaded {
			model = e.modelFor(ctx, user, false)
			modelLoaded = true
		}
		return model
	}
	for _, ap := range policies {
		pol := ap.pol
		matched := false
		for _, r := range rows {
			if matchRank(pol.Row, r.HubID, hubStem(r.HubID), r.Kind) > 0 {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		hubID, kind, title, source := e.resolvePolicyIdentity(ctx, pol.Row, user, hc, section, d)
		code, reason := e.absentAlwaysReason(ctx, pol, ap.layer, hubID, kind, user, hc, section, d, emptied, getModel)
		polCopy := pol
		placement := "Always included by " + ap.layer
		if ap.layer == "override" {
			placement = "Viewer override"
		}
		if pol.Slot > 0 {
			placement = fmt.Sprintf("%s anchor at slot %d", ap.layer, pol.Slot)
		}
		rows = append(rows, core.HomeRow{
			HubID:        hubID,
			Title:        title,
			Kind:         kind,
			Source:       source,
			Items:        []core.Title{},
			Hub:          core.Hub{ID: hubID, Title: title},
			Hidden:       true,
			HiddenCode:   code,
			HiddenReason: reason,
			Layer:        ap.layer,
			Policy:       &polCopy,
			PolicyLayer:  ap.layer,
			AnchorSlot:   pol.Slot,
			Pinned:       pol.Slot > 0,
			Placement:    placement,
		})
	}
	return rows
}

func (e *Engine) absentAlwaysReason(ctx context.Context, pol core.RowPolicy, layer, hubID, kind string, user *core.User, hc core.HubContext, section core.MediaType, d core.HomeLayout, emptied map[string]bool, getModel func() *userModel) (code, reason string) {
	if c, why := policyHiddenReason(&pol, layer, user.ID, hc); why != "" {
		return c, why
	}
	surf := sectionName(section)
	if collID := collectionRowID(pol.Row); collID != "" {
		cr, hasCR := e.cachedOwnerCollection(user, hc, section, d, collID)
		for _, sp := range e.collectionSpecs(ctx) {
			if sp.ID != collID {
				continue
			}
			if !sp.Enabled {
				return "collection_disabled", "Collection is disabled in Collection Studio"
			}
			var targetUser *core.User
			if user != nil && user.ID > 0 {
				targetUser = user
			}
			if (sp.Targets.Users.All || len(sp.Targets.Users.IDs) > 0) && !sp.Targets.Users.Includes(targetUser) {
				return "collection_not_shared", "Collection is not shared with this viewer"
			}
			if !slices.Contains(collectionSections(sp.MediaType), surf) {
				return "wrong_surface", "Not available on this page"
			}
			if !hasCR && !(layer == "override" && pol.When != nil) {
				sc := sp.Targets.Schedule
				if draft, ok := d.CollectionSchedules[collID]; ok {
					sc = draft
				}
				if !sc.Allows(hc.Device, hc.Now) {
					return "outside_collection_schedule", "Outside collection schedule"
				}
			}
			break
		}
		if hasCR {
			if !collectionScheduleAllows(d, user.ID, section, hc, cr) {
				return "outside_collection_schedule", "Outside collection schedule"
			}
			if len(cr.Hub.Items) > 0 && e.opts.FilterAvailable != nil && len(e.opts.FilterAvailable(ctx, cr.Hub.Items)) == 0 {
				return "filtered_by_availability", "No available titles right now"
			}
		}
		if emptied[kind] || emptied[hubID] || emptied[pol.Row] {
			return "filtered_by_restrictions", "No titles allowed for this viewer"
		}
		return "collection_empty", "Collection has no titles for this viewer"
	}
	if _, knownKind := kindByName[kind]; knownKind && !fitsSurface(kind, surf) {
		return "wrong_surface", "Not available on this page"
	}
	if emptied[kind] || emptied[hubID] || emptied[pol.Row] {
		return "filtered_by_restrictions", "No titles allowed for this viewer"
	}
	m := getModel()
	if m != nil && e.opts.FilterAvailable != nil {
		prefix := sectionPrefix(section)
		allowed := e.allowedFor(user)
		for _, c := range m.rows {
			if matchRank(pol.Row, prefix+"."+c.id, c.id, c.kind) == 0 {
				continue
			}
			var raw []core.Title
			src := c.items
			if section != "" && c.typed != nil {
				if typed, ok := c.typed[section]; ok {
					src = typed
				}
			}
			for _, it := range src {
				if section != "" && baseType(it.t.Type) != section {
					continue
				}
				if allowed != nil && !allowed(it.t.Type) {
					continue
				}
				if !contentrating.Allowed(it.t.ContentRating, user.MaxContentRating) {
					continue
				}
				raw = append(raw, it.t)
			}
			if len(raw) > 0 && len(e.opts.FilterAvailable(ctx, raw)) == 0 {
				return "filtered_by_availability", "No available titles right now"
			}
		}
	}
	if user.ID <= 0 && isPersonalKind(kind) {
		return "typical_viewer_personal", "Hidden for Typical viewer: personal row"
	}
	conf := 0.0
	if m != nil && m.taste != nil {
		conf = m.taste.Confidence
	}
	switch {
	case kind == KindShowcase && conf >= coldStartConfidence:
		return "cold_start_only", "Only shown for new viewers while learning their taste"
	case kind == KindTraktRecs || kind == KindTraktWatch:
		return "needs_trakt", "No Trakt titles for this viewer"
	case kind == KindLLM:
		if e.llm == nil {
			return "ai_unavailable", "AI recommendations are not configured"
		}
		if conf < minPersonalConfidence {
			return "needs_watch_history", "Needs more watch history for this viewer"
		}
		return "ai_unavailable", "No AI themed rows ready for this viewer"
	case kind == KindPlaylists:
		return "no_playlists", "Viewer has no playlists"
	case kind == KindFavorites:
		return "no_favorites", "Viewer has no favorites"
	case kind == KindUpNext:
		return "no_in_progress", "No next episodes in progress for this viewer"
	case kind == KindFinishShow:
		return "no_in_progress", "No stalled series in progress for this viewer"
	case kind == KindTop10 && conf < top10Confidence:
		return "needs_watch_history", "Needs more watch history for this viewer"
	case kind == KindDifferent && conf < 0.2:
		return "needs_watch_history", "Needs more watch history for this viewer"
	case isPersonalKind(kind) && conf < minPersonalConfidence:
		return "needs_watch_history", "Needs more watch history for this viewer"
	default:
		return "no_matching_titles", "No matching titles for this viewer"
	}
}

// resolveSection maps a preview section name to a library section.
func (e *Engine) resolveSection(name string) (int, core.MediaType, error) {
	var want core.MediaType
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "home", "foryou", "for-you":
		return 0, "", nil
	case "movies", "movie":
		want = core.TypeMovie
	case "shows", "show", "tv":
		want = core.TypeShow
	default:
		id, err := strconv.Atoi(name)
		if t, ok := e.opts.Sections[id]; err == nil && ok {
			return id, t, nil
		}
		return 0, "", &ValidationError{Problems: []Problem{{"section", "must be home, movies, shows or a library section id"}}}
	}
	ids := make([]int, 0, len(e.opts.Sections))
	for id := range e.opts.Sections {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if e.opts.Sections[id] == want {
			return id, want, nil
		}
	}
	return 0, "", &ValidationError{Problems: []Problem{{"section", "no " + string(want) + " library"}}}
}

func plexRow(hubID, title, kind, reason string, items []candItem) core.HomeRow {
	titles := make([]core.Title, len(items))
	reasons := map[core.RatingKey]string{}
	for i, it := range items {
		titles[i] = it.t
		if it.reason != "" {
			reasons[it.t.Key] = it.reason
		}
	}
	return core.HomeRow{HubID: hubID, Title: title, Kind: kind, Reason: reason, Source: core.RowSourcePlex,
		Items: titles, ItemReasons: reasons, Hub: core.Hub{ID: hubID, Title: title, Items: titles}}
}

func insertBeforeHidden(rows []core.HomeRow, row core.HomeRow) []core.HomeRow {
	i := len(rows)
	for i > 0 && rows[i-1].Hidden {
		i--
	}
	rows = append(rows, core.HomeRow{})
	copy(rows[i+1:], rows[i:])
	rows[i] = row
	return rows
}

func (e *Engine) playlistsRow(ctx context.Context, user *core.User) (core.HomeRow, bool) {
	ctx = core.WithCatalogViewer(ctx, user)
	if e.opts.Playlists == nil {
		return core.HomeRow{}, false
	}
	pls, err := e.opts.Playlists.List(ctx, user.ID)
	if err != nil || len(pls) == 0 {
		return core.HomeRow{}, false
	}
	names := make([]string, 0, len(pls))
	for _, p := range pls {
		names = append(names, p.Title)
	}
	sort.Strings(names)
	r := plexRow("home.playlists", "Recent Playlists", KindPlaylists,
		"The viewer's playlists (Plex's own row): "+strings.Join(names, ", ")+".", nil)
	return r, true
}

func (e *Engine) recentlyAdded(ctx context.Context, user *core.User, t core.MediaType, limit int) []candItem {
	ctx = core.WithCatalogViewer(ctx, user)
	q := core.BrowseQuery{Filter: core.Filter{Type: t, KnownAddedDate: true, MaxContentRating: user.MaxContentRating}, Sort: core.SortAdded, Desc: true, Limit: limit}
	page, err := e.catalog.Browse(ctx, q)
	if err != nil {
		return nil
	}
	out := make([]candItem, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, candItem{t: it})
	}
	return out
}

// Refresh rebuilds a viewer's model now (and asks the LLM for fresh themed
// rows when it supports invalidation).
func (e *Engine) Refresh(ctx context.Context, user *core.User) error {
	ctx = core.WithCatalogViewer(ctx, user)
	if inv, ok := e.llm.(core.ViewerInvalidator); ok && e.llm != nil && user != nil {
		inv.Invalidate(user.ID)
	}
	e.mu.Lock()
	for k := range e.owner {
		if strings.HasPrefix(k, fmt.Sprint(user.ID)+"|") {
			delete(e.owner, k)
		}
	}
	e.mu.Unlock()
	e.rec.flush(ctx)
	e.invalidateStats()
	if err := e.Rebuild(ctx, user); err != nil {
		return err
	}
	e.clearPresentation(user.ID)
	return nil
}

// Taste returns the viewer's taste profile (from the cached model when
// there is one).
func (e *Engine) Taste(ctx context.Context, user *core.User) (*core.TasteProfile, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	if user == nil {
		return nil, fmt.Errorf("personalize: no user")
	}
	e.mu.Lock()
	var ts *Taste
	if m := e.models[user.ID]; m != nil {
		ts = m.taste
	}
	e.mu.Unlock()
	if ts == nil {
		var err error
		if ts, _, err = e.buildTaste(ctx, *user); err != nil {
			return nil, err
		}
	}
	return ts.Profile(e.opts.ImageURL), nil
}

// SetTasteOverrides replaces the owner's corrections for a viewer and
// rebuilds their model so Plex reflects them on the next Home load.
func (e *Engine) SetTasteOverrides(ctx context.Context, user *core.User, os []core.TasteOverride) (*core.TasteProfile, error) {
	ctx = core.WithCatalogViewer(ctx, user)
	if user == nil {
		return nil, fmt.Errorf("personalize: no user")
	}
	ve := &ValidationError{}
	clean := make([]core.TasteOverride, 0, len(os))
	seen := map[string]int{}
	for i, o := range os {
		f := fmt.Sprintf("overrides[%d]", i)
		o.Kind = strings.ToLower(strings.TrimSpace(o.Kind))
		o.Value = strings.TrimSpace(o.Value)
		switch o.Kind {
		case "genre", "person", "keyword":
		default:
			ve.add(f+".kind", "must be genre, person or keyword")
		}
		if o.Value == "" {
			ve.add(f+".value", "is required")
		}
		if math.IsNaN(o.Weight) || o.Weight < -1 || o.Weight > 1 {
			ve.add(f+".weight", "must be between -1 and 1")
		}
		if o.Kind == "keyword" {
			o.Value = strings.ToLower(o.Value)
		}
		k := o.Kind + "\x00" + strings.ToLower(o.Value)
		if i, ok := seen[k]; ok {
			clean[i] = o // the last correction for a value wins
			continue
		}
		seen[k] = len(clean)
		clean = append(clean, o)
	}
	if err := ve.orNil(); err != nil {
		return nil, err
	}
	if err := e.store.setOverrides(ctx, user.ID, clean, e.now()); err != nil {
		return nil, err
	}
	e.notify("overrides:" + strconv.FormatInt(user.ID, 10))
	if err := e.Rebuild(ctx, user); err != nil {
		return nil, err
	}
	return e.Taste(ctx, user)
}

type lightCollectionSpecSource interface {
	ListSpecsLight(ctx context.Context) ([]core.CollectionSpec, error)
}

// AvailableRows lists the row kinds layout policies can target, plus owner
// collection rows recently served, each with the pages it can appear on
// (Sections) and its designer group.
func (e *Engine) AvailableRows(ctx context.Context) ([]core.RowDescriptor, error) {
	e.rec.flush(ctx)
	shows, err := e.store.rowShowsSince(ctx, e.now().AddDate(0, 0, -30))
	if err != nil {
		return nil, err
	}
	var extra []core.RowDescriptor
	for kind, st := range shows {
		if strings.HasPrefix(kind, KindCollectionPrefix) {
			extra = append(extra, core.RowDescriptor{HubID: kind, Title: st.title, Kind: kind,
				Description: "Owner collection row (" + st.hubID + ").", Sections: append([]string(nil), allSurfaces...), Group: groupCollections})
		}
	}
	if source, ok := e.opts.Collections.(core.CollectionSpecSource); ok {
		var specs []core.CollectionSpec
		if light, ok := e.opts.Collections.(lightCollectionSpecSource); ok {
			specs, err = light.ListSpecsLight(ctx)
		} else {
			specs, err = source.ListSpecs(ctx)
		}
		if err != nil {
			return nil, err
		}
		index := map[string]int{}
		for i, d := range extra {
			index[d.HubID] = i
		}
		for _, sp := range specs {
			key := KindCollectionPrefix + sp.ID
			if !sp.Enabled {
				continue
			}
			secs := collectionSections(sp.MediaType)
			if i, ok := index[key]; ok {
				extra[i].Sections = secs
				continue
			}
			index[key] = len(extra)
			extra = append(extra, core.RowDescriptor{HubID: key, Title: sp.Name, Kind: key,
				Description: "Owner collection; choose Always or Eligible to add it to this page.", Sections: secs, Group: groupCollections})
		}
	}
	if e.extraHomeRows != nil {
		index := map[string]int{}
		for i, d := range extra {
			index[d.HubID] = i
		}
		hc := core.NewHubContext(e.now(), "", "", "")
		hc.Device = core.DeviceTV
		mctx := reqmemo.With(ctx)
		for _, section := range allSurfaces {
			for _, hub := range e.extraHomeRows(mctx, &core.User{IsOwner: true}, hc, section, 1) {
				if i, ok := index[hub.ID]; ok {
					if !slices.Contains(extra[i].Sections, section) {
						extra[i].Sections = append(extra[i].Sections, section)
					}
					continue
				}
				index[hub.ID] = len(extra)
				group := groupPicks
				if strings.HasPrefix(hub.ID, "live.") {
					group = groupPlex
				}
				extra = append(extra, core.RowDescriptor{HubID: hub.ID, Kind: hubKind(hub.ID), Title: hub.Title,
					Description: "Additional Plex row.", Sections: []string{section}, Group: group})
			}
		}
	}
	return e.rowKindRegistry().RowDescriptors(extra), nil
}

// collectionSections is where a collection of a media type can appear.
func collectionSections(mediaType string) []string {
	switch mediaType {
	case "movie":
		return []string{surfHome, surfMovies}
	case "show":
		return []string{surfHome, surfShows}
	}
	return append([]string(nil), allSurfaces...)
}

// RowInsights reports, per row kind, how often it was shown and played
// over the last `days` days (row-days shown; plays attributed).
func (e *Engine) RowInsights(ctx context.Context, days int) ([]core.RowInsight, error) {
	if days <= 0 {
		days = 7
	}
	e.rec.flush(ctx)
	since := e.now().AddDate(0, 0, -days)
	shows, err := e.store.rowShowsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	plays, err := e.store.plays(ctx, 0, since)
	if err != nil {
		return nil, err
	}
	played := map[string]int{}
	for _, p := range sessions(plays, e.loc) {
		if p.kind != "" && !p.abandoned {
			played[p.kind]++
		}
	}
	kinds := map[string]bool{}
	for k := range shows {
		kinds[k] = true
	}
	for k := range played {
		kinds[k] = true
	}
	out := make([]core.RowInsight, 0, len(kinds))
	for k := range kinds {
		info := infoFor(k)
		ri := core.RowInsight{HubID: k, Title: info.title, Plays7d: played[k]}
		if st := shows[k]; st != nil {
			ri.Impressions7d = int(st.shows)
			if strings.HasPrefix(k, KindCollectionPrefix) && st.title != "" {
				ri.Title = st.title
			}
		}
		if ri.Impressions7d > 0 {
			ri.PlayRate = round3(float64(ri.Plays7d) / float64(ri.Impressions7d))
		}
		out = append(out, ri)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plays7d != out[j].Plays7d {
			return out[i].Plays7d > out[j].Plays7d
		}
		if out[i].Impressions7d != out[j].Impressions7d {
			return out[i].Impressions7d > out[j].Impressions7d
		}
		return out[i].HubID < out[j].HubID
	})
	return out, nil
}

// ---- activity ----------------------------------------------------------------------

type activityResult struct {
	since time.Time
	views map[int64][]viewing
}

// activityTTL caches server-wide activity for the owner dashboard.
const activityTTL = 2 * time.Minute

// activity returns every viewer's viewings since `since` (cached briefly).
func (e *Engine) activity(ctx context.Context, since time.Time) (*activityResult, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	if e.actRes != nil && !e.actRes.since.After(since) && e.now().Sub(e.actAt) < activityTTL {
		return e.actRes, nil
	}
	users, err := e.allUsers(ctx)
	if err != nil {
		return nil, err
	}
	res := &activityResult{since: since, views: map[int64][]viewing{}}
	plays, err := e.store.plays(ctx, 0, since)
	if err != nil {
		return nil, err
	}
	byUser := map[int64][]playRecord{}
	for _, p := range plays {
		byUser[p.userID] = append(byUser[p.userID], p)
	}
	many, hasMany := e.catalog.(interface {
		CachedGetMany(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error)
	})
	for _, u := range users {
		if e.state == nil {
			break
		}
		hist, err := e.state.History(ctx, u.ID, since, 5000)
		if err != nil {
			return nil, err
		}
		if len(hist) == 0 {
			continue
		}
		in := tasteInput{loc: e.loc, history: hist, plays: byUser[u.ID], titles: map[core.RatingKey]core.Title{}}
		keys := make([]core.RatingKey, 0, len(hist))
		seen := map[core.RatingKey]bool{}
		for _, h := range hist {
			if !seen[h.Key] {
				seen[h.Key] = true
				keys = append(keys, h.Key)
			}
		}
		if in.statuses, err = e.state.GetMany(ctx, u.ID, keys); err != nil {
			return nil, err
		}
		var want []core.RatingKey
		wantSeen := map[core.RatingKey]bool{}
		for _, k := range keys {
			if in.statuses[k].Duration > 0 || len(want) >= 300 {
				continue
			}
			tk := tasteKey(k)
			if wantSeen[tk] {
				continue
			}
			wantSeen[tk] = true
			want = append(want, tk)
		}
		var batch map[core.RatingKey]core.Title
		if hasMany && len(want) > 0 {
			batch, _ = many.CachedGetMany(ctx, want)
		}
		for _, tk := range want {
			if bt, ok := batch[tk]; ok {
				in.titles[tk] = bt
			} else if t, err := e.catalog.Get(ctx, tk); err == nil && t != nil {
				in.titles[tk] = *t
			}
		}
		res.views[u.ID] = viewingsFrom(in)
	}
	e.actRes, e.actAt = res, e.now()
	return res, nil
}

func (e *Engine) allUsers(ctx context.Context) ([]core.User, error) {
	if e.opts.Users != nil {
		return e.opts.Users.Users(ctx)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]core.User, 0, len(e.users))
	for id, u := range e.users {
		if id != genericID {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Activity returns server-wide viewing per local day for the last `days`
// days, oldest first, including empty days.
func (e *Engine) Activity(ctx context.Context, days int) ([]core.ActivityDay, error) {
	if days <= 0 {
		days = 30
	}
	now := e.now().In(e.loc)
	start := dayTime(dayOf(now.AddDate(0, 0, -(days-1)), e.loc), e.loc)
	res, err := e.activity(ctx, start)
	if err != nil {
		return nil, err
	}
	type agg struct {
		plays   int
		minutes float64
		users   map[int64]bool
	}
	byDay := map[int]*agg{}
	for uid, views := range res.views {
		for _, v := range views {
			if v.at.Before(start) {
				continue
			}
			d := dayOf(v.at, e.loc)
			a := byDay[d]
			if a == nil {
				a = &agg{users: map[int64]bool{}}
				byDay[d] = a
			}
			a.plays++
			a.minutes += v.minutes
			a.users[uid] = true
		}
	}
	out := make([]core.ActivityDay, 0, days)
	for i := 0; i < days; i++ {
		t := start.AddDate(0, 0, i)
		d := dayOf(t, e.loc)
		ad := core.ActivityDay{Date: t.Format("2006-01-02")}
		if a := byDay[d]; a != nil {
			ad.Plays, ad.Hours, ad.UniqueViewers = a.plays, math.Round(a.minutes/60*10)/10, len(a.users)
		}
		out = append(out, ad)
	}
	return out, nil
}

// ViewerStats summarises each viewer's last 30 days.
func (e *Engine) ViewerStats(ctx context.Context, userIDs []int64) (map[int64]core.ViewerStats, error) {
	since := e.now().AddDate(0, 0, -30)
	res, err := e.activity(ctx, since)
	if err != nil {
		return nil, err
	}
	e.rec.flush(ctx)
	seen, err := e.store.lastSeen(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]core.ViewerStats, len(userIDs))
	for _, id := range userIDs {
		st := core.ViewerStats{UserID: id, LastActive: seen[id]}
		minutes := 0.0
		for _, v := range res.views[id] {
			if v.at.Before(since) {
				continue
			}
			st.Plays30d++
			minutes += v.minutes
			if v.at.After(st.LastActive) {
				st.LastActive = v.at
			}
		}
		if e.state != nil {
			if hist, err := e.state.History(ctx, id, time.Time{}, 1); err == nil && len(hist) > 0 && hist[0].ViewedAt.After(st.LastActive) {
				st.LastActive = hist[0].ViewedAt
			}
		}
		st.HoursWatched30d = math.Round(minutes/60*10) / 10
		if !st.LastActive.IsZero() {
			st.LastActive = st.LastActive.UTC()
		}
		out[id] = st
	}
	return out, nil
}

// RecentActivity lists a viewer's latest viewings, newest first.
func (e *Engine) RecentActivity(ctx context.Context, userID int64, limit int) ([]core.ViewerActivity, error) {
	if limit <= 0 {
		limit = 20
	}
	out := []core.ViewerActivity{}
	if e.state == nil {
		return out, nil
	}
	hist, err := e.state.History(ctx, userID, time.Time{}, limit*4)
	if err != nil {
		return nil, err
	}
	type entry struct {
		at        time.Time
		completed bool
	}
	var order []core.RatingKey
	byKey := map[core.RatingKey]*entry{}
	for _, h := range hist { // newest first
		en := byKey[h.Key]
		if en == nil {
			if len(order) >= limit {
				continue
			}
			en = &entry{at: h.ViewedAt}
			byKey[h.Key] = en
			order = append(order, h.Key)
		}
		en.completed = en.completed || h.Completed
	}
	// Plex records resume positions independently of completed viewing history.
	// Include those positions in owner activity without manufacturing a Plex
	// history event or marking the title watched.
	progressing, err := e.state.InProgress(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	for _, ws := range progressing {
		if ws.ViewOffset <= 0 {
			continue
		}
		en := byKey[ws.Key]
		if en == nil {
			en = &entry{}
			byKey[ws.Key] = en
			order = append(order, ws.Key)
		}
		if !ws.LastViewedAt.Before(en.at) {
			en.at = ws.LastViewedAt
			en.completed = false
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := byKey[order[i]].at, byKey[order[j]].at
		if a.Equal(b) {
			return order[i] < order[j]
		}
		return a.After(b)
	})
	if len(order) > limit {
		order = order[:limit]
	}
	statuses, err := e.state.GetMany(ctx, userID, order)
	if err != nil {
		return nil, err
	}
	// One batched read for the page's titles; a title it lacks is read alone.
	var batch map[core.RatingKey]core.Title
	if many, ok := e.catalog.(interface {
		CachedGetMany(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error)
	}); ok {
		batch, _ = many.CachedGetMany(ctx, order)
	}
	for _, k := range order {
		var t *core.Title
		if bt, ok := batch[k]; ok {
			t = &bt
		} else if t, err = e.catalog.Get(ctx, k); err != nil {
			continue
		}
		if t == nil {
			continue
		}
		en := byKey[k]
		progress := 0.0
		ws := statuses[k]
		switch {
		case en.completed && ws.ViewOffset == 0:
			progress = 1
		case ws.Duration > 0:
			progress = round3(float64(ws.ViewOffset) / float64(ws.Duration))
		}
		out = append(out, core.ViewerActivity{Title: *t, At: en.at, Completed: en.completed, Progress: progress})
	}
	return out, nil
}

// Flush writes queued impressions now (tests, shutdown hooks).
func (e *Engine) Flush(ctx context.Context) { e.rec.flush(ctx) }
