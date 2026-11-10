// Package plex is Ether's Plex Media Server front end: the HTTP API that
// official Plex apps (Web, iOS, Apple TV, Android, TVs) talk to. Everything
// behind it is reached through internal/frontend host interfaces.
package plex

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// DefaultPMSVersion is reported when Identity.Version is empty. Clients gate
// features on the server version, so it must look like a current PMS build.
const DefaultPMSVersion = "1.43.4.10903-e5521bd8c"

// Identity is how the server presents itself. Empty fields fall back to the
// config snapshot (friendly name, machine identifier) or sensible defaults.
type Identity struct {
	MachineIdentifier  string
	FriendlyName       string
	Version            string
	Platform           string
	PlatformVersion    string
	MyPlexSubscription bool
	OwnerFeatures      []string
}

// Section is a library section exposed to clients.
type Section struct {
	ID       int
	Type     frontend.MediaType // movie or show
	Title    string
	UUID     string // stable; derived from the machine identifier when empty
	Language string
}

// SessionToucher is optionally implemented by frontend.Playback to keep a play
// session alive on timeline updates.
type SessionToucher interface {
	Touch(sessionID string)
}

// Deps are the server's dependencies. Host.Experience, Host.Identity and Host.Sessions are
// required; the rest degrade gracefully when nil.
type Deps struct {
	Host       frontend.Host
	Playback   frontend.Playback
	Transcoder frontend.Transcoder

	// Sections overrides the default Movies (1) and TV Shows (2) sections.
	Sections []Section
	// DB persists artwork data and ordered shelf-generation keys. Clustered
	// nodes share this store so continuations survive failover and restarts.
	// Optional: without it, these caches remain process-local.
	DB *sql.DB
	// DisableEnrichment turns off the background title-detail and
	// UltraBlurColors work (the spike test library has nothing to fetch).
	DisableEnrichment bool
	// TracePlayback logs HLS/transcode requests when ETHER_TRACE_PLAYBACK=1.
	// It does not change responses.
	TracePlayback bool
}

