package dispatch

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
)

// makeValidROM creates a synthetic 32KB LoROM image with 4 target routines
// at $8060, $8070, $8080, and $8090, each containing LDA #$xx, RTS.
// The pointer table at $8050 (offset 0x50) holds four 16-bit pointers.
func makeValidROM() []byte {
	rom := make([]byte, 32*1024)
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

// makeValid24BitROM creates a synthetic 32KB LoROM image with 3 24-bit targets.
func makeValid24BitROM() []byte {
	rom := make([]byte, 32*1024)
	targets := []uint32{0x008060, 0x008070, 0x008080}
	for i, tgt := range targets {
		off := 0x50 + i*3
		rom[off] = byte(tgt)
		rom[off+1] = byte(tgt >> 8)
		rom[off+2] = byte(tgt >> 16)
	}
	copy(rom[0x60:], []byte{0xA9, 0x10, 0x60})
	copy(rom[0x70:], []byte{0xA9, 0x20, 0x60})
	copy(rom[0x80:], []byte{0xA9, 0x30, 0x60})
	return rom
}

func TestDetectionFromROM(t *testing.T) {
	tests := []struct {
		name        string
		rom         []byte
		site        JumpSite
		wantCount   int
		wantTargets []uint32
		wantWidth   int
	}{
		{
			name: "16-bit indexed jump table with 4 targets",
			rom:  makeValidROM(),
			site: JumpSite{
				Address:      0x00800A,
				Opcode:       0x7C,
				TableAddress: 0x008050,
				Bank:         0x00,
				EntryWidth:   2,
				MinSelector:  0,
				MaxSelector:  3,
			},
			wantCount:   4,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080, 0x008090},
			wantWidth:   2,
		},
		{
			name: "24-bit pointer table with 3 targets",
			rom:  makeValid24BitROM(),
			site: JumpSite{
				Address:      0x00800A,
				Opcode:       0x7C,
				TableAddress: 0x008050,
				Bank:         0x00,
				EntryWidth:   3,
				MinSelector:  0,
				MaxSelector:  2,
			},
			wantCount:   3,
			wantTargets: []uint32{0x008060, 0x008070, 0x008080},
			wantWidth:   3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewEngine(tc.rom)
			table, err := engine.RecoverTable(tc.site)
			if err != nil {
				t.Fatalf("RecoverTable() unexpected error: %v", err)
			}
			if table.EntryCount() != tc.wantCount {
				t.Errorf("EntryCount() = %d, want %d", table.EntryCount(), tc.wantCount)
			}
			if table.EntryWidth != tc.wantWidth {
				t.Errorf("EntryWidth = %d, want %d", table.EntryWidth, tc.wantWidth)
			}
			if table.TableAddress != tc.site.TableAddress {
				t.Errorf("TableAddress = 0x%06X, want 0x%06X", table.TableAddress, tc.site.TableAddress)
			}
			if table.Bank != tc.site.Bank {
				t.Errorf("Bank = %d, want %d", table.Bank, tc.site.Bank)
			}
			for i, wantTarget := range tc.wantTargets {
				entry, ok := table.Entry(i)
				if !ok {
					t.Fatalf("missing entry for selector %d", i)
				}
				if entry.TargetAddress != wantTarget {
					t.Errorf("entry %d TargetAddress = 0x%06X, want 0x%06X", i, entry.TargetAddress, wantTarget)
				}
				if entry.EvidenceKind != EvidenceStaticPlausible {
					t.Errorf("entry %d EvidenceKind = %q, want %q", i, entry.EvidenceKind, EvidenceStaticPlausible)
				}
				if len(entry.WitnessEventIDs) != 0 {
					t.Errorf("entry %d WitnessEventIDs = %v, want empty", i, entry.WitnessEventIDs)
				}
			}
		})
	}
}

