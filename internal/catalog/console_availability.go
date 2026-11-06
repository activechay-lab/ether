package catalog

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
)

func init() {
	store.Register("catalog", 39, `CREATE INDEX catalog_release_evidence_quality_cover ON catalog_release_evidence(policy,verified_until,height,hdr,dv,atmos,cached,cache_until,rating_key);`)
	store.Register("catalog", 37, `
CREATE TABLE catalog_release_evidence (
 rating_key INTEGER NOT NULL, release_id TEXT NOT NULL, source_id TEXT NOT NULL,
 info_hash TEXT NOT NULL DEFAULT '', policy TEXT NOT NULL, verified_until INTEGER NOT NULL DEFAULT 0,
 cache_until INTEGER NOT NULL DEFAULT 0, cached INTEGER NOT NULL DEFAULT -1,
 height INTEGER NOT NULL DEFAULT 0, hdr INTEGER NOT NULL DEFAULT 0,
 dv INTEGER NOT NULL DEFAULT 0, atmos INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(rating_key,release_id,source_id)
);
CREATE INDEX catalog_release_evidence_playable ON catalog_release_evidence(policy,verified_until,rating_key);
CREATE INDEX catalog_release_evidence_cache ON catalog_release_evidence(policy,cached,cache_until,rating_key);
CREATE INDEX catalog_release_evidence_expired ON catalog_release_evidence(verified_until,cache_until,rating_key,release_id,source_id);
CREATE INDEX catalog_release_evidence_title ON catalog_release_evidence(rating_key,policy,verified_until,height,hdr,dv,atmos);
`)
}

// SetConsoleAvailabilityPolicy binds the same cached policy stamp used by the
// availability service. A changed source/quality configuration retracts evidence
// immediately, without rewriting every title or parsing release payloads.
func (c *Catalog) SetConsoleAvailabilityPolicy(policy func() string) {
	c.consoleEvidencePolicy.Store(&policy)
}
func (c *Catalog) evidencePolicy() string {
	if p := c.consoleEvidencePolicy.Load(); p != nil {
		return (*p)()
	}
	return ""
}

// RecordConsoleReleaseEvidence stores normalized observations on ingestion or
// verification. cached=-1 means unknown; addon cache hints never enter here.
func (c *Catalog) RecordConsoleReleaseEvidence(ctx context.Context, key core.RatingKey, r core.Release, source, policy string, verifiedUntil, cacheUntil int64, cached int) error {
	if key == 0 || r.ID == "" {
		return nil
	}
	hdr, dv, atmos := 0, 0, 0
	if r.Info.HDR10 {
		hdr = 1
	}
	if r.Info.DolbyVision {
		dv = 1
	}
	if r.Info.Atmos {
		atmos = 1
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO catalog_release_evidence(rating_key,release_id,source_id,info_hash,policy,verified_until,cache_until,cached,height,hdr,dv,atmos) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(rating_key,release_id,source_id) DO UPDATE SET info_hash=excluded.info_hash,policy=excluded.policy,verified_until=excluded.verified_until,cache_until=CASE WHEN excluded.cached<0 AND catalog_release_evidence.policy=excluded.policy AND catalog_release_evidence.info_hash=excluded.info_hash THEN catalog_release_evidence.cache_until ELSE excluded.cache_until END,cached=CASE WHEN excluded.cached<0 AND catalog_release_evidence.policy=excluded.policy AND catalog_release_evidence.info_hash=excluded.info_hash THEN catalog_release_evidence.cached ELSE excluded.cached END,height=excluded.height,hdr=excluded.hdr,dv=excluded.dv,atmos=excluded.atmos`, int64(key), r.ID, source, r.InfoHash, policy, verifiedUntil, cacheUntil, cached, r.Info.Height, hdr, dv, atmos)
	if err == nil {
		c.writeGen.Add(1)
	}
	return err
}
func (c *Catalog) InvalidateConsoleReleaseEvidence(ctx context.Context, id string) {
	_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_evidence WHERE release_id=?`, id)
	c.writeGen.Add(1)
}
func (c *Catalog) consoleAvailabilityConditions(f core.Filter) ([]string, []any) {
	if f.Playable == "" && f.DebridCached == "" && len(f.Qualities) == 0 {
		return nil, nil
	}
	var conds []string
	var args []any
	now := c.now().Unix()
	policy := c.evidencePolicy()
	if f.Playable == "no" || f.DebridCached == "no" {
		return []string{"1=0"}, nil
	}
	playable := `t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>?)`
	if f.Playable != "" && !(f.Playable == "yes" && len(f.Qualities) > 0) {
		if f.Playable == "unknown" {
			playable = "NOT (" + playable + ")"
		}
		conds = append(conds, playable)
		args = append(args, policy, now)
	}
	if f.DebridCached != "" && !(f.DebridCached == "yes" && len(f.Qualities) > 0) {
		sql := `t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.cached=1 AND ev.cache_until>?)`
		if f.DebridCached == "unknown" {
			sql = `t.key NOT IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.cached>=0 AND ev.cache_until>?)`
		}
		conds = append(conds, sql)
		args = append(args, policy, now)
	}
	if len(f.Qualities) > 0 {
		q := []string{}
		for _, quality := range f.Qualities {
			switch quality {
			case "4k":
				q = append(q, "ev.height>=2160")
			case "hdr":
				q = append(q, "(ev.hdr=1 OR ev.dv=1)")
			case "dv":
				q = append(q, "ev.dv=1")
			case "atmos":
				q = append(q, "ev.atmos=1")
			default:
				return []string{"1=0"}, nil
			}
		}
		if f.DebridCached == "yes" {
			q = append(q, "ev.cached=1 AND ev.cache_until>?")
		}
		conds = append(conds, fmt.Sprintf(`t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>? AND %s)`, strings.Join(q, " AND ")))
		args = append(args, policy, now)
		if f.DebridCached == "yes" {
			args = append(args, now)
		}
	}
	return conds, args
}

// PruneConsoleReleaseEvidence drops at most one fixed-size expired chunk per
// monitor cycle. It never scans the title library or blocks a browse request.
func (c *Catalog) PruneConsoleReleaseEvidence(ctx context.Context, now int64) error {
	result, err := c.db.ExecContext(ctx, `DELETE FROM catalog_release_evidence WHERE (rating_key,release_id,source_id) IN (SELECT rating_key,release_id,source_id FROM catalog_release_evidence WHERE verified_until<=? AND cache_until<=? ORDER BY verified_until,rating_key,release_id,source_id LIMIT 500)`, now, now)
	if err == nil {
		if n, _ := result.RowsAffected(); n > 0 {
			c.writeGen.Add(1)
		}
	}
	return err
}