// Server is the PMS API handler. It is an http.Handler; call Close on
// shutdown to stop background work and notification streams.
type Server struct {
	activityMu       sync.Mutex
	activities       map[string]model.Activity
	shelfMu          sync.Mutex
	shelfCuts        map[string]shelfCut
	shelfGenerations map[string]shelfGeneration
	shelfBytes       int
	shelfSerial      uint64
	shelfWriter      *shelfPersistence
	plexEncoderMu    sync.RWMutex
	plexEncoder      SegmentedEncoder
	homeSharedMu     sync.Mutex
	homeShared       map[string]*homeSharedEntry
	shelfRefMu       sync.Mutex
	shelfRefs        map[string]shelfRef
	shelfRefRebuilt  time.Time
	shelfRebuild     chan struct{}
	guidResolveMu    sync.Mutex
	liveLogoKeyOnce  sync.Once
	liveLogoKey      []byte
	guidResolving    map[string]bool
	home             *homeCache

	d               Deps
	host            frontend.Host
	runtime         frontend.SessionRuntime
	streamRuntime   frontend.SessionRuntime
	initPlayback    frontend.Playback
	initTranscoder  frontend.Transcoder
	hostLazyStreams frontend.LazyStreams
	hostLANStreams  frontend.LANLazyStreams
	hostTickets     frontend.Tickets
	experience      frontend.Experience
	personalHomes   frontend.PersonalHomeStore
	pageFreshness   func() frontend.PageFreshness
	log             *slog.Logger
	router          chi.Router

	sections []Section

	artwork          frontend.Artwork
	hostImages       frontend.Images
	imageGrants      frontend.ImageGrants
	events           frontend.Subscriber
	imageURL         frontend.ImageURLFunc
	generatedPosters frontend.TitlePosterRenderer
	liveTV           frontend.LiveTV
	support          frontend.DiagnosticRecorder
	clientSeen       func(string)
	remoteClientSeen func(string)

	images            frontend.ArtworkImageCache
	channelLogos      frontend.UntrustedImageFetcher
	channelRaster     frontend.SVGRasterizer
	trackPrefs        frontend.PlaybackTrackPreferences
	markers           frontend.MarkerRanges
	trickplay         frontend.TrickplayProvider
	extras            frontend.ExtraStreams
	prerolls          frontend.Prerolls
	subtitles         frontend.SubtitleCatalog
	variants          frontend.Editions
	plexGUID          frontend.LibraryGUIDs
	plexMeta          frontend.LibraryMetadata
	variantIDs        *variantIDRegistry
	playIDs           *playIdentities
	versionFlows      *versionFlowStore
	variantWatch      variantWatches
	downloads         frontend.DownloadQueueStore
	queues            frontend.PlayQueues
	sessions          *sessionActivities
	playbackBroker    frontend.Sessions
	playbackOwners    frontend.PlaybackOwners
	playbackLifecycle frontend.Lifecycle
	transcodes        *transcodeTable
	progressiveStops  progressiveStopGuard
	readCaps          *readCapabilityStore
	extraPlay         *extraPlayStore
	subtitleOffers    subtitleOffers
	subtitlePicks     subtitlePicks
	selections        *selectionStore
	notifier          *notifier
	facets            *facetCache
	cl                *clusterState // nil on a standalone server

	// Background enrichment (nil when disabled).
	titles    *titleCache
	ultra     frontend.ArtworkPaletteCache
	titleJobs *bgQueue
	blurJobs  *bgQueue

	// Owner stream kills, delivered on the client's next timeline; the
	// janitor drops those whose client never reports again.
	termMu       sync.Mutex
	terms        map[string]termination
	decisionArms frontend.DecisionArms

	// Stalled-start watches and Dolby Vision base-layer fallbacks.
	starts frontend.StartGuard

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// NewServer builds the PMS API handler.
func NewServer(d Deps) *Server {
	host := d.Host
	if host.TracePlayback {
		d.TracePlayback = true
	}
	log := host.Log
	if log == nil {
		log = slog.Default()
	}
	httpClient := host.MetadataHTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt, ok := host.Sessions.(frontend.SessionRuntime)
	if !ok || rt == nil {
		panic("plex: Host.Sessions (implementing frontend.SessionRuntime) is required")
	}
	spikeEnabled := host.HostConfig != nil && host.HostConfig().Spike.Enabled
	db := d.DB
	if db == nil && !spikeEnabled {
		db = rt.DB()
	}
	s := &Server{
		activities:        map[string]model.Activity{},
		d:                 d,
		host:              host,
		runtime:           rt,
		streamRuntime:     rt,
		initPlayback:      d.Playback,
		initTranscoder:    d.Transcoder,
		log:               log.With("component", "plex"),
		imageURL:          func(path, size string) string { return "https://image.tmdb.org/t/p/" + size + path },
		images:            frontend.NewArtworkRemoteCache(128<<20, httpClient),
		channelLogos:      frontend.NewUntrustedImageFetcher(nil),
		channelRaster:     frontend.NewSVGRasterizer(),
		variantIDs:        newVariantIDRegistry(rt, db),
		playIDs:           newPlayIdentities(),
		downloads:         rt.NewDownloadQueueStore(),
		queues:            rt.NewPlayQueues(),
		sessions:          newSessionActivities(),
		playbackOwners:    rt.PlaybackOwners(),
		playbackLifecycle: rt.LifecycleGates(),
		transcodes:        newTranscodeTable(),
		readCaps:          newReadCapabilityStore(),
		extraPlay:         newExtraPlayStore(newDirectHTTPClient(host.DirectProxy)),
		selections:        newSelectionStore(),
		notifier:          newNotifier(),
		facets:            newFacetCache(),
		ctx:               ctx,
		cancel:            cancel,
	}
	s.usePlaybackBroker(host.Sessions)
	s.shelfWriter = &shelfPersistence{pending: map[string]shelfWrite{}, active: map[string]bool{}, wake: make(chan struct{}, 1)}
	s.wg.Add(1)
	go s.writeShelves()
	s.variantIDs.start(ctx, &s.wg)
	s.transcodes.load = s.loadDistributedBinding
	s.sections = s.buildSections()
	s.queues.Configure(s.now, nil)
	cluster := s.clusterNode()
	if db != nil && cluster == nil {
		// Standalone queues share the durable store without starting cluster sessions.
		s.queues.Configure(s.now, newClusterState(s, db, frontend.StandaloneClusterNode("standalone-queues")).queuePersistence())
	}
	if cluster != nil && db != nil {
		s.cl = newClusterState(s, db, cluster)
		s.versionFlows = newVersionFlowStore(s, db)
		s.queues.Configure(s.now, s.cl.queuePersistence())
	}
	s.bindHost(host)
	node := "standalone"
	if s.cl != nil {
		node = s.cl.self
	}
	s.starts = rt.NewStartGuard()
	s.decisionArms = rt.NewDecisionArms(s.ctx, db, s.now, node, s.log, &s.wg)
	s.clearOwnDecisionArms()
	s.loadDownloads()
	s.wg.Add(1)
	go s.writeDownloads(s.downloadPersistence())
	s.scheduleDownloads()
	s.router = s.routes()
	s.wg.Add(1)
	go s.janitor()
	if s.cl != nil {
		s.cl.start()
	}
	return s
}

func newDirectHTTPClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if proxy == nil {
		return nil
	}
	if tr, ok := http.DefaultTransport.(*http.Transport); ok && tr != nil {
		cloned := tr.Clone()
		cloned.Proxy = proxy
		return &http.Client{Transport: cloned}
	}
	return &http.Client{Transport: &http.Transport{Proxy: proxy}}
}

