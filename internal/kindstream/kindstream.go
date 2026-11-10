// Package kindstream is Ether's unified byte-acquisition and streaming layer
// for plugin-provided content kinds (spec §4, §5, §15.1). Every plugin media
// item — comic page, ebook file, audiobook chapter, podcast episode, or video
// part — resolves through kindstream into a random-access ByteSource.
package kindstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
)

// Retrieval pattern constants (spec §4.1, §4.3).
const (
	PatternRange     = "range"
	PatternDownload  = "download"
	PatternDirect    = "direct"
	PatternContainer = "container"
	PatternRelease   = "release"
)

var (
	// ErrMemberUndecodable is returned when an archive member is compressed or
	// solid in a format/method that internal/archive cannot decode sequentially.
	ErrMemberUndecodable = errors.New("kindstream: archive member not sequentially decodable")

	// ErrHostNotAllowed is returned when a source URL's host is rejected by the
	// plugin's declared network allowlist.
	ErrHostNotAllowed = errors.New("kindstream: host not in plugin allowlist")

	// ErrSizeLimitExceeded is returned when a download or range-demoted source
	// exceeds its configured maxBytes or library cache quota.
	ErrSizeLimitExceeded = errors.New("kindstream: source exceeds download size limit")

	// ErrMIMEMismatch is returned when sniffed magic bytes disagree with the
	// plugin-declared MIME at the family level (e.g. text/html error page for an
	// image/* source).
	ErrMIMEMismatch = errors.New("kindstream: sniffed MIME family disagrees with declared MIME")
)

// ByteSource is the uniform random-access stream interface produced by
// kindstream for all plugin content kinds (spec §5.1).
type ByteSource interface {
	io.ReaderAt
	Size() int64  // total byte length; always > 0 once Open returns
	Name() string // filename for Content-Disposition and container sniffing
	MIME() string // MIME type (plugin-declared, verified/refined by sniffing first 512 B)
	Close() error
}

// Deps supplies the runtime dependencies needed by Open and OpenSources
// (spec §5.1).
type Deps struct {
	HTTP             *http.Client
	Cache            *blobcache.Namespace
	OpenRelease      func(ctx context.Context, r core.ReleaseHint) (core.MediaSource, error)
	Secret           func(ctx context.Context, name string) (string, error)
	AllowHost        func(host string) bool
	MaxDownloadBytes int64
	ItemIndex        int
	Backoff          *HostBackoff
}

// Open resolves a single core.PlayableSource into a random-access ByteSource
// according to its retrieval pattern (spec §4.3, §5.1).
func Open(ctx context.Context, src core.PlayableSource, deps Deps) (ByteSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pattern := strings.ToLower(strings.TrimSpace(src.Pattern))
	if pattern == "" {
		if src.Container != nil {
			pattern = PatternContainer
		} else if src.Release != nil {
			pattern = PatternRelease
		} else {
			pattern = PatternRange
		}
	}

	// Fast path: if BlobRef is already populated on a non-container source, open
	// directly from the library's blobcache namespace.
	if strings.TrimSpace(src.BlobRef) != "" && pattern != PatternContainer {
		if deps.Cache == nil {
			return nil, errors.New("kindstream: source specifies blobRef but Cache is nil")
		}
		cached, err := deps.Cache.Open(blobcache.Ref(strings.TrimSpace(src.BlobRef)))
		if err != nil {
			return nil, err
		}
		if src.Size > 0 && cached.Size() != src.Size {
			_ = cached.Close()
			return nil, fmt.Errorf("kindstream: blobRef size %d != declared %d", cached.Size(), src.Size)
		}
		return finalizeSourceMIME(cached, src.MIME, src.ID)
	}

	switch pattern {
	case PatternRange:
		return openRangeSource(ctx, src, deps)
	case PatternDownload:
		return openDownloadSource(ctx, src, deps)
	case PatternDirect:
		return openDirectSource(ctx, src, deps)
	case PatternContainer:
		if src.Container == nil {
			return nil, errors.New("kindstream: container pattern requires core.ContainerRef")
		}
		return OpenContainer(ctx, *src.Container, src.MIME, src.MaxBytes, deps)
	case PatternRelease:
		return openReleaseSource(ctx, src, deps)
	default:
		return nil, fmt.Errorf("kindstream: unknown retrieval pattern %q", src.Pattern)
	}
}

