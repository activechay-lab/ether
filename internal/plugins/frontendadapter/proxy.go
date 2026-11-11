package frontendadapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// instance is a mounted adapter: the proxy handler plus the per-mount
// limiters. It holds only Manager and identity; it cannot reach another
// frontend's Host seams.
type instance struct {
	frontend *Frontend
	auth     *authenticator
	handler  http.Handler
	limits   *limiters
}

var _ frontend.Instance = (*instance)(nil)

func (i *instance) Handler() http.Handler { return i.handler }

// Listeners places the adapter on the shared listener under its path
// prefix by default, or on its own port when defaultPort is set (spec
// §6.5). The host decides whether the owner enabled the port; the adapter
// only advertises what the manifest asked for.
func (i *instance) Listeners() []frontend.ListenerSpec {
	prefix := i.frontend.decl.Meta.PathPrefix
	if port := i.frontend.decl.Meta.DefaultPort; port > 0 {
		return []frontend.ListenerSpec{{Address: ":" + strconv.Itoa(port), PathPrefix: prefix, Shared: false}}
	}
	return []frontend.ListenerSpec{{PathPrefix: prefix, Shared: true}}
}

func (i *instance) Discovery() []frontend.Announcer { return nil }

func (i *instance) Status(context.Context) frontend.Status {
	return frontend.Status{Mounted: true}
}

func (i *instance) Close(context.Context) error { return nil }

// proxyHandler builds the HTTP handler that authenticates, rate-limits and
// relays each request to the plugin.
func (f *Frontend) proxyHandler(inst *instance) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, err := inst.auth.authenticate(r.Context(), r)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if !inst.limits.allow(auth.viewer.ID, time.Now()) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		req := plugins.Request{
			PluginID: f.decl.PluginID,
			Method:   r.Method,
			Path:     pluginPath(f.decl.Meta.Key, stripPrefix(r.URL.Path, f.decl.Meta.PathPrefix)),
			Query:    firstValues(r.URL.Query()),
			Headers:  outboundHeaders(r, auth),
			Body:     body,
		}
		resp, stream, err := f.call(r.Context(), req)
		if err != nil {
			writePluginError(w, err)
			return
		}
		if stream != nil {
			defer stream.Close()
			writeStream(w, resp, stream)
			return
		}
		writeResponse(w, resp)
	})
}

// call relays one request. It streams when the plugin runtime supports it
// and falls back to a buffered, size-capped Call for hosted plugins that
// serve small protocol documents (spec §6.5).
func (f *Frontend) call(ctx context.Context, req plugins.Request) (plugins.Response, io.ReadCloser, error) {
	resp, stream, err := f.mgr.CallStream(ctx, f.decl.PluginID, req)
	if err == nil {
		return resp, stream, nil
	}
	if !errors.Is(err, plugins.ErrStreamUnsupported) {
		return plugins.Response{}, nil, err
	}
	resp, err = f.mgr.Call(ctx, f.decl.PluginID, req)
	if err != nil {
		return plugins.Response{}, nil, err
	}
	if int64(len(resp.Body)) > MaxHostedResponseBytes {
		return plugins.Response{}, nil, errors.New("frontendadapter: hosted response exceeds cap")
	}
	return resp, nil, nil
}

// readBody reads an inbound request body, capping it at MaxRequestBodyBytes.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > MaxRequestBodyBytes {
		return nil, errors.New("request body too large")
	}
	return body, nil
}

// writeStream relays a streaming plugin response.
func writeStream(w http.ResponseWriter, resp plugins.Response, stream io.Reader) {
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	copyResponseHeaders(w, resp.Headers)
	w.WriteHeader(status)
	_, _ = io.Copy(w, stream)
}

// writeResponse relays a buffered plugin response.
func writeResponse(w http.ResponseWriter, resp plugins.Response) {
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	copyResponseHeaders(w, resp.Headers)
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}

// copyResponseHeaders copies the allowlisted plugin response headers.
func copyResponseHeaders(w http.ResponseWriter, headers map[string]string) {
	for _, name := range forwardedResponseHeaders {
		if v := headerValue(headers, name); v != "" {
			w.Header().Set(name, v)
		}
	}
}

// headerValue looks up a header case-insensitively.
func headerValue(headers map[string]string, name string) string {
	if v, ok := headers[name]; ok {
		return v
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// writeAuthError maps an authentication failure. A hidden library answers
// 404 (never 403) so its existence does not leak; missing credentials
// answer 401 with a Basic challenge so readers prompt for one.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotVisible):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		w.Header().Set("WWW-Authenticate", `Basic realm="Ether"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// writePluginError maps a plugin-call failure to a gateway error.
func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugins.ErrDisabled):
		http.Error(w, "adapter disabled", http.StatusServiceUnavailable)
	default:
		http.Error(w, "adapter unavailable", http.StatusBadGateway)
	}
}

// stripPrefix removes the adapter's path prefix so the plugin sees the
// path it declared. A request outside the prefix keeps its full path.
func stripPrefix(path, prefix string) string {
	if prefix != "" && strings.HasPrefix(path, prefix) {
		rest := strings.TrimPrefix(path, prefix)
		if rest == "" {
			return "/"
		}
		return rest
	}
	return path
}

// firstValues flattens a query into the map[string]string the plugin
// Request carries, keeping the first value per key.
func firstValues(values map[string][]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
