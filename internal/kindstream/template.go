package kindstream

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var (
	// ErrInvalidTemplate is returned when a URL template or secret placeholder
	// is malformed or expands to a disallowed URL.
	ErrInvalidTemplate = errors.New("kindstream: invalid URL template")
)

// TemplateVars holds the integer values allowed in a URL template (spec §4.2).
// Values are strictly non-negative integers; arbitrary string interpolation is
// never performed.
type TemplateVars struct {
	ItemIndex int
	ByteStart int64
	ByteEnd   int64
	HasRange  bool
}

// URLTemplate is a parsed and validated URL template.
type URLTemplate struct {
	raw          string
	hasItemIdx   bool
	hasPageNum   bool
	hasByteStart bool
	hasByteEnd   bool
	hasByteRange bool
}

// ParseURLTemplate validates that raw contains only known placeholders
// ({itemIndex}, {pageNumber}, {byteStart}, {byteEnd}, {byteRange}) and that a
// trial expansion with zero values yields a valid http/https URL with a host.
func ParseURLTemplate(raw string, allowHost func(host string) bool) (*URLTemplate, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty URL", ErrInvalidTemplate)
	}
	t := &URLTemplate{raw: raw}

	rest := raw
	for {
		openIdx := strings.IndexByte(rest, '{')
		if openIdx < 0 {
			if strings.IndexByte(rest, '}') >= 0 {
				return nil, fmt.Errorf("%w: unmatched '}' in %q", ErrInvalidTemplate, raw)
			}
			break
		}
		if strings.IndexByte(rest[:openIdx], '}') >= 0 {
			return nil, fmt.Errorf("%w: unmatched '}' in %q", ErrInvalidTemplate, raw)
		}
		afterOpen := rest[openIdx+1:]
		closeIdx := strings.IndexByte(afterOpen, '}')
		if closeIdx < 0 {
			return nil, fmt.Errorf("%w: unclosed '{' in %q", ErrInvalidTemplate, raw)
		}
		token := afterOpen[:closeIdx]
		switch token {
		case "itemIndex":
			t.hasItemIdx = true
		case "pageNumber":
			t.hasPageNum = true
		case "byteStart":
			t.hasByteStart = true
		case "byteEnd":
			t.hasByteEnd = true
		case "byteRange":
			t.hasByteRange = true
		default:
			return nil, fmt.Errorf("%w: unknown placeholder {%s}", ErrInvalidTemplate, token)
		}
		rest = afterOpen[closeIdx+1:]
	}

	// Trial-expand with zero values to verify URL structure and host allowlist.
	if _, err := t.Expand(TemplateVars{ItemIndex: 0, ByteStart: 0, ByteEnd: 0, HasRange: true}, allowHost); err != nil {
		return nil, err
	}
	return t, nil
}

// HasByteRange reports whether the template embeds a bounded byte range
// ({byteRange} or both {byteStart} and {byteEnd}) in the URL itself.
func (t *URLTemplate) HasByteRange() bool {
	return t.hasByteRange || (t.hasByteStart && t.hasByteEnd)
}

// HasByteStartOnly reports whether the template embeds {byteStart} without an
// end byte, indicating an upstream that seeks from {byteStart} without a
// Range request header (spec §4.2).
func (t *URLTemplate) HasByteStartOnly() bool {
	return t.hasByteStart && !t.hasByteEnd && !t.hasByteRange
}

// UsesRangePlaceholders reports whether any byte-offset placeholder is present.
func (t *URLTemplate) UsesRangePlaceholders() bool {
	return t.hasByteStart || t.hasByteEnd || t.hasByteRange
}

// Expand substitutes integer placeholders and validates the resulting URL's
// scheme (http/https), non-empty host, and optional host allowlist.
func (t *URLTemplate) Expand(v TemplateVars, allowHost func(host string) bool) (string, error) {
	if v.ItemIndex < 0 {
		return "", fmt.Errorf("%w: negative itemIndex %d", ErrInvalidTemplate, v.ItemIndex)
	}
	if v.HasRange && (v.ByteStart < 0 || v.ByteEnd < v.ByteStart) {
		return "", fmt.Errorf("%w: invalid byte range %d-%d", ErrInvalidTemplate, v.ByteStart, v.ByteEnd)
	}
	s := t.raw
	if t.hasItemIdx {
		s = strings.ReplaceAll(s, "{itemIndex}", strconv.Itoa(v.ItemIndex))
	}
	if t.hasPageNum {
		s = strings.ReplaceAll(s, "{pageNumber}", strconv.Itoa(v.ItemIndex+1))
	}
	if t.hasByteStart {
		s = strings.ReplaceAll(s, "{byteStart}", strconv.FormatInt(v.ByteStart, 10))
	}
	if t.hasByteEnd {
		s = strings.ReplaceAll(s, "{byteEnd}", strconv.FormatInt(v.ByteEnd, 10))
	}
	if t.hasByteRange {
		r := strconv.FormatInt(v.ByteStart, 10) + "-" + strconv.FormatInt(v.ByteEnd, 10)
		s = strings.ReplaceAll(s, "{byteRange}", r)
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: parse expanded URL: %v", ErrInvalidTemplate, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: disallowed scheme %q", ErrInvalidTemplate, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidTemplate)
	}
	if allowHost != nil && !allowHost(host) {
		return "", fmt.Errorf("%w: host %q", ErrHostNotAllowed, host)
	}
	return u.String(), nil
}

// ExpandHeaders resolves {secret:name} placeholders in header values via
// secretFn. Resolved values must never be logged by callers.
func ExpandHeaders(ctx context.Context, headers map[string]string, secretFn func(ctx context.Context, name string) (string, error)) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		resolved, err := expandSecretString(ctx, v, secretFn)
		if err != nil {
			return nil, fmt.Errorf("kindstream: header %q: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

func expandSecretString(ctx context.Context, s string, secretFn func(ctx context.Context, name string) (string, error)) (string, error) {
	if !strings.Contains(s, "{secret:") {
		return s, nil
	}
	var b strings.Builder
	rest := s
	for {
		idx := strings.Index(rest, "{secret:")
		if idx < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:idx])
		after := rest[idx+len("{secret:"):]
		end := strings.IndexByte(after, '}')
		if end < 0 {
			return "", fmt.Errorf("%w: unclosed {secret:...} placeholder", ErrInvalidTemplate)
		}
		name := strings.TrimSpace(after[:end])
		if name == "" {
			return "", fmt.Errorf("%w: empty secret name", ErrInvalidTemplate)
		}
		if secretFn == nil {
			return "", fmt.Errorf("kindstream: secret %q requested but no secret provider configured", name)
		}
		val, err := secretFn(ctx, name)
		if err != nil {
			return "", fmt.Errorf("kindstream: resolve secret %q: %w", name, err)
		}
		b.WriteString(val)
		rest = after[end+1:]
	}
	return b.String(), nil
}
