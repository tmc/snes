package dispatch

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
)

// EvidenceKind describes the provenance and confidence level of a recovered dispatch entry.
type EvidenceKind string

const (
	// EvidenceWitnessed denotes a target observed in authentic dynamic trace execution.
	EvidenceWitnessed EvidenceKind = "witnessed"

	// EvidenceStaticPlausible denotes a target validated statically against executable ROM code.
	EvidenceStaticPlausible EvidenceKind = "static_plausible"

	// EvidenceUnwitnessedFrontier denotes a statically valid table slot that has not been witnessed dynamically.
	EvidenceUnwitnessedFrontier EvidenceKind = "unwitnessed_frontier"
)

// DispatchEntry represents a single target route in a dispatch table.
type DispatchEntry struct {
	Selector        int          `json:"selector"`
	TargetAddress   uint32       `json:"target_address"`
	EvidenceKind    EvidenceKind `json:"evidence_kind"`
	WitnessEventIDs []uint64     `json:"witness_event_ids,omitempty"`
}

// DispatchTable represents a recovered indirect dispatch table and its target routes.
type DispatchTable struct {
	JumpAddress  uint32          `json:"jump_address,omitempty"`
	TableAddress uint32          `json:"table_address"`
	Bank         uint8           `json:"bank"`
	EntryWidth   int             `json:"entry_width"`
	MinSelector  int             `json:"min_selector"`
	MaxSelector  int             `json:"max_selector"`
	Entries      []DispatchEntry `json:"entries"`
}

// EntryCount returns the total number of entries in the dispatch table.
func (dt *DispatchTable) EntryCount() int {
	return len(dt.Entries)
}

// Entry returns the dispatch entry for the given selector, if present.
func (dt *DispatchTable) Entry(selector int) (*DispatchEntry, bool) {
	for i := range dt.Entries {
		if dt.Entries[i].Selector == selector {
			return &dt.Entries[i], true
		}
	}
	return nil, false
}

// Target returns the target address for the given selector, if present.
func (dt *DispatchTable) Target(selector int) (uint32, bool) {
	entry, ok := dt.Entry(selector)
	if !ok {
		return 0, false
	}
	return entry.TargetAddress, true
}

// SelectorToOffset converts a selector index to the corresponding table byte offset.
func (dt *DispatchTable) SelectorToOffset(selector int) (int, error) {
	if selector < dt.MinSelector || selector > dt.MaxSelector {
		return 0, fmt.Errorf("dispatch: selector %d out of bounds [%d, %d]", selector, dt.MinSelector, dt.MaxSelector)
	}
	return (selector - dt.MinSelector) * dt.EntryWidth, nil
}

// OffsetToSelector converts an index register byte offset to a selector index.
func (dt *DispatchTable) OffsetToSelector(byteOffset int) (int, error) {
	if byteOffset < 0 {
		return 0, fmt.Errorf("dispatch: negative byte offset %d", byteOffset)
	}
	if byteOffset%dt.EntryWidth != 0 {
		return 0, fmt.Errorf("dispatch: byte offset %d is not aligned to entry width %d", byteOffset, dt.EntryWidth)
	}
	selector := dt.MinSelector + (byteOffset / dt.EntryWidth)
	if selector > dt.MaxSelector {
		return 0, fmt.Errorf("dispatch: byte offset %d maps to selector %d out of bounds [%d, %d]", byteOffset, selector, dt.MinSelector, dt.MaxSelector)
	}
	return selector, nil
}

// Frontiers returns all entries that represent unwitnessed exploration frontiers.
func (dt *DispatchTable) Frontiers() []DispatchEntry {
	var frontiers []DispatchEntry
	for _, e := range dt.Entries {
		if e.EvidenceKind == EvidenceUnwitnessedFrontier {
			frontiers = append(frontiers, e)
		}
	}
	return frontiers
}

