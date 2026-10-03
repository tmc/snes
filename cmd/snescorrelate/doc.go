// Package main implements snescorrelate, an offline analytical tool that
// correlates CPU WRAM state mutations with downstream DMA transfers and PPU
// register consumption across emulator execution traces.
//
// Usage:
//
//	snescorrelate -case cases.jsonl -trace trace.jsonl [flags]
//
// Flags:
//
//	-case path
//	    Path to routine replay cases JSONL file (required)
//	-trace path
//	    Path to emulator trace events file (.jsonl or .jsonl.gz, required)
//	-runs path
//	    Path to cycle-accurate bus runs JSONL file (optional)
//	-id string
//	    Specific case ID or entry sequence to correlate (optional)
//	-format fmt
//	    Output format: markdown or json (default: markdown)
//	-o path
//	    Output file path (default: standard output)
package main
