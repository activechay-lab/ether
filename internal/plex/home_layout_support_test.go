package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

// finalizeHomeRows is the wire-level entry used by layout tests. Production
// home and section handlers pass facts captured while painting.
func (s *Server) finalizeHomeRows(ctx context.Context, pr *Request, hc core.HubContext, section string, rows []model.Hub) []model.Hub {
	hubs := make([]core.Hub, len(rows))
	neutral := make([]core.NeutralHomeRow, len(rows))
	for i, row := range rows {
		hubs[i], neutral[i] = homeLayoutFacts(row)
	}
	return s.arrangeHomeRows(ctx, pr, hc, section, rows, hubs, neutral)
}