// Witnessed returns all entries that have been dynamically witnessed.
func (dt *DispatchTable) Witnessed() []DispatchEntry {
	var witnessed []DispatchEntry
	for _, e := range dt.Entries {
		if e.EvidenceKind == EvidenceWitnessed {
			witnessed = append(witnessed, e)
		}
	}
	return witnessed
}

// EmitFrontiers marks any unobserved table entries as unwitnessed frontiers and returns them.
func (dt *DispatchTable) EmitFrontiers() []DispatchEntry {
	var frontiers []DispatchEntry
	for i := range dt.Entries {
		if dt.Entries[i].EvidenceKind != EvidenceWitnessed {
			dt.Entries[i].EvidenceKind = EvidenceUnwitnessedFrontier
			frontiers = append(frontiers, dt.Entries[i])
		}
	}
	return frontiers
}

// JumpSite represents a candidate indirect jump site in code.
type JumpSite struct {
	Address      uint32 `json:"address"`
	Opcode       byte   `json:"opcode"`
	TableAddress uint32 `json:"table_address"`
	Bank         uint8  `json:"bank"`
	EntryWidth   int    `json:"entry_width"`
	MinSelector  int    `json:"min_selector"`
	MaxSelector  int    `json:"max_selector"`
}

// TraceEvent represents a dynamic execution observation of an indirect jump.
type TraceEvent struct {
	EventID       uint64 `json:"event_id"`
	JumpAddress   uint32 `json:"jump_address,omitempty"`
	Selector      int    `json:"selector"`
	TargetAddress uint32 `json:"target_address"`
}

// AddressMapper maps a 24-bit SNES bus address to a physical ROM offset.
type AddressMapper func(addr uint32, romLen int) (uint32, bool)

// Engine recovers dispatch tables from ROM and correlates them with execution traces.
type Engine struct {
	rom    []byte
	mapper AddressMapper
}

// NewEngine creates a new dispatch recovery engine with standard LoROM address mapping.
func NewEngine(rom []byte) *Engine {
	return NewEngineWithMapper(rom, LoROMToOffset)
}

// NewEngineWithMapper creates a new dispatch recovery engine with a custom address mapper.
func NewEngineWithMapper(rom []byte, mapper AddressMapper) *Engine {
	if mapper == nil {
		mapper = LoROMToOffset
	}
	return &Engine{
		rom:    rom,
		mapper: mapper,
	}
}

// LoROMToOffset maps a 24-bit SNES bus address to a physical ROM offset in LoROM mapping.
func LoROMToOffset(addr uint32, romLen int) (uint32, bool) {
	bank := (addr >> 16) & 0xFF
	bankOffset := addr & 0xFFFF

	if bankOffset < 0x8000 {
		return 0, false
	}
	if bank == 0x7E || bank == 0x7F {
		return 0, false
	}

	var physBank uint32
	if bank >= 0x80 {
		physBank = bank - 0x80
	} else {
		physBank = bank
	}

	offset := (physBank * 32768) + (bankOffset - 0x8000)
	if int(offset) >= romLen {
		return 0, false
	}
	return offset, true
}

