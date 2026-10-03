package analysis

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

// Summary represents the conservative summary of a callee subroutine's return context
// and machine effects.
type Summary struct {
	// ReturnContext is the processor context upon returning from the subroutine.
	ReturnContext recovery.Context

	// PreservesDirectPage indicates whether the subroutine preserves the direct page register.
	PreservesDirectPage bool

	// StackDelta is the net stack pointer delta at return (0 for a balanced return).
	StackDelta int

	// ReturnsWith is the return opcode used by the subroutine (0x60 for RTS, 0x6B for RTL).
	ReturnsWith byte
}

// Known reports whether the summary provides a determinate return context
// sufficient for static decoding continuation (E, M, and X are known, direct page is
// preserved, and stack delta is zero).
func (s Summary) Known() bool {
	if !s.PreservesDirectPage || s.StackDelta != 0 {
		return false
	}
	if s.ReturnContext.E == "unknown" || s.ReturnContext.E == "" {
		return false
	}
	if s.ReturnContext.M == "unknown" || s.ReturnContext.M == "" {
		return false
	}
	if s.ReturnContext.X == "unknown" || s.ReturnContext.X == "" {
		return false
	}
	return true
}

type summaryKey struct {
	callOp byte
	target uint32
	ctx    recovery.Context
}

type stackItem int

const (
	stackItemOther stackItem = iota
	stackItemDP
)

const (
	maxCalleeDepth        = 16
	maxCalleeInstructions = 1000
)

// InferCallReturnSummary analyzes an acyclic callee subroutine reached by callOp (0x20 JSR or 0x22 JSL)
// at target address given caller entryCtx.
func InferCallReturnSummary(rom []byte, callOp byte, target uint32, entryCtx recovery.Context) (Summary, error) {
	cache := make(map[summaryKey]Summary)
	activeCallStack := make(map[uint32]bool)
	return inferSummary(rom, callOp, target, entryCtx, cache, activeCallStack, 0)
}

