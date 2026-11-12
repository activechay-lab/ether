package mdblist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
)

func metadataClient() *http.Client { return egress.Client(egress.CapMetadata, 30*time.Second) }

// HTTPError exposes status and Retry-After without copying upstream bodies
// into logs. Callers must honour Retry-After before retrying a rate limit.
type HTTPError struct {
	Status     int
	RetryAfter string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("mdblist: HTTP %d", e.Status) }

// Client talks to MDBList's account API using a viewer's personal API key.
// The key travels as MDBList's documented `apikey` query parameter.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) base() string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.mdblist.com"
	}
	return base
}

// request sends one bounded API request. Pacing and persisted retry policy
// belong to the coordinating service, shared across nodes.
func (c Client) request(ctx context.Context, method, path, apiKey string, query url.Values, body any, result any) error {
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("mdblist: credentials unavailable")
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("apikey", apiKey)
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path+"?"+query.Encode(), payload)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Ether/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = metadataClient()
	}
	safe := *hc
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safe.Do(req)
	if err != nil {
		// A transport error names the request URL, and the API key travels in
		// its query: report the endpoint without it, never the key.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("mdblist: %s %s: %w", uerr.Op, req.URL.Path, uerr.Err)
		}
		return fmt.Errorf("mdblist: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("mdblist: response too large")
	}
	if result != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, result)
	}
	return nil
}
