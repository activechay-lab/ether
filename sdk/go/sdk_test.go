package etherplugin

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestSettingsDecodeAndLint(t *testing.T) {
	minVal := 1.0
	maxVal := 100.0
	schema := &SettingsSchema{
		Fields: []SettingField{
			{
				Key:      "apiKey",
				Kind:     "secret",
				Label:    "Access token",
				Hint:     "Token used to query the upstream service",
				Required: true,
			},
			{
				Key:     "maxResults",
				Kind:    "number",
				Label:   "Maximum results",
				Default: 20,
				Min:     &minVal,
				Max:     &maxVal,
			},
			{
				Key:     "includeAdult",
				Kind:    "bool",
				Label:   "Include adult titles",
				Default: false,
			},
		},
	}

	if probs := LintSettingsSchema(schema); len(probs) != 0 {
		t.Fatalf("unexpected lint problems: %+v", probs)
	}
	if reason := LintCopy("Valid sentence case label", 40); reason != "" {
		t.Fatalf("expected valid copy, got %q", reason)
	}
	if LintCopy("Invalid — em dash", 40) == "" {
		t.Fatal("expected em dash failure")
	}
	if LintCopy("Invalid exclamation!", 40) == "" {
		t.Fatal("expected exclamation mark failure")
	}
	if LintCopy("Simply invalid word", 40) == "" {
		t.Fatal("expected banned word failure")
	}

	defaults, errs := DecodeSettings(schema, map[string]any{}, nil)
	if defaults["maxResults"] != float64(20) {
		t.Fatalf("expected default maxResults=20, got %v", defaults["maxResults"])
	}
	if errs["/apiKey"] != "is required" {
		t.Fatalf("expected /apiKey required error, got %v", errs)
	}

	valid, errs2 := DecodeSettings(schema, map[string]any{
		"apiKey":       "tok_1234",
		"maxResults":   50,
		"includeAdult": true,
	}, nil)
	if len(errs2) != 0 {
		t.Fatalf("unexpected errors: %v", errs2)
	}
	if valid["apiKey"] != "tok_1234" || valid["maxResults"] != float64(50) || valid["includeAdult"] != true {
		t.Fatalf("unexpected decoded settings: %+v", valid)
	}
}

func TestI18nHelper(t *testing.T) {
	i18n := NewI18n(StringsCatalog{
		"en":    {"greeting": "Hello {name}", "fallbackOnly": "Base text"},
		"fr-CA": {"greeting": "Bonjour {name}"},
	}, "en")

	if got := i18n.T("greeting", map[string]any{"name": "Ada"}, "fr-CA"); got != "Bonjour Ada" {
		t.Fatalf("expected Bonjour Ada, got %q", got)
	}
	if got := i18n.T("$t:fallbackOnly", nil, "fr-CA"); got != "Base text" {
		t.Fatalf("expected Base text, got %q", got)
	}
	if got := i18n.T("missing.key", nil, "fr-CA"); got != "missing.key" {
		t.Fatalf("expected missing.key, got %q", got)
	}
}

