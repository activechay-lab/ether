package resolver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// RankingHooks supplies a per-resolve plugin session. It is optional.
type RankingHooks interface {
	NewResolve(context.Context, core.ClientCaps) RankingSession
}

// RankingSession batches steps and evaluates external conditions within budgets.
type RankingSession interface {
	Close()
	Rank(step string, config json.RawMessage, releases []core.Release, verified []bool) []int64
	Match(core.Release, core.ReleaseCondition, bool) (bool, bool)
}

func (s *Service) SetRankingHooks(h RankingHooks) {
	s.extend.mu.Lock()
	s.extend.hot = h
	s.extend.mu.Unlock()
}
func (s *Service) rankingSession(ctx context.Context, caps core.ClientCaps) RankingSession {
	s.extend.mu.Lock()
	h := s.extend.hot
	s.extend.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.NewResolve(ctx, caps)
}
func usesPluginRanking(p core.QualityProfile) bool {
	for step := range p.PluginScores {
		if core.IsPluginRankingStep(step) {
			return true
		}
	}
	for _, rule := range p.ReleaseRules {
		if rule.Disabled {
			continue
		}
		for _, group := range [][]core.ReleaseCondition{rule.Conditions, rule.Unless, rule.Fallback} {
			for _, c := range group {
				if strings.HasPrefix(c.Field, "plugin:") {
					return true
				}
			}
		}
	}
	return false
}

// pluginKeysEqual keeps source routing and health races within the same plugin tier.
func pluginKeysEqual(a, b candidate) bool {
	for k, v := range a.pluginKeys {
		if b.pluginKeys[k] != v {
			return false
		}
	}
	for k, v := range b.pluginKeys {
		if a.pluginKeys[k] != v {
			return false
		}
	}
	return true
}