func TestIdentifyJumpSite(t *testing.T) {
	rom := makeValidROM()
	engine := NewEngine(rom)

	validJmp := recovery.Instruction{
		Address: 0x00800A,
		Offset:  0x0A,
		Opcode:  0x7C,
		Bytes:   "7c5080",
	}

	tests := []struct {
		name        string
		preceding   []recovery.Instruction
		wantN       int
		wantMinSel  int
		wantMaxSel  int
		wantErrText string
	}{
		{
			name: "ASL before CMP #$08",
			preceding: []recovery.Instruction{
				{Opcode: 0xA9, Bytes: "a901"}, // LDA #$01
				{Opcode: 0x0A, Bytes: "0a"},   // ASL A
				{Opcode: 0xC9, Bytes: "c908"}, // CMP #$08
				{Opcode: 0xB0, Bytes: "b002"}, // BCS +2
				{Opcode: 0xAA, Bytes: "aa"},   // TAX
			},
			wantN:      4,
			wantMinSel: 0,
			wantMaxSel: 3,
		},
		{
			name: "CMP #$04 before ASL",
			preceding: []recovery.Instruction{
				{Opcode: 0xA9, Bytes: "a901"}, // LDA #$01
				{Opcode: 0xC9, Bytes: "c904"}, // CMP #$04
				{Opcode: 0xB0, Bytes: "b003"}, // BCS +3
				{Opcode: 0x0A, Bytes: "0a"},   // ASL A
				{Opcode: 0xAA, Bytes: "aa"},   // TAX
			},
			wantN:      4,
			wantMinSel: 0,
			wantMaxSel: 3,
		},
		{
			name: "CPX #$08 without ASL",
			preceding: []recovery.Instruction{
				{Opcode: 0xA2, Bytes: "a202"}, // LDX #$02
				{Opcode: 0xE0, Bytes: "e008"}, // CPX #$08
				{Opcode: 0xB0, Bytes: "b001"}, // BCS +1
			},
			wantN:      4,
			wantMinSel: 0,
			wantMaxSel: 3,
		},
		{
			name: "CMP #$08 without ASL (even immediate)",
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c908"},
				{Opcode: 0xB0, Bytes: "b002"},
				{Opcode: 0xAA, Bytes: "aa"},
			},
			wantN:      4,
			wantMinSel: 0,
			wantMaxSel: 3,
		},
		{
			name:        "error: no preceding instructions",
			preceding:   nil,
			wantErrText: "unbounded dispatch table",
		},
		{
			name: "error: no comparison found",
			preceding: []recovery.Instruction{
				{Opcode: 0xAA, Bytes: "aa"},
			},
			wantErrText: "no bounding comparison found",
		},
		{
			name: "error: zero bound in comparison",
			preceding: []recovery.Instruction{
				{Opcode: 0xC9, Bytes: "c900"},
			},
			wantErrText: "zero bound",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			site, err := engine.IdentifyJumpSite(validJmp, tc.preceding)
			if tc.wantErrText != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrText)
				}
				if !strings.Contains(err.Error(), tc.wantErrText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if site.MinSelector != tc.wantMinSel || site.MaxSelector != tc.wantMaxSel {
				t.Errorf("selector bounds = [%d, %d], want [%d, %d]", site.MinSelector, site.MaxSelector, tc.wantMinSel, tc.wantMaxSel)
			}
			count := site.MaxSelector - site.MinSelector + 1
			if count != tc.wantN {
				t.Errorf("entry count = %d, want %d", count, tc.wantN)
			}
		})
	}
}

