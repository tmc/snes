package extractor

import (
	"encoding/json"
	"fmt"
	"sort"
)

// RawEvent represents any event from the capture stream.
type RawEvent struct {
	Kind   string          `json:"kind"`
	Frame  int             `json:"frame,omitempty"`
	ID     uint64          `json:"id,omitempty"`
	Cycle  uint64          `json:"cycle,omitempty"`
	Space  string          `json:"space,omitempty"`
	Op     string          `json:"op,omitempty"`
	Addr   uint32          `json:"addr,omitempty"`
	Value  uint8           `json:"value,omitempty"`
	After  uint8           `json:"after,omitempty"`
	Before uint8           `json:"before,omitempty"`
	Source *EventSource    `json:"source,omitempty"`
	CPU    *EventCPU       `json:"cpu,omitempty"`
	Insn   *RawInsn        `json:"insn,omitempty"`
	PC     *EventPC        `json:"pc,omitempty"`
	Raw    json.RawMessage `json:"-"`
}

type EventSource struct {
	Space string `json:"space,omitempty"`
}

type EventCPU struct {
	EffectiveAddr uint32 `json:"effective_addr,omitempty"`
}

type EventPC struct {
	Bank uint8  `json:"bank,omitempty"`
	Addr uint16 `json:"addr,omitempty"`
}

type RawInsn struct {
	Seq         uint64     `json:"seq"`
	Entry       CPUState   `json:"entry"`
	Exit        CPUState   `json:"exit"`
	Fetches     []FetchRec `json:"fetches,omitempty"`
	Length      int        `json:"length,omitempty"`
	Status      string     `json:"status"`
	Disasm      string     `json:"disasm,omitempty"`
	Instruction string     `json:"instruction,omitempty"`
}

type FetchRec struct {
	Addr      uint32 `json:"addr"`
	Value     uint8  `json:"value"`
	Role      string `json:"role"`
	ROMOffset uint32 `json:"rom_offset,omitempty"`
}

// OpcodeMemoryContract specifies the pinned producer completeness expectations for a CPU instruction.
type OpcodeMemoryContract struct {
	Supported bool
	MinWrites int
	MinReads  int
}

