package etherplugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// RecordedLog is one log message recorded by MockHost.
type RecordedLog struct {
	Level   string
	Message string
	Attrs   map[string]any
}

// MockHost is an in-memory Host implementation for unit tests.
type MockHost struct {
	mu            sync.RWMutex
	KV            map[string]string
	Secrets       map[string]string
	Catalog       map[string]CatalogTitleSummary
	History       map[int64][]ViewerWatchEntry
	WatchState    map[string]bool
	Collections   []map[string]any
	Kinds         map[string][]KindItem // kindKey -> pushed items, in upsert order
	Sources       []SourceCandidate     // returned by ResolveSources
	Blobs         map[string][]byte     // cache blob ref -> bytes
	Folders       map[string][]FolderEntry
	Notifications []string
	Logs          []RecordedLog
	FetchHandler  func(ctx context.Context, req HostFetchRequest) (HostFetchResponse, error)
}

// NewMockHost creates an initialized MockHost.
func NewMockHost() *MockHost {
	return &MockHost{
		KV:          map[string]string{},
		Secrets:     map[string]string{},
		Catalog:     map[string]CatalogTitleSummary{},
		History:     map[int64][]ViewerWatchEntry{},
		WatchState:  map[string]bool{},
		Folders:     map[string][]FolderEntry{},
		Collections: []map[string]any{},
		Kinds:       map[string][]KindItem{},
		Blobs:       map[string][]byte{},
	}
}

// KVGet reads a key from the in-memory KV store.
func (m *MockHost) KVGet(_ context.Context, key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.KV[key]
	return v, ok, nil
}

// KVPut writes a key-value pair to the in-memory KV store.
func (m *MockHost) KVPut(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.KV[key] = value
	return nil
}

// KVDelete deletes a key from the in-memory KV store.
func (m *MockHost) KVDelete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.KV, key)
	return nil
}

// KVList lists keys matching prefix in sorted order.
func (m *MockHost) KVList(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var keys []string
	for k := range m.KV {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// SecretGet reads a secret from the in-memory secrets map.
func (m *MockHost) SecretGet(_ context.Context, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Secrets[name], nil
}

// Fetch invokes FetchHandler if set, or returns a 200 "{}" response.
func (m *MockHost) Fetch(ctx context.Context, req HostFetchRequest) (HostFetchResponse, error) {
	if m.FetchHandler != nil {
		return m.FetchHandler(ctx, req)
	}
	return HostFetchResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    "{}",
	}, nil
}

// CatalogSearch searches the in-memory catalog by case-insensitive substring.
func (m *MockHost) CatalogSearch(_ context.Context, query string, limit, offset int) ([]CatalogTitleSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	needle := strings.ToLower(query)
	var matches []CatalogTitleSummary
	for _, item := range m.Catalog {
		if strings.Contains(strings.ToLower(item.Title), needle) {
			matches = append(matches, item)
		}
	}
	if offset >= len(matches) {
		return nil, nil
	}
	end := len(matches)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return matches[offset:end], nil
}

// CatalogGet returns a title by ID from the in-memory catalog.
func (m *MockHost) CatalogGet(_ context.Context, id string) (*CatalogTitleSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if item, ok := m.Catalog[id]; ok {
		cp := item
		return &cp, nil
	}
	return nil, nil
}

// ViewerHistory returns watch history for viewerID.
func (m *MockHost) ViewerHistory(_ context.Context, viewerID int64, limit, offset int) ([]ViewerWatchEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.History[viewerID]
	if offset >= len(list) {
		return nil, nil
	}
	end := len(list)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return list[offset:end], nil
}

// SetWatchState records watch state for viewerID:titleID.
func (m *MockHost) SetWatchState(_ context.Context, viewerID int64, titleID string, watched bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WatchState[fmt.Sprintf("%d:%s", viewerID, titleID)] = watched
	return nil
}

// UpsertCollection appends the collection to Collections.
func (m *MockHost) UpsertCollection(_ context.Context, collection map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Collections = append(m.Collections, collection)
	return nil
}

// FoldersList returns folder entries for root/path.
func (m *MockHost) FoldersList(_ context.Context, root, path string) ([]FolderEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := root
	if path != "" {
		key = root + "/" + path
	}
	return m.Folders[key], nil
}

// FoldersStat returns a FolderEntry for root and path.
func (m *MockHost) FoldersStat(_ context.Context, root, path string) (FolderEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, f := range m.Folders[root] {
		if f.Path == path || f.Name == path {
			return f, nil
		}
	}
	return FolderEntry{Name: path, Path: path}, nil
}

// NotifyOwner appends text to Notifications.
func (m *MockHost) NotifyOwner(_ context.Context, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Notifications = append(m.Notifications, text)
	return nil
}

// Log appends a RecordedLog entry to Logs.
func (m *MockHost) Log(_ context.Context, level, message string, attrs map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Logs = append(m.Logs, RecordedLog{Level: level, Message: message, Attrs: attrs})
	return nil
}

// KindUpsert records pushed items under their kind, replacing same-id items.
func (m *MockHost) KindUpsert(_ context.Context, kindKey string, items []KindItem) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Kinds == nil {
		m.Kinds = map[string][]KindItem{}
	}
	for _, it := range items {
		replaced := false
		for i, old := range m.Kinds[kindKey] {
			if old.ID == it.ID {
				m.Kinds[kindKey][i] = it
				replaced = true
			}
		}
		if !replaced {
			m.Kinds[kindKey] = append(m.Kinds[kindKey], it)
		}
	}
	return len(items), nil
}

