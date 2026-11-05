// Package sourceadmin provides the private owner source-console protocol. It
// starts no workers and never accepts provider configuration or target URLs.
package sourceadmin

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire"
)

const Path = "/cluster/runtime/source-admin"
const credentialHeader = "X-Ether-Source-Owner"
const MaxItems = 100
const maxRequest = 4096
const maxResponse = 128 * 1024

var (
	ErrInvalid     = errors.New("sourceadmin: invalid request")
	ErrDenied      = errors.New("sourceadmin: denied")
	ErrUnsupported = errors.New("sourceadmin: unsupported")
	ErrUnavailable = errors.New("sourceadmin: unavailable")
)

type Operation string

const (
	Snapshot       Operation = "snapshot"
	TestProvider   Operation = "test-provider"
	TestIndexer    Operation = "test-indexer"
	TestDebrid     Operation = "test-debrid"
	Traces         Operation = "traces"
	CacheStatus    Operation = "cache-status"
	ReleaseHistory Operation = "release-history"
	Variants       Operation = "variants"
)

// Query is deliberately not a generic payload: no host, path, headers, config,
// credential, viewer identity, or arbitrary search terms can cross this boundary.
type Query struct {
	Limit   int   `json:"limit"`
	TitleID int64 `json:"titleID,omitempty"`
}
type Request struct {
	Operation  Operation `json:"operation"`
	RequestID  string    `json:"requestID"`
	ResourceID string    `json:"resourceID,omitempty"`
	Payload    *Query    `json:"payload,omitempty"`
}

// Status and every other textual response field are closed enums. Real service
// adapters must project existing diagnostics, never copy raw errors/config/logs.
type Status string

const (
	Healthy Status = "healthy"
	Backoff Status = "backoff"
	Failed  Status = "failed"
	Unknown Status = "unknown"
)

type Provider struct {
	// Slot indexes the CURRENT shared-config list, not an arbitrary label/host.
	Slot              int    `json:"slot"`
	State             Status `json:"state"`
	MaxConnections    int    `json:"maxConnections"`
	LiveConnections   int    `json:"liveConnections"`
	ActiveConnections int    `json:"activeConnections"`
	NodeShare         int    `json:"nodeShare"`
}
type Indexer struct {
	Slot           int   `json:"slot"`
	HitsUsed       int   `json:"hitsUsed"`
	HitsRemaining  int   `json:"hitsRemaining"`
	GrabsUsed      int   `json:"grabsUsed"`
	GrabsRemaining int   `json:"grabsRemaining"`
	RetryAt        int64 `json:"retryAt"`
	Enabled        bool  `json:"enabled"`
	Torrent        bool  `json:"torrent"`
}
type Cache struct {
	Used     int64 `json:"used"`
	Budget   int64 `json:"budget"`
	Hits     int64 `json:"hits"`
	HotBytes int64 `json:"hotBytes"`
}
type Stream struct {
	Fetches       int64 `json:"fetches"`
	FetchedBytes  int64 `json:"fetchedBytes"`
	Missing       int64 `json:"missing"`
	ActiveSources int64 `json:"activeSources"`
	OpenReleases  int   `json:"openReleases"`
}
type Protocol string

const (
	Usenet  Protocol = "usenet"
	Torrent Protocol = "torrent"
)

type SourceStartup struct {
	Protocol      Protocol `json:"protocol"`
	AverageMillis int64    `json:"averageMillis"`
	LastMillis    int64    `json:"lastMillis"`
	Samples       int64    `json:"samples"`
}
type Diagnostics struct {
	Providers []Provider      `json:"providers"`
	Indexers  []Indexer       `json:"indexers"`
	Cache     *Cache          `json:"cache,omitempty"`
	Stream    *Stream         `json:"stream,omitempty"`
	Sources   []SourceStartup `json:"sources,omitempty"`
}

// Trace excludes raw candidate names, URLs, routing descriptions and errors.
type Trace struct {
	TitleID           int64  `json:"titleID"`
	AtUnixMilli       int64  `json:"atUnixMilli"`
	State             Status `json:"state"`
	Candidates        int    `json:"candidates"`
	SearchMillis      int64  `json:"searchMillis"`
	PreparationMillis int64  `json:"preparationMillis"`
	TotalMillis       int64  `json:"totalMillis"`
	Failover          bool   `json:"failover"`
	Pinned            bool   `json:"pinned"`
}
type Release struct {
	HistoryID   int64  `json:"historyID,omitempty"`
	TitleID     int64  `json:"titleID"`
	AtUnixMilli int64  `json:"atUnixMilli,omitempty"`
	Size        int64  `json:"size"`
	Height      int    `json:"height,omitempty"`
	State       Status `json:"state"`
	Pinned      bool   `json:"pinned"`
	Automatic   bool   `json:"automatic"`
}
type Response struct {
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`
	Cache       *Cache       `json:"cache,omitempty"`
	Traces      []Trace      `json:"traces,omitempty"`
	Releases    []Release    `json:"releases,omitempty"`
	Tested      bool         `json:"tested,omitempty"`
}

// Services are real, cancellation-aware operations. Test callbacks resolve the
// ID against CURRENT shared config at invocation, never a cached config snapshot.
// Nil means unsupported. Callbacks must not substitute successful dummy results.
type Services struct {
	Snapshot       func(context.Context, int) (Diagnostics, error)
	TestProvider   func(context.Context, string) error
	TestIndexer    func(context.Context, string) error
	TestDebrid     func(context.Context, string) error
	Traces         func(context.Context, Query) ([]Trace, error)
	CacheStatus    func(context.Context, string) (Cache, error)
	ReleaseHistory func(context.Context, Query) ([]Release, error)
	Variants       func(context.Context, Query) ([]Release, error)
}

type Options struct {
	Guard *nodewire.Guard
	Local nodewire.Identity
	// Authorize MUST freshly check exact caller boot/node has server role,
	// local boot/node has source role, and the operation's source/resource
	// remains eligible in current shared config. Unavailable policy denies.
	Authorize func(context.Context, nodewire.Identity, nodewire.Identity, Request) error
	// VerifyOwner independently checks the ORIGINAL opaque credential with
	// real current owner authority. No delegated/viewer/setup bypass is valid.
	VerifyOwner func(context.Context, string) error
	Services    Services
	Timeout     time.Duration
}
