package clusterplayback

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const deliveryOrigin = "X-Ether-Delivery-Origin"

var proxyHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Host", "X-Forwarded-Proto"}

type requestOrigin struct {
	RemoteAddr, Host, Scheme string
	Proxy                    http.Header
}

func validOrigin(o requestOrigin) bool {
	if len(o.RemoteAddr) > 256 || len(o.Host) > 255 || o.Host == "" || (o.Scheme != "http" && o.Scheme != "https") {
		return false
	}
	host, port, e := net.SplitHostPort(o.RemoteAddr)
	if e != nil {
		return false
	}
	if _, e = netip.ParseAddr(host); e != nil {
		return false
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 0 || n > 65535 {
		return false
	}
	u, e := url.Parse(o.Scheme + "://" + o.Host)
	if e != nil || u.Host != o.Host || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(o.Host, "\r\n\t ") {
		return false
	}
	for h, values := range o.Proxy {
		allowed := false
		for _, known := range proxyHeaders {
			if http.CanonicalHeaderKey(h) == http.CanonicalHeaderKey(known) {
				allowed = true
				break
			}
		}
		if !allowed || len(values) > 16 {
			return false
		}
		for _, v := range values {
			if len(v) > 2048 || strings.ContainsAny(v, "\r\n\x00") {
				return false
			}
		}
	}
	return true
}
func captureOrigin(q *http.Request) (string, error) {
	scheme := "http"
	if q.TLS != nil {
		scheme = "https"
	}
	o := requestOrigin{RemoteAddr: q.RemoteAddr, Host: q.Host, Scheme: scheme, Proxy: http.Header{}}
	for _, h := range proxyHeaders {
		if values := q.Header.Values(h); len(values) > 0 {
			o.Proxy[h] = append([]string(nil), values...)
		}
	}
	if !validOrigin(o) {
		return "", ErrDenied
	}
	raw, e := json.Marshal(o)
	if e != nil || len(raw) > 4096 {
		return "", ErrDenied
	}
	return string(raw), nil
}

// restoreOrigin runs only AFTER private peer/role, exact owner and media scope
// checks. Public callers never select provenance: the sender overwrites it from
// net/http socket facts. Existing trusted-proxy classification is reapplied.
func restoreOrigin(q *http.Request, u *url.URL, rawURI string) (*http.Request, error) {
	values := q.Header.Values(deliveryOrigin)
	if len(values) != 1 || len(values[0]) > 4096 {
		return nil, ErrDenied
	}
	var origin requestOrigin
	if json.Unmarshal([]byte(values[0]), &origin) != nil || !validOrigin(origin) {
		return nil, ErrDenied
	}
	clone := q.Clone(q.Context())
	clone.URL = u
	clone.RequestURI = rawURI
	clone.Host = origin.Host
	clone.RemoteAddr = origin.RemoteAddr
	clone.URL.Scheme = origin.Scheme
	clone.URL.Host = origin.Host
	if origin.Scheme == "http" {
		clone.TLS = nil
	}
	for _, h := range proxyHeaders {
		clone.Header.Del(h)
	}
	for h, values := range origin.Proxy {
		clone.Header[h] = append([]string(nil), values...)
	}
	clone.Header.Del(deliveryOrigin)
	clone.Header.Del(deliveryURI)
	clone.Header.Del(deliveryProtocol)
	return clone, nil
}

func (r *Runtime) mediaPeer(ctx context.Context, id nodewire.Identity) bool {
	return r.o.MemberRole(ctx, id, config.RoleDelivery) == nil || r.o.MemberRole(ctx, id, config.RoleServer) == nil
}
