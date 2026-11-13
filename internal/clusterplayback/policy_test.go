package clusterplayback

import (
	"github.com/braedonsaunders/ether/internal/core"
	md "github.com/braedonsaunders/ether/internal/frontends/mediabrowser/deliverypolicy"
	"github.com/braedonsaunders/ether/internal/frontends/plex/deliverypolicy"
	"net/http"
	"net/url"
)

func testSessionPolicy(p core.Protocol) core.SessionPolicy {
	switch p {
	case "plex":
		return core.SessionPolicy{ExternalAdmission: true}
	case "jellyfin":
		return core.SessionPolicy{DistributedBindings: true, PlainSourceGone: true}
	case "emby":
		return core.SessionPolicy{DistributedBindings: true, MasterMetadata: true}
	}
	return core.SessionPolicy{}
}

func adapterMediaSession(u *url.URL) (string, bool) { return deliverypolicy.MediaSession(u) }
func adapterGrantEnvelope(q *http.Request) bool     { return deliverypolicy.GrantEnvelope(q) }
func testPublicRepresentation(q *http.Request) bool {
	return IsPublicRepresentation(q, testDeliveryPolicy())
}

func testDeliveryPolicy() core.DeliveryPolicy {
	p := deliverypolicy.Policy()
	p.Route = md.DeliveryRoute
	return p
}
