// Package builtin holds Ether's built-in agent tools (SPEC §5.10, WS5).
// Every tool ends in an existing service behind a narrow interface, is
// bounded, and never mutates before approval: write tools return a
// tools.Proposal, and the one credential (a Stremio add-on URL) goes
// through a tools.SecretRequest so it never reaches a transcript.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/textclip"
)

// Deps are the services the built-in tools call. The app adapts its real
// services to these interfaces; tests use fakes. Plugins and Approvals may
// be nil until WS7 and WS4 land.
type Deps struct {
	OnAir       OnAirService
	Status      StatusService
	Playback    PlaybackService
	Resolver    ResolverService
	Logs        LogService
	Catalog     CatalogService
	Collections CollectionService
	LiveTV      LiveTVService
	Sources     SourceService
	Plugins     PluginService // optional (WS7)
	Approvals   ApprovalReader
	Profiles    ProfileService
	Frontends   FrontendService
	Server      ServerService
	OwnerUser   func(ctx context.Context) (*core.User, error)
	Routes      func() []RouteInfo
	Now         func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// ServerStatus is the server_status snapshot.
type ServerStatus struct {
	Version         string
	ServerName      string
	StartedAt       time.Time
	SetupComplete   bool
	ActiveSessions  int
	IndexersEnabled int
	IndexersTotal   int
	LiveChannels    int
}

// HealthCheck is one health_details row. Status is ok, attention or error.
type HealthCheck struct {
	Name   string
	Status string
	Detail string
}

type StatusService interface {
	ServerStatus(ctx context.Context) (ServerStatus, error)
	Health(ctx context.Context) ([]HealthCheck, error)
}

type PlaybackService interface {
	History(ctx context.Context, viewerID int64, since time.Time, limit int) ([]core.HistoryEntry, error)
}

// ReleaseHealth is the last known health of one release, read without
// probing: a fresh check spends indexer budget and can blocklist, so it
// stays a console action, not a read tool.
type ReleaseHealth struct {
	Known     bool // among recent search results
	Blocked   bool
	Pinned    bool
	Status    string // last recorded status, when known
	Reason    string
	CheckedAt time.Time
}

// ResolverService is satisfied by resolver.Service except ReleaseHealth and
// PinRelease, which the app composes from its exported reads.
type ResolverService interface {
	History(ctx context.Context, f resolver.HistoryFilter) ([]resolver.HistoryRecord, int, error)
	HistoryEntry(ctx context.Context, id int64) (resolver.HistoryRecord, error)
	ReleaseHealth(ctx context.Context, releaseID string, titleKey core.RatingKey) (ReleaseHealth, error)
	LookupRelease(id string) (core.Release, bool)
	Block(ctx context.Context, req resolver.BlockRequest, reason string) (resolver.BlockEntry, error)
	PinRelease(ctx context.Context, titleKey core.RatingKey, releaseID, note string) error
}

// LogLine is one redacted log line.
type LogLine struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   map[string]string
}

type LogService interface {
	Tail(limit int, level, query string) []LogLine
}

type CatalogService interface {
	Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error)
	Get(ctx context.Context, key core.RatingKey) (*core.Title, error)
}

type CollectionService interface {
	SnapshotList(ctx context.Context, offset, limit int) ([]core.Collection, error)
	SnapshotItems(ctx context.Context, key core.RatingKey, offset, limit int) (core.Page, error)
	Generate(ctx context.Context, prompt, mediaType string) (*core.CollectionSpec, string, error)
	CreateSpec(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error)
	// Draft cleans and validates a model-written spec without saving it.
	Draft(ctx context.Context, raw, mediaType string) (*core.CollectionSpec, []string, error)
	// Preview runs an unsaved spec against the catalogue.
	Preview(ctx context.Context, sp core.CollectionSpec, n int) (*core.CollectionPreview, error)
}

type LiveTVService interface {
	Channels(ctx context.Context) ([]livetv.Channel, error)
	StreamSourceStatuses(ctx context.Context, channelID string) ([]livetv.StreamSourceStatus, error)
	ImportSource(ctx context.Context, src livetv.Source) (livetv.Source, error)
}

// SourceInfo is one sources_list row. Host is a bare host name; full URLs
// can carry keys and are never sent to a model.
type SourceInfo struct {
	Name    string
	Kind    string // newznab, stremio, cardigann, usenet provider, debrid, budget…
	Class   string
	Enabled bool
	Host    string
	Health  string // ok, attention, error or a short usage note
	Detail  string
}

type SourceService interface {
	Sources(ctx context.Context) ([]SourceInfo, error)
	// AddStremio stores a Stremio add-on install URL and enables it. The
	// URL can carry keys: callers must treat it as a secret.
	AddStremio(ctx context.Context, addonURL string) (SourceInfo, error)
}

