package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

func fixtureServer(t *testing.T, fixture string, wantSubstr ...string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		for _, s := range wantSubstr {
			if !strings.Contains(string(raw), s) {
				t.Errorf("request to %s missing %q: %s", r.URL.Path, s, raw)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	for ev := range ch {
		evs = append(evs, ev)
		if ev.Kind == EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	return evs
}

func assertToolStream(t *testing.T, evs []Event) {
	t.Helper()
	var text string
	var call *Event
	usage := false
	for i := range evs {
		switch evs[i].Kind {
		case EventTextDelta:
			text += evs[i].Text
		case EventToolCall:
			call = &evs[i]
		case EventUsage:
			if evs[i].InputTokens > 0 {
				usage = true
			}
		}
	}
	if text != "Checking. " {
		t.Errorf("text = %q", text)
	}
	if call == nil || call.ToolName != "server_status" {
		t.Fatalf("no server_status call in %+v", evs)
	}
	var in map[string]any
	if json.Unmarshal(call.Input, &in) != nil || in["verbose"] != true {
		t.Errorf("call input = %s", call.Input)
	}
	if !usage {
		t.Errorf("no usage event")
	}
	if len(evs) == 0 || evs[len(evs)-1].Kind != EventDone {
		t.Errorf("last event not done: %+v", evs)
	}
}

func toolReq() ToolRequest {
	return ToolRequest{
		System:      "You are Ask Ether.",
		Temperature: 0.3,
		Messages:    []StreamMessage{{Role: "user", Content: "status?"}},
		Tools: []ToolDef{{Name: "server_status", Description: "Server status",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"verbose":{"type":"boolean"}}}`)}},
	}
}

func TestStreamToolsOpenAI(t *testing.T) {
	srv := fixtureServer(t, "openai_tools.sse", `"tools"`, `"server_status"`, `"stream":true`)
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "custom", BaseURL: srv.URL, Model: "gpt-x"}, HTTPClient: srv.Client()})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	assertToolStream(t, collect(t, ch))
}

func TestStreamToolsAnthropic(t *testing.T) {
	srv := fixtureServer(t, "anthropic_tools.sse", `"input_schema"`, `"server_status"`)
	defer srv.Close()
	cfg := config.LLM{Enabled: true, Provider: "anthropic", Model: "claude-x", APIKey: "k"}
	// Point the provider base at the test server via custom transport rewrite.
	c := NewClient(ClientOptions{Config: cfg, HTTPClient: rewriteClient(srv)})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	assertToolStream(t, collect(t, ch))
}

func TestStreamToolsGemini(t *testing.T) {
	srv := fixtureServer(t, "gemini_tools.sse", `"functionDeclarations"`, `"server_status"`)
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "google", Model: "gemini-x", APIKey: "k"}, HTTPClient: rewriteClient(srv)})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	assertToolStream(t, collect(t, ch))
}

// rewriteClient rewrites every request to the test server, keeping path.
func rewriteClient(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamToolsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"tools are not supported by this model"}}`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "custom", BaseURL: srv.URL, Model: "gpt-x"}, HTTPClient: srv.Client()})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for ev := range ch {
		if ev.Kind == EventError {
			gotErr = ev.Err
		}
	}
	if !errors.Is(gotErr, ErrToolsUnsupported) {
		t.Fatalf("want ErrToolsUnsupported, got %v", gotErr)
	}
}

func TestStreamOpenAIRetriesWithoutStreamOptions(t *testing.T) {
	fixture, err := os.ReadFile("testdata/openai_tools.sse")
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "stream_options") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "custom", BaseURL: srv.URL, Model: "gpt-x"}, HTTPClient: srv.Client()})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	assertToolStream(t, collect(t, ch))
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestToolsSupported(t *testing.T) {
	c := NewClient(ClientOptions{Config: config.LLM{}})
	if ok, _ := c.ToolsSupported(); ok {
		t.Error("disabled llm reported tools supported")
	}
	c = NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "custom", BaseURL: "http://x/v1", Model: "text-embedding-3"}})
	if ok, reason := c.ToolsSupported(); ok || reason == "" {
		t.Error("embedding model reported tools supported")
	}
}