// KindDelete drops recorded items by external id.
func (m *MockHost) KindDelete(_ context.Context, kindKey string, externalIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := map[string]bool{}
	for _, id := range externalIDs {
		drop[id] = true
	}
	kept := m.Kinds[kindKey][:0]
	for _, it := range m.Kinds[kindKey] {
		if !drop[it.ID] {
			kept = append(kept, it)
		}
	}
	m.Kinds[kindKey] = kept
	return nil
}

// KindItems pages recorded items; the cursor is the last item id seen.
func (m *MockHost) KindItems(_ context.Context, kindKey, cursor string, limit int) (KindPage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	var out []KindItem
	after := cursor == ""
	for _, it := range m.Kinds[kindKey] {
		if !after {
			after = it.ID == cursor
			continue
		}
		out = append(out, it)
		if len(out) == limit {
			return KindPage{Items: out, NextCursor: it.ID}, nil
		}
	}
	if out == nil {
		out = []KindItem{}
	}
	return KindPage{Items: out}, nil
}

// ResolveSources returns the canned Sources.
func (m *MockHost) ResolveSources(_ context.Context, _ SourceQuery) ([]SourceCandidate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]SourceCandidate{}, m.Sources...), nil
}

// CachePut stores bytes under a sha256 blob ref, like the real cache.
func (m *MockHost) CachePut(_ context.Context, _, _ string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Blobs == nil {
		m.Blobs = map[string][]byte{}
	}
	ref := fmt.Sprintf("blob:%x", sha256.Sum256(data))
	m.Blobs[ref] = append([]byte{}, data...)
	return ref, nil
}

// CacheOpen reads stored blob bytes back.
func (m *MockHost) CacheOpen(_ context.Context, blobRef string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.Blobs[blobRef]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", blobRef)
	}
	return append([]byte{}, b...), nil
}