func inferSummary(
	rom []byte,
	callOp byte,
	target uint32,
	entryCtx recovery.Context,
	cache map[summaryKey]Summary,
	activeCallStack map[uint32]bool,
	depth int,
) (Summary, error) {
	if depth > maxCalleeDepth {
		return Summary{}, fmt.Errorf("analysis: callee recursion depth %d exceeded", depth)
	}
	if activeCallStack[target] {
		return Summary{}, fmt.Errorf("analysis: recursive call to $%06X detected", target)
	}

	var wantRet byte
	switch callOp {
	case 0x20: // JSR
		wantRet = 0x60 // RTS
	case 0x22: // JSL
		wantRet = 0x6B // RTL
	default:
		return Summary{}, fmt.Errorf("analysis: unsupported call opcode 0x%02X", callOp)
	}

	key := summaryKey{callOp: callOp, target: target, ctx: entryCtx}
	if cached, ok := cache[key]; ok {
		return cached, nil
	}

	activeCallStack[target] = true
	defer delete(activeCallStack, target)

	var returnContexts []recovery.Context
	onPath := make(map[uint32]bool)
	remainingSteps := maxCalleeInstructions

	var tracePath func(addr uint32, ctx recovery.Context, stack []stackItem, dpPreserved bool) error
	tracePath = func(addr uint32, ctx recovery.Context, stack []stackItem, dpPreserved bool) error {
		if remainingSteps <= 0 {
			return errors.New("analysis: instruction trace limit exceeded in callee")
		}
		remainingSteps--

		if onPath[addr] {
			return fmt.Errorf("analysis: cycle detected at $%06X", addr)
		}
		onPath[addr] = true
		defer delete(onPath, addr)

		offset, ok := LoROMToOffset(addr, len(rom))
		if !ok {
			return fmt.Errorf("analysis: address $%06X outside mapped ROM", addr)
		}

		opcode := rom[offset]
		op := cpu.Opcodes[opcode]
		if op.Op == nil {
			return fmt.Errorf("analysis: unrecognized opcode 0x%02X at $%06X", opcode, addr)
		}

		size, _, _, err := determineInstructionSize(op, opcode, ctx)
		if err != nil {
			return fmt.Errorf("analysis: size error at $%06X: %w", addr, err)
		}

		if int(offset)+size > len(rom) {
			return fmt.Errorf("analysis: instruction at $%06X extends past ROM", addr)
		}
		instBytes := rom[offset : offset+uint32(size)]

		// Check for return instructions
		if opcode == 0x60 || opcode == 0x6B {
			if opcode != wantRet {
				return fmt.Errorf("analysis: returned with opcode 0x%02X at $%06X, expected 0x%02X", opcode, addr, wantRet)
			}
			if len(stack) != 0 {
				return fmt.Errorf("analysis: unbalanced stack delta %d at return at $%06X", len(stack), addr)
			}
			if !dpPreserved {
				return fmt.Errorf("analysis: direct page not preserved at return at $%06X", addr)
			}
			returnContexts = append(returnContexts, ctx)
			return nil
		}

		// Check for non-returning or unsupported control-flow opcodes
		switch opcode {
		case 0x00, 0x02: // BRK, COP
			return fmt.Errorf("analysis: software interrupt opcode 0x%02X at $%06X", opcode, addr)
		case 0x40: // RTI
			return fmt.Errorf("analysis: unexpected RTI at $%06X", addr)
		case 0xDB, 0xCB: // STP, WAI
			return fmt.Errorf("analysis: processor stop/wait opcode 0x%02X at $%06X", opcode, addr)
		case 0x6C, 0x7C, 0xDC, 0xFC: // Indirect JMP / indirect JSR
			return fmt.Errorf("analysis: unresolved indirect control flow 0x%02X at $%06X", opcode, addr)
		}

		// Update flags
		nextCtx := ctx
		applyFlagChanges(opcode, instBytes, &nextCtx)
		if modifiesCarry(opcode) {
			nextCtx.C = "unknown"
		}

		// Update stack and direct page
		nextStack := stack
		nextDPPreserved := dpPreserved
		switch opcode {
		case 0x48: // PHA
			if nextCtx.E == "set" || nextCtx.M == "set" {
				nextStack = append(cloneStack(nextStack), stackItemOther)
			} else if nextCtx.M == "clear" {
				nextStack = append(cloneStack(nextStack), stackItemOther, stackItemOther)
			} else {
				return fmt.Errorf("analysis: unknown M flag at PHA at $%06X", addr)
			}
		case 0x68: // PLA
			bytes := 1
			if nextCtx.E == "clear" && nextCtx.M == "clear" {
				bytes = 2
			} else if nextCtx.E == "clear" && nextCtx.M == "unknown" {
				return fmt.Errorf("analysis: unknown M flag at PLA at $%06X", addr)
			}
			if len(nextStack) < bytes {
				return fmt.Errorf("analysis: stack underflow at PLA at $%06X", addr)
			}
			nextStack = cloneStack(nextStack[:len(nextStack)-bytes])
		case 0xDA: // PHX
			if nextCtx.E == "set" || nextCtx.X == "set" {
				nextStack = append(cloneStack(nextStack), stackItemOther)
			} else if nextCtx.X == "clear" {
				nextStack = append(cloneStack(nextStack), stackItemOther, stackItemOther)
			} else {
				return fmt.Errorf("analysis: unknown X flag at PHX at $%06X", addr)
			}
		case 0xFA: // PLX
			bytes := 1
			if nextCtx.E == "clear" && nextCtx.X == "clear" {
				bytes = 2
			} else if nextCtx.E == "clear" && nextCtx.X == "unknown" {
				return fmt.Errorf("analysis: unknown X flag at PLX at $%06X", addr)
			}
			if len(nextStack) < bytes {
				return fmt.Errorf("analysis: stack underflow at PLX at $%06X", addr)
			}
			nextStack = cloneStack(nextStack[:len(nextStack)-bytes])
		case 0x5A: // PHY
			if nextCtx.E == "set" || nextCtx.X == "set" {
				nextStack = append(cloneStack(nextStack), stackItemOther)
			} else if nextCtx.X == "clear" {
				nextStack = append(cloneStack(nextStack), stackItemOther, stackItemOther)
			} else {
				return fmt.Errorf("analysis: unknown X flag at PHY at $%06X", addr)
			}
		case 0x7A: // PLY
			bytes := 1
			if nextCtx.E == "clear" && nextCtx.X == "clear" {
				bytes = 2
			} else if nextCtx.E == "clear" && nextCtx.X == "unknown" {
				return fmt.Errorf("analysis: unknown X flag at PLY at $%06X", addr)
			}
			if len(nextStack) < bytes {
				return fmt.Errorf("analysis: stack underflow at PLY at $%06X", addr)
			}
			nextStack = cloneStack(nextStack[:len(nextStack)-bytes])
		case 0x08: // PHP
			nextStack = append(cloneStack(nextStack), stackItemOther)
		case 0x28: // PLP
			if len(nextStack) < 1 {
				return fmt.Errorf("analysis: stack underflow at PLP at $%06X", addr)
			}
			nextStack = cloneStack(nextStack[:len(nextStack)-1])
			nextCtx.M = "unknown"
			nextCtx.X = "unknown"
			nextCtx.C = "unknown"
		case 0x0B: // PHD
			dpItem := stackItemOther
			if nextDPPreserved {
				dpItem = stackItemDP
			}
			nextStack = append(cloneStack(nextStack), dpItem, dpItem)
		case 0x2B: // PLD
			if len(nextStack) < 2 {
				return fmt.Errorf("analysis: stack underflow at PLD at $%06X", addr)
			}
			i1 := nextStack[len(nextStack)-1]
			i2 := nextStack[len(nextStack)-2]
			nextStack = cloneStack(nextStack[:len(nextStack)-2])
			if i1 == stackItemDP && i2 == stackItemDP {
				nextDPPreserved = true
			} else {
				nextDPPreserved = false
			}
		case 0x8B: // PHB
			nextStack = append(cloneStack(nextStack), stackItemOther)
		case 0xAB: // PLB
			if len(nextStack) < 1 {
				return fmt.Errorf("analysis: stack underflow at PLB at $%06X", addr)
			}
			nextStack = cloneStack(nextStack[:len(nextStack)-1])
		case 0x4B: // PHK
			nextStack = append(cloneStack(nextStack), stackItemOther)
		case 0xF4, 0xD4, 0x62: // PEA, PEI, PER
			nextStack = append(cloneStack(nextStack), stackItemOther, stackItemOther)
		case 0x5B: // TCD
			nextDPPreserved = false
		case 0x1B, 0x9A: // TCS, TXS
			return fmt.Errorf("analysis: unsupported stack manipulation opcode 0x%02X at $%06X", opcode, addr)
		}

		bank := addr & 0xFF0000
		pc16 := uint16(addr)
		nextPC := bank | uint32(pc16+uint16(size))

		// Dispatch control flow
		switch opcode {
		case 0x80: // BRA $rel8
			rel := int8(instBytes[1])
			target := bank | uint32(uint16(int32(pc16+2)+int32(rel)))
			return tracePath(target, nextCtx, nextStack, nextDPPreserved)
		case 0x82: // BRL $rel16
			rel16 := int16(binary.LittleEndian.Uint16(instBytes[1:3]))
			target := bank | uint32(uint16(int32(pc16+3)+int32(rel16)))
			return tracePath(target, nextCtx, nextStack, nextDPPreserved)
		case 0x4C: // JMP $abs
			target := bank | uint32(binary.LittleEndian.Uint16(instBytes[1:3]))
			return tracePath(target, nextCtx, nextStack, nextDPPreserved)
		case 0x5C: // JML $long
			target := uint32(instBytes[1]) | (uint32(instBytes[2]) << 8) | (uint32(instBytes[3]) << 16)
			return tracePath(target, nextCtx, nextStack, nextDPPreserved)
		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(instBytes[1])
			target := bank | uint32(uint16(int32(pc16+2)+int32(rel)))
			if err := tracePath(target, nextCtx, nextStack, nextDPPreserved); err != nil {
				return err
			}
			return tracePath(nextPC, nextCtx, nextStack, nextDPPreserved)
		case 0x20: // JSR $abs (nested call)
			target := bank | uint32(binary.LittleEndian.Uint16(instBytes[1:3]))
			nestedSummary, err := inferSummary(rom, opcode, target, nextCtx, cache, activeCallStack, depth+1)
			if err != nil || !nestedSummary.Known() {
				return fmt.Errorf("analysis: nested call to $%06X unresolved: %w", target, err)
			}
			return tracePath(nextPC, nestedSummary.ReturnContext, nextStack, nextDPPreserved)
		case 0x22: // JSL $long (nested call)
			target := uint32(instBytes[1]) | (uint32(instBytes[2]) << 8) | (uint32(instBytes[3]) << 16)
			nestedSummary, err := inferSummary(rom, opcode, target, nextCtx, cache, activeCallStack, depth+1)
			if err != nil || !nestedSummary.Known() {
				return fmt.Errorf("analysis: nested call to $%06X unresolved: %w", target, err)
			}
			return tracePath(nextPC, nestedSummary.ReturnContext, nextStack, nextDPPreserved)
		default:
			// Normal sequential execution
			return tracePath(nextPC, nextCtx, nextStack, nextDPPreserved)
		}
	}

	if err := tracePath(target, entryCtx, nil, true); err != nil {
		return Summary{}, err
	}

	if len(returnContexts) == 0 {
		return Summary{}, fmt.Errorf("analysis: no return paths reached in callee at $%06X", target)
	}

	// Merge all return contexts conservatively
	merged := returnContexts[0]
	for _, c := range returnContexts[1:] {
		if c.E != merged.E {
			merged.E = "unknown"
		}
		if c.M != merged.M {
			merged.M = "unknown"
		}
		if c.X != merged.X {
			merged.X = "unknown"
		}
		if c.C != merged.C {
			merged.C = "unknown"
		}
	}

	res := Summary{
		ReturnContext:       merged,
		PreservesDirectPage: true,
		StackDelta:          0,
		ReturnsWith:         wantRet,
	}
	cache[key] = res
	return res, nil
}

