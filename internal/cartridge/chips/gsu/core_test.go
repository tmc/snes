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
	if d.CBR != 0 || d.PBR != 0 {
		t.Errorf("CBR=%04X PBR=%02X, want both 0", d.CBR, d.PBR)
	}
	if d.cacheHasRow || d.validMask != 0 {
		t.Errorf("pixel cache not cleared: hasRow=%v mask=%02X", d.cacheHasRow, d.validMask)
	}
	if d.commits != 0 {
		t.Errorf("commit counter not reset: %d", d.commits)
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
