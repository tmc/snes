package gsu

import "testing"

// Pin the MULT/UMULT/FMULT/LMULT cycle penalties against bsnes.
//
// MULT/UMULT (op $80-$8F) at bsnes/processor/gsu/instructions.cpp:228:
//
//	if(!regs.cfgr.ms0) step(regs.clsr ? 1 : 2);
//
// FMULT/LMULT (op $9F) at instructions.cpp:304:
//
//	step((regs.cfgr.ms0 ? 3 : 7) * (regs.clsr ? 1 : 2));
//
// The penalty depends on both CFGR.MS0 (bit 5, mask 0x20) and CLSR
// (bit 0, mask 0x01). Cycle-cost for each axis combination:
//
//	MULT/UMULT step:        FMULT/LMULT step:
//	  MS0=0 CLSR=0 -> 2cy     MS0=0 CLSR=0 -> 14cy (slow,slow)
//	  MS0=0 CLSR=1 -> 1cy     MS0=0 CLSR=1 ->  7cy (slow,fast)
//	  MS0=1 CLSR=0 -> 0cy     MS0=1 CLSR=0 ->  6cy (fast,slow)
//	  MS0=1 CLSR=1 -> 0cy     MS0=1 CLSR=1 ->  3cy (fast,fast)
//
// We isolate the MULT step penalty by running two devices through
// identical ROMs with the SAME CLSR but different MS0 (or vice
// versa), so prefetch/cache costs cancel out and the cycle delta is
// exactly the bsnes step penalty difference.

// TestMULTStepPenaltyMS0 pins MULT (op $80) penalty difference
// between MS0=0 (slow multiply, 1-2cy) and MS0=1 (fast multiply,
// 0cy). With CLSR=0, the difference is 2 - 0 = 2 cycles per MULT.
// With CLSR=1, it is 1 - 0 = 1 cycle per MULT. Both must hold post-
// fix; pre-fix both penalties are 0 so the delta is 0 (test fails).
func TestMULTStepPenaltyMS0(t *testing.T) {
	for _, tt := range []struct {
		name      string
		clsr      uint8
		wantDelta uint64
	}{
		{"clsr=0_slow_clock", 0, 2},
		{"clsr=1_fast_clock", 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// ROM: MULT R0 (op $80, R0 = src*dst since no prefix);
			// STOP. R0=0x0001 is enough — the multiply result and
			// flags don't matter for cycle accounting.
			rom := []byte{0x80, 0x00}

			// Slow multiply (MS0=0)
			slow := New(rom, nil)
			slow.CLSR = tt.clsr
			slow.CFGR = 0x00 // MS0=0
			slow.R[0] = 0x0002
			GoAndRun(slow, 1) // run MULT
			cycSlow := slow.Cycles()

			// Fast multiply (MS0=1)
			fast := New(rom, nil)
			fast.CLSR = tt.clsr
			fast.CFGR = 0x20 // MS0=1
			fast.R[0] = 0x0002
			GoAndRun(fast, 1)
			cycFast := fast.Cycles()

			if cycSlow < cycFast {
				t.Fatalf("slow=%d < fast=%d (slow must be >= fast for MULT)",
					cycSlow, cycFast)
			}
			delta := cycSlow - cycFast
			if delta != tt.wantDelta {
				t.Errorf("MULT MS0=0 - MS0=1 delta = %d cy, want %d "+
					"(bsnes instructions.cpp:228 step(clsr ? 1 : 2) when !ms0; "+
					"clsr=%d)", delta, tt.wantDelta, tt.clsr)
			}
		})
	}
}

