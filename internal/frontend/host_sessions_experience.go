package frontend

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexmeta"
)

// This file holds the session/experience host contracts protocol adapters
// (Plex included) use instead of importing internal/sessions or
// internal/experience. Implementations of the pure policy helpers live here;
// stateful services stay in their domain packages and are wired by the app.

// AccessPolicy adds owner-defined restrictions to the mandatory title guard.
type AccessPolicy interface {
	Allowed(*core.User, *core.Title) bool
}

// TitleAllowed applies the mandatory content guards and the owner's policy.
func TitleAllowed(policy AccessPolicy, viewer *core.User, title *core.Title) bool {
	if title == nil || title.Adult {
		return false
	}
	if viewer != nil && contentrating.Exceeds(title.ContentRating, viewer.MaxContentRating) {
		return false
	}
	return policy == nil || policy.Allowed(viewer, title)
}

// SectionAllowed applies a viewer's library restrictions to an existing section.
// An empty restriction list means all sections, as it does for an owner.
func SectionAllowed(viewer *core.User, id int) bool {
	if viewer == nil || viewer.IsOwner || len(viewer.AllowedSections) == 0 {
		return true
	}
	for _, allowed := range viewer.AllowedSections {
		if allowed == id {
			return true
		}
	}
	return false
}

// Visibility applies content, library and inherited show restrictions. The
// section lookup returns false for media types with no configured library.
type Visibility struct {
	Policy          AccessPolicy
	SectionFor      func(core.MediaType) (int, bool)
	GetTitle        func(context.Context, core.RatingKey) (*core.Title, error)
	FilterAvailable func(context.Context, []core.Title) []core.Title
	// JudgedOnFacts returns, for one request, whether FilterAvailable judges
	// a title on its full facts rather than its row. Nil with FilterAvailable
	// set means every title may be.
	JudgedOnFacts func(context.Context) func(core.Title) bool
	Sessions      func() []*core.PlaySession
}

// TitleFacts is what the access policy reads of a title beyond its row for
// viewer (core.TitleFacts). A policy that does not say may read any of them.
func (v Visibility) TitleFacts(viewer *core.User) core.TitleFacts {
	if v.Policy == nil {
		return 0
	}
	if p, ok := v.Policy.(interface {
		TitleFacts(*core.User) core.TitleFacts
	}); ok {
		return p.TitleFacts(viewer)
	}
	return core.VisibilityFacts
}

// FactJudge is JudgedOnFacts for one request: nil when no title is,
// false when the visibility cannot tell.
func (v Visibility) FactJudge(ctx context.Context) (func(core.Title) bool, bool) {
	if v.FilterAvailable == nil {
		return nil, true
	}
	if v.JudgedOnFacts == nil {
		return nil, false
	}
	return v.JudgedOnFacts(ctx), true
}

func (v Visibility) TitleAllowed(viewer *core.User, title *core.Title) bool {
	if !TitleAllowed(v.Policy, viewer, title) {
		return false
	}
	if v.SectionFor == nil {
		return true
	}
	id, ok := v.SectionFor(title.Type)
	return !ok || SectionAllowed(viewer, id)
}

// Playable checks inherited show policy without requiring discovery evidence.
func (v Visibility) Playable(ctx context.Context, viewer *core.User, title *core.Title) bool {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if !v.TitleAllowed(viewer, title) {
		return false
	}
	if title.ShowKey == 0 {
		return true
	}
	if v.GetTitle == nil {
		return false
	}
	show, err := v.GetTitle(ctx, title.ShowKey)
	return err == nil && v.TitleAllowed(viewer, show)
}

// Visible includes availability in addition to the playback policy.
func (v Visibility) Visible(ctx context.Context, viewer *core.User, title *core.Title) bool {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if !v.TitleAllowed(viewer, title) {
		return false
	}
	if len(v.Available(ctx, viewer, []core.Title{*title})) == 0 {
		return false
	}
	return v.Playable(ctx, viewer, title)
}

