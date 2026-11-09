package updater

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// Manager states reported by Status.
const (
	StateIdle          = "idle"
	StateDownloading   = "downloading"
	StateStaged        = "staged"
	StateWaitingWindow = "waiting-for-window"
	StateWaitingIdle   = "waiting-for-idle"
	StateApplying      = "applying"
	StateRolledBack    = "rolled-back"
	// StateWaitingRollout: a staged rollout holds this server's automatic
	// install until Status().InstallAfter.
	StateWaitingRollout = "waiting-for-rollout"
	// Clustered nodes only (see Coordinator).
	StateWaitingTurn = "waiting-for-turn" // another node holds the update lease
	StateDraining    = "draining"         // lease held; waiting for this node's sessions to end
)

// Capabilities reported by Status.
const (
	CapabilitySelfApply  = "self-apply"
	CapabilityNotifyOnly = "notify-only"
	CapabilityDisabled   = "disabled"
)

// Manager defaults.
const (
	// DefaultPollInterval: the feed is one small static signed file, so
	// checking every few hours costs nothing and lets a fix (or a pulled
	// release) reach servers the same day.
	DefaultPollInterval = 6 * time.Hour
	// DefaultPollJitter spreads checks of servers that started together
	// (a power cut, a fleet reboot) so they never stampede the feed.
	DefaultPollJitter      = 30 * time.Minute
	DefaultFirstCheckDelay = 5 * time.Minute
	DefaultRetryInterval   = time.Hour
	DefaultQuietPeriod     = 10 * time.Minute
	DefaultHealthPeriod    = 10 * time.Minute
	DefaultTick            = 30 * time.Second
	DefaultWindowStart     = "03:00"
	DefaultWindowEnd       = "06:00"
	// DefaultMaxDrain bounds a clustered node's drain before an install.
	DefaultMaxDrain = 30 * time.Minute
	// DefaultMissedWindows: an automatic install that found no quiet
	// moment in this many maintenance windows (a server busy every night)
	// may install at the next long quiet moment outside the window.
	DefaultMissedWindows = 3
	// DefaultCatchUpQuiet is that long quiet moment.
	DefaultCatchUpQuiet = 30 * time.Minute
	// DefaultRecheckBefore: an automatic install first re-reads the feed
	// when the last good check is older than this, so a release pulled
	// meanwhile is never installed.
	DefaultRecheckBefore = time.Hour
	// installedNoticeFor bounds how long Status reports a finished update.
	installedNoticeFor = 7 * 24 * time.Hour
	// bootNoticeFor: a record older than this at start is not announced.
	bootNoticeFor = 24 * time.Hour
)

var (
	// ErrUpdatesDisabled: this build cannot verify updates (no release key)
	// or the feed settings are unusable.
	ErrUpdatesDisabled = errors.New("updater: updates are disabled in this build")
	// ErrNotSelfApplicable: the install is notify-only (package manager,
	// container, macOS app bundle, read-only install dir, ...).
	ErrNotSelfApplicable = errors.New("updater: this installation cannot update itself")
	// ErrClusterRolledBack is returned by Coordinator.AcquireUpdate for a
	// release that rolled back on another node of the cluster.
	ErrClusterRolledBack = errors.New("updater: this release was rolled back on another cluster node")
)

// Coordinator serialises self-updates across a cluster (rolling updates).
// With a Coordinator the manager installs a release only while holding the
// cluster-wide update lease, so nodes restart one at a time; before
// installing it drains this node (no new sessions, out of the published
// connections) and waits for this node's own sessions to end, up to
// Updates.MaxDrain. The lease stays held through the restart: the new
// process renews it while confirming its health and releases it once
// confirmed. A process that finds no update to confirm releases a lease
// this node still holds; one that rolled back reports the failed release
// so the other nodes skip it.
type Coordinator interface {
	// AcquireUpdate takes, or renews, the update lease for version. ok is
	// false while another node (holder) holds it; err wraps
	// ErrClusterRolledBack for a release that rolled back elsewhere.
	AcquireUpdate(ctx context.Context, version string) (ok bool, holder string, err error)
	// ReleaseUpdate gives the lease back if this node holds it.
	ReleaseUpdate(ctx context.Context) error
	// ReportFailed records that version rolled back on this node.
	ReportFailed(ctx context.Context, version string) error
	// SetDraining takes this node out of (true) or back into (false) the
	// rotation for new sessions.
	SetDraining(draining bool)
}

// Checker is the slice of *Client the Manager needs.
type Checker interface {
	Check(ctx context.Context) (*Update, error)
	Download(ctx context.Context, upd *Update, dir string, progress ProgressFunc) (string, error)
}

// ManagerOptions configures a Manager. Only Dir and Config are required.
type ManagerOptions struct {
	// NewClient builds the feed client for the current settings; it is
	// rebuilt when channel/feed change. Nil or failing at construction:
	// update checks are disabled (startup confirmation still runs).
	NewClient func(config.Updates) (Checker, error)
	// Config returns the live Updates settings.
	Config func() config.Updates
	// CurrentVersion is the running version (default: Version).
	CurrentVersion string
	// Dir is <dataDir>/updates: downloads and marker files.
	Dir string
	// Target is the running server executable that a binary update
	// replaces (default: os.Executable()).
	Target string
	// SelfApply reports whether this install may replace its own binary;
	// NotifyReason explains why not. See SelfApplyCapability.
	SelfApply    bool
	NotifyReason string
	// ActiveSessions returns how many playback / transcode / live TV
	// sessions are in progress. Nil: always idle.
	ActiveSessions func() int
	// RequestRestart asks the app to shut down gracefully and exit so the
	// service manager starts the new binary. Called at most once.
	RequestRestart func(reason string)
	// Boot is the result of ConfirmStartup for this process.
	Boot StartupResult
	// Coordinator, when set (a clustered node that can self-apply),
	// coordinates installs with the other nodes; nil keeps the standalone
	// behaviour (install after the server has been idle for QuietPeriod).
	Coordinator Coordinator
	// NodeName labels this node in status (the lease holder is compared
	// with it); optional.
	NodeName string
	// OnAvailable, when set, hears each release version a check finds,
	// without the manager's lock held. Once it returns nil for a version it
	// is not called for that version again in this process; an error makes
	// the next check that finds the version call it again.
	OnAvailable func(version string) error
	// OnInstalled / OnRolledBack, when set, hear an update confirmed
	// healthy and a rolled-back one, without the manager's lock held. An
	// error retries on the next step; implementations dedupe by version.
	OnInstalled    func(u InstalledUpdate) error
	OnRolledBack   func(f FailedUpdate) error
	OnStatusChange func()
	// InstallID is a stable identity of this server (its machine
	// identifier). It is hashed locally into the staged-rollout bucket and
	// never sent anywhere. Empty: bucket 0 (no rollout wait).
	InstallID string
	// NotifyKind classifies a notify-only install (see SelfApplyMode) so
	// the console can show the exact way to update it.
	NotifyKind string
	// GOOS/GOARCH pick the download shown to notify-only installs
	// (default: runtime).
	GOOS, GOARCH string

	Now             func() time.Time
	Location        *time.Location // maintenance window zone (default time.Local)
	PollInterval    time.Duration
	PollJitter      time.Duration // < 0: none
	FirstCheckDelay time.Duration
	RetryInterval   time.Duration
	QuietPeriod     time.Duration
	HealthPeriod    time.Duration
	Tick            time.Duration
	MissedWindows   int
	CatchUpQuiet    time.Duration
	RecheckBefore   time.Duration
	// Rand returns a uniform value in [0,1) for poll jitter (default
	// math/rand/v2).
	Rand func() float64
	// ApplyBinary / CleanupOld default to the package functions.
	ApplyBinary func(newPath, target string) error
	CleanupOld  func(target string) error
	Logger      *slog.Logger
}

