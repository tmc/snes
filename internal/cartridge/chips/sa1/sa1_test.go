package sa1

import (
	"fmt"
	"testing"
)

func TestRegisterWindowMirrorsBanks(t *testing.T) {
	d := New()
	if !d.Write(0x00_2200, 0x80) {
		t.Fatalf("write to SA-1 control register rejected")
	}
	if got, ok := d.Read(0x80_2200); !ok || got != 0x80 {
		t.Fatalf("mirror read = %02X,%v want 80,true", got, ok)
	}
	if _, ok := d.Read(0x40_2200); ok {
		t.Fatalf("bank 40 unexpectedly mapped to SA-1 registers")
	}
	if d.Write(0x00_2400, 0x01) {
		t.Fatalf("outside SA-1 register window accepted write")
	}
}

func TestStateRoundTrip(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0x80)
	d.Write(0x00_2209, 0x20)
	d.SignalCPUIRQ(0x0b)
	d.SignalCharacterDMAIRQ()

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	for _, addr := range []uint32{0x00_2200, 0x00_2209} {
		want, _ := d.Read(addr)
		got, ok := restored.Read(addr)
		if !ok || got != want {
			t.Fatalf("restored read %06X = %02X,%v want %02X,true", addr, got, ok, want)
		}
	}
	if got, _ := restored.Read(0x00_2300); got != 0xab {
		t.Fatalf("restored SFR=%02X, want AB", got)
	}
}

func TestCPUStatusAndClear(t *testing.T) {
	d := New()
	d.Write(0x00_2201, 0xa0)
	d.SignalCPUIRQ(0x05)
	d.SignalCharacterDMAIRQ()

	if got, _ := d.Read(0x00_2300); got != 0xa5 {
		t.Fatalf("SFR before clear=%02X, want A5", got)
	}
	d.Write(0x00_2202, 0x80)
	if got, _ := d.Read(0x00_2300); got != 0x25 {
		t.Fatalf("SFR after CPU IRQ clear=%02X, want 25", got)
	}
	d.Write(0x00_2202, 0x20)
	if got, _ := d.Read(0x00_2300); got != 0x05 {
		t.Fatalf("SFR after CHDMA IRQ clear=%02X, want 05", got)
	}
}