// bindHost binds or updates the generic frontend host services on s.
// Non-nil services in h replace existing ones so a partial test mount
// preserves services configured at construction time.
func (s *Server) bindHost(h frontend.Host) {
	if s == nil {
		return
	}
	if h.Support != nil {
		s.host.Support = h.Support
		s.support = h.Support
	}
	if h.TracePlayback {
		s.host.TracePlayback = true
		s.d.TracePlayback = true
	}
	if h.LiveTV != nil {
		s.host.LiveTV = h.LiveTV
		s.liveTV = h.LiveTV
	}
	if h.ClientSeen != nil {
		s.host.ClientSeen = h.ClientSeen
		s.clientSeen = h.ClientSeen
	}
	if h.RemoteClientSeen != nil {
		s.host.RemoteClientSeen = h.RemoteClientSeen
		s.remoteClientSeen = h.RemoteClientSeen
	}
	if h.Artwork != nil {
		s.host.Artwork = h.Artwork
		s.artwork = h.Artwork
		if rc := h.Artwork.RemoteCache(); rc != nil {
			s.images = rc
		}
		if pc := h.Artwork.PaletteCache(); pc != nil {
			s.ultra = pc
		}
		if pr := h.Artwork.PosterRenderer(); pr != nil {
			s.generatedPosters = pr
		}
		if fn := h.Artwork.ImageURLFunc(); fn != nil {
			s.imageURL = fn
		}
	}
	if h.Images != nil {
		s.host.Images = h.Images
		s.hostImages = h.Images
	}
	if h.ImageGrants != nil {
		s.host.ImageGrants = h.ImageGrants
		s.imageGrants = h.ImageGrants
	}
	if h.Events != nil {
		s.host.Events = h.Events
		s.events = h.Events
	}
	if h.DirectProxy != nil {
		s.host.DirectProxy = h.DirectProxy
		if s.extraPlay != nil {
			s.extraPlay.client = newDirectHTTPClient(h.DirectProxy)
		}
	}
	if h.MetadataHTTP != nil {
		s.host.MetadataHTTP = h.MetadataHTTP
	}
	if h.ViewerPolicy != nil {
		s.host.ViewerPolicy = h.ViewerPolicy
	}
	if h.Capabilities != nil {
		s.host.Capabilities = h.Capabilities
	}
	if h.Identity != nil {
		s.host.Identity = h.Identity
	}
	if h.Log != nil {
		s.host.Log = h.Log
		s.log = h.Log.With("component", "plex")
	}
	if h.ClientIP != nil {
		s.host.ClientIP = h.ClientIP
	}
	if h.SurfaceURL != nil {
		s.host.SurfaceURL = h.SurfaceURL
	}
	if h.PublicOrigin != nil {
		s.host.PublicOrigin = h.PublicOrigin
	}
	if h.RepoFileURL != nil {
		s.host.RepoFileURL = h.RepoFileURL
	}
	if h.Config != nil {
		s.host.Config = h.Config
	}
	if h.HostConfig != nil {
		s.host.HostConfig = h.HostConfig
	}
	if h.Now != nil {
		s.host.Now = h.Now
	}
	if h.ServerIdentity != (frontend.ServerIdentity{}) {
		s.host.ServerIdentity = h.ServerIdentity
	}
	if h.OwnerSubscription != nil {
		s.host.OwnerSubscription = h.OwnerSubscription
	}
	if h.MetadataLanguage != nil {
		s.host.MetadataLanguage = h.MetadataLanguage
	}
	if h.PersonalHomes != nil {
		s.host.PersonalHomes = h.PersonalHomes
		s.personalHomes = h.PersonalHomes
	}
	if h.PageFreshness != nil {
		s.host.PageFreshness = h.PageFreshness
		s.pageFreshness = h.PageFreshness
	}
	if h.TrackPreferences != nil {
		s.host.TrackPreferences = h.TrackPreferences
		s.trackPrefs = h.TrackPreferences
	}
	if h.ExtraStreams != nil {
		s.host.ExtraStreams = h.ExtraStreams
		s.extras = h.ExtraStreams
	}
	if h.Prerolls != nil {
		s.host.Prerolls = h.Prerolls
		s.prerolls = h.Prerolls
	}
	if h.Subtitles != nil {
		s.host.Subtitles = h.Subtitles
		s.subtitles = h.Subtitles
	}
	if h.Editions != nil {
		s.host.Editions = h.Editions
		s.variants = h.Editions
	}
	if h.Experience != nil {
		s.host.Experience = h.Experience
		s.attachExperience(h.Experience)
	}
	if h.LazyStreams != nil {
		s.host.LazyStreams = h.LazyStreams
		s.hostLazyStreams = h.LazyStreams
	}
	if h.LANStreams != nil {
		s.host.LANStreams = h.LANStreams
		s.hostLANStreams = h.LANStreams
	}
	if h.Tickets != nil {
		s.host.Tickets = h.Tickets
		s.hostTickets = h.Tickets
	}
	if h.Sessions != nil {
		s.usePlaybackBroker(h.Sessions)
	}
}

