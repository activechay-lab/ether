package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type subtitleDelete interface {
	DeleteSubtitle(context.Context, frontend.RatingKey, *frontend.Viewer, frontend.StreamInfo) error
}
