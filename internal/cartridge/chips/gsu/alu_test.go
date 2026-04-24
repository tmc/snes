package gsu

import "testing"

// loadAndRun primes a freshly-reset device with a ROM, enables SFR.G, and
// runs at most n instructions. The returned device is ready for
// inspection.
func loadAndRun(t *testing.T, rom []byte, n int) *Device {
	t.Helper()
	d := New(rom, nil)
	d.Go()
	d.Run(n)
	return d
}

// TestAddImmediate checks ADDi imm4 via ALT2 prefix. Opcode layout:
//
//	3E       ALT2
//	41       ADDi (slot 0x41, low nibble = imm = 1)
func TestAddImmediate(t *testing.T) {
	d := loadAndRun(t, []byte{0x3E, 0x41, 0x00}, 3)
	// R0 was 0, ADDi 1 -> R0 = 1, flags Z=0, S=0.
	if d.R[0] != 1 {
		t.Errorf("R0=%04X want 0001", d.R[0])
	}
	if d.SFR&SFRZ != 0 {
		t.Error("Z should not be set")
	}
}

// TestAddRegister — ADD R4 writes R0 = R0 + R4.
func TestAddRegister(t *testing.T) {
	d := New([]byte{0x44, 0x00}, nil) // ADD R4, then STOP
	d.R[0] = 0x1234
	d.R[4] = 0x1111
	d.Go()
	d.Run(2)
	if d.R[0] != 0x2345 {
		t.Errorf("R0=%04X want 2345", d.R[0])
	}
}

// TestAdcCarry — ALT1 selects ADC, consuming the carry bit.
func TestAdcCarry(t *testing.T) {
	d := New([]byte{0x3D, 0x44, 0x00}, nil) // ALT1, ADC R4
	d.R[0] = 0x00FF
	d.R[4] = 0x0001
	d.SFR |= SFRCY
	d.Go()
	d.Run(3)
	if d.R[0] != 0x0101 {
		t.Errorf("ADC R0=%04X want 0101", d.R[0])
	}
}

// TestAddOverflow — exercises the signed-overflow bit.
func TestAddOverflow(t *testing.T) {
	d := New([]byte{0x44, 0x00}, nil)
	d.R[0] = 0x7FFF
	d.R[4] = 0x0001
	d.Go()
	d.Run(2)
	if d.R[0] != 0x8000 {
		t.Errorf("R0=%04X want 8000", d.R[0])
	}
	if d.SFR&SFROV == 0 {
		t.Error("overflow should be set")
	}
}

// TestSubSetsCarryOnNoBorrow matches 6502-style carry semantics: 5-3
// should leave carry set.
func TestSubSetsCarryOnNoBorrow(t *testing.T) {
	d := New([]byte{0x54, 0x00}, nil)
	d.R[0] = 5
	d.R[4] = 3
	d.Go()
	d.Run(2)
	if d.R[0] != 2 {
		t.Errorf("R0=%04X want 2", d.R[0])
	}
	if d.SFR&SFRCY == 0 {
		t.Error("no-borrow subtract should set carry")
	}
}

// TestCmpDoesNotWriteBack — ALT3 (ALT1+ALT2) routes SUB to CMP; R0 must
// not be written.
func TestCmpDoesNotWriteBack(t *testing.T) {
	d := New([]byte{0x3F, 0x54, 0x00}, nil) // ALT3, CMP R4
	d.R[0] = 5
	d.R[4] = 5
	d.Go()
	d.Run(3)
	if d.R[0] != 5 {
		t.Errorf("CMP modified R0 = %04X, want 5", d.R[0])
	}
	if d.SFR&SFRZ == 0 {
		t.Error("CMP equal should set Z")
	}
}

// TestAltPrefixSelfClears — after executing an ALT1-ed instruction, a
// subsequent plain ADD must NOT behave as ADC. Sequence:
//
//	ALT1; ADC R4; ADD R4
//
// If the prefix leaks, the second ADD will use the carry as well.
func TestAltPrefixSelfClears(t *testing.T) {
	d := New([]byte{0x3D, 0x44, 0x44, 0x00}, nil)
	d.R[0] = 0
	d.R[4] = 1
	d.SFR |= SFRCY // start with carry set
	d.Go()
	d.Run(4)
	// First op (ADC R4, carry=1): R0 = 0 + 1 + 1 = 2, no carry out.
	// Second op (ADD R4): R0 = 2 + 1 = 3.
	// If prefix had leaked: R0 = 2 + 1 + <leaked carry> = 3 (same).
	// Differentiate via the flag: after the plain ADD, carry must be 0.
	if d.R[0] != 3 {
		t.Errorf("R0=%04X want 3", d.R[0])
	}
	// After ADC, carry was cleared (2 is below 0x10000). Then plain ADD
	// of (2,1) leaves carry clear. A leaking prefix would also end with
	// carry clear here, so also check ALT1 bit itself:
	if d.SFR&(SFRALT1|SFRALT2) != 0 {
		t.Errorf("ALT bits still set after second op: SFR=%04X", d.SFR)
	}
}

