package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// registerBuiltinTools registers Ether's built-in agent tools (WS5) into the
// shared tool catalogue and registers their extend.KindTool descriptors in the
// descriptor registry so the console and Studio can list every tool.
func (a *App) registerBuiltinTools(c *tools.Catalogue) error {
	startedAt := time.Now()
	deps := builtin.Deps{
		OnAir:       a.onair,
		Status:      &appStatusService{a: a, startedAt: startedAt},
		Playback:    a.state,
		Resolver:    &appResolverService{a: a},
		Logs:        &appLogService{a: a},
		Catalog:     a.catalog,
		Collections: a.collections,
		LiveTV:      a.livetv,
		Sources:     &appSourceService{a: a},
		Plugins:     &appPluginService{a: a},
		Approvals:   &appApprovalReader{a: a},
		Profiles:    &appProfileService{a: a},
		Frontends:   &appFrontendService{a: a},
		Server:      &appServerService{a: a},
		OwnerUser: func(ctx context.Context) (*core.User, error) {
			if a.users == nil {
				return nil, errors.New("no owner")
			}
			return a.users.Owner(ctx)
		},
		Routes: a.consoleRoutes,
	}
	built := builtin.All(deps)
	if err := c.Register(built...); err != nil {
		return err
	}
	if a.extendRT != nil && a.extendRT.registry != nil {
		if err := registerBuiltinToolDescriptors(a.extendRT.registry, built); err != nil {
			return err
		}
	}
	return nil
}

// builtinToolLabel returns the extend.T label for a built-in tool name using
// literal extend.T("...") calls so web/scripts/i18n.mjs extracts every title.
func builtinToolLabel(name, fallback string) extend.Text {
	switch name {
	case "livetv_channel_apply":
		return extend.T("Apply channel")
	case "look_draft", "livetv_look_draft":
		return extend.T("Draft a look")
	case "channel_summary":
		return extend.T("Channel programming summary")
	case "livetv_look_save":
		return extend.T("Save a channel look")
	case "livetv_channel_look":
		return extend.T("Apply a channel look")
	case "server_status":
		return extend.T("Server status")
	case "health_details":
		return extend.T("Health details")
	case "playback_history":
		return extend.T("Playback history")
	case "resolve_trace":
		return extend.T("Resolve trace")
	case "release_health":
		return extend.T("Release health")
	case "logs_tail":
		return extend.T("Recent logs")
	case "catalog_search":
		return extend.T("Search the catalogue")
	case "title_details":
		return extend.T("Title details")
	case "collection_list":
		return extend.T("Collections")
	case "collection_preview":
		return extend.T("Collection preview")
	case "livetv_guide_match":
		return extend.T("Match with AI")
	case "livetv_channels":
		return extend.T("Live TV channels")
	case "sources_list":
		return extend.T("Sources")
	case "plugins_list":
		return extend.T("Plugins")
	case "plugin_health":
		return extend.T("Plugin health")
	case "approval_status":
		return extend.T("Approval status")
	case "navigate":
		return extend.T("Open a page")
	case "collection_create_from_prompt":
		return extend.T("Create a collection")
	case "release_blocklist":
		return extend.T("Blocklist a release")
	case "release_pin":
		return extend.T("Pin a release")
	case "quality_profiles":
		return extend.T("Quality profiles")
	case "quality_order_set":
		return extend.T("Change quality order")
	case "format_scores_set":
		return extend.T("Change custom format scores")
	case "rule_add":
		return extend.T("Add a release rule")
	case "source_add_stremio":
		return extend.T("Add a plugin from Stremio")
	case "source_add_m3u":
		return extend.T("Add a playlist source")
	case "frontend_enable":
		return extend.T("Turn a frontend on or off")
	case "plugin_enable":
		return extend.T("Turn a plugin on or off")
	case "server_restart":
		return extend.T("Restart the server")
	default:
		return extend.Text{Source: fallback}
	}
}

func registerBuiltinToolDescriptors(r *extend.Registry, list []tools.Tool) error {
	ds := make([]extend.Descriptor, 0, len(list))
	for i, tl := range list {
		group := "Read"
		if !tl.ReadOnly {
			group = "Write"
		}
		d := extend.Descriptor{
			Kind:  extend.KindTool,
			ID:    tl.Name,
			Owner: extend.Builtin,
			Label: builtinToolLabel(tl.Name, tl.Title),
			Group: group,
			Order: i + 1,
		}.WithMeta(map[string]any{
			"name":        tl.Name,
			"readOnly":    tl.ReadOnly,
			"destructive": tl.Destructive,
			"openWorld":   tl.OpenWorld,
		})
		ds = append(ds, d)
	}
	return r.Register(ds...)
}

