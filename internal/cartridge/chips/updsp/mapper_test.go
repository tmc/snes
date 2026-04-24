package updsp

import "testing"

// TestMapperLoROMWindow pins the DSP-1 LoROM decode: banks 20-3F
// offsets 6000-7FFF are DR/SR; even address = DR, odd address = SR.
// Banks outside the range do not claim the access.
func TestMapperLoROMWindow(t *testing.T) {
	io := NewIO(NewCore())
	m := NewMapper(io, MapLoROM)

	cases := []struct {
		name    string
		addr    uint32
		drPort  bool
		claimed bool
	}{
		{"bank 20 DR", 0x20_6000, true, true},
		{"bank 20 SR", 0x20_6001, false, true},
		{"bank 3F SR", 0x3F_7FFF, false, true},
		{"mirror bank A0 DR", 0xA0_6002, true, true},
		{"bank 3F below window", 0x3F_5FFF, false, false},
		{"bank 1F outside mirror", 0x1F_6000, false, false},
		{"bank 40 outside mirror", 0x40_6000, false, false},
		{"above 7FFF", 0x20_8000, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dr, ok := m.mapped(tc.addr)
			if ok != tc.claimed {
				t.Fatalf("mapped(%06X) claimed = %v, want %v", tc.addr, ok, tc.claimed)
			}
			if ok && dr != tc.drPort {
				t.Fatalf("mapped(%06X) drPort = %v, want %v", tc.addr, dr, tc.drPort)
			}
		})
	}
}

// TestMapperHiROMWindow pins the DSP-1 HiROM decode: banks 00-1F
// offsets 6000-6BFF.
func TestMapperHiROMWindow(t *testing.T) {
	io := NewIO(NewCore())
	m := NewMapper(io, MapHiROM)

	cases := []struct {
		addr    uint32
		drPort  bool
		claimed bool
	}{
		{0x00_6000, true, true},
		{0x00_6001, false, true},
		{0x1F_6BFF, false, true},
		{0x80_6000, true, true},
		{0x00_6C00, false, false}, // above window
		{0x00_5FFF, false, false}, // below window
		{0x20_6000, false, false}, // outside banks
	}

	for _, tc := range cases {
		dr, ok := m.mapped(tc.addr)
		if ok != tc.claimed {
			t.Fatalf("mapped(%06X) claimed = %v, want %v", tc.addr, ok, tc.claimed)
		}
		if ok && dr != tc.drPort {
			t.Fatalf("mapped(%06X) drPort = %v, want %v", tc.addr, dr, tc.drPort)
		}
	}
}

// TestMapperReadWriteRoutesThroughIO pins that a Read/Write at the DR
// port drives IO.ReadDR / IO.WriteDR, and an SR read drives IO.ReadSR.
// Exercises the 16-bit half-read handshake end-to-end through the
// address decode.
func TestMapperReadWriteRoutesThroughIO(t *testing.T) {
	core := NewCore()
	io := NewIO(core)
	m := NewMapper(io, MapLoROM)

	// DSP program would normally set DR; we simulate.
	io.SetDSPResult(0xBEEF)

	// SR read should carry RQM (bit 15 of SR).
	sr, ok := m.Read(0x20_6001)
	if !ok {
		t.Fatalf("SR read at $20:6001 did not claim the decode")
	}
	if sr&0x80 == 0 {
		t.Fatalf("SR high byte missing RQM after SetDSPResult: got %02X", sr)
	}

	// First DR read returns low byte (0xEF).
	lo, ok := m.Read(0x20_6000)
	if !ok || lo != 0xEF {
		t.Fatalf("DR low read = %02X ok=%v, want EF true", lo, ok)
	}
	// Second DR read returns high byte (0xBE).
	hi, ok := m.Read(0x20_6000)
	if !ok || hi != 0xBE {
		t.Fatalf("DR high read = %02X ok=%v, want BE true", hi, ok)
	}

	// Two DR writes complete a 16-bit transfer: low, then high.
	m.Write(0x20_6000, 0x34)
	m.Write(0x20_6000, 0x12)
	if core.DR != 0x1234 {
		t.Fatalf("DR after write pair = %04X, want 1234", core.DR)
	}

	// SR writes are silently dropped.
	m.Write(0x20_6001, 0xFF)
	if core.SR&0xFF00 == 0xFF00 {
		t.Fatalf("SR write mutated SR high byte: %04X", core.SR)
	}
}

// TestMapperPassthroughWithoutIO pins that a Mapper built with nil IO
// claims the address range but returns 0 on reads and drops writes,
// leaving a cartridge whose DSP ROM is missing in a deterministic idle
// state rather than falling through to ROM reads.
func TestMapperPassthroughWithoutIO(t *testing.T) {
	m := NewMapper(nil, MapLoROM)
	if v, ok := m.Read(0x20_6000); !ok || v != 0 {
		t.Fatalf("nil-IO DR read = %02X ok=%v, want 0 true", v, ok)
	}
	if ok := m.Write(0x20_6000, 0x55); !ok {
		t.Fatalf("nil-IO DR write did not claim the decode")
	}
}
