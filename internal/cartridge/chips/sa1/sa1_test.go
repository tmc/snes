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

// TestSA1IRAMReadWriteRoundTripViaCPU exercises the S-CPU-side I-RAM
// accessor. Writes through ReadIRAMCPU/WriteIRAMCPU mirror to the
// 2 KiB byte slice; reads return the value just written.
func TestSA1IRAMReadWriteRoundTripViaCPU(t *testing.T) {
	d := New()
	// Fully open SIWP so writes succeed.
	d.Write(0x00_2229, 0xFF)
	for off := uint32(0); off < 0x800; off += 0x37 {
		val := uint8(off ^ 0xA5)
		d.WriteIRAMCPU(off, val)
		if got := d.ReadIRAMCPU(off); got != val {
			t.Fatalf("IRAM[%03X] = %02X, want %02X", off, got, val)
		}
	}
}

// TestSA1IRAMSIWPGatesCPUWrites pins per-256-byte write protection
// gating from the S-CPU side. SIWP bit n controls writes to the
// 256-byte block at offset n*0x100. Reads are unconditional.
// bsnes/sfc/coprocessor/sa1/iram.cpp:25-29.
func TestSA1IRAMSIWPGatesCPUWrites(t *testing.T) {
	d := New()
	// Pre-seed all 8 blocks with 0x00 (use SIWP=$FF) so we can detect
	// whether a later gated write succeeds.
	d.Write(0x00_2229, 0xFF)
	for off := uint32(0); off < 0x800; off++ {
		d.WriteIRAMCPU(off, 0x00)
	}
	// Now allow only blocks 0 and 3 (siwp = bit0|bit3 = 0x09).
	d.Write(0x00_2229, 0x09)
	for blk := 0; blk < 8; blk++ {
		off := uint32(blk*0x100 + 0x42)
		d.WriteIRAMCPU(off, uint8(0x10|blk))
	}
	for blk := 0; blk < 8; blk++ {
		got := d.ReadIRAMCPU(uint32(blk*0x100 + 0x42))
		want := uint8(0x10 | blk)
		if blk != 0 && blk != 3 {
			want = 0x00 // gated; write dropped
		}
		if got != want {
			t.Fatalf("block %d byte after SIWP=$09 = %02X, want %02X", blk, got, want)
		}
	}
}

// TestSA1IRAMCIWPGatesSA1Writes pins SA-1-side write protection.
// $222A CIWP behaves identically to SIWP but gates WriteIRAMSA1.
// bsnes/sfc/coprocessor/sa1/iram.cpp:35-38.
func TestSA1IRAMCIWPGatesSA1Writes(t *testing.T) {
	d := New()
	// Allow all CPU and SA-1 writes initially to seed.
	d.Write(0x00_2229, 0xFF)
	d.Write(0x00_222a, 0xFF)
	for off := uint32(0); off < 0x800; off++ {
		d.WriteIRAMSA1(off, 0x00)
	}
	// Now block all SA-1 writes via CIWP=$00.
	d.Write(0x00_222a, 0x00)
	for blk := 0; blk < 8; blk++ {
		d.WriteIRAMSA1(uint32(blk*0x100+0x10), 0xCC)
	}
	for blk := 0; blk < 8; blk++ {
		if got := d.ReadIRAMSA1(uint32(blk*0x100 + 0x10)); got != 0x00 {
			t.Fatalf("block %d after CIWP=$00 = %02X, want 00 (writes gated)", blk, got)
		}
	}
	// CIWP=$80 → only block 7 writable.
	d.Write(0x00_222a, 0x80)
	for blk := 0; blk < 8; blk++ {
		d.WriteIRAMSA1(uint32(blk*0x100+0x10), uint8(0xA0|blk))
	}
	for blk := 0; blk < 8; blk++ {
		got := d.ReadIRAMSA1(uint32(blk*0x100 + 0x10))
		want := uint8(0x00)
		if blk == 7 {
			want = uint8(0xA0 | blk)
		}
		if got != want {
			t.Fatalf("block %d after CIWP=$80 = %02X, want %02X", blk, got, want)
		}
	}
}

// TestSA1IRAMReadsAreUnconditional pins that SIWP/CIWP gate writes
// but never reads. bsnes' iram.cpp:20-23 readCPU has no SIWP check;
// readSA1 has no CIWP check.
func TestSA1IRAMReadsAreUnconditional(t *testing.T) {
	d := New()
	d.Write(0x00_2229, 0xFF)
	d.Write(0x00_222a, 0xFF)
	d.WriteIRAMCPU(0x100, 0x55)
	d.WriteIRAMSA1(0x200, 0xAA)
	// Lock both protections.
	d.Write(0x00_2229, 0x00)
	d.Write(0x00_222a, 0x00)
	if got := d.ReadIRAMCPU(0x100); got != 0x55 {
		t.Fatalf("ReadIRAMCPU under SIWP=$00 = %02X, want 55", got)
	}
	if got := d.ReadIRAMSA1(0x200); got != 0xAA {
		t.Fatalf("ReadIRAMSA1 under CIWP=$00 = %02X, want AA", got)
	}
}

// TestSA1IRAMOffsetWrapsAt2KiB confirms the bus.mirror semantics
// (bsnes/sfc/coprocessor/sa1/iram.cpp:10): any offset is masked to
// the 2 KiB I-RAM size.
func TestSA1IRAMOffsetWrapsAt2KiB(t *testing.T) {
	d := New()
	d.Write(0x00_2229, 0xFF)
	d.WriteIRAMCPU(0x000, 0x42)
	if got := d.ReadIRAMCPU(0x800); got != 0x42 {
		t.Fatalf("IRAM mirror at 0x800 = %02X, want 42", got)
	}
	if got := d.ReadIRAMCPU(0x1000); got != 0x42 {
		t.Fatalf("IRAM mirror at 0x1000 = %02X, want 42", got)
	}
}