// Available filters discovery evidence, retaining a viewer's open sources.
// Callers must separately enforce title policy.
func (v Visibility) Available(ctx context.Context, viewer *core.User, titles []core.Title) []core.Title {
	if policy, ok := v.Policy.(interface {
		DiscoveryAllowed(*core.User, *core.Title) bool
	}); ok && !core.ExplicitSearch(ctx) {
		out := make([]core.Title, 0, len(titles))
		for _, title := range titles {
			if policy.DiscoveryAllowed(viewer, &title) {
				out = append(out, title)
			}
		}
		titles = out
	}

	ctx = core.WithCatalogViewer(ctx, viewer)
	if v.FilterAvailable == nil {
		return titles
	}
	filtered := v.FilterAvailable(core.WithCatalogViewer(ctx, viewer), titles)
	if len(filtered) == len(titles) || viewer == nil || v.Sessions == nil {
		return filtered
	}
	keep := map[core.RatingKey]bool{}
	for _, title := range filtered {
		keep[title.Key] = true
	}
	for _, session := range v.Sessions() {
		if session != nil && session.UserID == viewer.ID {
			keep[session.Key] = true
		}
	}
	out := make([]core.Title, 0, len(titles))
	for _, title := range titles {
		if keep[title.Key] {
			out = append(out, title)
		}
	}
	return out
}

// CollectionAllowed checks the library restriction for a collection.
func (v Visibility) CollectionAllowed(viewer *core.User, c *core.Collection) bool {
	if c.Type != core.TypeMovie && c.Type != core.TypeShow {
		return true
	}
	if v.SectionFor == nil {
		return true
	}
	id, ok := v.SectionFor(c.Type)
	return !ok || SectionAllowed(viewer, id)
}

// UpcomingItems retains the existing placeholder discovery rules for a page.
func (v Visibility) UpcomingItems(ctx context.Context, viewer *core.User, items []core.Title, now func() time.Time) []core.Title {
	ctx = core.WithCatalogViewer(ctx, viewer)
	nowVal := time.Now()
	if now != nil {
		nowVal = now()
	}
	allowed := make([]bool, len(items))
	future := make([]bool, len(items))
	var released []core.Title
	for i := range items {
		title := &items[i]
		if !v.Playable(ctx, viewer, title) || title.Released.IsZero() {
			continue
		}
		allowed[i] = true
		if title.IsFutureRelease(nowVal) {
			future[i] = true
		} else {
			released = append(released, *title)
		}
	}
	availSet := make(map[core.RatingKey]bool, len(released))
	if len(released) > 0 {
		for _, t := range v.Available(ctx, viewer, released) {
			availSet[t.Key] = true
		}
	}
	seen := make(map[core.RatingKey]bool, len(items))
	out := make([]core.Title, 0, len(items))
	for i, title := range items {
		if !allowed[i] || seen[title.Key] {
			continue
		}
		if !future[i] && !availSet[title.Key] {
			continue
		}
		seen[title.Key] = true
		out = append(out, title)
	}
	return out
}

// TitlesAllowed filters the title/section guard without discovery filtering.
func (v Visibility) TitlesAllowed(viewer *core.User, titles []core.Title) []core.Title {
	out := make([]core.Title, 0, len(titles))
	for i := range titles {
		if v.TitleAllowed(viewer, &titles[i]) {
			out = append(out, titles[i])
		}
	}
	return out
}

// MaySetViewerRating is the owner-console rule for changing a household rating cap.
// Delegated sessions and non-owners cannot write it.
func MaySetViewerRating(actor *core.Viewer, active *core.User) bool {
	return actor != nil && !actor.Delegated && active != nil && active.IsOwner
}

// ViewerRestricted chooses inherited visibility for limited or unknown policies.
func ViewerRestricted(policy AccessPolicy, u *core.User) bool {
	if u == nil {
		return true
	}
	if u.IsOwner {
		return false
	}
	if u.MaxContentRating != "" || len(u.AllowedSections) > 0 {
		return true
	}
	if policy == nil {
		return false
	}
	if p, ok := policy.(core.ArtworkAccessPolicy); ok {
		return p.Restricted(u)
	}
	return true
}

// HomeRevision stamps a viewer's cached Home. Viewer changes the Home must
// show on the very next request: the latest watch-state write (Continue
// Watching, watched badges, ratings), playlists, watchlist, favorites and
// "not interested" exclusions. Policy is the shared viewer-policy epoch:
// it also moves on catalogue and guide changes, so only restricted viewers
// treat it as a hard input.
type HomeRevision struct {
	Viewer string
	Policy int64
}

// ApplyMetadataDisplay applies a cached Plex metadata overlay's display
// fields to title.
func ApplyMetadataDisplay(title *core.Title, overlay MetadataOverlay) {
	plexmeta.ApplyDisplay(title, overlay)
}

