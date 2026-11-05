package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// rowViewResolveBatch binds this call's viewer, device and protocol into the
// neutral legacy hydrator. The adapters exist only for this call.
func rowViewResolveBatch(ctx context.Context, viewer *core.User, device core.Device, protocol core.Protocol, refs []core.NeutralEntryRef, one func(context.Context, *core.User, core.Device, core.Protocol, core.NeutralEntryRef) (frontend.RowEntry, error), live func(context.Context, *core.User) ([]frontend.RowEntry, error)) ([]frontend.RowEntry, error) {
	return experience.RowViewHydrateLegacy(ctx, viewer, refs, func(ctx context.Context, ref core.NeutralEntryRef) (frontend.RowEntry, error) {
		return one(ctx, viewer, device, protocol, ref)
	}, func(ctx context.Context) ([]frontend.RowEntry, error) {
		return live(ctx, viewer)
	})
}
func rowViewResolveBatchTitles(ctx context.Context, viewer *core.User, device core.Device, protocol core.Protocol, refs []core.NeutralEntryRef, one func(context.Context, *core.User, core.Device, core.Protocol, core.NeutralEntryRef) (frontend.RowEntry, error), batch func(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error), get func(context.Context, *core.User, core.RatingKey) (*core.Title, error), visibility experience.Visibility, live func(context.Context, *core.User) ([]frontend.RowEntry, error)) ([]frontend.RowEntry, error) {
	return experience.RowViewResolveBatchTitles(ctx, viewer, device, protocol, refs, one, batch, get, visibility, live)
}
