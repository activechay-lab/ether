package mediabrowser

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// lazyHLSSourceURL accepts only the broker's scoped master capability. The
// caller must first establish resolved HLS eligibility; issuance reserves no
// encoder capacity, and media requests must stay on the encoder's node.
func (i *instance) lazyHLSSourceURL(r *http.Request, credential string, plan frontend.PlanRequest) string {
	issuer, ok := i.host.LazyStreams.(frontend.LazyHLSStreams)
	if !ok || i.host.SurfaceURL == nil || credential == "" {
		return ""
	}
	base, err := i.host.SurfaceURL(i.identityContext(r), r)
	if err != nil {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	path, err := issuer.IssueHLS(i.identityContext(r), credential, plan, frontend.StreamRequest{Format: frontend.StreamHLS, Position: plan.Intent.Position})
	if err != nil {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "sessions" || parts[2] != "lazy" || !embySafeGrant(parts[3]) || parts[4] != "hls" || parts[5] != "master.m3u8" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}
