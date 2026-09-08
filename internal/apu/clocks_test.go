package apu

// runSMPClocks advances explicit input clocks. A normal SPC opcode cycle is
// two input clocks; timer and DSP intervals are already input-clock counts.
func runSMPClocks(a *APU, clocks int) {
	for range clocks {
		a.Run()
	}
}
