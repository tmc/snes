package gsu

import "testing"

// TestResetClearsRegisters checks that Reset clears registers, SFR, and
// pixel-cache bookkeeping.
func TestResetClearsRegisters(t *testing.T) {
	d := New(nil, nil)
	for i := range d.R {
		d.R[i] = uint16(i) * 0x1111
	}
	d.SFR = 0xFFFF
	d.CBR = 0x5555
	d.PBR = 0xAA
	d.SCMR = 0x3f
	d.BRAMR = 1
	d.VCR = 0xff
	d.CFGR = 0xa0
	d.CLSR = 1
	d.cycles = 42
	d.cacheHasRow = true
	d.validMask = 0xFF
	d.commits = 7
	d.Reset()
	for i, v := range d.R {
		if v != 0 {
			t.Errorf("R%d=%04X, want 0", i, v)
		}
	}
	if d.SFR != 0 {
		t.Errorf("SFR=%04X, want 0", d.SFR)
	}
	if d.CBR != 0 || d.PBR != 0 || d.SCMR != 0 || d.BRAMR != 0 || d.CFGR != 0 || d.CLSR != 0 {
		t.Errorf("control reset mismatch: CBR=%04X PBR=%02X SCMR=%02X BRAMR=%02X CFGR=%02X CLSR=%02X", d.CBR, d.PBR, d.SCMR, d.BRAMR, d.CFGR, d.CLSR)
	}
	if d.VCR != 0x04 {
		t.Errorf("VCR=%02X, want 04", d.VCR)
	}
	if d.Cycles() != 0 {
		t.Errorf("cycles=%d, want 0", d.Cycles())
	}
	if d.cacheHasRow || d.validMask != 0 {
		t.Errorf("pixel cache not cleared: hasRow=%v mask=%02X", d.cacheHasRow, d.validMask)
	}
	if d.commits != 0 {
		t.Errorf("commit counter not reset: %d", d.commits)
	}
}

func TestStopRaisesIRQUnlessMasked(t *testing.T) {
	t.Run("irq enabled", func(t *testing.T) {
		d := New([]byte{0x00}, nil)
		d.Go()
		d.Run(1)

		if d.SFR&SFRG != 0 {
			t.Fatalf("STOP left G set: SFR=%04X", d.SFR)
		}
		if d.SFR&SFRIRQ == 0 {
			t.Fatalf("STOP did not raise IRQ: SFR=%04X", d.SFR)
		}
		if hi, ok := d.Read(0x3031); !ok || hi&0x80 == 0 {
			t.Fatalf("SFR high read=%02X ok=%v, want IRQ bit", hi, ok)
		}
		if d.SFR&SFRIRQ != 0 {
			t.Fatalf("SFR high read did not clear IRQ: SFR=%04X", d.SFR)
		}
	})

	t.Run("irq masked", func(t *testing.T) {
		d := New([]byte{0x00}, nil)
		d.CFGR = 0x80
		d.Go()
		d.Run(1)

		if d.SFR&SFRIRQ == 0 {
			t.Fatalf("masked STOP did not set SFR.IRQ: SFR=%04X", d.SFR)
		}
	})
}

func TestStopPrefetchesNextOpcodeByte(t *testing.T) {
	d := New([]byte{0x00, 0x01}, nil)
	d.Go()
	d.Run(1)

	if got := d.R[15]; got != 2 {
		t.Fatalf("PC after STOP=%04X, want 0002", got)
	}
	if d.Running() {
		t.Fatalf("STOP left GSU running")
	}
}

// TestGoStopGates verifies that Run refuses to step when SFR.G is clear.
func TestGoStopGates(t *testing.T) {
	d := New([]byte{0x01, 0x01, 0x01}, nil)
	if d.Running() {
		t.Fatal("expected GSU not running after New")
	}
	if n := d.Run(10); n != 0 {
		t.Fatalf("Run while stopped executed %d ops", n)
	}
	d.Go()
	if !d.Running() {
		t.Fatal("expected running after Go()")
	}
	// NOP is opcode 0x01 — three of them then fall off the end of ROM
	// (reads zero, which is STOP).
	if n := d.Run(10); n == 0 {
		t.Fatalf("expected at least one executed op, got 0")
	}
	if d.Running() {
		t.Fatal("STOP did not clear SFR.G")
	}
}

