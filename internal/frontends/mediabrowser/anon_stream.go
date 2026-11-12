package mediabrowser

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// anonSessionTTL bounds how long a credential-less stream may ride a session
// opened through PlaybackInfo. It is deliberately short: the index exists to
// carry the window between PlaybackInfo and the client's first ranged GET of
// the direct file, not to keep an anonymous grant alive for a whole movie.
const anonSessionTTL = 3 * time.Minute

// anonSessionCap bounds the in-memory index so a burst of PlaybackInfo calls
// cannot grow it without limit. Entries are evicted oldest-first.
const anonSessionCap = 1024

// anonEntry records a playback session an authenticated viewer opened, so a
// following credential-less stream request from the same peer for the same
// item can be served as that viewer. The viewer is stored by value: the
// session already belongs to it, and holding a pointer would share mutable
// state across requests.
type anonEntry struct {
	sessionID string
	peerAddr  netip.Addr
	item      frontend.RatingKey
	channel   string
	version   string
	detailSrc string
	// sourceID is the MediaSource Id PlaybackInfo returned (an Auto switch
	// returns a release handle the client then names as mediaSourceId).
	sourceID string
	viewer   frontend.Viewer
	device   frontend.Device
	expires  time.Time
	seq      uint64
}

// anonIndex is a bounded, per-instance map from (peer, item) to the most
// recent session opened for that item from that peer.
type anonIndex struct {
	mu      sync.Mutex
	entries map[anonKey][]anonEntry
	seq     uint64
}

type anonKey struct {
	peer netip.Addr
	item frontend.RatingKey
}

func newAnonIndex() *anonIndex {
	return &anonIndex{entries: map[anonKey][]anonEntry{}}
}

// remember stores one session, replacing any earlier entry for the same peer
// and item so a superseded session never wins. The index is pruned first.
func (x *anonIndex) remember(e anonEntry) {
	if !e.peerAddr.IsValid() || (e.item == 0 && e.channel == "") {
		return
	}
	now := time.Now()
	x.mu.Lock()
	defer x.mu.Unlock()
	x.pruneLocked(now)
	x.seq++
	e.seq = x.seq
	e.expires = now.Add(anonSessionTTL)
	key := anonKey{peer: e.peerAddr, item: e.item}
	kept := x.entries[key][:0]
	for _, existing := range x.entries[key] {
		if existing.channel == e.channel && existing.version == e.version && existing.detailSrc == e.detailSrc {
			continue
		}
		kept = append(kept, existing)
	}
	x.entries[key] = append(kept, e)
	x.trimLocked()
}

// lookup returns the newest unexpired entry for a peer and item.
func (x *anonIndex) lookup(peer netip.Addr, item frontend.RatingKey) (anonEntry, bool) {
	if !peer.IsValid() || item == 0 {
		return anonEntry{}, false
	}
	now := time.Now()
	x.mu.Lock()
	defer x.mu.Unlock()
	x.pruneLocked(now)
	live := x.entries[anonKey{peer: peer, item: item}]
	var best anonEntry
	found := false
	for _, e := range live {
		if !e.expires.After(now) {
			continue
		}
		if !found || e.seq > best.seq {
			best, found = e, true
		}
	}
	return best, found
}

func (x *anonIndex) pruneLocked(now time.Time) {
	for key, list := range x.entries {
		kept := list[:0]
		for _, e := range list {
			if e.expires.After(now) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(x.entries, key)
			continue
		}
		x.entries[key] = kept
	}
}

// trimLocked enforces the entry cap oldest-first.
func (x *anonIndex) trimLocked() {
	total := 0
	for _, list := range x.entries {
		total += len(list)
	}
	for total > anonSessionCap {
		var oldestKey anonKey
		oldestIdx, oldestSeq := -1, ^uint64(0)
		for key, list := range x.entries {
			for idx, e := range list {
				if e.seq < oldestSeq {
					oldestSeq, oldestKey, oldestIdx = e.seq, key, idx
				}
			}
		}
		if oldestIdx < 0 {
			return
		}
		list := x.entries[oldestKey]
		list = append(list[:oldestIdx], list[oldestIdx+1:]...)
		if len(list) == 0 {
			delete(x.entries, oldestKey)
		} else {
			x.entries[oldestKey] = list
		}
		total--
	}
}

// peerAddr returns the verified transport peer for a request, or an invalid
// address when the host cannot resolve one. Without a peer the anonymous
// stream path is disabled rather than binding an entry to every caller.
func (i *instance) peerAddr(r *http.Request) netip.Addr {
	if i.host.ClientIP == nil {
		return netip.Addr{}
	}
	if ip, ok := i.host.ClientIP(r); ok {
		return ip
	}
	return netip.Addr{}
}