type appStatusService struct {
	a         *App
	startedAt time.Time
}

func (s *appStatusService) ServerStatus(ctx context.Context) (builtin.ServerStatus, error) {
	cfg := s.a.cfg.Get()
	name := cfg.Server.FriendlyName
	if name == "" {
		name = "Ether"
	}
	enabledIx := 0
	for _, ix := range cfg.Indexers {
		if ix.Enabled {
			enabledIx++
		}
	}
	active := 0
	if s.a.playback != nil {
		active = len((ownerPlayback{Playback: s.a.playback, app: s.a}).Sessions())
	}
	channels := 0
	if s.a.livetv != nil {
		if list, err := s.a.livetv.Channels(ctx); err == nil {
			channels = len(list)
		}
	}
	return builtin.ServerStatus{
		Version:         s.a.opts.Version,
		ServerName:      name,
		StartedAt:       s.startedAt,
		SetupComplete:   cfg.SetupComplete,
		ActiveSessions:  active,
		IndexersEnabled: enabledIx,
		IndexersTotal:   len(cfg.Indexers),
		LiveChannels:    channels,
	}, nil
}

func (s *appStatusService) Health(ctx context.Context) ([]builtin.HealthCheck, error) {
	cfg := s.a.cfg.Get()
	var out []builtin.HealthCheck

	dbH := s.a.databaseHealth()
	dbStatus := "ok"
	dbDetail := "Database is reachable."
	if dbH.State != "ok" && dbH.State != "" {
		dbStatus = "attention"
		if !dbH.Reachable {
			dbStatus = "error"
		}
		if dbH.Message != "" {
			dbDetail = dbH.Message
		} else if dbH.LastError != "" {
			dbDetail = dbH.LastError
		}
	}
	out = append(out, builtin.HealthCheck{Name: "Database", Status: dbStatus, Detail: dbDetail})

	if s.a.cache != nil {
		used, budget := s.a.cache.Usage()
		detail := fmt.Sprintf("Mode %s, %d MiB used", firstNonEmptyStr(cfg.Cache.Mode, "demand"), used/(1<<20))
		if budget > 0 {
			detail = fmt.Sprintf("%s of %d MiB budget", detail, budget/(1<<20))
		}
		out = append(out, builtin.HealthCheck{Name: "Cache", Status: "ok", Detail: detail})
	}

	enabledSources := 0
	for _, ix := range cfg.Indexers {
		if ix.Enabled {
			enabledSources++
		}
	}
	for _, p := range cfg.Usenet.Providers {
		if p.Enabled {
			enabledSources++
		}
	}
	for _, p := range cfg.Debrid.Providers {
		if p.Enabled {
			enabledSources++
		}
	}
	srcStatus := "ok"
	srcDetail := fmt.Sprintf("%d enabled release sources.", enabledSources)
	if enabledSources == 0 {
		srcStatus = "attention"
		srcDetail = "No release sources are enabled."
	}
	out = append(out, builtin.HealthCheck{Name: "Sources", Status: srcStatus, Detail: srcDetail})

	if s.a.transcoder != nil {
		hw := s.a.transcoder.HWAccel()
		cap := s.a.transcoder.Capability()
		st := "ok"
		det := fmt.Sprintf("FFmpeg %s, hardware acceleration: %s", s.a.transcoder.Version(), firstNonEmptyStr(hw.Name, "none"))
		if cap.PlaybackError != "" {
			st = "error"
			det = cap.PlaybackError
		} else if cap.Notice != "" {
			st = "attention"
			det = cap.Notice
		}
		out = append(out, builtin.HealthCheck{Name: "Transcoder", Status: st, Detail: det})
	}

	return out, nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type appResolverService struct {
	a *App
}

func (r *appResolverService) History(ctx context.Context, f resolver.HistoryFilter) ([]resolver.HistoryRecord, int, error) {
	return r.a.resolver.History(ctx, f)
}

func (r *appResolverService) HistoryEntry(ctx context.Context, id int64) (resolver.HistoryRecord, error) {
	return r.a.resolver.HistoryEntry(ctx, id)
}

func (r *appResolverService) LookupRelease(id string) (core.Release, bool) {
	if rel, ok := r.a.resolver.LookupRelease(id); ok {
		return rel, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, _, err := r.a.resolver.History(ctx, resolver.HistoryFilter{Limit: 50})
	if err != nil {
		return core.Release{}, false
	}
	for _, row := range rows {
		if row.ReleaseID == id {
			return core.Release{ID: row.ReleaseID, Name: row.ReleaseName, Indexer: row.Indexer, Size: row.Size}, true
		}
		entry, eerr := r.a.resolver.HistoryEntry(ctx, row.ID)
		if eerr != nil || entry.Trace == nil {
			continue
		}
		for _, c := range entry.Trace.Candidates {
			if c.ReleaseID == id {
				return core.Release{ID: c.ReleaseID, Name: c.Name, Indexer: c.Indexer, Size: c.Size}, true
			}
		}
	}
	return core.Release{}, false
}

func (r *appResolverService) ReleaseHealth(ctx context.Context, releaseID string, titleKey core.RatingKey) (builtin.ReleaseHealth, error) {
	_, known := r.a.resolver.LookupRelease(releaseID)
	blocked, err := r.a.resolver.ReleaseBlocked(ctx, releaseID)
	if err != nil {
		return builtin.ReleaseHealth{}, err
	}
	out := builtin.ReleaseHealth{Known: known, Blocked: blocked}
	if blocked {
		out.Status = "blocked"
		if entries, _, berr := r.a.resolver.Blocklist(ctx, resolver.BlockFilter{Query: releaseID, Limit: 20}); berr == nil {
			for _, e := range entries {
				if e.ReleaseID == releaseID {
					out.Known = true
					out.Reason = e.Reason
					out.CheckedAt = e.UpdatedAt
					break
				}
			}
		}
	}
	for _, p := range r.a.resolver.Pins() {
		if p.Release.ID == releaseID && (titleKey == 0 || p.TitleKey == titleKey) {
			out.Pinned = true
			out.Known = true
			if out.Status == "" && p.LastStatus != "" {
				out.Status = string(p.LastStatus)
				out.Reason = p.LastReason
				out.CheckedAt = p.LastCheckedAt
			}
			break
		}
	}
	if out.Status == "" {
		rows, _, herr := r.a.resolver.History(ctx, resolver.HistoryFilter{TitleKey: titleKey, Limit: 5})
		if herr == nil {
			for _, row := range rows {
				entry, eerr := r.a.resolver.HistoryEntry(ctx, row.ID)
				if eerr != nil || entry.Trace == nil {
					continue
				}
				for _, c := range entry.Trace.Candidates {
					if c.ReleaseID == releaseID {
						out.Known = true
						if c.HealthChecked {
							out.CheckedAt = entry.At
							if c.Healthy {
								out.Status = "healthy"
							} else {
								out.Status = "unhealthy"
							}
							out.Reason = c.Reason
						} else if c.Rejected && c.Reason != "" {
							out.Status = "rejected"
							out.Reason = c.Reason
						}
						break
					}
				}
				if out.Status != "" {
					break
				}
			}
		}
	}
	return out, nil
}

func (r *appResolverService) Block(ctx context.Context, req resolver.BlockRequest, reason string) (resolver.BlockEntry, error) {
	entry, err := r.a.resolver.Block(ctx, req, reason)
	if err == nil && entry.TitleKey != 0 && r.a.playback != nil {
		r.a.playback.InvalidateTitleEverywhere(entry.TitleKey)
	}
	return entry, err
}

func (r *appResolverService) PinRelease(ctx context.Context, titleKey core.RatingKey, releaseID, note string) error {
	rel, ok := r.LookupRelease(releaseID)
	if !ok {
		return resolver.ErrUnknownRelease
	}
	var titleName string
	if r.a.catalog != nil {
		if t, err := r.a.catalog.Get(ctx, titleKey); err == nil && t != nil {
			titleName = t.Title
		}
	}
	if _, err := r.a.resolver.Pin(ctx, titleKey, titleName, rel, note); err != nil {
		return err
	}
	if r.a.playback != nil {
		r.a.playback.InvalidateTitleEverywhere(titleKey)
	}
	return nil
}

type appLogService struct {
	a *App
}

func (l *appLogService) Tail(limit int, level, query string) []builtin.LogLine {
	if l.a.logs == nil {
		return nil
	}
	entries := l.a.logs.Entries(limit, level, query)
	out := make([]builtin.LogLine, 0, len(entries))
	for _, e := range entries {
		out = append(out, builtin.LogLine{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Attrs:   e.Attrs,
		})
	}
	return out
}

type appSourceService struct {
	a *App
}

func (s *appSourceService) Sources(ctx context.Context) ([]builtin.SourceInfo, error) {
	cfg := s.a.cfg.Get()
	var out []builtin.SourceInfo
	for _, ix := range cfg.Indexers {
		kind := ix.Kind
		if kind == "" {
			kind = config.IndexerKindNewznab
		}
		class := "indexer"
		if ix.IsStremio() {
			class = "addon"
		}
		out = append(out, builtin.SourceInfo{
			Name:    ix.Name,
			Kind:    kind,
			Class:   class,
			Enabled: ix.Enabled,
			Host:    bareHost(ix.URL),
			Health:  enabledHealth(ix.Enabled),
		})
	}
	for _, p := range cfg.Usenet.Providers {
		out = append(out, builtin.SourceInfo{
			Name:    firstNonEmptyStr(p.Name, p.Host),
			Kind:    "usenet",
			Class:   "usenet",
			Enabled: p.Enabled,
			Host:    p.Host,
			Health:  enabledHealth(p.Enabled),
		})
	}
	for _, p := range cfg.Debrid.Providers {
		out = append(out, builtin.SourceInfo{
			Name:    firstNonEmptyStr(p.Name, p.Kind),
			Kind:    firstNonEmptyStr(p.Kind, "debrid"),
			Class:   "debrid",
			Enabled: p.Enabled,
			Health:  enabledHealth(p.Enabled),
		})
	}
	return out, nil
}

func (s *appSourceService) AddStremio(ctx context.Context, addonURL string) (builtin.SourceInfo, error) {
	base, err := stremio.BaseURL(addonURL)
	if err != nil {
		return builtin.SourceInfo{}, tools.Userf("%s", err.Error())
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	id := "stremio-" + hex.EncodeToString(b[:])
	name := base.Host
	ix := config.Indexer{
		ID:       id,
		Name:     name,
		URL:      base.String(),
		Kind:     config.IndexerKindStremio,
		Protocol: "torrent",
		Enabled:  true,
	}
	if err := s.a.cfg.Update(func(c *config.Config) error {
		c.Indexers = append(c.Indexers, ix)
		return nil
	}); err != nil {
		return builtin.SourceInfo{}, err
	}
	return builtin.SourceInfo{
		Name:    name,
		Kind:    config.IndexerKindStremio,
		Class:   "addon",
		Enabled: true,
		Host:    base.Host,
		Health:  "unknown",
	}, nil
}

func bareHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func enabledHealth(enabled bool) string {
	if enabled {
		return "unknown"
	}
	return "disabled"
}

type appPluginService struct {
	a *App
}

func (p *appPluginService) mgr() *plugins.Manager {
	if p.a.pluginsRT == nil {
		return nil
	}
	return p.a.pluginsRT.mgr
}

func (p *appPluginService) Plugins(ctx context.Context) ([]builtin.PluginInfo, error) {
	mgr := p.mgr()
	if mgr == nil {
		return nil, nil
	}
	recs, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]builtin.PluginInfo, 0, len(recs))
	for _, r := range recs {
		h, _ := mgr.HealthOf(ctx, r.ID)
		out = append(out, toPluginInfo(r, h))
	}
	return out, nil
}

func (p *appPluginService) PluginHealth(ctx context.Context, id string) (builtin.PluginInfo, error) {
	mgr := p.mgr()
	if mgr == nil {
		return builtin.PluginInfo{}, core.ErrNotFound
	}
	r, err := mgr.Get(ctx, id)
	if err != nil {
		if errors.Is(err, plugins.ErrNotFound) {
			return builtin.PluginInfo{}, core.ErrNotFound
		}
		return builtin.PluginInfo{}, err
	}
	h, err := mgr.HealthOf(ctx, id)
	if err != nil && !errors.Is(err, plugins.ErrNotFound) {
		return builtin.PluginInfo{}, err
	}
	return toPluginInfo(r, h), nil
}

func (p *appPluginService) SetPluginEnabled(ctx context.Context, id string, enabled bool) error {
	mgr := p.mgr()
	if mgr == nil {
		return core.ErrNotFound
	}
	err := mgr.SetEnabled(ctx, id, enabled)
	if errors.Is(err, plugins.ErrNotFound) {
		return tools.Userf("no plugin with that id")
	}
	return err
}

func toPluginInfo(r plugins.Record, h plugins.Health) builtin.PluginInfo {
	status := h.Status
	if status == "" {
		if r.Enabled {
			status = "ok"
		} else {
			status = "disabled"
		}
	}
	return builtin.PluginInfo{
		ID:      r.ID,
		Name:    r.Name,
		Version: r.Version,
		Kind:    r.Kind,
		Enabled: r.Enabled,
		Health:  status,
		Detail:  firstNonEmptyStr(h.Reason, h.LastError),
	}
}

type appApprovalReader struct {
	a *App
}

func (ar *appApprovalReader) Approval(ctx context.Context, id string) (builtin.ApprovalInfo, error) {
	if ar.a.db == nil {
		return builtin.ApprovalInfo{}, builtin.ErrApprovalNotFound
	}
	var info builtin.ApprovalInfo
	var decisionNote, failureReason, threadID string
	var createdMs, decidedMs int64
	err := ar.a.db.QueryRowContext(ctx,
		`SELECT id, tool_name, category, description, status, decision_note, failure_reason, created_at, decided_at, thread_id FROM agent_approvals WHERE id=?`, id).
		Scan(&info.ID, &info.Tool, &info.Category, &info.Description, &info.Status, &decisionNote, &failureReason, &createdMs, &decidedMs, &threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return builtin.ApprovalInfo{}, builtin.ErrApprovalNotFound
	}
	if err != nil {
		return builtin.ApprovalInfo{}, err
	}
	if info.Tool == "source_add_stremio" && info.Status == "approved" {
		var secretStatus string
		err := ar.a.db.QueryRowContext(ctx, `SELECT status FROM agent_secret_requests WHERE thread_id=? ORDER BY created_at DESC LIMIT 1`, threadID).Scan(&secretStatus)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return builtin.ApprovalInfo{}, err
		}
		if secretStatus != "stored" {
			info.Status = "pending_secret"
			decisionNote = "The owner must submit the add-on URL in the console before the source is added."
		}
	}
	info.Note = firstNonEmptyStr(decisionNote, failureReason)
	if createdMs > 0 {
		info.CreatedAt = time.UnixMilli(createdMs).UTC()
	}
	if decidedMs > 0 {
		t := time.UnixMilli(decidedMs).UTC()
		info.DecidedAt = &t
	}
	return info, nil
}