// IdentifyJumpSite analyzes an indirect jump instruction and preceding instructions
// to identify candidate pointer table parameters and selector bounds.
func (e *Engine) IdentifyJumpSite(jmp recovery.Instruction, preceding []recovery.Instruction) (*JumpSite, error) {
	if jmp.Opcode != 0x7C {
		return nil, fmt.Errorf("dispatch: unsupported opcode 0x%02X: expected 0x7C (JMP (abs,X))", jmp.Opcode)
	}

	var instBytes []byte
	if len(jmp.Bytes) >= 6 {
		var err error
		instBytes, err = hex.DecodeString(jmp.Bytes)
		if err != nil || len(instBytes) < 3 {
			instBytes = nil
		}
	}
	if len(instBytes) < 3 {
		if int(jmp.Offset)+3 <= len(e.rom) {
			instBytes = e.rom[jmp.Offset : jmp.Offset+3]
		} else {
			return nil, errors.New("dispatch: instruction extends past end of ROM")
		}
	}

	tableBase16 := binary.LittleEndian.Uint16(instBytes[1:3])
	if tableBase16 < 0x8000 {
		return nil, fmt.Errorf("dispatch: table base $0x%04X is below $8000 (RAM/MMIO)", tableBase16)
	}

	bank := uint8(jmp.Address >> 16)
	tableBase := (uint32(bank) << 16) | uint32(tableBase16)
	if _, ok := e.mapper(tableBase, len(e.rom)); !ok {
		return nil, fmt.Errorf("dispatch: table base 0x%06X is not mapped in ROM", tableBase)
	}

	n, err := extractBounds(preceding)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}
	if n <= 0 || n > 256 {
		return nil, fmt.Errorf("dispatch: invalid entry count %d", n)
	}

	return &JumpSite{
		Address:      jmp.Address,
		Opcode:       jmp.Opcode,
		TableAddress: tableBase,
		Bank:         bank,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  n - 1,
	}, nil
}

// ScanJumpSites scans a slice of decoded instructions and extracts all valid candidate jump sites.
func (e *Engine) ScanJumpSites(instructions []recovery.Instruction) ([]JumpSite, error) {
	var sites []JumpSite
	for i, inst := range instructions {
		if inst.Opcode == 0x7C {
			site, err := e.IdentifyJumpSite(inst, instructions[:i])
			if err != nil {
				continue
			}
			sites = append(sites, *site)
		}
	}
	return sites, nil
}

// RecoverTable reads candidate pointer table entries from ROM, validates each target
// as plausible code within mapped ROM, and returns the recovered DispatchTable.
func (e *Engine) RecoverTable(site JumpSite) (*DispatchTable, error) {
	if len(e.rom) == 0 {
		return nil, errors.New("dispatch: rom is empty")
	}
	if site.EntryWidth != 2 && site.EntryWidth != 3 {
		return nil, fmt.Errorf("dispatch: unsupported entry width %d: must be 2 or 3", site.EntryWidth)
	}
	if site.MinSelector < 0 || site.MinSelector > site.MaxSelector {
		return nil, fmt.Errorf("dispatch: invalid selector bounds [%d, %d]", site.MinSelector, site.MaxSelector)
	}

	count := site.MaxSelector - site.MinSelector + 1
	if count <= 0 || count > 256 {
		return nil, fmt.Errorf("dispatch: invalid entry count %d", count)
	}

	tableBase16 := site.TableAddress & 0xFFFF
	if tableBase16 < 0x8000 {
		return nil, fmt.Errorf("dispatch: table base $0x%04X is below $8000 (RAM/MMIO)", tableBase16)
	}

	baseOffset, ok := e.mapper(site.TableAddress, len(e.rom))
	if !ok {
		return nil, fmt.Errorf("dispatch: table base 0x%06X is not mapped in ROM", site.TableAddress)
	}

	tableBytes := count * site.EntryWidth
	if int(baseOffset)+tableBytes > len(e.rom) {
		return nil, fmt.Errorf("dispatch: table at offset 0x%06X (%d bytes) extends past end of ROM (%d bytes)", baseOffset, tableBytes, len(e.rom))
	}

	entries := make([]DispatchEntry, 0, count)
	for s := site.MinSelector; s <= site.MaxSelector; s++ {
		idx := s - site.MinSelector
		off := baseOffset + uint32(idx*site.EntryWidth)

		var targetAddr uint32
		if site.EntryWidth == 2 {
			tgt16 := binary.LittleEndian.Uint16(e.rom[off : off+2])
			if tgt16 < 0x8000 {
				return nil, fmt.Errorf("dispatch: target %d ($%04X) is in RAM/MMIO below $8000", s, tgt16)
			}
			targetAddr = (uint32(site.Bank) << 16) | uint32(tgt16)
		} else {
			targetAddr = uint32(e.rom[off]) | (uint32(e.rom[off+1]) << 8) | (uint32(e.rom[off+2]) << 16)
			if (targetAddr & 0xFFFF) < 0x8000 {
				return nil, fmt.Errorf("dispatch: target %d ($%06X) is in RAM/MMIO below $8000", s, targetAddr)
			}
		}

		targetOff, ok := e.mapper(targetAddr, len(e.rom))
		if !ok {
			return nil, fmt.Errorf("dispatch: target %d ($%06X) is outside ROM bounds", s, targetAddr)
		}

		opByte := e.rom[targetOff]
		op := cpu.Opcodes[opByte]
		if op.Op == nil {
			return nil, fmt.Errorf("dispatch: target %d at 0x%06X has unrecognized opcode 0x%02X", s, targetAddr, opByte)
		}
		if opByte == 0x00 {
			return nil, fmt.Errorf("dispatch: target %d at 0x%06X begins with BRK (0x00)", s, targetAddr)
		}

		entries = append(entries, DispatchEntry{
			Selector:      s,
			TargetAddress: targetAddr,
			EvidenceKind:  EvidenceStaticPlausible,
		})
	}

	return &DispatchTable{
		JumpAddress:  site.Address,
		TableAddress: site.TableAddress,
		Bank:         site.Bank,
		EntryWidth:   site.EntryWidth,
		MinSelector:  site.MinSelector,
		MaxSelector:  site.MaxSelector,
		Entries:      entries,
	}, nil
}

