package etherplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// HostFetchRequest describes an outbound HTTP call via the host service.
type HostFetchRequest struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// HostFetchResponse is returned by Host.Fetch.
type HostFetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// FolderHash contains digests, never file contents. ModTime is Unix seconds.
type FolderHash struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	CRC32   string `json:"crc32"`
	MD5     string `json:"md5"`
	SHA1    string `json:"sha1"`
}

// FolderEntry describes one entry returned by Host.FoldersList or Host.FoldersStat.
type FolderEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size,omitempty"`
	Mtime int64  `json:"mtime,omitempty"`
}

// CatalogTitleSummary is a catalog record returned by Host.CatalogSearch or Host.CatalogGet.
type CatalogTitleSummary struct {
	ID     any    `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Year   int    `json:"year,omitempty"`
	IMDBID string `json:"imdbId,omitempty"`
	TMDBID int    `json:"tmdbId,omitempty"`
}

// ViewerWatchEntry is one watch-history entry returned by Host.ViewerHistory.
type ViewerWatchEntry struct {
	TitleID   string `json:"titleId"`
	Title     string `json:"title,omitempty"`
	WatchedAt string `json:"watchedAt,omitempty"`
	Completed bool   `json:"completed,omitempty"`
}

// Host provides access to Ether host services (KV, secrets, HTTP, catalog,
// viewers, collections, folders, owner notifications, and logging).
type Host interface {
	KVGet(ctx context.Context, key string) (string, bool, error)
	KVPut(ctx context.Context, key, value string) error
	KVDelete(ctx context.Context, key string) error
	KVList(ctx context.Context, prefix string) ([]string, error)
	SecretGet(ctx context.Context, name string) (string, error)
	Fetch(ctx context.Context, req HostFetchRequest) (HostFetchResponse, error)
	CatalogSearch(ctx context.Context, query string, limit, offset int) ([]CatalogTitleSummary, error)
	CatalogGet(ctx context.Context, id string) (*CatalogTitleSummary, error)
	ViewerHistory(ctx context.Context, viewerID int64, limit, offset int) ([]ViewerWatchEntry, error)
	SetWatchState(ctx context.Context, viewerID int64, titleID string, watched bool) error
	UpsertCollection(ctx context.Context, collection map[string]any) error
	FoldersList(ctx context.Context, root, path string) ([]FolderEntry, error)
	FoldersStat(ctx context.Context, root, path string) (FolderEntry, error)
	FoldersHash(ctx context.Context, root, path string) (FolderHash, error)
	NotifyOwner(ctx context.Context, text string) error
	Log(ctx context.Context, level, message string, attrs map[string]any) error
	// Content-kind host services. KindUpsert/KindDelete need a push-mode
	// kind; ResolveSources needs permissions.sources.
	KindUpsert(ctx context.Context, kindKey string, items []KindItem) (accepted int, err error)
	KindDelete(ctx context.Context, kindKey string, externalIDs []string) error
	KindItems(ctx context.Context, kindKey, cursor string, limit int) (KindPage, error)
	ResolveSources(ctx context.Context, q SourceQuery) ([]SourceCandidate, error)
	CachePut(ctx context.Context, libraryKey, name string, data []byte) (blobRef string, err error)
	CacheOpen(ctx context.Context, blobRef string) ([]byte, error)
	// StreamURL mints a short-lived, scoped ticket for one of this plugin's
	// own kind items and returns the Ether stream URL to embed in a
	// frontend adapter response (spec §6.5). viewerID is the viewer the
	// adapter authenticated.
	StreamURL(ctx context.Context, viewerID int64, itemRef string) (string, error)
}

// RemoteHostOptions configures a RemoteHostClient for sidecar plugins.
type RemoteHostOptions struct {
	BaseURL    string
	Credential string
	HTTPClient *http.Client
}

// RemoteHostClient implements Host by calling `/ether/plugin-host/*` on the
// Ether server authenticated with `X-Ether-Plugin-Credential`.
type RemoteHostClient struct {
	baseURL    string
	credential string
	client     *http.Client
}

// NewRemoteHostClient creates a RemoteHostClient.
func NewRemoteHostClient(opts RemoteHostOptions) *RemoteHostClient {
	base := opts.BaseURL
	if base == "" {
		base = os.Getenv("ETHER_HOST_URL")
	}
	if base == "" {
		base = "http://127.0.0.1:32450"
	}
	base = strings.TrimRight(base, "/")

	cred := opts.Credential
	if cred == "" {
		cred = os.Getenv("ETHER_PLUGIN_CREDENTIAL")
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &RemoteHostClient{
		baseURL:    base,
		credential: cred,
		client:     hc,
	}
}

// SettingsGet reads the current non-secret settings from Ether.
func (c *RemoteHostClient) SettingsGet(ctx context.Context) (map[string]any, error) {
	var result struct {
		Settings map[string]any `json:"settings"`
	}
	_, err := c.callJSON(ctx, http.MethodGet, "/ether/plugin-host/settings", nil, &result, false)
	return result.Settings, err
}

func (c *RemoteHostClient) callJSON(
	ctx context.Context,
	method, path string,
	in any,
	out any,
	allow404 bool,
) (bool, error) {
	var bodyReader io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return false, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Ether-Plugin-Credential", c.credential)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return false, err
	}
	if allow404 && resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errDoc struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &errDoc) == nil && errDoc.Error.Message != "" {
			return false, fmt.Errorf("%s", errDoc.Error.Message)
		}
		return false, fmt.Errorf("host call %s %s failed with status %d", method, path, resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return false, err
		}
	}
	return true, nil
}

// KVGet reads a key from the plugin's KV namespace.
func (c *RemoteHostClient) KVGet(ctx context.Context, key string) (string, bool, error) {
	var res struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	found, err := c.callJSON(ctx, http.MethodGet, "/ether/plugin-host/kv/"+url.PathEscape(key), nil, &res, true)
	if err != nil || !found {
		return "", false, err
	}
	return res.Value, true, nil
}

// KVPut writes a key-value pair to the plugin's KV namespace.
func (c *RemoteHostClient) KVPut(ctx context.Context, key, value string) error {
	_, err := c.callJSON(
		ctx,
		http.MethodPut,
		"/ether/plugin-host/kv/"+url.PathEscape(key),
		map[string]string{"value": value},
		nil,
		false,
	)
	return err
}

// KVDelete deletes a key from the plugin's KV namespace.
func (c *RemoteHostClient) KVDelete(ctx context.Context, key string) error {
	_, err := c.callJSON(ctx, http.MethodDelete, "/ether/plugin-host/kv/"+url.PathEscape(key), nil, nil, false)
	return err
}

// KVList lists keys matching the given prefix in the plugin's KV namespace.
func (c *RemoteHostClient) KVList(ctx context.Context, prefix string) ([]string, error) {
	path := "/ether/plugin-host/kv"
	if prefix != "" {
		path += "?prefix=" + url.QueryEscape(prefix)
	}
	var res struct {
		Keys []string `json:"keys"`
	}
	if _, err := c.callJSON(ctx, http.MethodGet, path, nil, &res, false); err != nil {
		return nil, err
	}
	return res.Keys, nil
}

// SecretGet is only available in hosted mode; remote plugins receive secrets via /ether/configure.
func (c *RemoteHostClient) SecretGet(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("secret.get is only available in hosted plugins; remote plugins receive secrets via /ether/configure")
}

// Fetch performs an outbound HTTP request.
func (c *RemoteHostClient) Fetch(ctx context.Context, r HostFetchRequest) (HostFetchResponse, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if r.Body != "" {
		bodyReader = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL, bodyReader)
	if err != nil {
		return HostFetchResponse{}, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return HostFetchResponse{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return HostFetchResponse{}, err
	}
	hdrs := map[string]string{}
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			hdrs[strings.ToLower(k)] = vals[0]
		}
	}
	return HostFetchResponse{
		Status:  resp.StatusCode,
		Headers: hdrs,
		Body:    string(raw),
	}, nil
}

// CatalogSearch searches the Ether catalog.
func (c *RemoteHostClient) CatalogSearch(
	ctx context.Context,
	query string,
	limit, offset int,
) ([]CatalogTitleSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	var res struct {
		Titles []CatalogTitleSummary `json:"titles"`
	}
	if _, err := c.callJSON(ctx, http.MethodGet, "/ether/plugin-host/catalog/search?"+q.Encode(), nil, &res, false); err != nil {
		return nil, err
	}
	return res.Titles, nil
}

// CatalogGet fetches one title summary from the Ether catalog.
func (c *RemoteHostClient) CatalogGet(ctx context.Context, id string) (*CatalogTitleSummary, error) {
	var res CatalogTitleSummary
	found, err := c.callJSON(ctx, http.MethodGet, "/ether/plugin-host/catalog/"+url.PathEscape(id), nil, &res, true)
	if err != nil || !found {
		return nil, err
	}
	return &res, nil
}

// ViewerHistory reads watch history for a granted viewer.
func (c *RemoteHostClient) ViewerHistory(
	ctx context.Context,
	viewerID int64,
	limit, offset int,
) ([]ViewerWatchEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	path := fmt.Sprintf("/ether/plugin-host/viewers/%d/history?%s", viewerID, q.Encode())
	var out []ViewerWatchEntry
	if _, err := c.callJSON(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// SetWatchState updates the watch state for a granted viewer.
func (c *RemoteHostClient) SetWatchState(
	ctx context.Context,
	viewerID int64,
	titleID string,
	watched bool,
) error {
	_, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/ether/plugin-host/viewers/watch-state",
		map[string]any{
			"viewerId": viewerID,
			"titleId":  titleID,
			"watched":  watched,
		},
		nil,
		false,
	)
	return err
}

// UpsertCollection creates or updates a plugin-managed collection.
func (c *RemoteHostClient) UpsertCollection(ctx context.Context, collection map[string]any) error {
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/collections/upsert", collection, nil, false)
	return err
}

// FoldersList is only supported in hosted mode; sidecars access local folders directly.
func (c *RemoteHostClient) FoldersList(_ context.Context, _, _ string) ([]FolderEntry, error) {
	return nil, fmt.Errorf("folders.list is for hosted plugins; sidecar plugins read their local fileRoot directly")
}

// FoldersStat is only supported in hosted mode; sidecars access local folders directly.
func (c *RemoteHostClient) FoldersStat(_ context.Context, _, _ string) (FolderEntry, error) {
	return FolderEntry{}, fmt.Errorf("folders.stat is for hosted plugins; sidecar plugins read their local fileRoot directly")
}

// NotifyOwner sends an owner notification through Ether.
func (c *RemoteHostClient) NotifyOwner(ctx context.Context, text string) error {
	_, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/ether/plugin-host/notify",
		map[string]string{"text": text},
		nil,
		false,
	)
	return err
}

// Log sends a structured log entry to Ether.
func (c *RemoteHostClient) Log(ctx context.Context, level, message string, attrs map[string]any) error {
	payload := map[string]any{
		"level":   level,
		"message": message,
	}
	if attrs != nil {
		payload["attrs"] = attrs
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/log", payload, nil, false)
	return err
}

// KindUpsert pushes items into a push-mode kind library.
func (c *RemoteHostClient) KindUpsert(ctx context.Context, kindKey string, items []KindItem) (int, error) {
	var out struct {
		Accepted int `json:"accepted"`
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/kinds/"+url.PathEscape(kindKey)+"/upsert",
		map[string]any{"items": items}, &out, false)
	return out.Accepted, err
}

// KindDelete removes items from a push-mode kind library.
func (c *RemoteHostClient) KindDelete(ctx context.Context, kindKey string, externalIDs []string) error {
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/kinds/"+url.PathEscape(kindKey)+"/delete",
		map[string]any{"externalIds": externalIDs}, nil, false)
	return err
}

// KindItems reads the plugin's own kind library back, keyset-paged.
func (c *RemoteHostClient) KindItems(ctx context.Context, kindKey, cursor string, limit int) (KindPage, error) {
	path := "/ether/plugin-host/kinds/" + url.PathEscape(kindKey) + "/items?cursor=" + url.QueryEscape(cursor)
	if limit > 0 {
		path += "&limit=" + strconv.Itoa(limit)
	}
	var page KindPage
	_, err := c.callJSON(ctx, http.MethodGet, path, nil, &page, false)
	return page, err
}

// ResolveSources asks Ether to find sources through its indexers/debrid.
func (c *RemoteHostClient) ResolveSources(ctx context.Context, q SourceQuery) ([]SourceCandidate, error) {
	var out struct {
		Candidates []SourceCandidate `json:"candidates"`
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/sources/resolve", q, &out, false)
	return out.Candidates, err
}

// CachePut stores one blob in a kind library's cache; the ref is content-addressed.
func (c *RemoteHostClient) CachePut(ctx context.Context, libraryKey, name string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/ether/plugin-host/cache/"+url.PathEscape(libraryKey)+"?name="+url.QueryEscape(name),
		bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if c.credential != "" {
		req.Header.Set("X-Ether-Plugin-Credential", c.credential)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("plugin-host cache put: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Blob, nil
}

// CacheOpen reads one cached blob back.
func (c *RemoteHostClient) CacheOpen(ctx context.Context, blobRef string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/ether/plugin-host/cache/"+strings.TrimPrefix(blobRef, "/"), nil)
	if err != nil {
		return nil, err
	}
	if c.credential != "" {
		req.Header.Set("X-Ether-Plugin-Credential", c.credential)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugin-host cache open: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// StreamURL mints a scoped stream ticket and returns the Ether stream URL
// for one of this plugin's own kind items (spec §6.5).
func (c *RemoteHostClient) StreamURL(ctx context.Context, viewerID int64, itemRef string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	_, err := c.callJSON(ctx, http.MethodPost, "/ether/plugin-host/frontend/stream-url",
		map[string]any{"viewerId": viewerID, "itemRef": itemRef}, &out, false)
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (*RemoteHostClient) FoldersHash(context.Context, string, string) (FolderHash, error) {
	return FolderHash{}, fmt.Errorf("folders.hash requires the hosted runtime")
}