func TestRouterAllCapabilities(t *testing.T) {
	host := NewMockHost()
	plug := New(Definition{
		Manifest: Manifest{
			ID:          "org.ether.test.all",
			Name:        "Test all capabilities",
			Version:     "1.2.3",
			Description: "Test plugin covering all capabilities.",
			Types:       []string{"movie", "series"},
			IDPrefixes:  []string{"tt"},
			Ether: &EtherManifestBlock{
				Protocol: 1,
				Kind:     KindExtension,
				Capabilities: []Capability{
					CapListSource,
					CapStreamSource,
					CapSubtitles,
					CapMarkers,
					CapEvents,
					CapUI,
					CapTools,
					CapRankingStep,
					CapRuleCondition,
				},
				Summary: "Test plugin covering all capabilities.",
				Runtime: RuntimeDecl{Kind: "remote"},
				Settings: &SettingsSchema{
					Fields: []SettingField{{
						Key:      "prefix",
						Kind:     "text",
						Label:    "Title prefix",
						Default:  "Ether",
						Required: true,
					}},
				},
				Contributes: &Contributes{
					Pages: []PageMeta{{
						Key:   "home",
						Label: "Overview",
						Route: "/ether/view/home",
					}},
				},
			},
		},
		Strings: StringsCatalog{
			"en": {"title": "{prefix} overview"},
		},
		Host: host,
		Routes: Routes{
			Options: func(_ *Context, field string) ([]FieldOption, error) {
				if field == "prefix" {
					return []FieldOption{{Value: "Ether", Label: "Ether"}}, nil
				}
				return nil, nil
			},
			Catalog: func(ctx *Context, req CatalogRequest) (CatalogResponse, error) {
				prefix, _ := ctx.Settings["prefix"].(string)
				search := req.Search
				if search == "" {
					search = "Featured"
				}
				item := Fixtures.CatalogItem()
				item.ID = req.ID + ":1"
				item.Type = req.Type
				item.Name = prefix + " " + search
				return CatalogResponse{Metas: []CatalogItem{item}}, nil
			},
			Meta: func(_ *Context, req MetaRequest) (MetaResponse, error) {
				m := Fixtures.Movie()
				m.ID = req.ID
				m.Type = req.Type
				return MetaResponse{Meta: m}, nil
			},
			Stream: func(_ *Context, req StreamRequest) (StreamResponse, error) {
				return StreamResponse{
					Streams: []StreamItem{{
						Name: "Direct 1080p",
						URL:  "https://streams.example.invalid/" + req.ID + ".mp4",
					}},
				}, nil
			},
			Subtitles: func(_ *Context, req SubtitlesRequest) (SubtitlesResponse, error) {
				return SubtitlesResponse{
					Subtitles: []SubtitleItem{{
						ID:   req.ID + ":en",
						URL:  "https://subs.example.invalid/" + req.ID + ".vtt?pick=" + req.PickToken,
						Lang: "en",
					}},
				}, nil
			},
			SubtitleDownload: func(_ *Context, req SubtitleDownloadRequest) (SubtitleDownloadResponse, error) {
				return SubtitleDownloadResponse{Format: req.Format, Encoding: "utf8", Data: "picked"}, nil
			},
			Markers: func(_ *Context, _ MarkersRequest) (MarkersResponse, error) {
				return MarkersResponse{
					Markers: []MarkerItem{{Kind: "intro", Start: 10, End: 65, Label: "Skip intro"}},
				}, nil
			},
			Event: func(ctx *Context, events []EtherEvent) (EventResponse, error) {
				var ack []string
				for _, ev := range events {
					_ = ctx.Ether.KVPut(ctx, "evt:"+ev.ID, ev.Name)
					ack = append(ack, ev.ID)
				}
				return EventResponse{Ack: ack}, nil
			},
			View: func(ctx *Context, _ ViewRequest) (ViewResponse, error) {
				return ViewResponse{
					Spec: ViewSpecDocument{
						SpecVersion: 1,
						Layout:      "list",
						Header: []map[string]any{{
							"kind":  "page-header",
							"title": map[string]any{"$": "title"},
						}},
						Body: []map[string]any{{
							"kind":    "text",
							"content": "Ready.",
						}},
					},
					Data: map[string]any{
						"title": ctx.T("title", map[string]any{"prefix": ctx.Settings["prefix"]}),
					},
				}, nil
			},
			Action: func(ctx *Context, req ActionRequest) (ActionResponse, error) {
				target, _ := req.Input["target"].(string)
				_ = ctx.Ether.NotifyOwner(ctx, "Action "+req.Action+": "+target)
				return ActionResponse{Toast: "Done.", Refresh: true}, nil
			},
			Tool: func(_ *Context, req ToolRequest) (ToolResponse, error) {
				return ToolResponse{"executed": req.Name, "echo": req.Input}, nil
			},
			RankKey: func(candidate CandidateFacts, _ map[string]any) int64 {
				if candidate.Cached {
					return 100
				}
				return 10
			},
			CondMatch: func(candidate CandidateFacts, _ string, value any) bool {
				want, _ := value.(bool)
				return candidate.Verified == want
			},
			File: func(_ *Context, req FileRequest) (FileRangeResponse, error) {
				content := []byte("file:" + req.Path)
				total := int64(len(content))
				if req.Range == "bytes=0-3" {
					return FileRangeResponse{
						Status:       http.StatusPartialContent,
						ContentType:  "text/plain",
						ContentRange: "bytes 0-3/19",
						TotalSize:    &total,
						Body:         content[:4],
					}, nil
				}
				return FileRangeResponse{
					Status:      http.StatusOK,
					ContentType: "text/plain",
					TotalSize:   &total,
					Body:        content,
				}, nil
			},
		},
	})

	h := NewTestHarness(plug, host)

	man, err := h.Manifest()
	if err != nil || man.ID != "org.ether.test.all" {
		t.Fatalf("unexpected manifest: %+v (%v)", man, err)
	}

	status, health, err := h.Health()
	if err != nil || status != http.StatusOK || health.Status != "ok" {
		t.Fatalf("unexpected health: %d %+v %v", status, health, err)
	}

	status, cfgRes, err := h.Configure(map[string]any{"prefix": "Custom"})
	if err != nil || status != http.StatusOK || len(cfgRes.Errors) != 0 {
		t.Fatalf("unexpected configure: %d %+v %v", status, cfgRes, err)
	}

	status, opts, err := h.Options("prefix")
	if err != nil || status != http.StatusOK || len(opts) != 1 {
		t.Fatalf("unexpected options: %d %+v %v", status, opts, err)
	}

	status, catRes, err := h.Catalog("movie", "top", map[string]string{"search": "Matrix"})
	if err != nil || status != http.StatusOK || len(catRes.Metas) != 1 || catRes.Metas[0].Name != "Custom Matrix" {
		t.Fatalf("unexpected catalog: %d %+v %v", status, catRes, err)
	}

	status, metaRes, err := h.Meta("movie", "tt0133093")
	if err != nil || status != http.StatusOK || metaRes.Meta.ID != "tt0133093" {
		t.Fatalf("unexpected meta: %d %+v %v", status, metaRes, err)
	}

	status, streamRes, err := h.Stream("movie", "tt0133093", nil)
	if err != nil || status != http.StatusOK || len(streamRes.Streams) != 1 {
		t.Fatalf("unexpected stream: %d %+v %v", status, streamRes, err)
	}

	// Listing needs no token; download does.
	subStatus, _, _ := h.Subtitles("movie", "tt0133093", "", nil)
	if subStatus != http.StatusOK {
		t.Fatalf("listing status %d", subStatus)
	}
	rawSub := h.Request(RawRequest{Method: "POST", Path: "/ether/subtitles/download", Body: json.RawMessage(`{"id":"sub","lang":"en","format":"srt"}`)})
	if rawSub.Status != http.StatusForbidden {
		t.Fatalf("download status %d", rawSub.Status)
	}
	picked := h.Request(RawRequest{Method: "POST", Path: "/ether/subtitles/download", Headers: map[string]string{"X-Ether-Pick": "pick"}, Body: json.RawMessage(`{"id":"sub","lang":"en","format":"srt"}`)})
	if picked.Status != http.StatusOK || !strings.Contains(string(picked.Body), "picked") {
		t.Fatalf("picked download: %+v", picked)
	}

	// Subtitles with X-Ether-Pick succeeds
	pick := Fixtures.PickToken("tt0133093")
	subStatus, subRes, _ := h.Subtitles("movie", "tt0133093", pick, nil)
	if subStatus != http.StatusOK || len(subRes.Subtitles) != 1 {
		t.Fatalf("expected 200 subtitles, got %d %+v", subStatus, subRes)
	}

	status, markersRes, err := h.Markers("movie", "tt0133093")
	if err != nil || status != http.StatusOK || len(markersRes.Markers) != 1 {
		t.Fatalf("unexpected markers: %d %+v %v", status, markersRes, err)
	}

	ev := Fixtures.PlaybackEvent("playback.completed")
	status, evRes, err := h.Event([]EtherEvent{ev})
	if err != nil || status != http.StatusOK || len(evRes.Ack) != 1 || host.KV["evt:"+ev.ID] != "playback.completed" {
		t.Fatalf("unexpected event: %d %+v %v", status, evRes, err)
	}

	status, viewRes, err := h.View("home", nil)
	if err != nil || status != http.StatusOK || viewRes.Data["title"] != "Custom overview" {
		t.Fatalf("unexpected view: %d %+v %v", status, viewRes, err)
	}

	status, actRes, err := h.Action("sync", map[string]any{"target": "library"})
	if err != nil || status != http.StatusOK || actRes.Toast != "Done." || len(host.Notifications) != 1 {
		t.Fatalf("unexpected action: %d %+v %v", status, actRes, err)
	}

	status, toolRes, err := h.Tool("lookup", map[string]any{"q": "Arrival"})
	if err != nil || status != http.StatusOK || toolRes["executed"] != "lookup" {
		t.Fatalf("unexpected tool: %d %+v %v", status, toolRes, err)
	}

	cand := Fixtures.Candidate()
	if score := plug.RankKey(cand, nil); score != 100 {
		t.Fatalf("expected score 100, got %d", score)
	}
	if !plug.CondMatch(cand, "eq", true) || plug.CondMatch(cand, "eq", false) {
		t.Fatal("unexpected CondMatch result")
	}

	fileFull := h.File("clips/intro.txt", "")
	if fileFull.Status != http.StatusOK || string(fileFull.BinaryBody) != "file:clips/intro.txt" {
		t.Fatalf("unexpected file full: %d %q", fileFull.Status, string(fileFull.BinaryBody))
	}

	fileRange := h.File("clips/intro.txt", "bytes=0-3")
	if fileRange.Status != http.StatusPartialContent || string(fileRange.BinaryBody) != "file" {
		t.Fatalf("unexpected file range: %d %q", fileRange.Status, string(fileRange.BinaryBody))
	}

	badFile := h.Request(RawRequest{Method: http.MethodGet, Path: "/ether/file/../secrets.txt"})
	if badFile.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 on path traversal, got %d", badFile.Status)
	}
}

