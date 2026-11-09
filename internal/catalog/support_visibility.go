package catalog

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// SupportVisibility examines one locally stored item without a provider fetch.
// It reports the first known catalogue gate; "visible" only means these
// catalogue gates passed, not that viewer policy or availability passed.
func (c *Catalog) SupportVisibility(ctx context.Context, key core.RatingKey) (*core.Title, string, error) {
	st, err := c.loadByKey(ctx, key)
	if errors.Is(err, core.ErrNotFound) {
		return nil, "missing from local catalogue", nil
	}
	if err != nil {
		return nil, "catalogue lookup failed", err
	}
	t := &st.t
	switch {
	case t.Adult:
		return t, "excluded by adult-content catalogue rule", nil
	case !c.upcomingMovie(t) && !t.IsReleased(c.now()):
		return t, "not yet released", nil
	case !c.upcomingMovie(t) && c.config().ReleaseMode == config.ModeDigital && !homeReleaseVisible(t, c.now()):
		return t, "excluded by digital release mode", nil
	case t.Type == core.TypeSeason && !listableSeason(t, c.now()):
		return t, "season has no listable released episodes", nil
	case !c.isTitleOrShowOwnerIncluded(t) && c.visibilityFilter().Hidden(t):
		return t, "a catalogue visibility rule matches; selection reason not confirmed", nil
	default:
		return t, "catalogue gates passed; selection or availability excluded this item", nil
	}
}