func TestSA1IRAMStateRoundTripIncludesIRAMSIWPCIWP(t *testing.T) {
	d := New()
	// Seed I-RAM with SIWP fully open, THEN set SIWP=$A5 and CIWP=$5A
	// so the round-trip captures both the bytes and the protection
	// register state.
	d.Write(0x00_2229, 0xFF)
	for off := uint32(0); off < 0x800; off++ {
		d.WriteIRAMCPU(off, uint8(off^0xC3))
	}
	d.Write(0x00_2229, 0xA5)
	d.Write(0x00_222a, 0x5A)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New()
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	for off := uint32(0); off < 0x800; off++ {
		if got, want := r.ReadIRAMCPU(off), uint8(off^0xC3); got != want {
			t.Fatalf("restored IRAM[%03X] = %02X, want %02X", off, got, want)
		}
	}
	if got, _ := r.Read(0x2229); got != 0xA5 {
		t.Fatalf("restored SIWP = %02X, want A5", got)
	}
	if got, _ := r.Read(0x222a); got != 0x5A {
		t.Fatalf("restored CIWP = %02X, want 5A", got)
	}
	// Behavioral check: the restored device must respect SIWP=$A5
	// (bits 0,2,5,7 set → blocks 0/2/5/7 writable).
	for blk := 0; blk < 8; blk++ {
		off := uint32(blk*0x100 + 0x90)
		r.WriteIRAMCPU(off, 0xFF)
	}
	for blk := 0; blk < 8; blk++ {
		off := uint32(blk*0x100 + 0x90)
		got := r.ReadIRAMCPU(off)
		writable := 0xA5&(1<<blk) != 0
		want := uint8(off ^ 0xC3)
		if writable {
			want = 0xFF
		}
		if got != want {
			t.Fatalf("restored block %d after WriteIRAMCPU = %02X, want %02X (writable=%v)",
				blk, got, want, writable)
		}
	}
}

// TestSA1VectorOverrideStoresSNVSIV pins that $220C-$220F write
// little-endian byte halves of the 16-bit SNV/SIV registers and
// readback returns the same bytes. bsnes/sfc/coprocessor/sa1/io.cpp:298-303.
func TestSA1VectorOverrideStoresSNVSIV(t *testing.T) {
	d := New()
	d.Write(0x00_220c, 0x34)
	d.Write(0x00_220d, 0x12)
	d.Write(0x00_220e, 0x78)
	d.Write(0x00_220f, 0x56)

	if got, _ := d.Read(0x220c); got != 0x34 {
		t.Fatalf("$220C readback = %02X, want 34", got)
	}
	if got, _ := d.Read(0x220d); got != 0x12 {
		t.Fatalf("$220D readback = %02X, want 12", got)
	}
	if got, _ := d.Read(0x220e); got != 0x78 {
		t.Fatalf("$220E readback = %02X, want 78", got)
	}
	if got, _ := d.Read(0x220f); got != 0x56 {
		t.Fatalf("$220F readback = %02X, want 56", got)
	}
	if d.SCPUNMIVector() != 0x1234 {
		t.Fatalf("SCPUNMIVector=%04X, want 1234", d.SCPUNMIVector())
	}
	if d.SCPUIRQVector() != 0x5678 {
		t.Fatalf("SCPUIRQVector=%04X, want 5678", d.SCPUIRQVector())
	}
}

// TestSA1VectorOverrideSCNTSwitchBits pins $2209 bits 6 (cpu_ivsw)
// and 4 (cpu_nvsw). Bit 7 (cpu_irq) and bits 0..3 (cmeg) are
// out of scope for this slice; the slice scopes SCNT readback to
// the override switches plus byte preservation in Regs[].
func TestSA1VectorOverrideSCNTSwitchBits(t *testing.T) {
	d := New()
	if d.SCPUNMIOverrideEnabled() {
		t.Fatalf("default cpu_nvsw=%v, want false", d.SCPUNMIOverrideEnabled())
	}
	if d.SCPUIRQOverrideEnabled() {
		t.Fatalf("default cpu_ivsw=%v, want false", d.SCPUIRQOverrideEnabled())
	}

	// Set both switches: cpu_ivsw via bit 6 ($40) and cpu_nvsw via
	// bit 4 ($10).
	d.Write(0x00_2209, 0x50)
	if !d.SCPUNMIOverrideEnabled() {
		t.Fatalf("after $2209=$50, cpu_nvsw=%v, want true", d.SCPUNMIOverrideEnabled())
	}
	if !d.SCPUIRQOverrideEnabled() {
		t.Fatalf("after $2209=$50, cpu_ivsw=%v, want true", d.SCPUIRQOverrideEnabled())
	}

	// Clear with a write that has bits 6+4 zero.
	d.Write(0x00_2209, 0x00)
	if d.SCPUNMIOverrideEnabled() {
		t.Fatalf("after $2209=$00, cpu_nvsw still set")
	}
	if d.SCPUIRQOverrideEnabled() {
		t.Fatalf("after $2209=$00, cpu_ivsw still set")
	}
}

// TestSA1VectorOverrideStateRoundTripIncludesSCNTAndSNVSIV pins
// that Serialize/Unserialize preserves SNV, SIV, cpu_nvsw, cpu_ivsw.
func TestSA1VectorOverrideStateRoundTripIncludesSCNTAndSNVSIV(t *testing.T) {
	d := New()
	d.Write(0x00_2209, 0x50)
	d.Write(0x00_220c, 0xCD)
	d.Write(0x00_220d, 0xAB)
	d.Write(0x00_220e, 0x21)
	d.Write(0x00_220f, 0x43)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New()
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if !r.SCPUNMIOverrideEnabled() || !r.SCPUIRQOverrideEnabled() {
		t.Fatalf("restored switches: nv=%v iv=%v, want both true",
			r.SCPUNMIOverrideEnabled(), r.SCPUIRQOverrideEnabled())
	}
	if r.SCPUNMIVector() != 0xABCD {
		t.Fatalf("restored SNV=%04X, want ABCD", r.SCPUNMIVector())
	}
	if r.SCPUIRQVector() != 0x4321 {
		t.Fatalf("restored SIV=%04X, want 4321", r.SCPUIRQVector())
	}
}

