package markers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
)

// SkipDBSource is the name this remote source stores its cached ranges
// under, both in markers_plugin_cache and in the owner's source order.
const SkipDBSource = "skipdb"

// SkipDBBaseURL is the open read API. It serves the same data as the
// skipdb.tv console and needs no key.
const SkipDBBaseURL = "https://api.skipdb.tv"

// FetchSkipDB reads the best intro/recap/credits/preview ranges for one
// movie or episode. SkipDB keys everything by IMDb id and shifts the
// timestamps to the played duration itself, so duration is passed in
// seconds (as the API expects) and no local alignment is needed. A 404 or
// an empty body is not an error: it means no approved ranges exist yet.
func FetchSkipDB(ctx context.Context, baseURL, apiKey, imdb string, season, episode int, duration time.Duration) ([]Marker, error) {
	if imdb == "" {
		return nil, nil
	}
	if baseURL == "" {
		baseURL = SkipDBBaseURL
	}
	q := url.Values{"imdb_id": {imdb}}
	if season > 0 && episode > 0 {
		q.Set("season", strconv.Itoa(season))
		q.Set("episode", strconv.Itoa(episode))
	}
	if duration > 0 {
		q.Set("duration", strconv.FormatInt(int64(duration.Round(time.Second)/time.Second), 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/segments?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ether/1")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := egress.Client(egress.CapMetadata, 5*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skipdb: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Segments map[string]*struct {
			StartMS *int64 `json:"start_ms"`
			EndMS   *int64 `json:"end_ms"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	return skipDBMarkers(body.Segments, duration), nil
}

// skipDBMarkers maps SkipDB's segment types onto Ether's marker types and
// drops anything outside the played runtime. A zero-length start/end pair is
// SkipDB's "confirmed, no segment of this type" sentinel and yields nothing.
func skipDBMarkers(segments map[string]*struct {
	StartMS *int64 `json:"start_ms"`
	EndMS   *int64 `json:"end_ms"`
}, duration time.Duration) []Marker {
	if len(segments) == 0 {
		return nil
	}
	kinds := []struct{ remote, local string }{
		{"intro", "intro"},
		{"recap", "recap"},
		{"outro", "credits"},
		{"preview", "preview"},
	}
	var out []Marker
	for _, k := range kinds {
		seg := segments[k.remote]
		if seg == nil || seg.StartMS == nil {
			continue
		}
		end := int64(0)
		if seg.EndMS != nil {
			end = *seg.EndMS
		} else if k.remote == "outro" && duration > 0 {
			end = duration.Milliseconds() // credits run to the end of the stream
		}
		if *seg.StartMS == 0 && end == 0 {
			continue // confirmed absent, not a range
		}
		m := Marker{Type: k.local, StartMS: *seg.StartMS, EndMS: end,
			Final:  k.local == "credits" && duration > 0 && end >= duration.Milliseconds(),
			Source: SkipDBSource}
		if valid(m, duration) {
			out = append(out, m)
		}
	}
	return out
}

// ProbeSkipDB checks that SkipDB answers, and that apiKey is accepted when
// one is set. Reading is open, so an empty key probes the public API. A 404
// still means the service accepted the request.
func ProbeSkipDB(ctx context.Context, baseURL, apiKey string) error {
	if baseURL == "" {
		baseURL = SkipDBBaseURL
	}
	// A fixed, well-known episode. The probe only cares that the service
	// accepts the call, not that ranges exist.
	endpoint := strings.TrimRight(baseURL, "/") + "/api/segments?imdb_id=tt0903747&season=1&episode=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ether/1")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := egress.Client(egress.CapMetadata, 10*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		if apiKey != "" {
			return errors.New("skipdb rejected the key")
		}
		return fmt.Errorf("skipdb: HTTP %d", resp.StatusCode)
	default:
		return fmt.Errorf("skipdb: HTTP %d", resp.StatusCode)
	}
}
