// Package computation provides backward computation extraction, dynamic program
// slicing, and value tracing for SNES CPU execution traces.
//
// Starting from an observable effect—such as a memory store or register
// definition—Extract traces data and address dependencies backward through
// machine instructions, identifying input variables, resolving versioned memory,
// classifying address indexing, and isolating the minimal executable slice.
//
// The extracted Computation can be inspected, simulated with new inputs via
// Execute, or exported as readable C code.
package computation