// TestSFRFlagHelpers exercises the flag setters directly.
func TestSFRFlagHelpers(t *testing.T) {
	d := New(nil, nil)
	d.setZN(0)
	if d.SFR&SFRZ == 0 {
		t.Error("Z not set for zero")
	}
	if d.SFR&SFRS != 0 {
		t.Error("S set for zero")
	}
	d.setZN(0x8000)
	if d.SFR&SFRZ != 0 {
		t.Error("Z set for negative")
	}
	if d.SFR&SFRS == 0 {
		t.Error("S not set for negative")
	}
	d.setCarry(true)
	if d.SFR&SFRCY == 0 {
		t.Error("CY not set")
	}
	d.setCarry(false)
	if d.SFR&SFRCY != 0 {
		t.Error("CY not cleared")
	}
}

func TestCacheOpcodeInvalidatesAndAlignsCBR(t *testing.T) {
	d := New([]byte{0x02, 0x00}, nil)
	d.CBR = 0x1230
	d.cacheValid[0] = true
	d.Go()
	d.Run(1)

	if d.CBR != 0 {
		t.Fatalf("CACHE CBR=%04X want 0000", d.CBR)
	}
	if d.cacheValid[0] {
		t.Fatalf("CACHE did not invalidate cache line")
	}
}

func TestCacheOpcodeInvalidatesWhenCBRUnchanged(t *testing.T) {
	d := New(nil, nil)
	d.Cache[0] = 0x02
	d.CBR = 0
	d.cacheValid[0] = true
	d.Go()
	d.Run(1)

	if d.cacheValid[0] {
		t.Fatalf("CACHE with unchanged CBR did not invalidate cache line")
	}
}

func TestControlRegisterWindowAndCacheInvalidation(t *testing.T) {
	d := New(nil, nil)
	d.cacheValid[0] = true
	d.cacheValid[1] = true

	d.Write(0x3033, 0xff)
	d.Write(0x3037, 0xff)
	d.Write(0x3039, 0xff)
	if got, _ := d.Read(0x3033); got != 0x01 {
		t.Fatalf("BRAMR=%02X want 01", got)
	}
	if got, _ := d.Read(0x3037); got != 0xa0 {
		t.Fatalf("CFGR=%02X want A0", got)
	}
	if got, _ := d.Read(0x3039); got != 0x01 {
		t.Fatalf("CLSR=%02X want 01", got)
	}
	if got, _ := d.Read(0x303b); got != 0x04 {
		t.Fatalf("VCR=%02X want 04", got)
	}

	d.Write(0x3034, 0x02)
	if d.cacheValid[0] || d.cacheValid[1] {
		t.Fatalf("PBR write did not flush cache: %v %v", d.cacheValid[0], d.cacheValid[1])
	}

	d.cacheValid[0] = true
	d.Cache[0] = 0x5a
	d.Write(0x303e, 0x10)
	if d.cacheValid[0] {
		t.Fatalf("CBR low write did not flush cache")
	}
	if got := d.Cache[0]; got != 0 {
		t.Fatalf("CBR low write left stale cache byte=%02X, want 00", got)
	}

	d.cacheValid[0] = true
	d.CBR = 0x1230
	d.SFR |= SFRG
	d.Write(0x3030, 0x00)
	if d.CBR != 0 || d.cacheValid[0] {
		t.Fatalf("CPU clear G CBR=%04X cacheValid=%v, want reset+flush", d.CBR, d.cacheValid[0])
	}
}

func TestOpcodeFetchUsesCacheUntilInvalidated(t *testing.T) {
	rom := []byte{0x01, 0x01, 0x00}
	d := New(rom, nil)
	d.Go()
	d.Run(1) // fetches line 0 into cache and executes NOP

	rom[1] = 0x00
	d.Run(1)
	if !d.Running() {
		t.Fatalf("cached opcode fetch observed ROM mutation before invalidation")
	}

	d.flushCache()
	d.R[15] = 1
	d.Run(1)
	if d.Running() {
		t.Fatalf("opcode fetch did not observe ROM mutation after invalidation")
	}
}

func TestCacheWindowReadWriteUsesCBRRelativeAddress(t *testing.T) {
	d := New(nil, nil)
	d.CBR = 0x0010
	if !d.Write(0x310F, 0xAB) {
		t.Fatalf("cache write rejected")
	}
	if !d.cacheValid[1] {
		t.Fatalf("cache line 1 not marked valid after final byte write")
	}
	got, ok := d.Read(0x310F)
	if !ok {
		t.Fatalf("cache read rejected")
	}
	if got != 0xAB {
		t.Fatalf("cache read=%02X want AB", got)
	}
}

