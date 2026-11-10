package sessions

import "github.com/braedonsaunders/ether/internal/frontend"

// Estimator and Estimate live in internal/frontend
// (host_sessions_experience.go).
type Estimator = frontend.Estimator

var Estimate = frontend.Estimate