func (s *Server) attachExperience(exp frontend.Experience) {
	if s == nil || exp == nil {
		return
	}
	s.experience = exp
	cat := s.catalog()
	if !s.d.DisableEnrichment && cat != nil && s.titles == nil {
		s.titleJobs = newBGQueue(s.ctx, &s.wg, titleFetchWorkers, titleFetchQueueLen)
		s.blurJobs = newBGQueue(s.ctx, &s.wg, ultraBlurWorkers, ultraBlurQueueLen)
		s.titles = newTitleCache(s.titleJobs, s.now, s.log, s.enrichFetch)
		if s.ultra == nil {
			s.ultra = frontend.NewArtworkPaletteCache(s.runtimeDB(), s.blurJobs.submit, s.now, s.log, func(ctx context.Context, key frontend.RatingKey, path string) (frontend.ArtworkPalette, error) {
				var art frontend.ArtworkCollectionArt
				if c, ok := s.collections().(frontend.ArtworkCollectionArt); ok {
					art = c
				}
				return frontend.ComputeArtworkItemPalette(ctx, s.images, s.imageURL, key, path, s.now(), art)
			})
		}
	}
	if s.titles != nil {
		if pre, ok := cat.(frontend.StoredTitlePreloader); ok {
			s.titles.prefetch = pre.PreloadStored
		} else {
			s.titles.prefetch = nil
		}
	}
	if p, ok := cat.(frontend.ArtworkPersistenceCatalog); ok && s.ultra != nil {
		s.ultra.PersistFor(p.PersistsArtwork)
	}
	if s.home == nil {
		if _, hasRev, _ := exp.HomeRevision(s.ctx, nil); hasRev {
			s.home = newHomeCache()
			s.wg.Add(1)
			go s.homeRefresher()
		}
	}
	s.sections = s.buildSections()
}

