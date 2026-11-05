// Package mcp serves Ether's tool catalogue over MCP streamable HTTP at
// /ether/mcp (SPEC §5.12), plus the console pairing API that mints MCP
// grants. It is a thin adapter over internal/agent/tools: the catalogue
// decides what a principal may see, writes come back as pending approvals
// in WS4's agent store, and only mapped error messages reach a client.
//
// The official github.com/modelcontextprotocol/go-sdk was evaluated
// (v1.8.0: pure Go, Apache-2.0/MIT transition licence) and rejected for
// this surface: it pulls OAuth/JWT/x-tools weight Ether does not need,
// and the catalogue already owns validation, visibility and audit. This
// is the minimal stateless JSON-RPC surface instead, mirroring appkit-mcp.
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/agent"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
)

// ApprovalStore is the slice of WS4's agent.Store MCP writes need.
type ApprovalStore interface {
	CreateApproval(ctx context.Context, a agent.Approval) (agent.Approval, error)
}

// transcriptStore is optionally satisfied by *agent.Store so MCP write
// proposals also create a console conversation card the owner can approve in
// the Ask Ether drawer.
type transcriptStore interface {
	CreateThread(ctx context.Context, title string) (agent.Thread, error)
	AppendMessage(ctx context.Context, threadID, role string, parts json.RawMessage) (agent.Message, error)
	UpdateParts(ctx context.Context, messageID string, parts json.RawMessage) error
}

// AuditEvent is one tools/call, for the audit sink.
type AuditEvent struct {
	Tool      string
	ViewerID  int64
	Scopes    []string
	Status    string // ok, proposal, secret_request, error, denied
	Error     string // mapped message only
	Duration  time.Duration
	RequestID string
}

// Options configures the MCP server.
type Options struct {
	Catalogue *tools.Catalogue
	// Approvals receives write proposals. Nil means write tools answer
	// with an error result instead of a pending approval.
	Approvals ApprovalStore
	// OnApproval is called after a write proposal is stored, so the app
	// can notify the console stream (admin.StreamAgentApproval).
	OnApproval func(ctx context.Context, a agent.Approval)
	// Authenticate resolves a bearer token to a principal (surface "mcp").
	Authenticate func(ctx context.Context, token string) (tools.Principal, error)
	Resources    []Resource
	Version      string
	Logger       *slog.Logger
	// AllowedHosts and AllowedOrigins extend the boundary allowlists.
	AllowedHosts   []string
	AllowedOrigins []string
	Audit          func(ctx context.Context, e AuditEvent)
	ToolTimeout    time.Duration // default 30s
	Now            func() time.Time
}

// Server is the MCP endpoint. It is safe for concurrent use.
type Server struct {
	opts Options
	log  *slog.Logger
}

// New builds an MCP server.
func New(opts Options) (*Server, error) {
	if opts.Catalogue == nil {
		return nil, fmt.Errorf("mcp: catalogue is required")
	}
	if opts.Authenticate == nil {
		return nil, fmt.Errorf("mcp: authenticate is required")
	}
	if opts.ToolTimeout <= 0 {
		opts.ToolTimeout = 30 * time.Second
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{opts: opts, log: log}, nil
}

// Supported protocol versions, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const maxBodyBytes = 1 << 20

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Handler serves the MCP endpoint. Mount it LAN-only, like the console.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	requestID := resolveRequestID(r)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodOptions {
		s.preflight(w, r)
		return
	}
	if v := checkBoundary(r, s.opts.AllowedHosts, s.opts.AllowedOrigins); v != nil {
		s.writeRPC(w, r, v.status, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: v.code, Message: v.message},
		})
		return
	}
	principal, err := s.authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+absoluteURL(r, "/.well-known/oauth-protected-resource/ether/mcp")+`"`)
		s.writeRPC(w, r, http.StatusUnauthorized, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: -32001, Message: "Unauthorized"},
		})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		s.writeRPC(w, r, http.StatusMethodNotAllowed, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: -32000, Message: "Method not allowed"},
		})
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		s.writeRPC(w, r, http.StatusUnsupportedMediaType, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: -32700, Message: "Content-Type must be application/json"},
		})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		s.writeRPC(w, r, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: -32700, Message: "Request body is unreadable or too large"},
		})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		s.writeRPC(w, r, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: -32700, Message: "Parse error"},
		})
		return
	}
	// Notifications carry no id and expect no reply.
	if len(req.ID) == 0 || string(req.ID) == "null" && strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(req.ID) == 0 {
		req.ID = nullID
	}
	if pv := r.Header.Get("MCP-Protocol-Version"); pv != "" {
		w.Header().Set("MCP-Protocol-Version", pv)
	}
	result, rpcErr := s.dispatch(r.Context(), r, principal, req, requestID)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
	s.writeRPC(w, r, http.StatusOK, resp)
}

