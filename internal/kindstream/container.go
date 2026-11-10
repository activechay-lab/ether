package kindstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
)

// OpenContainer opens an archive container (RAR4/RAR5/7z/ZIP) over one or more
// VolumeRefs and returns a ByteSource for the requested inner member (spec §5.3).
//
// Store-mode members (including encrypted store-mode RAR/7z when PasswordSecret
// is supplied) are served directly via archive.NewMemberReader without
// extracting the archive. Compressed ZIP members are sequentially decoded into
// deps.Cache via ExtractMember; undecodable compressed archives (such as
// compressed RAR) fail with ErrMemberUndecodable so the caller can fall back to
// the next core.PlayableSource.
func OpenContainer(ctx context.Context, c core.ContainerRef, declaredMIME string, maxBytes int64, deps Deps) (ByteSource, error) {
	if len(c.Volumes) == 0 {
		return nil, errors.New("kindstream: container has no volumes")
	}

	var password string
	if strings.TrimSpace(c.PasswordSecret) != "" {
		if deps.Secret == nil {
			return nil, fmt.Errorf("kindstream: container requires password secret %q but no secret provider configured", c.PasswordSecret)
		}
		pw, err := deps.Secret(ctx, strings.TrimSpace(c.PasswordSecret))
		if err != nil {
			return nil, fmt.Errorf("kindstream: resolve container password %q: %w", c.PasswordSecret, err)
		}
		password = pw
	}

	vols := make([]archive.Volume, 0, len(c.Volumes))
	var closers []io.Closer
	closeAll := func() {
		for _, cl := range closers {
			_ = cl.Close()
		}
	}

	for i, vref := range c.Volumes {
		volSrc, err := openVolumeRef(ctx, vref, maxBytes, deps)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("kindstream: open container volume %d: %w", i, err)
		}
		closers = append(closers, volSrc)
		vols = append(vols, archive.Volume{
			R:    volSrc,
			Size: volSrc.Size(),
		})
	}

	format, err := detectContainerFormat(vols[0], c.Format)
	if err != nil {
		closeAll()
		return nil, err
	}

	members, err := listContainerMembers(ctx, format, vols, password)
	if err != nil {
		closeAll()
		return nil, err
	}

	selected, nonDirIdx, err := selectArchiveMember(members, c.Member, c.MemberIndex)
	if err != nil {
		closeAll()
		return nil, err
	}

	streamErr := selected.Streamable()
	if streamErr == nil {
		mr, err := archive.NewMemberReader(selected, vols)
		if err != nil {
			closeAll()
			return nil, err
		}
		headLen := min(selected.Size, int64(probeHeadBytes))
		var head []byte
		if headLen > 0 {
			head = make([]byte, headLen)
			n, rerr := mr.ReadAt(head, 0)
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				closeAll()
				return nil, fmt.Errorf("kindstream: read member head: %w", rerr)
			}
			head = head[:n]
		}
		resolvedMIME, err := verifyAndResolveMIME(head, declaredMIME, selected.Name)
		if err != nil {
			closeAll()
			return nil, err
		}
		return &containerMemberSource{
			r:       mr,
			size:    selected.Size,
			name:    memberDisplayName(selected.Name),
			mime:    resolvedMIME,
			closers: closers,
		}, nil
	}

	if errors.Is(streamErr, archive.ErrCompressed) || errors.Is(streamErr, archive.ErrSolid) {
		// Release any open volume handles after ExtractMember caches what it needs.
		defer closeAll()
		return ExtractMember(ctx, c, format, selected, nonDirIdx, declaredMIME, maxBytes, deps)
	}

	closeAll()
	return nil, streamErr
}