func (s *Server) policy() frontend.AccessPolicy {
	if s == nil {
		return nil
	}
	return s.host.ViewerPolicy
}

func (s *Server) listUsers(ctx context.Context) ([]frontend.User, error) {
	if s == nil || s.host.Identity == nil {
		return nil, nil
	}
	return s.host.Identity.Users(ctx)
}

func (s *Server) exp() frontend.Experience {
	if s != nil && s.experience != nil {
		return s.experience
	}
	return frontend.NoopExperience()
}
func (s *Server) libraryGUIDs() frontend.LibraryGUIDs {
	if s == nil {
		return nil
	}
	if s.plexGUID != nil {
		return s.plexGUID
	}
	return s.exp().LibraryGUIDs()
}
func (s *Server) libraryMetadata() frontend.LibraryMetadata {
	if s == nil {
		return nil
	}
	if s.plexMeta != nil {
		return s.plexMeta
	}
	return s.exp().LibraryMetadata()
}
func (s *Server) catalog() frontend.Catalog         { return s.exp().CatalogSource() }
func (s *Server) collections() frontend.Collections { return s.exp().CollectionsSource() }
func (s *Server) state() frontend.State             { return s.exp().StateSource() }
func (s *Server) playlists() frontend.Playlists     { return s.exp().PlaylistsSource() }
func (s *Server) recommender() frontend.Recommender { return s.exp().RecommenderSource() }
func (s *Server) vibeSearch() frontend.VibeSearcher { return s.exp().VibeSearchSource() }
func (s *Server) searchSources() frontend.SearchSources {
	return s.exp().SearchSources()
}
func (s *Server) favoriteShows() func(context.Context, *frontend.User) (map[frontend.RatingKey]bool, error) {
	return s.exp().FavoriteShowsFunc()
}
func (s *Server) filterAvailable() func(context.Context, []frontend.Title) []frontend.Title {
	return s.exp().FilterAvailableFunc()
}
func (s *Server) judgedOnFacts() func(context.Context) func(frontend.Title) bool {
	return s.exp().JudgedOnFactsFunc()
}
func (s *Server) prioritizeAvailable() func(context.Context, []frontend.Title) []frontend.Title {
	return s.exp().PrioritizeAvailableFunc()
}
func (s *Server) episodeOrder() frontend.EpisodeOrder    { return s.exp().EpisodeOrderSource() }
func (s *Server) discovery() *frontend.DiscoveryFeatures { return s.exp().DiscoveryFeatures() }
func (s *Server) dashboard() frontend.Dashboard {
	if d := s.exp().DashboardSource(); d != nil {
		return d
	}
	return noopDashboard{}
}
func (s *Server) resources() frontend.ResourceStatistics {
	if r := s.exp().ResourcesSource(); r != nil {
		return r
	}
	return noopResources{}
}
func (s *Server) libraryTitles() (movies, shows string) { return s.exp().LibraryTitles() }

