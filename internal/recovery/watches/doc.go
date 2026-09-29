// Package watches provides explicit, offline evaluation of game-state
// watches against immutable memory snapshots.
//
// Watches observe backing physical memory (such as SNES WRAM) and map
// raw bytes to decoded numeric or enumerated values with typed validity
// conditions and sampled change intervals.
package watches
