package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// experienceVisibility binds host sources. Library selection lives in experience.BindVisibility.
func experienceVisibility(policy experience.AccessPolicy, get func(context.Context, core.RatingKey) (*core.Title, error), available func(context.Context, []core.Title) []core.Title, sessions func() []*core.PlaySession, libraries []experience.Library) experience.Visibility {
	return experience.BindVisibility(policy, get, available, sessions, libraries)
}