func (s *Server) playback() frontend.Playback {
	if s == nil {
		return nil
	}
	if s.d.Playback != nil && s.d.Playback != s.initPlayback {
		return s.d.Playback
	}
	if s.runtime != nil {
		if p := s.runtime.Playback(); p != nil {
			return p
		}
	}
	if s.d.Playback != nil {
		return s.d.Playback
	}
	return s.initPlayback
}

func (s *Server) transcoder() frontend.Transcoder {
	if s == nil {
		return nil
	}
	if s.d.Transcoder != nil && s.d.Transcoder != s.initTranscoder {
		return s.d.Transcoder
	}
	if s.runtime != nil {
		if t := s.runtime.Transcoder(); t != nil {
			return t
		}
	}
	if s.d.Transcoder != nil {
		return s.d.Transcoder
	}
	return s.initTranscoder
}

func (s *Server) runtimeDB() *sql.DB {
	if s == nil {
		return nil
	}
	if s.cfg().Spike.Enabled {
		return s.d.DB
	}
	if s.runtime != nil {
		if db := s.runtime.DB(); db != nil {
			return db
		}
	}
	return s.d.DB
}

func (s *Server) clusterNode() frontend.ClusterNode {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.Cluster()
}

func (s *Server) sessionHeartbeat() time.Duration {
	if s == nil || s.runtime == nil {
		return 0
	}
	return s.runtime.SessionHeartbeat()
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

// Closed reports whether Close has begun.
func (s *Server) Closed() bool {
	return s == nil || s.ctx.Err() != nil
}

// Done is closed when the server is closed.
func (s *Server) Done() <-chan struct{} { return s.ctx.Done() }

// Close stops background goroutines and disconnects notification clients.
func (s *Server) Close() error {
	s.once.Do(func() {
		if s.runtimeDB() != nil {
			s.stopDownloadsForRestart()
		} else {
			s.releaseAllDownloads()
		}
		s.cancel()

		s.notifier.close()
		s.wg.Wait()
		s.clearOwnDecisionArms()
		if err := s.saveDownloads(); err != nil {
			s.log.Warn("persist downloads on close", "err", err)
		}
		if s.cl != nil {
			s.cl.close()
		}
	})
	return nil
}

func (s *Server) settings() frontend.Settings {
	if s == nil || s.host.Config == nil {
		return nil
	}
	return s.host.Config()
}

func (s *Server) hasConfig() bool {
	return s != nil && s.host.HostConfig != nil
}

func (s *Server) now() time.Time {
	if s != nil && s.host.Now != nil {
		return s.host.Now()
	}
	return time.Now()
}

func (s *Server) cfg() frontend.HostConfig {
	if s == nil || s.host.HostConfig == nil {
		return frontend.HostConfig{}
	}
	return s.host.HostConfig()
}

// identity resolves the effective identity.
func (s *Server) identity() Identity {
	var id Identity
	if s != nil {
		id = Identity{
			MachineIdentifier: s.host.ServerIdentity.MachineID,
			FriendlyName:      s.host.ServerIdentity.FriendlyName,
			Version:           s.host.ServerIdentity.Version,
			Platform:          s.host.ServerIdentity.Platform,
			PlatformVersion:   s.host.ServerIdentity.PlatformVersion,
		}
		if s.host.OwnerSubscription != nil {
			id.MyPlexSubscription, id.OwnerFeatures = s.host.OwnerSubscription()
		}
	}
	c := s.cfg()
	if id.MachineIdentifier == "" {
		id.MachineIdentifier = c.Server.MachineIdentifier
	}
	if id.MachineIdentifier == "" {
		id.MachineIdentifier = "ether"
	}
	if name := strings.TrimSpace(c.Server.FriendlyName); name != "" {
		id.FriendlyName = name
	} else if strings.TrimSpace(id.FriendlyName) == "" {
		if st := s.settings(); st != nil {
			id.FriendlyName = frontend.AdvertisedName(st)
		} else {
			id.FriendlyName = "Ether"
		}
	}
	if c.Plex.CompatibilityVersion != "" {
		id.Version = c.Plex.CompatibilityVersion
	} else if c.Plex.TrackLatestVersion && c.Plex.LatestCompatibilityVersion != "" {
		id.Version = c.Plex.LatestCompatibilityVersion
	}
	if id.Version == "" {
		id.Version = DefaultPMSVersion
	}
	if id.Platform == "" {
		switch runtime.GOOS {
		case "darwin":
			id.Platform = "MacOSX"
		case "windows":
			id.Platform = "Windows"
		default:
			id.Platform = "Linux"
		}
	}
	return id
}

// LibrarySections returns the canonical section identities for account
// registration. Titles follow the live config so renames publish to plex.tv.
// The copy prevents publishers from changing local libraries.
func (s *Server) LibrarySections() []Section {
	return s.withLibraryTitles(s.sections)
}

func (s *Server) buildSections() []Section {
	secs := s.d.Sections
	if len(secs) == 0 {
		secs = []Section{{ID: 1, Type: frontend.TypeMovie}}
		if !s.cfg().Spike.Enabled {
			secs = append(secs, Section{ID: 2, Type: frontend.TypeShow})
		}
	}
	out := make([]Section, len(secs))
	for i, sec := range secs {
		if sec.Language == "" {
			sec.Language = "en-US"
		}
		if sec.UUID == "" {
			sum := sha1.Sum([]byte(fmt.Sprintf("%s/section/%d", s.identity().MachineIdentifier, sec.ID)))
			h := hex.EncodeToString(sum[:16])
			sec.UUID = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
		}
		out[i] = sec
	}
	return s.withLibraryTitles(out)
}

// librarySectionTitle is the Movies / TV Shows name Ether advertises to
// Plex, from libraries 1/2 via Deps.LibraryTitles.
func librarySectionTitle(t frontend.MediaType, movies, shows string) string {
	switch t {
	case frontend.TypeShow, frontend.TypeSeason, frontend.TypeEpisode:
		if n := strings.TrimSpace(shows); n != "" {
			return n
		}
		return "TV Shows"
	default:
		if n := strings.TrimSpace(movies); n != "" {
			return n
		}
		return "Movies"
	}
}

// withLibraryTitles copies secs and applies the library names.
// IDs and UUIDs are not rewritten; UUIDs stay derived from section id only.
// Empty library names already resolve to Movies and TV Shows.
func (s *Server) withLibraryTitles(secs []Section) []Section {
	movies, shows := s.libraryTitles()
	out := append([]Section(nil), secs...)
	for i := range out {
		switch out[i].Type {
		case frontend.TypeMovie, frontend.TypeShow:
			out[i].Title = librarySectionTitle(out[i].Type, movies, shows)
		}
	}
	return out
}

// sectionByID returns the section with the given id.
func (s *Server) sectionByID(id int) (Section, bool) {
	for _, sec := range s.withLibraryTitles(s.sections) {
		if sec.ID == id {
			return sec, true
		}
	}
	return Section{}, false
}

// sectionFor returns the section an item of type t lives in.
func (s *Server) sectionFor(t frontend.MediaType) (Section, bool) {
	sec, ok := s.untitledSectionFor(t)
	if !ok {
		return Section{}, false
	}
	return s.withLibraryTitles([]Section{sec})[0], true
}

// untitledSectionFor is sectionFor without the configured title. A
// visibility check needs only the section id, and runs for every title a
// page shows, so it reads no configuration.
func (s *Server) untitledSectionFor(t frontend.MediaType) (Section, bool) {
	want := frontend.TypeMovie
	switch t {
	case frontend.TypeShow, frontend.TypeSeason, frontend.TypeEpisode:
		want = frontend.TypeShow
	}
	for _, sec := range s.sections {
		if sec.Type == want {
			return sec, true
		}
	}
	return Section{}, false
}

// userSections lists the sections a user may see.
func (s *Server) userSections(u *frontend.User) []Section {
	secs := s.withLibraryTitles(s.sections)
	if u == nil || len(u.AllowedLibraries) == 0 || u.IsOwner {
		return secs
	}
	var out []Section
	for _, sec := range secs {
		if frontend.SectionAllowed(u, sec.ID) {
			out = append(out, sec)
		}
	}
	return out
}

func (s *Server) canSeeSection(u *frontend.User, id int) bool {
	for _, sec := range s.sections {
		if sec.ID == id {
			return u == nil || len(u.AllowedLibraries) == 0 || u.IsOwner || frontend.SectionAllowed(u, sec.ID)
		}
	}
	return false
}

func (s *Server) expireTranscodes(now time.Time, ttl time.Duration) {
	for _, id := range s.transcodes.expire(now, ttl) {
		s.revokeReadCapability(id)
	}
}

// janitor expires idle play queues, sessions and transcode bindings.
func (s *Server) janitor() {
	defer s.wg.Done()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			now := s.now()

			s.expireDownloads(now)
			s.queues.Expire(now, 6*time.Hour)
			s.playIDs.expire(now, s.playIdentityAlive)
			s.expireTranscodes(now, 2*time.Hour)
			s.playbackOwners.Expire(now, 2*time.Hour)
			s.extraPlay.expire(now, 2*time.Hour)
			s.expireTerminations(now, terminationTTL)
			s.refreshDecisionArms(now)
			s.starts.Expire(now)
			expired := s.sessions.expire(now, 3*time.Minute)
			for _, e := range expired {
				s.notifyPlaying(e, "stopped")
				// Timeline silence is a stop, not just a display expiry:
				// end the playback and encoder sessions too (#410).
				s.endExpiredEntry(e)
			}
			if len(expired) > 0 {
				s.cl.poke()
			}
		}
	}
}

