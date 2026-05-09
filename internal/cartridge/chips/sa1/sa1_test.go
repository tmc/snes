package sa1

import "testing"

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
