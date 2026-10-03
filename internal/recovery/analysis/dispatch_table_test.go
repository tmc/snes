package analysis

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestRecoverDispatchTable(t *testing.T) {
	// Base synthetic ROM with 4 target routines at $8060, $8070, $8080, $8090.
	// Each target routine has LDA #$xx (A9 xx), RTS (60).
	makeValidROM := func() []byte {
		rom := make([]byte, 32*1024)
		// Table at $8050 (offset 0x50): 4 16-bit targets
		targets := []uint16{0x8060, 0x8070, 0x8080, 0x8090}
		for i, tgt := range targets {
			binary.LittleEndian.PutUint16(rom[0x50+i*2:], tgt)
		}
		// Routine 0 at $8060: LDA #$10, RTS
		copy(rom[0x60:], []byte{0xA9, 0x10, 0x60})
		// Routine 1 at $8070: LDA #$20, RTS
		copy(rom[0x70:], []byte{0xA9, 0x20, 0x60})
		// Routine 2 at $8080: LDA #$30, RTS
		copy(rom[0x80:], []byte{0xA9, 0x30, 0x60})
		// Routine 3 at $8090: LDA #$40, RTS
		copy(rom[0x90:], []byte{0xA9, 0x40, 0x60})
		return rom
	}

	validJmp := recovery.Instruction{
		Address: 0x00800A,
		Offset:  0x0A,
		Opcode:  0x7C,
		Bytes:   "7c5080", // JMP ($8050,X)
	}

	tests := []struct {
		name        string
		rom         []byte
		jmp         recovery.Instruction
		preceding   []recovery.Instruction
		wantN       int
		wantTargets []uint32
		wantErr     bool
	}{
		{
			name: "ASL before CMP #$08",
			rom:  makeValidROM(),
			jmp:  validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xA9, Bytes: "a901"}, // LDA #$01
				{Opcode: 0x0A, Bytes: "0a"},   // ASL A
				{Opcode: 0xC9, Bytes: "c908"}, // CMP #$08
				{Opcode: 0xB0, Bytes: "b002"}, // BCS +2
				{Opcode: 0xAA, Bytes: "aa"},   // TAX
			},
			wantN:       4,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantErr:     false,
		},
		{
			name: "CMP #$04 before ASL",
			rom:  makeValidROM(),
			jmp:  validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xA9, Bytes: "a901"}, // LDA #$01
				{Opcode: 0xC9, Bytes: "c904"}, // CMP #$04
				{Opcode: 0xB0, Bytes: "b003"}, // BCS +3
				{Opcode: 0x0A, Bytes: "0a"},   // ASL A
				{Opcode: 0xAA, Bytes: "aa"},   // TAX
			},
			wantN:       4,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantErr:     false,
		},
		{
			name: "CPX #$08 without ASL",
			rom:  makeValidROM(),
			jmp:  validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xA2, Bytes: "a202"}, // LDX #$02
				{Opcode: 0xE0, Bytes: "e008"}, // CPX #$08
				{Opcode: 0xB0, Bytes: "b001"}, // BCS +1
			},
			wantN:       4,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantErr:     false,
		},
		{
			name: "CMP #$08 without ASL (even immediate)",
			rom:  makeValidROM(),
			jmp:  validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"}, // CMP #$08
				{Opcode: 0xB0, Bytes: "b002"}, // BCS +2
				{Opcode: 0xAA, Bytes: "aa"},   // TAX
			},
			wantN:       4,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantErr:     false,
		},
		{
			name:      "refusal: unbounded table (no comparison)",
			rom:       makeValidROM(),
			jmp:       validJmp,
			preceding: []recovery.Instruction{{Opcode: 0xAA, Bytes: "aa"}},
			wantErr:   true,
		},
		{
			name:      "refusal: empty preceding instructions",
			rom:       makeValidROM(),
			jmp:       validJmp,
			preceding: nil,
			wantErr:   true,
		},
		{
			name: "refusal: zero comparison bound",
			rom:  makeValidROM(),
			jmp:  validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c900"}, // CMP #$00
			},
			wantErr: true,
		},
		{
			name: "refusal: table base below $8000 (RAM/MMIO)",
			rom:  makeValidROM(),
			jmp: recovery.Instruction{
				Address: 0x00800A,
				Offset:  0x0A,
				Opcode:  0x7C,
				Bytes:   "7c0010", // JMP ($1000,X)
			},
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
			},
			wantErr: true,
		},
		{
			name: "refusal: target points below $8000",
			rom: func() []byte {
				r := makeValidROM()
				// Corrupt target 1 to point to $0100 (RAM)
				binary.LittleEndian.PutUint16(r[0x50+2:], 0x0100)
				return r
			}(),
			jmp: validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
			},
			wantErr: true,
		},
		{
			name: "refusal: target points to unmapped ROM offset",
			rom: func() []byte {
				// 32KB ROM: offset extends from $8000 to $FFFF in bank 0.
				// Bank 1 is not mapped in a 32KB ROM.
				// Setting target to point to $8000 in bank 0 is offset 0.
				// Setting target to 0x8050 with table pointing outside 32KB:
				r := makeValidROM()
				// Target pointing to $FFFF (valid address, but offset 0x7FFF is last byte, no opcode room)
				binary.LittleEndian.PutUint16(r[0x50+2:], 0xFFFF)
				return r
			}(),
			jmp: validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
			},
			wantErr: true,
		},
		{
			name: "refusal: target begins with BRK (0x00)",
			rom: func() []byte {
				r := makeValidROM()
				// Corrupt target routine 0 at 0x60 with 0x00 (BRK)
				r[0x60] = 0x00
				return r
			}(),
			jmp: validJmp,
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
			},
			wantErr: true,
		},
		{
			name: "refusal: not JMP ($abs,X) instruction",
			rom:  makeValidROM(),
			jmp: recovery.Instruction{
				Address: 0x00800A,
				Offset:  0x0A,
				Opcode:  0x4C, // JMP $abs
				Bytes:   "4c5080",
			},
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
			},
			wantErr: true,
		},
		{
			name: "refusal: table extends past end of ROM",
			rom:  makeValidROM(),
			jmp: recovery.Instruction{
				Address: 0x00800A,
				Offset:  0x0A,
				Opcode:  0x7C,
				Bytes:   "7cfcff", // JMP ($FFFC,X) table at end of bank
			},
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"}, // 4 entries = 8 bytes; $FFFC + 8 = past 32KB
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table, err := RecoverDispatchTable(tc.rom, tc.jmp, tc.preceding)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RecoverDispatchTable() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if table.EntryCount != tc.wantN {
				t.Errorf("EntryCount = %d, want %d", table.EntryCount, tc.wantN)
			}
			if len(table.Targets) != len(tc.wantTargets) {
				t.Fatalf("len(Targets) = %d, want %d", len(table.Targets), len(tc.wantTargets))
			}
			for i, tgt := range table.Targets {
				if tgt != tc.wantTargets[i] {
					t.Errorf("Targets[%d] = 0x%06X, want 0x%06X", i, tgt, tc.wantTargets[i])
				}
			}
		})
	}
}

