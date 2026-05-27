package gsu

import "testing"

// TestRAMReadBusWaitMatchesBsnes pins the cycle cost of GSU shared-RAM
// reads against bsnes/sfc/coprocessor/superfx/{memory,timing}.cpp.
//
// Two distinct surfaces share the same RAM read primitive in bsnes:
//
//	(A) Instruction-handler RAM reads (LDB/LDW/LMS via instructionLoad,
//	    instructionIBT_LMS_SMS in bsnes processor/gsu/instructions.cpp).
//	    These call readRAMBuffer (timing.cpp:39-42), which calls
//	    SuperFX::read (memory.cpp:20-26). When SCMR.RAN is set (GSU
//	    owns RAM), the `while(!regs.scmr.ran)` loop is skipped and
//	    no step() executes — cost is **0 cycles** beyond any pending
//	    sync.
//
//	(B) Pixel-cache RAM reads (rpix, flushPixelCache in
//	    sfc/coprocessor/superfx/core.cpp). These add an explicit
//	    step(regs.clsr ? 5 : 6) BEFORE each per-byte read/write
//	    (core.cpp:66, 94, 98). So per-byte cost is **6 cycles** even
//	    when RAN is owned.
//
// The Go side must mirror this two-surface split. ramRead/ramWrite/
// romRead in alu.go are shared low-level primitives; instruction
// handlers and pixel-cache callers must produce different total
// cycle costs by virtue of where the bus-wait is paid.
func TestRAMReadBusWaitMatchesBsnes_InstructionHandler(t *testing.T) {
	// LDB Rn from RAM via opcode 0x40 (LDW) with ALT1 = LDB.
	// Sequence: WITH R1 (set src/dst=R1 via prefix, op 0x21), ALT1,
	// LDB (R1) (op 0x41 under ALT1).
	//
	// Simpler: use IBT R0 first to set R1=0x00 (so RAM addr = 0),
	// then LDB (R1) reads RAM[0]. We measure dev.Cycles() before
	// and after the LDB retire and assert it incremented only by the
	// readOpcode prefetch cost (cache hit = 2cy), with no extra 6cy
	// bus-wait per RAM byte.
	//
	// Because the pipeline + TraceHook ordering matters here, we use
	// a fixed ROM and compare cycle deltas between consecutive same-
	// kind retires (LDB then NOP) to isolate the LDB's RAM-read cost.

	// ROM layout (simple enough to predict cycles):
	//   $00: 21        WITH R1
	//   $01: 3D        ALT1
	//   $02: 41        LDB (R1)        ; reads RAM[0]
	//   $03: 01        NOP             ; baseline
	//   $04: 01        NOP
	//   $05: 00        STOP
	rom := []byte{0x21, 0x3D, 0x41, 0x01, 0x01, 0x00}
	ram := make([]byte, 0x10000)
	ram[0] = 0xAB

	d := New(rom, ram)
	d.SCMR = SCMRRAN | SCMRRON // GSU owns RAM and ROM
	GoAndRun(d, 5)

	// After running, R0 should hold 0xAB (LDB destination defaulted
	// to R0 since dreg was R1, but WITH R1 set both sreg=dreg=R1, so
	// LDB writes R1).
	if d.R[1] != 0x00AB {
		t.Errorf("R1 after LDB = %04X, want 00AB (RAM[0]=0xAB)", d.R[1])
	}

	// The cycle bound: in bsnes, when RAN is owned, instructionLoad
	// pays only the readOpcode prefetch costs (cache hits ≈ 2cy each
	// after the cold-fill) plus operand fetches. There is NO 6cy bus-
	// wait per RAM byte. Total cycles for this 5-instruction sequence
	// should be dominated by the cold cache fill (96cy on first miss)
	// + ~2cy per subsequent retire ≈ 100-110cy.
	//
	// If Go's ramRead unconditionally added 6cy per byte, LDB(R1) would
	// read 1 byte (LDB, ALT1) and add 6cy. After the fix this drops to
	// 0cy. Bound: total cycles must be < 130 for this 5-step sequence.
	cyc := d.Cycles()
	t.Logf("Cycles() after 5-instruction LDB sequence with RAN owned = %d", cyc)
	// Expected breakdown post-fix (matching bsnes):
	//   cold-NOP retire: 16 * 6 = 96 cy (cold cache fill at R15=0)
	//   WITH R1   (cache hit) :  2 cy
	//   ALT1      (cache hit) :  2 cy
	//   LDB (R1)  (cache hit prefetch 2cy + 0cy RAM read when RAN owned)
	//             per bsnes memory.cpp:20-26 + timing.cpp:39-42 = 2 cy
	//   NOP       (cache hit) :  2 cy
	//   NOP       (cache hit) :  2 cy
	//   total                  = 106 cy
	// Pre-fix Go counts an extra 6cy per RAM byte in ramRead; LDB
	// reads 1 byte (LDB = single byte under ALT1) so total = 112 cy.
	// After the fix the RAM-read 6cy must vanish.
	if cyc > 108 {
		t.Errorf("Cycles() = %d after 5-instruction LDB sequence with RAN owned; "+
			"want <=108 (per bsnes memory.cpp:20-26 read() with !ran skipped, "+
			"timing.cpp:39-42 readRAMBuffer; pre-fix observed 112)", cyc)
	}
}