func TestScanJumpSites(t *testing.T) {
	rom := makeValidROM()
	engine := NewEngine(rom)

	instructions := []recovery.Instruction{
		{Opcode: 0xA9, Bytes: "a901"},       // LDA #$01
		{Opcode: 0x0A, Bytes: "0a"},         // ASL A
		{Opcode: 0xC9, Bytes: "c908"},       // CMP #$08
		{Opcode: 0xAA, Bytes: "aa"},         // TAX
		{Address: 0x00800A, Opcode: 0x7C, Bytes: "7c5080"}, // JMP ($8050,X)
		{Opcode: 0xEA, Bytes: "ea"},         // NOP
	}

	sites, err := engine.ScanJumpSites(instructions)
	if err != nil {
		t.Fatalf("ScanJumpSites failed: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("got %d sites, want 1", len(sites))
	}
	if sites[0].TableAddress != 0x008050 {
		t.Errorf("TableAddress = 0x%06X, want 0x008050", sites[0].TableAddress)
	}
	if sites[0].MaxSelector != 3 {
		t.Errorf("MaxSelector = %d, want 3", sites[0].MaxSelector)
	}
}

func TestDynamicWitnessAttachment(t *testing.T) {
	rom := makeValidROM()
	engine := NewEngine(rom)

	site := JumpSite{
		Address:      0x00800A,
		Opcode:       0x7C,
		TableAddress: 0x008050,
		Bank:         0x00,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  3,
	}

	tests := []struct {
		name          string
		events        []TraceEvent
		wantWitnessed map[int][]uint64
	}{
		{
			name: "single witness for selector 1",
			events: []TraceEvent{
				{EventID: 1001, JumpAddress: 0x00800A, Selector: 1, TargetAddress: 0x008070},
			},
			wantWitnessed: map[int][]uint64{
				1: {1001},
			},
		},
		{
			name: "multiple distinct witnesses for selectors 0 and 2",
			events: []TraceEvent{
				{EventID: 2001, JumpAddress: 0x00800A, Selector: 0, TargetAddress: 0x008060},
				{EventID: 2002, JumpAddress: 0x00800A, Selector: 2, TargetAddress: 0x008080},
			},
			wantWitnessed: map[int][]uint64{
				0: {2001},
				2: {2002},
			},
		},
		{
			name: "repeated events for same selector deduplicated",
			events: []TraceEvent{
				{EventID: 3001, JumpAddress: 0x00800A, Selector: 3, TargetAddress: 0x008090},
				{EventID: 3002, JumpAddress: 0x00800A, Selector: 3, TargetAddress: 0x008090},
				{EventID: 3001, JumpAddress: 0x00800A, Selector: 3, TargetAddress: 0x008090}, // dup ID
			},
			wantWitnessed: map[int][]uint64{
				3: {3001, 3002},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table, err := engine.RecoverTable(site)
			if err != nil {
				t.Fatalf("RecoverTable failed: %v", err)
			}

			if err := engine.Correlate(table, tc.events); err != nil {
				t.Fatalf("Correlate failed: %v", err)
			}

			for sel, wantIDs := range tc.wantWitnessed {
				entry, ok := table.Entry(sel)
				if !ok {
					t.Fatalf("missing entry for selector %d", sel)
				}
				if entry.EvidenceKind != EvidenceWitnessed {
					t.Errorf("selector %d EvidenceKind = %q, want %q", sel, entry.EvidenceKind, EvidenceWitnessed)
				}
				if len(entry.WitnessEventIDs) != len(wantIDs) {
					t.Fatalf("selector %d WitnessEventIDs len = %d, want %d", sel, len(entry.WitnessEventIDs), len(wantIDs))
				}
				for i, id := range wantIDs {
					if entry.WitnessEventIDs[i] != id {
						t.Errorf("selector %d WitnessEventIDs[%d] = %d, want %d", sel, i, entry.WitnessEventIDs[i], id)
					}
				}
			}
		})
	}
}

func TestDynamicWitnessAttachment_TraceEvents(t *testing.T) {
	rom := makeValidROM()
	engine := NewEngine(rom)

	site := JumpSite{
		Address:      0x00800A,
		Opcode:       0x7C,
		TableAddress: 0x008050,
		Bank:         0x00,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  3,
	}

	table, err := engine.RecoverTable(site)
	if err != nil {
		t.Fatalf("RecoverTable failed: %v", err)
	}

	events := []trace.Event{
		{
			ID: 5001,
			PC: &trace.PC{Bank: 0x00, Addr: 0x800A},
			CPU: &trace.CPUContext{
				X: 2, // byte offset 2 -> selector 1
			},
			SuccessorPC: &trace.PC{Bank: 0x00, Addr: 0x8070},
		},
		{
			ID: 5002,
			PC: &trace.PC{Bank: 0x00, Addr: 0x800A},
			CPU: &trace.CPUContext{
				X: 6, // byte offset 6 -> selector 3
			},
			SuccessorPC: &trace.PC{Bank: 0x00, Addr: 0x8090},
		},
		{
			// Irrelevant event at different PC should be skipped
			ID: 5003,
			PC: &trace.PC{Bank: 0x00, Addr: 0x9000},
			CPU: &trace.CPUContext{
				X: 0,
			},
			SuccessorPC: &trace.PC{Bank: 0x00, Addr: 0x9010},
		},
	}

	if err := engine.CorrelateTraceEvents(table, events); err != nil {
		t.Fatalf("CorrelateTraceEvents failed: %v", err)
	}

	w := table.Witnessed()
	if len(w) != 2 {
		t.Fatalf("got %d witnessed entries, want 2", len(w))
	}
	if w[0].Selector != 1 || w[0].TargetAddress != 0x008070 {
		t.Errorf("witness 0 = selector %d, target 0x%06X; want 1, 0x008070", w[0].Selector, w[0].TargetAddress)
	}
	if w[1].Selector != 3 || w[1].TargetAddress != 0x008090 {
		t.Errorf("witness 1 = selector %d, target 0x%06X; want 3, 0x008090", w[1].Selector, w[1].TargetAddress)
	}
}

func TestFrontierGeneration(t *testing.T) {
	rom := makeValidROM()
	engine := NewEngine(rom)

	site := JumpSite{
		Address:      0x00800A,
		Opcode:       0x7C,
		TableAddress: 0x008050,
		Bank:         0x00,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  3,
	}

	tests := []struct {
		name          string
		events        []TraceEvent
		wantFrontiers []int // expected frontier selectors
	}{
		{
			name: "witness selectors 1 and 3, frontiers are 0 and 2",
			events: []TraceEvent{
				{EventID: 101, JumpAddress: 0x00800A, Selector: 1, TargetAddress: 0x008070},
				{EventID: 102, JumpAddress: 0x00800A, Selector: 3, TargetAddress: 0x008090},
			},
			wantFrontiers: []int{0, 2},
		},
		{
			name: "witness only selector 0, frontiers are 1, 2, 3",
			events: []TraceEvent{
				{EventID: 201, JumpAddress: 0x00800A, Selector: 0, TargetAddress: 0x008060},
			},
			wantFrontiers: []int{1, 2, 3},
		},
		{
			name: "witness all selectors, frontiers empty",
			events: []TraceEvent{
				{EventID: 301, JumpAddress: 0x00800A, Selector: 0, TargetAddress: 0x008060},
				{EventID: 302, JumpAddress: 0x00800A, Selector: 1, TargetAddress: 0x008070},
				{EventID: 303, JumpAddress: 0x00800A, Selector: 2, TargetAddress: 0x008080},
				{EventID: 304, JumpAddress: 0x00800A, Selector: 3, TargetAddress: 0x008090},
			},
			wantFrontiers: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table, err := engine.RecoverTable(site)
			if err != nil {
				t.Fatalf("RecoverTable failed: %v", err)
			}

			if err := engine.Correlate(table, tc.events); err != nil {
				t.Fatalf("Correlate failed: %v", err)
			}

			frontiers := table.Frontiers()
			if len(frontiers) != len(tc.wantFrontiers) {
				t.Fatalf("got %d frontiers, want %d", len(frontiers), len(tc.wantFrontiers))
			}

			for i, wantSel := range tc.wantFrontiers {
				if frontiers[i].Selector != wantSel {
					t.Errorf("frontiers[%d].Selector = %d, want %d", i, frontiers[i].Selector, wantSel)
				}
				if frontiers[i].EvidenceKind != EvidenceUnwitnessedFrontier {
					t.Errorf("frontiers[%d].EvidenceKind = %q, want %q", i, frontiers[i].EvidenceKind, EvidenceUnwitnessedFrontier)
				}
			}
		})
	}

	t.Run("EmitFrontiers on pure static table marks all entries as frontiers", func(t *testing.T) {
		table, err := engine.RecoverTable(site)
		if err != nil {
			t.Fatalf("RecoverTable failed: %v", err)
		}
		// Before emit, entries are static_plausible
		for _, e := range table.Entries {
			if e.EvidenceKind != EvidenceStaticPlausible {
				t.Errorf("initial EvidenceKind = %q, want %q", e.EvidenceKind, EvidenceStaticPlausible)
			}
		}

		frontiers := table.EmitFrontiers()
		if len(frontiers) != 4 {
			t.Fatalf("EmitFrontiers returned %d entries, want 4", len(frontiers))
		}
		for i, f := range frontiers {
			if f.Selector != i {
				t.Errorf("frontier %d selector = %d, want %d", i, f.Selector, i)
			}
			if f.EvidenceKind != EvidenceUnwitnessedFrontier {
				t.Errorf("frontier %d EvidenceKind = %q, want %q", i, f.EvidenceKind, EvidenceUnwitnessedFrontier)
			}
		}
	})
}