// SaferMetadataRating is the stricter of a catalogue and overlay rating.
func SaferMetadataRating(catalogue, overlay string) string {
	return plexmeta.SaferRating(catalogue, overlay)
}

// MetadataOverlayRatingAllowed reports whether a cached metadata overlay's
// rating stays within viewer's content-rating cap when combined with the
// catalogue rating.
func MetadataOverlayRatingAllowed(viewer *core.User, catalogue, overlay string) bool {
	if viewer == nil || viewer.MaxContentRating == "" || overlay == "" {
		return true
	}
	return !contentrating.Exceeds(SaferMetadataRating(catalogue, overlay), viewer.MaxContentRating)
}

// DownloadsAllowed is shared capability admission; handlers still enforce it.
func DownloadsAllowed(viewer *core.User, ready, allowFriends bool, policy AccessPolicy) bool {
	if viewer == nil || !ready {
		return false
	}
	if viewer.IsOwner {
		return true
	}
	if p, ok := policy.(core.DownloadAccessPolicy); ok {
		return p.AllowDownloads(viewer)
	}
	return allowFriends
}

// PlexWireID is the positive int32-range id Plex clients already parse.
// Offer rows use it directly and are not stream bindings.
func PlexWireID(key core.RatingKey, slot string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(int64(key), 10) + "/" + slot))
	return int64(h.Sum64()%2_000_000_000) + 1
}

type Binding[R any] struct {
	ID          string // client "session" param
	StreamToken string // independent random capability; never a client-chosen ID
	PlayID      string // core.PlaySession.ID
	PlaybackID  string // explicit X-Plex-Session-IDentifier owning this encoder
	UserID      int64
	ClientID    string
	Key         core.RatingKey
	Requested   R // original Decision/start inputs, before policy refinement
	Decision    core.Decision
	Info        core.MediaInfo
	Subtitle    int // selected Subtitle's position in Info, -1 = none
	Caps        core.ClientCaps
	Opts        core.DecideOptions
	Offset      time.Duration
	Started     bool // StartHLS ran
	UpdatedAt   time.Time
	// A failed Decision for this binding (FailKey, FailErr at FailedAt):
	// the start request that follows it answers with the same error at
	// once instead of resolving again. One-shot and short-lived.
	FailKey  core.RatingKey
	FailErr  error
	FailedAt time.Time
}

type QueueItem struct {
	ID    int64
	Key   core.RatingKey
	Added bool // explicit Up Next addition, distinct from original queue members
}

type PlayQueue struct {
	ID               int64
	UserID           int64
	ClientID         string
	Items            []QueueItem
	Original         []QueueItem // order before shuffle
	Selected         int         // index into items
	Shuffled         bool
	SourceURI        string
	PlaylistID       core.RatingKey
	PlaylistTitle    string
	Version          int
	LastAdded        int64
	UpdatedAt        time.Time
	SuppressItem     int64
	SuppressRows     bool // mutation response only
	SuppressMetadata bool
	ForceOffsetZero  bool
	LiveSession      string // live queues resolve through LiveTV, never the catalogue

	Rev      int64     // clustered: stored row revision
	LoadedAt time.Time // clustered: when the cached copy was read or written
}

// PlayQueuePersistence connects shared queue transactions to node invalidation
// and outage reporting. Configure it before serving requests.
type PlayQueuePersistence struct {
	DB           *sql.DB
	Counter      func(context.Context, string, int) (int64, error)
	Invalidate   func(int64)
	Warn         func(string, error)
	ExpireResult func(error)
}

type DownloadItem struct {
	ID      int64
	QueueID int64
	Key     core.RatingKey
	Title   string // display Title, for the served filename

	Caps core.ClientCaps // as requested at add time (Download set at Run)
	User *core.User

	Status   string
	Error    string
	Progress float64 // 0..1
	Started  bool
	Cancel   context.CancelFunc
	// Run numbers each start of the Item, so a restarted job gets its own
	// playback session instead of sharing (and later stopping) the
	// previous Run's.
	Run int64

	Path       string
	Size       int64
	ReadyAt    time.Time
	VideoCodec string
	AudioCodec string
	Duration   time.Duration
	Bandwidth  int // requested maxVideoBitrate, kbps
	Burn       bool
}

type DownloadQueue struct {
	ID       int64
	UserID   int64
	ClientID string
	Items    []*DownloadItem
}