// TestAltPrefixExclusive — ALT1 then ALT2 must leave only ALT2 set; the
// two are exclusive, not cumulative.
func TestAltPrefixExclusive(t *testing.T) {
	d := New([]byte{0x3D, 0x3E, 0x01}, nil) // ALT1, ALT2, NOP
	d.Go()
	d.Run(3)
	if d.SFR&SFRALT1 != 0 {
		t.Errorf("ALT1 should be clear after ALT2: SFR=%04X", d.SFR)
	}
	// After NOP, ALT2 was consumed and cleared.
	if d.SFR&SFRALT2 != 0 {
		t.Errorf("ALT2 should be consumed by NOP: SFR=%04X", d.SFR)
	}
}

// TestFMULTvsLMULT — opcode 0x9F with no prefix is FMULT (top 16 bits of
// 16×16 signed product); with ALT1 it is LMULT (32-bit product split into
// R4 low and R[dst] high).
func TestFMULTvsLMULT(t *testing.T) {
	// FMULT: 0x4000 * 0x4000 = 0x10000000; top half = 0x1000.
	d := New([]byte{0x9F, 0x00}, nil)
	d.R[6] = 0x4000
	d.R[0] = 0x4000 // src defaults to R0 without FROM prefix
	d.Go()
	d.Run(2)
	if d.R[0] != 0x1000 {
		t.Errorf("FMULT R0=%04X want 1000", d.R[0])
	}

	// LMULT: same inputs.
	d2 := New([]byte{0x3D, 0x9F, 0x00}, nil)
	d2.R[6] = 0x4000
	d2.R[0] = 0x4000
	d2.Go()
	d2.Run(3)
	// Full 32-bit product = 0x10000000. Low16 -> R4 = 0x0000,
	// High16 -> R[dst]=R0 = 0x1000.
	if d2.R[4] != 0x0000 {
		t.Errorf("LMULT R4=%04X want 0000", d2.R[4])
	}
	if d2.R[0] != 0x1000 {
		t.Errorf("LMULT R0 (high)=%04X want 1000", d2.R[0])
	}
}

// TestIBT — no-ALT IBT sign-extends an immediate byte into a full Rn.
func TestIBT(t *testing.T) {
	d := New([]byte{0xA5, 0xFE, 0x00}, nil) // IBT R5, -2
	d.Go()
	d.Run(2)
	if d.R[5] != 0xFFFE {
		t.Errorf("IBT R5=%04X want FFFE", d.R[5])
	}
}

// TestBranchBEQ — taken branch jumps; not-taken falls through.
func TestBranchBEQ(t *testing.T) {
	// Program: BEQ +2, NOP, NOP, NOP (target)
	// With Z set the branch jumps; R15 after = start+2+2 = 4.
	d := New([]byte{0x09, 0x02, 0x01, 0x01, 0x01, 0x00}, nil)
	d.SFR |= SFRZ
	d.Go()
	// Execute exactly the branch.
	d.stepOne()
	if d.R[15] != 0x0004 {
		t.Errorf("BEQ taken PC=%04X want 0004", d.R[15])
	}

	// Without Z: falls through.
	d2 := New([]byte{0x09, 0x02, 0x01, 0x01, 0x01, 0x00}, nil)
	d2.Go()
	d2.stepOne()
	if d2.R[15] != 0x0002 {
		t.Errorf("BEQ not-taken PC=%04X want 0002", d2.R[15])
	}
}

// TestBranchBRA — unconditional branch regardless of flags.
func TestBranchBRA(t *testing.T) {
	d := New([]byte{0x05, 0xFE, 0x00}, nil) // BRA -2 => infinite loop to 0
	d.Go()
	d.stepOne()
	if d.R[15] != 0x0000 {
		t.Errorf("BRA -2 PC=%04X want 0000", d.R[15])
	}
}

// TestToPrefixRedirectsWriteback — TO R5 followed by ADD R4 stores into R5.
func TestToPrefixRedirectsWriteback(t *testing.T) {
	d := New([]byte{0x15, 0x44, 0x00}, nil) // TO R5, ADD R4
	d.R[0] = 1
	d.R[4] = 2
	d.Go()
	d.Run(3)
	if d.R[5] != 3 {
		t.Errorf("TO-redirected ADD R5=%04X want 3", d.R[5])
	}
	// R0 must not be clobbered.
	if d.R[0] != 1 {
		t.Errorf("R0 clobbered: %04X want 1", d.R[0])
	}
}
