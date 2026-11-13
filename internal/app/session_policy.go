package app

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	md "github.com/braedonsaunders/ether/internal/frontends/mediabrowser/deliverypolicy"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	pd "github.com/braedonsaunders/ether/internal/frontends/plex/deliverypolicy"
)

func sessionPolicy(protocol core.Protocol) core.SessionPolicy {
	switch protocol {
	case "plex":
		return plex.SessionPolicy()
	case "jellyfin", "emby":
		return mediabrowser.SessionPolicy(protocol)
	}
	return core.SessionPolicy{}
}

func deliveryPolicy() core.DeliveryPolicy { p := pd.Policy(); p.Route = md.DeliveryRoute; return p }