// AssertValidViewSpec validates a ViewSpecDocument for structural rules and
// rejects any `*ClassName` fields.
func AssertValidViewSpec(doc ViewSpecDocument) error {
	if doc.SpecVersion != 1 {
		return fmt.Errorf("ViewSpecDocument specVersion must be 1 (got %d)", doc.SpecVersion)
	}
	switch doc.Layout {
	case "list", "detail", "bare":
	default:
		return fmt.Errorf("ViewSpecDocument layout must be list, detail, or bare (got %q)", doc.Layout)
	}
	if doc.Header == nil {
		return fmt.Errorf("ViewSpecDocument header must not be nil")
	}
	if doc.Body == nil {
		return fmt.Errorf("ViewSpecDocument body must not be nil")
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	return checkNoClassName(raw, "spec")
}

func checkNoClassName(v any, path string) error {
	switch t := v.(type) {
	case []any:
		for i, item := range t {
			if err := checkNoClassName(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, val := range t {
			if k == "className" || strings.HasSuffix(k, "ClassName") {
				return fmt.Errorf("ViewSpec forbids %s at %s.%s", k, path, k)
			}
			if err := checkNoClassName(val, path+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

// Fixtures provides sample EPP/1 records for plugin unit tests.
var Fixtures = struct {
	PickToken     func(itemID string) string
	Movie         func() MetaItem
	CatalogItem   func() CatalogItem
	PlaybackEvent func(name string) EtherEvent
	Candidate     func() CandidateFacts
}{
	PickToken: func(itemID string) string {
		if itemID == "" {
			itemID = "ether:movie:1"
		}
		return "pick-v1:" + itemID + ":sig-test"
	},
	Movie: func() MetaItem {
		return MetaItem{
			CatalogItem: CatalogItem{
				ID:          "tt0133093",
				Type:        "movie",
				Name:        "The Matrix",
				ReleaseInfo: "1999",
				Description: "A computer hacker learns about the true nature of reality.",
				Genres:      []string{"Action", "Sci-Fi"},
				IMDBRating:  "8.7",
			},
		}
	},
	CatalogItem: func() CatalogItem {
		return CatalogItem{
			ID:          "tt0133093",
			Type:        "movie",
			Name:        "The Matrix",
			ReleaseInfo: "1999",
		}
	},
	PlaybackEvent: func(name string) EtherEvent {
		if name == "" {
			name = "playback.completed"
		}
		return EtherEvent{
			ID:       "evt_01",
			Name:     name,
			At:       "2026-11-02T12:00:00Z",
			ViewerID: 1,
			Payload: map[string]any{
				"titleId":  "tt0133093",
				"title":    "The Matrix",
				"progress": 0.95,
			},
		}
	},
	Candidate: func() CandidateFacts {
		return CandidateFacts{
			ReleaseID:  "rel_01",
			Name:       "The.Matrix.1999.2160p.UHD.BluRay.x265",
			Resolution: 2160,
			HDR:        []string{"DV", "HDR10"},
			VideoCodec: "hevc",
			Cached:     true,
			Verified:   true,
		}
	},
}

// TestHarness wraps a Plugin and MockHost for ergonomic unit tests.
type TestHarness struct {
	Plugin *Plugin
	Host   *MockHost
}

// NewTestHarness constructs a TestHarness for p.
func NewTestHarness(p *Plugin, host *MockHost) *TestHarness {
	if host == nil {
		if mh, ok := p.host.(*MockHost); ok {
			host = mh
		} else {
			host = NewMockHost()
		}
	}
	p.host = host
	return &TestHarness{Plugin: p, Host: host}
}

func encodeExtraSegment(extra map[string]string) string {
	if len(extra) == 0 {
		return ""
	}
	vals := url.Values{}
	for k, v := range extra {
		vals.Set(k, v)
	}
	return "/" + vals.Encode()
}

// Request dispatches a RawRequest against the plugin.
func (h *TestHarness) Request(req RawRequest) RawResponse {
	return h.Plugin.Dispatch(context.Background(), req)
}

// Manifest fetches and decodes GET /manifest.json.
func (h *TestHarness) Manifest() (Manifest, error) {
	res := h.Request(RawRequest{Method: http.MethodGet, Path: "/manifest.json"})
	var m Manifest
	err := json.Unmarshal(res.Body, &m)
	return m, err
}

// Health calls GET /ether/health.
func (h *TestHarness) Health() (int, HealthResponse, error) {
	res := h.Request(RawRequest{Method: http.MethodGet, Path: "/ether/health"})
	var out HealthResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Configure calls POST /ether/configure.
func (h *TestHarness) Configure(settings map[string]any) (int, ConfigureResponse, error) {
	b, _ := json.Marshal(settings)
	res := h.Request(RawRequest{Method: http.MethodPost, Path: "/ether/configure", Body: b})
	var out ConfigureResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Test calls POST /ether/test.
func (h *TestHarness) Test() (int, TestResponse, error) {
	res := h.Request(RawRequest{Method: http.MethodPost, Path: "/ether/test"})
	var out TestResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Options calls GET /ether/options/{field}.
func (h *TestHarness) Options(field string) (int, []FieldOption, error) {
	res := h.Request(RawRequest{Method: http.MethodGet, Path: "/ether/options/" + url.PathEscape(field)})
	var out []FieldOption
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Catalog calls GET /catalog/{type}/{id}[/{extra}].json.
func (h *TestHarness) Catalog(typ, id string, extra map[string]string) (int, CatalogResponse, error) {
	suffix := encodeExtraSegment(extra)
	path := fmt.Sprintf("/catalog/%s/%s%s.json", url.PathEscape(typ), url.PathEscape(id), suffix)
	res := h.Request(RawRequest{Method: http.MethodGet, Path: path})
	var out CatalogResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Meta calls GET /meta/{type}/{id}.json.
func (h *TestHarness) Meta(typ, id string) (int, MetaResponse, error) {
	path := fmt.Sprintf("/meta/%s/%s.json", url.PathEscape(typ), url.PathEscape(id))
	res := h.Request(RawRequest{Method: http.MethodGet, Path: path})
	var out MetaResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Stream calls GET /stream/{type}/{id}[/{extra}].json.
func (h *TestHarness) Stream(typ, id string, extra map[string]string) (int, StreamResponse, error) {
	suffix := encodeExtraSegment(extra)
	path := fmt.Sprintf("/stream/%s/%s%s.json", url.PathEscape(typ), url.PathEscape(id), suffix)
	res := h.Request(RawRequest{Method: http.MethodGet, Path: path})
	var out StreamResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Subtitles calls GET /subtitles/{type}/{id}[/{extra}].json with optional pickToken.
func (h *TestHarness) Subtitles(typ, id, pickToken string, extra map[string]string) (int, SubtitlesResponse, RawResponse) {
	suffix := encodeExtraSegment(extra)
	path := fmt.Sprintf("/subtitles/%s/%s%s.json", url.PathEscape(typ), url.PathEscape(id), suffix)
	hdrs := map[string]string{}
	if pickToken != "" {
		hdrs["X-Ether-Pick"] = pickToken
	}
	res := h.Request(RawRequest{Method: http.MethodGet, Path: path, Headers: hdrs})
	var out SubtitlesResponse
	_ = json.Unmarshal(res.Body, &out)
	return res.Status, out, res
}

// Markers calls GET /ether/markers/{type}/{id}.json.
func (h *TestHarness) Markers(typ, id string) (int, MarkersResponse, error) {
	path := fmt.Sprintf("/ether/markers/%s/%s.json", url.PathEscape(typ), url.PathEscape(id))
	res := h.Request(RawRequest{Method: http.MethodGet, Path: path})
	var out MarkersResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Event calls POST /ether/event.
func (h *TestHarness) Event(events []EtherEvent) (int, EventResponse, error) {
	b, _ := json.Marshal(map[string]any{"events": events})
	res := h.Request(RawRequest{Method: http.MethodPost, Path: "/ether/event", Body: b})
	var out EventResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// View calls GET /ether/view/{page} and validates the returned ViewSpecDocument.
func (h *TestHarness) View(page string, query map[string]string) (int, ViewResponse, error) {
	clean := strings.TrimLeft(page, "/")
	if clean == "" {
		clean = "home"
	}
	res := h.Request(RawRequest{
		Method: http.MethodGet,
		Path:   "/ether/view/" + url.PathEscape(clean),
		Query:  query,
	})
	var out ViewResponse
	if err := json.Unmarshal(res.Body, &out); err != nil {
		return res.Status, out, err
	}
	if res.Status == http.StatusOK {
		if err := AssertValidViewSpec(out.Spec); err != nil {
			return res.Status, out, err
		}
	}
	return res.Status, out, nil
}

// Action calls POST /ether/action/{action}.
func (h *TestHarness) Action(action string, input map[string]any) (int, ActionResponse, error) {
	b, _ := json.Marshal(input)
	res := h.Request(RawRequest{
		Method: http.MethodPost,
		Path:   "/ether/action/" + url.PathEscape(action),
		Body:   b,
	})
	var out ActionResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// Tool calls POST /ether/tool/{name}.
func (h *TestHarness) Tool(name string, input map[string]any) (int, ToolResponse, error) {
	b, _ := json.Marshal(input)
	res := h.Request(RawRequest{
		Method: http.MethodPost,
		Path:   "/ether/tool/" + url.PathEscape(name),
		Body:   b,
	})
	var out ToolResponse
	err := json.Unmarshal(res.Body, &out)
	return res.Status, out, err
}

// File calls GET /ether/file/{path}.
func (h *TestHarness) File(path, rangeHeader string) RawResponse {
	hdrs := map[string]string{}
	if rangeHeader != "" {
		hdrs["Range"] = rangeHeader
	}
	return h.Request(RawRequest{
		Method:  http.MethodGet,
		Path:    "/ether/file/" + strings.TrimLeft(path, "/"),
		Headers: hdrs,
	})
}
