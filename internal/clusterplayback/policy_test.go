package clusterplayback

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/plexdelivery"
	md "github.com/braedonsaunders/ether/internal/frontends/mediabrowser/deliverypolicy"
	"net/http"
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

func testPublicRepresentation(q *http.Request) bool {
	return IsPublicRepresentation(q, testDeliveryPolicy())
}

func testDeliveryPolicy() core.DeliveryPolicy {
	p := deliverypolicy.Policy()
	p.Route = md.DeliveryRoute
	return p
}