// ExtractMember downloads the container's volume(s) into deps.Cache (if not
// already local) and sequentially decodes the target member into a cached file
// in deps.Cache, returning a random-access ByteSource over that file.
//
// If the archive format or compression method is not sequentially decodable by
// internal/archive, ExtractMember returns ErrMemberUndecodable.
func ExtractMember(
	ctx context.Context,
	c core.ContainerRef,
	format archive.Format,
	selected archive.Member,
	nonDirIdx int,
	declaredMIME string,
	maxBytes int64,
	deps Deps,
) (ByteSource, error) {
	if format != archive.FormatZIP || selected.Encrypted || (selected.Method != "deflate" && selected.Method != "store") {
		return nil, fmt.Errorf("%w: %s member %q (method %s)", ErrMemberUndecodable, format, selected.Name, selected.Method)
	}
	if deps.Cache == nil {
		return nil, fmt.Errorf("%w: blobcache namespace required to extract compressed member %q", ErrMemberUndecodable, selected.Name)
	}
	limit := effectiveDownloadMaxBytes(maxBytes, deps)
	if limit > 0 && selected.Size > limit {
		return nil, fmt.Errorf("%w: member %q unpacked size %d exceeds limit %d", ErrSizeLimitExceeded, selected.Name, selected.Size, limit)
	}

	extractKey := containerExtractCacheKey(c, selected.Name, nonDirIdx, deps.ItemIndex)
	ref, err := deps.Cache.SingleFlightPut(ctx, extractKey, func(ctx context.Context) (blobcache.Ref, error) {
		localVols := make([]archive.Volume, 0, len(c.Volumes))
		var volClosers []io.Closer
		defer func() {
			for _, cl := range volClosers {
				_ = cl.Close()
			}
		}()

		for i, vref := range c.Volumes {
			localSrc, err := ensureVolumeCached(ctx, vref, maxBytes, deps)
			if err != nil {
				return "", fmt.Errorf("kindstream: cache container volume %d: %w", i, err)
			}
			volClosers = append(volClosers, localSrc)
			localVols = append(localVols, archive.Volume{
				R:    localSrc,
				Size: localSrc.Size(),
			})
		}

		rc, m, err := archive.OpenZIPMember(localVols, func(idx int, cand archive.Member) bool {
			if strings.TrimSpace(c.Member) != "" {
				return normalizeArchivePath(cand.Name) == normalizeArchivePath(selected.Name)
			}
			return idx == nonDirIdx
		})
		if err != nil {
			if errors.Is(err, archive.ErrUnsupported) || errors.Is(err, archive.ErrCompressed) || errors.Is(err, archive.ErrSolid) {
				return "", fmt.Errorf("%w: %v", ErrMemberUndecodable, err)
			}
			return "", err
		}
		defer rc.Close()

		return deps.Cache.PutWithExpectedSHA256(ctx, extractKey, rc, m.Size, "", declaredMIME)
	})
	if err != nil {
		return nil, err
	}

	cachedSrc, err := deps.Cache.Open(ref)
	if err != nil {
		return nil, err
	}
	headLen := min(cachedSrc.Size(), int64(probeHeadBytes))
	var head []byte
	if headLen > 0 {
		head = make([]byte, headLen)
		n, rerr := cachedSrc.ReadAt(head, 0)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			_ = cachedSrc.Close()
			return nil, rerr
		}
		head = head[:n]
	}
	resolvedMIME, err := verifyAndResolveMIME(head, declaredMIME, selected.Name)
	if err != nil {
		_ = cachedSrc.Close()
		return nil, err
	}
	return &renamedByteSource{
		ByteSource: cachedSrc,
		name:       memberDisplayName(selected.Name),
		mime:       resolvedMIME,
	}, nil
}

func openVolumeRef(ctx context.Context, v core.VolumeRef, maxBytes int64, deps Deps) (ByteSource, error) {
	if strings.TrimSpace(v.BlobRef) != "" {
		if deps.Cache == nil {
			return nil, errors.New("kindstream: volume references blobRef but Cache is nil")
		}
		src, err := deps.Cache.Open(blobcache.Ref(strings.TrimSpace(v.BlobRef)))
		if err != nil {
			return nil, err
		}
		if v.Size > 0 && src.Size() != v.Size {
			_ = src.Close()
			return nil, fmt.Errorf("kindstream: cached volume size %d != declared %d", src.Size(), v.Size)
		}
		return src, nil
	}
	if strings.TrimSpace(v.URL) == "" {
		return nil, errors.New("kindstream: volume has neither url nor blobRef")
	}

	tmpl, err := ParseURLTemplate(v.URL, deps.AllowHost)
	if err != nil {
		return nil, err
	}
	hdrs, err := ExpandHeaders(ctx, v.Headers, deps.Secret)
	if err != nil {
		return nil, err
	}
	hr, err := newHTTPReaderAt(ctx, httpReaderConfig{
		Template:  tmpl,
		Headers:   hdrs,
		ItemIndex: deps.ItemIndex,
		KnownSize: v.Size,
		MIME:      "application/octet-stream",
		Client:    deps.HTTP,
		AllowHost: deps.AllowHost,
		Backoff:   deps.Backoff,
	})
	if err == nil {
		return hr, nil
	}
	var noRange *noRangeSupportError
	if errors.As(err, &noRange) {
		defer noRange.closeBody()
		if deps.Cache == nil {
			return nil, err
		}
		return downloadFromNoRangeResponse(ctx, v.URL, noRange, "", "application/octet-stream", maxBytes, deps)
	}
	return nil, err
}

