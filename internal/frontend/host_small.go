package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

// Host configuration snapshots and quality rules exchanged across the
// frontend boundary.
type HostConfig = config.Config
type HostQualityConfig = config.Quality

// ServerIdentity describes how this server presents its machine and build
// identity. Empty fields fall back to HostConfig or runtime defaults.
type ServerIdentity struct {
	MachineID       string
	FriendlyName    string
	Version         string
	Platform        string
	PlatformVersion string
}

// Cluster coordination primitives used by protocol adapters when clustered.
type ClusterBus = clusterbus.Bus
type ClusterNode = clusterbus.Node

const (
	ClusterTopicResync    = clusterbus.TopicResync
	ClusterMaxPayload     = clusterbus.MaxPayload
	ClusterPublishTimeout = clusterbus.PublishTimeout
)

func NewClusterOrigin() string                         { return clusterbus.NewOrigin() }
func TagClusterMessage(origin string, b []byte) []byte { return clusterbus.Tag(origin, b) }
func UntagClusterMessage(msg []byte) (string, []byte)  { return clusterbus.Untag(msg) }
func StandaloneClusterNode(id string) ClusterNode      { return clusterbus.NewHub().Node(id) }

// Store helpers exposed across the frontend boundary.
func RegisterMigration(component string, version int, sql string) {
	store.Register(component, version, sql)
}

func DetachQueryStats(ctx context.Context) context.Context {
	return store.DetachQueryStats(ctx)
}

func SQLPlaceholders(n int) string {
	return store.Placeholders(n)
}