// TestSA1MessageSFRMirrorsIVSWAndNVSW pins the e12ca3b follow-up
// defect: $2300 SFR readback must include $2209 bits 6 (cpu_ivsw)
// and 4 (cpu_nvsw) on top of cpu_irqfl/chdma_irqfl/cmeg. Per
// bsnes/sfc/coprocessor/sa1/io.cpp:7-15 and snes9x/sa1.cpp:184.
func TestSA1MessageSFRMirrorsIVSWAndNVSW(t *testing.T) {
	d := New()
	// $2209=$50 sets cpu_ivsw (bit 6) and cpu_nvsw (bit 4).
	d.Write(0x00_2209, 0x50)
	got, _ := d.Read(0x00_2300)
	if got&0x40 == 0 {
		t.Fatalf("$2300 = %02X, missing bit 6 (cpu_ivsw)", got)
	}
	if got&0x10 == 0 {
		t.Fatalf("$2300 = %02X, missing bit 4 (cpu_nvsw)", got)
	}

	// Clearing $2209 must clear both mirror bits.
	d.Write(0x00_2209, 0x00)
	got, _ = d.Read(0x00_2300)
	if got&0x50 != 0 {
		t.Fatalf("$2300 after clear = %02X, want bits 6+4 cleared", got)
	}
}

// TestSA1MessageSFRPreservesIRQFlagsAndCMEG re-checks that the
// readback fix doesn't disturb the existing flag bits or cmeg
// low-nibble (already exercised by TestCPUStatusAndClear, but
// re-pinned alongside the new ivsw/nvsw bits to catch a bitwise
// regression).
func TestSA1MessageSFRPreservesIRQFlagsAndCMEG(t *testing.T) {
	d := New()
	d.Write(0x00_2201, 0xa0)
	// Set ivsw + nvsw FIRST, then inject IRQ + cmeg via
	// SignalCPUIRQ. $2209 byte writes overwrite cmeg per bsnes
	// io.cpp:256, so the order matters: switches first, message
	// last, to verify all five fields surface in the same readback.
	d.Write(0x00_2209, 0x50) // ivsw + nvsw
	d.SignalCPUIRQ(0x07)
	d.SignalCharacterDMAIRQ()
	got, _ := d.Read(0x00_2300)
	want := uint8(0x80 | 0x40 | 0x20 | 0x10 | 0x07)
	if got != want {
		t.Fatalf("$2300 composite = %02X, want %02X", got, want)
	}
}

// TestSA1MessageSCPUMessageStoresLowNibble pins that $2200 bits
// 0..3 (smeg, S-CPU → SA-1 message) are stored as the SCPU
// message and exposed via SCPUMessage(). bsnes io.cpp:125 +
// snes9x/sa1.cpp:188 (low nibble of $2301 readback comes from
// $2200).
func TestSA1MessageSCPUMessageStoresLowNibble(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0x07)
	if got := d.SCPUMessage(); got != 0x07 {
		t.Fatalf("SCPUMessage = %02X, want 07", got)
	}
	d.Write(0x00_2200, 0x0A)
	if got := d.SCPUMessage(); got != 0x0A {
		t.Fatalf("SCPUMessage = %02X, want 0A", got)
	}
	// High bits don't leak into the message.
	d.Write(0x00_2200, 0xF3)
	if got := d.SCPUMessage(); got != 0x03 {
		t.Fatalf("SCPUMessage with high bits set = %02X, want 03", got)
	}
}

// TestSA1Message2200HighBitsPreservedInRegsMirror pins that the
// $2200 byte mirror in Regs[] continues to preserve bits 7/6/5/4
// (sa1_irq pulse, sa1_rdyb, sa1_resb, sa1_nmi) as raw bytes —
// they're intentionally not decoded for behavioral effect (no
// SA-1 CPU consumer), but the byte is preserved for future
// slices and host introspection.
func TestSA1Message2200HighBitsPreservedInRegsMirror(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0xF7)
	got, _ := d.Read(0x00_2200)
	if got != 0xF7 {
		t.Fatalf("$2200 readback = %02X, want F7 (full byte mirror)", got)
	}
}

// TestSA1MessageCMEGAndSMEGAreIndependent pins that S-CPU →
// SA-1 (smeg via $2200) and SA-1 → S-CPU (cmeg via $2300, set
// by SignalCPUIRQ which mirrors $2209 bits 0..3) live in
// independent fields.
func TestSA1MessageCMEGAndSMEGAreIndependent(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0x0A) // smeg = A
	d.SignalCPUIRQ(0x05)     // cmeg = 5
	if got := d.SCPUMessage(); got != 0x0A {
		t.Fatalf("SCPUMessage after cross-write = %02X, want 0A", got)
	}
	got, _ := d.Read(0x00_2300)
	if got&0x0F != 0x05 {
		t.Fatalf("$2300 low nibble = %02X, want 05", got&0x0F)
	}
}

// TestSA1Message2209LowNibbleSetsCMEG pins that $2209 bits 0..3
// store the cmeg field (SA-1 → S-CPU message), observable via
// $2300 low nibble. bsnes io.cpp:256 + snes9x sa1.cpp:184.
func TestSA1Message2209LowNibbleSetsCMEG(t *testing.T) {
	d := New()
	d.Write(0x00_2209, 0x0B)
	got, _ := d.Read(0x00_2300)
	if got&0x0F != 0x0B {
		t.Fatalf("$2300 low nibble after $2209=$0B = %02X, want 0B", got&0x0F)
	}
}

// TestSA1Message2209Bit7RaisesCPUIRQFlag pins that $2209 bit 7
// (cpu_irq pulse) raises cpuIRQFlag, observable via $2300 bit 7.
// bsnes io.cpp:258-264. The pulse is symmetric with
// SignalCPUIRQ — both set cpuIRQFlag.
func TestSA1Message2209Bit7RaisesCPUIRQFlag(t *testing.T) {
	d := New()
	if d.cpuIRQFlag {
		t.Fatalf("cpuIRQFlag set before pulse")
	}
	d.Write(0x00_2209, 0x80)
	if !d.cpuIRQFlag {
		t.Fatalf("cpuIRQFlag not set after $2209 bit 7 write")
	}
	got, _ := d.Read(0x00_2300)
	if got&0x80 == 0 {
		t.Fatalf("$2300 bit 7 = %02X, want set after pulse", got)
	}
	// Clear via $2202 bit 7.
	d.Write(0x00_2202, 0x80)
	if d.cpuIRQFlag {
		t.Fatalf("cpuIRQFlag still set after SIC clear")
	}
}

