package availability

import (
	"context"
	"database/sql"
	"errors"
	"github.com/braedonsaunders/ether/internal/store"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// A checkpoint has two boundaries: the newest observed release, and a pending
// historical gap. Updating the head must never discard that unfinished gap.
// Offsets are only hints: a resumed page must contain the saved tail at its
// overlapping page, otherwise we walk again from the head (deletions/reordering).
type feedCheckpoint struct {
	head, target, tail string
	offset             int
	oldest, started    int64
	synced             int64 // last complete sync (read only)
}

const bootstrapTarget = "__ether_recent_history__"

func (s *Service) checkpoint(ctx context.Context, f core.ReleaseFeed) (feedCheckpoint, error) {
	var c feedCheckpoint
	var gap int
	err := s.db.QueryRowContext(ctx, `SELECT anchor,catchup_target,catchup_tail,catchup_offset,gap,catchup_oldest,catchup_started,synced_at FROM availability_feeds WHERE source_id=? AND generation=?`, f.ID, f.Generation).Scan(&c.head, &c.target, &c.tail, &c.offset, &gap, &c.oldest, &c.started, &c.synced)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	// Old versions advanced past gaps. With catch-up on, recover the recent
	// feed history instead of presenting the lost boundary as proof of
	// complete coverage.
	if gap != 0 && c.target == "" && s.cfg().Availability.CatchUp {
		c.target = bootstrapTarget
	}
	// History walks are catch-up's alone: without it a feed reads only
	// what was posted since its previous sync.
	if c.target == bootstrapTarget && !s.cfg().Availability.CatchUp {
		c.target, c.tail, c.offset = "", "", 0
	}
	return c, err
}
func (s *Service) saveCheckpoint(ctx context.Context, f core.ReleaseFeed, c feedCheckpoint, message string, count int, complete bool) error {
	synced := int64(0)
	if complete {
		synced = s.now().Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO availability_feeds(source_id,generation,anchor,checked_at,synced_at,gap,message,discovered,catchup_target,catchup_tail,catchup_offset,catchup_oldest,catchup_started) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(source_id) DO UPDATE SET generation=excluded.generation,anchor=excluded.anchor,checked_at=excluded.checked_at,
 synced_at=CASE WHEN excluded.synced_at>0 THEN excluded.synced_at WHEN availability_feeds.generation<>excluded.generation THEN 0 ELSE availability_feeds.synced_at END,
 gap=excluded.gap,message=excluded.message,discovered=availability_feeds.discovered+excluded.discovered,
 catchup_target=excluded.catchup_target,catchup_tail=excluded.catchup_tail,catchup_offset=excluded.catchup_offset,
 catchup_oldest=excluded.catchup_oldest,catchup_started=excluded.catchup_started`, f.ID, f.Generation, c.head, s.now().Unix(), synced, store.BoolInt(c.target != ""), message, count, c.target, c.tail, c.offset, c.oldest, c.started)
	return err
}

func pageSize(f core.ReleaseFeed) int {
	if f.PageMax >= 1 && f.PageMax <= 100 {
		return f.PageMax
	}
	return 100
}

// syncFeed reads a feed from its head back to the previous sync's head:
// one page when fewer releases than a page arrived in between (the usual
// case), otherwise only the pages the gap needs, at most MaxPages per sync.
// An unfinished gap resumes on the next cycle within the allowance left
// after today's scheduled head polls. Catch-up additionally walks the
// recent history once.
func (s *Service) syncFeed(parent context.Context, f core.ReleaseFeed, scheduled bool) error {
	old, err := s.checkpoint(parent, f)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	c := old
	if c.head == "" && c.target == "" && s.cfg().Availability.CatchUp {
		c.target = bootstrapTarget
	}
	limit := min(20, max(1, s.budgets().MaxPages))
	if f.SinglePage {
		limit = 1
	}
	size := pageSize(f)
	head := ""
	offset := 0
	count := 0
	foundHead := old.head == ""
	target := c.target
	expectedTail := ""
	resumed := false
	// If an existing gap is pending we cannot move its offset until we locate
	// the previous head: new releases may have shifted every subsequent page.
	canCheckpoint := old.target == ""
	policy := s.policy()
	finish := func() error {
		return s.saveCheckpoint(parent, f, feedCheckpoint{head: head}, "Feed is current within the recent-release window", count, true)
	}
	for page := 0; page < limit; page++ {
		// Only a scheduled poll's first page spends the head-poll reserve.
		requestCtx := s.budgetContext(ctx, feedPage)
		if page == 0 && scheduled {
			requestCtx = s.budgetContext(ctx, headPoll)
		}
		releases, e := s.d.Feeds.Recent(requestCtx, f.ID, offset, size)
		if e != nil {
			// Successful pages remain useful. Preserve an exact resumable boundary;
			// never discard a prior gap merely because the next request was denied.
			if head != "" && canCheckpoint {
				c.head = head
			}
			if head != "" && c.target == "" {
				c.target = old.head
			}
			if parent.Err() == nil {
				_ = s.saveCheckpoint(parent, f, c, safeReason(e), count, false)
			}
			return e
		}
		if len(releases) == 0 {
			// A resumed offset may have moved beyond the end after deletions. Verify
			// from the head before accepting the end of the feed as coverage.
			if expectedTail != "" {
				offset = size
				expectedTail = ""
				resumed = true
				continue
			}
			if head == "" {
				head = old.head
			}
			return finish()
		}
		if head == "" {
			head = releases[0].ID
		}
		if expectedTail != "" {
			found := false
			for _, r := range releases {
				if r.ID == expectedTail {
					found = true
					break
				}
			}
			if !found {
				offset = size
				expectedTail = ""
				resumed = true
				// Lost boundary: rescan instead of silently skipping history.
				continue
			}
		}
		expectedTail = ""
		headIndex := -1
		reachedTarget := false
		oldPage := true
		// A gap is closed once a page holds only releases from a day before
		// the previous sync, even if its head release was since deleted.
		pastGap := target != bootstrapTarget && old.synced > 0
		for i := range releases {
			releases[i].Info = releaseparse.Parse(releases[i].Name)
			if releases[i].ID == old.head {
				headIndex = i
			}
			if target != "" && releases[i].ID == target {
				reachedTarget = true
			}
			if !releases[i].PubDate.IsZero() && (c.oldest == 0 || releases[i].PubDate.Unix() < c.oldest) {
				c.oldest = releases[i].PubDate.Unix()
			}
			if releases[i].PubDate.IsZero() || releases[i].PubDate.After(s.now().Add(-time.Duration(max(60, s.cfg().Availability.EffectiveMovieEvidenceDays())+1)*24*time.Hour)) {
				oldPage = false
			}
			if releases[i].PubDate.IsZero() || releases[i].PubDate.After(time.Unix(old.synced, 0).Add(-24*time.Hour)) {
				pastGap = false
			}
		}
		eligible := feedCategories(f, releases)
		if e = s.retainFeed(ctx, f, eligible); e != nil {
			return e
		}
		var matched map[string][]core.Title
		if len(eligible) > 0 {
			matched, e = s.d.Catalog.ReleaseMatches(ctx, eligible)
			if e != nil {
				return e
			}
		}
		for _, r := range eligible {
			for _, t := range matched[r.ID] {
				if e = s.remember(ctx, f, t, r, policy); e != nil {
					return e
				}
				count++
			}
		}
		// A first sync without catch-up starts at the head: history is not
		// needed, a title is searched once when it enters the window. A
		// single-page feed cannot page back.
		if reachedTarget || len(releases) < size || oldPage || (page > 0 && pastGap) || (old.head == "" && target == "") || f.SinglePage {
			return finish()
		}
		if headIndex >= 0 {
			foundHead = true
			canCheckpoint = true
			if target == "" {
				return finish()
			}
			if !resumed && old.tail != "" {
				shifted := old.offset + offset + headIndex
				// The entire current page is already observed. Resume only beyond it.
				if shifted >= offset+len(releases) {
					keptOldest, keptStarted := c.oldest, c.started
					c = feedCheckpoint{head: head, target: target, offset: shifted, tail: old.tail, oldest: keptOldest, started: keptStarted}
					// New posts can shift offsets between the head and this
					// request. Re-read a bounded overlap and find the exact
					// saved ID anywhere in it, rather than requiring index 0.
					offset = max(offset+len(releases)-1, shifted-50)
					expectedTail = old.tail
					resumed = true
					continue
				}
			}
		}
		if foundHead || old.target == "" {
			canCheckpoint = true
			c.head = head
			c.target = target
			if c.target == "" {
				c.target = old.head
			}
			c.offset = offset + len(releases) - 1
			c.tail = releases[len(releases)-1].ID
		}
		offset += len(releases)
	}
	if c.target == "" {
		c.target = bootstrapTarget
	}
	if c.started == 0 {
		c.started = s.now().Unix()
	}
	return s.saveCheckpoint(parent, f, c, "Catching up on recent releases; progress is saved and fresh arrivals are polled first", count, false)
}

// Pagination always counts the raw page, including excluded categories. A page
// full of books or games must not look like the end of the movie/TV feed.
func feedCategories(f core.ReleaseFeed, rs []core.Release) []core.Release {
	if len(f.Categories) == 0 {
		return rs
	}
	out := make([]core.Release, 0, len(rs))
	for _, r := range rs {
		allowed := r.Category == 0 // some otherwise-valid APIs omit category attributes
		for _, cat := range f.Categories {
			if r.Category == cat || (cat%1000 == 0 && r.Category/1000 == cat/1000) {
				allowed = true
			}
		}
		if allowed {
			out = append(out, r)
		}
	}
	return out
}