type appProfileService struct {
	a *App
}

func (ps *appProfileService) Profiles(ctx context.Context) ([]builtin.ProfileInfo, error) {
	cfg := ps.a.cfg.Get()
	out := make([]builtin.ProfileInfo, 0, len(cfg.Quality.Profiles))
	for _, p := range cfg.Quality.Profiles {
		out = append(out, builtin.ProfileInfo{
			Name:               p.Name,
			Media:              p.MediaType,
			Scores:             maps.Clone(p.CustomFormatScores),
			Rules:              append([]core.ReleaseRule(nil), p.ReleaseRules...),
			QualityOrder:       config.CloneQualityOrder(p.QualityOrder),
			Language:           p.Language,
			PreferredLanguages: append([]string(nil), p.PreferredLanguages...),
			MaxHeight:          p.MaxHeight,
			MinHeight:          p.MinHeight,
			AllowedQualities:   append([]string(nil), p.AllowedQualities...),
		})
	}
	return out, nil
}

func (ps *appProfileService) SetQualityOrder(ctx context.Context, profile, media string, order []core.QualityItem) error {
	return ps.a.saveValidated(func(c *config.Config) error {
		for i := range c.Quality.Profiles {
			p := &c.Quality.Profiles[i]
			if strings.EqualFold(p.Name, profile) && p.MediaType == media {
				p.QualityOrder = config.CloneQualityOrder(order)
				return nil
			}
		}
		return tools.Userf("no profile named %q", profile)
	})
}

