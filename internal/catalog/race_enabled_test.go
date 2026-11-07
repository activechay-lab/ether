//go:build race

package catalog

// raceEnabled reports a -race build. Wall-clock budgets measure the product
// only without the detector, which slows this code by an order of magnitude.
const raceEnabled = true