type DownloadStart struct {
	Item    *DownloadItem
	Context context.Context
	Run     int64
}

type DownloadRequest struct {
	Viewer    *core.User
	Item      core.RatingKey
	Caps      core.ClientCaps
	Run       int64
	NoRelease func(error) bool
	Prepared  func(*core.PlaySession) (core.DecideOptions, error)
	Progress  func(float64)
}

type DownloadPreparationError struct{ Err error }

type VariantRef struct {
	Key       core.RatingKey
	ReleaseID string
}

// StartHintKey names one remembered fallback. A title Key is the Dolby
// Vision HDR10-base hint for that title; a zero Key is client-wide: this
// viewer's client stalled on surround audio copied into segmented output.
type StartHintKey struct {
	UserID   int64
	ClientID string
	Key      core.RatingKey
}

// PlexStreamBinding is one Plex-visible stream id. WireID is zero on bind;
// BindPlexStream assigns it. SessionID is empty for library metadata and is
// the broker session id after Open.
type PlexStreamBinding struct {
	WireID    int64
	SessionID string
	Key       core.RatingKey
	Index     int
	Info      core.StreamInfo
}

// PlexAdmission is the source and the version and track selections for one
// Plex plan. Plan makes the direct, remux, or transcode decision from them.
// Open publishes that decision. The adapter renders it and does not decide again.
type PlexAdmission struct {
	Source               *core.PlaySession
	Media                core.MediaInfo
	Caps                 core.ClientCaps
	Options              core.DecideOptions
	PreferRemux          bool
	Protocol             string
	RemoteMaxBitrateKbps int
}

// DecisionOwner identifies the authenticated public player, independently of
// its encoder delivery handle.
type DecisionOwner struct {
	ID                  string
	UserID              int64
	ClientID, SessionID string
}

// VersionFlowKey scopes issued versions to one viewer, client and title.
type VersionFlowKey struct {
	UserID   int64
	ClientID string
	Key      core.RatingKey
}

type VersionSnapshot struct {
	Variants []core.ReleaseVariant
	Frozen   bool
	Until    time.Time
}

// PlexActivity is one Plex client's playback as the session list shows it.
// The broker is the only store. Cluster rows for these activities use
// protocol plex.
type PlexActivity struct {
	ID                                                     string
	DecisionSeen                                           bool
	PlayingSeen                                            bool
	SessionKey                                             int
	User                                                   core.User
	ClientID, Product, Version, Platform, PlatformVersion  string
	Device, DeviceName, Model, Vendor, SessionID, RemoteIP string
	ProfileName                                            string
	Remote                                                 bool
	Key                                                    core.RatingKey
	State                                                  string
	Offset, Duration                                       time.Duration
	PlayQueueItemID                                        int64
	TranscodeID                                            string
	StartedAt, UpdatedAt                                   time.Time
}

// StartObservation is the evidence used by stalled-start detection.
type StartObservation interface {
	core.PlaybackPosition
	Decision() core.Decision
	Info() core.MediaInfo
}

type PlexPartSource struct {
	Session               *core.PlaySession
	Matches               bool
	PreviousID            string
	AnonymousContinuation bool
}

func (q *PlayQueue) Snapshot() PlayQueue {
	cp := *q
	cp.Items = append([]QueueItem(nil), q.Items...)
	cp.Original = append([]QueueItem(nil), q.Original...)
	return cp
}

// writePlayQueue renders a queue window around the selected item.
func (q *PlayQueue) ReanchorAdded() {
	if q.LastAdded == 0 {
		return
	}
	for i := max(0, q.Selected+1); i < len(q.Items) && q.Items[i].Added; i++ {
		q.LastAdded = q.Items[i].ID
	}
}

func (q *DownloadQueue) Item(ID int64) *DownloadItem {
	for _, it := range q.Items {
		if it.ID == ID {
			return it
		}
	}
	return nil
}

func (e *DownloadPreparationError) Error() string { return e.Err.Error() }
func (e *DownloadPreparationError) Unwrap() error { return e.Err }

// Selections and BindingTable are generic and cannot be constructed
// through the PlexSessionStores factory (Go interfaces have no generic
// methods); their small generic implementations live here and sessions
// aliases them.

// Selections stores per-viewer track choices. The adapter owns the value
// shape and translates wire stream identifiers before updating a choice.
type Selections[T any] struct {
	mu     sync.Mutex
	values map[[2]int64]T
}