func (ps *appProfileService) SetFormatScores(ctx context.Context, profile, media string, scores map[string]int) error {
	return ps.a.saveValidated(func(c *config.Config) error {
		for i := range c.Quality.Profiles {
			p := &c.Quality.Profiles[i]
			if strings.EqualFold(p.Name, profile) && p.MediaType == media {
				if p.CustomFormatScores == nil {
					p.CustomFormatScores = map[string]int{}
				}
				for id, v := range scores {
					if v == 0 {
						delete(p.CustomFormatScores, id)
					} else {
						p.CustomFormatScores[id] = v
					}
				}
				return nil
			}
		}
		return tools.Userf("no profile named %q", profile)
	})
}

func (ps *appProfileService) AddRule(ctx context.Context, profile, media string, rule core.ReleaseRule) error {
	return ps.a.saveValidated(func(c *config.Config) error {
		for i := range c.Quality.Profiles {
			if strings.EqualFold(c.Quality.Profiles[i].Name, profile) && c.Quality.Profiles[i].MediaType == media {
				p := &c.Quality.Profiles[i]
				baseID := rule.ID
				if baseID == "" {
					baseID = "rule"
				}
				id := baseID
				for n := 2; hasRuleID(p.ReleaseRules, id); n++ {
					id = fmt.Sprintf("%s-%d", baseID, n)
				}
				rule.ID = id
				p.ReleaseRules = append(p.ReleaseRules, rule)
				return nil
			}
		}
		return tools.Userf("no profile named %q", profile)
	})
}