// TestRAMReadBusWaitMatchesBsnes_PixelPath pins that pixel-cache RAM
// access still pays 6cy per byte, matching bsnes core.cpp:66/94/98.
// rpix on a missed-cache pixel must read bpp bytes from RAM, each at
// 6cy bus-wait (CLSR=0).
func TestRAMReadBusWaitMatchesBsnes_PixelPath(t *testing.T) {
	d := New(nil, nil)
	// SCMR.MD=0 → bpp=2. SCMR.RAN must be set so RPIX runs.
	d.SCMR = SCMRRAN | SCMRRON
	d.SCBR = 0
	cycBefore := d.Cycles()
	// rpix on a cold pixel cache: triggers flushPixelCache (no-op
	// since cache is empty) then reads bpp=2 bytes from RAM. Per
	// bsnes core.cpp:64-68, each byte costs step(6) + read.
	_ = d.rpix(0, 0)
	cycAfter := d.Cycles()
	delta := cycAfter - cycBefore
	// bpp=2 bytes × 6cy each = 12cy expected. Allow some slack for
	// sync paths but require AT LEAST 6cy (one bus-wait) so the
	// regression where pixel reads drop to 0cy is caught.
	if delta < 6 {
		t.Errorf("rpix(0,0) cycle delta = %d, want >=6 "+
			"(bsnes core.cpp:66 step(6) per bpp byte must be preserved)", delta)
	}
}

// TestRAMWriteBusWaitMatchesBsnes_PixelPath pins that pixel-cache RAM
// writes (writeBitplaneRow in pixel.go) pay step(6) per byte, matching
// bsnes flushPixelCache (core.cpp:98 step(regs.clsr ? 5 : 6) per byte
// write).
func TestRAMWriteBusWaitMatchesBsnes_PixelPath(t *testing.T) {
	d := New(nil, nil)
	d.SCMR = SCMRRAN | SCMRRON
	d.COLR = 0x01
	// Plot 8 pixels in row 0 (fills row, no commit yet).
	for x := uint16(0); x < 8; x++ {
		d.plot(x, 0, d.COLR)
	}
	cycBefore := d.Cycles()
	// Force flush of the row by plotting in a new row, which triggers
	// flushPixelCache → writeBitplaneRow (bpp=2 bytes written per row).
	d.plot(0, 8, d.COLR)
	cycAfter := d.Cycles()
	delta := cycAfter - cycBefore
	// bpp=2 bytes × 6cy each (write step) = 12cy minimum. Some
	// implementations also step before the read-modify-write read, so
	// we allow up to ~24cy. Required minimum: 6cy.
	if delta < 6 {
		t.Errorf("flushPixelCache cycle delta = %d, want >=6 "+
			"(bsnes core.cpp:98 step(6) per byte write must be preserved)", delta)
	}
}