func TestOpcodeFetchUsesGSULoROMBanking(t *testing.T) {
	rom := make([]byte, 0x10000)
	rom[0x0000] = 0x00 // STOP at LoROM-mapped 00:8000
	rom[0x8000] = 0x01 // raw-linear 00:8000 would be NOP
	d := New(rom, nil)
	d.SetPC(0x8000)
	d.Go()
	d.Run(1)

	if d.Running() {
		t.Fatalf("opcode fetch used raw 00:8000 offset instead of LoROM bank mapping")
	}
}

func TestCLSRControlsOpcodeWaitCycles(t *testing.T) {
	for _, tt := range []struct {
		name  string
		clsr  uint8
		miss  uint64
		cache uint64
	}{
		{"slow", 0, 96, 2},
		{"fast", 1, 80, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := New([]byte{0x01, 0x01, 0x00}, nil)
			d.CLSR = tt.clsr
			d.Go()

			d.Run(1)
			if got := d.Cycles(); got != tt.miss {
				t.Fatalf("after cache miss cycles=%d, want %d", got, tt.miss)
			}
			d.Run(1)
			if got, want := d.Cycles(), tt.miss+tt.cache; got != want {
				t.Fatalf("after cache hit cycles=%d, want %d", got, want)
			}
		})
	}
}

func TestStepUsesWaitCycleBudget(t *testing.T) {
	d := New([]byte{0x01, 0x01, 0x01, 0x00}, nil)
	d.Go()

	d.Step(95)
	if got := d.R[15]; got != 0 {
		t.Fatalf("PC after first Step=%d, want 0", got)
	}
	if got := d.Cycles(); got != 0 {
		t.Fatalf("cycles after first Step=%d, want 0", got)
	}

	d.Step(1)
	if got := d.R[15]; got != 1 {
		t.Fatalf("PC after second Step=%d, want 1", got)
	}
	if got := d.Cycles(); got != 96 {
		t.Fatalf("cycles after second Step=%d, want 96", got)
	}

	d.Step(1)
	if got := d.R[15]; got != 1 {
		t.Fatalf("PC after third Step=%d, want 1", got)
	}
	d.Step(1)
	if got := d.R[15]; got != 2 {
		t.Fatalf("PC after fourth Step=%d, want 2", got)
	}
	if got := d.Cycles(); got != 98 {
		t.Fatalf("cycles after second Step=%d, want 98", got)
	}
}

func TestStepDebtSerializes(t *testing.T) {
	d := New([]byte{0x01, 0x00}, nil)
	d.Go()
	d.Step(95)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New([]byte{0x01, 0x00}, nil)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	restored.Step(1)

	if got := restored.R[15]; got != 1 {
		t.Fatalf("restored PC after accumulated Step=%d, want 1", got)
	}
}

func TestFragmentedStepMatchesCoarseStep(t *testing.T) {
	rom := []byte{0x01, 0x01, 0x01, 0x00, 0x01}
	coarse := New(append([]byte(nil), rom...), nil)
	fragmented := New(append([]byte(nil), rom...), nil)
	coarse.Go()
	fragmented.Go()

	coarse.Step(100)
	for i := 0; i < 100; i++ {
		fragmented.Step(1)
	}

	if fragmented.R[15] != coarse.R[15] {
		t.Fatalf("fragmented PC=%d, coarse PC=%d", fragmented.R[15], coarse.R[15])
	}
	if fragmented.Cycles() != coarse.Cycles() {
		t.Fatalf("fragmented cycles=%d, coarse cycles=%d", fragmented.Cycles(), coarse.Cycles())
	}
	if fragmented.Running() != coarse.Running() {
		t.Fatalf("fragmented running=%v, coarse running=%v", fragmented.Running(), coarse.Running())
	}
}

func TestBusDataWaitCycles(t *testing.T) {
	for _, tt := range []struct {
		name string
		clsr uint8
		wait uint64
	}{
		{"slow", 0, 6},
		{"fast", 1, 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := New([]byte{0x7c}, []byte{0x12})
			d.CLSR = tt.clsr

			if got := d.romRead(); got != 0x7c {
				t.Fatalf("romRead=%02X, want 7c", got)
			}
			if got := d.ramRead(0); got != 0x12 {
				t.Fatalf("ramRead=%02X, want 12", got)
			}
			d.ramWrite(0, 0x34)
			if got := d.RAM[0]; got != 0x34 {
				t.Fatalf("RAM[0]=%02X, want 34", got)
			}
			if got, want := d.Cycles(), 3*tt.wait; got != want {
				t.Fatalf("cycles=%d, want %d", got, want)
			}
		})
	}
}