// PluginInfo is one installed plugin (WS7).
type PluginInfo struct {
	ID      string
	Name    string
	Version string
	Kind    string
	Enabled bool
	Health  string
	Detail  string
}

type PluginService interface {
	Plugins(ctx context.Context) ([]PluginInfo, error)
	PluginHealth(ctx context.Context, id string) (PluginInfo, error)
	SetPluginEnabled(ctx context.Context, id string, enabled bool) error
}

// ProfileInfo is one quality profile with its ranking and rules.
// A profile serves one media; a movie and a TV profile may share a name.
type ProfileInfo struct {
	Name    string
	Media   string // core.MediaMovie or core.MediaTV
	Ranking []string
	Rules   []core.ReleaseRule
}

type ProfileService interface {
	Profiles(ctx context.Context) ([]ProfileInfo, error)
	SetRanking(ctx context.Context, profile, media string, ranking []string) error
	AddRule(ctx context.Context, profile, media string, rule core.ReleaseRule) error
}

// FrontendInfo is one client protocol frontend.
type FrontendInfo struct {
	ID      string
	Name    string
	Enabled bool
}

type FrontendService interface {
	Frontends(ctx context.Context) ([]FrontendInfo, error)
	SetFrontendEnabled(ctx context.Context, id string, enabled bool) error
}

type ServerService interface {
	Restart(ctx context.Context, whenIdle bool) error
}

// RouteInfo is one console destination for navigate.
type RouteInfo struct {
	Route string
	Label string
}

// ApprovalInfo is one proposed write as approval_status reports it. The
// app adapts WS4's agent.Store (internal/agent) to ApprovalReader.
type ApprovalInfo struct {
	ID          string
	Tool        string
	Category    string
	Description string
	Status      string // pending, approved, rejected, failed
	Note        string
	CreatedAt   time.Time
	DecidedAt   *time.Time
}

// ErrApprovalNotFound reports an unknown approval id.
var ErrApprovalNotFound = errors.New("no such approval")

// ApprovalReader reads approval records.
type ApprovalReader interface {
	Approval(ctx context.Context, id string) (ApprovalInfo, error)
}

// All returns every built-in tool. It panics on a nil required service,
// which is a wiring bug caught at startup.
func All(d Deps) []tools.Tool {
	for name, ok := range map[string]bool{
		"Status": d.Status != nil, "Playback": d.Playback != nil, "Resolver": d.Resolver != nil,
		"Logs": d.Logs != nil, "Catalog": d.Catalog != nil, "Collections": d.Collections != nil,
		"LiveTV": d.LiveTV != nil, "Sources": d.Sources != nil, "Profiles": d.Profiles != nil,
		"Frontends": d.Frontends != nil, "Server": d.Server != nil, "OwnerUser": d.OwnerUser != nil,
		"Routes": d.Routes != nil,
	} {
		if !ok {
			panic("builtin: missing service " + name)
		}
	}
	out := []tools.Tool{}
	out = append(out, readTools(d)...)
	out = append(out, writeTools(d)...)
	out = append(out, webTools()...)
	out = append(out, onairTools(d)...)
	return out
}

// --- input helpers (Execute re-validates; models send JSON numbers) ---

func str(in map[string]any, key string) (string, bool, error) {
	v, ok := in[key]
	if !ok || v == nil {
		return "", false, nil
	}
	s, is := v.(string)
	if !is {
		return "", false, tools.Userf("%s must be text", key)
	}
	return strings.TrimSpace(s), true, nil
}

func reqStr(in map[string]any, key string, max int) (string, error) {
	s, ok, err := str(in, key)
	if err != nil {
		return "", err
	}
	if !ok || s == "" {
		return "", tools.Userf("%s is required", key)
	}
	if max > 0 && len(s) > max {
		return "", tools.Userf("%s is too long (at most %d characters)", key, max)
	}
	return s, nil
}

func optStr(in map[string]any, key string, max int) (string, error) {
	s, _, err := str(in, key)
	if err != nil {
		return "", err
	}
	if max > 0 && len(s) > max {
		return "", tools.Userf("%s is too long (at most %d characters)", key, max)
	}
	return s, nil
}

func num(in map[string]any, key string) (float64, bool, error) {
	v, ok := in[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		return n, true, nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false, tools.Userf("%s must be a number", key)
		}
		return f, true, nil
	case int:
		return float64(n), true, nil
	case int64:
		return float64(n), true, nil
	}
	return 0, false, tools.Userf("%s must be a number", key)
}

