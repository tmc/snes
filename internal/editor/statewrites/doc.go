// Package statewrites presents ordered byte writes from pinned complete WRAM
// observation windows. It preserves physical aliases and same-value writes.
// Before values require preceding observed accesses; initial values and dispatch
// ancestry are unavailable. Reports grant no recovery or rendering proof.
package statewrites