// TestFMULTStepPenaltyMS0 pins FMULT (op $9F, no prefix) MS0
// penalty: MS0=0 -> 7*(2-clsr) cycles, MS0=1 -> 3*(2-clsr) cycles.
// Difference per FMULT:
//
//	CLSR=0: (7 - 3) * 2 = 8 cycles
//	CLSR=1: (7 - 3) * 1 = 4 cycles
//
// Pre-fix the delta is 0 (test fails). Post-fix it matches.
func TestFMULTStepPenaltyMS0(t *testing.T) {
	for _, tt := range []struct {
		name      string
		clsr      uint8
		wantDelta uint64
	}{
		{"clsr=0_slow_clock", 0, 8},
		{"clsr=1_fast_clock", 1, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// ROM: FMULT (op $9F); STOP. FMULT uses R6 and R[srcReg].
			rom := []byte{0x9F, 0x00}

			slow := New(rom, nil)
			slow.CLSR = tt.clsr
			slow.CFGR = 0x00 // MS0=0
			slow.R[6] = 0x0002
			slow.R[0] = 0x0003
			GoAndRun(slow, 1) // FMULT
			cycSlow := slow.Cycles()

			fast := New(rom, nil)
			fast.CLSR = tt.clsr
			fast.CFGR = 0x20 // MS0=1
			fast.R[6] = 0x0002
			fast.R[0] = 0x0003
			GoAndRun(fast, 1)
			cycFast := fast.Cycles()

			if cycSlow < cycFast {
				t.Fatalf("FMULT slow=%d < fast=%d", cycSlow, cycFast)
			}
			delta := cycSlow - cycFast
			if delta != tt.wantDelta {
				t.Errorf("FMULT MS0=0 - MS0=1 delta = %d cy, want %d "+
					"(bsnes instructions.cpp:304 step((ms0 ? 3 : 7) * (clsr ? 1 : 2)); "+
					"clsr=%d)", delta, tt.wantDelta, tt.clsr)
			}
		})
	}
}

// TestLMULTSamePenaltyAsFMULT pins that LMULT (op $9F under ALT1)
// shares the FMULT step formula. ALT1 prefix takes one extra
// retire (op $3D) so we run both through the same prefix prefix
// and assert the MS0 delta is the same as FMULT.
func TestLMULTSamePenaltyAsFMULT(t *testing.T) {
	// ROM: ALT1 (op $3D); LMULT (op $9F under ALT1); STOP
	rom := []byte{0x3D, 0x9F, 0x00}

	slow := New(rom, nil)
	slow.CLSR = 0
	slow.CFGR = 0x00
	slow.R[6] = 0x0002
	slow.R[0] = 0x0003
	GoAndRun(slow, 2) // ALT1 + LMULT
	cycSlow := slow.Cycles()

	fast := New(rom, nil)
	fast.CLSR = 0
	fast.CFGR = 0x20
	fast.R[6] = 0x0002
	fast.R[0] = 0x0003
	GoAndRun(fast, 2)
	cycFast := fast.Cycles()

	if cycSlow < cycFast {
		t.Fatalf("LMULT slow=%d < fast=%d", cycSlow, cycFast)
	}
	delta := cycSlow - cycFast
	const want = uint64(8) // CLSR=0: (7-3)*2 = 8
	if delta != want {
		t.Errorf("LMULT MS0=0 - MS0=1 delta = %d cy, want %d "+
			"(bsnes instructions.cpp:304 same step() as FMULT)", delta, want)
	}
}

// TestUMULTSamePenaltyAsMULT pins UMULT (op $80 under ALT1) shares
// MULT's step formula. With CLSR=0, MS0 delta is 2cy.
func TestUMULTSamePenaltyAsMULT(t *testing.T) {
	// ROM: ALT1 (op $3D); UMULT R0 (op $80 under ALT1); STOP
	rom := []byte{0x3D, 0x80, 0x00}

	slow := New(rom, nil)
	slow.CLSR = 0
	slow.CFGR = 0x00
	slow.R[0] = 0x0002
	GoAndRun(slow, 2)
	cycSlow := slow.Cycles()

	fast := New(rom, nil)
	fast.CLSR = 0
	fast.CFGR = 0x20
	fast.R[0] = 0x0002
	GoAndRun(fast, 2)
	cycFast := fast.Cycles()

	if cycSlow < cycFast {
		t.Fatalf("UMULT slow=%d < fast=%d", cycSlow, cycFast)
	}
	delta := cycSlow - cycFast
	const want = uint64(2)
	if delta != want {
		t.Errorf("UMULT MS0=0 - MS0=1 delta = %d cy, want %d "+
			"(bsnes instructions.cpp:228 same step() as MULT)", delta, want)
	}
}