// intArg reads an integer argument clamped to [min, max]; def when absent.
func intArg(in map[string]any, key string, def, min, max int) (int, error) {
	f, ok, err := num(in, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return def, nil
	}
	n := int(f)
	if float64(n) != f {
		return 0, tools.Userf("%s must be a whole number", key)
	}
	if n < min || n > max {
		return 0, tools.Userf("%s must be between %d and %d", key, min, max)
	}
	return n, nil
}

func boolArg(in map[string]any, key string, def bool) (bool, error) {
	v, ok := in[key]
	if !ok || v == nil {
		return def, nil
	}
	b, is := v.(bool)
	if !is {
		return def, tools.Userf("%s must be true or false", key)
	}
	return b, nil
}

func stringsArg(in map[string]any, key string, maxItems, maxLen int) ([]string, error) {
	v, ok := in[key]
	if !ok || v == nil {
		return nil, nil
	}
	raw, is := v.([]any)
	if !is {
		// A spec round-trip through JSON gives []any; direct Go callers
		// may send []string.
		if ss, is := v.([]string); is {
			raw = make([]any, len(ss))
			for i, s := range ss {
				raw[i] = s
			}
		} else {
			return nil, tools.Userf("%s must be a list of text", key)
		}
	}
	if len(raw) > maxItems {
		return nil, tools.Userf("%s has too many entries (at most %d)", key, maxItems)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, is := item.(string)
		if !is {
			return nil, tools.Userf("%s must be a list of text", key)
		}
		s = strings.TrimSpace(s)
		if len(s) > maxLen {
			return nil, tools.Userf("%s entries are too long (at most %d characters)", key, maxLen)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// keyArg parses a rating key (tools address titles by their string key).
func keyArg(in map[string]any, key string) (core.RatingKey, error) {
	if _, isStr := in[key].(string); !isStr {
		if f, ok, err := num(in, key); err != nil {
			return 0, err
		} else if ok {
			if f <= 0 || f > 1<<62 || float64(int64(f)) != f {
				return 0, tools.Userf("%s is not a valid title key", key)
			}
			return core.RatingKey(int64(f)), nil
		}
	}
	s, err := reqStr(in, key, 32)
	if err != nil {
		return 0, err
	}
	var k int64
	if _, err := fmt.Sscan(s, &k); err != nil || k <= 0 {
		return 0, tools.Userf("%s is not a valid title key", key)
	}
	return core.RatingKey(k), nil
}

// pageArgs reads and bounds page/pageSize. At most 50 per page, at most
// page 200, so nothing can page through the whole library.
func pageArgs(in map[string]any) (page, pageSize int, err error) {
	page, err = intArg(in, "page", 1, 1, 200)
	if err != nil {
		return 0, 0, err
	}
	pageSize, err = intArg(in, "pageSize", 20, 1, 50)
	return page, pageSize, err
}

// --- schema helpers ---

func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	raw, _ := json.Marshal(s)
	return raw
}

func pString(desc string, maxLen int) map[string]any {
	return map[string]any{"type": "string", "description": desc, "maxLength": maxLen}
}
func pInt(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}
func pBool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func pEnum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}
func pStrings(desc string, maxItems, maxLen int) map[string]any {
	return map[string]any{"type": "array", "description": desc, "maxItems": maxItems,
		"items": map[string]any{"type": "string", "maxLength": maxLen}}
}
func pPage() map[string]any {
	return map[string]any{
		"page":     pInt("Page number, starting at 1", 1, 200),
		"pageSize": pInt("Entries per page, at most 50", 1, 50),
	}
}

func tool(t tools.Tool) tools.Tool {
	t.Audience = tools.AudienceOwner
	t.Owner = "builtin"
	return t
}

// titleSummary is the compact title shape results use. Third-party text
// (title, summary) is untrusted data for the model; summaries are cut short.
func titleSummary(t core.Title) map[string]any {
	m := map[string]any{
		"key":   fmt.Sprint(int64(t.Key)),
		"title": truncate(t.Title, 200),
		"type":  string(t.Type),
	}
	if t.Year != 0 {
		m["year"] = t.Year
	}
	if t.ShowTitle != "" {
		m["show"] = truncate(t.ShowTitle, 200)
	}
	if t.Season != 0 || t.Episode != 0 {
		m["season"] = t.Season
		m["episode"] = t.Episode
	}
	if t.Rating != 0 {
		m["rating"] = t.Rating
	}
	return m
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return textclip.Bytes(s, n) + "…"
}

func paged[T any](items []T, page, pageSize int) ([]T, int) {
	total := len(items)
	start := (page - 1) * pageSize
	if start >= total {
		return nil, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return items[start:end], total
}

func pageMeta(page, pageSize, total int) map[string]any {
	return map[string]any{
		"page": page, "pageSize": pageSize, "total": total,
		"hasMore": page*pageSize < total,
	}
}
