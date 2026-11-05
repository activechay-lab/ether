package stremio

// The Stremio indexer's own HTTP transport (backend.go, client.go): the
// owner-entered add-on link is read directly, errors name only its host.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// userAgent identifies Ether honestly. An addon that serves only the
// Stremio app may refuse it; Ether never poses as a browser or as Stremio.
const userAgent = "Ether (+https://github.com/braedonsaunders/ether)"

const (
	defaultTimeout = 20 * time.Second
	maxManifest    = 1 << 20
	maxStreams     = 8 << 20
)

// BaseURL returns an addon's base URL from its install URL: a stremio://
// link becomes https://, and a trailing /manifest.json or /configure is
// removed. The path keeps the addon's settings segment, if any.
func BaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(raw, "stremio://"); ok {
		raw = "https://" + rest
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("stremio: the addon URL must be an http(s) or stremio:// install link")
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	p := strings.TrimRight(u.EscapedPath(), "/")
	for _, suffix := range []string{"/manifest.json", "/configure"} {
		p = strings.TrimSuffix(p, suffix)
	}
	u.RawPath, u.Path = "", ""
	if p != "" {
		if u.Path, err = url.PathUnescape(p); err != nil {
			return nil, errors.New("stremio: the addon URL path is not valid")
		}
		u.RawPath = p
	}
	return u, nil
}

// FetchManifest reads and checks the manifest of the addon at rawURL.
func FetchManifest(ctx context.Context, client *http.Client, rawURL string) (Manifest, error) {
	base, err := BaseURL(rawURL)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := getJSON(ctx, client, base, "/manifest.json", maxManifest, &m); err != nil {
		return Manifest{}, err
	}
	if m.ID == "" && len(m.Resources) == 0 {
		return Manifest{}, fmt.Errorf("stremio: %s did not answer with an addon manifest", base.Host)
	}
	return m, m.CheckStreams()
}

// httpError is a non-2xx addon answer.
type httpError struct {
	host   string
	status int
	retry  string
}

func (e *httpError) Error() string {
	if e.status == http.StatusForbidden || e.status == http.StatusUnauthorized {
		return fmt.Sprintf("stremio: %s refused Ether (HTTP %d); some public addon instances serve only the Stremio app, so use an instance that allows servers or host the addon yourself", e.host, e.status)
	}
	return fmt.Sprintf("stremio: %s answered HTTP %d", e.host, e.status)
}

// withoutReferer keeps credential-bearing URLs out of redirect headers.
func withoutReferer(client *http.Client) *http.Client {
	safeClient := *client
	priorRedirect := safeClient.CheckRedirect
	safeClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		next.Header.Del("Referer")
		if priorRedirect != nil {
			return priorRedirect(next, via)
		}
		if len(via) >= 10 {
			return errors.New("stremio: too many redirects")
		}
		return nil
	}
	return &safeClient
}

// getJSON decodes base+suffix into out. Errors name only the addon host:
// an addon URL may carry the owner's settings, including keys.
func getJSON(ctx context.Context, client *http.Client, base *url.URL, suffix string, limit int64, out any) error {
	u := *base
	u.Path += suffix
	if u.RawPath != "" {
		u.RawPath += suffix
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("stremio: %s: invalid request", base.Host)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := withoutReferer(client).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("stremio: %s: request failed", base.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return &httpError{host: base.Host, status: resp.StatusCode, retry: resp.Header.Get("Retry-After")}
	}
	body := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("stremio: %s: response read failed", base.Host)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("stremio: %s answered with more than %d bytes", base.Host, limit)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("stremio: %s did not answer with JSON", base.Host)
	}
	return nil
}