var nullID = json.RawMessage("null")

func (s *Server) authenticate(r *http.Request) (tools.Principal, error) {
	token, ok := authhdr.Bearer(r.Header.Get("Authorization"))
	if !ok || strings.TrimSpace(token) == "" {
		return tools.Principal{}, fmt.Errorf("missing bearer token")
	}
	return s.opts.Authenticate(r.Context(), strings.TrimSpace(token))
}

func (s *Server) dispatch(ctx context.Context, r *http.Request, p tools.Principal, req rpcRequest, requestID string) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if params.ProtocolVersion == v {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"subscribe": false, "listChanged": false},
			},
			"serverInfo":   map[string]any{"name": "ether", "version": s.opts.Version},
			"instructions": "Ether is a media server. Read tools answer questions about playback, releases and health. Write tools never apply directly: they return a pending approval the owner decides in the Ether console. Read the ether://docs/how-ether-works resource first.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return s.listTools(ctx, p), nil
	case "tools/call":
		return s.callTool(ctx, r, p, req.Params, requestID)
	case "resources/list":
		out := make([]any, 0, len(s.opts.Resources))
		for _, res := range s.opts.Resources {
			out = append(out, map[string]any{
				"uri": res.URI, "name": res.Name, "description": res.Description, "mimeType": res.MIMEType,
			})
		}
		return map[string]any{"resources": out}, nil
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.URI == "" {
			return nil, &rpcError{Code: -32602, Message: "uri is required"}
		}
		for _, res := range s.opts.Resources {
			if res.URI == params.URI {
				return map[string]any{"contents": []any{map[string]any{
					"uri": res.URI, "mimeType": res.MIMEType, "text": res.Text,
				}}}, nil
			}
		}
		return nil, &rpcError{Code: -32002, Message: "Resource not found"}
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return map[string]any{}, nil
		}
		return nil, &rpcError{Code: -32601, Message: "Method not found: " + req.Method}
	}
}

func (s *Server) listTools(ctx context.Context, p tools.Principal) map[string]any {
	visible := s.opts.Catalogue.Visible(ctx, p)
	out := make([]any, 0, len(visible))
	for _, t := range visible {
		out = append(out, map[string]any{
			"name": t.Name, "title": t.Title, "description": t.Description,
			"inputSchema": t.InputSchema,
			"annotations": map[string]any{
				"readOnlyHint":    t.ReadOnly,
				"destructiveHint": t.Destructive,
				"openWorldHint":   t.OpenWorld,
			},
		})
	}
	return map[string]any{"tools": out}
}

func (s *Server) callTool(ctx context.Context, r *http.Request, p tools.Principal, rawParams json.RawMessage, requestID string) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		return nil, &rpcError{Code: -32602, Message: "tools/call needs a tool name"}
	}
	input := map[string]any{}
	if len(params.Arguments) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(params.Arguments)))
		dec.UseNumber()
		if err := dec.Decode(&input); err != nil {
			return nil, &rpcError{Code: -32602, Message: "arguments must be an object"}
		}
	}
	start := s.now()
	audit := AuditEvent{Tool: params.Name, ViewerID: p.ViewerID, Scopes: p.Scopes, RequestID: requestID}
	defer func() {
		audit.Duration = s.now().Sub(start)
		if s.opts.Audit != nil {
			s.opts.Audit(ctx, audit)
		}
	}()

	tool, ok := s.opts.Catalogue.Lookup(ctx, p, params.Name)
	if !ok {
		audit.Status = "denied"
		// A read-scope token never sees write tools, exactly like the
		// agent loop: unknown and forbidden look the same.
		return nil, &rpcError{Code: -32602, Message: "Unknown tool: " + params.Name}
	}
	callCtx, cancel := context.WithTimeout(ctx, s.opts.ToolTimeout)
	defer cancel()
	result, err := tool.Execute(callCtx, p, input)
	if err != nil {
		audit.Status = "error"
		audit.Error = tools.SafeMessage(err)
		s.log.Debug("mcp tool failed", "tool", params.Name, "err", err)
		return toolResult(map[string]any{"error": audit.Error}, true), nil
	}

	switch {
	case result.Proposal != nil:
		audit.Status = "proposal"
		return s.proposalResult(ctx, r, p, tool, result.Proposal), nil
	case result.Secret != nil:
		audit.Status = "proposal"
		return s.proposalResult(ctx, r, p, tool, &tools.Proposal{
			Category:    tool.Title,
			Description: result.Secret.Purpose + " After approval, enter the address in the secret card to add the source.",
			Input:       input,
		}), nil
	default:
		audit.Status = "ok"
		structured := map[string]any{}
		for k, v := range result.Data {
			structured[k] = v
		}
		if result.Navigate != "" {
			structured["status"] = "navigate"
			structured["consoleUrl"] = consoleURL(r, result.Navigate)
		}
		if len(result.Links) > 0 {
			links := make([]any, 0, len(result.Links))
			for _, l := range result.Links {
				links = append(links, map[string]any{"label": l.Label, "url": consoleURL(r, l.Route)})
			}
			structured["links"] = links
		}
		return toolResult(structured, false), nil
	}
}

