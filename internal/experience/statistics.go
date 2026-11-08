package experience

import (
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/hoststat"
	"github.com/braedonsaunders/ether/internal/stats"
)

// Statistics supplies process-local services for embedded hosts that have no
// durable dashboard or sampler. Supplied services retain their identity.
func Statistics(dashboard frontend.Dashboard, resources frontend.ResourceStatistics, now func() time.Time) (frontend.Dashboard, frontend.ResourceStatistics) {
	if dashboard == nil {
		dashboard = stats.NewDashboard(stats.DashboardOptions{Now: now})
	}
	if resources == nil {
		resources = hoststat.NewResources(hoststat.Options{Now: now})
	}
	return dashboard, resources
}