func ensureVolumeCached(ctx context.Context, v core.VolumeRef, maxBytes int64, deps Deps) (ByteSource, error) {
	if strings.TrimSpace(v.BlobRef) != "" {
		return deps.Cache.Open(blobcache.Ref(strings.TrimSpace(v.BlobRef)))
	}
	ps := core.PlayableSource{
		Pattern:  PatternDownload,
		URL:      v.URL,
		Headers:  v.Headers,
		Size:     v.Size,
		MaxBytes: maxBytes,
		MIME:     "application/octet-stream",
	}
	return openDownloadSource(ctx, ps, deps)
}

func detectContainerFormat(vol0 archive.Volume, hint string) (archive.Format, error) {
	if vol0.Size < archive.SignatureLen {
		return archive.FormatUnknown, fmt.Errorf("kindstream: volume 0 too short (%d bytes): %w", vol0.Size, archive.ErrNotArchive)
	}
	var head [archive.SignatureLen]byte
	n, err := vol0.R.ReadAt(head[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return archive.FormatUnknown, fmt.Errorf("kindstream: read volume 0 signature: %w", err)
	}
	detected := archive.Detect(head[:n])
	hint = strings.ToLower(strings.TrimSpace(hint))
	switch hint {
	case "":
		if detected == archive.FormatUnknown {
			return archive.FormatUnknown, archive.ErrNotArchive
		}
		return detected, nil
	case "rar", "cbr":
		if detected == archive.FormatRAR4 || detected == archive.FormatRAR5 {
			return detected, nil
		}
		return archive.FormatUnknown, fmt.Errorf("kindstream: expected rar container, detected %s: %w", detected, archive.ErrNotArchive)
	case "rar4":
		if detected != archive.FormatRAR4 {
			return archive.FormatUnknown, fmt.Errorf("kindstream: expected rar4 container, detected %s: %w", detected, archive.ErrNotArchive)
		}
		return detected, nil
	case "rar5":
		if detected != archive.FormatRAR5 {
			return archive.FormatUnknown, fmt.Errorf("kindstream: expected rar5 container, detected %s: %w", detected, archive.ErrNotArchive)
		}
		return detected, nil
	case "7z", "cb7":
		if detected != archive.Format7z {
			return archive.FormatUnknown, fmt.Errorf("kindstream: expected 7z container, detected %s: %w", detected, archive.ErrNotArchive)
		}
		return detected, nil
	case "zip", "cbz", "epub":
		if detected != archive.FormatZIP {
			return archive.FormatUnknown, fmt.Errorf("kindstream: expected zip container, detected %s: %w", detected, archive.ErrNotArchive)
		}
		return detected, nil
	default:
		return archive.FormatUnknown, fmt.Errorf("kindstream: unsupported container format %q: %w", hint, archive.ErrUnsupported)
	}
}

func listContainerMembers(ctx context.Context, format archive.Format, vols []archive.Volume, password string) ([]archive.Member, error) {
	switch format {
	case archive.FormatRAR4, archive.FormatRAR5:
		parsed := make([]*archive.RARVolume, len(vols))
		opts := &archive.RAROptions{
			Context:  ctx,
			Password: password,
		}
		for i, v := range vols {
			rv, err := archive.ReadRARVolume(v.R, v.Size, opts)
			if err != nil {
				return nil, fmt.Errorf("kindstream: read rar volume %d: %w", i, err)
			}
			parsed[i] = rv
		}
		return archive.ComposeRAR(parsed)

	case archive.Format7z:
		mr := archive.NewMultiReader(vols)
		members, err := archive.List7zWithPassword(ctx, mr, mr.Size(), password)
		if err != nil {
			return nil, err
		}
		for i := range members {
			if len(members[i].Extents) > 0 {
				exts, err := archive.SplitExtents(vols, members[i].Extents)
				if err != nil {
					return nil, err
				}
				members[i].Extents = exts
			}
		}
		return members, nil

	case archive.FormatZIP:
		members, err := archive.ListZIP(vols)
		if err != nil {
			return nil, err
		}
		for i := range members {
			if len(members[i].Extents) > 0 {
				exts, err := archive.SplitExtents(vols, members[i].Extents)
				if err != nil {
					return nil, err
				}
				members[i].Extents = exts
			}
		}
		return members, nil

	default:
		return nil, archive.ErrNotArchive
	}
}

func selectArchiveMember(members []archive.Member, wantName string, wantIndex *int) (archive.Member, int, error) {
	var nonDir []archive.Member
	for _, m := range members {
		if !m.Dir {
			nonDir = append(nonDir, m)
		}
	}
	if len(nonDir) == 0 {
		return archive.Member{}, -1, fmt.Errorf("kindstream: archive contains no non-directory members: %w", archive.ErrCorrupt)
	}

	wantName = normalizeArchivePath(wantName)
	if wantName != "" {
		for idx, m := range nonDir {
			if normalizeArchivePath(m.Name) == wantName {
				return m, idx, nil
			}
		}
		// Fallback to unique basename match when wantName has no directory separator.
		if !strings.Contains(wantName, "/") {
			matchIdx := -1
			for idx, m := range nonDir {
				if path.Base(normalizeArchivePath(m.Name)) == wantName {
					if matchIdx >= 0 {
						matchIdx = -2
						break
					}
					matchIdx = idx
				}
			}
			if matchIdx >= 0 {
				return nonDir[matchIdx], matchIdx, nil
			}
		}
		return archive.Member{}, -1, fmt.Errorf("kindstream: archive member %q not found", wantName)
	}

	if wantIndex != nil {
		idx := *wantIndex
		if idx < 0 || idx >= len(nonDir) {
			return archive.Member{}, -1, fmt.Errorf("kindstream: archive memberIndex %d out of range [0,%d)", idx, len(nonDir))
		}
		return nonDir[idx], idx, nil
	}

	return nonDir[0], 0, nil
}

func normalizeArchivePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

func memberDisplayName(p string) string {
	norm := normalizeArchivePath(p)
	base := path.Base(norm)
	if base == "" || base == "." || base == "/" {
		return "member"
	}
	return base
}

func containerExtractCacheKey(c core.ContainerRef, memberName string, nonDirIdx, itemIdx int) string {
	h := sha256.New()
	for _, v := range c.Volumes {
		_, _ = io.WriteString(h, v.BlobRef)
		_, _ = io.WriteString(h, "|")
		_, _ = io.WriteString(h, v.URL)
		_, _ = io.WriteString(h, "\n")
	}
	_, _ = fmt.Fprintf(h, "member=%s|idx=%d|item=%d", normalizeArchivePath(memberName), nonDirIdx, itemIdx)
	return "extract-" + hex.EncodeToString(h.Sum(nil))[:24] + "-" + memberDisplayName(memberName)
}

type containerMemberSource struct {
	r       *archive.MemberReader
	size    int64
	name    string
	mime    string
	closers []io.Closer
}

func (s *containerMemberSource) ReadAt(p []byte, off int64) (int, error) {
	return s.r.ReadAt(p, off)
}

func (s *containerMemberSource) Size() int64  { return s.size }
func (s *containerMemberSource) Name() string { return s.name }
func (s *containerMemberSource) MIME() string { return s.mime }

func (s *containerMemberSource) Close() error {
	var firstErr error
	for _, c := range s.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.closers = nil
	return firstErr
}
