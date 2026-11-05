// Package remotetranscode provides an opt-in private HLS execution foundation.
// It installs no routes and changes no local transcoder behavior.
package remotetranscode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const MaxMessage = 64 << 10
const Version = 1

var ErrUnsupported = errors.New("remotetranscode: unsupported capability")
var ErrUncertain = errors.New("remotetranscode: execution responsibility requires reconciliation")

// Capabilities must be consulted by placement before choosing this adapter.
// Segment seeks use the backend's ordinary ServeSegment behavior. Source swaps,
// probe, subtitles, DASH, HTTP Matroska, ABR and optional session controls are not supported.
type Capabilities struct{ HLS, SegmentSeek, DASH, HTTP, Probe, SourceSwap, Subtitles, ABR bool }

func Supported() Capabilities { return Capabilities{HLS: true, SegmentSeek: true} }

// Identity is assigned by trusted placement, never by a public request decoder.
// Controller and Worker are boot-scoped identities, not stable node names alone.
type Identity struct {
	Controller string
	Worker     string
	Session    string
	Generation uint64
}
type Start struct {
	ReleaseID        string
	Identity         Identity
	Grant            string
	Expires          time.Time
	Info             core.MediaInfo
	Decision         core.Decision
	Offset           time.Duration
	SegmentDuration  time.Duration
	IndependentProof bool
}
type message struct {
	Version  int
	Head     bool `json:",omitempty"`
	Op       string
	Identity Identity
	Start    *Start `json:",omitempty"`
	Segment  int    `json:",omitempty"`
}
type status struct {
	State    string
	Identity Identity
}

// Security authenticates mTLS peer identity and authorizes the complete operation.
// Authorize must check current placement/source authority, generation and expiry;
// an authenticated certificate alone does not authorize work. Called for reads too.
type Security struct {
	Authenticate func(*http.Request) (string, error)
	Authorize    func(context.Context, string, string, Identity, *Start) error
}

// Source is produced by trusted source-grant validation, never by a wire URL.
// URL must be a private literal-IP HTTPS range endpoint. Resolver must ensure no
// redirects, nested fetches, or scope/header escape and bind grant to this exact
// worker/controller/session/generation/expiry. Lease tracks worker input producers.
type Source struct {
	URL   string
	Lease core.EncoderInputLease
}
type ResolveSource func(context.Context, string, Start) (Source, error)

func validIdentity(i Identity) bool {
	return i.Generation > 0 && token(i.Controller) && token(i.Worker) && token(i.Session)
}
func token(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func privateURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	ip, e := netip.ParseAddr(u.Hostname())
	return e == nil && ip.IsPrivate() && !ip.IsLoopback() && !ip.Is4In6() && !strings.Contains(u.Host, "%")
}
func supported(r core.HLSRequest) bool {
	if r.Protocol != "" && r.Protocol != core.ProtocolHLS {
		return false
	}
	if len(r.ReleaseID) > 512 {
		return false
	}
	if r.InputLease != nil || len(r.InputHeaders) > 0 || r.LiveInput || r.CopyTimestamps || len(r.SlateText) > 0 || r.SoftSubtitleStream != nil || len(r.Info.Keyframes) > 0 {
		return false
	}
	b, err := json.Marshal(Start{ReleaseID: r.ReleaseID, Info: r.Info, Decision: r.Decision, Offset: r.StartOffset, SegmentDuration: r.SegmentDuration, IndependentProof: r.RequireIndependentProof})
	if err != nil || len(b) > MaxMessage-8192 {
		return false
	}
	return validPlan(r.Info, r.Decision, r.StartOffset, r.SegmentDuration)
}
func validPlan(info core.MediaInfo, d core.Decision, offset, segment time.Duration) bool {
	if len(info.Streams) > 64 || len(info.Chapters) > 128 || offset < 0 || segment < 0 || segment > time.Minute || info.Duration < 0 {
		return false
	}
	if d.Kind != core.DecisionRemux && d.Kind != core.DecisionTranscode {
		return false
	}
	if d.BurnSubtitles || d.SubtitleStream >= 0 || len(d.AudioFilters) > 0 || d.Enhancement != (core.VideoEnhancement{}) {
		return false
	}
	for _, s := range info.Streams {
		if s.External || s.ExternalURL != "" {
			return false
		}
	}
	return true
}
