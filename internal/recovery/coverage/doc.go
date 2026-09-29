// Package coverage provides instruction hit and execution count indexing
// over runtime observation traces.
//
// An [Index] is a derived, bounded summary of instruction execution frequency
// and frame distribution across one or more execution runs. It aggregates
// retired instruction dispatches into [Site] records keyed by instruction identity,
// enabling fast interval queries, routine rollups, and physical ROM bin heatmaps.
//
// The coverage index is not a lossless event history. It retains execution
// counts, frame histograms, and boundary sequence numbers, while raw trace streams
// on disk retain individual dispatches, register snapshots, and memory bus fetches.
// Evidence entries reference original runs and sequence intervals to bridge between
// the aggregate index and underlying immutable traces.
package coverage
