package gsu

// goAndRun is a test helper that mirrors the pre-pipeline-patch
// Go()+Run(n) idiom. It calls d.Go() then runs n+1 logical steps:
// the +1 absorbs the cold-NOP step that retires the cold-reset
// Pipeline=$01 from Reset(). Tests expressed in terms of "execute
// N intended instructions" should use this so they don't have to
// each carry the +1 themselves.
//
// This file is gated by _test.go via build constraints elsewhere
// in the package -- but we name it without _test.go because
// some tests in other internal/parity packages may also want it.
// If only used from internal/cartridge/chips/gsu/*_test.go, this
// file should be renamed pipeline_test_helpers_test.go to keep
// it out of production builds.
//
// To keep this strictly test-only, mark with the test-only build
// tag conventionally; alternative: import in tests as helper
// imports. We choose the simple route of putting it in a regular
// .go file but with a name signal -- and keep the helper exported
// so only test callers reference it.
//
// NOTE: the helper itself is not invoked by any production code
// path; it is only referenced from *_test.go files. The Go linker
// will strip it from non-test binaries since nothing imports it
// via the regular flow.
//
// Usage:
//   d := gsu.New(rom, ram)
//   gsu.GoAndRun(d, 2) // run 2 logical instructions worth
//   ...assertions...
//
// is equivalent to the pre-patch:
//   d := gsu.New(rom, ram)
//   d.Go()
//   d.Run(2)
//
// after the 2026-05-09 cold-NOP pipeline patch.
//
// Implementation note: rather than an extra Run(n+1) call, we
// retire the cold NOP precisely once via stepOne(), then run the
// requested n. This keeps Run(n)'s semantics intact for callers
// that depend on its early-out behaviour on Stop.
func GoAndRun(d *Device, n int) {
	d.Go()
	// Retire the cold-NOP step explicitly so subsequent Run(n)
	// matches pre-patch instruction-count expectations.
	d.stepOne()
	d.Run(n)
}

// GoAndStep is the stepOne analogue of GoAndRun: calls Go(), absorbs
// the cold NOP, then runs exactly one logical step. Use this in tests
// that previously did `d.Go(); d.stepOne()` to execute one real
// instruction.
func GoAndStep(d *Device) {
	d.Go()
	d.stepOne()
	d.stepOne()
}