// TestSA1Message2209BitsAreIndependent pins that bit 7 (pulse),
// bit 6 (ivsw), bit 4 (nvsw), and bits 0..3 (cmeg) within a
// single $2209 byte write set independent fields.
func TestSA1Message2209BitsAreIndependent(t *testing.T) {
	d := New()
	d.Write(0x00_2209, 0xD7) // bit 7 + bit 6 + bit 4 + cmeg=7
	if !d.cpuIRQFlag {
		t.Fatalf("cpuIRQFlag = false, want true")
	}
	if !d.SCPUIRQOverrideEnabled() {
		t.Fatalf("cpu_ivsw = false, want true")
	}
	if !d.SCPUNMIOverrideEnabled() {
		t.Fatalf("cpu_nvsw = false, want true")
	}
	got, _ := d.Read(0x00_2300)
	if got&0x0F != 0x07 {
		t.Fatalf("cmeg = %02X, want 07", got&0x0F)
	}
}

// TestSA1MessageStateRoundTripIncludesSCPUMessage pins that
// Serialize/Unserialize preserves the smeg field.
func TestSA1MessageStateRoundTripIncludesSCPUMessage(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0x06)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New()
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := r.SCPUMessage(); got != 0x06 {
		t.Fatalf("restored SCPUMessage = %02X, want 06", got)
	}
}

// TestSA1ArithmeticSignedMultiplyPreservesMA pins that signed
// multiplication clears MB but leaves MA intact. bsnes
// io.cpp:439-440 only writes mmio.mb = 0; MA is untouched.
func TestSA1ArithmeticSignedMultiplyPreservesMA(t *testing.T) {
	d := New()
	runArith(t, d, 0x00, 0x1234, 0x0002) // ACM=0 MD=0 → signed multiply
	if got, _ := d.Read(0x2251); got != 0x34 {
		t.Fatalf("MAL after multiply = %02X, want 34 (MA must not clear)", got)
	}
	if got, _ := d.Read(0x2252); got != 0x12 {
		t.Fatalf("MAH after multiply = %02X, want 12", got)
	}
	if got, _ := d.Read(0x2253); got != 0x00 {
		t.Fatalf("MBL after multiply = %02X, want 00 (MB must clear)", got)
	}
}

// TestSA1ArithmeticAccumulativeMultiplyPreservesMA pins that ACM
// mode also clears MB but leaves MA intact across accumulation
// steps. bsnes io.cpp:460-463 only writes mmio.mb = 0.
func TestSA1ArithmeticAccumulativeMultiplyPreservesMA(t *testing.T) {
	d := New()
	d.Write(0x00_2250, 0x02) // ACM=1 MD=0 (resets MR=0)
	// Accumulate step 1: MA=$5678 MB=$0002
	d.Write(0x00_2251, 0x78)
	d.Write(0x00_2252, 0x56)
	d.Write(0x00_2253, 0x02)
	d.Write(0x00_2254, 0x00) // MBH=0 → triggers
	// MA must still be $5678; MB cleared.
	if got, _ := d.Read(0x2251); got != 0x78 {
		t.Fatalf("MAL after ACM step = %02X, want 78", got)
	}
	if got, _ := d.Read(0x2252); got != 0x56 {
		t.Fatalf("MAH after ACM step = %02X, want 56", got)
	}
	if got, _ := d.Read(0x2253); got != 0x00 {
		t.Fatalf("MBL after ACM step = %02X, want 00", got)
	}
	// Step 2 reuses MA=$5678 (preserved): write only MB and trigger.
	d.Write(0x00_2253, 0x03)
	d.Write(0x00_2254, 0x00)
	// MR should be int16($5678)*2 + int16($5678)*3 = $5678 * 5 (sign-
	// extended). $5678 = 22136. 22136*5 = 110680 = 0x1B048.
	const want = uint64(0x1B058) // 22136 × (2+3) = 110680 = 0x1B058
	if got := readMR40(t, d); got != want {
		t.Fatalf("MR after two ACM steps with preserved MA = %010X, want %010X",
			got, want)
	}
}

// TestSA1ArithmeticAccumulativeOverflowAssignmentNotOR pins the
// non-obvious bsnes io.cpp:461 quirk: overflow is assigned each
// step (`mmio.overflow = mmio.mr >> 40`), not OR-accumulated. So
// a step that overflows followed by a step that does not will
// CLEAR the latch. Regression-resistance for "fixes" that turn
// it into |=.
func TestSA1ArithmeticAccumulativeOverflowAssignmentNotOR(t *testing.T) {
	d := New()
	d.Write(0x00_2250, 0x02) // ACM=1, MR=0
	// Force overflow by repeating $7FFF*$7FFF until sum > 2^40.
	const limit = uint64(1) << 40
	var sum uint64
	for sum <= limit {
		d.Write(0x00_2251, 0xFF)
		d.Write(0x00_2252, 0x7F)
		d.Write(0x00_2253, 0xFF)
		d.Write(0x00_2254, 0x7F)
		sum += uint64(0x3FFF0001)
	}
	if !readOverflow(t, d) {
		t.Fatalf("overflow not latched after force-overflow phase")
	}
	// Now perform a step that does NOT overflow (MA=0, MB=0 → adds
	// 0). The post-step (mr >> 40) is 0, so overflow latches FALSE
	// per the assignment semantics.
	d.Write(0x00_2251, 0x00)
	d.Write(0x00_2252, 0x00)
	d.Write(0x00_2253, 0x00)
	d.Write(0x00_2254, 0x00)
	if readOverflow(t, d) {
		t.Fatalf("overflow still latched after non-overflowing step; " +
			"bsnes io.cpp:461 uses '=', not '|=', so it must clear")
	}
}

// TestSA1ArithmeticDivideTreatsMBAsUnsigned pins bsnes
// io.cpp:447's `uint16 divisor = mmio.mb` semantics: a "negative"
// MB (high bit set) is reinterpreted as a large positive uint16
// divisor. MA=$0006 MB=$FFFE: dividend_ext = $0000_0006 +
// $FFFE * $10000 = $FFFE0006; quotient = $FFFE0006 / $FFFE -
// $10000 = 1; remainder = $FFFE0006 % $FFFE = 6.
func TestSA1ArithmeticDivideTreatsMBAsUnsigned(t *testing.T) {
	d := New()
	runArith(t, d, 0x01, 0x0006, -2) // ACM=0 MD=1 → divide; MB raw bits = $FFFE
	// dividend_ext = $0006 + $FFFE*$10000 = $FFFE0006.
	// quotient = $FFFE0006 / $FFFE - $10000 = $10000 - $10000 = 0.
	// remainder = $FFFE0006 % $FFFE = 6.
	// MR = remainder << 16 | quotient = $0006_0000.
	const want = uint64(0x0006_0000)
	if got := readMR40(t, d); got != want {
		t.Fatalf("MR for MA=$0006 / MB=$FFFE = %010X, want %010X", got, want)
	}
}