// OpenSources tries each candidate core.PlayableSource in order, returning the first
// ByteSource that opens and validates cleanly. When all candidates fail (or
// sources is empty), OpenSources returns an error wrapping
// core.ErrSourceUnavailable (spec §4.5).
func OpenSources(ctx context.Context, sources []core.PlayableSource, deps Deps) (ByteSource, core.PlayableSource, error) {
	if len(sources) == 0 {
		return nil, core.PlayableSource{}, core.ErrSourceUnavailable
	}
	var errs []error
	for _, cand := range sources {
		if err := ctx.Err(); err != nil {
			return nil, core.PlayableSource{}, err
		}
		bs, err := Open(ctx, cand, deps)
		if err == nil {
			return bs, cand, nil
		}
		errs = append(errs, fmt.Errorf("source %q (%s): %w", cand.ID, cand.Pattern, err))
	}
	return nil, core.PlayableSource{}, fmt.Errorf("%w: %v", core.ErrSourceUnavailable, errors.Join(errs...))
}

func openRangeSource(ctx context.Context, src core.PlayableSource, deps Deps) (ByteSource, error) {
	tmpl, err := ParseURLTemplate(src.URL, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	hdrs, err := ExpandHeaders(ctx, src.Headers, deps.Secret)
	if err != nil {
		return nil, err
	}
	hr, err := newHTTPReaderAt(ctx, httpReaderConfig{
		Template:  tmpl,
		Headers:   hdrs,
		ItemIndex: deps.ItemIndex,
		KnownSize: src.Size,
		MIME:      src.MIME,
		Client:    deps.HTTP,
		AllowHost: deps.AllowHost,
		Backoff:   deps.Backoff,
	})
	if err == nil {
		resolvedMIME, merr := verifyAndResolveMIME(hr.SniffBytes(), src.MIME, hr.Name())
		if merr != nil {
			_ = hr.Close()
			return nil, merr
		}
		hr.mime = resolvedMIME
		return hr, nil
	}

	var noRange *noRangeSupportError
	if errors.As(err, &noRange) {
		defer noRange.closeBody()
		// Demote range -> download when within maxBytes (spec §4.3, §5.2).
		return downloadFromNoRangeResponse(ctx, src.URL, noRange, src.SHA256, src.MIME, src.MaxBytes, deps)
	}
	return nil, err
}

func downloadFromNoRangeResponse(
	ctx context.Context,
	rawURL string,
	noRange *noRangeSupportError,
	expectedSHA256 string,
	declaredMIME string,
	maxBytes int64,
	deps Deps,
) (ByteSource, error) {
	limit := effectiveDownloadMaxBytes(maxBytes, deps)
	if limit > 0 && noRange.size > limit {
		return nil, fmt.Errorf("%w: upstream returned 200 without Range support and size %d > limit %d", ErrSizeLimitExceeded, noRange.size, limit)
	}
	resolvedMIME, err := verifyAndResolveMIME(noRange.head, declaredMIME, defaultNameFromURL(rawURL))
	if err != nil {
		return nil, err
	}

	combined := io.MultiReader(bytes.NewReader(noRange.head), noRange.resp.Body)
	if limit > 0 {
		combined = io.LimitReader(combined, limit+1)
	}

	cacheKey := downloadCacheKey(rawURL, deps.ItemIndex)
	if deps.Cache != nil {
		expectedSize := int64(-1)
		if noRange.size > 0 {
			expectedSize = noRange.size
		}
		ref, err := deps.Cache.PutWithExpectedSHA256(ctx, cacheKey, combined, expectedSize, expectedSHA256, resolvedMIME)
		if err != nil {
			if errors.Is(err, blobcache.ErrTooLarge) {
				return nil, fmt.Errorf("%w: %v", ErrSizeLimitExceeded, err)
			}
			return nil, err
		}
		cached, err := deps.Cache.Open(ref)
		if err != nil {
			return nil, err
		}
		return &renamedByteSource{
			ByteSource: cached,
			name:       defaultNameFromURL(rawURL),
			mime:       resolvedMIME,
		}, nil
	}

	return readIntoMemoryByteSource(combined, limit, noRange.size, expectedSHA256, defaultNameFromURL(rawURL), resolvedMIME)
}

func openDownloadSource(ctx context.Context, src core.PlayableSource, deps Deps) (ByteSource, error) {
	limit := effectiveDownloadMaxBytes(src.MaxBytes, deps)
	if limit > 0 && src.Size > limit {
		return nil, fmt.Errorf("%w: declared size %d exceeds limit %d", ErrSizeLimitExceeded, src.Size, limit)
	}

	tmpl, err := ParseURLTemplate(src.URL, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	targetURL, err := tmpl.Expand(TemplateVars{ItemIndex: deps.ItemIndex}, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	hdrs, err := ExpandHeaders(ctx, src.Headers, deps.Secret)
	if err != nil {
		return nil, err
	}

	cacheKey := downloadCacheKey(targetURL, deps.ItemIndex)
	name := defaultNameFromURL(targetURL)

	fetchStream := func(ctx context.Context) (*http.Response, error) {
		client := deps.HTTP
		if client == nil {
			client = &http.Client{Timeout: defaultHTTPTimeout}
		}
		backoff := deps.Backoff
		if backoff == nil {
			backoff = defaultBackoff
		}
		if err := backoff.wait(ctx, targetURL); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			backoff.onRateLimit(targetURL, resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("kindstream: download HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("kindstream: download HTTP %d", resp.StatusCode)
		}
		backoff.onSuccess(targetURL)
		return resp, nil
	}

	if deps.Cache != nil {
		ref, err := deps.Cache.SingleFlightPut(ctx, cacheKey, func(ctx context.Context) (blobcache.Ref, error) {
			resp, err := fetchStream(ctx)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()

			expectedSize := int64(-1)
			if src.Size > 0 {
				expectedSize = src.Size
			} else if resp.ContentLength > 0 {
				expectedSize = resp.ContentLength
			}
			if limit > 0 && expectedSize > limit {
				return "", fmt.Errorf("%w: Content-Length %d exceeds limit %d", ErrSizeLimitExceeded, expectedSize, limit)
			}

			var r io.Reader = resp.Body
			if limit > 0 {
				r = &limitEnforcingReader{r: resp.Body, max: limit}
			}
			ref, err := deps.Cache.PutWithExpectedSHA256(ctx, cacheKey, r, expectedSize, src.SHA256, src.MIME)
			if err != nil {
				if errors.Is(err, blobcache.ErrTooLarge) {
					return "", fmt.Errorf("%w: %v", ErrSizeLimitExceeded, err)
				}
				return "", err
			}
			return ref, nil
		})
		if err != nil {
			return nil, err
		}
		cached, err := deps.Cache.Open(ref)
		if err != nil {
			return nil, err
		}
		return finalizeSourceMIME(&renamedByteSource{
			ByteSource: cached,
			name:       name,
			mime:       src.MIME,
		}, src.MIME, name)
	}

	resp, err := fetchStream(ctx)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	expectedSize := src.Size
	if expectedSize <= 0 && resp.ContentLength > 0 {
		expectedSize = resp.ContentLength
	}
	return readIntoMemoryByteSource(resp.Body, limit, expectedSize, src.SHA256, name, src.MIME)
}

// DirectSource represents a validated "direct" core.PlayableSource. Callers that
// support client redirects (spec §4.3) can extract the target URL via
// DirectURL() and issue a 307 Temporary Redirect without proxying bytes; callers
// that require a ByteSource stream (such as tests or local transcode bridges)
// can call ReadAt, which lazily opens the underlying HTTP source.
type DirectSource struct {
	ctx     context.Context
	src     core.PlayableSource
	deps    Deps
	url     string
	headers map[string]string
	name    string
	mime    string

	mu       sync.Mutex
	delegate ByteSource
}

// DirectURL returns the validated, template-expanded target URL for a 307 redirect.
func (d *DirectSource) DirectURL() string { return d.url }

// DirectHeaders returns the resolved request headers for the direct URL.
func (d *DirectSource) DirectHeaders() map[string]string { return d.headers }

// AsDirectSource extracts the *DirectSource if bs was opened from a "direct"
// core.PlayableSource.
func AsDirectSource(bs ByteSource) (*DirectSource, bool) {
	d, ok := bs.(*DirectSource)
	return d, ok
}

func openDirectSource(ctx context.Context, src core.PlayableSource, deps Deps) (ByteSource, error) {
	tmpl, err := ParseURLTemplate(src.URL, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	expandedURL, err := tmpl.Expand(TemplateVars{ItemIndex: deps.ItemIndex}, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	hdrs, err := ExpandHeaders(ctx, src.Headers, deps.Secret)
	if err != nil {
		return nil, err
	}
	mime := strings.TrimSpace(src.MIME)
	if mime == "" {
		mime = "application/octet-stream"
	}
	return &DirectSource{
		ctx:     ctx,
		src:     src,
		deps:    deps,
		url:     expandedURL,
		headers: hdrs,
		name:    defaultNameFromURL(expandedURL),
		mime:    mime,
	}, nil
}

func (d *DirectSource) ensureDelegate() (ByteSource, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.delegate != nil {
		return d.delegate, nil
	}
	cp := d.src
	cp.Pattern = PatternRange
	bs, err := openRangeSource(d.ctx, cp, d.deps)
	if err != nil {
		return nil, err
	}
	d.delegate = bs
	return bs, nil
}

func (d *DirectSource) ReadAt(p []byte, off int64) (int, error) {
	bs, err := d.ensureDelegate()
	if err != nil {
		return 0, err
	}
	return bs.ReadAt(p, off)
}

func (d *DirectSource) Size() int64 {
	if d.src.Size > 0 {
		return d.src.Size
	}
	if bs, err := d.ensureDelegate(); err == nil {
		return bs.Size()
	}
	return 0
}

func (d *DirectSource) Name() string { return d.name }
func (d *DirectSource) MIME() string { return d.mime }

func (d *DirectSource) Close() error {
	d.mu.Lock()
	del := d.delegate
	d.delegate = nil
	d.mu.Unlock()
	if del != nil {
		return del.Close()
	}
	return nil
}

func openReleaseSource(ctx context.Context, src core.PlayableSource, deps Deps) (ByteSource, error) {
	if src.Release == nil {
		return nil, errors.New("kindstream: release pattern requires core.ReleaseHint")
	}
	if deps.OpenRelease == nil {
		return nil, errors.New("kindstream: OpenRelease dependency is not configured")
	}
	ms, err := deps.OpenRelease(ctx, *src.Release)
	if err != nil {
		return nil, err
	}

	wrapped := &mediaSourceByteSource{
		ms:   ms,
		name: path.Base(ms.Name()),
		mime: src.MIME,
	}
	if wrapped.name == "" || wrapped.name == "." || wrapped.name == "/" {
		wrapped.name = ms.ReleaseID()
	}

	// Check if the release should compose with Container extraction (spec §4.3:
	// "If the resolved file in the release is itself an archive (.rar, .cbz, .7z),
	// release and container compose").
	var head [probeHeadBytes]byte
	headLen := min(ms.Size(), int64(probeHeadBytes))
	var headSlice []byte
	if headLen > 0 {
		n, rerr := ms.ReadAt(head[:headLen], 0)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			_ = ms.Close()
			return nil, rerr
		}
		headSlice = head[:n]
	}

	archFmt := archive.Detect(headSlice)
	if src.Container != nil || (archFmt != archive.FormatUnknown && !isDeclaredArchiveMIME(src.MIME)) {
		pwSecret := src.Release.PasswordSecret
		var member string
		var memberIdx *int
		var fmtHint string
		if src.Container != nil {
			if src.Container.PasswordSecret != "" {
				pwSecret = src.Container.PasswordSecret
			}
			member = src.Container.Member
			memberIdx = src.Container.MemberIndex
			fmtHint = src.Container.Format
		} else if src.Release.FileHint != "" {
			member = src.Release.FileHint
		}
		return openContainerOverVolumes(ctx, []archive.Volume{{R: ms, Size: ms.Size()}}, []io.Closer{ms}, fmtHint, member, memberIdx, pwSecret, src.MIME, deps)
	}

	resolvedMIME, merr := verifyAndResolveMIME(headSlice, src.MIME, wrapped.name)
	if merr != nil {
		_ = ms.Close()
		return nil, merr
	}
	wrapped.mime = resolvedMIME
	return wrapped, nil
}

func openContainerOverVolumes(
	ctx context.Context,
	vols []archive.Volume,
	closers []io.Closer,
	formatHint string,
	memberName string,
	memberIdx *int,
	passwordSecret string,
	declaredMIME string,
	deps Deps,
) (ByteSource, error) {
	closeAll := func() {
		for _, cl := range closers {
			_ = cl.Close()
		}
	}
	var password string
	if strings.TrimSpace(passwordSecret) != "" {
		if deps.Secret == nil {
			closeAll()
			return nil, fmt.Errorf("kindstream: release archive requires password secret %q", passwordSecret)
		}
		pw, err := deps.Secret(ctx, strings.TrimSpace(passwordSecret))
		if err != nil {
			closeAll()
			return nil, err
		}
		password = pw
	}
	format, err := detectContainerFormat(vols[0], formatHint)
	if err != nil {
		closeAll()
		return nil, err
	}
	members, err := listContainerMembers(ctx, format, vols, password)
	if err != nil {
		closeAll()
		return nil, err
	}
	selected, _, err := selectArchiveMember(members, memberName, memberIdx)
	if err != nil {
		closeAll()
		return nil, err
	}
	mr, err := archive.NewMemberReader(selected, vols)
	if err != nil {
		closeAll()
		if errors.Is(err, archive.ErrCompressed) || errors.Is(err, archive.ErrSolid) {
			return nil, fmt.Errorf("%w: %v", ErrMemberUndecodable, err)
		}
		return nil, err
	}
	var head [probeHeadBytes]byte
	headLen := min(selected.Size, int64(probeHeadBytes))
	var headSlice []byte
	if headLen > 0 {
		n, _ := mr.ReadAt(head[:headLen], 0)
		headSlice = head[:n]
	}
	resolvedMIME, merr := verifyAndResolveMIME(headSlice, declaredMIME, selected.Name)
	if merr != nil {
		closeAll()
		return nil, merr
	}
	return &containerMemberSource{
		r:       mr,
		size:    selected.Size,
		name:    memberDisplayName(selected.Name),
		mime:    resolvedMIME,
		closers: closers,
	}, nil
}

// ---------------------------------------------------------------------------
// MIME sniffing and verification (spec §4.3, §5.1)

func finalizeSourceMIME(src ByteSource, declaredMIME, fallbackName string) (ByteSource, error) {
	headLen := min(src.Size(), int64(probeHeadBytes))
	var head []byte
	if headLen > 0 {
		head = make([]byte, headLen)
		n, err := src.ReadAt(head, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			_ = src.Close()
			return nil, err
		}
		head = head[:n]
	}
	name := src.Name()
	if name == "" {
		name = fallbackName
	}
	resolvedMIME, err := verifyAndResolveMIME(head, declaredMIME, name)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	if resolvedMIME == src.MIME() && name == src.Name() {
		return src, nil
	}
	return &renamedByteSource{
		ByteSource: src,
		name:       name,
		mime:       resolvedMIME,
	}, nil
}

// verifyAndResolveMIME sniffs the first 512 bytes of a stream and compares the
// detected type against the plugin-declared MIME.
//
// If the upstream returned an HTML error page (text/html) when the plugin
// declared a non-HTML type, or if positive magic bytes belong to a different
// media family than declared (e.g. audio/* vs image/*), ErrMIMEMismatch is
// returned so OpenSources falls back to the next core.PlayableSource.
// When magic bytes identify a more specific MIME within the same family (e.g.
// image/png when declared was image/jpeg), the sniffed MIME wins (spec §5.1).
func verifyAndResolveMIME(head []byte, declared string, filename string) (string, error) {
	declaredBase := normalizeMIME(declared)
	magicMIME, positiveMagic := sniffMagicBytes(head)

	if positiveMagic {
		if magicMIME == "text/html" && declaredBase != "" && declaredBase != "text/html" {
			return "", fmt.Errorf("%w: declared %q, sniffed %q", ErrMIMEMismatch, declaredBase, magicMIME)
		}
		if declaredBase != "" && declaredBase != "application/octet-stream" {
			declFam := mimeFamily(declaredBase)
			sniffFam := mimeFamily(magicMIME)
			if declFam != "" && sniffFam != "" && declFam != sniffFam && !compatibleArchiveMIME(declaredBase, magicMIME) {
				return "", fmt.Errorf("%w: declared %q (%s), sniffed %q (%s)", ErrMIMEMismatch, declaredBase, declFam, magicMIME, sniffFam)
			}
		}
		// Keep comicbook/ebook subtype if magic is the underlying container (e.g. application/vnd.comicbook+zip over zip).
		if compatibleArchiveMIME(declaredBase, magicMIME) && declaredBase != "application/octet-stream" && declaredBase != "" {
			return declaredBase, nil
		}
		return magicMIME, nil
	}

	if declaredBase != "" {
		return declaredBase, nil
	}
	if len(head) > 0 {
		detected := normalizeMIME(http.DetectContentType(head))
		if detected != "" {
			return detected, nil
		}
	}
	return "application/octet-stream", nil
}

// sniffMagicBytes checks well-known magic byte signatures for images, audio,
// video, ebooks, comic archives, and HTML error pages (spec §4.3).
func sniffMagicBytes(head []byte) (mime string, positive bool) {
	if len(head) == 0 {
		return "", false
	}
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg", true
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(head, []byte("GIF87a")) || bytes.HasPrefix(head, []byte("GIF89a")):
		return "image/gif", true
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp", true
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf", true
	case bytes.HasPrefix(head, []byte("fLaC")):
		return "audio/flac", true
	case bytes.HasPrefix(head, []byte("ID3")) || (len(head) >= 2 && head[0] == 0xFF && (head[1]&0xE0) == 0xE0 && (head[1]&0x18) != 0x08 && (head[1]&0x06) != 0x00):
		return "audio/mpeg", true
	case bytes.HasPrefix(head, []byte("OggS")):
		if bytes.Contains(head[:min(len(head), 64)], []byte("OpusHead")) {
			return "audio/opus", true
		}
		return "audio/ogg", true
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE")):
		return "audio/wav", true
	case bytes.HasPrefix(head, []byte("\x1A\x45\xDF\xA3")):
		if bytes.Contains(head[:min(len(head), 64)], []byte("webm")) {
			return "video/webm", true
		}
		return "video/x-matroska", true
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		brand := string(head[8:12])
		switch brand {
		case "avif", "avis":
			return "image/avif", true
		case "M4A ", "M4B ", "F4A ":
			return "audio/mp4", true
		default:
			return "video/mp4", true
		}
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")), bytes.HasPrefix(head, []byte("PK\x07\x08")):
		if len(head) >= 58 && bytes.Equal(head[30:58], []byte("mimetypeapplication/epub+zip")) {
			return "application/epub+zip", true
		}
		return "application/zip", true
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		return "application/vnd.rar", true
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return "application/x-7z-compressed", true
	}

	trimmed := bytes.TrimLeft(head, " \t\r\n")
	lower := bytes.ToLower(trimmed[:min(len(trimmed), 64)])
	if bytes.HasPrefix(lower, []byte("<!doctype html")) ||
		bytes.HasPrefix(lower, []byte("<html")) ||
		bytes.HasPrefix(lower, []byte("<head")) ||
		bytes.HasPrefix(lower, []byte("<body")) {
		return "text/html", true
	}
	return "", false
}

func normalizeMIME(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if semi := strings.IndexByte(m, ';'); semi >= 0 {
		m = strings.TrimSpace(m[:semi])
	}
	return m
}

func mimeFamily(m string) string {
	m = normalizeMIME(m)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "text/"):
		return "text"
	case m == "application/pdf":
		return "pdf"
	case isDeclaredArchiveMIME(m):
		return "archive"
	default:
		return ""
	}
}

func isDeclaredArchiveMIME(m string) bool {
	switch normalizeMIME(m) {
	case "application/zip",
		"application/x-zip-compressed",
		"application/vnd.comicbook+zip",
		"application/epub+zip",
		"application/vnd.rar",
		"application/x-rar-compressed",
		"application/vnd.comicbook-rar",
		"application/x-cbr",
		"application/x-cbz",
		"application/x-7z-compressed",
		"application/vnd.comicbook+7z":
		return true
	default:
		return false
	}
}

func compatibleArchiveMIME(declared, sniffed string) bool {
	if isDeclaredArchiveMIME(declared) && isDeclaredArchiveMIME(sniffed) {
		return true
	}
	// Audio MP4 / M4B containers often sniff with a generic MP4 ftyp brand.
	if (strings.HasPrefix(declared, "audio/") && sniffed == "video/mp4") ||
		(strings.HasPrefix(declared, "video/") && sniffed == "audio/mp4") {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Transcode bridge & HTTP serving helpers for Slice J (spec §15.1)

// Seeker wraps any ByteSource as an io.ReadSeekCloser so it can be passed to
// http.ServeContent, standard library readers, or transcode pipelines.
func Seeker(src ByteSource) io.ReadSeekCloser {
	if rsc, ok := src.(io.ReadSeekCloser); ok {
		return rsc
	}
	return &byteSourceSeeker{
		src:  src,
		sr:   io.NewSectionReader(src, 0, src.Size()),
		size: src.Size(),
	}
}

type byteSourceSeeker struct {
	src  ByteSource
	sr   *io.SectionReader
	size int64
}

func (b *byteSourceSeeker) Read(p []byte) (int, error) {
	return b.sr.Read(p)
}

func (b *byteSourceSeeker) Seek(offset int64, whence int) (int64, error) {
	return b.sr.Seek(offset, whence)
}

func (b *byteSourceSeeker) Close() error {
	return b.src.Close()
}

// Handler returns an http.Handler that serves a ByteSource with full HTTP Range
// support (206 Partial Content, Accept-Ranges: bytes, Content-Type, and
// Content-Length).
func Handler(src ByteSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := src.MIME(); m != "" {
			w.Header().Set("Content-Type", m)
		}
		sr := io.NewSectionReader(src, 0, src.Size())
		http.ServeContent(w, r, src.Name(), time.Time{}, sr)
	})
}

// ServeLoopback starts a loopback HTTP server (http://127.0.0.1:<port>/<name>)
// backed by src so local ffmpeg / ffprobe sessions in internal/transcode can
// seek and stream any ByteSource over HTTP Range (spec §15.1).
// Calling closeFn shuts down the loopback server without closing src.
func ServeLoopback(src ByteSource) (urlStr string, closeFn func() error, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("kindstream: listen loopback: %w", err)
	}
	srv := &http.Server{
		Handler:           Handler(src),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	name := path.Clean("/" + src.Name())
	if name == "/" || name == "/." {
		name = "/stream"
	}
	var once sync.Once
	closeServer := func() error {
		var cerr error
		once.Do(func() {
			cerr = srv.Close()
		})
		return cerr
	}
	return "http://" + ln.Addr().String() + name, closeServer, nil
}

// MediaSource adapts a ByteSource into a core.MediaSource so any pipeline
// accepting core.MediaSource can consume a kindstream ByteSource directly.
func MediaSource(src ByteSource, releaseID string) core.MediaSource {
	if releaseID == "" {
		releaseID = src.Name()
	}
	return &byteSourceMediaAdapter{
		ByteSource: src,
		releaseID:  releaseID,
	}
}

type byteSourceMediaAdapter struct {
	ByteSource
	releaseID string
}

func (a *byteSourceMediaAdapter) ReleaseID() string { return a.releaseID }

// ---------------------------------------------------------------------------
// Internal helpers

func effectiveDownloadMaxBytes(srcMax int64, deps Deps) int64 {
	if srcMax > 0 {
		return srcMax
	}
	if deps.MaxDownloadBytes > 0 {
		return deps.MaxDownloadBytes
	}
	if deps.Cache != nil {
		return deps.Cache.Quota().EffectiveMaxBytes()
	}
	return blobcache.DefaultMaxBytes
}

func downloadCacheKey(rawURL string, itemIdx int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", rawURL, itemIdx)))
	return "dl-" + hex.EncodeToString(h[:12]) + "-" + defaultNameFromURL(rawURL)
}

type limitEnforcingReader struct {
	r    io.Reader
	read int64
	max  int64
}

func (l *limitEnforcingReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		l.read += int64(n)
		if l.read > l.max {
			return n, fmt.Errorf("%w: stream exceeded %d bytes", ErrSizeLimitExceeded, l.max)
		}
	}
	return n, err
}

func readIntoMemoryByteSource(r io.Reader, limit, expectedSize int64, expectedSHA256, name, declaredMIME string) (ByteSource, error) {
	if limit > 0 {
		r = &limitEnforcingReader{r: r, max: limit}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if expectedSize > 0 && int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("kindstream: size mismatch: got %d bytes, want %d", len(data), expectedSize)
	}
	if strings.TrimSpace(expectedSHA256) != "" {
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, strings.TrimSpace(expectedSHA256)) {
			return nil, fmt.Errorf("kindstream: sha256 mismatch: got %s, want %s", got, expectedSHA256)
		}
	}
	head := data[:min(len(data), probeHeadBytes)]
	resolvedMIME, err := verifyAndResolveMIME(head, declaredMIME, name)
	if err != nil {
		return nil, err
	}
	return &bytesByteSource{
		r:    bytes.NewReader(data),
		size: int64(len(data)),
		name: name,
		mime: resolvedMIME,
	}, nil
}

