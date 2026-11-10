package kindstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	probeHeadBytes     = 512
	defaultHTTPTimeout = 30 * time.Second
	maxRangeRetries    = 2
)

// errNoRangeSupport is returned by probe when an upstream server ignores Range
// and responds with 200 OK for the whole resource. When body is non-nil, the
// caller must close it (or stream it into blobcache when demoting to download).
type noRangeSupportError struct {
	resp *http.Response
	size int64
	head []byte
}

func (e *noRangeSupportError) Error() string {
	return "kindstream: upstream does not support byte ranges (returned 200 OK)"
}

func (e *noRangeSupportError) closeBody() {
	if e != nil && e.resp != nil && e.resp.Body != nil {
		_ = e.resp.Body.Close()
	}
}

// HostBackoff tracks per-host exponential backoff and Retry-After windows on
// 429 and 503 responses.
type HostBackoff struct {
	mu    sync.Mutex
	hosts map[string]hostBackoffEntry
}

type hostBackoffEntry struct {
	failures int
	until    time.Time
}

// NewHostBackoff creates a per-host backoff tracker.
func NewHostBackoff() *HostBackoff {
	return &HostBackoff{hosts: make(map[string]hostBackoffEntry)}
}

var defaultBackoff = NewHostBackoff()

func (b *HostBackoff) wait(ctx context.Context, rawURL string) error {
	if b == nil {
		return nil
	}
	host := extractHost(rawURL)
	if host == "" {
		return nil
	}
	b.mu.Lock()
	entry, ok := b.hosts[host]
	b.mu.Unlock()
	if !ok {
		return nil
	}
	waitDur := time.Until(entry.until)
	if waitDur <= 0 {
		return nil
	}
	t := time.NewTimer(waitDur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (b *HostBackoff) onSuccess(rawURL string) {
	if b == nil {
		return
	}
	host := extractHost(rawURL)
	if host == "" {
		return
	}
	b.mu.Lock()
	delete(b.hosts, host)
	b.mu.Unlock()
}

func (b *HostBackoff) onRateLimit(rawURL string, retryAfterHeader string) {
	if b == nil {
		return
	}
	host := extractHost(rawURL)
	if host == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.hosts[host]
	e.failures++
	delay := time.Duration(100<<min(e.failures-1, 5)) * time.Millisecond // 100ms .. 3.2s
	if ra := parseRetryAfter(retryAfterHeader); ra > 0 {
		delay = min(ra, 10*time.Second)
	}
	e.until = time.Now().Add(delay)
	b.hosts[host] = e
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

type httpReaderConfig struct {
	Template  *URLTemplate
	Headers   map[string]string
	ItemIndex int
	KnownSize int64
	Name      string
	MIME      string
	Client    *http.Client
	AllowHost func(host string) bool
	Backoff   *HostBackoff
}

// HTTPReaderAt implements ByteSource (and io.ReaderAt) over an HTTP Range or
// range-templated URL source.
type HTTPReaderAt struct {
	tmpl      *URLTemplate
	headers   map[string]string
	itemIndex int
	size      int64
	name      string
	mime      string
	client    *http.Client
	allowHost func(host string) bool
	backoff   *HostBackoff

	head []byte // first <=512 bytes captured during initial probe
}

func newHTTPReaderAt(ctx context.Context, cfg httpReaderConfig) (*HTTPReaderAt, error) {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	r := &HTTPReaderAt{
		tmpl:      cfg.Template,
		headers:   cfg.Headers,
		itemIndex: cfg.ItemIndex,
		size:      cfg.KnownSize,
		name:      cfg.Name,
		mime:      cfg.MIME,
		client:    client,
		allowHost: cfg.AllowHost,
		backoff:   backoff,
	}
	if err := r.probe(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *HTTPReaderAt) probe(ctx context.Context) error {
	probeEnd := int64(probeHeadBytes - 1)
	if r.size > 0 && r.size-1 < probeEnd {
		probeEnd = r.size - 1
	}
	if probeEnd < 0 {
		probeEnd = 0
	}

	resp, targetURL, err := r.doRangeRequest(ctx, 0, probeEnd)
	if err != nil {
		return err
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		defer resp.Body.Close()
		totalSize, err := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if err == nil && totalSize > 0 {
			if r.size > 0 && r.size != totalSize {
				return fmt.Errorf("kindstream: size mismatch on probe: upstream reported %d, source declared %d", totalSize, r.size)
			}
			r.size = totalSize
		} else if r.size <= 0 {
			return errors.New("kindstream: 206 response missing total size in Content-Range and source Size is unset")
		}
		head, err := io.ReadAll(io.LimitReader(resp.Body, probeHeadBytes))
		if err != nil {
			return fmt.Errorf("kindstream: read probe body: %w", err)
		}
		r.head = head
		if r.name == "" {
			r.name = defaultNameFromURL(targetURL)
		}
		return nil

	case http.StatusOK:
		// If the URL template itself encodes the byte range ({byteRange} or
		// {byteStart}+{byteEnd}) or {byteStart}, the upstream may legitimately
		// return 200 OK for that slice.
		if r.tmpl.HasByteRange() || r.tmpl.HasByteStartOnly() {
			defer resp.Body.Close()
			head, err := io.ReadAll(io.LimitReader(resp.Body, probeHeadBytes))
			if err != nil {
				return fmt.Errorf("kindstream: read templated probe body: %w", err)
			}
			r.head = head
			if r.size <= 0 {
				if total, err := parseContentRangeTotal(resp.Header.Get("Content-Range")); err == nil && total > 0 {
					r.size = total
				} else if r.tmpl.HasByteStartOnly() && resp.ContentLength > 0 {
					r.size = resp.ContentLength
				}
			}
			if r.size <= 0 {
				return errors.New("kindstream: templated range source requires declared Size or Content-Range")
			}
			if r.name == "" {
				r.name = defaultNameFromURL(targetURL)
			}
			return nil
		}

		// Standard URL returned 200 OK to Range: bytes=0-511 -> upstream does
		// not support Range requests (or ignored Range). Read up to 512 bytes
		// for sniffing and let Open decide whether to demote to download.
		head, err := io.ReadAll(io.LimitReader(resp.Body, probeHeadBytes))
		if err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("kindstream: read 200 probe head: %w", err)
		}
		size := r.size
		if resp.ContentLength > 0 {
			if size > 0 && size != resp.ContentLength {
				_ = resp.Body.Close()
				return fmt.Errorf("kindstream: size mismatch on 200 response: Content-Length %d != declared %d", resp.ContentLength, size)
			}
			size = resp.ContentLength
		}
		return &noRangeSupportError{
			resp: resp,
			size: size,
			head: head,
		}

	default:
		_ = resp.Body.Close()
		return fmt.Errorf("kindstream: probe HTTP %d", resp.StatusCode)
	}
}

func (r *HTTPReaderAt) doRangeRequest(ctx context.Context, start, end int64) (*http.Response, string, error) {
	targetURL, err := r.tmpl.Expand(TemplateVars{
		ItemIndex: r.itemIndex,
		ByteStart: start,
		ByteEnd:   end,
		HasRange:  true,
	}, r.allowHost)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRangeRetries; attempt++ {
		if err := r.backoff.wait(ctx, targetURL); err != nil {
			return nil, targetURL, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, targetURL, err
		}
		for k, v := range r.headers {
			req.Header.Set(k, v)
		}
		if !r.tmpl.HasByteStartOnly() && !r.tmpl.HasByteRange() {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		}
		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			ra := resp.Header.Get("Retry-After")
			_ = resp.Body.Close()
			r.backoff.onRateLimit(targetURL, ra)
			lastErr = fmt.Errorf("kindstream: upstream HTTP %d", resp.StatusCode)
			continue
		}
		r.backoff.onSuccess(targetURL)
		return resp, targetURL, nil
	}
	return nil, targetURL, lastErr
}

// ReadAt reads len(p) bytes starting at byte offset off.
func (r *HTTPReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("kindstream: negative read offset %d", off)
	}
	if off >= r.size {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	want := int64(len(p))
	hitEOF := false
	if rem := r.size - off; want > rem {
		want = rem
		hitEOF = true
	}

	// Serve directly from cached probe head when the read falls inside it.
	if off+want <= int64(len(r.head)) {
		n := copy(p[:want], r.head[off:off+want])
		if hitEOF {
			return n, io.EOF
		}
		return n, nil
	}

	start := off
	end := off + want - 1
	resp, _, err := r.doRangeRequest(context.Background(), start, end)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && !(resp.StatusCode == http.StatusOK && r.tmpl.UsesRangePlaceholders()) {
		return 0, fmt.Errorf("kindstream: range read [%d-%d] unexpected HTTP %d", start, end, resp.StatusCode)
	}

	n, err := io.ReadFull(io.LimitReader(resp.Body, want), p[:want])
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if int64(n) == want && hitEOF {
				return n, io.EOF
			}
			return n, io.ErrUnexpectedEOF
		}
		return n, err
	}
	if hitEOF {
		return n, io.EOF
	}
	return n, nil
}

