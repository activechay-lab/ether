package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (a *App) newRowViews(protocol core.Protocol, rows *experience.RowsService, receipts *experience.Receipts, visibility experience.Visibility, libraries []experience.Library) (*experience.RowViewService, error) {
	d := experience.RuntimeRowViews{DB: a.db, Protocol: protocol, Receipts: receipts, Visibility: visibility, Libraries: libraries, Active: a.users.PolicyUser, CachedGet: a.catalog.CachedGet, CachedGetMany: a.catalog.CachedGetMany, Playlists: a.playlists, Situation: situationFor,
		Snapshot: func(ctx context.Context, request frontend.RowRequest, situation core.HomeSituation) ([]frontend.Row, error) {
			return rows.SnapshotRows(context.WithValue(ctx, experienceSituationKey{}, situation), request, protocol)
		}}
	if a.collections != nil {
		d.Collections = a.collections
	}
	if a.availability != nil {
		d.CurrentAvailable = a.availability.FilterDiscoveryCurrent
	}
	if a.episodeorder != nil {
		d.CachedVirtualSeason = a.episodeorder.CachedVirtualSeason
	}
	if a.livetv != nil {
		d.Live = a.livetv
	}
	return experience.NewRuntimeRowViews(d)
}

type rowViewCollectionSource = experience.RowViewCollectionSource

func rowViewCollectionMembers(ctx context.Context, source rowViewCollectionSource, user *core.User, collection core.RatingKey, refs []core.NeutralEntryRef) bool {
	return experience.RowViewCollectionMembers(ctx, source, user, collection, refs)
}