func NewSelections[T any]() *Selections[T] { return &Selections[T]{values: map[[2]int64]T{}} }
func (s *Selections[T]) Get(viewer, item int64) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[[2]int64{viewer, item}]
}
func (s *Selections[T]) Update(viewer, item int64, fn func(*T)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) > 100_000 {
		s.values = map[[2]int64]T{}
	}
	key := [2]int64{viewer, item}
	value := s.values[key]
	fn(&value)
	s.values[key] = value
}

// decisionFailureTTL bounds how long a failed Decision answers the start
// request that follows it; after that a start resolves afresh.
const decisionFailureTTL = 15 * time.Second

type BindingTable[R any] struct {
	mu sync.Mutex
	m  map[string]*Binding[R]
}

func NewBindingTable[R any]() *BindingTable[R] { return &BindingTable[R]{m: map[string]*Binding[R]{}} }

func (t *BindingTable[R]) Put(e *Binding[R]) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.m[e.ID]; ok {
		if old.UserID != e.UserID || old.ClientID != e.ClientID {
			return
		}
		e.StreamToken = old.StreamToken
	}
	if e.StreamToken == "" {
		e.StreamToken = crand.Text()
	}
	cp := *e
	t.m[e.ID] = &cp
}

// Claim reserves a client-chosen ID before any playback/transcoder work. A
// different viewer or device cannot replace, reuse, stop, or probe that binding.
func (t *BindingTable[R]) Claim(ID string, UserID int64, ClientID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.m[ID]; ok {
		return e.UserID == UserID && e.ClientID == ClientID
	}
	t.m[ID] = &Binding[R]{ID: ID, UserID: UserID, ClientID: ClientID, StreamToken: crand.Text(), UpdatedAt: now}
	return true
}

func (t *BindingTable[R]) Get(ID string) (*Binding[R], bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[ID]
	if !ok {
		return nil, false
	}
	cp := *e
	return &cp, true
}

// Fail records a failed Decision on the binding the Decision claimed. A
// binding owned by another viewer or device is left alone.
func (t *BindingTable[R]) Fail(ID string, UserID int64, ClientID string, Key core.RatingKey, err error, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[ID]
	if !ok || e.UserID != UserID || e.ClientID != ClientID || err == nil {
		return
	}
	e.FailKey, e.FailErr, e.FailedAt, e.UpdatedAt = Key, err, now, now
}

// TakeFailure returns and clears a recent failed Decision for this viewer's
// binding and title.
func (t *BindingTable[R]) TakeFailure(ID string, UserID int64, Key core.RatingKey, now time.Time) (error, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[ID]
	if !ok || e.FailErr == nil {
		return nil, false
	}
	err, match := e.FailErr, e.UserID == UserID && e.FailKey == Key && now.Sub(e.FailedAt) < decisionFailureTTL
	if e.UserID == UserID {
		e.FailKey, e.FailErr, e.FailedAt = 0, nil, time.Time{}
	}
	if !match {
		return nil, false
	}
	return err, true
}

func (t *BindingTable[R]) Touch(ID string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.m[ID]; ok {
		e.UpdatedAt = now
	}
}

func (t *BindingTable[R]) Remove(ID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, ID)
}

// ForClient lists the transcode sessions of a client for a title.
func (t *BindingTable[R]) ForClient(UserID int64, ClientID string, Key core.RatingKey) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for ID, e := range t.m {
		if e.UserID == UserID && e.ClientID == ClientID && (Key == 0 || e.Key == Key) {
			out = append(out, ID)
		}
	}
	return out
}

func (t *BindingTable[R]) Expire(now time.Time, ttl time.Duration) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var gone []string
	for ID, e := range t.m {
		if now.Sub(e.UpdatedAt) > ttl {
			delete(t.m, ID)
			gone = append(gone, ID)
		}
	}
	return gone
}

// IDs lists every binding this table still holds.
func (t *BindingTable[R]) IDs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.m))
	for id := range t.m {
		ids = append(ids, id)
	}
	return ids
}

// ByPlayID returns the transcode session ids fed by a playback session.
func (t *BindingTable[R]) ByPlayID(PlayID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ids []string
	for ID, e := range t.m {
		if e.PlayID == PlayID && e.Started {
			ids = append(ids, ID)
		}
	}
	return ids
}

