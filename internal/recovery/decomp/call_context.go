package decomp

import (
	"fmt"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

const (
	maxCallContextDepth        = 64
	maxCallContextInstructions = 1 << 18
)

type callContextKey struct {
	entry  uint32
	m8, x8 bool
}
type callContextWidths struct{ m, x string }
type callContextAnalyzer struct {
	code      []byte
	entry     uint32
	cfg       DecodeRegionConfig
	cache     map[callContextKey]callContextWidths
	active    map[uint32]bool
	remaining int
}

func context8(flag string) bool { return flag == "set" || flag == "1" }

func unsupportedRegionControl(op byte, allowJSR bool) string {
	switch op {
	case 0x00:
		return "BRK"
	case 0x02:
		return "COP"
	case 0x20:
		if !allowJSR {
			return "JSR abs"
		}
	case 0x22:
		return "JSL long"
	case 0x28:
		return "PLP status restore"
	case 0x40:
		return "RTI"
	case 0x44:
		return "MVP"
	case 0x4C:
		return "JMP abs"
	case 0x54:
		return "MVN"
	case 0x5C:
		return "JMP long"
	case 0x6C:
		return "JMP (abs)"
	case 0x7C:
		return "JMP (abs,X)"
	case 0xCB:
		return "WAI"
	case 0xDB:
		return "STP"
	case 0xDC:
		return "JMP [abs]"
	case 0xFB:
		return "XCE mode change"
	case 0xFC:
		return "JSR (abs,X)"
	}
	return ""
}

// returnContext follows every reachable return and requires one M/X result.
// Recursion and unmodeled status changes are refused. Loops with no return
// cannot supply a caller continuation; runtime fuel still bounds looping paths.
func (a *callContextAnalyzer) returnContext(subEntry uint32, entryCtx recovery.Context) (recovery.Context, error) {
	codeBytes, regionEntry, cfg := a.code, a.entry, a.cfg
	if a.active[subEntry] {
		return recovery.Context{}, fmt.Errorf("recursive subroutine call to $%06X is not supported", subEntry)
	}
	if len(a.active) >= maxCallContextDepth {
		return recovery.Context{}, fmt.Errorf("callee context depth exceeds %d", maxCallContextDepth)
	}
	key := callContextKey{subEntry, context8(entryCtx.M), context8(entryCtx.X)}
	if widths, ok := a.cache[key]; ok {
		entryCtx.M, entryCtx.X = widths.m, widths.x
		return entryCtx, nil
	}
	a.active[subEntry] = true
	defer delete(a.active, subEntry)

	type subItem struct {
		addr uint32
		ctx  recovery.Context
	}
	subWork := []subItem{{addr: subEntry, ctx: entryCtx}}
	subVisited := make(map[uint32]recovery.Context)
	var rtsContexts []recovery.Context

	bank := regionEntry & 0xFF0000
	byteLen := len(codeBytes)

	for len(subWork) > 0 {
		item := subWork[0]
		subWork = subWork[1:]
		addr := item.addr
		ctx := item.ctx

		if cfg.RefusalTargets[addr] != "" {
			continue
		}
		if addr < regionEntry || int(addr-regionEntry) >= byteLen {
			return recovery.Context{}, fmt.Errorf("subroutine $%06X targets address $%06X outside region bounds", subEntry, addr)
		}

		if prevCtx, seen := subVisited[addr]; seen {
			prevM := prevCtx.M == "set" || prevCtx.M == "1"
			prevX := prevCtx.X == "set" || prevCtx.X == "1"
			curM := ctx.M == "set" || ctx.M == "1"
			curX := ctx.X == "set" || ctx.X == "1"
			if prevM != curM || prevX != curX {
				return recovery.Context{}, fmt.Errorf("conflicting contexts at $%06X in callee $%06X: M=%v,X=%v vs M=%v,X=%v",
					addr, subEntry, prevM, prevX, curM, curX)
			}
			continue
		}
		if a.remaining == 0 {
			return recovery.Context{}, fmt.Errorf("callee context analysis exceeds %d instructions", maxCallContextInstructions)
		}
		a.remaining--
		subVisited[addr] = ctx

		pc := addr - regionEntry
		opByte := codeBytes[pc]
		op := cpu.Opcodes[opByte]
		if reason := unsupportedRegionControl(opByte, cfg.AllowInternalJSR); reason != "" {
			return recovery.Context{}, fmt.Errorf("callee $%06X: unsupported %s at $%06X", subEntry, reason, addr)
		}
		if op.Name == "" && opByte != 0x00 {
			return recovery.Context{}, fmt.Errorf("unrecognized opcode $%02X at $%06X in callee $%06X", opByte, addr, subEntry)
		}

		m8 := ctx.M == "set" || ctx.M == "1"
		x8 := ctx.X == "set" || ctx.X == "1"
		size, err := calcInstructionSize(opByte, op, m8, x8, addr)
		if err != nil {
			return recovery.Context{}, err
		}

		if int(pc)+size > byteLen {
			return recovery.Context{}, fmt.Errorf("instruction at $%06X in callee $%06X extends past region length", addr, subEntry)
		}

		instBytes := codeBytes[pc : pc+uint32(size)]
		if opByte == 0xE2 && instBytes[1]&0x08 != 0 {
			return recovery.Context{}, fmt.Errorf("callee $%06X enables unsupported decimal mode at $%06X", subEntry, addr)
		}
		nextCtx := ctx
		if opByte == 0xC2 && size >= 2 { // REP #imm
			imm := instBytes[1]
			if (imm & 0x20) != 0 {
				nextCtx.M = "clear"
			}
			if (imm & 0x10) != 0 {
				nextCtx.X = "clear"
			}
		} else if opByte == 0xE2 && size >= 2 { // SEP #imm
			imm := instBytes[1]
			if (imm & 0x20) != 0 {
				nextCtx.M = "set"
			}
			if (imm & 0x10) != 0 {
				nextCtx.X = "set"
			}
		}

		fallthroughAddr := bank | uint32(uint16(addr)+uint16(size))

		switch opByte {
		case 0x60: // RTS
			rtsContexts = append(rtsContexts, nextCtx)

		case 0x6B: // RTL
			return recovery.Context{}, fmt.Errorf("callee $%06X exits via RTL ($%06X) rather than RTS to caller", subEntry, addr)

		case 0x20: // JSR abs (nested)
			if !cfg.AllowInternalJSR {
				return recovery.Context{}, fmt.Errorf("nested JSR at $%06X in callee $%06X with AllowInternalJSR=false", addr, subEntry)
			}
			target16 := uint16(instBytes[1]) | (uint16(instBytes[2]) << 8)
			nestedTarget := bank | uint32(target16)
			retCtx, err := a.returnContext(nestedTarget, nextCtx)
			if err != nil {
				return recovery.Context{}, fmt.Errorf("nested call to $%06X from $%06X: %w", nestedTarget, addr, err)
			}
			subWork = append(subWork, subItem{addr: fallthroughAddr, ctx: retCtx})

		case 0x80: // BRA rel8
			rel := int8(instBytes[1])
			t16 := uint16(int32(uint16(addr+2)) + int32(rel))
			subWork = append(subWork, subItem{addr: bank | uint32(t16), ctx: nextCtx})

		case 0x82: // BRL rel16
			rel16 := int16(uint16(instBytes[1]) | (uint16(instBytes[2]) << 8))
			t16 := uint16(int32(uint16(addr+3)) + int32(rel16))
			subWork = append(subWork, subItem{addr: bank | uint32(t16), ctx: nextCtx})

		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(instBytes[1])
			t16 := uint16(int32(uint16(addr+2)) + int32(rel))
			targetAddr := bank | uint32(t16)
			subWork = append(subWork, subItem{addr: fallthroughAddr, ctx: nextCtx})
			if cfg.RefusalTargets != nil && cfg.RefusalTargets[targetAddr] != "" {
				// Refusal branch: does not reach RTS
			} else {
				subWork = append(subWork, subItem{addr: targetAddr, ctx: nextCtx})
			}

		default:
			subWork = append(subWork, subItem{addr: fallthroughAddr, ctx: nextCtx})
		}
	}

	if len(rtsContexts) == 0 {
		return recovery.Context{}, fmt.Errorf("callee $%06X has no reachable RTS return path", subEntry)
	}

	first := rtsContexts[0]
	firstM := first.M == "set" || first.M == "1"
	firstX := first.X == "set" || first.X == "1"
	for _, c := range rtsContexts[1:] {
		cM := c.M == "set" || c.M == "1"
		cX := c.X == "set" || c.X == "1"
		if cM != firstM || cX != firstX {
			return recovery.Context{}, fmt.Errorf("divergent return context in callee $%06X: conflicting RTS contexts M=%v,X=%v vs M=%v,X=%v",
				subEntry, firstM, firstX, cM, cX)
		}
	}

	a.cache[key] = callContextWidths{first.M, first.X}
	entryCtx.M, entryCtx.X = first.M, first.X
	return entryCtx, nil
}