// SniffBytes returns up to the first 512 bytes captured during the initial probe.
func (r *HTTPReaderAt) SniffBytes() []byte {
	return r.head
}

// Size returns the total byte size of the resource.
func (r *HTTPReaderAt) Size() int64 { return r.size }

// Name returns the resource's filename.
func (r *HTTPReaderAt) Name() string { return r.name }

// MIME returns the resource's MIME type.
func (r *HTTPReaderAt) MIME() string { return r.mime }

// Close is a no-op for stateless HTTP range readers.
func (r *HTTPReaderAt) Close() error { return nil }

func parseContentRangeTotal(hdr string) (int64, error) {
	hdr = strings.TrimSpace(hdr)
	if hdr == "" {
		return 0, errors.New("empty Content-Range")
	}
	slash := strings.LastIndexByte(hdr, '/')
	if slash < 0 || slash+1 >= len(hdr) {
		return 0, fmt.Errorf("malformed Content-Range %q", hdr)
	}
	totalStr := strings.TrimSpace(hdr[slash+1:])
	if totalStr == "*" {
		return 0, errors.New("unknown total in Content-Range")
	}
	total, err := strconv.ParseInt(totalStr, 10, 64)
	if err != nil || total < 0 {
		return 0, fmt.Errorf("invalid Content-Range total %q", totalStr)
	}
	return total, nil
}

func defaultNameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "stream"
	}
	base := path.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return "stream"
	}
	return base
}
