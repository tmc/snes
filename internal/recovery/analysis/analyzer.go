package analysis

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

// LoROMToOffset maps a 24-bit SNES bus address to a physical ROM offset.
func LoROMToOffset(addr uint32, romLen int) (uint32, bool) {
	bank := (addr >> 16) & 0xFF
	bankOffset := addr & 0xFFFF

	if bankOffset < 0x8000 {
		return 0, false // RAM or hardware register
	}
	if bank == 0x7E || bank == 0x7F {
		return 0, false // WRAM
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

// AnalyzeLoROM performs conservative static analysis starting from the LoROM reset vector.
func AnalyzeLoROM(rom []byte, doc *recovery.Document, cfg Config) (*Result, error) {
	if len(rom) < 32*1024 {
		return nil, errors.New("analysis: rom too small for LoROM")
	}
	if cfg.MaxInstructions <= 0 {
		cfg.MaxInstructions = 5000
	}

	// 1. Read Emulation Reset Vector from header ($7FC0 + $3C = $7FFC).
	headerOffset := 0x7FC0
	if len(rom) < headerOffset+0x40 {
		return nil, errors.New("analysis: rom missing vector table")
	}

	resetWord := binary.LittleEndian.Uint16(rom[headerOffset+0x3C:])
	if resetWord < 0x8000 {
		return nil, fmt.Errorf("analysis: reset vector $00:%04X points below $8000", resetWord)
	}

	resetAddr := uint32(resetWord)
	resetOffset, ok := LoROMToOffset(resetAddr, len(rom))
	if !ok {
		return nil, fmt.Errorf("analysis: reset vector $00:%04X cannot be mapped to ROM", resetWord)
	}

	res := &Result{
		ResetAddress: resetAddr,
		ResetOffset:  resetOffset,
	}

	// 2. Initial execution context at power-on (Emulation mode).
	initialCtx := recovery.Context{
		E: "set",
		M: "set",
		X: "set",
		C: "unknown",
	}

	// 3. Worklist queue.
	queue := []WorkItem{
		{Address: resetAddr, Context: initialCtx},
	}

	visited := make(map[string]bool)
	instByOffset := make(map[uint32]recovery.Instruction)
	summaryCache := make(map[summaryKey]Summary)
	var edges []recovery.Edge
	var issues []recovery.Issue

	instructionCount := 0

	for len(queue) > 0 && instructionCount < cfg.MaxInstructions {
		item := queue[0]
		queue = queue[1:]

		stateKey := fmt.Sprintf("%06X-%s-%s-%s-%s", item.Address, item.Context.E, item.Context.M, item.Context.X, item.Context.C)
		if visited[stateKey] {
			continue
		}
		visited[stateKey] = true

		offset, ok := LoROMToOffset(item.Address, len(rom))
		if !ok {
			issues = append(issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-%06x", item.Address),
				Address:  item.Address,
				Reason:   "execution target outside mapped ROM space",
				Blocking: false,
			})
			continue
		}

		opcode := rom[offset]
		op := cpu.Opcodes[opcode]
		if op.Op == nil {
			issues = append(issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-%06x", item.Address),
				Offset:   offset,
				Address:  item.Address,
				Reason:   fmt.Sprintf("unrecognized opcode 0x%02X", opcode),
				Blocking: true,
			})
			continue
		}

		// Sizing determination
		size, variableM, variableX, err := determineInstructionSize(op, opcode, item.Context)
		if err != nil {
			issues = append(issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-%06x", item.Address),
				Offset:   offset,
				Address:  item.Address,
				Reason:   err.Error(),
				Blocking: true,
			})
			continue
		}

		if int(offset)+size > len(rom) {
			issues = append(issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-%06x", item.Address),
				Offset:   offset,
				Address:  item.Address,
				Reason:   "instruction extends past end of ROM",
				Blocking: true,
			})
			continue
		}

		instBytes := rom[offset : offset+uint32(size)]
		hexBytes := hex.EncodeToString(instBytes)

		// Context propagation
		nextCtx := item.Context
		applyFlagChanges(opcode, instBytes, &nextCtx)

		// Format operand and build instruction
		mnemonic, modeName := formatInstruction(op, opcode, instBytes, variableM, variableX)

		instID := computeInstructionID(doc.ROM.NormalizedSHA256, item.Address, offset, hexBytes, item.Context)
		inst := recovery.Instruction{
			ID:           instID,
			Architecture: "wdc65816",
			Address:      item.Address,
			Offset:       offset,
			Bytes:        hexBytes,
			Opcode:       opcode,
			Mnemonic:     mnemonic,
			Mode:         modeName,
			Context:      item.Context,
			Evidence:     []string{"derived"},
		}

		instByOffset[offset] = inst
		instructionCount++

		nextPC := item.Address + uint32(size)

		// Control flow
		switch opcode {
		case 0x60, 0x6B: // RTS, RTL
			// Routine return - ends linear trace.
		case 0x40: // RTI
			// Return from interrupt - ends linear trace.
		case 0xDB, 0xCB: // STP, WAI
			// Processor stop/wait.
		case 0x80: // BRA $rel8
			rel := int8(instBytes[1])
			target := uint32(int32(item.Address) + 2 + int32(rel))
			queue = append(queue, WorkItem{Address: target, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
				Kind:        "branch",
				Source:      instID,
				Destination: target,
				Evidence:    []string{"derived"},
			})
		case 0x82: // BRL $rel16
			rel := int16(binary.LittleEndian.Uint16(instBytes[1:3]))
			target := uint32(int32(item.Address) + 3 + int32(rel))
			queue = append(queue, WorkItem{Address: target, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
				Kind:        "branch",
				Source:      instID,
				Destination: target,
				Evidence:    []string{"derived"},
			})
		case 0x4C: // JMP $abs
			dest := (item.Address & 0xFF0000) | uint32(binary.LittleEndian.Uint16(instBytes[1:3]))
			queue = append(queue, WorkItem{Address: dest, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, dest),
				Kind:        "jump",
				Source:      instID,
				Destination: dest,
				Evidence:    []string{"derived"},
			})
		case 0x5C: // JML $long
			dest := uint32(instBytes[1]) | (uint32(instBytes[2]) << 8) | (uint32(instBytes[3]) << 16)
			queue = append(queue, WorkItem{Address: dest, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, dest),
				Kind:        "jump",
				Source:      instID,
				Destination: dest,
				Evidence:    []string{"derived"},
			})
		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(instBytes[1])
			target := uint32(int32(item.Address) + 2 + int32(rel))
			queue = append(queue, WorkItem{Address: target, Context: nextCtx})
			queue = append(queue, WorkItem{Address: nextPC, Context: nextCtx, Preceding: appendPreceding(item.Preceding, inst)})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
				Kind:        "branch",
				Source:      instID,
				Destination: target,
				Evidence:    []string{"derived"},
			})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, nextPC),
				Kind:        "fallthrough",
				Source:      instID,
				Destination: nextPC,
				Evidence:    []string{"derived"},
			})
		case 0x20: // JSR $abs
			target := (item.Address & 0xFF0000) | uint32(binary.LittleEndian.Uint16(instBytes[1:3]))
			queue = append(queue, WorkItem{Address: target, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
				Kind:        "call",
				Source:      instID,
				Destination: target,
				Evidence:    []string{"derived"},
			})
			summary, err := inferSummary(rom, opcode, target, nextCtx, summaryCache, make(map[uint32]bool), 0)
			if err == nil && summary.Known() {
				queue = append(queue, WorkItem{Address: nextPC, Context: summary.ReturnContext, Preceding: appendPreceding(item.Preceding, inst)})
				edges = append(edges, recovery.Edge{
					ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, nextPC),
					Kind:        "fallthrough",
					Source:      instID,
					Destination: nextPC,
					Evidence:    []string{"derived"},
				})
			} else {
				issues = append(issues, recovery.Issue{
					ID:       fmt.Sprintf("iss-%06x", item.Address),
					Offset:   offset,
					Address:  nextPC,
					Reason:   "call fallthrough return context not assumed",
					Blocking: false,
				})
			}
		case 0x22: // JSL $long
			target := uint32(instBytes[1]) | (uint32(instBytes[2]) << 8) | (uint32(instBytes[3]) << 16)
			queue = append(queue, WorkItem{Address: target, Context: nextCtx})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
				Kind:        "call",
				Source:      instID,
				Destination: target,
				Evidence:    []string{"derived"},
			})
			summary, err := inferSummary(rom, opcode, target, nextCtx, summaryCache, make(map[uint32]bool), 0)
			if err == nil && summary.Known() {
				queue = append(queue, WorkItem{Address: nextPC, Context: summary.ReturnContext, Preceding: appendPreceding(item.Preceding, inst)})
				edges = append(edges, recovery.Edge{
					ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, nextPC),
					Kind:        "fallthrough",
					Source:      instID,
					Destination: nextPC,
					Evidence:    []string{"derived"},
				})
			} else {
				issues = append(issues, recovery.Issue{
					ID:       fmt.Sprintf("iss-%06x", item.Address),
					Offset:   offset,
					Address:  nextPC,
					Reason:   "call fallthrough return context not assumed",
					Blocking: false,
				})
			}
		case 0x7C: // JMP ($abs,X) - Indexed Indirect Jump
			table, err := RecoverDispatchTable(rom, inst, item.Preceding)
			if err == nil && len(table.Targets) > 0 {
				seenTarget := make(map[uint32]bool)
				for _, target := range table.Targets {
					edges = append(edges, recovery.Edge{
						ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, target),
						Kind:        "dispatch",
						Source:      instID,
						Destination: target,
						Evidence:    []string{"derived"},
					})
					if !seenTarget[target] {
						seenTarget[target] = true
						queue = append(queue, WorkItem{Address: target, Context: nextCtx})
					}
				}
			} else {
				issues = append(issues, recovery.Issue{
					ID:       fmt.Sprintf("iss-%06x", item.Address),
					Offset:   offset,
					Address:  item.Address,
					Reason:   "indirect jump destination unresolved",
					Blocking: false,
				})
			}
		case 0x6C, 0xDC: // Indirect JMP/JML
			issues = append(issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-%06x", item.Address),
				Offset:   offset,
				Address:  item.Address,
				Reason:   "indirect jump destination unresolved",
				Blocking: false,
			})
		default:
			// Normal sequential execution
			queue = append(queue, WorkItem{Address: nextPC, Context: nextCtx, Preceding: appendPreceding(item.Preceding, inst)})
			edges = append(edges, recovery.Edge{
				ID:          fmt.Sprintf("edge-%06x-%06x", item.Address, nextPC),
				Kind:        "fallthrough",
				Source:      instID,
				Destination: nextPC,
				Evidence:    []string{"derived"},
			})
		}
	}

	// Collect and sort instructions deterministically by offset.
	var instList []recovery.Instruction
	for _, inst := range instByOffset {
		instList = append(instList, inst)
	}
	sort.Slice(instList, func(i, j int) bool {
		return instList[i].Offset < instList[j].Offset
	})

	res.Instructions = instList
	res.Edges = edges
	res.Issues = issues

	// Update document
	doc.Instructions = instList
	doc.Edges = edges
	doc.Issues = issues

	return res, nil
}

