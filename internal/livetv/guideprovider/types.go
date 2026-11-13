// Package guideprovider defines protocol-neutral upstream guide data.
package guideprovider

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type BreakPoint struct {
	AtMS       int64   `json:"atMs"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence,omitempty"`
}

type Item struct {
	BreakAvailableAfterMS int64          `json:"breakAvailableAfterMs,omitempty"`
	SourceID              string         `json:"sourceId,omitempty"`
	BreakReleaseID        string         `json:"breakReleaseId,omitempty"`
	BreakRevision         string         `json:"breakRevision,omitempty"`
	BreakPoints           []BreakPoint   `json:"breakPoints,omitempty"`
	BreakPointsMS         []int64        `json:"breakPointsMs,omitempty"`
	ReelID                string         `json:"reelId,omitempty"`
	ReelClipID            string         `json:"reelClipId,omitempty"`
	EpisodeTitle          string         `json:"episodeTitle,omitempty"`
	ShowTitle             string         `json:"showTitle,omitempty"`
	Art                   string         `json:"art,omitempty"`
	Genres                []string       `json:"genres,omitempty"`
	GUID                  string         `json:"guid,omitempty"`
	ShowGUID              string         `json:"showGuid,omitempty"`
	MediaType             string         `json:"mediaType,omitempty"`
	Premiere              bool           `json:"premiere,omitempty"`
	Live                  bool           `json:"live,omitempty"`
	Key                   core.RatingKey `json:"key"`
	Title                 string         `json:"title"`
	DurationMS            int64          `json:"durationMs"`
	Summary               string         `json:"summary,omitempty"`
	Rating                string         `json:"rating,omitempty"`
	Thumb                 string         `json:"thumb,omitempty"`
	// Show metadata keeps episodes in season order inside a shuffle,
	// marathon or newest-first playout. Zero values are movies and items
	// saved before weekly scheduling, which stay in their stored order.
	ShowKey    core.RatingKey `json:"showKey,omitempty"`
	Season     int            `json:"season,omitempty"`
	Episode    int            `json:"episode,omitempty"`
	ReleasedMS int64          `json:"releasedMs,omitempty"`
	// Slate is "offair" for a guide entry that fills a gap. It is not a
	// catalogue title and is never stored on a channel.
	Slate string `json:"slate,omitempty"`
}

type Lineup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}
type Channel struct {
	ID, Name, Logo string
	Aliases        []string
}
type Programme struct {
	ChannelID  string
	Start, End time.Time
	Item       Item
}
type Provider interface {
	Lineups(context.Context, string, string) ([]Lineup, error)
	Channels(context.Context, string, *int) ([]Channel, error)
	Grid(context.Context, string, time.Time, *int) ([]Programme, error)
}