// Manager polls the signed feed and, when allowed, downloads a release
// early and installs it at a safe moment: inside the maintenance window
// (unless the owner pressed "Update now") and after the server has been
// idle for the quiet period (unless forced).
type Manager struct {
	opts ManagerOptions
	log  *slog.Logger
	kick chan struct{}

	mu             sync.Mutex
	announced      string // last version OnAvailable accepted
	client         Checker
	clientKey      string
	disabledReason string
	noKey          bool // build has no release key: never check
	state          string
	available      *Update
	failed         *FailedUpdate
	staged         string // verified download for available
	stagedVersion  string
	requested      bool
	force          bool
	lastCheck      time.Time
	lastOK         time.Time // last check that read the feed
	nextCheck      time.Time
	autoSince      time.Time // automatic installs on since (this process)
	bucket         float64   // staged-rollout position in [0,1)
	installed      *InstalledUpdate
	announceDone   *InstalledUpdate // still to report through OnInstalled
	announceFailed *FailedUpdate    // still to report through OnRolledBack
	downloadAfter  time.Time
	applyAfter     time.Time
	lastErr        string
	lastActive     int
	idleSince      time.Time
	startedAt      time.Time
	confirmed      bool
	restarting     bool
	checking       bool
	checkPending   bool

	// Rolling updates (Coordinator set).
	leaseHeld      bool      // lease taken by this process for a pending install
	draining       bool      // SetDraining(true) in effect
	drainStart     time.Time // when draining began
	holder         string    // node holding the lease, as last seen
	releasePending bool      // a lease from a previous update must be released
	reportFailed   string    // a rolled-back version still to report
}

const disabledBySettingsReason = "update checks are turned off in Maintenance › Updates"

// NewManager builds a Manager; it does not start polling (see Run).
func NewManager(opts ManagerOptions) *Manager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.CurrentVersion == "" {
		opts.CurrentVersion = Version
	}
	if opts.Target == "" {
		if exe, err := os.Executable(); err == nil {
			opts.Target = exe
		}
	}
	if opts.Config == nil {
		opts.Config = func() config.Updates { return config.Updates{Enabled: true, Channel: ChannelStable} }
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&opts.PollInterval, DefaultPollInterval)
	def(&opts.FirstCheckDelay, DefaultFirstCheckDelay)
	def(&opts.RetryInterval, DefaultRetryInterval)
	def(&opts.QuietPeriod, DefaultQuietPeriod)
	def(&opts.HealthPeriod, DefaultHealthPeriod)
	def(&opts.Tick, DefaultTick)
	def(&opts.CatchUpQuiet, DefaultCatchUpQuiet)
	def(&opts.RecheckBefore, DefaultRecheckBefore)
	switch {
	case opts.PollJitter == 0:
		opts.PollJitter = DefaultPollJitter
	case opts.PollJitter < 0:
		opts.PollJitter = 0
	}
	if opts.MissedWindows <= 0 {
		opts.MissedWindows = DefaultMissedWindows
	}
	if opts.Rand == nil {
		opts.Rand = rand.Float64
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.ApplyBinary == nil {
		opts.ApplyBinary = ApplyBinary
	}
	if opts.CleanupOld == nil {
		opts.CleanupOld = CleanupOld
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SelfApply && opts.Target == "" {
		opts.SelfApply, opts.NotifyReason = false, "cannot locate the running executable"
	}
	m := &Manager{opts: opts, log: opts.Logger, kick: make(chan struct{}, 1), state: StateIdle,
		bucket: RolloutBucket(opts.InstallID)}
	if opts.Boot.Confirming == nil {
		m.confirmed = true
	}
	var f FailedUpdate
	if ok, _ := readJSON(FailedPath(opts.Dir), &f); ok {
		m.failed = &f
	}
	if opts.Boot.RolledBack != nil {
		m.failed = opts.Boot.RolledBack
	}
	var inst InstalledUpdate
	if ok, _ := readJSON(InstalledPath(opts.Dir), &inst); ok && sameVersion(inst.To, opts.CurrentVersion) {
		m.installed = &inst
	}
	// Re-announce recent outcomes a previous process may not have
	// delivered (it restarted, or the rollback process exited at once);
	// the hooks dedupe by version.
	now := opts.Now()
	if m.installed != nil && now.Sub(m.installed.At) < bootNoticeFor {
		cp := *m.installed
		m.announceDone = &cp
	}
	if m.failed != nil && now.Sub(m.failed.At) < bootNoticeFor {
		cp := *m.failed
		m.announceFailed = &cp
	}
	if opts.NewClient == nil {
		m.disabledReason = ErrUpdatesDisabled.Error()
	} else {
		cfg := opts.Config()
		if !cfg.Enabled {
			m.disabledReason = disabledBySettingsReason
		} else if c, err := opts.NewClient(cfg); err != nil {
			m.disabledReason = err.Error()
			m.noKey = errors.Is(err, ErrNoPublicKey)
		} else {
			m.client, m.clientKey = c, clientKey(cfg)
		}
	}
	return m
}

func clientKey(u config.Updates) string { return u.Channel + "\x00" + u.FeedURL }

// Enabled reports whether update checks run.
func (m *Manager) Enabled() bool {
	cfg := m.opts.Config()
	m.mu.Lock()
	defer m.mu.Unlock()
	return cfg.Enabled && m.client != nil
}

// Wake prompts the manager to re-evaluate settings immediately.
func (m *Manager) Wake() {
	m.wake()
}

func (m *Manager) statusSig() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var avail, inst, fail string
	if m.available != nil {
		avail = m.available.Version()
	}
	if m.installed != nil {
		inst = m.installed.To
	}
	if m.failed != nil {
		fail = m.failed.Version + "|" + m.failed.Reason
	}
	return fmt.Sprintf("%s|%s|%t|%t|%t|%t|%s|%s|%s|%s|%s",
		m.state, m.lastErr, m.checking, m.checkPending, m.confirmed, m.requested,
		m.disabledReason, m.staged, avail, inst, fail)
}

