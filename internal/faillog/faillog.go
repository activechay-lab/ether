// Package faillog retains the shared repeating-failure logger API.
package faillog

import (
	kit "github.com/braedonsaunders/ether/internal/frontend/kit/faillog"
	"log/slog"
)

const DefaultEvery = kit.DefaultEvery

type Tracker = kit.Tracker

func New(log *slog.Logger, what string) *Tracker { return kit.New(log, what) }
