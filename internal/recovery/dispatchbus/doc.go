// Package dispatchbus checks the data accesses of a bounded native dispatch
// helper. It grants no evidence authority and does not model device timing.
// Callers supply fixture-verified instructions and normalized schema 2 byte
// accesses. The producer omits zero read values; writes require an explicit
// value or after field. Native binary mode and D=0 are supported.
package dispatchbus
