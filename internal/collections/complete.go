package collections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/braedonsaunders/ether/internal/core"
)

// ListComplete builds every eligible collection or returns an error. Unlike
// List, it never suppresses refresh or franchise-list failures; consumers that
// advertise an exact total must not use a partial result.
func (s *Service) ListComplete(ctx context.Context, user *core.User, typ core.MediaType) ([]core.Collection, error) {
	return s.listCompleteUntil(ctx, user, typ, nil)
}

// FindVisible stops evaluating collections as soon as accept finds a visible
// member. It never materializes the complete collection listing.
func (s *Service) FindVisible(ctx context.Context, user *core.User, typ core.MediaType, accept func(core.Collection) (bool, error)) (bool, error) {
	rows, err := s.listCompleteUntil(ctx, user, typ, accept)
	return len(rows) > 0, err
}

func (s *Service) listCompleteUntil(ctx context.Context, user *core.User, typ core.MediaType, accept func(core.Collection) (bool, error)) ([]core.Collection, error) {
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].builtin && !rows[j].builtin })
	out := make([]core.Collection, 0)
	for _, row := range rows {
		if !visible(row, user) || !row.spec.Targets.CollectionsTab || !typeMatches(row.spec, typ) {
			continue
		}
		keys, err := s.serve(ctx, row, user, true)
		if err != nil {
			return nil, fmt.Errorf("collections: complete list: %w", err)
		}
		if len(keys) > 0 && (row.spec.MediaType != MediaMixed || typ == "" || len(sectionKeys(keys, typ)) > 0) {
			c := s.collectionFor(ctx, row, user, typ, keys)
			if accept != nil {
				yes, e := accept(c)
				if e != nil {
					return nil, e
				}
				if yes {
					return []core.Collection{c}, nil
				}
			} else {
				out = append(out, c)
			}
		}
	}
	for _, c := range s.listEphemeral(user, typ) {
		if accept != nil {
			yes, e := accept(c)
			if e != nil {
				return nil, e
			}
			if yes {
				return []core.Collection{c}, nil
			}
		} else {
			out = append(out, c)
		}
	}
	hideFranchises := s.catCfg != nil && s.catCfg().HideFranchiseCollections
	if !hideFranchises && (typ == "" || typ == core.TypeMovie) {
		if accept != nil {
			var after int64
			for {
				c, e := s.mirror.NextFranchise(ctx, after)
				if errors.Is(e, sql.ErrNoRows) {
					return out, nil
				}
				if e != nil {
					return nil, e
				}
				yes, e := accept(*c)
				if e != nil {
					return nil, e
				}
				if yes {
					return []core.Collection{*c}, nil
				}
				after = c.Key.TMDB()
			}
		}
		franchises, err := s.mirror.Franchises(ctx)
		if err != nil {
			return nil, fmt.Errorf("collections: complete franchises: %w", err)
		}
		out = append(out, franchises...)
	}
	return out, nil
}
