package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"io"
	"net/http"
	"time"
)

const PreparedBytesPath = "/cluster/playback/prepared-bytes"
const preparedOperationHeader = "X-Ether-Prepared-Operation"

func (r *Runtime) preparationCurrent(c context.Context, peer nodewire.Identity, operation string) (PreparedSource, error) {
	key := peer.NodeID + "/" + peer.Boot + "/" + operation
	r.mu.Lock()
	entry := r.preparations[key]
	if entry == nil || entry.peer != peer || !entry.finished || entry.retiring {
		r.mu.Unlock()
		return PreparedSource{}, ErrDenied
	}
	result := entry.result
	viewerID := entry.viewer
	r.mu.Unlock()
	if r.o.MemberRole(c, peer, config.RoleServer) != nil || r.o.MemberRole(c, r.o.Self, config.RoleSource) != nil {
		return PreparedSource{}, ErrDenied
	}
	viewer, e := r.o.ActiveViewer(c, viewerID)
	ps, ok := r.o.Playback.Session(result.PlayID)
	if e != nil || viewer == nil || !ok || ps == nil || ps.UserID != viewerID || core.SourceRevision(ps) != result.Revision || r.o.SourcePolicy(c, viewer, ps) != nil {
		return PreparedSource{}, ErrDenied
	}
	return result, nil
}
func (r *Runtime) preparedBytes(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "GET" && q.Method != "HEAD" {
		http.Error(w, "method", 405)
		return
	}
	if len(q.Header.Values(preparedOperationHeader)) != 1 {
		http.Error(w, "denied", 401)
		return
	}
	result, e := r.preparationCurrent(q.Context(), peer, q.Header.Get(preparedOperationHeader))
	if e != nil {
		http.Error(w, "denied", 403)
		return
	}
	r.o.Playback.ServeRange(w, q, result.PlayID)
}
func (p *RemotePlayback) serveRange(w http.ResponseWriter, q *http.Request, id string) {
	p.mu.Lock()
	entry := p.entries[id]
	if entry == nil || entry.retiring || entry.retired || entry.snapshot == nil {
		p.mu.Unlock()
		http.NotFound(w, q)
		return
	}
	owner, operation := entry.owner, entry.operation
	p.mu.Unlock()
	ep, tc, check, e := p.r.peer(q.Context(), owner, PreparedBytesPath)
	if e != nil {
		http.Error(w, "source unavailable", 502)
		return
	}
	transport, e := outbound.New(ep, tc, 5*time.Second, check)
	if e != nil {
		http.Error(w, "source unavailable", 502)
		return
	}
	defer transport.CloseIdleConnections()
	request, e := http.NewRequestWithContext(q.Context(), q.Method, ep, nil)
	if e != nil {
		http.Error(w, "source unavailable", 502)
		return
	}
	request.Header.Set(preparedOperationHeader, operation)
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		request.Header[h] = append([]string(nil), q.Header.Values(h)...)
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		http.Error(w, "source unavailable", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != 304 {
		http.Error(w, "source unavailable", 502)
		return
	}
	for _, h := range deliveryHeaders {
		if values := response.Header.Values(h); len(values) > 0 {
			w.Header()[h] = values
		}
	}
	w.WriteHeader(response.StatusCode)
	if q.Method != "HEAD" {
		_, _ = io.Copy(w, response.Body)
	}
}