// Correlate attaches dynamic trace events to the recovered dispatch table.
// Witnessed entries are updated with witness event IDs. Unwitnessed entries are
// classified as unwitnessed frontiers. Out-of-bounds selectors and target mismatches
// cause an error.
func (e *Engine) Correlate(table *DispatchTable, events []TraceEvent) error {
	if table == nil {
		return errors.New("dispatch: nil table")
	}
	if len(events) == 0 {
		return nil
	}

	entryMap := make(map[int]*DispatchEntry, len(table.Entries))
	for i := range table.Entries {
		entryMap[table.Entries[i].Selector] = &table.Entries[i]
	}

	for _, ev := range events {
		if table.JumpAddress != 0 && ev.JumpAddress != 0 && table.JumpAddress != ev.JumpAddress {
			continue
		}
		if ev.Selector < table.MinSelector || ev.Selector > table.MaxSelector {
			return fmt.Errorf("dispatch: trace event %d has out-of-bounds selector %d (valid range [%d, %d])", ev.EventID, ev.Selector, table.MinSelector, table.MaxSelector)
		}
		entry, ok := entryMap[ev.Selector]
		if !ok {
			return fmt.Errorf("dispatch: trace event %d has selector %d not in table", ev.EventID, ev.Selector)
		}
		if entry.TargetAddress != ev.TargetAddress {
			return fmt.Errorf("dispatch: trace event %d target 0x%06X does not match table target 0x%06X at selector %d", ev.EventID, ev.TargetAddress, entry.TargetAddress, ev.Selector)
		}

		entry.EvidenceKind = EvidenceWitnessed
		seen := false
		for _, id := range entry.WitnessEventIDs {
			if id == ev.EventID {
				seen = true
				break
			}
		}
		if !seen {
			entry.WitnessEventIDs = append(entry.WitnessEventIDs, ev.EventID)
		}
	}

	// Classify unobserved entries as unwitnessed frontiers.
	for i := range table.Entries {
		if table.Entries[i].EvidenceKind != EvidenceWitnessed {
			table.Entries[i].EvidenceKind = EvidenceUnwitnessedFrontier
		}
	}

	return nil
}