func determineInstructionSize(op cpu.Opcode, opcode byte, ctx recovery.Context) (int, bool, bool, error) {
	if op.Mode == cpu.AddrImm {
		// M-dependent immediates
		if isMDependent(opcode) {
			if ctx.M == "unknown" {
				return 0, false, false, errors.New("unknown M flag at immediate operand")
			}
			if ctx.M == "set" {
				return 2, true, false, nil // 8-bit
			}
			return 3, true, false, nil // 16-bit
		}
		// X-dependent immediates
		if isXDependent(opcode) {
			if ctx.X == "unknown" {
				return 0, false, false, errors.New("unknown X flag at immediate operand")
			}
			if ctx.X == "set" {
				return 2, false, true, nil // 8-bit
			}
			return 3, false, true, nil // 16-bit
		}
	}
	return int(op.Size), false, false, nil
}

func isMDependent(opcode byte) bool {
	switch opcode {
	case 0x09, 0x29, 0x49, 0x69, 0x89, 0xA9, 0xC9, 0xE9:
		return true
	default:
		return false
	}
}

func isXDependent(opcode byte) bool {
	switch opcode {
	case 0xA0, 0xA2, 0xC0, 0xE0:
		return true
	default:
		return false
	}
}

func applyFlagChanges(opcode byte, bytes []byte, ctx *recovery.Context) {
	if modifiesCarry(opcode) {
		ctx.C = "unknown"
	}
	switch opcode {
	case 0x18: // CLC
		ctx.C = "clear"
	case 0x38: // SEC
		ctx.C = "set"
	case 0xFB: // XCE
		oldC := ctx.C
		oldE := ctx.E
		ctx.E = oldC
		ctx.C = oldE
		if ctx.E == "set" {
			ctx.M = "set"
			ctx.X = "set"
		}
	case 0xC2: // REP #$imm
		if len(bytes) >= 2 {
			imm := bytes[1]
			if ctx.E != "set" {
				if imm&0x20 != 0 {
					if ctx.E == "clear" {
						ctx.M = "clear"
					} else {
						ctx.M = "unknown"
					}
				}
				if imm&0x10 != 0 {
					if ctx.E == "clear" {
						ctx.X = "clear"
					} else {
						ctx.X = "unknown"
					}
				}
			}
			if imm&0x01 != 0 {
				ctx.C = "clear"
			}
		}
	case 0xE2: // SEP #$imm
		if len(bytes) >= 2 {
			imm := bytes[1]
			if imm&0x20 != 0 {
				ctx.M = "set"
			}
			if imm&0x10 != 0 {
				ctx.X = "set"
			}
			if imm&0x01 != 0 {
				ctx.C = "set"
			}
		}
	case 0x28, 0x40: // PLP, RTI
		ctx.M = "unknown"
		ctx.X = "unknown"
		ctx.C = "unknown"
	}
}