func TestAssertValidViewSpecRejectsClassName(t *testing.T) {
	err := AssertValidViewSpec(ViewSpecDocument{
		SpecVersion: 1,
		Layout:      "list",
		Header:      []map[string]any{},
		Body: []map[string]any{{
			"kind":               "panel",
			"title":              "Bad",
			"containerClassName": "bg-red-500",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "forbids containerClassName") {
		t.Fatalf("expected containerClassName rejection, got %v", err)
	}
}

func TestStartHTTPAndTunnelSidecar(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	plug := NewCatalogTemplate(TemplateOptions{
		ID:   "org.ether.go.sidecar",
		Name: "Go sidecar",
	})
	plug.routes.File = func(_ *Context, _ FileRequest) (FileRangeResponse, error) {
		return FileRangeResponse{
			Status:       http.StatusPartialContent,
			ContentType:  "video/mp4",
			ContentRange: "bytes 0-7/16",
			Body:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
		}, nil
	}

	handle, err := plug.Start(ctx, ServeOptions{Mode: "http", Port: 0})
	if err != nil {
		t.Fatalf("Start http failed: %v", err)
	}
	defer handle.Close() //nolint:errcheck

	resp, err := http.Get(handle.URL + "/manifest.json")
	if err != nil {
		t.Fatalf("GET /manifest.json failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "org.ether.go.sidecar") {
		t.Fatalf("unexpected manifest body: %s", string(raw))
	}

	// Test WebSocket tunnel handshake + request dispatch
	tunnelReplyCh := make(chan TunnelFrame, 1)
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ether-Plugin-Credential") != "cred-tunnel" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{TunnelProtocol},
		})
		if err != nil {
			return
		}
		defer c.CloseNow() //nolint:errcheck

		_, helloRaw, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var hello TunnelFrame
		_ = json.Unmarshal(helloRaw, &hello)
		if hello.Type != "hello" {
			return
		}
		welcome, _ := json.Marshal(TunnelFrame{
			Type:          "welcome",
			Protocol:      TunnelProtocol,
			PluginID:      hello.Plugin,
			MaxChunkBytes: 4096,
		})
		_ = c.Write(r.Context(), websocket.MessageText, welcome)

		reqFrame, _ := json.Marshal(TunnelFrame{
			Type:   "request",
			ID:     "req-tunnel-1",
			Method: "GET",
			Path:   "/ether/health",
		})
		_ = c.Write(r.Context(), websocket.MessageText, reqFrame)

		_, replyRaw, err := c.Read(r.Context())
		if err == nil {
			var reply TunnelFrame
			_ = json.Unmarshal(replyRaw, &reply)
			tunnelReplyCh <- reply
		}
	}))
	defer wsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")
	tHandle, err := plug.Start(ctx, ServeOptions{
		Mode:       "tunnel",
		TunnelURL:  wsURL,
		Credential: "cred-tunnel",
	})
	if err != nil {
		t.Fatalf("Start tunnel failed: %v", err)
	}
	defer tHandle.Close() //nolint:errcheck

	reply := <-tunnelReplyCh
	if reply.Type != "response" || reply.ID != "req-tunnel-1" || reply.Status != http.StatusOK {
		t.Fatalf("unexpected tunnel reply: %+v", reply)
	}

	fileFrames := plug.DispatchTunnelFrame(ctx, TunnelFrame{
		Type:    "request",
		ID:      "req-file",
		Method:  "GET",
		Path:    "/ether/file/sample.mp4",
		Headers: map[string][]string{"Range": {"bytes=0-7"}},
	}, 1024)
	if len(fileFrames) != 3 || fileFrames[0].Type != "response_start" || fileFrames[1].Type != "response_chunk" || fileFrames[2].Type != "response_end" {
		t.Fatalf("unexpected streamed file frames: %+v", fileFrames)
	}
}