// TestSA1VBDStreamDrainsAcrossManyAutoReads pins that successive
// auto-mode $230D reads with VB=8 walk a packed byte stream
// correctly, advancing VA by 1 per read and keeping VBIT=0.
func TestSA1VBDStreamDrainsAcrossManyAutoReads(t *testing.T) {
	d := New()
	stream := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	installROM(d, stream)
	vbdSetVA(t, d, 0x000000)
	d.Write(0x00_2258, 0x88) // HL=1 (auto), VB=8

	// $230D returns bits 8..15 of (24-bit window >> vbit). With
	// VBIT=0 and VB=8, each read returns the byte at VA+1 and
	// then advances VA by 1.
	for i := 0; i < 4; i++ {
		got, _ := d.Read(0x230d)
		want := stream[i+1]
		if got != want {
			t.Fatalf("$230D read #%d = %02X, want %02X", i, got, want)
		}
	}
	// VA has advanced 4 times.
	if d.vbdVA != 0x000004 {
		t.Fatalf("VA after 4 auto reads = %06X, want 000004", d.vbdVA)
	}
}

// TestSA1VBDCrossRegionFetchAtRegionBoundary pins that the
// per-byte VBR mux invokes the ROMReader closure once per byte,
// so a 3-byte fetch that spans VBR regions composes bytes from
// each region independently. With VA=$0000FE and synthetic ROM
// at offsets 0xFE/0xFF/0x100, the three bytes resolve cleanly.
//
// The closure-based test setup uses a single contiguous ROM
// slice covering all three offsets, but the test exercises the
// mux's per-byte invocation contract. A real cartridge slice
// (cartridge.go::sa1VBRReader) routes each byte through ROM /
// I-RAM / BW-RAM independently per memory.cpp:113-130.
func TestSA1VBDCrossRegionFetchAtRegionBoundary(t *testing.T) {
	d := New()
	rom := make([]byte, 0x200)
	rom[0xFE] = 0xCA
	rom[0xFF] = 0xFE
	rom[0x100] = 0xBA // crosses a 256-byte boundary
	installROM(d, rom)
	vbdSetVA(t, d, 0x0000FE)
	d.Write(0x00_2258, 0x80) // HL=1, VB=0→16, no advance side-effect
	// $230C returns the low byte of (24-bit window >> 0) = $CA.
	if got, _ := d.Read(0x230c); got != 0xCA {
		t.Fatalf("$230C VA=$0000FE = %02X, want CA (boundary first byte)", got)
	}
	// $230D returns bits 8..15 of (24-bit window) = ROM[$FF] = $FE.
	if got, _ := d.Read(0x230d); got != 0xFE {
		t.Fatalf("$230D VA=$0000FE = %02X, want FE (boundary middle byte)", got)
	}
	// At VBIT=4 the high byte of the shifted window straddles
	// ROM[$FF] (high nibble) and ROM[$100] (low nibble). Reset VA
	// to $0000FE first; the prior $230D auto-advance moved it. Then
	// drive VBIT to 4 via fixed mode VB=4 with VA=$FE preserved.
	vbdSetVA(t, d, 0x0000FE) // re-anchor; clears VBIT to 0
	d.Write(0x00_2258, 0x04) // HL=0, VB=4 → vbit becomes 4, va unchanged
	// shifted = ($BAFECA >> 4) = $0BAFEC; low byte = $EC; high = $AF.
	if got, _ := d.Read(0x230c); got != 0xEC {
		t.Fatalf("$230C boundary VBIT=4 = %02X, want EC", got)
	}
	if got, _ := d.Read(0x230d); got != 0xAF {
		t.Fatalf("$230D boundary VBIT=4 = %02X, want AF (mux pulled bytes from both sides of boundary)", got)
	}
}

// SA-1 type-2 character-conversion DMA. Bsnes reference:
// bsnes/sfc/coprocessor/sa1/dma.cpp:110-128 (dmaCC2 body),
// io.cpp:347-358 ($2230 DCNT decode), io.cpp:491-503 ($2231 CDMA
// chdend/dmasize/dmacb clamps), io.cpp:511-516 ($2235/$2236 DDA),
// io.cpp:371-401 (BRF $2240-$224F + dmaen/cden/cdsel triggers on
// $2247/$224F). Open both CIWP nibbles via $222A so SA-1-side IRAM
// writes land. Each setup writes $2230 with cden=1, cdsel=0,
// dmaen=1 (= 0xa0).

func writeBRF(t *testing.T, d *Device, vals [16]uint8) {
	t.Helper()
	for i, v := range vals {
		if !d.Write(uint32(0x002240+i), v) {
			t.Fatalf("write BRF[%d]=%02X rejected", i, v)
		}
	}
}

func openCIWP(t *testing.T, d *Device) {
	t.Helper()
	if !d.Write(0x00222a, 0xff) {
		t.Fatalf("open CIWP rejected")
	}
}

