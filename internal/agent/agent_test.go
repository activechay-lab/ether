package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/store"
)

// fakeModel plays scripted steps: each StreamTools call returns one script.
type fakeModel struct {
	mu       sync.Mutex
	scripts  [][]llm.Event
	calls    int
	requests []llm.ToolRequest
}

func (f *fakeModel) Enabled() bool                  { return true }
func (f *fakeModel) ProviderID() string             { return "fake" }
func (f *fakeModel) ModelName() string              { return "fake-1" }
func (f *fakeModel) ToolsSupported() (bool, string) { return true, "" }

func (f *fakeModel) StreamTools(ctx context.Context, req llm.ToolRequest) (<-chan llm.Event, error) {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if idx >= len(f.scripts) {
		idx = len(f.scripts) - 1
	}
	evs := f.scripts[idx]
	out := make(chan llm.Event, len(evs)+1)
	go func() {
		defer close(out)
		for _, ev := range evs {
			select {
			case out <- ev:
			case <-ctx.Done():
				out <- llm.Event{Kind: llm.EventError, Err: ctx.Err()}
				return
			}
		}
		out <- llm.Event{Kind: llm.EventDone}
	}()
	return out, nil
}

// blockingModel never emits until its context is cancelled.
type blockingModel struct{}

func (blockingModel) Enabled() bool                  { return true }
func (blockingModel) ProviderID() string             { return "fake" }
func (blockingModel) ModelName() string              { return "fake-1" }
func (blockingModel) ToolsSupported() (bool, string) { return true, "" }
func (blockingModel) StreamTools(ctx context.Context, req llm.ToolRequest) (<-chan llm.Event, error) {
	out := make(chan llm.Event, 1)
	go func() {
		defer close(out)
		<-ctx.Done()
		out <- llm.Event{Kind: llm.EventError, Err: ctx.Err()}
	}()
	return out, nil
}

func callEv(id, name, input string) []llm.Event {
	return []llm.Event{
		{Kind: llm.EventToolCallStart, ToolCallID: id, ToolName: name},
		{Kind: llm.EventToolCallDelta, ToolCallID: id, ToolName: name, InputDelta: input},
		{Kind: llm.EventToolCall, ToolCallID: id, ToolName: name, Input: json.RawMessage(input)},
		{Kind: llm.EventUsage, InputTokens: 10, OutputTokens: 5},
	}
}

type fixture struct {
	svc     *Service
	store   *Store
	model   *fakeModel
	applied *[]string
	stored  *[]string
	p       tools.Principal
}

func newFixture(t *testing.T, model *fakeModel, opts Options) *fixture {
	t.Helper()
	db := store.OpenTest(t)
	applied := []string{}
	stored := []string{}
	fx := &fixture{store: NewStore(db), model: model, applied: &applied, stored: &stored,
		p: tools.Principal{IsOwner: true, Surface: "console"}}
	cat := tools.NewCatalogue()
	schema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	readSchema := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	err := cat.Register(
		tools.Tool{Name: "server_status", Title: "Server status", Description: "status", InputSchema: readSchema,
			ReadOnly: true, Audience: tools.AudienceOwner, Owner: "builtin",
			Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
				return tools.Result{Data: map[string]any{"healthy": true}}, nil
			}},
		tools.Tool{Name: "catalog_search", Title: "Search", Description: "search", InputSchema: schema,
			ReadOnly: true, Audience: tools.AudienceOwner, Owner: "builtin",
			Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
				return tools.Result{Data: map[string]any{"found": in["title"]}}, nil
			}},
		tools.Tool{Name: "release_block", Title: "Block a release", Description: "block", InputSchema: schema,
			Audience: tools.AudienceOwner, Owner: "builtin",
			Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
				title, _ := in["title"].(string)
				return tools.Result{Proposal: &tools.Proposal{
					Category: "Blocklist a release", Description: "Block " + title,
					Details: []tools.ProposalDetail{{Label: "Release", Value: title}},
					Apply: func(ctx context.Context) (tools.Result, error) {
						*fx.applied = append(*fx.applied, title)
						return tools.Result{Data: map[string]any{"blocked": title}}, nil
					}}}, nil
			}},
		tools.Tool{Name: "source_connect", Title: "Connect a source", Description: "connect", InputSchema: readSchema,
			Audience: tools.AudienceOwner, Owner: "builtin",
			Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
				return tools.Result{Secret: &tools.SecretRequest{
					ProviderLabel: "Real-Debrid", CredentialLabel: "API token", Purpose: "Connect your account.",
					Store: func(ctx context.Context, value string) error {
						*fx.stored = append(*fx.stored, value)
						return nil
					}}}, nil
			}},
		tools.Tool{Name: "boom_tool", Title: "Boom", Description: "fails", InputSchema: readSchema,
			ReadOnly: true, Audience: tools.AudienceOwner, Owner: "builtin",
			Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
				return tools.Result{}, fmt.Errorf("database exploded with secret detail")
			}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Model == nil {
		opts.Model = model
	}
	opts.Catalogue = cat
	opts.Store = fx.store
	fx.svc = New(opts)
	return fx
}