func cloneStack(s []stackItem) []stackItem {
	if len(s) == 0 {
		return nil
	}
	c := make([]stackItem, len(s))
	copy(c, s)
	return c
}

func modifiesCarry(opcode byte) bool {
	switch opcode {
	case 0x69, 0x65, 0x75, 0x6D, 0x7D, 0x79, 0x61, 0x71, 0x72, 0x67, 0x77, 0x6F, 0x7F, 0x63, 0x73, // ADC
		0xE9, 0xE5, 0xF5, 0xED, 0xFD, 0xF9, 0xE1, 0xF1, 0xF2, 0xE7, 0xF7, 0xEF, 0xFF, 0xE3, 0xF3, // SBC
		0xC9, 0xC5, 0xD5, 0xCD, 0xDD, 0xD9, 0xC1, 0xD1, 0xD2, 0xC7, 0xD7, 0xCF, 0xDF, 0xC3, 0xD3, // CMP
		0xE0, 0xE4, 0xEC,                                                                            // CPX
		0xC0, 0xC4, 0xCC,                                                                            // CPY
		0x0A, 0x06, 0x16, 0x0E, 0x1E,                                                                // ASL
		0x4A, 0x46, 0x56, 0x4E, 0x5E,                                                                // LSR
		0x2A, 0x26, 0x36, 0x2E, 0x3E,                                                                // ROL
		0x6A, 0x66, 0x76, 0x6E, 0x7E:                                                                // ROR
		return true
	default:
		return false
	}
}