// TestStreamToolsOpenAIResponses checks the Responses-API stream shape:
// a text delta, one function call whose arguments arrive in two deltas, and
// usage, mapping to the same Event sequence the chat/completions path emits.
func TestStreamToolsOpenAIResponses(t *testing.T) {
	body, err := os.ReadFile("testdata/openai_responses.sse")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "openai", Model: "gpt-6-luna", APIKey: "k"}, HTTPClient: rewriteClient(srv)})
	ch, err := c.StreamTools(context.Background(), toolReq())
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if gotPath != "/v1/responses" {
		t.Errorf("path = %q, want /v1/responses", gotPath)
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatal(err)
	}
	if req["stream"] != true || req["store"] != false {
		t.Errorf("want stream:true store:false, got %v", req)
	}
	if _, ok := req["temperature"]; ok {
		t.Errorf("Responses request must not send temperature: %v", req)
	}
	if req["instructions"] != "You are Ask Ether." {
		t.Errorf("instructions = %v", req["instructions"])
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", req["tools"])
	}
	tr := tools[0].(map[string]any)
	if tr["type"] != "function" || tr["name"] != "server_status" {
		t.Errorf("tool = %v", tr)
	}
	if _, ok := tr["function"]; ok {
		t.Errorf("Responses tools must not nest under \"function\": %v", tr)
	}
	assertToolStream(t, evs)
	// The last event carries the finish reason the loop and clients rely on.
	last := evs[len(evs)-1]
	if last.Kind != EventDone || last.FinishReason != "tool-calls" {
		t.Errorf("done finish reason = %q (%v)", last.FinishReason, last.Kind)
	}
}

// TestStreamToolsOpenAIResponsesInputRoundTrip checks the request body for a
// prior tool round-trip: assistant function_call items carry call_id/name/
// arguments and tool results become function_call_output items.
func TestStreamToolsOpenAIResponsesInputRoundTrip(t *testing.T) {
	body, err := os.ReadFile("testdata/openai_responses.sse")
	if err != nil {
		t.Fatal(err)
	}
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{Config: config.LLM{Enabled: true, Provider: "openai", Model: "gpt-6-luna", APIKey: "k"}, HTTPClient: rewriteClient(srv)})
	req := ToolRequest{
		System: "sys",
		Messages: []StreamMessage{
			{Role: "user", Content: "status?"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "server_status", Input: json.RawMessage(`{"verbose":true}`)}}},
			{Role: "tool", ToolResults: []ToolResult{{ToolCallID: "call_1", Name: "server_status", Content: `{"ok":true}`}}},
		},
		Tools:     []ToolDef{{Name: "server_status", Description: "Server status", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 4096,
	}
	ch, err := c.StreamTools(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	var got struct {
		MaxOutputTokens int `json:"max_output_tokens"`
		Input           []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Content   string `json:"content"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatal(err)
	}
	if got.MaxOutputTokens != 4096 {
		t.Errorf("max_output_tokens = %d", got.MaxOutputTokens)
	}
	want := []struct{ typ, role, callID, name, args, output string }{
		{typ: "message", role: "user"},
		{typ: "function_call", callID: "call_1", name: "server_status", args: `{"verbose":true}`},
		{typ: "function_call_output", callID: "call_1", output: `{"ok":true}`},
	}
	if len(got.Input) != len(want) {
		t.Fatalf("input items = %d, want %d: %s", len(got.Input), len(want), gotBody)
	}
	for i, w := range want {
		g := got.Input[i]
		if g.Type != w.typ || (w.role != "" && g.Role != w.role) || (w.callID != "" && g.CallID != w.callID) ||
			(w.name != "" && g.Name != w.name) || (w.args != "" && g.Arguments != w.args) || (w.output != "" && g.Output != w.output) {
			t.Errorf("input[%d] = %+v, want %+v", i, g, w)
		}
	}
}

// TestToolsRefusalMatching keeps generic 400s that merely name a tool from
// being mislabelled as a tools-unsupported refusal.
func TestToolsRefusalMatching(t *testing.T) {
	unsupported := []string{
		"tools are not supported by this model",
		"This model does not support function calling",
		"function calling is not available for this model",
	}
	for _, msg := range unsupported {
		if !isToolRefusal(msg) {
			t.Errorf("want refusal for %q", msg)
		}
	}
	other := []string{
		"Invalid value for 'function.arguments': expected valid JSON",
		"Unknown tool: server_staus",
		"max_tokens unsupported, use max_completion_tokens",
		"temperature is not supported by this model",
	}
	for _, msg := range other {
		if isToolRefusal(msg) {
			t.Errorf("want non-refusal for %q", msg)
		}
	}
}