func hasRuleID(rules []core.ReleaseRule, id string) bool {
	for _, r := range rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

type appFrontendService struct {
	a *App
}

func (fs *appFrontendService) Frontends(ctx context.Context) ([]builtin.FrontendInfo, error) {
	if fs.a.frontends != nil && fs.a.frontends.control != nil {
		list, err := fs.a.frontends.control.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]builtin.FrontendInfo, 0, len(list))
		for _, f := range list {
			out = append(out, builtin.FrontendInfo{
				ID:      string(f.Descriptor.ID),
				Name:    f.Descriptor.Name,
				Enabled: f.Enabled,
			})
		}
		return out, nil
	}
	cfg := fs.a.cfg.Get()
	var out []builtin.FrontendInfo
	// Wire the built-in frontend identities for the agent app list.
	for _, id := range []string{"plex", "jellyfin", "emby", "stremio", "dlna", "webdav", "m3u"} {
		enabled := cfg.Frontends[id].Enabled
		if cfg.Frontends == nil && frontendHostPolicy(frontend.Protocol(id)).MainListener {
			enabled = true
		}
		out = append(out, builtin.FrontendInfo{ID: id, Name: id, Enabled: enabled})
	}
	return out, nil
}

func (fs *appFrontendService) SetFrontendEnabled(ctx context.Context, id string, enabled bool) error {
	if fs.a.frontends != nil && fs.a.frontends.control != nil {
		return fs.a.frontends.control.Toggle(ctx, frontend.Protocol(id), enabled)
	}
	return fs.a.cfg.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			if !frontendHostPolicy(frontend.Protocol(id)).MainListener {
				c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
			}
		}
		v := c.Frontends[id]
		v.Enabled = enabled
		c.Frontends[id] = v
		return nil
	})
}