// supportedOpcodeContracts lists the supported opcodes and their required minimum data memory accesses.
// Any candidate containing unmodeled opcodes is conservatively refused rather than claiming complete memory effects.
var supportedOpcodeContracts = map[uint8]OpcodeMemoryContract{
	// Immediate and register operations (0 data writes, 0 data reads)
	0xA9: {Supported: true, MinWrites: 0, MinReads: 0}, // LDA #imm
	0xA2: {Supported: true, MinWrites: 0, MinReads: 0}, // LDX #imm
	0xA0: {Supported: true, MinWrites: 0, MinReads: 0}, // LDY #imm
	0xC9: {Supported: true, MinWrites: 0, MinReads: 0}, // CMP #imm
	0xE0: {Supported: true, MinWrites: 0, MinReads: 0}, // CPX #imm
	0xC0: {Supported: true, MinWrites: 0, MinReads: 0}, // CPY #imm
	0x49: {Supported: true, MinWrites: 0, MinReads: 0}, // EOR #imm
	0x09: {Supported: true, MinWrites: 0, MinReads: 0}, // ORA #imm
	0x29: {Supported: true, MinWrites: 0, MinReads: 0}, // AND #imm
	0x69: {Supported: true, MinWrites: 0, MinReads: 0}, // ADC #imm
	0xE9: {Supported: true, MinWrites: 0, MinReads: 0}, // SBC #imm
	0x18: {Supported: true, MinWrites: 0, MinReads: 0}, // CLC
	0x38: {Supported: true, MinWrites: 0, MinReads: 0}, // SEC
	0x58: {Supported: true, MinWrites: 0, MinReads: 0}, // CLI
	0x78: {Supported: true, MinWrites: 0, MinReads: 0}, // SEI
	0xB8: {Supported: true, MinWrites: 0, MinReads: 0}, // CLV
	0xD8: {Supported: true, MinWrites: 0, MinReads: 0}, // CLD
	0xF8: {Supported: true, MinWrites: 0, MinReads: 0}, // SED
	0xC2: {Supported: true, MinWrites: 0, MinReads: 0}, // REP #imm
	0xE2: {Supported: true, MinWrites: 0, MinReads: 0}, // SEP #imm
	0xEA: {Supported: true, MinWrites: 0, MinReads: 0}, // NOP
	0xAA: {Supported: true, MinWrites: 0, MinReads: 0}, // TAX
	0x8A: {Supported: true, MinWrites: 0, MinReads: 0}, // TXA
	0xA8: {Supported: true, MinWrites: 0, MinReads: 0}, // TAY
	0x98: {Supported: true, MinWrites: 0, MinReads: 0}, // TYA
	0xBA: {Supported: true, MinWrites: 0, MinReads: 0}, // TSX
	0x9A: {Supported: true, MinWrites: 0, MinReads: 0}, // TXS
	0x9B: {Supported: true, MinWrites: 0, MinReads: 0}, // TXY
	0xBB: {Supported: true, MinWrites: 0, MinReads: 0}, // TYX
	0x1B: {Supported: true, MinWrites: 0, MinReads: 0}, // TCS
	0x3B: {Supported: true, MinWrites: 0, MinReads: 0}, // TSC
	0x5B: {Supported: true, MinWrites: 0, MinReads: 0}, // TCD
	0x7B: {Supported: true, MinWrites: 0, MinReads: 0}, // TDC
	0xEB: {Supported: true, MinWrites: 0, MinReads: 0}, // XBA
	0xFB: {Supported: true, MinWrites: 0, MinReads: 0}, // XCE
	0x42: {Supported: true, MinWrites: 0, MinReads: 0}, // WDM
	0xE8: {Supported: true, MinWrites: 0, MinReads: 0}, // INX
	0xC8: {Supported: true, MinWrites: 0, MinReads: 0}, // INY
	0xCA: {Supported: true, MinWrites: 0, MinReads: 0}, // DEX
	0x88: {Supported: true, MinWrites: 0, MinReads: 0}, // DEY
	0x0A: {Supported: true, MinWrites: 0, MinReads: 0}, // ASL A
	0x4A: {Supported: true, MinWrites: 0, MinReads: 0}, // LSR A
	0x2A: {Supported: true, MinWrites: 0, MinReads: 0}, // ROL A
	0x6A: {Supported: true, MinWrites: 0, MinReads: 0}, // ROR A

	// Branches (0 data writes, 0 data reads)
	0x90: {Supported: true, MinWrites: 0, MinReads: 0}, // BCC
	0xB0: {Supported: true, MinWrites: 0, MinReads: 0}, // BCS
	0xF0: {Supported: true, MinWrites: 0, MinReads: 0}, // BEQ
	0x30: {Supported: true, MinWrites: 0, MinReads: 0}, // BMI
	0xD0: {Supported: true, MinWrites: 0, MinReads: 0}, // BNE
	0x10: {Supported: true, MinWrites: 0, MinReads: 0}, // BPL
	0x50: {Supported: true, MinWrites: 0, MinReads: 0}, // BVC
	0x70: {Supported: true, MinWrites: 0, MinReads: 0}, // BVS
	0x80: {Supported: true, MinWrites: 0, MinReads: 0}, // BRA
	0x82: {Supported: true, MinWrites: 0, MinReads: 0}, // BRL

	// Memory stores (at least 1 write, 0 data reads)
	0x85: {Supported: true, MinWrites: 1, MinReads: 0}, // STA dp
	0x8D: {Supported: true, MinWrites: 1, MinReads: 0}, // STA abs
	0x9D: {Supported: true, MinWrites: 1, MinReads: 0}, // STA abs,X
	0x99: {Supported: true, MinWrites: 1, MinReads: 0}, // STA abs,Y
	0x8F: {Supported: true, MinWrites: 1, MinReads: 0}, // STA long
	0x9F: {Supported: true, MinWrites: 1, MinReads: 0}, // STA long,X
	0x95: {Supported: true, MinWrites: 1, MinReads: 0}, // STA dp,X
	0x81: {Supported: true, MinWrites: 1, MinReads: 0}, // STA (dp,X)
	0x91: {Supported: true, MinWrites: 1, MinReads: 0}, // STA (dp),Y
	0x87: {Supported: true, MinWrites: 1, MinReads: 0}, // STA [dp]
	0x97: {Supported: true, MinWrites: 1, MinReads: 0}, // STA [dp],Y
	0x86: {Supported: true, MinWrites: 1, MinReads: 0}, // STX dp
	0x8E: {Supported: true, MinWrites: 1, MinReads: 0}, // STX abs
	0x96: {Supported: true, MinWrites: 1, MinReads: 0}, // STX dp,Y
	0x84: {Supported: true, MinWrites: 1, MinReads: 0}, // STY dp
	0x8C: {Supported: true, MinWrites: 1, MinReads: 0}, // STY abs
	0x94: {Supported: true, MinWrites: 1, MinReads: 0}, // STY dp,X
	0x64: {Supported: true, MinWrites: 1, MinReads: 0}, // STZ dp
	0x9C: {Supported: true, MinWrites: 1, MinReads: 0}, // STZ abs
	0x74: {Supported: true, MinWrites: 1, MinReads: 0}, // STZ dp,X
	0x9E: {Supported: true, MinWrites: 1, MinReads: 0}, // STZ abs,X

	// Stack pushes (at least 1 or 2 writes, 0 data reads)
	0x48: {Supported: true, MinWrites: 1, MinReads: 0}, // PHA
	0xDA: {Supported: true, MinWrites: 1, MinReads: 0}, // PHX
	0x5A: {Supported: true, MinWrites: 1, MinReads: 0}, // PHY
	0x08: {Supported: true, MinWrites: 1, MinReads: 0}, // PHP
	0x8B: {Supported: true, MinWrites: 1, MinReads: 0}, // PHB
	0x4B: {Supported: true, MinWrites: 1, MinReads: 0}, // PHK
	0xD4: {Supported: true, MinWrites: 2, MinReads: 0}, // PEI
	0xF4: {Supported: true, MinWrites: 2, MinReads: 0}, // PEA
	0x62: {Supported: true, MinWrites: 2, MinReads: 0}, // PER
	0x20: {Supported: true, MinWrites: 2, MinReads: 0}, // JSR abs
	0x22: {Supported: true, MinWrites: 3, MinReads: 0}, // JSL

	// Stack pulls / returns (0 writes, reads from stack)
	0x68: {Supported: true, MinWrites: 0, MinReads: 1}, // PLA
	0xFA: {Supported: true, MinWrites: 0, MinReads: 1}, // PLX
	0x7A: {Supported: true, MinWrites: 0, MinReads: 1}, // PLY
	0x28: {Supported: true, MinWrites: 0, MinReads: 1}, // PLP
	0xAB: {Supported: true, MinWrites: 0, MinReads: 1}, // PLB
	0x60: {Supported: true, MinWrites: 0, MinReads: 2}, // RTS (pulls 2 bytes)
	0x6B: {Supported: true, MinWrites: 0, MinReads: 3}, // RTL (pulls 3 bytes)

	// Memory loads (at least 1 data read)
	0xA5: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA dp
	0xAD: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA abs
	0xAF: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA long
	0xB5: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA dp,X
	0xBD: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA abs,X
	0xB9: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA abs,Y
	0xBF: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA long,X
	0xA1: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA (dp,X)
	0xB1: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA (dp),Y
	0xA7: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA [dp]
	0xB7: {Supported: true, MinWrites: 0, MinReads: 1}, // LDA [dp],Y
	0xA6: {Supported: true, MinWrites: 0, MinReads: 1}, // LDX dp
	0xAE: {Supported: true, MinWrites: 0, MinReads: 1}, // LDX abs
	0xB6: {Supported: true, MinWrites: 0, MinReads: 1}, // LDX dp,Y
	0xBE: {Supported: true, MinWrites: 0, MinReads: 1}, // LDX abs,Y
	0xA4: {Supported: true, MinWrites: 0, MinReads: 1}, // LDY dp
	0xAC: {Supported: true, MinWrites: 0, MinReads: 1}, // LDY abs
	0xB4: {Supported: true, MinWrites: 0, MinReads: 1}, // LDY dp,X
	0xBC: {Supported: true, MinWrites: 0, MinReads: 1}, // LDY abs,X

	// Read-Modify-Write memory operations (1 read, 1 write)
	0xEE: {Supported: true, MinWrites: 1, MinReads: 1}, // INC abs
	0xFE: {Supported: true, MinWrites: 1, MinReads: 1}, // INC abs,X
	0xE6: {Supported: true, MinWrites: 1, MinReads: 1}, // INC dp
	0xF6: {Supported: true, MinWrites: 1, MinReads: 1}, // INC dp,X
	0xCE: {Supported: true, MinWrites: 1, MinReads: 1}, // DEC abs
	0xDE: {Supported: true, MinWrites: 1, MinReads: 1}, // DEC abs,X
	0xC6: {Supported: true, MinWrites: 1, MinReads: 1}, // DEC dp
	0xD6: {Supported: true, MinWrites: 1, MinReads: 1}, // DEC dp,X
}

