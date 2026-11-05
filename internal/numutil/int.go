// Package numutil contains shared integer arithmetic helpers.
package numutil

// Abs returns the integer magnitude; like integer negation, MinInt remains MinInt.
func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