func (s *Server) proposalResult(ctx context.Context, r *http.Request, p tools.Principal, tool tools.Tool, prop *tools.Proposal) any {
	if s.opts.Approvals == nil {
		return toolResult(map[string]any{
			"error": "Approvals are not available on this server yet, so this change cannot be queued.",
		}, true)
	}
	details, _ := json.Marshal(prop.Details)
	if prop.Details == nil {
		details = []byte(`[]`)
	}
	input := prop.Input
	if input == nil {
		input = map[string]any{}
	}
	var threadID, messageID string
	ts, hasTranscript := s.opts.Approvals.(transcriptStore)
	if hasTranscript {
		title := prop.Category
		if title == "" {
			title = tool.Title
		}
		if th, err := ts.CreateThread(ctx, title); err == nil {
			threadID = th.ID
			if msg, err := ts.AppendMessage(ctx, threadID, "assistant", json.RawMessage(`[]`)); err == nil {
				messageID = msg.ID
			}
		}
	}
	appr, err := s.opts.Approvals.CreateApproval(ctx, agent.Approval{
		ThreadID: threadID, MessageID: messageID,
		ToolName: tool.Name, Input: input,
		Category: prop.Category, Description: prop.Description, Details: details,
	})
	if err != nil {
		s.log.Warn("mcp approval store failed", "tool", tool.Name, "err", err)
		return toolResult(map[string]any{"error": tools.SafeMessage(err)}, true)
	}
	if hasTranscript && messageID != "" {
		detailParts := make([]any, 0, len(prop.Details))
		for _, d := range prop.Details {
			detailParts = append(detailParts, map[string]any{"label": d.Label, "value": d.Value})
		}
		parts := []map[string]any{
			{
				"type":       "tool-" + tool.Name,
				"toolCallId": "call_" + appr.ID,
				"state":      "output-available",
				"input":      input,
				"output": map[string]any{
					"status":     "pending_approval",
					"approvalId": appr.ID,
					"change":     prop.Description,
				},
			},
			{
				"type":          "approval-request",
				"approvalId":    appr.ID,
				"categoryLabel": prop.Category,
				"description":   prop.Description,
				"details":       detailParts,
				"status":        "pending",
			},
		}
		if raw, err := json.Marshal(parts); err == nil {
			_ = ts.UpdateParts(ctx, messageID, raw)
		}
	}
	if s.opts.OnApproval != nil {
		s.opts.OnApproval(ctx, appr)
	}
	return toolResult(map[string]any{
		"status":      "pending_approval",
		"approvalId":  appr.ID,
		"consoleUrl":  consoleURL(r, "/"),
		"category":    prop.Category,
		"description": prop.Description,
		"message":     "This change is waiting for the owner's approval in the Ether console. It has not been applied. Poll approval_status with the approvalId to learn the decision.",
	}, false)
}

func toolResult(structured map[string]any, isError bool) map[string]any {
	text, _ := json.MarshalIndent(structured, "", "  ")
	out := map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": structured,
	}
	if isError {
		out["isError"] = true
	}
	return out
}

// consoleURL builds an absolute console link for a console route.
func consoleURL(r *http.Request, route string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return scheme + "://" + r.Host + "/ether" + route
}

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// writeRPC answers as JSON, or as one SSE event for clients that only
// accept text/event-stream (streamable HTTP allows either).
func (s *Server) writeRPC(w http.ResponseWriter, r *http.Request, status int, resp rpcResponse) {
	body, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"Internal error"}}`, http.StatusInternalServerError)
		return
	}
	accept := r.Header.Get("Accept")
	sseOnly := strings.Contains(accept, "text/event-stream") && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "*/*")
	if sseOnly {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && originAllowed(r, origin, s.opts.AllowedOrigins) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, Mcp-Session-Id, X-Request-ID")
	w.Header().Set("Access-Control-Expose-Headers", "MCP-Protocol-Version, X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

var requestIDChars = func(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '.' || r == '_' || r == ':' || r == '-'
}

// resolveRequestID echoes a well-formed X-Request-ID or mints one, as in
// appkit-mcp: a hostile header can never inject log noise.
func resolveRequestID(r *http.Request) string {
	v := r.Header.Get("X-Request-ID")
	if len(v) >= 8 && len(v) <= 200 {
		ok := true
		for _, ch := range v {
			if !requestIDChars(ch) {
				ok = false
				break
			}
		}
		if ok {
			return v
		}
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
