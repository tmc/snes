// Package disasm provides stateless one-line disassemblers for the 65c816 and
// the SPC700. The output matches the format produced by bsnes and snes9x trace
// logs so that lockstep traces can be diffed directly.
//
// The disassemblers read operand bytes through a narrow Peek interface and do
// not advance PC or mutate CPU state. They are intended for use from test
// harnesses such as internal/parity/writetrace_test.go, not from the main
// execution path.
//
// The mnemonic and operand-type tables are attributed in the repository NOTICE.
package disasm