// rememberAnonymousSession records a session an authenticated viewer just
// opened so a following credential-less stream from the same peer and item
// can be served as that viewer. Only item streams are indexed: a live
// channel is opened and read through the LiveStreams endpoints, and a
// credential-less channel request has no session to match against.
func (i *instance) rememberAnonymousSession(r *http.Request, viewer *frontend.Viewer, sessionID string, item frontend.RatingKey, channel string, device frontend.Device, version, detailSrc, sourceID string) {
	if i.anon == nil || viewer == nil || sessionID == "" || item == 0 {
		return
	}
	peer := i.peerAddr(r)
	if !peer.IsValid() {
		return
	}
	i.anon.remember(anonEntry{
		sessionID: sessionID, peerAddr: peer, item: item, channel: channel,
		version: version, detailSrc: detailSrc, sourceID: sourceID, viewer: *viewer,
		device: device,
	})
}

// serveAnonymousStream answers a credential-less stream alias by matching a
// session an authenticated viewer recently opened from the same peer for the
// same item. It reports whether it wrote a response; the caller keeps the
// 401 when it did not.
func (i *instance) serveAnonymousStream(w http.ResponseWriter, r *http.Request, name string) bool {
	if i.host.Sessions == nil || i.anon == nil {
		return false
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("id"))
	if err != nil {
		return false
	}
	format := frontend.StreamDirect
	switch {
	case name == "stream", strings.HasPrefix(name, "stream."), name == "universal":
	case name == "master.m3u8", name == "main.m3u8":
		format = frontend.StreamHLS
	default:
		return false
	}
	peer := i.peerAddr(r)
	entry, ok := i.anon.lookup(peer, key)
	if !ok {
		return false
	}
	// A mediaSourceId/version handle given on the anonymous URL must be the
	// one the session was opened for; nothing else is served.
	if sourceIDs := route.Query(r.URL.Query(), "MediaSourceId"); len(sourceIDs) > 1 {
		return false
	} else if len(sourceIDs) == 1 && sourceIDs[0] != "" && !sameItemID(sourceIDs[0], key) {
		if sourceIDs[0] != entry.sourceID && !sameVersionHandle(sourceIDs[0], entry.version) && !sameVersionHandle(sourceIDs[0], entry.detailSrc) {
			return false
		}
	}
	viewer := entry.viewer
	access := frontend.SessionAccess{Viewer: &viewer, Device: entry.device, Protocol: i.protocol}
	if access.Device.ID == "" {
		return false
	}
	sessions, err := i.host.Sessions.Sessions(r.Context(), access)
	if err != nil {
		return false
	}
	found := false
	for _, session := range sessions {
		if session.ID == entry.sessionID && session.Item == entry.item && session.Channel == entry.channel {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	versionScope, err := i.versionScope(r, &viewer)
	if err != nil {
		return false
	}
	i.serveAliasSession(w, r, access, sessions, entry.sessionID, versionScope, entry.version, entry.detailSrc, format)
	return true
}

// serveAliasSession serves one already-verified broker session: a direct file
// from its scoped media path, or an HLS start. The version/detail handles are
// re-checked against the session so a forged handle can never select it.
func (i *instance) serveAliasSession(w http.ResponseWriter, r *http.Request, access frontend.SessionAccess, sessions []frontend.Session, sessionID string, versionScope frontend.VersionScope, version, detailSource string, format frontend.StreamFormat) {
	if version != "" && i.host.VersionHandles != nil && i.host.VersionHandles.MatchSession(i.identityContext(r), versionScope, version, sessionID) != nil {
		http.NotFound(w, r)
		return
	}
	if detailSource != "" {
		planner, ok := i.host.Sessions.(frontend.DetailPlanner)
		if !ok || planner.MatchDetailSession(i.identityContext(r), versionScope, detailSource, sessionID) != nil {
			http.NotFound(w, r)
			return
		}
	}
	// PlaybackInfo already started this direct file. Serve that URL here so
	// the client does not pay a second round trip, or a second stream start.
	if format == frontend.StreamDirect {
		for _, session := range sessions {
			if session.ID != sessionID || !directFilePath(session.MediaPath) {
				continue
			}
			req := r.Clone(r.Context())
			u := *req.URL
			u.Path = session.MediaPath
			u.RawPath = ""
			req.URL = &u
			if i.protocol == "emby" {
				i.embyMediaHeaders(w, req)
			}
			i.host.Sessions.ServeMedia(w, req)
			return
		}
	}
	url, err := i.host.Sessions.StartStream(r.Context(), access, sessionID, frontend.StreamRequest{Format: format})
	if err != nil {
		streamError(i.protocol, w, err)
		return
	}
	if !strings.HasPrefix(url, "/sessions/media/") || strings.ContainsAny(url, "\r\n") {
		http.Error(w, "Unavailable", 503)
		return
	}
	if prefix, ok := r.Context().Value(prefixKey{}).(string); ok {
		url = prefix + url
	}
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// sameItemID reports whether a mediaSourceId names the item itself, in
// either GUID form: the Jellyfin Kotlin SDK (Findroid) puts the dashed form
// in the path and the bare form in mediaSourceId.
func sameItemID(sourceID string, key frontend.RatingKey) bool {
	parsed, err := ids.ParseRatingKeyGUID(sourceID)
	return err == nil && parsed == key
}
