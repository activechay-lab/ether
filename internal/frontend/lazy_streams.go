package frontend

import (
	"context"
	"net/http"
)

// LazyStreams issues reusable, short-lived playback capabilities without opening
// a playback session. The issuing credential never appears in the returned URL.
type LazyStreams interface {
	Issue(context.Context, string, PlanRequest) (string, error)
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// LazyStreamWarmer is optionally implemented by LazyStreams. Warm starts the
// resolve and probe that a later GET of the URL Issue returned for the same
// request joins, so a client that lists streams before the viewer picks one
// does not pay for resolution after the pick. It is best effort, never
// blocks on the resolve, and never opens a session or consumes a stream.
type LazyStreamWarmer interface {
	Warm(context.Context, string, PlanRequest)
}

// LazyHLSStreams issues an HLS master capability without opening a session.
// All playlist and segment requests remain under that capability and are
// reauthorized by the broker. IssueHLS alone is not an eligibility check;
// advertise it only when a resolved PlayPlan reports HLS. Capacity and the
// current source and policy are checked again when playback starts.
type LazyHLSStreams interface {
	IssueHLS(context.Context, string, PlanRequest, StreamRequest) (string, error)
}

// AuthenticatedLazyStreams is the optional WebDAV-style capability for a
// surface that authenticates each GET and HEAD instead of issuing a bearer URL.
type AuthenticatedLazyStreams interface {
	ServeAuthenticated(http.ResponseWriter, *http.Request, string, PlanRequest)
}

// LANLazyStreams issues a capability only for the currently selected DLNA
// viewer and verified network peer. The host derives the viewer and device;
// callers cannot assert either identity. Every use revalidates both.
type LANLazyStreams interface {
	IssueLAN(context.Context, *http.Request, PlanRequest) (string, error)
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// LineupStreams issues a reusable, media-only lineup capability. The issuer
// revalidates the source grant and channel policy on every media request.
// The returned /sessions/lineup/<token>/ prefix is followed by a canonical
// base64url channel ID and /live/index.m3u8.
type LineupStreams interface {
	IssueLineup(context.Context, string, PlanRequest) (string, error)
}