// ---------------------------------------------------------------------------
// Session store host contracts. Implementations live in internal/sessions;
// the app wires them through PlexSessionStores. Value types shared across
// the boundary are defined in this file; sessions aliases them.

// PlaybackOwners is the v39 playback owner history.
type PlaybackOwners interface {
	Bind(user int64, client string, key core.RatingKey, id, playID string, decision bool, now time.Time)
	BindFor(surface string, user int64, client string, key core.RatingKey, id, playID string, decision bool, now time.Time)
	Stale(user int64, client string, key core.RatingKey, id string) bool
	StaleFor(surface string, user int64, client string, key core.RatingKey, id string) bool
	Expire(now time.Time, ttl time.Duration)
}

// Lifecycle serializes playback lifecycle operations per key.
type Lifecycle interface {
	Acquire(ctx context.Context, key string) (func(), error)
	References(key string) int
}

// Coordination holds the owner history and lifecycle gates shared by every
// protocol adapter in one Ether process.
type Coordination interface {
	PlaybackOwners() PlaybackOwners
	LifecycleGates() Lifecycle
}

// DecisionArms records armed playback decisions per owner and title.
type DecisionArms interface {
	Expire(now time.Time)
	Arm(pr DecisionOwner, key core.RatingKey)
	Armed(pr DecisionOwner, key core.RatingKey) bool
	Disarm(pr DecisionOwner, key core.RatingKey)
	Refresh(now time.Time)
	ClearOwn()
}

// StartGuard watches stalled playback starts and start hints.
type StartGuard interface {
	Observe(tid, state string, offset, ahead time.Duration, now time.Time) bool
	Forget(tid string)
	Remember(k StartHintKey, now time.Time)
	BaseOnly(k StartHintKey, now time.Time) bool
	Expire(now time.Time)
}

// PlayQueues is the play-queue store.
type PlayQueues interface {
	Preallocate(keys []core.RatingKey) ([]QueueItem, error)
	CreateWith(userID int64, clientID string, keys []core.RatingKey, now time.Time, fn func(q *PlayQueue)) (PlayQueue, error)
	With(id, userID int64, now time.Time, fn func(q *PlayQueue)) (PlayQueue, bool)
	Get(id, userID int64) (PlayQueue, bool)
	SelectItem(userID, itemID int64)
	Expire(now time.Time, ttl time.Duration)
	AddItems(q *PlayQueue, keys []core.RatingKey, pre []QueueItem, next bool)
	Forget(id int64)
	DropCache()
	Reshuffle(q *PlayQueue, shuffled bool)
	Take(id, userID int64, clientID string, now time.Time) (PlayQueue, bool)
	Configure(now func() time.Time, persistence *PlayQueuePersistence)
	Persistent() bool
	Len() int
	FollowingPlayable(userID int64, key core.RatingKey, playable func(core.RatingKey) bool) core.RatingKey
	TimelineQueue(user int64, client string, key core.RatingKey, raw string, present bool) core.RatingKey
}

// VersionFlows stores version-picker flow snapshots.
type VersionFlows interface {
	Call(ctx context.Context, k VersionFlowKey, vs []core.ReleaseVariant, mode int, now time.Time, ttl time.Duration) (VersionSnapshot, error)
	Run()
}

// DownloadQueueStore is the download-queue store.
type DownloadQueueStore interface {
	QueueFor(UserID int64, ClientID string, create bool) *DownloadQueue
	QueueByID(ID, UserID int64, ClientID string) *DownloadQueue
	Add(q *DownloadQueue, u *core.User, t core.Title, caps core.ClientCaps, burn bool) *DownloadItem
	StartReady(ctx context.Context, perUser int, allowed func(*core.User) bool) []DownloadStart
	Expire(now time.Time, expiry time.Duration) []string
	StopForRestart()
	Remove(q *DownloadQueue, ids []int64) []*DownloadItem
	Restart(q *DownloadQueue, ids []int64, release func(string))
	CancelItem(it *DownloadItem) string
	Fail(ctx context.Context, it *DownloadItem, message string)
	Lock()
	Unlock()
	WakeDownloads()
	AllItems() []*DownloadItem
	Prepared(ctx context.Context, it *DownloadItem, info core.MediaInfo) bool
	Progress(ctx context.Context, it *DownloadItem, fraction float64)
	Complete(ctx context.Context, it *DownloadItem, path string, size int64, now time.Time) bool
}

