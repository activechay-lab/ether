// Package dburl recognises and redacts database connection URLs. It is the
// one implementation behind config.Database.Redacted (logs, the admin config
// API) and the store's own logging, so a password is masked the same way
// everywhere whatever characters it holds.
package dburl

import (
	"net/url"
	"regexp"
	"strings"
)

// Mask replaces a removed password.
const Mask = "xxxxx"

var reQueryPassword = regexp.MustCompile(`(?i)((?:^|[?&])[^=&]*password=)[^&]*`)

// IsPostgres reports whether v is a postgres:// or postgresql:// URL.
func IsPostgres(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(v, "postgres://") || strings.HasPrefix(v, "postgresql://")
}

// Redact hides the password (and any *password query parameter) in a
// database URL so it can be logged or shown. Non-URL text that looks like a
// key=value DSN has its password values masked.
func Redact(raw string) string {
	if raw == "" {
		return ""
	}
	// A database URL has no fragment and no '@' outside its userinfo: either
	// means a password holding an unencoded '#', '/', '?' or '@' that
	// url.Parse split at the wrong place, leaving part or all of the
	// password outside the userinfo it would mask.
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Opaque == "" && !strings.Contains(raw, "#") &&
		!strings.Contains(u.Path+u.RawPath+u.RawQuery, "@") {
		if u.User != nil {
			if _, has := u.User.Password(); has {
				u.User = url.UserPassword(u.User.Username(), Mask)
			}
		}
		q := u.Query()
		changed := false
		for k := range q {
			if strings.Contains(strings.ToLower(k), "password") {
				q.Set(k, Mask)
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
		return u.String()
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		// Unparseable: mask everything between the user name's ':' and the
		// last '@', which may itself be part of the password.
		rest := raw[i+3:]
		if at := strings.LastIndexByte(rest, '@'); at >= 0 {
			if c := strings.IndexByte(rest[:at], ':'); c >= 0 && !strings.ContainsAny(rest[:c], "/?#@") {
				rest = rest[:c+1] + Mask + rest[at:]
			}
		}
		return raw[:i+3] + reQueryPassword.ReplaceAllString(rest, "${1}"+Mask)
	}
	fields := strings.Fields(raw)
	for i, f := range fields {
		if k, _, ok := strings.Cut(f, "="); ok && strings.Contains(strings.ToLower(k), "password") {
			fields[i] = k + "=" + Mask
		}
	}
	return strings.Join(fields, " ")
}