type bytesByteSource struct {
	r    *bytes.Reader
	size int64
	name string
	mime string
}

func (b *bytesByteSource) ReadAt(p []byte, off int64) (int, error) {
	return b.r.ReadAt(p, off)
}

func (b *bytesByteSource) Size() int64  { return b.size }
func (b *bytesByteSource) Name() string { return b.name }
func (b *bytesByteSource) MIME() string { return b.mime }
func (b *bytesByteSource) Close() error { return nil }

type renamedByteSource struct {
	ByteSource
	name string
	mime string
}

func (r *renamedByteSource) Name() string {
	if r.name != "" {
		return r.name
	}
	return r.ByteSource.Name()
}

func (r *renamedByteSource) MIME() string {
	if r.mime != "" {
		return r.mime
	}
	return r.ByteSource.MIME()
}

type mediaSourceByteSource struct {
	ms   core.MediaSource
	name string
	mime string
}

func (m *mediaSourceByteSource) ReadAt(p []byte, off int64) (int, error) {
	return m.ms.ReadAt(p, off)
}

func (m *mediaSourceByteSource) Size() int64  { return m.ms.Size() }
func (m *mediaSourceByteSource) Name() string { return m.name }
func (m *mediaSourceByteSource) MIME() string { return m.mime }
func (m *mediaSourceByteSource) Close() error { return m.ms.Close() }