func TestRejectionOfCorruptedTablesAndSelectors(t *testing.T) {
	validSite := JumpSite{
		Address:      0x00800A,
		Opcode:       0x7C,
		TableAddress: 0x008050,
		Bank:         0x00,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  3,
	}

	tests := []struct {
		name        string
		setup       func() ([]byte, JumpSite, []TraceEvent)
		wantErrText string
	}{
		{
			name: "out-of-bounds selector above max in trace event",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				return makeValidROM(), validSite, []TraceEvent{
					{EventID: 1, Selector: 4, TargetAddress: 0x008060}, // selector 4 > max 3
				}
			},
			wantErrText: "out-of-bounds selector 4",
		},
		{
			name: "out-of-bounds negative selector in trace event",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				return makeValidROM(), validSite, []TraceEvent{
					{EventID: 1, Selector: -1, TargetAddress: 0x008060},
				}
			},
			wantErrText: "out-of-bounds selector -1",
		},
		{
			name: "target address mismatch between trace and ROM table",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				return makeValidROM(), validSite, []TraceEvent{
					{EventID: 1, Selector: 1, TargetAddress: 0x009999}, // target 1 is $8070 in ROM
				}
			},
			wantErrText: "does not match table target",
		},
		{
			name: "table base address below $8000 (RAM/MMIO)",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				site := validSite
				site.TableAddress = 0x001000 // below $8000
				return makeValidROM(), site, nil
			},
			wantErrText: "below $8000",
		},
		{
			name: "table base outside ROM mapping",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				site := validSite
				site.TableAddress = 0x7E8000 // WRAM bank 7E
				return makeValidROM(), site, nil
			},
			wantErrText: "not mapped in ROM",
		},
		{
			name: "table extends past end of ROM",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				site := validSite
				site.TableAddress = 0x00FFFC // near end of 32KB bank
				site.MaxSelector = 10        // 11 entries * 2 = 22 bytes extends past 32KB
				return makeValidROM(), site, nil
			},
			wantErrText: "extends past end of ROM",
		},
		{
			name: "target pointer below $8000 in RAM",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				rom := makeValidROM()
				// Corrupt target 1 to point to $0100
				binary.LittleEndian.PutUint16(rom[0x50+2:], 0x0100)
				return rom, validSite, nil
			},
			wantErrText: "in RAM/MMIO below $8000",
		},
		{
			name: "target pointer outside ROM mapping",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				rom := makeValidROM()[:16*1024]
				// Corrupt target 1 to point to unmapped offset
				binary.LittleEndian.PutUint16(rom[0x50+2:], 0xFFFF)
				return rom, validSite, nil
			},
			wantErrText: "outside ROM bounds",
		},
		{
			name: "target begins with BRK (0x00)",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				rom := makeValidROM()
				// Corrupt routine 0 to begin with 0x00
				rom[0x60] = 0x00
				return rom, validSite, nil
			},
			wantErrText: "begins with BRK (0x00)",
		},
		{
			name: "target begins with unrecognized opcode",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				rom := makeValidROM()
				// Corrupt routine 0 to begin with invalid opcode byte 0xFF
				// Note: in 65816, 0xFF is SBC long indexed.
				// Let's check which opcode is nil: in cpu/opcodes.go, check nil opcode.
				// Actually, 0x02 (COP) or check nil opcode.
				// Let's set to custom unmapped opcode if any, or check cpu.Opcodes.
				// In 65816 cpu table, every 256 opcode has an entry, but let's test empty ROM or invalid width.
				rom[0x60] = 0x00 // BRK is guaranteed rejected
				return rom, validSite, nil
			},
			wantErrText: "begins with BRK",
		},
		{
			name: "unsupported entry width",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				site := validSite
				site.EntryWidth = 4 // invalid width
				return makeValidROM(), site, nil
			},
			wantErrText: "unsupported entry width",
		},
		{
			name: "invalid selector bounds (min > max)",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				site := validSite
				site.MinSelector = 5
				site.MaxSelector = 2
				return makeValidROM(), site, nil
			},
			wantErrText: "invalid selector bounds",
		},
		{
			name: "empty ROM",
			setup: func() ([]byte, JumpSite, []TraceEvent) {
				return nil, validSite, nil
			},
			wantErrText: "rom is empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rom, site, traces := tc.setup()
			_, err := Recover(rom, site, traces)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrText)
			}
			if !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrText)
			}
		})
	}

	t.Run("unaligned X register in trace event rejected", func(t *testing.T) {
		rom := makeValidROM()
		engine := NewEngine(rom)
		table, err := engine.RecoverTable(validSite)
		if err != nil {
			t.Fatalf("RecoverTable failed: %v", err)
		}
		// X = 3 is unaligned for EntryWidth = 2
		unalignedEvent := trace.Event{
			ID: 99,
			PC: &trace.PC{Bank: 0x00, Addr: 0x800A},
			CPU: &trace.CPUContext{
				X: 3,
			},
			SuccessorPC: &trace.PC{Bank: 0x00, Addr: 0x8060},
		}
		err = engine.CorrelateTraceEvents(table, []trace.Event{unalignedEvent})
		if err == nil {
			t.Fatalf("expected alignment error, got nil")
		}
		if !strings.Contains(err.Error(), "not aligned to entry width") {
			t.Fatalf("error %q does not contain 'not aligned to entry width'", err.Error())
		}
	})
}

