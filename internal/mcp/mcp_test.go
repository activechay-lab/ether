package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent"
	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/mcp"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

type testMembers struct{ db *sql.DB }

func (m testMembers) ActiveUser(ctx context.Context, id int64) (*core.User, error) {
	var active, isOwner int
	err := m.db.QueryRowContext(ctx, `SELECT active, is_owner FROM auth_users WHERE id=?`, id).Scan(&active, &isOwner)
	if err != nil || active != 1 {
		return nil, core.ErrUnauthorized
	}
	return &core.User{ID: id, IsOwner: isOwner == 1}, nil
}

type stubServices struct {
	blocked   []string
	approvals map[string]builtin.ApprovalInfo
}

func (s *stubServices) ServerStatus(ctx context.Context) (builtin.ServerStatus, error) {
	return builtin.ServerStatus{Version: "1.0.0", ServerName: "Ether", SetupComplete: true}, nil
}
func (s *stubServices) Health(ctx context.Context) ([]builtin.HealthCheck, error) {
	return []builtin.HealthCheck{{Name: "Database", Status: "ok", Detail: "Reachable"}}, nil
}
func (s *stubServices) History(ctx context.Context, viewerID int64, since time.Time, limit int) ([]core.HistoryEntry, error) {
	return nil, nil
}
func (s *stubServices) HistoryEntry(ctx context.Context, id int64) (resolver.HistoryRecord, error) {
	return resolver.HistoryRecord{}, core.ErrNotFound
}
func (s *stubServices) ReleaseHealth(ctx context.Context, releaseID string, titleKey core.RatingKey) (builtin.ReleaseHealth, error) {
	return builtin.ReleaseHealth{Known: releaseID == "rel-1", Blocked: len(s.blocked) > 0}, nil
}
func (s *stubServices) LookupRelease(id string) (core.Release, bool) {
	if id == "rel-1" {
		return core.Release{ID: "rel-1", Name: "Sample.Release.1080p", Indexer: "NZBGeek"}, true
	}
	return core.Release{}, false
}
func (s *stubServices) Block(ctx context.Context, req resolver.BlockRequest, reason string) (resolver.BlockEntry, error) {
	s.blocked = append(s.blocked, req.ReleaseID)
	return resolver.BlockEntry{ReleaseID: req.ReleaseID, Reason: reason}, nil
}
func (s *stubServices) PinRelease(ctx context.Context, titleKey core.RatingKey, releaseID, note string) error {
	return nil
}
func (s *stubServices) Tail(limit int, level, query string) []builtin.LogLine { return nil }
func (s *stubServices) Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	return nil, nil
}
func (s *stubServices) Get(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	return nil, core.ErrNotFound
}
func (s *stubServices) SnapshotList(ctx context.Context, offset, limit int) ([]core.Collection, error) {
	return nil, nil
}
func (s *stubServices) SnapshotItems(ctx context.Context, key core.RatingKey, offset, limit int) (core.Page, error) {
	return core.Page{}, core.ErrNotFound
}
func (s *stubServices) Generate(ctx context.Context, prompt, mediaType string) (*core.CollectionSpec, string, error) {
	return &core.CollectionSpec{Name: "Draft", MediaType: "movie"}, "", nil
}
func (s *stubServices) Draft(ctx context.Context, raw, mediaType string) (*core.CollectionSpec, []string, error) {
	return &core.CollectionSpec{Name: "Draft", MediaType: "movie"}, nil, nil
}
func (s *stubServices) Preview(ctx context.Context, sp core.CollectionSpec, n int) (*core.CollectionPreview, error) {
	return &core.CollectionPreview{}, nil
}
func (s *stubServices) CreateSpec(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	return &sp, nil
}
func (s *stubServices) Channels(ctx context.Context) ([]livetv.Channel, error) { return nil, nil }
func (s *stubServices) StreamSourceStatuses(ctx context.Context, channelID string) ([]livetv.StreamSourceStatus, error) {
	return nil, nil
}
func (s *stubServices) ImportSource(ctx context.Context, src livetv.Source) (livetv.Source, error) {
	return src, nil
}
func (s *stubServices) Sources(ctx context.Context) ([]builtin.SourceInfo, error) { return nil, nil }
func (s *stubServices) AddStremio(ctx context.Context, addonURL string) (builtin.SourceInfo, error) {
	return builtin.SourceInfo{Name: "stremio", Enabled: true}, nil
}
func (s *stubServices) Plugins(ctx context.Context) ([]builtin.PluginInfo, error) { return nil, nil }
func (s *stubServices) PluginHealth(ctx context.Context, id string) (builtin.PluginInfo, error) {
	return builtin.PluginInfo{}, core.ErrNotFound
}
func (s *stubServices) SetPluginEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (s *stubServices) Approval(ctx context.Context, id string) (builtin.ApprovalInfo, error) {
	if a, ok := s.approvals[id]; ok {
		return a, nil
	}
	return builtin.ApprovalInfo{}, builtin.ErrApprovalNotFound
}
func (s *stubServices) Profiles(ctx context.Context) ([]builtin.ProfileInfo, error) {
	return []builtin.ProfileInfo{{Name: "Default", Ranking: core.DefaultRankingSteps}}, nil
}
func (s *stubServices) SetRanking(ctx context.Context, profile, media string, ranking []string) error {
	return nil
}
func (s *stubServices) AddRule(ctx context.Context, profile, media string, rule core.ReleaseRule) error {
	return nil
}
func (s *stubServices) Frontends(ctx context.Context) ([]builtin.FrontendInfo, error) {
	return []builtin.FrontendInfo{{ID: "plex", Name: "Plex", Enabled: true}}, nil
}
func (s *stubServices) SetFrontendEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (s *stubServices) Restart(ctx context.Context, whenIdle bool) error { return nil }