func (fx *fixture) emitCollect() (func(Chunk) error, *[]Chunk) {
	chunks := []Chunk{}
	return func(c Chunk) error { chunks = append(chunks, c); return nil }, &chunks
}

func TestLoopMultiToolWithApproval(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{
		append(append(callEv("c1", "server_status", `{}`), callEv("c2", "catalog_search", `{"title":"Alien"}`)...), callEv("c3", "release_block", `{"title":"Bad.Release"}`)...),
		{{Kind: llm.EventTextDelta, Text: "All set. The block waits for approval."}},
	}}
	fx := newFixture(t, model, Options{})
	emit, chunks := fx.emitCollect()
	threadID, turnID, err := fx.svc.RunTurn(context.Background(), fx.p, "", "check and block", TurnContext{Route: "/releases"}, emit)
	if err != nil {
		t.Fatal(err)
	}
	types := []string{}
	outputs := 0
	for _, c := range *chunks {
		types = append(types, c["type"].(string))
		if c["type"] == "tool-output-available" {
			outputs++
		}
	}
	if outputs != 3 {
		t.Errorf("tool outputs = %d, chunks %v", outputs, types)
	}
	if len(*fx.applied) != 0 {
		t.Errorf("proposal applied before approval: %v", *fx.applied)
	}
	msgs, err := fx.store.AllMessages(context.Background(), threadID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messages = %d err %v", len(msgs), err)
	}
	var parts []map[string]any
	_ = json.Unmarshal(msgs[1].Parts, &parts)
	var approvalID string
	toolParts := 0
	for _, p := range parts {
		if strings.HasPrefix(p["type"].(string), "tool-") {
			toolParts++
		}
		if p["type"] == "approval-request" {
			approvalID, _ = p["approvalId"].(string)
			if p["categoryLabel"] != "Blocklist a release" || p["status"] != "pending" {
				t.Errorf("approval part = %v", p)
			}
		}
	}
	if toolParts != 3 {
		t.Errorf("tool parts = %d", toolParts)
	}
	if approvalID == "" {
		t.Fatalf("no approval part in %s", msgs[1].Parts)
	}
	// Model was told it waits for approval.
	last := model.requests[len(model.requests)-1]
	foundPending := false
	for _, m := range last.Messages {
		for _, r := range m.ToolResults {
			if strings.Contains(r.Content, "pending_approval") {
				foundPending = true
			}
		}
	}
	if !foundPending {
		t.Error("model not told change waits for approval")
	}
	// Approve: rebuilds via Execute, applies, patches part.
	appr, err := fx.svc.DecideApproval(context.Background(), fx.p, approvalID, "approved", "yes")
	if err != nil {
		t.Fatal(err)
	}
	if appr.Status != "approved" || len(*fx.applied) != 1 || (*fx.applied)[0] != "Bad.Release" {
		t.Errorf("after approve: status %s applied %v", appr.Status, *fx.applied)
	}
	msgs, _ = fx.store.AllMessages(context.Background(), threadID)
	_ = json.Unmarshal(msgs[1].Parts, &parts)
	for _, p := range parts {
		if p["type"] == "approval-request" && (p["status"] != "approved" || p["decisionNote"] != "yes") {
			t.Errorf("patched approval part = %v", p)
		}
	}
	if msgs[1].ID != turnID {
		t.Errorf("turn id mismatch")
	}
	// Audit rows exist, no secrets.
	var n int
	// direct store check via messages only; audit exercised in store test
	_ = n
}