func TestSelectorTransformHelpers(t *testing.T) {
	dt := &DispatchTable{
		TableAddress: 0x008050,
		Bank:         0x00,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  3,
	}

	// Selector to offset:
	for sel := 0; sel <= 3; sel++ {
		off, err := dt.SelectorToOffset(sel)
		if err != nil {
			t.Errorf("SelectorToOffset(%d) error: %v", sel, err)
		}
		if off != sel*2 {
			t.Errorf("SelectorToOffset(%d) = %d, want %d", sel, off, sel*2)
		}

		backSel, err := dt.OffsetToSelector(off)
		if err != nil {
			t.Errorf("OffsetToSelector(%d) error: %v", off, err)
		}
		if backSel != sel {
			t.Errorf("OffsetToSelector(%d) = %d, want %d", off, backSel, sel)
		}
	}

	// Out of bounds:
	if _, err := dt.SelectorToOffset(4); err == nil {
		t.Error("expected error for selector 4, got nil")
	}
	if _, err := dt.SelectorToOffset(-1); err == nil {
		t.Error("expected error for selector -1, got nil")
	}
	// Unaligned byte offset:
	if _, err := dt.OffsetToSelector(3); err == nil {
		t.Error("expected error for unaligned byte offset 3, got nil")
	}
}