type appServerService struct {
	a *App
}

func (ss *appServerService) Restart(ctx context.Context, whenIdle bool) error {
	return ss.a.scheduleRestart("restart approved in Ask Ether", whenIdle)
}

func (a *App) consoleRoutes() []builtin.RouteInfo {
	out := []builtin.RouteInfo{
		{Route: "/", Label: "Overview"},
		{Route: "/sources", Label: "Sources"},
		{Route: "/releases", Label: "Releases"},
		{Route: "/collections", Label: "Collections"},
		{Route: "/livetv", Label: "Live TV"},
		{Route: "/viewers", Label: "Viewers"},
		{Route: "/apps", Label: "Apps"},
		{Route: "/integrations", Label: "Integrations"},
		{Route: "/plugins", Label: "Plugins"},
		{Route: "/notifications", Label: "Notifications"},
		{Route: "/logs", Label: "Logs"},
		{Route: "/settings", Label: "Settings"},
	}
	if a.extendRT != nil && a.extendRT.registry != nil {
		seen := map[string]bool{}
		for _, r := range out {
			seen[r.Route] = true
		}
		for _, d := range a.extendRT.registry.List(extend.KindPage) {
			var meta struct {
				Route string `json:"route"`
			}
			_, _ = extend.MetaOf[struct {
				Route string `json:"route"`
			}](d)
			if m, err := extend.MetaOf[map[string]any](d); err == nil {
				if rt, ok := m["route"].(string); ok {
					meta.Route = rt
				}
			}
			if meta.Route == "" || seen[meta.Route] {
				continue
			}
			label := d.Label.Source
			if label == "" {
				label = d.Label.Resolved
			}
			if label == "" {
				label = d.ID
			}
			seen[meta.Route] = true
			out = append(out, builtin.RouteInfo{Route: meta.Route, Label: label})
		}
	}
	return out
}