// CorrelateTraceEvents translates trace.Event records into dynamic dispatch witnesses
// and correlates them with the dispatch table.
func (e *Engine) CorrelateTraceEvents(table *DispatchTable, events []trace.Event) error {
	if table == nil {
		return errors.New("dispatch: nil table")
	}

	var traceEvents []TraceEvent
	for _, ev := range events {
		var eventPC uint32
		if ev.PC != nil {
			eventPC = (uint32(ev.PC.Bank) << 16) | uint32(ev.PC.Addr)
		} else if ev.Insn != nil && ev.Insn.Entry != (trace.Registers{}) {
			eventPC = (uint32(ev.Insn.Entry.PB) << 16) | uint32(ev.Insn.Entry.PC)
		}

		if table.JumpAddress != 0 && eventPC != 0 && eventPC != table.JumpAddress {
			continue
		}
		if ev.CPU == nil {
			continue
		}

		var targetAddr uint32
		if ev.SuccessorPC != nil {
			targetAddr = (uint32(ev.SuccessorPC.Bank) << 16) | uint32(ev.SuccessorPC.Addr)
		} else if ev.Insn != nil && (ev.Insn.SuccessorPC.Bank != 0 || ev.Insn.SuccessorPC.Addr != 0) {
			targetAddr = (uint32(ev.Insn.SuccessorPC.Bank) << 16) | uint32(ev.Insn.SuccessorPC.Addr)
		} else if ev.CPUAfter != nil {
			targetAddr = (uint32(ev.CPUAfter.PBR) << 16) | uint32(ev.CPUAfter.PC)
		} else {
			continue
		}

		x := int(ev.CPU.X)
		if x%table.EntryWidth != 0 {
			return fmt.Errorf("dispatch: trace event %d register X=%d not aligned to entry width %d", ev.ID, x, table.EntryWidth)
		}
		selector := table.MinSelector + (x / table.EntryWidth)

		traceEvents = append(traceEvents, TraceEvent{
			EventID:       ev.ID,
			JumpAddress:   eventPC,
			Selector:      selector,
			TargetAddress: targetAddr,
		})
	}

	return e.Correlate(table, traceEvents)
}

// Recover is a convenience function that detects, validates, and correlates a dispatch table.
func Recover(rom []byte, site JumpSite, traces []TraceEvent) (*DispatchTable, error) {
	engine := NewEngine(rom)
	table, err := engine.RecoverTable(site)
	if err != nil {
		return nil, err
	}
	if len(traces) > 0 {
		if err := engine.Correlate(table, traces); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// extractBounds inspects basic block instructions preceding JMP ($abs,X) to identify
// the entry count from an immediate comparison instruction.
func extractBounds(preceding []recovery.Instruction) (int, error) {
	if len(preceding) == 0 {
		return 0, errors.New("unbounded dispatch table: no preceding instructions")
	}

	cmpIdx := -1
	for i := len(preceding) - 1; i >= 0; i-- {
		op := preceding[i].Opcode
		if op == 0xC9 || op == 0xE0 || op == 0xC0 {
			cmpIdx = i
			break
		}
	}
	if cmpIdx == -1 {
		return 0, errors.New("unbounded dispatch table: no bounding comparison found")
	}

	cmpInst := preceding[cmpIdx]
	raw, err := hex.DecodeString(cmpInst.Bytes)
	if err != nil || len(raw) < 2 {
		return 0, errors.New("failed to decode comparison immediate operand")
	}

	var imm uint16
	if len(raw) == 2 {
		imm = uint16(raw[1])
	} else {
		imm = binary.LittleEndian.Uint16(raw[1:3])
	}
	if imm == 0 {
		return 0, errors.New("zero bound in comparison")
	}

	aslIdx := -1
	for i := len(preceding) - 1; i >= 0; i-- {
		op := preceding[i].Opcode
		if op == 0x0A || op == 0x0E || op == 0x06 {
			aslIdx = i
			break
		}
	}

	if aslIdx != -1 {
		if aslIdx < cmpIdx {
			return int(imm / 2), nil
		}
		return int(imm), nil
	}

	if cmpInst.Opcode == 0xE0 {
		return int(imm / 2), nil
	}

	if imm%2 == 0 {
		return int(imm / 2), nil
	}
	return int(imm), nil
}
