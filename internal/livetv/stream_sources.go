package livetv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// StreamSource is one independently replaceable input to a channel. EPG,
// numbering, favourites and viewer policy belong to Channel, never to an input.
type StreamSource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"` // smaller first; equal priorities retain list order
	// AuthSourceID names the source whose provider credentials this input
	// uses; empty means the channel's own source.
	AuthSourceID string `json:"authSourceId,omitempty"`
	// Headers are request headers this input needs (a Stremio stream's
	// proxy headers), sent after the source's own.
	Headers map[string]string `json:"headers,omitempty"`
}

// MaxChannelInputs bounds one channel's stream inputs.
const MaxChannelInputs = 16

// maxInputHeaders bounds one input's request headers.
const maxInputHeaders = 16

// CleanInputHeaders keeps the request headers an input may carry, with
// canonical names: no headers the HTTP stack or the proxy manages, no line
// breaks, at most 16. It returns nil when none remain.
func CleanInputHeaders(in map[string]string) map[string]string {
	var out map[string]string
	for k, v := range in {
		name := http.CanonicalHeaderKey(strings.TrimSpace(k))
		if !validInputHeader(name, v) || len(out) == maxInputHeaders {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = v
	}
	return out
}

func validInputHeader(name, value string) bool {
	return name != "" && len(name) <= 128 && len(value) <= 4096 &&
		!strings.ContainsAny(name, " :\r\n") && !strings.ContainsAny(value, "\r\n") && !forbiddenHeaders[name]
}

type StreamSourceStatus struct {
	ID          string    `json:"id"`
	State       string    `json:"state"`
	Failures    int       `json:"failures"`
	LastChecked time.Time `json:"lastChecked,omitempty"`
	LastPlayed  time.Time `json:"lastPlayed,omitempty"`
	RetryAfter  time.Time `json:"retryAfter,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
}

type streamSourceHealth struct {
	StreamSourceStatus
}

func validStreamURL(raw string) bool {
	if validHTTP(raw) {
		return true
	}
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "plugin" && u.Host != "" && strings.Trim(u.Path, "/") != ""
}

func normalizeStreamSources(c *Channel) error {
	if len(c.StreamSources) == 0 {
		if !validStreamURL(c.StreamURL) {
			return fmt.Errorf("IPTV needs an HTTP(S) stream source")
		}
		id := stableID(c.ID, "primary")
		if c.SourceID != "" {
			id = stableID(c.ID, "playlist")
		}
		c.StreamSources = []StreamSource{{ID: id, Name: "Primary stream", URL: c.StreamURL, Enabled: true}}
	}
	if len(c.StreamSources) > MaxChannelInputs {
		return fmt.Errorf("a channel supports at most %d stream sources", MaxChannelInputs)
	}
	c.StreamSources = append([]StreamSource(nil), c.StreamSources...)
	ids, urls := map[string]bool{}, map[string]bool{}
	for i := range c.StreamSources {
		src := &c.StreamSources[i]
		src.URL = strings.TrimSpace(src.URL)
		src.Name = strings.TrimSpace(src.Name)
		if !validStreamURL(src.URL) {
			return fmt.Errorf("stream source %d must use HTTP(S)", i+1)
		}
		if src.ID == "" {
			src.ID = stableID(c.ID, src.URL)
		}
		if len(src.ID) > 200 || len(src.Name) > 200 || src.Priority < 0 || ids[src.ID] || urls[src.URL] {
			return fmt.Errorf("stream sources need unique IDs and URLs, valid names and nonnegative priorities")
		}
		if src.Name == "" {
			src.Name = fmt.Sprintf("Stream %d", i+1)
		}
		if len(src.Headers) > maxInputHeaders {
			return fmt.Errorf("stream source %d has more than %d headers", i+1, maxInputHeaders)
		}
		for k, v := range src.Headers {
			if !validInputHeader(http.CanonicalHeaderKey(strings.TrimSpace(k)), v) {
				return fmt.Errorf("stream source %d: header %q can't be set", i+1, k)
			}
		}
		ids[src.ID], urls[src.URL] = true, true
	}
	sort.SliceStable(c.StreamSources, func(i, j int) bool { return c.StreamSources[i].Priority < c.StreamSources[j].Priority })
	c.StreamURL = ""
	for i := range c.StreamSources {
		c.StreamSources[i].Priority = i
		if c.StreamURL == "" && c.StreamSources[i].Enabled {
			c.StreamURL = c.StreamSources[i].URL
		}
	}
	if c.Enabled && c.StreamURL == "" {
		return fmt.Errorf("an enabled IPTV channel needs an enabled stream source")
	}
	return nil
}

func mergeImportedStreams(imported, previous Channel) []StreamSource {
	if len(previous.StreamSources) == 0 {
		return imported.StreamSources
	}
	out := append([]StreamSource(nil), previous.StreamSources...)
	// Only the import-owned entry follows refreshed playlist URLs. Removing that
	// entry is an explicit owner override; refresh must not recreate it.
	for i := range out {
		if out[i].ID == stableID(imported.ID, "playlist") {
			out[i].URL = imported.StreamURL
			for _, src := range imported.StreamSources {
				if src.ID == out[i].ID {
					out[i].Headers = CleanInputHeaders(src.Headers)
					break
				}
			}
		}
	}
	return out
}

func sourceHealthKey(channelID string, src StreamSource) string {
	// A replacement URL starts with fresh health even when its entry ID survives.
	return stableID(channelID, src.ID, src.URL)
}

func (s *Service) sourceStatus(channelID string, src StreamSource) StreamSourceStatus {
	s.sourceMu.Lock()
	h := s.sourceHealth[sourceHealthKey(channelID, src)]
	s.sourceMu.Unlock()
	h.ID = src.ID
	switch {
	case !src.Enabled:
		h.State = "disabled"
	case h.RetryAfter.After(s.o.Now()):
		h.State = "cooling_down"
	case h.LastChecked.IsZero():
		h.State = "unchecked"
	case h.LastError != "":
		h.State = "retry_ready"
	default:
		h.State = "available"
	}
	return h.StreamSourceStatus
}

func (s *Service) recordStreamFailure(ss *session, rctx context.Context, err error) {
	if rctx.Err() == nil {
		s.recordSourceHealth(ss.info.Channel.ID, ss.inputSource, err, false)
		ss.mu.Lock()
		warn := ss.lastMediaWarning.IsZero() || time.Since(ss.lastMediaWarning) >= 5*time.Second
		if warn {
			ss.lastMediaWarning = time.Now()
		}
		ss.mu.Unlock()
		if warn {
			s.sessionLog(ss).Warn("live upstream media failed", Diagnose("media_fetch", err).LogAttrs()...)
		}
	}
}

// StreamSourceStatuses is owner-only health, without source URLs or upstream
// errors that might contain credentials. Transient failures never ban a source.
func (s *Service) StreamSourceStatuses(ctx context.Context, channelID string) ([]StreamSourceStatus, error) {
	c, err := s.Channel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if c.Kind != "iptv" {
		return []StreamSourceStatus{}, nil
	}
	if err := normalizeStreamSources(&c); err != nil {
		return nil, err
	}
	out := make([]StreamSourceStatus, 0, len(c.StreamSources))
	for _, src := range c.StreamSources {
		out = append(out, s.sourceStatus(c.ID, src))
	}
	return out, nil
}

func (s *Service) recordSourceHealth(channelID string, src StreamSource, err error, played bool) {
	if src.ID == "" || errors.Is(err, context.Canceled) {
		return
	}
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()
	if s.sourceHealth == nil {
		s.sourceHealth = map[string]streamSourceHealth{}
	}
	key := sourceHealthKey(channelID, src)
	h := s.sourceHealth[key]
	h.ID, h.LastChecked = src.ID, s.o.Now()
	if err != nil {
		h.Failures++
		cooldown := 15 * time.Second
		for i := 1; i < h.Failures && cooldown < 2*time.Minute; i++ {
			cooldown *= 2
		}
		if cooldown > 2*time.Minute {
			cooldown = 2 * time.Minute
		}
		h.RetryAfter = s.o.Now().Add(cooldown)
		h.LastError = "source unavailable"
		if errors.Is(err, context.DeadlineExceeded) {
			h.LastError = "source timed out"
		}
	} else {
		h.Failures, h.LastError, h.RetryAfter = 0, "", time.Time{}
		if played {
			h.LastPlayed = s.o.Now()
		}
	}
	if len(s.sourceHealth) >= 4096 {
		// Bound stale entries left by edited/deleted channel URLs.
		for k, v := range s.sourceHealth {
			if v.LastChecked.Before(s.o.Now().Add(-10 * time.Minute)) {
				delete(s.sourceHealth, k)
			}
		}
		if len(s.sourceHealth) >= 4096 {
			var oldest string
			var at time.Time
			for k, v := range s.sourceHealth {
				if oldest == "" || v.LastChecked.Before(at) {
					oldest, at = k, v.LastChecked
				}
			}
			delete(s.sourceHealth, oldest)
		}
	}
	s.sourceHealth[key] = h
}

// ResolveStreamSource resolves a channel's stream input at tune time. HTTP(S)
// URLs return directly; plugin:// inputs delegate to the installed stream
// opener under its Open deadline.
func (s *Service) ResolveStreamSource(ctx context.Context, c Channel, src StreamSource) (string, http.Header, error) {
	headers, err := s.upstreamHeaders(ctx, c, src)
	if err != nil {
		return "", nil, err
	}
	if !strings.HasPrefix(src.URL, "plugin://") {
		return src.URL, headers, nil
	}
	r := s.kindRegistry()
	r.mu.RLock()
	opener := r.streamOpener
	r.mu.RUnlock()
	if opener == nil {
		return "", nil, core.ErrNotAvailable
	}
	resolved, extra, err := opener(ctx, c, src)
	if err != nil {
		return "", nil, err
	}
	if !validHTTP(resolved) {
		return "", nil, core.ErrNotAvailable
	}
	for k, vals := range extra {
		for _, v := range vals {
			headers.Add(k, v)
		}
	}
	return resolved, headers, nil
}

// startIPTV selects one input per viewing session, under the caller's existing
// ten-second startup deadline. It never substitutes another provider's bytes,
// keys or media sequence into the active HLS resource map.
func (s *Service) startIPTV(ctx context.Context, ss *session) error {
	c := ss.info.Channel
	if err := normalizeStreamSources(&c); err != nil {
		return err
	}
	var candidates []StreamSource
	var earliestRetry time.Time
	for _, src := range c.StreamSources {
		if !src.Enabled {
			continue
		}
		retry := s.sourceStatus(c.ID, src).RetryAfter
		if retry.After(s.o.Now()) {
			if earliestRetry.IsZero() || retry.Before(earliestRetry) {
				earliestRetry = retry
			}
			s.sessionLog(ss).Debug("live source skipped", "input", src.ID, "reason", "cooldown", "retry_after", retry.Sub(s.o.Now()))
			continue
		}
		candidates = append(candidates, src)
	}
	var last error = core.ErrNotAvailable
	if len(candidates) == 0 && !earliestRetry.IsZero() {
		return &TuneError{Code: "source_cooldown", Stage: "source_selection", RetryAfter: earliestRetry.Sub(s.o.Now()), cause: core.ErrNotAvailable}
	}
	for index, src := range candidates {
		started := time.Now()
		log := s.sessionLog(ss).With("input", src.ID, "attempt", index+1, "candidates", len(candidates))
		log.Debug("live source attempt started")
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx := ctx
		cancel := func() {}
		if len(candidates) > 1 {
			// Leave a stalled primary only half the total tune budget, so a
			// backup can still be tried. Fast failures advance immediately.
			attemptCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		}
		resolvedURL, headers, err := s.ResolveStreamSource(attemptCtx, c, src)
		if err != nil {
			cancel()
			last = Diagnose("source_resolve", err)
			log.Warn("live source resolve failed", append(Diagnose("source_resolve", err).LogAttrs(), "elapsed", time.Since(started))...)
			if !errors.Is(ctx.Err(), context.Canceled) {
				s.recordSourceHealth(c.ID, src, err, false)
			}
			continue
		}
		grantSrc := src
		grantSrc.URL = resolvedURL
		ss.inputURL, ss.inputSource = resolvedURL, src
		ss.inputHeaders = headers.Clone()
		ss.info.MediaInfo, ss.relayInfo = nil, nil
		ss.resources = map[string]string{}
		// ffmpeg and ffprobe read this input through a loopback grant, never
		// with the provider URL or headers on their command line.
		err = s.grantInput(ss, grantSrc, headers)
		if err == nil {
			err = s.startIPTVSource(attemptCtx, ss)
		}
		cancel()
		if err == nil {
			ss.info.ActiveSourceID = src.ID
			s.recordSourceHealth(c.ID, src, nil, false)
			log.Info("live source ready", "elapsed", time.Since(started))
			return nil
		}
		if ss.relay != nil {
			ss.relay.Close()
			ss.relay = nil
		}
		if ss.relayCancel != nil {
			ss.relayCancel()
			ss.relayCancel = nil
		}
		s.revokeInput(ss)
		last = Diagnose("source_start", err)
		if !errors.Is(ctx.Err(), context.Canceled) {
			s.recordSourceHealth(c.ID, src, err, false)
		}
		health := s.sourceStatus(c.ID, src)
		attrs := append(Diagnose("source_start", err).LogAttrs(), "elapsed", time.Since(started), "next_candidate", index+1 < len(candidates), "cooldown_until", health.RetryAfter)
		if errors.Is(err, context.Canceled) {
			log.Debug("live source attempt cancelled", attrs...)
		} else {
			log.Warn("live source attempt failed", attrs...)
		}
	}
	return last
}
