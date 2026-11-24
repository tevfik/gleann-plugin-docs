//go:build race

package engine

// Under the race detector sync.Pool drops items at random, so allocation totals
// are not comparable with a normal run; allocation bounds are skipped there.
const raceEnabled = true