// BusEventList holds sorted bus events for binary search.
type BusEventList []*RawEvent

func (b BusEventList) Len() int { return len(b) }
func (b BusEventList) Less(i, j int) bool {
	if b[i].Cycle != b[j].Cycle {
		return b[i].Cycle < b[j].Cycle
	}
	return b[i].ID < b[j].ID
}
func (b BusEventList) Swap(i, j int) { b[i], b[j] = b[j], b[i] }

// MemoryTrace records the resolved accesses for an occurrence.
type MemoryTrace struct {
	InitialMemory  []MemoryCell
	ObservedWrites []MemoryCell
	RefusalReason  string
}

// ExtractMemoryTrace joins bus events within [startCycle, endCycle] to routine instructions.
// It verifies that all data accesses map cleanly to WRAM and checks for any intervening
// hardware events (DMA, HDMA, MMIO, interrupts) or read-after-write inconsistencies.
func ExtractMemoryTrace(busEvents BusEventList, transitions []*RawEvent, startCycle, endCycle uint64, insns []*RawInsn) MemoryTrace {
	// Check for intervening transitions, DMA, HDMA, MMIO, or interrupts.
	for _, t := range transitions {
		c := t.Cycle
		if c > startCycle && c <= endCycle {
			switch t.Kind {
			case "dma", "hdma":
				return MemoryTrace{RefusalReason: fmt.Sprintf("hardware_event_in_interval:%s", t.Kind)}
			case "mmio":
				return MemoryTrace{RefusalReason: "hardware_event_in_interval:mmio"}
			case "cpu_transition":
				return MemoryTrace{RefusalReason: "interrupt_or_transition_in_interval"}
			default:
				return MemoryTrace{RefusalReason: fmt.Sprintf("intervening_event_in_interval:%s", t.Kind)}
			}
		}
	}

	// Find the slice of bus events where startCycle < cycle <= endCycle.
	first := sort.Search(len(busEvents), func(i int) bool {
		return busEvents[i].Cycle > startCycle
	})
	last := sort.Search(len(busEvents), func(i int) bool {
		return busEvents[i].Cycle > endCycle
	})

	// Pinned producer completeness certification: evaluate the instruction sequence against
	// the explicit supported opcode memory contract.
	var (
		expectedMinWrites int
		expectedMinReads  int
	)
	for _, insn := range insns {
		if insn != nil && len(insn.Fetches) > 0 {
			op := insn.Fetches[0].Value
			contract, ok := supportedOpcodeContracts[op]
			if !ok {
				return MemoryTrace{RefusalReason: fmt.Sprintf("unsupported_memory_contract_opcode:0x%02X", op)}
			}
			expectedMinWrites += contract.MinWrites
			expectedMinReads += contract.MinReads
		}
	}

	var (
		curMemory  = make(map[uint32]uint8)
		initMemory = make(map[uint32]uint8)
		writes     []MemoryCell
		busReads   int
	)

	for i := first; i < last; i++ {
		ev := busEvents[i]
		// Skip program opcode/operand instruction fetches from ROM.
		if ev.Space == "cpu" && ev.Source != nil && ev.Source.Space == "rom" {
			continue
		}
		// Data bus memory accesses must resolve to pure WRAM.
		if ev.Space != "wram" {
			return MemoryTrace{RefusalReason: fmt.Sprintf("unsupported_memory_space:%s", ev.Space)}
		}

		// Resolve canonical 24-bit SNES WRAM address (0x7E0000..0x7FFFFF).
		canonicalAddr, err := canonicalWRAMOffset(ev.Addr)
		if err != nil {
			return MemoryTrace{RefusalReason: err.Error()}
		}
		val := ev.Value

		if ev.Op == "read" {
			busReads++
			if storedVal, exists := curMemory[canonicalAddr]; exists {
				if storedVal != val {
					return MemoryTrace{
						RefusalReason: fmt.Sprintf("read_after_write_mismatch:addr=0x%06x,cur=%d,read=%d", canonicalAddr, storedVal, val),
					}
				}
			} else {
				initMemory[canonicalAddr] = val
				curMemory[canonicalAddr] = val
			}
		} else if ev.Op == "write" {
			curMemory[canonicalAddr] = val
			writes = append(writes, MemoryCell{Address: canonicalAddr, Value: val})
		}
	}

	// Verify that the observed memory accesses satisfy the producer completeness contract.
	if len(writes) < expectedMinWrites {
		return MemoryTrace{
			RefusalReason: fmt.Sprintf("unverified_bus_completeness:missing_bus_writes:got=%d,want=%d", len(writes), expectedMinWrites),
		}
	}
	if busReads < expectedMinReads {
		return MemoryTrace{
			RefusalReason: fmt.Sprintf("unverified_bus_completeness:missing_bus_reads:got=%d,want=%d", busReads, expectedMinReads),
		}
	}

	// Sort initial memory by canonical address.
	var initCells []MemoryCell
	for a, v := range initMemory {
		initCells = append(initCells, MemoryCell{Address: a, Value: v})
	}
	sort.Slice(initCells, func(i, j int) bool {
		return initCells[i].Address < initCells[j].Address
	})

	return MemoryTrace{
		InitialMemory:  initCells,
		ObservedWrites: writes,
	}
}

// canonicalWRAMOffset maps the producer's WRAM byte offset, not the CPU
// instruction's effective base address, to the physical WRAM bank range.
func canonicalWRAMOffset(addr uint32) (uint32, error) {
	if addr >= 0x20000 {
		return 0, fmt.Errorf("invalid_wram_offset:0x%x", addr)
	}
	return 0x7e0000 + addr, nil
}