type stubResolver struct{ *stubServices }

func (r stubResolver) History(ctx context.Context, f resolver.HistoryFilter) ([]resolver.HistoryRecord, int, error) {
	return nil, 0, nil
}

func newTestCatalogue(t *testing.T, svc *stubServices) *tools.Catalogue {
	t.Helper()
	cat := tools.NewCatalogue()
	deps := builtin.Deps{
		Status:      svc,
		Playback:    svc,
		Resolver:    stubResolver{svc},
		Logs:        svc,
		Catalog:     svc,
		Collections: svc,
		LiveTV:      svc,
		Sources:     svc,
		Plugins:     svc,
		Approvals:   svc,
		Profiles:    svc,
		Frontends:   svc,
		Server:      svc,
		OwnerUser: func(ctx context.Context) (*core.User, error) {
			return &core.User{ID: 1, Username: "owner", IsOwner: true}, nil
		},
		Routes: func() []builtin.RouteInfo {
			return []builtin.RouteInfo{{Route: "/releases", Label: "Releases"}}
		},
	}
	if err := cat.Register(builtin.All(deps)...); err != nil {
		t.Fatal(err)
	}
	return cat
}

func postRPC(t *testing.T, h http.Handler, token string, method string, params any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:32400/ether/mcp", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestMCPBoundaryAuthResourcesAndScopes(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,is_owner,active,created_at,updated_at) VALUES(1,100,1,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	svc := &stubServices{approvals: map[string]builtin.ApprovalInfo{}}
	cat := newTestCatalogue(t, svc)
	agentStore := agent.NewStore(db)

	var audits []mcp.AuditEvent
	var onApprovals []agent.Approval
	srv, err := mcp.New(mcp.Options{
		Catalogue: cat,
		Approvals: agentStore,
		OnApproval: func(ctx context.Context, a agent.Approval) {
			onApprovals = append(onApprovals, a)
			svc.approvals[a.ID] = builtin.ApprovalInfo{
				ID:          a.ID,
				Tool:        a.ToolName,
				Category:    a.Category,
				Description: a.Description,
				Status:      a.Status,
			}
		},
		Authenticate: func(ctx context.Context, token string) (tools.Principal, error) {
			switch token {
			case "tok-read":
				return tools.Principal{ViewerID: 1, IsOwner: true, Surface: "mcp", Scopes: []string{"read"}}, nil
			case "tok-full":
				return tools.Principal{ViewerID: 1, IsOwner: true, Surface: "mcp", Scopes: []string{"full"}}, nil
			default:
				return tools.Principal{}, errors.New("unauthorized")
			}
		},
		Resources: mcp.DefaultResources(""),
		Version:   "1.2.3",
		Audit: func(ctx context.Context, e mcp.AuditEvent) {
			audits = append(audits, e)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	// 1. Boundary checks: DNS rebinding Host rejected with 421; cross-origin Origin rejected with 403.
	badHostReq := httptest.NewRequest(http.MethodPost, "http://evil.example.com/ether/mcp", strings.NewReader(`{}`))
	badHostRec := httptest.NewRecorder()
	h.ServeHTTP(badHostRec, badHostReq)
	if badHostRec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("expected 421 for bad Host, got %d", badHostRec.Code)
	}

	localHostReq := httptest.NewRequest(http.MethodPost, "http://attacker.local/ether/mcp", strings.NewReader(`{}`))
	localHostRec := httptest.NewRecorder()
	h.ServeHTTP(localHostRec, localHostReq)
	if localHostRec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unconfigured local hostname: %d", localHostRec.Code)
	}

	badOriginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:32400/ether/mcp", strings.NewReader(`{}`))
	badOriginReq.Header.Set("Origin", "https://evil.example.com")
	badOriginRec := httptest.NewRecorder()
	h.ServeHTTP(badOriginRec, badOriginReq)
	if badOriginRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin Origin, got %d", badOriginRec.Code)
	}

	// 2. Missing/invalid Bearer token -> 401 with WWW-Authenticate and X-Request-ID + Cache-Control: no-store.
	unauthRec, _ := postRPC(t, h, "", "ping", nil)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", unauthRec.Code)
	}
	if !strings.Contains(unauthRec.Header().Get("WWW-Authenticate"), `resource_metadata="http://127.0.0.1:32400/.well-known/oauth-protected-resource/ether/mcp"`) {
		t.Fatalf("expected WWW-Authenticate header, got %q", unauthRec.Header().Get("WWW-Authenticate"))
	}
	if unauthRec.Header().Get("Cache-Control") != "no-store" || unauthRec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("missing Cache-Control or X-Request-ID headers: %v", unauthRec.Header())
	}

	// 3. initialize, ping, resources/list, resources/read.
	initRec, initBody := postRPC(t, h, "tok-read", "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d", initRec.Code)
	}
	initRes := initBody["result"].(map[string]any)
	if initRes["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unexpected protocolVersion: %v", initRes["protocolVersion"])
	}

	_, resListBody := postRPC(t, h, "tok-read", "resources/list", nil)
	resources := resListBody["result"].(map[string]any)["resources"].([]any)
	if len(resources) != 5 {
		t.Fatalf("expected 5 static resources, got %d", len(resources))
	}

	_, schemaReadBody := postRPC(t, h, "tok-read", "resources/read", map[string]any{"uri": "ether://schemas/plugin-manifest"})
	contents := schemaReadBody["result"].(map[string]any)["contents"].([]any)
	schemaText := contents[0].(map[string]any)["text"].(string)
	var parsedSchema map[string]any
	if err := json.Unmarshal([]byte(schemaText), &parsedSchema); err != nil || parsedSchema["title"] == nil {
		t.Fatalf("expected valid embedded plugin-manifest.v1.json schema, got err=%v text=%q", err, schemaText)
	}

	// 4. Scope isolation: read token sees 18 tools and cannot call write tools.
	_, listReadBody := postRPC(t, h, "tok-read", "tools/list", nil)
	readTools := listReadBody["result"].(map[string]any)["tools"].([]any)
	if len(readTools) != 18 {
		t.Fatalf("expected 18 visible tools for read scope, got %d", len(readTools))
	}

	readNames := map[string]bool{}
	for _, raw := range readTools {
		readNames[raw.(map[string]any)["name"].(string)] = true
	}
	if !readNames["livetv_look_draft"] {
		t.Fatal("read scope must see the public look draft")
	}
	for _, name := range []string{"look_draft", "channel_summary", "livetv_look_save", "livetv_channel_look"} {
		if readNames[name] {
			t.Fatalf("read scope exposes console-only or write tool %s", name)
		}
	}
	for _, name := range []string{"livetv_look_save", "livetv_channel_look"} {
		_, denied := postRPC(t, h, "tok-read", "tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
		if denied["error"] == nil {
			t.Fatalf("read scope called write proposal %s", name)
		}
	}
	_, deniedCall := postRPC(t, h, "tok-read", "tools/call", map[string]any{
		"name":      "release_blocklist",
		"arguments": map[string]any{"releaseId": "rel-1"},
	})
	if deniedCall["error"] == nil {
		t.Fatalf("expected error calling write tool with read scope, got %v", deniedCall)
	}

	// 5. Full scope sees all 31 tools; calling a write tool creates a pending approval without mutating state.
	_, listFullBody := postRPC(t, h, "tok-full", "tools/list", nil)
	fullTools := listFullBody["result"].(map[string]any)["tools"].([]any)
	if len(fullTools) != 31 {
		t.Fatalf("expected 31 visible tools for full scope, got %d", len(fullTools))
	}

	_, propBody := postRPC(t, h, "tok-full", "tools/call", map[string]any{
		"name":      "release_blocklist",
		"arguments": map[string]any{"releaseId": "rel-1", "reason": "bad audio sync"},
	})
	propResult := propBody["result"].(map[string]any)
	structured := propResult["structuredContent"].(map[string]any)
	if structured["status"] != "pending_approval" {
		t.Fatalf("expected pending_approval status, got %v", structured)
	}
	approvalID, _ := structured["approvalId"].(string)
	if approvalID == "" {
		t.Fatalf("expected non-empty approvalId, got %v", structured)
	}
	if len(svc.blocked) != 0 {
		t.Fatalf("write tool mutated state before approval: %v", svc.blocked)
	}
	if len(onApprovals) != 1 || onApprovals[0].ID != approvalID {
		t.Fatalf("expected OnApproval callback for %s, got %+v", approvalID, onApprovals)
	}

	// Polling approval_status over MCP returns the pending approval.
	_, statusBody := postRPC(t, h, "tok-read", "tools/call", map[string]any{
		"name":      "approval_status",
		"arguments": map[string]any{"approvalId": approvalID},
	})
	statusStructured := statusBody["result"].(map[string]any)["structuredContent"].(map[string]any)
	if statusStructured["status"] != "pending" || statusStructured["approvalId"] != approvalID {
		t.Fatalf("unexpected approval_status: %v", statusStructured)
	}

	// 6. source_add_stremio over MCP requires console approval.
	_, secBody := postRPC(t, h, "tok-full", "tools/call", map[string]any{
		"name":      "source_add_stremio",
		"arguments": map[string]any{},
	})
	secStructured := secBody["result"].(map[string]any)["structuredContent"].(map[string]any)
	if secStructured["status"] != "pending_approval" {
		t.Fatalf("expected pending_approval, got %v", secStructured)
	}

	if len(audits) == 0 {
		t.Fatal("expected audit events to be recorded")
	}
}
