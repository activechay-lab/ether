package config

// Availability bounds automatic recent-release monitoring and optional checks.
// Zero budgets disable the corresponding background work.
type Availability struct {
	DisableMovieEvidenceGate bool     `json:"disableMovieEvidenceGate"`
	MovieEvidenceDays        int      `json:"movieEvidenceDays"` // zero preserves the 60-day default
	Monitor                  bool     `json:"monitor"`
	Verify                   bool     `json:"verify"`
	PreferVerified           bool     `json:"preferVerified"`
	NewlyAvailable           bool     `json:"newlyAvailable"`
	IndexerIDs               []string `json:"indexerIds"` // empty = all supported enabled indexers
	Debrid                   bool     `json:"debrid"`
	PollMinutes              int      `json:"pollMinutes"`
	MaxPages                 int      `json:"maxPages"`
	FeedRequests             int      `json:"feedRequests"`  // total per UTC day, across indexers
	CheckRequests            int      `json:"checkRequests"` // indexer + provider API calls per day
	Grabs                    int      `json:"grabs"`
	Checks                   int      `json:"checks"`
	CheckBytes               int64    `json:"checkBytes"`     // conservative wire reservations for validation
	ReservePercent           int      `json:"reservePercent"` // preserve this share of configured indexer caps
	// Effort is gentle, normal, unlimited, or custom. Empty means the
	// v0.2.57 default budgets still apply and should scale; any other
	// stored budget is a custom choice.
	Effort string `json:"effort,omitempty"`
	// CatchUp walks older feed pages. It is optional and never required
	// before a title can appear.
	CatchUp bool `json:"catchUp,omitempty"`
	// DigitalDateEvidence lets a passed digital or physical release date
	// show a movie before a playable file has been seen. Theatrical dates
	// never count.
	DigitalDateEvidence bool `json:"digitalDateEvidence,omitempty"`
	// HomeReleaseOffsetDays shifts the digital/physical release admission date
	// in ModeDigital (negative shows earlier, positive later). Missing-date
	// fallback and playable-release gating are unchanged.
	HomeReleaseOffsetDays int `json:"homeReleaseOffsetDays,omitempty"`
	// Mode is playable, digital, or immediate. Empty is derived from the
	// gate and date flags so existing configs keep working.
	Mode string `json:"mode,omitempty"`
}

// IndexerCap is one indexer's own limit. DailyLimit 0 means the indexer
// reports no daily cap ("unlimited"). PageMax is the caps document's page
// size; 0 means it was not reported.
type IndexerCap struct {
	ID         string
	DailyLimit int
	PageMax    int
}

func DefaultAvailability() Availability {
	return Availability{Monitor: true, PollMinutes: 15, MaxPages: 5, FeedRequests: 400, CheckRequests: 100, Grabs: 20, Checks: 20, CheckBytes: 512 << 20, ReservePercent: 80, Debrid: true, PreferVerified: true, NewlyAvailable: true, Mode: ModePlayable}
}

const (
	ModePlayable  = "playable"
	ModeDigital   = "digital"
	ModeImmediate = "immediate"
)

// EffectiveMode maps stored flags onto the three owner modes. The shipped
// default (gate on, no digital-date shortcut) is "When they can play".
func (a Availability) EffectiveMode() string {
	switch a.Mode {
	case ModePlayable, ModeDigital, ModeImmediate:
		return a.Mode
	}
	if a.DisableMovieEvidenceGate {
		return ModeImmediate
	}
	if a.DigitalDateEvidence {
		return ModeDigital
	}
	return ModePlayable
}

// ApplyMode writes the engine flags for EffectiveMode. Gentle/Unlimited
// presets become the self-tuned normal budget unless the owner set a ceiling.
func (a *Availability) ApplyMode() {
	mode := a.EffectiveMode()
	a.Mode = mode
	switch mode {
	case ModeDigital:
		a.Monitor = false
		a.DisableMovieEvidenceGate = false
		a.DigitalDateEvidence = true
	case ModeImmediate:
		a.Monitor = false
		a.DisableMovieEvidenceGate = true
		a.DigitalDateEvidence = false
	default:
		a.Monitor = true
		a.DisableMovieEvidenceGate = false
		a.DigitalDateEvidence = false
	}
	if a.Effort == "gentle" || a.Effort == "unlimited" {
		a.Effort = "normal"
	}
}

// EffectiveMovieEvidenceDays preserves legacy configurations.
func (a Availability) EffectiveMovieEvidenceDays() int {
	if a.MovieEvidenceDays <= 0 {
		return 60
	}
	return a.MovieEvidenceDays
}

// LegacyDefaultBudgets reports the v0.2.57 shipped feed allowance.
func (a Availability) LegacyDefaultBudgets() bool {
	return a.Effort == "" && a.FeedRequests == 400 && a.PollMinutes == 15 && a.MaxPages == 5
}

// CustomBudgets reports an owner-chosen allowance. Presets recompute.
func (a Availability) CustomBudgets() bool {
	switch a.Effort {
	case "gentle", "normal", "unlimited":
		return false
	case "custom":
		return true
	default:
		return !a.LegacyDefaultBudgets()
	}
}

// AssumedDailyLimit is the daily API (and grab) limit assumed for an indexer
// that reports none: the entry tier of the strictest private indexers.
const AssumedDailyLimit = 1000

// BackgroundShare is the percentage of an indexer's daily limit that work
// nobody is waiting on may spend, by effort. The owner's ReservePercent can
// only lower it further.
func (a Availability) BackgroundShare() int {
	share := 10
	switch a.Effort {
	case "gentle":
		share = 5
	case "unlimited":
		share = 100
	}
	return max(0, min(share, 100-min(99, max(0, a.ReservePercent))))
}

// IndexerAllowance is one indexer's daily background request allowance:
// BackgroundShare of its own daily API limit, assuming AssumedDailyLimit
// when it reports none (100 requests a day by default). It is a ceiling,
// never a target: feeds and searches spend only what they need.
func (a Availability) IndexerAllowance(dailyLimit int) int {
	if dailyLimit <= 0 {
		dailyLimit = AssumedDailyLimit
	}
	return max(1, dailyLimit*a.BackgroundShare()/100)
}

// Resolved applies Gentle / Normal / Unlimited. Custom and non-default
// saved numbers are kept. The total feed-and-search allowance is the sum
// of each indexer's own allowance (IndexerAllowance).
func (a Availability) Resolved(indexers []IndexerCap) Availability {
	if a.CustomBudgets() {
		if a.Effort == "" {
			a.Effort = "custom"
		}
		return a
	}
	effort := a.Effort
	if effort == "" {
		effort = "normal"
	}
	poll, pages := 30, 8
	switch effort {
	case "gentle":
		poll, pages = 60, 3
	case "unlimited":
		poll, pages = 15, 20
	}
	a.Effort = effort
	a.PollMinutes = poll
	a.MaxPages = pages
	sum := 0
	for _, ix := range indexers {
		sum += a.IndexerAllowance(ix.DailyLimit)
	}
	if len(indexers) == 0 {
		sum = a.IndexerAllowance(0)
	}
	a.FeedRequests = sum
	return a
}