func formatInstruction(op cpu.Opcode, opcode byte, bytes []byte, varM, varX bool) (string, string) {
	name := strings.ToLower(op.Name)
	modeStr := addressingModeName(op.Mode)

	if varM {
		if len(bytes) == 3 {
			name += ".w"
		} else {
			name += ".b"
		}
	} else if varX {
		if len(bytes) == 3 {
			name += ".w"
		} else {
			name += ".b"
		}
	}

	return name, modeStr
}

func addressingModeName(mode cpu.AddressingMode) string {
	switch mode {
	case cpu.AddrImpl:
		return "implied"
	case cpu.AddrAcc:
		return "accumulator"
	case cpu.AddrImm:
		return "immediate"
	case cpu.AddrAbs:
		return "absolute"
	case cpu.AddrAbsX:
		return "absolute_x"
	case cpu.AddrAbsY:
		return "absolute_y"
	case cpu.AddrDir:
		return "direct_page"
	case cpu.AddrDirX:
		return "direct_page_x"
	case cpu.AddrDirY:
		return "direct_page_y"
	case cpu.AddrInd:
		return "indirect"
	case cpu.AddrIndX:
		return "indirect_x"
	case cpu.AddrIndY:
		return "indirect_y"
	case cpu.AddrLong:
		return "long"
	case cpu.AddrLongX:
		return "long_x"
	case cpu.AddrSr:
		return "stack_relative"
	case cpu.AddrSrIndY:
		return "stack_relative_y"
	case cpu.AddrRel:
		return "relative"
	case cpu.AddrRelL:
		return "relative_long"
	case cpu.AddrDirInd:
		return "direct_indirect"
	case cpu.AddrDirIndL:
		return "direct_indirect_long"
	case cpu.AddrAbsInd:
		return "absolute_indirect"
	case cpu.AddrAbsIndX:
		return "absolute_indirect_x"
	case cpu.AddrAbsIndLong:
		return "absolute_indirect_long"
	case cpu.AddrBlock:
		return "block_move"
	case cpu.AddrDirIndLIdxY:
		return "direct_indirect_long_y"
	default:
		return "unknown"
	}
}

func computeInstructionID(romHash string, addr, offset uint32, hexBytes string, ctx recovery.Context) string {
	return recovery.ComputeInstructionID(romHash, addr, offset, hexBytes, ctx)
}

func appendPreceding(preceding []recovery.Instruction, inst recovery.Instruction) []recovery.Instruction {
	const maxPreceding = 16
	next := make([]recovery.Instruction, len(preceding)+1)
	copy(next, preceding)
	next[len(preceding)] = inst
	if len(next) > maxPreceding {
		next = next[len(next)-maxPreceding:]
	}
	return next
}