func TestRemoteHostClient(t *testing.T) {
	var seenCred string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCred = r.Header.Get("X-Ether-Plugin-Credential")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/ether/plugin-host/kv/greeting":
			_, _ = w.Write([]byte(`{"key":"greeting","value":"hello"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/ether/plugin-host/kv":
			_, _ = w.Write([]byte(`{"keys":["greeting"]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	client := NewRemoteHostClient(RemoteHostOptions{
		BaseURL:    srv.URL,
		Credential: "cred-go-123",
	})
	ctx := context.Background()
	val, found, err := client.KVGet(ctx, "greeting")
	if err != nil || !found || val != "hello" {
		t.Fatalf("unexpected KVGet: %q %v %v", val, found, err)
	}
	if err := client.KVPut(ctx, "greeting", "updated"); err != nil {
		t.Fatalf("KVPut failed: %v", err)
	}
	keys, err := client.KVList(ctx, "")
	if err != nil || len(keys) != 1 || keys[0] != "greeting" {
		t.Fatalf("unexpected KVList: %+v %v", keys, err)
	}
	if err := client.NotifyOwner(ctx, "Sync complete."); err != nil {
		t.Fatalf("NotifyOwner failed: %v", err)
	}
	if seenCred != "cred-go-123" {
		t.Fatalf("expected credential header cred-go-123, got %q", seenCred)
	}
}

func TestStarterTemplates(t *testing.T) {
	catH := NewTestHarness(NewCatalogTemplate(TemplateOptions{ID: "org.ether.tpl.cat", Name: "Catalog template"}), nil)
	if status, _, err := catH.Catalog("movie", "featured", nil); err != nil || status != http.StatusOK {
		t.Fatalf("catalog template failed: %d %v", status, err)
	}

	subH := NewTestHarness(NewSubtitlesTemplate(TemplateOptions{ID: "org.ether.tpl.sub", Name: "Subtitles template"}), nil)
	if status, _, _ := subH.Subtitles("movie", "tt0133093", Fixtures.PickToken("tt0133093"), nil); status != http.StatusOK {
		t.Fatalf("subtitles template failed: %d", status)
	}

	scrobbleH := NewTestHarness(NewScrobblerTemplate(TemplateOptions{ID: "org.ether.tpl.scrobble", Name: "Scrobbler template"}), nil)
	if status, _, err := scrobbleH.Event([]EtherEvent{Fixtures.PlaybackEvent("")}); err != nil || status != http.StatusOK {
		t.Fatalf("scrobbler template failed: %d %v", status, err)
	}

	viewH := NewTestHarness(NewViewTemplate(TemplateOptions{ID: "org.ether.tpl.view", Name: "View template"}), nil)
	if status, _, err := viewH.View("home", nil); err != nil || status != http.StatusOK {
		t.Fatalf("view template failed: %d %v", status, err)
	}
	if status, _, err := viewH.Action("refresh", nil); err != nil || status != http.StatusOK {
		t.Fatalf("view action template failed: %d %v", status, err)
	}

	rankP := NewRankerTemplate(TemplateOptions{ID: "org.ether.tpl.rank", Name: "Ranker template"})
	if score := rankP.RankKey(Fixtures.Candidate(), nil); score != 1500 {
		t.Fatalf("expected rank score 1500, got %d", score)
	}

	condP := NewConditionTemplate(TemplateOptions{ID: "org.ether.tpl.cond", Name: "Condition template"})
	if !condP.CondMatch(Fixtures.Candidate(), "eq", true) {
		t.Fatal("expected condition match true")
	}
}

func TestServerPackagesDoNotImportSDKGo(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, dir := range []string{"internal", "cmd"} {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// Offline server fixtures may use the public SDK harness; shipped code may not.
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if impPath == "github.com/braedonsaunders/ether/sdk/go" || strings.HasPrefix(impPath, "github.com/braedonsaunders/ether/sdk/") {
					t.Errorf("server file %s must not import %s", path, impPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
}