// VariantIDs assigns stable ids to release variants.
type VariantIDs interface {
	IDFor(key core.RatingKey, releaseID string) int64
	Lookup(id int64) (VariantRef, bool)
	Start(ctx context.Context, wg *sync.WaitGroup)
	Flush(ctx context.Context) error
	Prune(ctx context.Context, liveIDs []int64, liveRefs []VariantRef) (int64, error)
}

// PlexStreamTable is the Plex visible-id map.
type PlexStreamTable interface {
	Bind(binding PlexStreamBinding) (int64, error)
	Lookup(id int64) (PlexStreamBinding, bool)
}

// PlexStreamAPI is the visible-id and sidecar surface Plex calls.
type PlexStreamAPI interface {
	BindPlexStream(context.Context, SessionAccess, PlexStreamBinding) (int64, error)
	ResolvePlexStream(context.Context, SessionAccess, int64) (PlexStreamBinding, error)
	OpenPlexSidecar(context.Context, SessionAccess, auth.Acceptance, int64) (io.ReadCloser, core.StreamInfo, error)
}

// PlexSources prepares and serves Plex direct-play part sources.
type PlexSources interface {
	Prepare(ctx context.Context, viewer *core.User, key core.RatingKey, caps core.ClientCaps, release string, wait time.Duration) (*core.PlaySession, error)
	Part(viewer int64, client string, key core.RatingKey, release string) PlexPartSource
	ServePart(w http.ResponseWriter, r *http.Request, sourceID string)
}

// DownloadJob runs one download preparation.
type DownloadJob interface {
	Run(ctx context.Context, q DownloadRequest) (string, int64, error)
}

// DownloadQueuePersistence persists the download queue.
type DownloadQueuePersistence interface {
	Retain(path string, ready time.Time)
	Save() error
	Load(ctx context.Context)
	Run(ctx context.Context)
}

// Estimator is the non-resolving playback surface for detail pages.
type Estimator interface {
	Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo
	SessionFor(int64, string, core.RatingKey) (*core.PlaySession, bool)
}

// Estimate returns an existing source's media information or a virtual
// estimate. It never prepares or resolves a release.
func Estimate(ctx context.Context, playback Estimator, viewer *core.Viewer, clientID string, title *core.Title, caps func() core.ClientCaps) (core.MediaInfo, *core.PlaySession) {
	if playback == nil {
		return core.MediaInfo{Estimated: true, Duration: title.Runtime}, nil
	}
	if viewer != nil {
		if ps, ok := playback.SessionFor(viewer.ID, clientID, title.Key); ok && ps != nil {
			return ps.Info, ps
		}
	}
	return playback.Estimate(ctx, title, caps()), nil
}

// PlexActivityStore is the broker's Plex session-list store.
type PlexActivityStore interface {
	HasPlexActivity(id string) bool
	EnsurePlexActivity(id string, key int)
	UpsertPlexActivity(id string, fn func(*PlexActivity)) PlexActivity
	RemovePlexActivity(id string) (PlexActivity, bool)
	ListPlexActivities() []PlexActivity
	ExpirePlexActivities(now time.Time, ttl time.Duration) []PlexActivity
}

// PlexActivitySource reaches the Plex activity store from a session broker
// or its surface binding.
type PlexActivitySource interface {
	PlexActivities() PlexActivityStore
}

// PlexSessionStores constructs the session stores Plex owns and provides
// the package-level session helpers Plex calls. Implemented in
// internal/sessions, wired by the app (and spike).
type PlexSessionStores interface {
	NewCoordination() Coordination
	NewDecisionArms(ctx context.Context, db *sql.DB, now func() time.Time, node string, log *slog.Logger, wg *sync.WaitGroup) DecisionArms
	NewStartGuard() StartGuard
	NewPlayQueues() PlayQueues
	NewVersionFlows(ctx context.Context, db *sql.DB) VersionFlows
	NewDownloadQueueStore() DownloadQueueStore
	NewVariantIDs(db *sql.DB, now func() time.Time) VariantIDs
	NewPlexStreamTable() PlexStreamTable
	NewDownloadJob(playback core.Playback, remuxer core.DownloadRemuxer) DownloadJob
	NewDownloadQueuePersistence(queue DownloadQueueStore, db *sql.DB, artifacts core.DownloadRemuxer, log *slog.Logger, expiry func() time.Duration, storageID func() string, users func(context.Context) ([]core.User, error)) DownloadQueuePersistence
	NewPlexSources(playback core.Playback, log *slog.Logger) PlexSources
	ObserveStart(ctx context.Context, source core.HLSSession, id string) (StartObservation, bool)
	EncodeQueue(q *PlayQueue) (string, error)
	DecodeQueue(id, userID, rev, updated int64, data string) (*PlayQueue, error)
	ShuffleQueue(p *PlayQueue)
	UnshuffleQueue(p *PlayQueue)
	QueueItemIndex(items []QueueItem, id int64) int
	VariantHash(key core.RatingKey, releaseID string) int64
	UnderlyingPlexActivityStore(s Sessions) PlexActivityStore
}