func TestLoopStepCapAndBudget(t *testing.T) {
	script := callEv("c1", "server_status", `{}`)
	model := &fakeModel{scripts: [][]llm.Event{script}}
	fx := newFixture(t, model, Options{MaxSteps: 2, TokenBudget: 20})
	emit, chunks := fx.emitCollect()
	_, _, err := fx.svc.RunTurn(context.Background(), fx.p, "", "loop forever", TurnContext{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls > 2 {
		t.Errorf("model calls = %d, want <= 2", model.calls)
	}
	hasFinish := false
	for _, c := range *chunks {
		if c["type"] == "finish" {
			hasFinish = true
		}
	}
	if !hasFinish {
		t.Error("no finish chunk")
	}
	// The stored transcript explains the stop instead of ending silently.
	thread, _ := fx.svc.store.ListThreads(context.Background(), 1)
	msgs, _ := fx.svc.store.AllMessages(context.Background(), thread[0].ID)
	if last := string(msgs[len(msgs)-1].Parts); !strings.Contains(last, "Ask me to continue") {
		t.Errorf("no stored stop note: %s", last)
	}
	if got := fx.svc.maxSteps; got != 2 {
		t.Errorf("maxSteps clamp = %d", got)
	}
	if clamp := New(Options{MaxSteps: 999}); clamp.maxSteps != 32 {
		t.Errorf("clamp high = %d", clamp.maxSteps)
	}
	if clamp := New(Options{MaxSteps: 1}); clamp.maxSteps != 2 {
		t.Errorf("clamp low = %d", clamp.maxSteps)
	}
}

func TestLoopCancellationAndSafeError(t *testing.T) {
	// Tool error must reach model only as SafeMessage.
	model := &fakeModel{scripts: [][]llm.Event{
		callEv("c1", "boom_tool", `{}`),
		{{Kind: llm.EventTextDelta, Text: "It failed."}},
	}}
	fx := newFixture(t, model, Options{})
	emit, _ := fx.emitCollect()
	_, _, err := fx.svc.RunTurn(context.Background(), fx.p, "", "boom", TurnContext{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range model.requests {
		for _, m := range req.Messages {
			for _, r := range m.ToolResults {
				if strings.Contains(r.Content, "secret detail") {
					t.Fatalf("unsafe error reached model: %s", r.Content)
				}
			}
			if strings.Contains(m.Content, "secret detail") {
				t.Fatalf("unsafe error in content: %s", m.Content)
			}
		}
	}
	// Cancellation: a blocking model is stopped through CancelTurn using the
	// turn id from the start chunk.
	slow := &blockingModel{}
	fx2 := newFixture(t, &fakeModel{scripts: [][]llm.Event{{{Kind: llm.EventTextDelta, Text: "hi"}}}}, Options{Model: slow, TurnTimeout: time.Minute})
	chunks2 := []Chunk{}
	emit2 := func(c Chunk) error {
		chunks2 = append(chunks2, c)
		if c["type"] == "start" {
			if id, _ := c["messageId"].(string); id != "" {
				go fx2.svc.CancelTurn(id)
			}
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := fx2.svc.RunTurn(context.Background(), fx2.p, "", "x", TurnContext{}, emit2)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled turn did not stop")
	}
	hasFinish := false
	for _, c := range chunks2 {
		if c["type"] == "finish" {
			hasFinish = true
		}
	}
	if !hasFinish {
		t.Error("cancelled turn emitted no finish chunk")
	}
}

func TestValidateInputRejectsBad(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{
		callEv("c1", "catalog_search", `{"wrong":1}`),
		{{Kind: llm.EventTextDelta, Text: "ok"}},
	}}
	fx := newFixture(t, model, Options{})
	emit, chunks := fx.emitCollect()
	_, _, err := fx.svc.RunTurn(context.Background(), fx.p, "", "search", TurnContext{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	sawErr := false
	for _, c := range *chunks {
		if c["type"] == "tool-output-error" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("invalid input was not rejected")
	}
}

func TestSecretFlowNeverStoredOrSentToModel(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{
		callEv("c1", "source_connect", `{}`),
		{{Kind: llm.EventTextDelta, Text: "Use the card."}},
	}}
	fx := newFixture(t, model, Options{})
	emit, _ := fx.emitCollect()
	threadID, _, err := fx.svc.RunTurn(context.Background(), fx.p, "", "connect", TurnContext{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := fx.store.AllMessages(context.Background(), threadID)
	var parts []map[string]any
	_ = json.Unmarshal(msgs[1].Parts, &parts)
	var reqID string
	for _, p := range parts {
		if p["type"] == "secret-request" {
			reqID, _ = p["requestId"].(string)
			if p["providerLabel"] != "Real-Debrid" || p["status"] != "pending" {
				t.Errorf("secret part = %v", p)
			}
		}
	}
	if reqID == "" {
		t.Fatalf("no secret part in %s", msgs[1].Parts)
	}
	secretValue := "s3cr3t-token-value"
	if err := fx.svc.SubmitSecret(context.Background(), reqID, secretValue); err != nil {
		t.Fatal(err)
	}
	if len(*fx.stored) != 1 || (*fx.stored)[0] != secretValue {
		t.Fatalf("store did not receive value: %v", *fx.stored)
	}
	msgs, _ = fx.store.AllMessages(context.Background(), threadID)
	for _, m := range msgs {
		if strings.Contains(string(m.Parts), secretValue) {
			t.Fatalf("secret in stored message: %s", m.Parts)
		}
	}
	for _, req := range model.requests {
		b, _ := json.Marshal(req)
		if strings.Contains(string(b), secretValue) {
			t.Fatalf("secret in model input: %s", b)
		}
	}
	req, ok, _ := fx.store.GetSecret(context.Background(), reqID)
	if !ok || req.Status != "stored" {
		t.Errorf("secret status = %v", req)
	}
}

// HTTP end-to-end: three tool calls and one approval through the API.
func TestHTTPEndToEnd(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{
		append(append(callEv("c1", "server_status", `{}`), callEv("c2", "catalog_search", `{"title":"Alien"}`)...), callEv("c3", "release_block", `{"title":"Bad.Release"}`)...),
		{{Kind: llm.EventTextDelta, Text: "Done. One change waits for approval."}},
	}}
	fx := newFixture(t, model, Options{})
	srv := httptest.NewServer(http.StripPrefix("/api/agent", fx.svc.Handler()))
	defer srv.Close()

	// status
	resp, err := http.Get(srv.URL + "/api/agent/status")
	if err != nil {
		t.Fatal(err)
	}
	var st Status
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if !st.Enabled || !st.ToolsSupported || st.Provider != "fake" {
		t.Errorf("status = %+v", st)
	}

	// turn (streaming)
	body := strings.NewReader(`{"prompt":"check, search, block","context":{"route":"/releases"}}`)
	resp, err = http.Post(srv.URL+"/api/agent/turn", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("x-vercel-ai-ui-message-stream"); got != "v1" {
		t.Errorf("stream header = %q", got)
	}
	threadID := resp.Header.Get("X-Ether-Thread-Id")
	if threadID == "" {
		t.Error("missing X-Ether-Thread-Id")
	}
	raw := readAll(t, resp)
	for _, want := range []string{`"type":"start"`, `"type":"start-step"`, `"type":"tool-input-start"`, `"type":"tool-input-available"`, `"type":"tool-output-available"`, `"type":"text-delta"`, `"type":"finish"`, "data: [DONE]"} {
		if !strings.Contains(raw, want) {
			t.Errorf("stream missing %s\n%s", want, raw)
		}
	}
	if strings.Count(raw, "tool-output-available") != 3 {
		t.Errorf("want 3 tool outputs in stream:\n%s", raw)
	}

	// threads + messages
	resp, _ = http.Get(srv.URL + "/api/agent/threads")
	var th struct {
		Threads []Thread `json:"threads"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&th)
	resp.Body.Close()
	if len(th.Threads) != 1 {
		t.Fatalf("threads = %v", th)
	}
	resp, _ = http.Get(srv.URL + "/api/agent/threads/" + threadID + "/messages?limit=10")
	var page struct {
		Messages []Message `json:"messages"`
		HasOlder bool      `json:"hasOlder"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if len(page.Messages) != 2 {
		t.Fatalf("messages = %+v", page)
	}
	var parts []map[string]any
	_ = json.Unmarshal(page.Messages[1].Parts, &parts)
	var approvalID string
	for _, p := range parts {
		if p["type"] == "approval-request" {
			approvalID, _ = p["approvalId"].(string)
		}
	}
	if approvalID == "" {
		t.Fatalf("no approval in messages: %s", page.Messages[1].Parts)
	}

	// approve through HTTP
	resp, err = http.Post(srv.URL+"/api/agent/approvals/"+approvalID, "application/json",
		strings.NewReader(`{"decision":"approved","note":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	var dec map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&dec)
	resp.Body.Close()
	if dec["status"] != "approved" || len(*fx.applied) != 1 {
		t.Errorf("approval response %v applied %v", dec, *fx.applied)
	}
	// The card promises the agent continues: a follow-up turn ran with the
	// decision visible in the tool result rather than a stale "pending".
	if dec["continued"] != true {
		t.Errorf("approval did not continue the turn: %v", dec)
	}
	model.mu.Lock()
	last := model.requests[len(model.requests)-1]
	model.mu.Unlock()
	sawApproved, sawPending, sawNudge := false, false, false
	for _, m := range last.Messages {
		for _, r := range m.ToolResults {
			if r.Name == "release_block" {
				sawApproved = strings.Contains(r.Content, `"status":"approved"`)
				sawPending = strings.Contains(r.Content, "pending_approval")
			}
		}
		if m.Role == "user" && strings.Contains(m.Content, "owner approved") {
			sawNudge = true
		}
	}
	if !sawApproved || sawPending || !sawNudge {
		t.Errorf("follow-up history approved=%v pending=%v nudge=%v: %+v", sawApproved, sawPending, sawNudge, last.Messages)
	}
	resp, _ = http.Get(srv.URL + "/api/agent/threads/" + threadID + "/messages?limit=10")
	_ = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if len(page.Messages) != 3 || page.Messages[2].Role != "assistant" {
		t.Errorf("follow-up turn not stored: %+v", page.Messages)
	}

	// cancel unknown turn is a clean no-op
	resp, _ = http.Post(srv.URL+"/api/agent/turns/msg_nope/cancel", "application/json", nil)
	if resp.StatusCode != 200 {
		t.Errorf("cancel status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// secret endpoint only returns status
	resp, _ = http.Post(srv.URL+"/api/agent/secrets/sec_nope", "application/json", strings.NewReader(`{"value":"x"}`))
	if resp.StatusCode != 404 {
		t.Errorf("unknown secret status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// delete
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/agent/threads/"+threadID, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("delete: %v %v", err, resp.StatusCode)
	}
	resp.Body.Close()
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func TestApprovalPreservesCollectionDraft(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{scripts: [][]llm.Event{callEv("draft", "collection_create_from_prompt", `{"prompt":"films"}`), {{Kind: llm.EventTextDelta, Text: "Approval pending."}}}}
	fx := newFixture(t, model, Options{})
	generated := 0
	var applied string
	err := fx.svc.catalogue.Register(tools.Tool{Name: "collection_create_from_prompt", Title: "Create a collection", Description: "Draft", Audience: tools.AudienceOwner, Owner: "builtin", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"draft":{"type":"string"}},"required":["prompt"]}`), Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
		draft, _ := in["draft"].(string)
		if draft == "" {
			generated++
			draft = fmt.Sprintf("draft-%d", generated)
		}
		return tools.Result{Proposal: &tools.Proposal{Category: "Create", Description: draft, Input: map[string]any{"prompt": in["prompt"], "draft": draft}, Apply: func(context.Context) (tools.Result, error) { applied = draft; return tools.Result{}, nil }}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	emit, _ := fx.emitCollect()
	thread, _, err := fx.svc.RunTurn(ctx, fx.p, "", "make a collection", TurnContext{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := fx.store.AllMessages(ctx, thread)
	if err != nil {
		t.Fatal(err)
	}
	var parts []map[string]any
	if err := json.Unmarshal(msgs[1].Parts, &parts); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, p := range parts {
		if p["type"] == "approval-request" {
			id, _ = p["approvalId"].(string)
			if p["description"] != "draft-1" {
				t.Fatalf("proposal: %v", p)
			}
		}
	}
	if id == "" {
		t.Fatal("approval missing")
	}
	approval, err := fx.svc.DecideApproval(ctx, fx.p, id, "approved", "")
	if err != nil || approval.Status != "approved" || applied != "draft-1" || generated != 1 {
		t.Fatalf("approval=%+v err=%v applied=%q generated=%d", approval, err, applied, generated)
	}
}

func TestApprovalCannotBeRejectedWhileBeingApplied(t *testing.T) {
	fx := newFixture(t, &fakeModel{}, Options{})
	entered := make(chan struct{})
	release := make(chan struct{})
	err := fx.svc.catalogue.Register(tools.Tool{Name: "slow_write", Title: "Slow", Description: "write", InputSchema: json.RawMessage(`{"type":"object"}`), Audience: tools.AudienceOwner, Owner: "builtin", Execute: func(context.Context, tools.Principal, map[string]any) (tools.Result, error) {
		return tools.Result{Proposal: &tools.Proposal{Apply: func(context.Context) (tools.Result, error) { close(entered); <-release; return tools.Result{}, nil }}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	appr, err := fx.store.CreateApproval(context.Background(), Approval{ToolName: "slow_write", Input: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := fx.svc.DecideApproval(context.Background(), fx.p, appr.ID, "approved", "")
		done <- err
	}()
	<-entered
	_, rejectErr := fx.svc.DecideApproval(context.Background(), fx.p, appr.ID, "rejected", "")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rejectErr == nil {
		t.Fatal("rejected an approval already being applied")
	}
}

func TestSecretRequestCannotBeSubmittedTwiceConcurrently(t *testing.T) {
	fx := newFixture(t, &fakeModel{}, Options{})
	req, err := fx.store.CreateSecret(context.Background(), SecretReq{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	fx.svc.secrets[req.ID] = func(context.Context, string) error {
		select {
		case <-entered:
			return nil
		default:
			close(entered)
			<-release
			return nil
		}
	}
	done := make(chan error, 1)
	go func() { done <- fx.svc.SubmitSecret(context.Background(), req.ID, "first") }()
	<-entered
	err = fx.svc.SubmitSecret(context.Background(), req.ID, "second")
	close(release)
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err == nil {
		t.Fatal("accepted concurrent secret submission")
	}
}

// Studio turns see only the tools that build and install drafts.
func TestStudioRouteNarrowsTools(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{{{Kind: llm.EventTextDelta, Text: "ok"}}}}
	fx := newFixture(t, model, Options{})
	emit, _ := fx.emitCollect()
	if _, _, err := fx.svc.RunTurn(context.Background(), fx.p, "", "build", TurnContext{Route: "/plugins/studio"}, emit); err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	for _, d := range model.requests[0].Tools {
		if !routeTools["/plugins/studio"][d.Name] {
			t.Errorf("tool %s offered in Studio", d.Name)
		}
	}
}

// Catalogue search shares collection curation but cannot expose mutation tools
// or retain a background conversation after its request has completed.
func TestCatalogSearchSharesCollectionScopeAndCleansTranscript(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{{{Kind: llm.EventTextDelta, Text: "Checking matching films."}}}}
	fx := newFixture(t, model, Options{})
	request := httptest.NewRequest(http.MethodPost, "/turn", strings.NewReader(`{"prompt":"rainy night films","context":{"route":"/catalog/search"}}`))
	response := httptest.NewRecorder()
	fx.svc.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status %d", response.Code)
	}
	id := response.Header().Get("X-Ether-Thread-Id")
	if id == "" {
		t.Fatal("missing search thread")
	}
	if _, ok, err := fx.store.Thread(context.Background(), id); err != nil || ok {
		t.Fatalf("temporary transcript retained: %v %v", ok, err)
	}
	for name := range routeTools[CatalogSearchRoute] {
		if !routeTools[DescribeCollectionRoute][name] {
			t.Fatalf("unexpected search tool %s", name)
		}
	}
	if !strings.HasPrefix(routeGuidance[CatalogSearchRoute], routeGuidance[DescribeCollectionRoute]) {
		t.Fatal("search no longer shares collection curation guidance")
	}
	if detachedRoutes[CatalogSearchRoute] {
		t.Fatal("search must stop on client disconnect")
	}
	for _, definition := range model.requests[0].Tools {
		if !routeTools[CatalogSearchRoute][definition.Name] {
			t.Fatalf("tool escaped route scope: %s", definition.Name)
		}
	}
}
