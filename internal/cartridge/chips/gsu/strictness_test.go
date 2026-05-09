package gsu

import "testing"

// TestSTOPResetsPipeline pins bsnes/processor/gsu/instructions.cpp:8
// instructionSTOP behavior: after STOP retires, the prefetch
// pipeline is reset to 0x01 (NOP). Without this, when the CPU
// re-launches the GSU (via $301F write) the next stepOne dispatches
// the LEFTOVER prefetched byte from before the STOP, instead of the
// expected cold-NOP first dispatch.
//
// Pre-fix Go's Stop() in device.go does not touch d.Pipeline; the
// leftover byte (typically the byte at oldR15 prefetched during the
// STOP step itself) remains in pipeline.
func TestSTOPResetsPipeline(t *testing.T) {
	// ROM layout designed to make the leftover byte distinctive:
	//   $00: 01     NOP   (cold pipeline already loads $01, retire 0)
	//   $01: 00     STOP  (retire 1 — Pipeline at this moment refills
	//                      to ROM[2]=$AA which is non-NOP)
	//   $02: AA     (prefetched into pipeline during STOP step)
	rom := []byte{0x01, 0x00, 0xAA}
	d := New(rom, nil)
	GoAndRun(d, 2) // cold-NOP + ROM[0]=NOP + ROM[1]=STOP retire

	if d.Running() {
		t.Fatalf("expected SFR.G clear after STOP, got Running()")
	}
	if d.Pipeline != 0x01 {
		t.Errorf("Pipeline after STOP = %02X, want 01 "+
			"(bsnes instructions.cpp:8 sets regs.pipeline = 0x01)",
			d.Pipeline)
	}
}

// TestSTOPRelaunchDispatchesNOP pins the user-visible consequence:
// when the CPU re-launches the GSU (Go() called on a stopped device
// with R15 advanced past the STOP), the FIRST retired byte must be
// NOP — not the leftover prefetched byte.
//
// Strategy: place a recognisable trace at ROM[oldR15+1] so the
// leftover byte (if it slipped through) would change R0 visibly,
// and assert R0 stays untouched after the relaunch.
func TestSTOPRelaunchDispatchesNOP(t *testing.T) {
	// ROM:
	//   $00: 01           NOP  (retired)
	//   $01: 00           STOP (retired; clears SFR.G)
	//   $02: 50           ADD R0,R0 (op 0x50; adds R0 to itself, would
	//                                double R0 if executed)
	//   $03: 00           STOP (terminates the relaunch run)
	rom := []byte{0x01, 0x00, 0x50, 0x00}
	d := New(rom, nil)
	d.R[0] = 0x1234
	GoAndRun(d, 2) // cold + ROM[0]=NOP + ROM[1]=STOP

	if d.Running() {
		t.Fatalf("expected stopped after first STOP")
	}
	// Pre-relaunch R0 unchanged.
	if d.R[0] != 0x1234 {
		t.Fatalf("R0 changed during pre-relaunch: %04X", d.R[0])
	}

	// Re-launch: SetPC past the STOP, Go(). r15Modified is set by
	// SetPC so the post-step R15++ is suppressed for the first
	// retire. Pipeline at this point: pre-fix = $50 (the leftover
	// from STOP-step's prefetch refill at R15=2), post-fix = $01
	// (cold-NOP reset by Stop()).
	d.SetPC(2)
	d.Go()
	d.Run(1) // single retire

	// Pre-fix: leftover Pipeline=$50 dispatched, ADD R0,R0 doubles R0
	//          to 0x2468. Post-fix: NOP dispatched, R0 stays 0x1234.
	if d.R[0] != 0x1234 {
		t.Errorf("R0 after relaunch first retire = %04X, want 1234 "+
			"(bsnes instructions.cpp:8 ensures the relaunch's first "+
			"retire is NOP, not the leftover prefetched byte)", d.R[0])
	}
}

// TestSCMRMasksUpperBits pins bsnes registers.hpp:58-75 SCMR struct
// behavior: writes drop bits 6,7 (real hardware doesn't store them
// since md uses 0,1, ht uses 2,5, ran=3, ron=4). Reads return only
// the bits the struct stored. Pre-fix Go board.go:132 stores the
// full byte unmasked.
func TestSCMRMasksUpperBits(t *testing.T) {
	// CPU writes 0xFF to $00:303A SCMR. Read back $00:303A SCMR
	// must be 0x3F (only bits 0..5 retained).
	d := New(nil, nil)
	if !d.Write(0x303A, 0xFF) {
		t.Fatalf("Write to $303A returned false")
	}
	got, ok := d.Read(0x303A)
	if !ok {
		t.Fatalf("Read $303A returned not-ok")
	}
	if got != 0x3F {
		t.Errorf("SCMR readback after Write(0xFF) = %02X, want 3F "+
			"(bsnes registers.hpp:58-75 SCMR struct keeps only "+
			"bits 0,1,2,3,4,5; bits 6,7 unused)", got)
	}
}

// TestSCMRPreservesValidBits ensures the strict-mask fix doesn't
// drop bits that bsnes does keep (ht_high=5, ron=4, ran=3, ht_low=2,
// md=0,1).
func TestSCMRPreservesValidBits(t *testing.T) {
	d := New(nil, nil)
	if !d.Write(0x303A, 0x3F) {
		t.Fatalf("Write to $303A returned false")
	}
	got, ok := d.Read(0x303A)
	if !ok {
		t.Fatalf("Read $303A returned not-ok")
	}
	if got != 0x3F {
		t.Errorf("SCMR readback after Write(0x3F) = %02X, want 3F", got)
	}
}