func TestCPUBWRAMPageMasksToFiveBits(t *testing.T) {
	d := New()
	d.Write(0x00_2224, 0xff)
	if got := d.CPUBWRAMPage(); got != 0x1f {
		t.Fatalf("BMAPS page=%02X, want 1F", got)
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := restored.CPUBWRAMPage(); got != 0x1f {
		t.Fatalf("restored BMAPS page=%02X, want 1F", got)
	}
}

func TestBWRAMWriteProtectionState(t *testing.T) {
	d := New()
	d.Write(0x00_2228, 0x02)
	if d.AllowCPUBWRAMWrite(0x0003ff) {
		t.Fatalf("protected BW-RAM address reported writable")
	}
	if !d.AllowCPUBWRAMWrite(0x000400) {
		t.Fatalf("unprotected BW-RAM address reported read-only")
	}
	d.Write(0x00_2226, 0x80)
	if !d.AllowCPUBWRAMWrite(0x000000) {
		t.Fatalf("SWEN did not enable protected BW-RAM write")
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if !restored.AllowCPUBWRAMWrite(0x000000) {
		t.Fatalf("restored SWEN did not preserve BW-RAM write enable")
	}
}

func TestCPUROMBankMapping(t *testing.T) {
	d := New()
	for _, tt := range []struct {
		addr uint32
		want uint32
	}{
		{0x00_9234, 0x00_1234},
		{0x20_9234, 0x10_1234},
		{0x80_9234, 0x00_1234},
		{0xc0_1234, 0x00_1234},
		{0xd0_1234, 0x10_1234},
		{0xe0_1234, 0x20_1234},
		{0xf0_1234, 0x30_1234},
	} {
		got, ok := d.CPUROMAddress(tt.addr)
		if !ok || got != tt.want {
			t.Fatalf("CPUROMAddress(%06X)=%06X,%v want %06X,true", tt.addr, got, ok, tt.want)
		}
	}

	d.Write(0x00_2220, 0x82)
	got, ok := d.CPUROMAddress(0xc0_1234)
	if !ok || got != 0x20_1234 {
		t.Fatalf("remapped C bank=%06X,%v want 201234,true", got, ok)
	}
	got, ok = d.CPUROMAddress(0x00_9234)
	if !ok || got != 0x20_1234 {
		t.Fatalf("remapped low C bank=%06X,%v want 201234,true", got, ok)
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	got, ok = restored.CPUROMAddress(0xc0_1234)
	if !ok || got != 0x20_1234 {
		t.Fatalf("restored remapped C bank=%06X,%v want 201234,true", got, ok)
	}
}

func TestSA1BWRAMLinearView(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x03)
	ram := make([]byte, 256*1024)

	addr, ok := d.SA1BWRAMAddress(0x40_1234)
	if !ok || addr != 0x7234 {
		t.Fatalf("SA1BWRAMAddress=%05X,%v want 07234,true", addr, ok)
	}
	d.WriteSA1BWRAM(ram, 0x40_1234, 0x5a)
	if got := ram[0x7234]; got != 0x5a {
		t.Fatalf("linear BW-RAM byte=%02X, want 5A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x40_1234); got != 0x5a {
		t.Fatalf("linear BW-RAM read=%02X, want 5A", got)
	}
}

func TestSA1BWRAMBitmapView4BPP(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x80|0x02)
	d.Write(0x00_2227, 0x80)
	ram := make([]byte, 256*1024)

	d.WriteSA1BWRAM(ram, 0x60_0000, 0x0a)
	d.WriteSA1BWRAM(ram, 0x60_0001, 0x05)
	if got := ram[0x2000]; got != 0x5a {
		t.Fatalf("4bpp packed byte=%02X, want 5A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x60_0000); got != 0x0a {
		t.Fatalf("4bpp low pixel=%02X, want 0A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x60_0001); got != 0x05 {
		t.Fatalf("4bpp high pixel=%02X, want 05", got)
	}
}

func TestSA1BWRAMBitmapView2BPPSerializes(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x80|0x02)
	d.Write(0x00_2227, 0x80)
	d.Write(0x00_223f, 0x80)
	ram := make([]byte, 256*1024)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	for i, v := range []uint8{1, 2, 3, 0} {
		restored.WriteSA1BWRAM(ram, 0x60_0000+uint32(i), v)
	}
	if got := ram[0x1000]; got != 0x39 {
		t.Fatalf("2bpp packed byte=%02X, want 39", got)
	}
	for i, want := range []uint8{1, 2, 3, 0} {
		if got := restored.ReadSA1BWRAM(ram, 0x60_0000+uint32(i)); got != want {
			t.Fatalf("2bpp pixel %d=%02X, want %02X", i, got, want)
		}
	}
}

// runArith writes MA, MB, and triggers the SA-1 arithmetic unit by writing
// MBH ($2254). MCNT ($2250) selects mode (acm bit=$02, md bit=$01).
func runArith(t *testing.T, d *Device, mcnt uint8, ma int16, mb int16) {
	t.Helper()
	d.Write(0x00_2250, mcnt)
	d.Write(0x00_2251, uint8(uint16(ma)))
	d.Write(0x00_2252, uint8(uint16(ma)>>8))
	d.Write(0x00_2253, uint8(uint16(mb)))
	d.Write(0x00_2254, uint8(uint16(mb)>>8))
}

func readMR40(t *testing.T, d *Device) uint64 {
	t.Helper()
	var mr uint64
	for i, addr := range []uint32{0x2306, 0x2307, 0x2308, 0x2309, 0x230a} {
		b, ok := d.Read(addr)
		if !ok {
			t.Fatalf("read %04X: not mapped", addr)
		}
		mr |= uint64(b) << (8 * i)
	}
	return mr
}

func readOverflow(t *testing.T, d *Device) bool {
	t.Helper()
	b, ok := d.Read(0x230b)
	if !ok {
		t.Fatalf("read 230B: not mapped")
	}
	return b&0x80 != 0
}

func TestSA1ArithmeticSignedMultiply(t *testing.T) {
	cases := []struct {
		name string
		a, b int16
		want uint64 // low 32 bits used; uint64 of (uint32)(int16*int16) per bsnes
	}{
		{"unit", 1, 1, 0x00000001},
		{"max_pos_squared", 0x7FFF, 0x7FFF, 0x3FFF0001},
		{"neg_one_squared", -1, -1, 0x00000001},
		{"neg_one_times_one", -1, 1, 0xFFFFFFFF},
		{"min_neg_squared", -32768, -32768, 0x40000000},
		{"min_neg_times_one", -32768, 1, 0xFFFF8000},
		{"zero", 0, 0x1234, 0x00000000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			runArith(t, d, 0x00, tc.a, tc.b) // ACM=0, MD=0 → signed multiply
			if got := readMR40(t, d); got != tc.want {
				t.Fatalf("MR=%010X, want %010X", got, tc.want)
			}
			// signed multiplication clears MB only.
			b, _ := d.Read(0x2253)
			if b != 0 {
				t.Fatalf("MBL after multiply=%02X, want 00", b)
			}
		})
	}
}

func TestSA1ArithmeticSignedDivideFloor(t *testing.T) {
	// bsnes io.cpp:446-453 implements floor-toward-negative-infinity
	// division: dividend_ext = (int16)ma + (uint32)(uint16)mb*65536;
	// remainder = dividend_ext % mb; quotient = dividend_ext / mb - 65536.
	// MR = remainder<<16 | quotient.
	cases := []struct {
		name      string
		ma, mb    int16
		quotient  int16
		remainder uint16
	}{
		{"pos_pos_exact", 6, 3, 2, 0},
		{"pos_pos_remainder", 7, 3, 2, 1},
		{"neg_pos_floor", -7, 3, -3, 2},
		{"neg_pos_exact", -6, 3, -2, 0},
		{"min_by_one", -32768, 1, -32768, 0},
		{"max_by_one", 32767, 1, 32767, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			runArith(t, d, 0x01, tc.ma, tc.mb) // ACM=0, MD=1 → signed divide
			want := uint64(tc.remainder)<<16 | uint64(uint16(tc.quotient))
			if got := readMR40(t, d); got != want {
				t.Fatalf("MR=%010X, want %010X (q=%d r=%d)",
					got, want, tc.quotient, tc.remainder)
			}
			// signed division clears both MA and MB.
			al, _ := d.Read(0x2251)
			ah, _ := d.Read(0x2252)
			bl, _ := d.Read(0x2253)
			if al|ah|bl != 0 {
				t.Fatalf("MA/MB after divide: AL=%02X AH=%02X BL=%02X, want 00", al, ah, bl)
			}
		})
	}
}

func TestSA1ArithmeticDivideByZero(t *testing.T) {
	d := New()
	runArith(t, d, 0x01, 0x1234, 0)
	if got := readMR40(t, d); got != 0 {
		t.Fatalf("MR after divide-by-zero=%010X, want 0", got)
	}
}

func TestSA1ArithmeticAccumulativeMultiply(t *testing.T) {
	d := New()
	// MCNT = $02 → ACM=1, MD=0. The MCNT write resets MR to 0 per
	// bsnes io.cpp:408 ("if(acm) mr = 0;").
	d.Write(0x00_2250, 0x02)

	type op struct{ a, b int16 }
	steps := []op{
		{100, 200},     // mr += 20000 → 20000
		{-50, 30},      // mr += -1500 → 18500
		{0x7FFF, 0x7FFF}, // mr += 0x3FFF0001 → 0x3FFF496D
	}
	var want int64
	for _, s := range steps {
		want += int64(s.a) * int64(s.b)
		// only set MA/MB then trigger via MBH; MCNT stays at $02.
		d.Write(0x00_2251, uint8(uint16(s.a)))
		d.Write(0x00_2252, uint8(uint16(s.a)>>8))
		d.Write(0x00_2253, uint8(uint16(s.b)))
		d.Write(0x00_2254, uint8(uint16(s.b)>>8))
	}
	const mask40 = (uint64(1) << 40) - 1
	wantMR := uint64(want) & mask40
	if got := readMR40(t, d); got != wantMR {
		t.Fatalf("MR after accumulate=%010X, want %010X", got, wantMR)
	}
	if readOverflow(t, d) {
		t.Fatalf("overflow set after non-overflowing accumulate")
	}
	// MB cleared after each accumulate trigger.
	bl, _ := d.Read(0x2253)
	if bl != 0 {
		t.Fatalf("MBL after accumulate=%02X, want 00", bl)
	}
}

func TestSA1ArithmeticAccumulativeOverflow(t *testing.T) {
	d := New()
	d.Write(0x00_2250, 0x02)
	// 0x7FFF * 0x7FFF = 0x3FFF0001 ≈ 2^30. Repeat until sum > 2^40 so
	// that bsnes' "overflow = mr >> 40" assignment latches true.
	const limit = uint64(1) << 40
	var sum uint64
	reps := 0
	for sum <= limit {
		d.Write(0x00_2251, 0xFF)
		d.Write(0x00_2252, 0x7F)
		d.Write(0x00_2253, 0xFF)
		d.Write(0x00_2254, 0x7F)
		sum += uint64(0x3FFF0001)
		reps++
		if reps > 2000 {
			t.Fatalf("did not overflow in %d reps (sum=%d)", reps, sum)
		}
	}
	const mask40 = (uint64(1) << 40) - 1
	if got, want := readMR40(t, d), sum&mask40; got != want {
		t.Fatalf("MR=%010X, want %010X (reps=%d sum=%d)", got, want, reps, sum)
	}
	if !readOverflow(t, d) {
		t.Fatalf("overflow not latched after sum crossed 2^40 (sum=%d)", sum)
	}
}

func TestSA1ArithmeticMCNTACMBitResetsMR(t *testing.T) {
	d := New()
	// First, do a signed multiply to populate MR.
	runArith(t, d, 0x00, 100, 200)
	if readMR40(t, d) == 0 {
		t.Fatalf("MR should be non-zero after multiply")
	}
	// Writing MCNT with ACM=1 resets MR=0 per bsnes io.cpp:408.
	d.Write(0x00_2250, 0x02)
	if got := readMR40(t, d); got != 0 {
		t.Fatalf("MR after MCNT ACM=1 write=%010X, want 0", got)
	}
}

func TestSA1ArithmeticOverflowReadIsBit7Only(t *testing.T) {
	// bsnes io.cpp:67-68: $230B returns overflow << 7. Other bits are 0.
	d := New()
	d.Write(0x00_2250, 0x02)
	// Overflow not yet latched.
	if got, _ := d.Read(0x230b); got != 0 {
		t.Fatalf("$230B before overflow=%02X, want 00", got)
	}
}

// vbdSetVA programs the 24-bit VA register at $2259/$225A/$225B. Writing
// $225B clears VBIT per bsnes io.cpp:486.
func vbdSetVA(t *testing.T, d *Device, va uint32) {
	t.Helper()
	d.Write(0x00_2259, uint8(va))
	d.Write(0x00_225a, uint8(va>>8))
	d.Write(0x00_225b, uint8(va>>16))
}

// installROM gives the SA-1 device a synthetic ROM-byte reader for VBR
// reads. Returns 0xFF outside the slice (matches bsnes' "unmapped" default).
func installROM(d *Device, rom []byte) {
	d.SetROMReader(func(addr uint32) uint8 {
		if int(addr) < len(rom) {
			return rom[addr]
		}
		return 0xff
	})
}

func TestSA1VBDFixedModeAdvancesOnVBSWrite(t *testing.T) {
	d := New()
	vbdSetVA(t, d, 0x000000)
	// $2258 = $08: HL=0 (fixed), VB=8.
	d.Write(0x00_2258, 0x08)
	// Per io.cpp:476-478: vbit += 8 (=8); va += (vbit>>3)=1; vbit &= 7 (=0).
	if got, _ := d.Read(0x2259); got != 0x01 {
		t.Fatalf("VAL after fixed +8 = %02X, want 01", got)
	}
	if got, _ := d.Read(0x225a); got != 0x00 {
		t.Fatalf("VAH after fixed +8 = %02X, want 00", got)
	}
}

func TestSA1VBDAutoModeNoAdvanceOnVBSWrite(t *testing.T) {
	d := New()
	vbdSetVA(t, d, 0x001234)
	// $2258 = $88: HL=1 (auto), VB=8. Auto mode: do NOT advance on write.
	d.Write(0x00_2258, 0x88)
	if got, _ := d.Read(0x2259); got != 0x34 {
		t.Fatalf("VAL after auto VBS = %02X, want 34 (no advance)", got)
	}
	if got, _ := d.Read(0x225a); got != 0x12 {
		t.Fatalf("VAH after auto VBS = %02X, want 12", got)
	}
}

func TestSA1VBDVBZeroIs16(t *testing.T) {
	d := New()
	vbdSetVA(t, d, 0x000000)
	// $2258 = $00: HL=0 (fixed), VB=0 → bsnes substitutes VB=16 (io.cpp:472).
	d.Write(0x00_2258, 0x00)
	// vbit += 16 (=16); va += 16>>3 = 2; vbit &= 7 = 0.
	if got, _ := d.Read(0x2259); got != 0x02 {
		t.Fatalf("VAL after fixed VB=0 (=16) = %02X, want 02", got)
	}
}

func TestSA1VBDVAComposesFrom24BitWrites(t *testing.T) {
	d := New()
	d.Write(0x00_2259, 0xCD)
	d.Write(0x00_225a, 0xAB)
	d.Write(0x00_225b, 0x12)
	if got, _ := d.Read(0x2259); got != 0xCD {
		t.Fatalf("VAL = %02X, want CD", got)
	}
	if got, _ := d.Read(0x225a); got != 0xAB {
		t.Fatalf("VAH = %02X, want AB", got)
	}
	if got, _ := d.Read(0x225b); got != 0x12 {
		t.Fatalf("VAB = %02X, want 12", got)
	}
}

func TestSA1VBD225BClearsVBIT(t *testing.T) {
	d := New()
	vbdSetVA(t, d, 0x000000)
	// Push VBIT to 5 via fixed mode VB=5.
	d.Write(0x00_2258, 0x05)
	// $225B write should reset VBIT to 0 per io.cpp:486.
	d.Write(0x00_225b, 0x00)
	// Now write VBS=8 fixed: vbit was 0, so vbit+=8=8; va+=1; vbit=0.
	vbdSetVA(t, d, 0x000000)
	d.Write(0x00_225b, 0x00) // re-clear and rewrite VAB, VBIT=0
	d.Write(0x00_2258, 0x08)
	if got, _ := d.Read(0x2259); got != 0x01 {
		t.Fatalf("VAL after VBIT reset + fixed +8 = %02X, want 01", got)
	}
}

func TestSA1VBD230CReadsWithoutAdvancing(t *testing.T) {
	d := New()
	// ROM bytes: 0x55 0xAA 0x33 ... at addresses 0x008000+.
	installROM(d, []byte{0x55, 0xAA, 0x33})
	vbdSetVA(t, d, 0x000000)
	// $2258 = $80: HL=1 (auto), VB=0 → 16. Avoid advance side-effects on write.
	d.Write(0x00_2258, 0x80)
	prevLo, _ := d.Read(0x2259)
	prevMid, _ := d.Read(0x225a)
	prevHi, _ := d.Read(0x225b)

	// $230C: 24-bit data >> vbit (=0) → low byte = 0x55.
	got, ok := d.Read(0x230c)
	if !ok {
		t.Fatalf("$230C not mapped")
	}
	if got != 0x55 {
		t.Fatalf("$230C low byte = %02X, want 55", got)
	}
	// VA must not advance (only $230D in auto mode does).
	if lo, _ := d.Read(0x2259); lo != prevLo {
		t.Fatalf("VAL advanced on $230C read: %02X -> %02X", prevLo, lo)
	}
	if mid, _ := d.Read(0x225a); mid != prevMid {
		t.Fatalf("VAH advanced on $230C read: %02X -> %02X", prevMid, mid)
	}
	if hi, _ := d.Read(0x225b); hi != prevHi {
		t.Fatalf("VAB advanced on $230C read: %02X -> %02X", prevHi, hi)
	}
}

func TestSA1VBD230DAutoModeAdvances(t *testing.T) {
	d := New()
	installROM(d, []byte{0x55, 0xAA, 0x33})
	vbdSetVA(t, d, 0x000000)
	// HL=1 (auto), VB=8.
	d.Write(0x00_2258, 0x88)
	// $230D returns bits 8..15 of (24-bit data >> vbit). vbit=0 → 0xAA.
	got, ok := d.Read(0x230d)
	if !ok {
		t.Fatalf("$230D not mapped")
	}
	if got != 0xAA {
		t.Fatalf("$230D high byte = %02X, want AA", got)
	}
	// After auto-advance: vbit += 8 = 8; va += 1; vbit &= 7 = 0.
	if lo, _ := d.Read(0x2259); lo != 0x01 {
		t.Fatalf("VAL after $230D auto advance = %02X, want 01", lo)
	}
}

func TestSA1VBD230DFixedModeNoAdvance(t *testing.T) {
	d := New()
	installROM(d, []byte{0x55, 0xAA, 0x33})
	vbdSetVA(t, d, 0x000000)
	// HL=0 (fixed), VB=8 → advances ON THE WRITE itself, not on read.
	d.Write(0x00_2258, 0x08)
	// After VBS write: vbit was 0, +8=8, va+=1, vbit=0.
	// Now $230D should read at VA=1 with vbit=0 → bits 8..15 of (3-byte
	// stream starting at 1) = 0x33.
	got, _ := d.Read(0x230d)
	if got != 0x33 {
		t.Fatalf("$230D fixed mode = %02X, want 33", got)
	}
	// $230D must NOT advance VA again in fixed mode.
	if lo, _ := d.Read(0x2259); lo != 0x01 {
		t.Fatalf("VAL after fixed $230D = %02X, want 01 (no second advance)", lo)
	}
}

func TestSA1VBDBitExtractionShiftPattern(t *testing.T) {
	// rom: 0x21 0x43 0x65 → 24-bit little-endian value $654321.
	// (data.byte(0) = LSB per bsnes uint24 layout — confirmed by io.cpp:71-74.)
	rom := []byte{0x21, 0x43, 0x65}
	for _, vbit := range []uint8{0, 1, 4, 7} {
		t.Run(fmt.Sprintf("vbit=%d", vbit), func(t *testing.T) {
			d := New()
			installROM(d, rom)
			vbdSetVA(t, d, 0x000000)
			// Drive VBIT to the desired value via $2258 in fixed mode
			// (HL=0, VB=vbit advances vbit by vbit; va += vbit>>3 = 0
			// for vbit<=7, leaving vbit at the requested value).
			if vbit > 0 {
				d.Write(0x00_2258, vbit&0x0F)
			}
			data := uint32(rom[0]) | uint32(rom[1])<<8 | uint32(rom[2])<<16
			shifted := data >> vbit
			wantLo := uint8(shifted & 0xFF)
			wantHi := uint8((shifted >> 8) & 0xFF)
			gotLo, _ := d.Read(0x230c)
			if gotLo != wantLo {
				t.Fatalf("vbit=%d $230C = %02X, want %02X", vbit, gotLo, wantLo)
			}
			// Fixed mode: $230D must NOT advance, so it returns the
			// next 8 bits of the same shift window.
			gotHi, _ := d.Read(0x230d)
			if gotHi != wantHi {
				t.Fatalf("vbit=%d $230D = %02X, want %02X", vbit, gotHi, wantHi)
			}
		})
	}
}

func TestSA1VBDOutOfRangeReadReturns0xFFFromMux(t *testing.T) {
	// No ROM reader installed → readVBR returns 0xFF (bsnes memory.cpp:132).
	d := New()
	vbdSetVA(t, d, 0x000000)
	d.Write(0x00_2258, 0x80) // HL=1, VB=0→16
	got, _ := d.Read(0x230c)
	// data = 0xFF | 0xFF<<8 | 0xFF<<16 = 0xFFFFFF; >> 0 = same; low = 0xFF.
	if got != 0xFF {
		t.Fatalf("$230C with no reader = %02X, want FF", got)
	}
}

func TestSA1VBDVAWrapsAt24Bits(t *testing.T) {
	d := New()
	installROM(d, []byte{0xAA, 0xBB, 0xCC})
	// VA = $FFFFFE. Auto VBS write does not advance.
	vbdSetVA(t, d, 0xFFFFFE)
	d.Write(0x00_2258, 0x88) // HL=1, VB=8
	// $230D: read 3 bytes at VA, advance va += 1 → wraps from $FFFFFE+1 = $FFFFFF
	// (still <= 0xFFFFFF). Just verify no panic + advance.
	if _, ok := d.Read(0x230d); !ok {
		t.Fatalf("$230D not mapped")
	}
	lo, _ := d.Read(0x2259)
	mid, _ := d.Read(0x225a)
	hi, _ := d.Read(0x225b)
	gotVA := uint32(lo) | uint32(mid)<<8 | uint32(hi)<<16
	if gotVA != 0xFFFFFF {
		t.Fatalf("VA after first auto advance = %06X, want FFFFFF", gotVA)
	}
	// Trigger another auto advance by reading $230D again. va += 1 → $FFFFFF + 1
	// must wrap to $000000 within 24 bits.
	if _, ok := d.Read(0x230d); !ok {
		t.Fatalf("$230D not mapped (second)")
	}
	lo, _ = d.Read(0x2259)
	mid, _ = d.Read(0x225a)
	hi, _ = d.Read(0x225b)
	gotVA = uint32(lo) | uint32(mid)<<8 | uint32(hi)<<16
	if gotVA != 0x000000 {
		t.Fatalf("VA after wrap = %06X, want 000000", gotVA)
	}
}

func TestSA1VBDStateRoundTripIncludesVBDFields(t *testing.T) {
	d := New()
	installROM(d, []byte{0x11, 0x22, 0x33})
	vbdSetVA(t, d, 0x000000)
	d.Write(0x00_2258, 0x84) // HL=1 (auto), VB=4
	// Drive VBIT to 4 by performing one auto $230D read so the cursor
	// is mid-byte. After read: vbit was 0; vbit+=4=4; va+=4>>3=0; vbit=4.
	if _, ok := d.Read(0x230d); !ok {
		t.Fatalf("$230D not mapped (warmup)")
	}
	if d.vbdVBIT != 4 {
		t.Fatalf("warmup VBIT=%d, want 4", d.vbdVBIT)
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New()
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	// Mirror-byte readbacks must match.
	for _, addr := range []uint32{0x2258, 0x2259, 0x225a, 0x225b} {
		want, _ := d.Read(addr)
		got, _ := r.Read(addr)
		if got != want {
			t.Fatalf("restored %04X = %02X, want %02X", addr, got, want)
		}
	}
	// Behavioral check: install the same synthetic ROM on the restored
	// device, then a $230D read must return the same byte AND must
	// advance VA in auto mode the same way the original would have.
	installROM(r, []byte{0x11, 0x22, 0x33})
	wantHi, _ := d.Read(0x230d)
	gotHi, _ := r.Read(0x230d)
	if gotHi != wantHi {
		t.Fatalf("restored $230D = %02X, want %02X (live HL/VB/VBIT not restored?)",
			gotHi, wantHi)
	}
	// Both devices should now agree on the post-advance VA.
	for _, addr := range []uint32{0x2259, 0x225a, 0x225b} {
		w, _ := d.Read(addr)
		g, _ := r.Read(addr)
		if g != w {
			t.Fatalf("post-advance %04X = %02X, want %02X", addr, g, w)
		}
	}
}
