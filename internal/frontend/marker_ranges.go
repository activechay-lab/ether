package frontend

import "context"

// MarkerRange is a source-specific skip range. Final identifies a terminal
// credits range; adapters decide how to encode that hint on their wire.
type MarkerRange struct {
	Type    string `json:"type"`
	StartMS int64  `json:"startTimeOffset"`
	EndMS   int64  `json:"endTimeOffset"`
	Final   bool   `json:"final"`
	Source  string `json:"source"`
}

type MarkerRanges interface {
	Markers(context.Context, RatingKey, MediaInfo) ([]MarkerRange, error)
}