func TestSBKStoresSourceThroughRAMBuffer(t *testing.T) {
	d := New([]byte{0x90, 0x00}, nil) // SBK; STOP
	d.R[0] = 0x1234
	d.RAMAddr = 0x0010
	d.Go()

	d.Run(1)
	if got := d.RAM[0x10]; got != 0x34 {
		t.Fatalf("SBK low byte=%02X, want 34", got)
	}
	if got := d.RAM[0x11]; got != 0x00 {
		t.Fatalf("SBK high byte committed early=%02X, want 00", got)
	}
	d.advanceCycles(6)
	if got := d.RAM[0x11]; got != 0x12 {
		t.Fatalf("SBK delayed high byte=%02X, want 12", got)
	}
}

func TestRAMBufferSerializesPendingWrite(t *testing.T) {
	d := New(nil, nil)
	d.RAMBR = 1
	d.writeRAMBuffer(0x0010, 0x5a)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(nil, nil)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	restored.advanceCycles(6)
	if got := restored.RAM[0x0010]; got != 0x5a {
		t.Fatalf("restored delayed RAM write=%02X, want 5a", got)
	}
}

func TestRAMBufferCapturesBank(t *testing.T) {
	ram := make([]byte, 128*1024)
	d := New([]byte{0x3d, 0x34, 0x00}, ram) // ALT1; STB (R4); STOP
	d.RAMBR = 1
	d.R[0] = 0x00a5
	d.R[4] = 0x0020
	d.Go()
	d.Run(2)

	d.RAMBR = 0
	d.advanceCycles(6)
	if got := ram[0x10020]; got != 0xa5 {
		t.Fatalf("banked delayed RAM write=%02X, want a5", got)
	}
	if got := ram[0x0020]; got != 0x00 {
		t.Fatalf("delayed RAM write used current bank: RAM[0020]=%02X want 00", got)
	}
}

func TestROMBufferLoadsAfterR14CPUWrite(t *testing.T) {
	d := New([]byte{0x11, 0x22, 0x33, 0x44}, nil)
	if !d.Write(0x301c, 0x03) {
		t.Fatalf("R14 low write rejected")
	}
	if d.SFR&SFRR == 0 {
		t.Fatalf("R14 write did not set SFR.R")
	}
	if got := d.romData; got != 0 {
		t.Fatalf("romData before wait=%02X, want 00", got)
	}

	d.advanceCycles(5)
	if d.SFR&SFRR == 0 {
		t.Fatalf("SFR.R cleared before wait elapsed")
	}
	d.advanceCycles(1)
	if d.SFR&SFRR != 0 {
		t.Fatalf("SFR.R still set after wait")
	}
	if got := d.romData; got != 0x44 {
		t.Fatalf("romData=%02X, want 44", got)
	}
}

func TestROMBufferSerializesPendingLoad(t *testing.T) {
	d := New([]byte{0x11, 0x22, 0x33, 0x44}, nil)
	d.R[14] = 3
	d.updateROMBuffer()

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New([]byte{0x11, 0x22, 0x33, 0x44}, nil)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if got := restored.romRead(); got != 0x44 {
		t.Fatalf("restored romRead=%02X, want 44", got)
	}
	if restored.SFR&SFRR != 0 {
		t.Fatalf("restored romRead left SFR.R set")
	}
}

func TestR14InstructionWritesUpdateROMBuffer(t *testing.T) {
	t.Run("ibt", func(t *testing.T) {
		d := New([]byte{0xae, 0x03, 0x00, 0x44}, nil) // IBT R14,#3
		d.Go()
		d.Run(1)

		if d.SFR&SFRR == 0 {
			t.Fatalf("IBT R14 did not set SFR.R")
		}
		if got := d.romRead(); got != 0x44 {
			t.Fatalf("IBT R14 buffered romRead=%02X, want 44", got)
		}
	})

	t.Run("to add", func(t *testing.T) {
		d := New([]byte{0x1e, 0x3e, 0x50, 0x00, 0x55}, nil) // TO R14; ALT2; ADDI #0
		d.R[0] = 4
		d.Go()
		d.Run(3)

		if d.R[14] != 4 {
			t.Fatalf("R14=%04X, want 0004", d.R[14])
		}
		if d.SFR&SFRR == 0 {
			t.Fatalf("TO R14 ADD did not set SFR.R")
		}
		if got := d.romRead(); got != 0x55 {
			t.Fatalf("TO R14 buffered romRead=%02X, want 55", got)
		}
	})
}

func TestCyclesSerialize(t *testing.T) {
	d := New(nil, nil)
	d.cycles = 123

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	e := New(nil, nil)
	if err := e.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if got := e.Cycles(); got != 123 {
		t.Fatalf("cycles=%d, want 123", got)
	}
}
