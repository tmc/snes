// Package progress presents measured recovery documents, coverage and revalidated
// recorded batch outcomes in separate units. It never executes or admits C.
//
// Load requires a pinned progress config. Evidence returns copied source bytes;
// missing measurements use nil counts, while recorded zero remains explicit.
package progress