func trimSlash(p string) string { return strings.TrimSuffix(p, "/") }

// releaseMountedPlayback stops encoders, trickplay, and downloads this server
// started. The HTTP server itself stays up so an in-flight request can finish
// and a later mount can reuse it.
func (s *Server) releaseMountedPlayback() {
	if s == nil {
		return
	}
	s.usePlaybackBroker(nil)
	var ids []string
	if s.transcodes != nil {
		ids = s.transcodes.ids()
	}
	for _, id := range ids {
		if s.transcoder() != nil {
			s.stopPlexHLS(id)
		}
		s.dropTranscode(id)
	}

	if s.runtimeDB() != nil {
		s.stopDownloadsForRestart()
		s.stageDownloads()
	} else {
		s.releaseAllDownloads()
	}
}

type noopDashboard struct{}

func (noopDashboard) SeeDevice(string, string, string) frontend.StatisticsDevice {
	return frontend.StatisticsDevice{}
}
func (noopDashboard) Devices() []frontend.StatisticsDevice { return nil }
func (noopDashboard) Device(int64) (frontend.StatisticsDevice, bool) {
	return frontend.StatisticsDevice{}, false
}
func (noopDashboard) Bandwidth(frontend.StatisticsQuery) frontend.StatisticsPage {
	return frontend.StatisticsPage{}
}
func (noopDashboard) Media(frontend.StatisticsQuery) frontend.StatisticsPage {
	return frontend.StatisticsPage{}
}
func (noopDashboard) RecordBytes(int64, string, string, string, string, bool, int64) {}
func (noopDashboard) RecordPlay(int64, string, string, int, int64)                   {}

type noopResources struct{}

func (noopResources) Query(int, int64, int64, int64, bool, bool, bool) []frontend.ResourceSample {
	return nil
}