func TestAnalyzeLoROM_DispatchTable(t *testing.T) {
	tests := []struct {
		name             string
		setupROM         func() []byte
		wantEdges        int
		wantTargets      []uint32
		wantUnresolved   bool
		checkAnalyzedPC  []uint32
	}{
		{
			name: "recover 4-target dispatch table and continue analysis into handlers",
			setupROM: func() []byte {
				rom := make([]byte, 32*1024)
				// Reset vector at $7FFC points to $8000
				binary.LittleEndian.PutUint16(rom[0x7FC0+0x3C:], 0x8000)

				// Code at $8000 (offset 0):
				// CLC (18)
				// XCE (FB)
				// LDA #$01 (A9 01)
				// ASL A (0A)
				// CMP #$08 (C9 08)
				// BCS $8012 (B0 09) -> branch to STP at $8012
				// TAX (AA)
				// JMP ($8030,X) (7C 30 80)
				// $800D - $8011: NOP padding
				// $8012: STP (DB)
				code := []byte{
					0x18,       // 8000: CLC
					0xFB,       // 8001: XCE
					0xA9, 0x01, // 8002: LDA #$01
					0x0A,       // 8004: ASL A
					0xC9, 0x08, // 8005: CMP #$08
					0xB0, 0x09, // 8007: BCS $8012
					0xAA,       // 8009: TAX
					0x7C, 0x30, 0x80, // 800A: JMP ($8030,X)
					0xEA, 0xEA, 0xEA, 0xEA, 0xEA, // 800D-8011: NOPs
					0xDB, // 8012: STP
				}
				copy(rom[0:], code)

				// Dispatch table at $8030 (offset 0x30): 4 targets
				targets := []uint16{0x8060, 0x8070, 0x8080, 0x8090}
				for i, tgt := range targets {
					binary.LittleEndian.PutUint16(rom[0x30+i*2:], tgt)
				}

				// Target routine 0 at $8060 (offset 0x60): LDA #$10 (A9 10), RTS (60)
				copy(rom[0x60:], []byte{0xA9, 0x10, 0x60})
				// Target routine 1 at $8070 (offset 0x70): LDA #$20 (A9 20), RTS (60)
				copy(rom[0x70:], []byte{0xA9, 0x20, 0x60})
				// Target routine 2 at $8080 (offset 0x80): LDA #$30 (A9 30), RTS (60)
				copy(rom[0x80:], []byte{0xA9, 0x30, 0x60})
				// Target routine 3 at $8090 (offset 0x90): LDA #$40 (A9 40), RTS (60)
				copy(rom[0x90:], []byte{0xA9, 0x40, 0x60})

				return rom
			},
			wantEdges:      4,
			wantTargets:    []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantUnresolved: false,
			checkAnalyzedPC: []uint32{
				0x008060, 0x008062, // Routine 0: LDA, RTS
				0x008070, 0x008072, // Routine 1: LDA, RTS
				0x008080, 0x008082, // Routine 2: LDA, RTS
				0x008090, 0x008092, // Routine 3: LDA, RTS
			},
		},
		{
			name: "refusal on unbounded table halts and records issue",
			setupROM: func() []byte {
				rom := make([]byte, 32*1024)
				binary.LittleEndian.PutUint16(rom[0x7FC0+0x3C:], 0x8000)

				// Code at $8000:
				// CLC (18)
				// XCE (FB)
				// LDA #$01 (A9 01)
				// TAX (AA)
				// JMP ($8030,X) (7C 30 80) -- no comparison!
				code := []byte{
					0x18,       // 8000: CLC
					0xFB,       // 8001: XCE
					0xA9, 0x01, // 8002: LDA #$01
					0xAA,       // 8004: TAX
					0x7C, 0x30, 0x80, // 8005: JMP ($8030,X)
				}
				copy(rom[0:], code)

				// Table at $8030:
				binary.LittleEndian.PutUint16(rom[0x30:], 0x8060)
				copy(rom[0x60:], []byte{0xA9, 0x10, 0x60})

				return rom
			},
			wantEdges:       0,
			wantTargets:     nil,
			wantUnresolved:  true,
			checkAnalyzedPC: nil,
		},
		{
			name: "refusal on out-of-ROM table halts and records issue",
			setupROM: func() []byte {
				rom := make([]byte, 32*1024)
				binary.LittleEndian.PutUint16(rom[0x7FC0+0x3C:], 0x8000)

				// Code at $8000 with comparison, but table at $1000 (< $8000, RAM):
				code := []byte{
					0x18,       // 8000: CLC
					0xFB,       // 8001: XCE
					0xC9, 0x08, // 8002: CMP #$08
					0xB0, 0x04, // 8004: BCS $800A
					0xAA,       // 8006: TAX
					0x7C, 0x00, 0x10, // 8007: JMP ($1000,X)
					0xDB, // 800A: STP
				}
				copy(rom[0:], code)
				return rom
			},
			wantEdges:       0,
			wantTargets:     nil,
			wantUnresolved:  true,
			checkAnalyzedPC: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rom := tc.setupROM()
			doc := &recovery.Document{}
			res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 500})
			if err != nil {
				t.Fatalf("AnalyzeLoROM failed: %v", err)
			}

			// Check dispatch edges.
			var dispatchEdges []recovery.Edge
			for _, e := range res.Edges {
				if e.Kind == "dispatch" {
					dispatchEdges = append(dispatchEdges, e)
				}
			}

			if len(dispatchEdges) != tc.wantEdges {
				t.Errorf("got %d dispatch edges, want %d", len(dispatchEdges), tc.wantEdges)
			}

			// Verify dispatch edge destinations match expected targets.
			for i, wantTgt := range tc.wantTargets {
				if i < len(dispatchEdges) && dispatchEdges[i].Destination != wantTgt {
					t.Errorf("dispatch edge %d destination = 0x%06X, want 0x%06X", i, dispatchEdges[i].Destination, wantTgt)
				}
			}

			// Check issue presence.
			hasUnresolvedIssue := false
			for _, iss := range res.Issues {
				if iss.Reason == "indirect jump destination unresolved" {
					hasUnresolvedIssue = true
					break
				}
			}
			if hasUnresolvedIssue != tc.wantUnresolved {
				t.Errorf("hasUnresolvedIssue = %v, want %v", hasUnresolvedIssue, tc.wantUnresolved)
			}

			// Check that target routines were actually analyzed.
			analyzedAddrs := make(map[uint32]bool)
			for _, inst := range res.Instructions {
				analyzedAddrs[inst.Address] = true
			}
			for _, expectedPC := range tc.checkAnalyzedPC {
				if !analyzedAddrs[expectedPC] {
					t.Errorf("expected instruction at 0x%06X to be analyzed, but was not", expectedPC)
				}
			}
		})
	}
}
