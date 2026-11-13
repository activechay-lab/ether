package sessions

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (b *Broker) policy(protocol frontend.Protocol) core.SessionPolicy {
	if b != nil && b.deps.SurfacePolicy != nil {
		return b.deps.SurfacePolicy(protocol)
	}
	return core.SessionPolicy{}
}
func (b *Broker) neutralProtocol(protocol frontend.Protocol) bool {
	return b.policy(protocol).DistributedBindings
}