// PlexAdmission is the source and the version and track selections for one
// Plex plan. Plan makes the direct, remux, or transcode decision from them.
// Open publishes that decision. The adapter renders it and does not decide again.
type plexAdmissionKey struct{}

// WithPlexAdmission attaches the prepared source for one Plex Plan call.
func WithPlexAdmission(ctx context.Context, admit func(context.Context) (PlexAdmission, error)) context.Context {
	if admit == nil {
		return ctx
	}
	return context.WithValue(ctx, plexAdmissionKey{}, admit)
}

// PlexAdmissionFrom returns the admission callback WithPlexAdmission attached.
func PlexAdmissionFrom(ctx context.Context) (func(context.Context) (PlexAdmission, error), bool) {
	admit, ok := ctx.Value(plexAdmissionKey{}).(func(context.Context) (PlexAdmission, error))
	return admit, ok && admit != nil
}

type plexServe struct {
	remote bool
	format string
}

type plexServeKey struct{}

// WithPlexServe carries the request's remote bit and subtitle format into
// ResolvePlexStream and OpenPlexSidecar without changing their signatures.
func WithPlexServe(ctx context.Context, remote bool, format string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, plexServeKey{}, plexServe{remote: remote, format: format})
}

// PlexServeFrom returns the remote bit and subtitle format WithPlexServe attached.
func PlexServeFrom(ctx context.Context) (remote bool, format string) {
	if ctx == nil {
		return false, ""
	}
	v, _ := ctx.Value(plexServeKey{}).(plexServe)
	return v.remote, v.format
}

// HLSDecision applies the v39 start-time decision rules. A client may reject
// direct play after its initial decision; in that case reconsider is called
// before selecting the segmented output. forcedStream, when supplied, applies
// adapter output constraints only when a still-direct decision is forced to HLS.
func HLSDecision(dec core.Decision, allowDirect bool, reconsider func() core.Decision, forcedStream func(core.Decision) core.Decision) core.Decision {
	if dec.Kind == core.DecisionDirectPlay && !allowDirect {
		dec = reconsider()
	}
	if dec.Kind == core.DecisionDirectPlay {
		dec.Kind, dec.VideoCopy, dec.AudioCopy, dec.Container = core.DecisionRemux, true, true, "mpegts"
		if forcedStream != nil {
			dec = forcedStream(dec)
		}
	}
	if dec.Container == "" {
		dec.Container = "mpegts"
	}
	return dec
}

// NotifyTranscodeActivity forwards timeline state to an active output. It is
// deliberately a no-op for transcoders and outputs without this capability.
func NotifyTranscodeActivity(transcoder core.Transcoder, id, state string) {
	if transcoder == nil {
		return
	}
	if output, ok := transcoder.Session(id); ok {
		if activity, ok := output.(core.SessionActivity); ok {
			activity.PlaybackState(state)
		}
	}
}

// Version flow modes for VersionFlows.Call.
const (
	VersionRead = iota
	VersionOffer
	VersionFreeze
	VersionEnd
	VersionTouch
)

// MaxPlayQueues bounds the play-queue store.
const MaxPlayQueues = 2000

// VersionPreplayTTL bounds a version flow's preplay snapshot.
const VersionPreplayTTL = 90 * time.Second

const (
	VariantIDBase  = 9_000_000_000_000_000
	VariantIDSpace = 7_199_254_740_990 // ids stay below 2^53
)

// VariantHash is the stable hash slot for a variant id.
func VariantHash(key core.RatingKey, releaseID string) int64 {
	h := fnv.New64a()
	h.Write([]byte(strconv.FormatInt(int64(key), 10) + "/" + releaseID))
	return VariantIDBase + int64(h.Sum64()%VariantIDSpace) + 1
}