func (m *Manager) notifyStatusChanged(prev string) {
	if m.opts.OnStatusChange != nil && m.statusSig() != prev {
		m.opts.OnStatusChange()
	}
}

// CheckNow schedules a feed check, coalescing concurrent requests.
func (m *Manager) CheckNow() error {
	prev := m.statusSig()
	defer func() { m.notifyStatusChanged(prev) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.opts.Config().Enabled || m.noKey || m.opts.NewClient == nil {
		return ErrUpdatesDisabled
	}
	if !m.checking && !m.checkPending {
		m.checkPending = true
		m.nextCheck = time.Time{}
		m.wake()
	}
	return nil
}

// Run drives the manager until ctx ends or a restart has been requested.
func (m *Manager) Run(ctx context.Context) {
	m.begin()
	t := time.NewTicker(m.opts.Tick)
	defer t.Stop()
	for {
		m.step(ctx)
		if m.isRestarting() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

func (m *Manager) begin() {
	now := m.opts.Now().Round(0)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startedAt = now
	m.idleSince = now
	if m.opts.Coordinator != nil {
		switch {
		case m.opts.Boot.RolledBack != nil:
			m.reportFailed = m.opts.Boot.RolledBack.Version
		case m.opts.Boot.Confirming == nil:
			// Nothing to confirm: a lease this node still holds is left over
			// (an update that did not take effect, or a crash) — free it.
			m.releasePending = true
			// The rollback process restarted before it could tell the
			// cluster: report the restored-from release now, before the
			// lease goes back to nodes that would install it next.
			if m.failed != nil && m.failed.ReportPending {
				m.reportFailed = m.failed.Version
			}
		}
	}
	if !m.requested && !m.checkPending { // Requests before Run keep their immediate check
		m.nextCheck = now.Add(m.opts.FirstCheckDelay)
	}
}

func (m *Manager) isRestarting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restarting
}

func (m *Manager) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// step is one evaluation of the state machine at the injected clock.
func (m *Manager) step(ctx context.Context) {
	prev := m.statusSig()
	defer func() { m.notifyStatusChanged(prev) }()
	now := m.opts.Now().Round(0)
	m.confirmIfHealthy(now)
	m.announce()
	m.coordinate(ctx)
	cfg := m.opts.Config()
	feedKey := clientKey(cfg)
	active := 0
	if m.opts.ActiveSessions != nil {
		active = m.opts.ActiveSessions()
	}
	m.mu.Lock()
	m.lastActive = active
	switch {
	case !cfg.AutoUpdate:
		m.autoSince = time.Time{}
	case m.autoSince.IsZero():
		m.autoSince = now
	}
	if active > 0 {
		m.idleSince = time.Time{}
	} else if m.idleSince.IsZero() {
		m.idleSince = now
	}
	if !cfg.Enabled {
		m.client, m.clientKey = nil, ""
		m.disabledReason = disabledBySettingsReason
		m.resetAvailableLocked()
		m.requested, m.force = false, false
		m.checkPending = false
		m.state = StateIdle
		m.mu.Unlock()
		m.stopDrain(ctx)
		return
	}
	enabled := m.opts.NewClient != nil && !m.noKey
	// Correcting a badly advanced system clock must not defer checks for
	// days. Keep at most one normal polling interval after the correction.
	if now.Before(m.lastCheck) && m.nextCheck.Sub(now) > m.opts.PollInterval {
		m.nextCheck = now.Add(m.opts.PollInterval)
	}
	// A channel/feed change in settings, or turning checks back on, re-checks right away.
	due := !now.Before(m.nextCheck) || m.client == nil || feedKey != m.clientKey
	stop := m.restarting
	m.mu.Unlock()
	if stop || !enabled {
		return
	}
	if due {
		m.check(ctx, now)
	}
	m.advance(ctx, now)
}

// confirmIfHealthy finishes a self-update once the new version has run for
// the health period: drop the marker, the rollback copy and old downloads.
func (m *Manager) confirmIfHealthy(now time.Time) {
	m.mu.Lock()
	if m.confirmed || m.startedAt.IsZero() || now.Sub(m.startedAt) < m.opts.HealthPeriod {
		m.mu.Unlock()
		return
	}
	m.confirmed = true
	p := m.opts.Boot.Confirming
	// A newer release may already be staged (checks run while confirming);
	// it must survive the cleanup of the confirmed update's download.
	keep := m.staged
	m.mu.Unlock()

	if err := removeIfExists(PendingPath(m.opts.Dir)); err != nil {
		m.log.Warn("updater: remove update marker", "err", err)
	}
	target := p.Target
	if target == "" {
		target = m.opts.Target
	}
	cleanupLeftovers(target, m.opts.CleanupOld, m.log)
	m.cleanDownloads(keep)
	forgetFailedIfSuperseded(m.opts.Dir, m.opts.CurrentVersion, m.log)
	inst := &InstalledUpdate{From: p.From, To: p.To, At: p.AppliedAt}
	if inst.At.IsZero() {
		inst.At = now
	}
	if err := writeJSONAtomic(InstalledPath(m.opts.Dir), inst); err != nil {
		m.log.Warn("updater: record the finished update", "err", err)
	}
	m.mu.Lock()
	if m.failed != nil && !Newer(m.failed.Version, m.opts.CurrentVersion) {
		m.failed = nil
	}
	m.installed = inst
	cp := *inst
	m.announceDone = &cp
	if m.opts.Coordinator != nil {
		m.releasePending = true // the next node may update now
	}
	m.mu.Unlock()
	m.log.Info("updater: update confirmed healthy", "from", p.From, "version", p.To)
}

// announce reports a finished or rolled-back update through the owner's
// notification hooks; a failed delivery is retried on the next step.
func (m *Manager) announce() {
	m.mu.Lock()
	done, failed := m.announceDone, m.announceFailed
	m.mu.Unlock()
	if done != nil && m.opts.OnInstalled != nil {
		if err := m.opts.OnInstalled(*done); err != nil {
			m.log.Debug("updater: announce finished update", "version", done.To, "err", err)
		} else {
			m.mu.Lock()
			if m.announceDone == done {
				m.announceDone = nil
			}
			m.mu.Unlock()
		}
	}
	if failed != nil && m.opts.OnRolledBack != nil {
		if err := m.opts.OnRolledBack(*failed); err != nil {
			m.log.Debug("updater: announce rolled-back update", "version", failed.Version, "err", err)
		} else {
			m.mu.Lock()
			if m.announceFailed == failed {
				m.announceFailed = nil
			}
			m.mu.Unlock()
		}
	}
}

// DismissInstalled hides the "Updated to X" note in Status.
func (m *Manager) DismissInstalled() error {
	prev := m.statusSig()
	defer func() { m.notifyStatusChanged(prev) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installed = nil
	return removeIfExists(InstalledPath(m.opts.Dir))
}

func (m *Manager) check(ctx context.Context, now time.Time) {
	cfg := m.opts.Config()
	if !cfg.Enabled {
		m.mu.Lock()
		m.checkPending = false
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.checkPending = false
	m.checking = true
	defer func() { m.mu.Lock(); m.checking = false; m.mu.Unlock() }()
	if key := clientKey(cfg); m.client == nil || key != m.clientKey {
		c, err := m.opts.NewClient(cfg)
		if err != nil {
			m.noKey = errors.Is(err, ErrNoPublicKey)
			if m.noKey {
				m.disabledReason = err.Error()
			} else {
				m.lastErr = "update feed settings: " + err.Error()
				m.nextCheck = now.Add(m.opts.RetryInterval)
			}
			m.mu.Unlock()
			return
		}
		m.client, m.clientKey, m.disabledReason = c, key, ""
		m.resetAvailableLocked()
	}
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return
	}

	upd, err := client.Check(ctx)

	var announce string
	defer func() {
		// Runs after the unlock below.
		if announce == "" || m.opts.OnAvailable == nil {
			return
		}
		if err := m.opts.OnAvailable(announce); err != nil {
			m.log.Warn("updater: update announcement failed; retrying at the next check", "version", announce, "err", err)
			return
		}
		m.mu.Lock()
		m.announced = announce
		m.mu.Unlock()
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCheck = now
	if err != nil {
		if ctx.Err() == nil {
			m.lastErr = "check: " + err.Error()
			m.log.Warn("updater: check failed", "err", err)
		}
		m.nextCheck = now.Add(m.opts.RetryInterval)
		return
	}
	m.nextCheck = now.Add(m.pollDelay())
	m.lastOK = now
	m.lastErr = ""

	if upd != nil && m.failed != nil && sameVersion(upd.Version(), m.failed.Version) {

		m.log.Info("updater: skipping previously rolled-back version", "version", upd.Version())
		upd = nil
	}
	if upd == nil {
		m.resetAvailableLocked()
		m.requested, m.force = false, false
		return
	}
	if m.available == nil || m.available.Version() != upd.Version() {
		m.resetAvailableLocked()
		m.log.Info("updater: update available", "current", m.opts.CurrentVersion, "available", upd.Version(), "kind", upd.Artifact.Kind)
	}
	if upd.Version() != m.announced {
		announce = upd.Version()
	}
	m.available = upd
}

// pollDelay is the poll interval with random jitter, never shorter than
// half the interval.
func (m *Manager) pollDelay() time.Duration {
	d := m.opts.PollInterval
	if j := m.opts.PollJitter; j > 0 {
		d += time.Duration((m.opts.Rand()*2 - 1) * float64(j))
	}
	return max(d, m.opts.PollInterval/2)
}

// resetAvailableLocked forgets the offered release. A download staged for
// it is deleted: the feed no longer offers it (pulled, or superseded), so
// it must never be installed.
func (m *Manager) resetAvailableLocked() {
	if m.staged != "" {
		_ = os.Remove(m.staged)
	}
	m.available = nil
	m.staged, m.stagedVersion = "", ""
	m.downloadAfter, m.applyAfter = time.Time{}, time.Time{}
}

// selfApplicableLocked reports whether upd can be installed by this process.
func (m *Manager) selfApplicableLocked(upd *Update) (bool, string) {
	if !m.opts.SelfApply {
		return false, m.opts.NotifyReason
	}
	if upd != nil && upd.Artifact.Kind != KindBinary {
		return false, "this release is distributed as an installer for this platform"
	}
	return true, ""
}

func (m *Manager) advance(ctx context.Context, now time.Time) {
	cfg := m.opts.Config()
	m.mu.Lock()
	upd := m.available
	if upd == nil {
		m.state = StateIdle
		m.mu.Unlock()
		m.stopDrain(ctx)
		return
	}
	if ok, _ := m.selfApplicableLocked(upd); !ok || !(cfg.AutoUpdate || m.requested) {
		m.state = StateIdle
		m.mu.Unlock()
		m.stopDrain(ctx)
		return
	}
	// A staged rollout holds this server's automatic install (and its
	// download, so a release pulled meanwhile is never fetched).
	if !m.requested && now.Before(m.installAfter(upd)) {
		m.state = StateWaitingRollout
		m.mu.Unlock()
		m.stopDrain(ctx)
		return
	}
	if m.staged == "" || m.stagedVersion != upd.Version() {
		if now.Before(m.downloadAfter) {
			m.state = StateIdle
			m.mu.Unlock()
			return
		}
		m.state = StateDownloading
		m.mu.Unlock()
		path, err := m.client.Download(ctx, upd, m.opts.Dir, nil)
		m.mu.Lock()
		if m.available != upd { // feed moved on meanwhile
			m.mu.Unlock()
			if err == nil {
				_ = os.Remove(path)
			}
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				m.lastErr = "download: " + err.Error()
				m.log.Warn("updater: download failed", "version", upd.Version(), "err", err)
			}
			m.downloadAfter = now.Add(m.opts.RetryInterval)
			m.state = StateIdle
			m.mu.Unlock()
			return
		}
		m.staged, m.stagedVersion = path, upd.Version()
		m.lastErr = ""
		m.state = StateStaged
		m.mu.Unlock()
		m.cleanDownloads(path)
		m.log.Info("updater: update downloaded and verified; waiting for a safe moment", "version", upd.Version())
		m.mu.Lock()
	}

	if m.opts.Coordinator != nil {
		staged := m.staged
		m.mu.Unlock()
		m.advanceCoordinated(ctx, now, cfg, upd, staged)
		return
	}
	pl := m.planLocked(now, cfg, upd)
	switch {
	case !pl.open && !pl.early:
		m.state = StateWaitingWindow
	case !m.confirmed:
		// Never stack a second update on one still being confirmed.
		m.state = StateWaitingIdle
	case !(m.requested && m.force) && !m.quietForLocked(now, pl.quiet):
		m.state = StateWaitingIdle
	case now.Before(m.applyAfter):
		m.state = StateStaged
	case m.needsRecheckLocked(now):
		m.state = StateStaged
	default:
		m.state = StateApplying
		staged := m.staged
		m.mu.Unlock()
		m.apply(now, upd, staged)
		return
	}
	m.mu.Unlock()
}

// installPlan is when a staged update may install.
type installPlan struct {
	// open: inside the maintenance window, or the owner asked.
	open bool
	// early: outside the window, but a critical release or one that has
	// waited through MissedWindows windows may install once quiet.
	early   bool
	catchUp bool
	// quiet is how long the server must have been idle.
	quiet time.Duration
}

func (m *Manager) planLocked(now time.Time, cfg config.Updates, upd *Update) installPlan {
	p := installPlan{quiet: m.opts.QuietPeriod}
	p.open = m.requested || inWindow(now.In(m.opts.Location), cfg)
	switch {
	case p.open:
	case upd.Manifest.Critical:
		p.early = true
	case m.missedWindowsLocked(now, cfg, upd) >= m.opts.MissedWindows:
		p.early, p.catchUp, p.quiet = true, true, m.opts.CatchUpQuiet
	}
	return p
}

// quietForLocked reports whether nothing has played for d.
func (m *Manager) quietForLocked(now time.Time, d time.Duration) bool {
	return m.lastActive == 0 && !m.idleSince.IsZero() && now.Sub(m.idleSince) >= d
}

// missedWindowsLocked counts the maintenance windows that ended while the
// update was due to install automatically: after it cleared the rollout,
// with automatic installs on in this process.
func (m *Manager) missedWindowsLocked(now time.Time, cfg config.Updates, upd *Update) int {
	from := m.installAfter(upd)
	if m.autoSince.After(from) {
		from = m.autoSince
	}
	return windowEndsBetween(from, now, cfg, m.opts.Location)
}

// needsRecheckLocked holds an automatic install until the feed has been
// read within RecheckBefore, so a release pulled since the last check is
// never installed. It schedules that check at most once per RecheckBefore,
// so an unreachable feed is not hammered.
func (m *Manager) needsRecheckLocked(now time.Time) bool {
	if m.requested || now.Sub(m.lastOK) <= m.opts.RecheckBefore {
		return false
	}
	if now.Sub(m.lastCheck) > m.opts.RecheckBefore && m.nextCheck.After(now) {
		m.nextCheck = now
		m.wake()
	}
	return true
}

// installAfter is when the staged rollout lets this server install upd on
// its own: PublishedAt plus its bucket's share of RolloutHours. Zero when
// the release is critical or not staged.
func (m *Manager) installAfter(upd *Update) time.Time {
	mf := upd.Manifest
	if mf.Critical || mf.RolloutHours <= 0 || mf.PublishedAt.IsZero() {
		return time.Time{}
	}
	span := time.Duration(min(mf.RolloutHours, MaxRolloutHours)) * time.Hour
	return mf.PublishedAt.Add(time.Duration(m.bucket * float64(span)))
}

// RolloutBucket maps an install identity to a stable position in [0,1)
// for staged rollouts. Only a local hash is used; nothing leaves the server.
func RolloutBucket(id string) float64 {
	if id == "" {
		return 0
	}
	sum := sha256.Sum256([]byte("ether-update-rollout:" + id))
	return float64(binary.BigEndian.Uint64(sum[:8])>>11) / (1 << 53)
}

// windowEndsBetween counts maintenance-window ends in (from, to].
func windowEndsBetween(from, to time.Time, cfg config.Updates, loc *time.Location) int {
	s, e := windowBounds(cfg)
	start, _ := parseClock(s)
	end, _ := parseClock(e)
	if start == end || from.IsZero() || !to.After(from) {
		return 0 // an always-open window cannot be missed
	}
	from, to = from.In(loc), to.In(loc)
	n := 0
	for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc); !d.After(to) && n < 100; d = d.AddDate(0, 0, 1) {
		t := time.Date(d.Year(), d.Month(), d.Day(), end/60, end%60, 0, 0, loc)
		if t.After(from) && !t.After(to) {
			n++
		}
	}
	return n
}

func (m *Manager) apply(now time.Time, upd *Update, staged string) {
	fail := func(msg string, err error, redownload bool) {
		m.log.Error("updater: "+msg, "version", upd.Version(), "err", err)
		m.mu.Lock()
		m.lastErr = msg + ": " + err.Error()
		m.applyAfter = now.Add(m.opts.RetryInterval)
		if redownload {
			m.staged, m.stagedVersion = "", ""
		}
		m.state = StateStaged
		m.mu.Unlock()
	}
	// The download sat on disk while we waited; verify it again.
	if err := VerifyFile(staged, upd.Artifact); err != nil {
		_ = os.Remove(staged)
		fail("staged update failed verification", err, true)
		return
	}
	target := m.opts.Target
	p := Pending{From: m.opts.CurrentVersion, To: upd.Version(), Target: target, AppliedAt: now}
	if err := writeJSONAtomic(PendingPath(m.opts.Dir), &p); err != nil {
		fail("write update marker", err, false)
		return
	}
	if err := m.opts.ApplyBinary(staged, target); err != nil {
		_ = removeIfExists(PendingPath(m.opts.Dir))
		fail("install update", err, false)
		return
	}
	m.log.Info("updater: update installed; restarting", "from", p.From, "to", p.To, "target", target)
	m.mu.Lock()
	m.restarting = true
	m.mu.Unlock()
	if m.opts.RequestRestart != nil {
		m.opts.RequestRestart(fmt.Sprintf("updated %s → %s", p.From, p.To))
	}
}

// coordinate does the lease bookkeeping that does not depend on an
// available update: renew the lease while confirming a just-installed
// version, release it once confirmed (or when left over), and report a
// rollback.
func (m *Manager) coordinate(ctx context.Context) {
	c := m.opts.Coordinator
	if c == nil {
		return
	}
	m.mu.Lock()
	confirming := !m.confirmed && m.opts.Boot.Confirming != nil
	release, failed := m.releasePending, m.reportFailed
	m.mu.Unlock()
	if failed != "" {
		if err := c.ReportFailed(ctx, failed); err != nil {
			// Keep any lease until the cluster knows: releasing it first
			// would hand the bad release to the next node.
			m.log.Debug("updater: report rolled-back release to the cluster", "err", err)
			return
		}
		m.log.Warn("updater: told the other cluster nodes to skip the rolled-back release", "version", failed)
		m.mu.Lock()
		m.reportFailed = ""
		if m.failed != nil && sameVersion(m.failed.Version, failed) {
			m.failed.ReportPending = false
		}
		m.mu.Unlock()
		m.markFailedReported(failed)
	}
	switch {
	case confirming:
		// Keep the lease while the new version proves itself, so no other
		// node restarts meanwhile.
		if _, _, err := c.AcquireUpdate(ctx, m.opts.CurrentVersion); err != nil {
			m.log.Debug("updater: renew cluster update lease while confirming", "err", err)
		}
	case release:
		if err := c.ReleaseUpdate(ctx); err != nil {
			m.log.Debug("updater: release cluster update lease", "err", err)
			return
		}
		m.mu.Lock()
		m.releasePending = false
		m.mu.Unlock()
	}
}

// markFailedReported clears ReportPending in the failed-version record so
// later starts do not report it again.
func (m *Manager) markFailedReported(version string) {
	var f FailedUpdate
	if ok, err := readJSON(FailedPath(m.opts.Dir), &f); !ok || err != nil || !f.ReportPending || !sameVersion(f.Version, version) {
		return
	}
	f.ReportPending = false
	if err := writeJSONAtomic(FailedPath(m.opts.Dir), &f); err != nil {
		m.log.Warn("updater: record that the rolled-back release was reported", "err", err)
	}
}

// maxDrain is the configured drain bound.
func maxDrain(cfg config.Updates) time.Duration {
	if d := time.Duration(cfg.MaxDrain); d > 0 {
		return d
	}
	return DefaultMaxDrain
}

// advanceCoordinated installs a staged update on a clustered node: inside
// the window (or on request), once this node holds the cluster update
// lease and has drained — its own sessions ended, or MaxDrain passed.
func (m *Manager) advanceCoordinated(ctx context.Context, now time.Time, cfg config.Updates, upd *Update, staged string) {
	c := m.opts.Coordinator
	m.mu.Lock()
	pl := m.planLocked(now, cfg, upd)
	inWin := pl.open
	// Outside the window (a critical release, or one that missed its
	// windows) a node installs only once it has been quiet: it never
	// drains or interrupts anyone in the daytime.
	early := !inWin && pl.early
	quiet := m.quietForLocked(now, pl.quiet)
	confirmed := m.confirmed
	force := m.requested && m.force
	retryWait := now.Before(m.applyAfter)
	// Re-read the feed before taking the lease, not mid-drain.
	recheck := !m.draining && (inWin || (early && quiet)) && confirmed && !retryWait && m.needsRecheckLocked(now)
	m.mu.Unlock()
	if !(inWin || early) || !confirmed || retryWait || (early && !quiet) || recheck {
		m.stopDrain(ctx)
		m.mu.Lock()
		switch {
		case !(inWin || early):
			m.state = StateWaitingWindow
		case !confirmed, early && !quiet:
			m.state = StateWaitingIdle
		default:
			m.state = StateStaged
		}
		m.mu.Unlock()
		return
	}
	ok, holder, err := c.AcquireUpdate(ctx, upd.Version())
	switch {
	case errors.Is(err, ErrClusterRolledBack):
		m.stopDrain(ctx)
		m.mu.Lock()
		m.log.Warn("updater: skipping a release that rolled back on another cluster node", "version", upd.Version())
		m.failed = &FailedUpdate{Version: upd.Version(), From: m.opts.CurrentVersion, At: now,
			Reason: "rolled back on another cluster node"}

		m.resetAvailableLocked()
		m.requested, m.force = false, false
		m.state = StateIdle
		m.mu.Unlock()
		return
	case err != nil:
		// Never install without the lease; keep any drain in place and
		// try again next tick.
		m.mu.Lock()
		m.lastErr = "cluster update lease: " + err.Error()
		if !m.draining {
			m.state = StateWaitingTurn
		}
		m.mu.Unlock()
		return
	case !ok:
		m.stopDrain(ctx) // lost a lease we had (it expired and was taken)
		m.mu.Lock()
		m.holder, m.state = holder, StateWaitingTurn
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.leaseHeld, m.holder = true, m.opts.NodeName
	if m.lastErr != "" && strings.HasPrefix(m.lastErr, "cluster update lease: ") {
		m.lastErr = ""
	}
	start := !m.draining
	if start {
		m.draining, m.drainStart = true, now
	}
	drainStart, active := m.drainStart, m.lastActive
	m.mu.Unlock()
	if start {
		m.log.Info("updater: holding the cluster update lease; draining this node before installing",
			"version", upd.Version(), "activeSessions", active, "maxDrain", maxDrain(cfg))
		c.SetDraining(true)
	}
	if early && active > 0 {
		// Someone started watching: outside the window wait for quiet again.
		m.stopDrain(ctx)
		m.mu.Lock()
		m.state = StateWaitingIdle
		m.mu.Unlock()
		return
	}
	if !force && active > 0 && now.Sub(drainStart) < maxDrain(cfg) {
		m.mu.Lock()
		m.state = StateDraining
		m.mu.Unlock()
		return
	}
	if active > 0 {
		m.log.Warn("updater: drain time is up; installing with sessions still active", "activeSessions", active)
	}
	m.mu.Lock()
	m.state = StateApplying
	m.mu.Unlock()
	m.apply(now, upd, staged)
	if !m.isRestarting() {
		m.stopDrain(ctx) // install failed: let another node have its turn
	}
}

// stopDrain ends a drain and gives back a lease taken for an install that
// is not going ahead.
func (m *Manager) stopDrain(ctx context.Context) {
	c := m.opts.Coordinator
	if c == nil {
		return
	}
	m.mu.Lock()
	if m.restarting {
		m.mu.Unlock()
		return
	}
	draining, held := m.draining, m.leaseHeld
	m.draining, m.drainStart, m.leaseHeld = false, time.Time{}, false
	m.mu.Unlock()
	if draining {
		m.log.Info("updater: install postponed; this node is back in rotation")
		c.SetDraining(false)
	}
	if held {
		if err := c.ReleaseUpdate(ctx); err != nil {
			m.log.Debug("updater: release cluster update lease", "err", err)
			m.mu.Lock()
			m.releasePending = true
			m.mu.Unlock()
		}
	}
}

// cleanDownloads removes old downloaded artifacts, keeping keep and the
// marker files, so years of releases never pile up in the data dir.
func (m *Manager) cleanDownloads(keep string) {
	entries, err := os.ReadDir(m.opts.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == pendingFile || name == failedFile || name == installedFile || strings.HasSuffix(name, ".json.tmp") {
			continue
		}
		p := filepath.Join(m.opts.Dir, name)
		if keep != "" && filepath.Clean(p) == filepath.Clean(keep) {
			continue
		}
		_ = os.Remove(p)
	}
}

// UpdateNow asks for the available update (checking the feed first if none
// is known) to be installed as soon as the server is idle, ignoring the
// maintenance window. force also skips the idle wait.
func (m *Manager) UpdateNow(force bool) error {
	prev := m.statusSig()
	defer func() { m.notifyStatusChanged(prev) }()
	cfg := m.opts.Config()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !cfg.Enabled {
		return fmt.Errorf("%w: %s", ErrUpdatesDisabled, disabledBySettingsReason)
	}
	if m.client == nil {
		if m.disabledReason != "" {
			return fmt.Errorf("%w: %s", ErrUpdatesDisabled, m.disabledReason)
		}
		return ErrUpdatesDisabled
	}
	if ok, why := m.selfApplicableLocked(m.available); !ok {
		return fmt.Errorf("%w: %s", ErrNotSelfApplicable, why)
	}
	if m.restarting {
		return nil
	}
	m.requested = true
	m.force = m.force || force
	m.lastErr = ""
	m.downloadAfter, m.applyAfter = time.Time{}, time.Time{}
	if m.available == nil {
		m.nextCheck = time.Time{}
	}
	m.wake()
	return nil
}

// Window is the maintenance window as reported by Status.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Open  bool   `json:"open"`
}

// AvailableUpdate describes the newest release on the feed.
type AvailableUpdate struct {
	Version     string     `json:"version"`
	Kind        string     `json:"kind"`
	NotesURL    string     `json:"notesUrl,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	Staged      bool       `json:"staged"`
	// Critical: a security fix that installs at the next quiet moment.
	Critical bool `json:"critical,omitempty"`
	// InstallAfter is when the staged rollout lets this server install it
	// on its own (set while that is still ahead).
	InstallAfter *time.Time `json:"installAfter,omitempty"`
	// CatchUp: it found no quiet moment in several maintenance windows and
	// installs the next time nobody has watched for CatchUpQuietSeconds.
	CatchUp bool `json:"catchUp,omitempty"`
	// DownloadURL / DownloadName: what a notify-only install downloads (the
	// macOS DMG, the .deb/.rpm package, the platform installer).
	DownloadURL  string `json:"downloadUrl,omitempty"`
	DownloadName string `json:"downloadName,omitempty"`
}

// ManagerStatus is the admin-facing snapshot.
type ManagerStatus struct {
	Enabled            bool             `json:"enabled"`
	Checking           bool             `json:"checking"`
	DisabledReason     string           `json:"disabledReason,omitempty"`
	Current            string           `json:"current"`
	Available          *AvailableUpdate `json:"available,omitempty"`
	Capability         string           `json:"capability"`
	CapabilityReason   string           `json:"capabilityReason,omitempty"`
	State              string           `json:"state"`
	AutoUpdate         bool             `json:"autoUpdate"`
	Requested          bool             `json:"requested"`
	Force              bool             `json:"force"`
	LastCheck          *time.Time       `json:"lastCheck,omitempty"`
	NextCheck          *time.Time       `json:"nextCheck,omitempty"`
	LastError          string           `json:"lastError,omitempty"`
	Window             Window           `json:"window"`
	ActiveSessions     int              `json:"activeSessions"`
	IdleSince          *time.Time       `json:"idleSince,omitempty"`
	QuietPeriodSeconds int              `json:"quietPeriodSeconds"`
	Confirming         bool             `json:"confirming"`
	FailedVersion      string           `json:"failedVersion,omitempty"`
	FailedReason       string           `json:"failedReason,omitempty"`
	Note               string           `json:"note,omitempty"`
	Channel            string           `json:"channel"`
	// NotifyKind says how a notify-only install is updated (NotifyKind*
	// constants), so the console can show the exact steps.
	NotifyKind string `json:"notifyKind,omitempty"`
	Arch       string `json:"arch"`
	// CatchUpQuietSeconds is the quiet time a catch-up install waits for.
	CatchUpQuietSeconds int `json:"catchUpQuietSeconds"`
	// Installed is the last update that installed and confirmed healthy,
	// until the owner dismisses it (or a week passes).
	Installed *InstalledUpdate `json:"installed,omitempty"`
	// Cluster is the rolling-update state of a clustered node.
	Cluster *ClusterUpdateStatus `json:"cluster,omitempty"`
}

// ClusterUpdateStatus is a clustered node's part in a rolling update.
type ClusterUpdateStatus struct {
	// LeaseHeld is true while this node holds the cluster update lease
	// for an install.
	LeaseHeld bool `json:"leaseHeld"`
	// LeaseHolder is the node last seen holding the lease (this node's
	// name when LeaseHeld).
	LeaseHolder     string     `json:"leaseHolder,omitempty"`
	Draining        bool       `json:"draining"`
	DrainStarted    *time.Time `json:"drainStarted,omitempty"`
	MaxDrainSeconds int        `json:"maxDrainSeconds"`
}

// Status snapshots the manager.
func (m *Manager) Status() ManagerStatus {
	cfg := m.opts.Config()
	now := m.opts.Now().Round(0)
	start, end := windowBounds(cfg)
	m.mu.Lock()
	defer m.mu.Unlock()
	enabled := cfg.Enabled && m.client != nil
	disabledReason := m.disabledReason
	if !cfg.Enabled {
		disabledReason = disabledBySettingsReason
	}
	st := ManagerStatus{
		Enabled:            enabled,
		Checking:           m.checking || m.checkPending,
		DisabledReason:     disabledReason,
		Current:            m.opts.CurrentVersion,
		State:              m.state,
		AutoUpdate:         cfg.AutoUpdate,
		Requested:          m.requested,
		Force:              m.force,
		LastError:          m.lastErr,
		Window:             Window{Start: start, End: end, Open: inWindow(now.In(m.opts.Location), cfg)},
		ActiveSessions:     m.lastActive,
		QuietPeriodSeconds: int(m.opts.QuietPeriod / time.Second),
		Confirming:         !m.confirmed,
		Note:               m.opts.Boot.Note,
		Channel:            cfg.Channel,
		Arch:               m.opts.GOARCH,

		CatchUpQuietSeconds: int(m.opts.CatchUpQuiet / time.Second),
	}
	if st.Channel == "" {
		st.Channel = ChannelStable
	}
	if m.installed != nil && now.Sub(m.installed.At) < installedNoticeFor {
		cp := *m.installed
		st.Installed = &cp
	}
	tp := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	st.LastCheck = tp(m.lastCheck)
	if st.Enabled {
		st.NextCheck = tp(m.nextCheck)
	}
	st.IdleSince = tp(m.idleSince)
	if m.opts.Coordinator != nil {
		st.Cluster = &ClusterUpdateStatus{LeaseHeld: m.leaseHeld, LeaseHolder: m.holder, Draining: m.draining,
			DrainStarted: tp(m.drainStart), MaxDrainSeconds: int(maxDrain(cfg) / time.Second)}
	}
	if enabled && m.available != nil {
		mf := m.available.Manifest
		st.Available = &AvailableUpdate{
			Version:  mf.Version,
			Kind:     m.available.Artifact.Kind,
			NotesURL: mf.NotesURL,
			Notes:    mf.Notes,
			Staged:   m.staged != "" && m.stagedVersion == mf.Version,
		}
		if !mf.PublishedAt.IsZero() {
			pub := mf.PublishedAt
			st.Available.PublishedAt = &pub
		}
		st.Available.Critical = mf.Critical
		if at := m.installAfter(m.available); now.Before(at) {
			st.Available.InstallAfter = &at
		} else if cfg.AutoUpdate && !m.requested {
			st.Available.CatchUp = m.planLocked(now, cfg, m.available).catchUp
		}
	}
	switch ok, why := m.selfApplicableLocked(m.available); {
	case !enabled:
		st.Capability = CapabilityDisabled
	case ok:
		st.Capability = CapabilitySelfApply
	default:
		st.Capability, st.CapabilityReason = CapabilityNotifyOnly, why
		st.NotifyKind = m.opts.NotifyKind
		if m.opts.SelfApply { // the release ships only an installer here
			st.NotifyKind = NotifyKindInstaller
		}
		if st.Available != nil {
			st.Available.DownloadURL, st.Available.DownloadName = m.manualDownload(st.NotifyKind)
		}
	}
	if m.failed != nil {
		st.FailedVersion, st.FailedReason = m.failed.Version, m.failed.Reason
		if st.State == StateIdle && m.available == nil {
			st.State = StateRolledBack
		}
	}
	return st
}

// manualDownload picks the file a notify-only install downloads by hand.
func (m *Manager) manualDownload(kind string) (url, name string) {
	upd := m.available
	if upd == nil {
		return "", ""
	}
	mf := upd.Manifest
	switch kind {
	case NotifyKindMacOS, NotifyKindInstaller, NotifyKindUnsupervised, NotifyKindUnwritable:
		if a, err := mf.ArtifactFor(m.opts.GOOS, m.opts.GOARCH, KindInstaller); err == nil {
			return a.URL, artifactName(a)
		}
	case NotifyKindDeb, NotifyKindRPM:
		// Packages sit beside the bare binaries on the release; the
		// manifest lists only the binaries.
		a, err := mf.ArtifactFor(m.opts.GOOS, m.opts.GOARCH, KindBinary)
		if err != nil {
			return "", ""
		}
		i := strings.LastIndex(a.URL, "/")
		if i < 0 {
			return "", ""
		}
		name := fmt.Sprintf("ether_%s_%s_%s.%s", strings.TrimPrefix(mf.Version, "v"), m.opts.GOOS, m.opts.GOARCH, kind)
		return a.URL[:i+1] + name, name
	}
	return "", ""
}

func artifactName(a *Artifact) string {
	if a.Filename != "" {
		return a.Filename
	}
	return filepath.Base(strings.Split(a.URL, "?")[0])
}

// parseClock parses "HH:MM" into minutes past midnight.
func parseClock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// windowBounds returns the effective window (defaults for empty/invalid).
func windowBounds(cfg config.Updates) (start, end string) {
	start, end = cfg.MaintenanceStart, cfg.MaintenanceEnd
	if _, ok := parseClock(start); !ok {
		start = DefaultWindowStart
	}
	if _, ok := parseClock(end); !ok {
		end = DefaultWindowEnd
	}
	return start, end
}

// inWindow reports whether local time t falls inside [start, end). The
// window wraps midnight when end <= start; start == end means always open.
func inWindow(t time.Time, cfg config.Updates) bool {
	s, e := windowBounds(cfg)
	start, _ := parseClock(s)
	end, _ := parseClock(e)
	now := t.Hour()*60 + t.Minute()
	switch {
	case start == end:
		return true
	case start < end:
		return now >= start && now < end
	default:
		return now >= start || now < end
	}
}

// Environment describes the install for SelfApplyCapability.
type Environment struct {
	GOOS        string
	Target      string // running executable
	Dev         bool   // IsDev()
	InContainer bool
	// Supervised: a service manager restarts the process after it exits.
	Supervised bool
	// DirWritable probes the executable's directory (default: DirWritable).
	DirWritable func(dir string) bool
	// Exists reports whether a path exists (default: os.Stat); it tells a
	// Debian-family package install from an RPM one.
	Exists func(path string) bool
}

// Notify-only install kinds (SelfApplyMode, ManagerStatus.NotifyKind).
const (
	NotifyKindDev          = "dev"
	NotifyKindMacOS        = "macos"
	NotifyKindContainer    = "container"
	NotifyKindDeb          = "deb"
	NotifyKindRPM          = "rpm"
	NotifyKindPackage      = "package" // another package manager (snap, ...)
	NotifyKindUnsupervised = "unsupervised"
	NotifyKindUnwritable   = "unwritable"
	NotifyKindUnknown      = "unknown"
	// NotifyKindInstaller: the release ships only an installer for this
	// platform.
	NotifyKindInstaller = "installer"
)

// SelfApplyCapability decides whether this install may replace its own
// binary. Anything owned by something else — a package manager, a
// container image, a signed macOS app bundle — is notify-only.
func SelfApplyCapability(env Environment) (bool, string) {
	ok, _, why := SelfApplyMode(env)
	return ok, why
}

// SelfApplyMode is SelfApplyCapability plus the NotifyKind of a
// notify-only install.
func SelfApplyMode(env Environment) (ok bool, kind, reason string) {
	if env.DirWritable == nil {
		env.DirWritable = DirWritable
	}
	if env.Exists == nil {
		env.Exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	switch {
	case env.Dev:
		return false, NotifyKindDev, "development build"
	case env.GOOS == "darwin":
		return false, NotifyKindMacOS, "the macOS app is updated with its installer"
	case env.InContainer:
		return false, NotifyKindContainer, "running in a container; pull the new image instead"
	case env.Target == "":
		return false, NotifyKindUnknown, "cannot locate the running executable"
	case env.GOOS == "linux" && packageManaged(env.Target):
		kind := NotifyKindPackage
		switch {
		case strings.HasPrefix(env.Target, "/snap/"):
		case env.Exists("/var/lib/dpkg/info/ether.list"):
			kind = NotifyKindDeb
		case env.Exists("/usr/bin/rpm"):
			kind = NotifyKindRPM
		case env.Exists("/usr/bin/dpkg"):
			kind = NotifyKindDeb
		}
		return false, kind, "installed by the system package manager; update with it"
	case !env.Supervised:
		return false, NotifyKindUnsupervised, "not running as a service, so nothing would restart Ether after an update"
	case !env.DirWritable(filepath.Dir(env.Target)):
		return false, NotifyKindUnwritable, "the install directory is not writable by the Ether service"
	}
	return true, "", ""
}

func packageManaged(target string) bool {
	for _, p := range []string{"/usr/bin/", "/usr/sbin/", "/usr/lib/", "/usr/libexec/", "/usr/share/", "/snap/"} {
		if strings.HasPrefix(target, p) {
			return true
		}
	}
	return false
}

// DirWritable reports whether this process can create files in dir.
func DirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".ether-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}