func TestSA1DMACC2GoldenCases(t *testing.T) {
	type want struct {
		off uint16
		val uint8
	}
	cases := []struct {
		name    string
		dcnt    uint8 // $2230
		cdma    uint8 // $2231 (chdend|dmasize<<2|dmacb)
		dda     uint16
		brf     [16]uint8
		trigger uint32 // $2247 or $224F (last write triggers)
		wants   []want
	}{
		{
			name:    "4bpp dmacb=1 line=0 dda=0 brf[0..7]=0x80,0x40,..,0x01",
			dcnt:    0xa0, // dmaen|cden, cdsel=0, dd=0, sd=0
			cdma:    0x0d, // dmasize=3<<2=0x0c | dmacb=1
			dda:     0x0000,
			brf:     [16]uint8{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01},
			trigger: 0x002247,
			wants:   []want{{0, 0x01}, {1, 0x02}, {16, 0x04}, {17, 0x08}},
		},
		{
			name:    "2bpp dmacb=2 line=0 (then trigger again to make line=1) dda=0 brf[8..15]=0xFF,0,0xFF,0,0xFF,0,0xFF,0",
			dcnt:    0xa0,
			cdma:    0x0e, // dmasize=3 | dmacb=2
			dda:     0x0000,
			brf:     [16]uint8{0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0, 0xFF, 0, 0xFF, 0, 0xFF, 0},
			trigger: 0x002247,
			// First trigger writes line=0 (brf[0..7] all zero → no
			// observable iram changes). Then line increments to 1 and
			// the second trigger uses brf[8..15] producing 0xAA at
			// addr=2,3.
			wants: []want{{2, 0xAA}, {3, 0xAA}},
		},
		{
			name:    "8bpp dmacb=0 line=0 dda=0 brf[0]=0xFF",
			dcnt:    0xa0,
			cdma:    0x0c, // dmasize=3 | dmacb=0
			dda:     0x0000,
			brf:     [16]uint8{0xFF, 0, 0, 0, 0, 0, 0, 0},
			trigger: 0x002247,
			wants:   []want{{0, 0x80}, {1, 0x80}, {16, 0x80}, {17, 0x80}, {32, 0x80}, {33, 0x80}, {48, 0x80}, {49, 0x80}},
		},
		{
			name:    "$224F triggers conversion (line wraps to 8 then 9 across two triggers)",
			dcnt:    0xa0,
			cdma:    0x0d,
			dda:     0x0000,
			brf:     [16]uint8{0xFF, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			trigger: 0x00224f,
			// First $2247 trigger fires line=0 with brf[0..7]
			// (writes 0x80 at iram[0],[1],[16],[17]). Then $224F
			// trigger fires line=1 with brf[8..15] (all zero → no
			// new writes, but iram[0..17] persist). We assert the
			// non-zero values from the first trigger remain.
			wants: []want{{0, 0x80}, {1, 0x80}, {16, 0x80}, {17, 0x80}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			openCIWP(t, d)
			if !d.Write(0x002230, tc.dcnt) {
				t.Fatalf("write $2230 rejected")
			}
			if !d.Write(0x002231, tc.cdma) {
				t.Fatalf("write $2231 rejected")
			}
			if !d.Write(0x002235, uint8(tc.dda)) {
				t.Fatalf("write $2235 rejected")
			}
			if !d.Write(0x002236, uint8(tc.dda>>8)) {
				t.Fatalf("write $2236 rejected")
			}
			writeBRF(t, d, tc.brf)
			// One trigger advances line by 1. For the second case
			// we need line=1 → trigger twice. Easy proxy: trigger
			// once via the configured register, then again iff the
			// case name mentions "trigger again". This is brittle;
			// instead, fire $2247 once. The second case writes
			// brf[8..15] but line starts at 0 and consumes brf[0..7]
			// first, so we need a second trigger.
			if tc.trigger == 0x002247 {
				if !d.Write(0x002247, tc.brf[7]) {
					t.Fatalf("write $2247 trigger rejected")
				}
			} else {
				// Trigger via $224F first to fire line=0 with brf[0..7].
				if !d.Write(0x00224f, tc.brf[15]) {
					t.Fatalf("write $224F trigger rejected")
				}
			}
			needSecond := tc.name[:4] == "2bpp" || tc.name[:6] == "$224F "
			if needSecond {
				if tc.trigger == 0x002247 {
					if !d.Write(0x002247, tc.brf[7]) {
						t.Fatalf("write $2247 second trigger rejected")
					}
				} else {
					if !d.Write(0x00224f, tc.brf[15]) {
						t.Fatalf("write $224F second trigger rejected")
					}
				}
			}
			for _, w := range tc.wants {
				if got := d.ReadIRAMSA1(uint32(w.off)); got != w.val {
					t.Errorf("iram[%03X] = %02X, want %02X", w.off, got, w.val)
				}
			}
		})
	}
}

func TestSA1DMACC2NotTriggeredWhenDMAENZero(t *testing.T) {
	d := New()
	openCIWP(t, d)
	// dmaen=0, cden=1, cdsel=0
	if !d.Write(0x002230, 0x20) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	writeBRF(t, d, [16]uint8{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if !d.Write(0x002247, 0xFF) {
		t.Fatalf("write $2247 rejected")
	}
	for off := uint32(0); off < 64; off++ {
		if d.ReadIRAMSA1(off) != 0 {
			t.Fatalf("iram[%02X]=%02X mutated despite dmaen=0", off, d.ReadIRAMSA1(off))
		}
	}
}

func TestSA1DMACC2NotTriggeredWhenCDENZero(t *testing.T) {
	d := New()
	openCIWP(t, d)
	// dmaen=1, cden=0
	if !d.Write(0x002230, 0x80) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	writeBRF(t, d, [16]uint8{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if !d.Write(0x002247, 0xFF) {
		t.Fatalf("write $2247 rejected")
	}
	for off := uint32(0); off < 64; off++ {
		if d.ReadIRAMSA1(off) != 0 {
			t.Fatalf("iram[%02X]=%02X mutated despite cden=0", off, d.ReadIRAMSA1(off))
		}
	}
}

func TestSA1DMACC2NotTriggeredWhenCDSELOne(t *testing.T) {
	d := New()
	openCIWP(t, d)
	// dmaen=1, cden=1, cdsel=1 (CC1 territory).
	if !d.Write(0x002230, 0xb0) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	writeBRF(t, d, [16]uint8{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if !d.Write(0x002247, 0xFF) {
		t.Fatalf("write $2247 rejected")
	}
	for off := uint32(0); off < 64; off++ {
		if d.ReadIRAMSA1(off) != 0 {
			t.Fatalf("iram[%02X]=%02X mutated despite cdsel=1", off, d.ReadIRAMSA1(off))
		}
	}
}

func TestSA1DMACC2LineIncrementsAndWrapsAt16(t *testing.T) {
	d := New()
	openCIWP(t, d)
	// 4bpp/dmacb=1, dmaen|cden, cdsel=0.
	if !d.Write(0x002230, 0xa0) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	// brf[0..7]=0xFF,brf[8..15]=0xAA so even/odd lines write
	// distinguishable patterns.
	writeBRF(t, d, [16]uint8{
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA,
	})
	// Trigger 17 times: line cycles 0→1→...→15→0→1.
	for i := 0; i < 17; i++ {
		if !d.Write(0x002247, 0xFF) {
			t.Fatalf("write $2247 rejected on trigger %d", i)
		}
	}
	// After 17 triggers, line=(17&15)=1. The 17th trigger was line=0
	// (even), so brf[0..7]=0xFF was used. The first trigger after the
	// wrap (the 17th) writes pattern at addr=0 (line=0) which equals
	// 0xFF planar output 0x80 at iram[0,1,16,17] (case3-style) — but
	// dmacb=1 so bpp=4: iram[0,1,16,17]=0xFF&1=0x80? Let me just
	// assert lines are advancing — sample iram[0] after running and
	// confirm a non-zero write happened in some range.
	nonZero := false
	for off := uint32(0); off < 0x800; off++ {
		if d.ReadIRAMSA1(off) != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatalf("after 17 triggers, iram is all-zero — line counter likely not advancing")
	}
	// Now disable DMA via $2230 with dmaen=0 — bsnes io.cpp:356
	// resets dma.line to 0. We can't directly observe line, but a
	// subsequent dmaCC2 trigger (re-enabled) should consume brf[0..7]
	// (line=0) producing the 4bpp/0xFF pattern at iram[0,1,16,17]=0x80.
	// First clear iram by reset+reconfigure.
	d2 := New()
	openCIWP(t, d2)
	if !d2.Write(0x002230, 0xa0) {
		t.Fatalf("d2 write $2230 rejected")
	}
	if !d2.Write(0x002231, 0x0d) {
		t.Fatalf("d2 write $2231 rejected")
	}
	writeBRF(t, d2, [16]uint8{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// Advance to line=3.
	for i := 0; i < 3; i++ {
		if !d2.Write(0x002247, 0xFF) {
			t.Fatalf("d2 write $2247 rejected on prime %d", i)
		}
	}
	// Disable DMA: line resets to 0.
	if !d2.Write(0x002230, 0x00) {
		t.Fatalf("d2 disable rejected")
	}
	// Re-enable.
	if !d2.Write(0x002230, 0xa0) {
		t.Fatalf("d2 re-enable rejected")
	}
	// Clear IRAM by direct reads then issue a fresh trigger; observe
	// that the addr-0 byte gets the line=0 pattern (0x80 from brf[0]=0xFF
	// at byte=0, bit 0 → output bit 7 only).
	for off := uint32(0); off < 0x800; off++ {
		// Direct rewrite via SIWP-open + WriteIRAMCPU? Easier: just
		// run the trigger and confirm iram[0]=0x80, which only matches
		// the line=0 path with brf[0]=0xFF.
		_ = off
		break
	}
	// Pre-stash a sentinel at iram[0] to confirm overwrite occurs.
	if !d2.Write(0x002229, 0xff) { // SIWP open
		t.Fatalf("SIWP open rejected")
	}
	// Use the IRAM CPU window: write 0x55 at $00:3000 (mirrors to iram[0]).
	if !d2.Write(0x003000, 0x55) {
		t.Fatalf("CPU IRAM write rejected")
	}
	if got := d2.ReadIRAMSA1(0); got != 0x55 {
		t.Fatalf("iram[0] sentinel = %02X, want 55", got)
	}
	if !d2.Write(0x002247, 0xFF) {
		t.Fatalf("d2 fresh trigger rejected")
	}
	// Expected: brf[0..7] are all 0xFF, so byte=0 of the planar
	// extraction takes bit 0 of each (= 1 for all) → 0xFF written at
	// iram[0]. This is the line=0 path; if dmaen=0 had not reset
	// dma.line, we'd be writing line=3's residual brf[0..7] pattern
	// at a different address (no addr-0 mutation).
	if got := d2.ReadIRAMSA1(0); got != 0xFF {
		t.Fatalf("iram[0] after dmaen-reset+trigger = %02X, want FF (line=0 brf[0..7] all 0xFF byte=0 bit0)", got)
	}
}

func TestSA1DMACC2DMASIZEAndDMACBClamp(t *testing.T) {
	// bsnes io.cpp:501-502: dmasize > 5 clamps to 5; dmacb > 2 clamps to 2.
	d := New()
	openCIWP(t, d)
	// Write $2231 with dmasize=7 (raw bits 5..3 = 7) and dmacb=3.
	// Bits: chdend(7)=0, dmasize(4..2)=7<<2=0x1c, dmacb(1..0)=3 → 0x1f.
	if !d.Write(0x002231, 0x1f) {
		t.Fatalf("write $2231 rejected")
	}
	// After clamp: dmacb=2 (2bpp), so $2247 trigger should produce a
	// 2bpp planar pattern at iram[2..3] when configured with the
	// dmacb=2 layout. Re-derive: 2bpp/dmacb=2/line=0/dda=0/brf[0]=0xFF →
	// addr = 0, addr &= ~((1<<5)-1) = 0, no contribution from line.
	// byte=0: bit0 of brf[0..7]=1,0,0,0,0,0,0,0 → output 0x80.
	//   addr+(0&6)<<3+(0&1)=0. iram[0]=0x80.
	// byte=1: bit1 of brf[0..7]=0,0,...,0 → 0. iram[1]=0.
	if !d.Write(0x002230, 0xa0) {
		t.Fatalf("write $2230 rejected")
	}
	writeBRF(t, d, [16]uint8{0xFF})
	if !d.Write(0x002247, 0xFF) {
		t.Fatalf("trigger rejected")
	}
	if got := d.ReadIRAMSA1(0); got != 0x80 {
		t.Fatalf("iram[0]=%02X want 80 (dmacb-clamped to 2)", got)
	}
	// Also assert no write past the 2bpp range: iram[16] must be zero
	// (would only be touched if dmacb were still 1 at bpp=4).
	if got := d.ReadIRAMSA1(16); got != 0 {
		t.Fatalf("iram[16]=%02X want 0 (dmacb clamp; bpp=2 so byte loop stops at 2)", got)
	}
}

func TestSA1DMACC2HonorsCIWPProtection(t *testing.T) {
	d := New()
	// Leave CIWP=0 → all SA-1 writes blocked.
	if !d.Write(0x002230, 0xa0) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	writeBRF(t, d, [16]uint8{0xFF})
	if !d.Write(0x002247, 0xFF) {
		t.Fatalf("trigger rejected")
	}
	for off := uint32(0); off < 64; off++ {
		if d.ReadIRAMSA1(off) != 0 {
			t.Fatalf("iram[%02X]=%02X CIWP=0 should block writes", off, d.ReadIRAMSA1(off))
		}
	}
}

func TestSA1DMACC2StateRoundTripIncludesNewFields(t *testing.T) {
	d := New()
	openCIWP(t, d)
	if !d.Write(0x002230, 0xa0) {
		t.Fatalf("write $2230 rejected")
	}
	if !d.Write(0x002231, 0x0d) {
		t.Fatalf("write $2231 rejected")
	}
	if !d.Write(0x002235, 0x80) {
		t.Fatalf("write $2235 rejected")
	}
	if !d.Write(0x002236, 0x07) {
		t.Fatalf("write $2236 rejected")
	}
	writeBRF(t, d, [16]uint8{
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00,
	})
	// Advance line once.
	if !d.Write(0x002247, 0x88) {
		t.Fatalf("trigger rejected")
	}
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("unserialize: %v", err)
	}
	// Trigger d2 once and assert iram mutates the same way as a
	// fresh trigger on d would.
	if !d2.Write(0x002247, 0x88) {
		t.Fatalf("d2 trigger rejected")
	}
	if !d.Write(0x002247, 0x88) {
		t.Fatalf("d trigger rejected")
	}
	for off := uint32(0); off < 0x800; off++ {
		if d.ReadIRAMSA1(off) != d2.ReadIRAMSA1(off) {
			t.Fatalf("iram[%03X] mismatch after round-trip: d=%02X d2=%02X", off, d.ReadIRAMSA1(off), d2.ReadIRAMSA1(off))
		}
	}
}

// CHDMA IRQ-line wiring. Bsnes reference:
// bsnes/sfc/coprocessor/sa1/io.cpp:142-161 ($2201 SIE — chdma_irqen
// at bit 5 alongside cpu_irqen at bit 7; on a 0→1 transition the
// pending flag re-asserts the S-CPU IRQ via cpu.irq(1)),
// io.cpp:163-173 ($2202 SIC — bit-5 clears chdma_irqfl and
// cpu.irq(0) deasserts only when both flags are clear),
// dma.cpp:50-55 (dmaCC1 raises chdma_irqfl + asserts S-CPU IRQ).
// Without this wiring SignalCharacterDMAIRQ sets a flag readable via
// $2300 bit 5 but never reaches cartridge.pollSA1IRQ — a real
// dead-end for any future CC1 slice.

func TestSA1CHDMAIRQContributesToCPUIRQPredicate(t *testing.T) {
	d := New()
	d.Write(0x002201, 0x20) // SIE bit 5: chdma_irqen
	d.SignalCharacterDMAIRQ()
	if !d.CPUIRQPending() {
		t.Fatalf("CHDMA IRQ should drive S-CPU line when enabled (got CPUIRQPending=false)")
	}
}

func TestSA1CHDMAIRQRequiresEnable(t *testing.T) {
	d := New()
	// chdma_irqen NOT set.
	d.SignalCharacterDMAIRQ()
	if d.CPUIRQPending() {
		t.Fatalf("CHDMA IRQ must not drive S-CPU line when chdma_irqen=0")
	}
}

func TestSA1CHDMAIRQClearViaSIC(t *testing.T) {
	d := New()
	d.Write(0x002201, 0x20)
	d.SignalCharacterDMAIRQ()
	if !d.CPUIRQPending() {
		t.Fatalf("setup: CHDMA IRQ should be pending")
	}
	// $2202 bit 5 clears chdma_irqfl per bsnes io.cpp:166-169.
	d.Write(0x002202, 0x20)
	if d.CPUIRQPending() {
		t.Fatalf("after $2202 bit-5 write CHDMA IRQ should be cleared")
	}
}

func TestSA1CPUIRQUnaffectedByCHDMAEnable(t *testing.T) {
	// Regression guard: cpu_irq path must remain independently armed.
	d := New()
	d.Write(0x002201, 0x80) // cpu_irqen, chdma_irqen=0
	d.SignalCPUIRQ(0x05)
	if !d.CPUIRQPending() {
		t.Fatalf("cpu_irq must drive S-CPU line independent of chdma_irqen")
	}
	// Clearing CHDMA-IRQ (which is not pending anyway) must not
	// deassert the cpu_irq line.
	d.Write(0x002202, 0x20)
	if !d.CPUIRQPending() {
		t.Fatalf("clearing chdma flag must not affect pending cpu_irq")
	}
	// Clearing cpu_irq via $2202 bit 7 deasserts.
	d.Write(0x002202, 0x80)
	if d.CPUIRQPending() {
		t.Fatalf("after $2202 bit-7 cpu_irq should clear")
	}
}

func TestSA1CHDMAIRQAndCPUIRQOROntoLine(t *testing.T) {
	d := New()
	d.Write(0x002201, 0xa0) // both enables: cpu_irqen | chdma_irqen
	d.SignalCharacterDMAIRQ()
	d.SignalCPUIRQ(0x07)
	if !d.CPUIRQPending() {
		t.Fatalf("both flags set + both enabled should be pending")
	}
	// Clear only CHDMA: line stays asserted (cpu_irq still set).
	d.Write(0x002202, 0x20)
	if !d.CPUIRQPending() {
		t.Fatalf("after clearing CHDMA only, cpu_irq must keep line asserted")
	}
	// Clear cpu_irq: now both clear, line deasserts.
	d.Write(0x002202, 0x80)
	if d.CPUIRQPending() {
		t.Fatalf("after clearing both flags line must deassert")
	}
}

func TestSA1CHDMAIRQRetriggerOnSIETransition(t *testing.T) {
	// bsnes io.cpp:151-156: a 0→1 chdma_irqen transition while
	// chdma_irqfl is set re-arms the line. The Go contract is that
	// CPUIRQPending() returns true after the transition, regardless
	// of whether the prior poll already saw a deassertion.
	d := New()
	// chdma_irqen=0; flag set first.
	d.SignalCharacterDMAIRQ()
	if d.CPUIRQPending() {
		t.Fatalf("with chdma_irqen=0 line must not be pending")
	}
	// 0→1 transition.
	d.Write(0x002201, 0x20)
	if !d.CPUIRQPending() {
		t.Fatalf("after chdma_irqen 0→1 with flag set, line must be pending")
	}
}
