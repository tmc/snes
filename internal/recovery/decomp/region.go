package decomp

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// RegionIR represents a connected machine-semantic IR region composed of multiple basic blocks.
type RegionIR struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	EntryAddress  uint32           `json:"entry_address"`
	ReturnAddress uint32           `json:"return_address,omitempty"`
	EntryContext  recovery.Context `json:"entry_context"`
	Blocks        []*BlockIR       `json:"blocks"`
	MaxSteps      int              `json:"max_steps,omitempty"`
	ROMBaseAddr   uint32           `json:"rom_base_addr,omitempty"`
	ROMBytes      []byte           `json:"-"`
}

// RegionManifest documents the identity, contracts, and blocks of a generated region.
type RegionManifest struct {
	SchemaVersion string                 `json:"schema_version"`
	RegionID      string                 `json:"region_id"`
	EntryPC       uint32                 `json:"entry_pc"`
	EntryPCHex    string                 `json:"entry_pc_hex"`
	ReturnPC      uint32                 `json:"return_pc,omitempty"`
	ReturnPCHex   string                 `json:"return_pc_hex,omitempty"`
	BlockCount    int                    `json:"block_count"`
	TotalInsn     int                    `json:"total_instructions"`
	BasicBlocks   []RegionBlockSummary   `json:"basic_blocks"`
	EntryContract RegionContractSummary  `json:"entry_contract"`
	ExitContract  RegionExitSummary      `json:"exit_contract"`
	CSource       CSourceSummary         `json:"c_source"`
}

// RegionBlockSummary summarizes a basic block within a region.
type RegionBlockSummary struct {
	ID                  string   `json:"id"`
	Start               uint32   `json:"start"`
	StartHex            string   `json:"start_hex"`
	End                 uint32   `json:"end"`
	EndHex              string   `json:"end_hex"`
	Length              int      `json:"length"`
	TerminalInstruction string   `json:"terminal_instruction"`
	TerminalType        string   `json:"terminal_type"`
	Successors          []string `json:"successors"`
}

// RegionContractSummary describes requirements for entry.
type RegionContractSummary struct {
	Mode     string `json:"mode"`
	E        bool   `json:"e"`
	M        bool   `json:"m"`
	X        bool   `json:"x"`
	RequireD bool   `json:"require_d_zero"`
}

// RegionExitSummary describes expected exit register state constraints.
type RegionExitSummary struct {
	PCExact    uint32 `json:"pc_exact,omitempty"`
	PCExactHex string `json:"pc_exact_hex,omitempty"`
	SDelta     int    `json:"s_delta,omitempty"`
}

// CSourceSummary captures the source file identity and provenance.
type CSourceSummary struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Provenance string `json:"provenance"`
}

// DecodeRegion decodes a contiguous slice of instructions from ROM bytes starting at entryAddr,
// partitions them into basic blocks, and lifts each block into machine-semantic IR.
func DecodeRegion(rom []byte, entryAddr uint32, byteLen int, entryCtx recovery.Context) (*RegionIR, error) {
	if len(rom) < byteLen {
		return nil, fmt.Errorf("decode region: rom length %d < byteLen %d", len(rom), byteLen)
	}
	return DecodeRegionFromBytes(rom[:byteLen], entryAddr, entryCtx, rom[:byteLen], entryAddr, 50000)
}

// DecodeRegionFromBytes decodes code bytes into a RegionIR with configurable pinned ROM environment.
func DecodeRegionFromBytes(codeBytes []byte, entryAddr uint32, entryCtx recovery.Context, pinnedROM []byte, romBaseAddr uint32, maxSteps int) (*RegionIR, error) {
	byteLen := len(codeBytes)
	rom := codeBytes

	// 1. Validate entry context
	if entryCtx.E == "" || entryCtx.E == "unknown" {
		return nil, fmt.Errorf("decode region: unresolved entry context E")
	}
	if entryCtx.M == "" || entryCtx.M == "unknown" {
		return nil, fmt.Errorf("decode region: unresolved entry context M")
	}
	if entryCtx.X == "" || entryCtx.X == "unknown" {
		return nil, fmt.Errorf("decode region: unresolved entry context X")
	}

	currCtx := entryCtx
	m8 := currCtx.M == "set" || currCtx.M == "1"
	x8 := currCtx.X == "set" || currCtx.X == "1"

	// 2. Decode instruction stream
	type decodedInsn struct {
		inst     recovery.Instruction
		addr     uint32
		size     int
		ctx      recovery.Context
		isBranch bool
		target   uint32
	}

	var insns []decodedInsn
	pc := uint32(0)
	endPC := uint32(byteLen)

	branchTargets := make(map[uint32]bool)
	branchTargets[entryAddr] = true

	for pc < endPC {
		addr := entryAddr + pc
		opByte := rom[pc]
		op := cpu.Opcodes[opByte]
		if op.Name == "" && opByte != 0x00 {
			return nil, fmt.Errorf("decode region: unrecognized opcode $%02X at offset +$%04X ($%06X)", opByte, pc, addr)
		}

		size := 1
		switch op.Mode {
		case cpu.AddrImm:
			// Mode depends on M or X flag
			if (op.Name == "LDA" || op.Name == "ADC" || op.Name == "SBC" || op.Name == "AND" || op.Name == "ORA" || op.Name == "EOR" || op.Name == "CMP") && !m8 {
				size = 3
			} else if (op.Name == "LDX" || op.Name == "LDY" || op.Name == "CPX" || op.Name == "CPY") && !x8 {
				size = 3
			} else {
				size = 2
			}
		case cpu.AddrAbs, cpu.AddrAbsX, cpu.AddrAbsY:
			size = 3
		case cpu.AddrLong, cpu.AddrLongX:
			size = 4
		case cpu.AddrDir, cpu.AddrDirX, cpu.AddrDirY, cpu.AddrDirInd, cpu.AddrDirIndL, cpu.AddrDirIndLIdxY:
			size = 2
		case cpu.AddrRel:
			size = 2
		case cpu.AddrRelL:
			size = 3
		default:
			size = 1
		}

		if int(pc)+size > byteLen {
			return nil, fmt.Errorf("decode region: instruction at offset +$%04X extends past region length", pc)
		}

		instBytes := rom[pc : pc+uint32(size)]
		hexBytes := hex.EncodeToString(instBytes)

		isBranch := false
		target := uint32(0)
		if op.Mode == cpu.AddrRel && size >= 2 {
			rel := int8(instBytes[1])
			bank := addr & 0xFF0000
			next16 := uint16(addr) + 2
			target16 := uint16(int32(next16) + int32(rel))
			target = bank | uint32(target16)
			isBranch = true
			if target >= entryAddr && target < entryAddr+uint32(byteLen) {
				branchTargets[target] = true
			}
		}

		insnCtx := currCtx
		insns = append(insns, decodedInsn{
			inst: recovery.Instruction{
				ID:           fmt.Sprintf("inst-%06x", addr),
				Architecture: "wdc65816",
				Address:      addr,
				Offset:       pc,
				Bytes:        hexBytes,
				Opcode:       opByte,
				Mnemonic:     op.Name,
				Context:      insnCtx,
			},
			addr:     addr,
			size:     size,
			ctx:      insnCtx,
			isBranch: isBranch,
			target:   target,
		})

		// Track flag changes affecting subsequent instructions
		if opByte == 0xC2 && size >= 2 { // REP #imm
			imm := instBytes[1]
			if (imm & 0x20) != 0 {
				m8 = false
				currCtx.M = "clear"
			}
			if (imm & 0x10) != 0 {
				x8 = false
				currCtx.X = "clear"
			}
		} else if opByte == 0xE2 && size >= 2 { // SEP #imm
			imm := instBytes[1]
			if (imm & 0x20) != 0 {
				m8 = true
				currCtx.M = "set"
			}
			if (imm & 0x10) != 0 {
				x8 = true
				currCtx.X = "set"
			}
		}

		pc += uint32(size)
	}

	// 3. Mark block leaders
	isLeader := make(map[uint32]bool)
	isLeader[entryAddr] = true
	for t := range branchTargets {
		isLeader[t] = true
	}
	for i, dec := range insns {
		op := dec.inst.Opcode
		// Following branch, jump, or return is a leader
		if op == 0x60 || op == 0x6B || op == 0x80 || op == 0x82 || op == 0xD0 || op == 0xF0 ||
			op == 0x90 || op == 0xB0 || op == 0x10 || op == 0x30 || op == 0x4C || op == 0x5C {
			if i+1 < len(insns) {
				isLeader[insns[i+1].addr] = true
			}
		}
	}

	// 4. Partition into basic blocks
	var blocks []*structure.BasicBlock
	var currentBlock *structure.BasicBlock

	for _, dec := range insns {
		if isLeader[dec.addr] || currentBlock == nil {
			if currentBlock != nil && len(currentBlock.Instructions) > 0 {
				last := currentBlock.Instructions[len(currentBlock.Instructions)-1]
				lastBytes, _ := hex.DecodeString(last.Bytes)
				currentBlock.EndAddress = last.Address + uint32(len(lastBytes))
				blocks = append(blocks, currentBlock)
			}
			currentBlock = &structure.BasicBlock{
				ID:           fmt.Sprintf("bb-%06x", dec.addr),
				StartAddress: dec.addr,
				StartOffset:  dec.addr - entryAddr,
				Instructions: []recovery.Instruction{dec.inst},
			}
		} else {
			currentBlock.Instructions = append(currentBlock.Instructions, dec.inst)
		}
	}
	if currentBlock != nil && len(currentBlock.Instructions) > 0 {
		last := currentBlock.Instructions[len(currentBlock.Instructions)-1]
		lastBytes, _ := hex.DecodeString(last.Bytes)
		currentBlock.EndAddress = last.Address + uint32(len(lastBytes))
		blocks = append(blocks, currentBlock)
	}

	// Compute successors for each block
	blockByAddr := make(map[uint32]*structure.BasicBlock)
	for _, b := range blocks {
		blockByAddr[b.StartAddress] = b
	}

	for i, b := range blocks {
		last := b.Instructions[len(b.Instructions)-1]
		lastBytes, _ := hex.DecodeString(last.Bytes)
		bank := last.Address & 0xFF0000
		fallthroughAddr := bank | uint32(uint16(last.Address)+uint16(len(lastBytes)))
		op := last.Opcode

		switch op {
		case 0x60, 0x6B: // RTS, RTL
			// Exits region
		case 0x80: // BRA
			rel := int8(lastBytes[1])
			target16 := uint16(int32(uint16(fallthroughAddr)) + int32(rel))
			target := bank | uint32(target16)
			b.Successors = append(b.Successors, target)
		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(lastBytes[1])
			target16 := uint16(int32(uint16(fallthroughAddr)) + int32(rel))
			target := bank | uint32(target16)
			b.Successors = append(b.Successors, target, fallthroughAddr)
		default: // Fallthrough
			if i+1 < len(blocks) {
				b.Successors = append(b.Successors, blocks[i+1].StartAddress)
			}
		}
	}

	// 5. Lift blocks into BlockIR
	var liftedBlocks []*BlockIR
	for _, b := range blocks {
		bir, err := LiftBlock(b, b.Instructions[0].Context)
		if err != nil {
			return nil, fmt.Errorf("lift block %s: %w", b.ID, err)
		}
		liftedBlocks = append(liftedBlocks, bir)
	}

	if maxSteps <= 0 {
		maxSteps = 50000
	}
	if romBaseAddr == 0 {
		romBaseAddr = entryAddr
	}
	if pinnedROM == nil {
		pinnedROM = codeBytes
	}

	region := &RegionIR{
		ID:           fmt.Sprintf("region-%06x", entryAddr),
		Name:         fmt.Sprintf("sub_%06x", entryAddr),
		EntryAddress: entryAddr,
		EntryContext: entryCtx,
		Blocks:       liftedBlocks,
		MaxSteps:     maxSteps,
		ROMBaseAddr:  romBaseAddr,
		ROMBytes:     pinnedROM,
	}

	return region, nil
}

// GenerateRegionC translates a multi-block RegionIR into self-contained, standard-compliant C.
func GenerateRegionC(region *RegionIR) (string, error) {
	if region == nil {
		return "", fmt.Errorf("generate region C: nil RegionIR")
	}
	if len(region.Blocks) == 0 {
		return "", fmt.Errorf("generate region C: region has no basic blocks")
	}

	maxSteps := region.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 50000
	}

	internalAddrs := make(map[uint32]bool)
	for _, b := range region.Blocks {
		internalAddrs[b.StartAddress] = true
	}

	var body strings.Builder

	// Entry contract check
	body.WriteString("    /* Strict entry contract enforcement */\n")
	body.WriteString("    if (init_state.e || init_state.d != 0 || (init_state.p & 0x08) != 0 ||\n")
	body.WriteString("        (init_state.db > 0x3F && (init_state.db < 0x80 || init_state.db > 0xBF)) ||\n")
	body.WriteString("        init_state.s > 0x1FFD")
	if region.EntryContext.M == "set" || region.EntryContext.M == "1" {
		body.WriteString(" || !(init_state.p & 0x20)")
	} else if region.EntryContext.M == "clear" || region.EntryContext.M == "0" {
		body.WriteString(" || (init_state.p & 0x20)")
	}
	if region.EntryContext.X == "set" || region.EntryContext.X == "1" {
		body.WriteString(" || !(init_state.p & 0x10)")
	} else if region.EntryContext.X == "clear" || region.EntryContext.X == "0" {
		body.WriteString(" || (init_state.p & 0x10)")
	}
	body.WriteString(" ||\n        ((init_state.p & 0x10) && (init_state.x > 0xFF || init_state.y > 0xFF))) {\n")
	body.WriteString("        res.uninitialized_read = true;\n")
	body.WriteString("        if (init_state.d != 0) {\n")
	body.WriteString("            res.uninitialized_addr = (uint32_t)init_state.d + 0x8C;\n")
	body.WriteString("        } else if (init_state.db > 0x3F && (init_state.db < 0x80 || init_state.db > 0xBF)) {\n")
	body.WriteString("            res.uninitialized_addr = ((uint32_t)init_state.db << 16) | 0x8000;\n")
	body.WriteString("        } else {\n")
	body.WriteString("            res.uninitialized_addr = 0x008000;\n")
	body.WriteString("        }\n")
	body.WriteString("        return res;\n")
	body.WriteString("    }\n\n")

	body.WriteString(fmt.Sprintf("    goto block_%06x;\n\n", region.EntryAddress))

	for _, block := range region.Blocks {
		body.WriteString(fmt.Sprintf("block_%06x:;\n", block.StartAddress))
		body.WriteString("    {\n")
		body.WriteString("        if (++steps > MAX_STEPS) {\n")
		body.WriteString("            res.fuel_exhausted = true;\n")
		body.WriteString("            res.uninitialized_read = true;\n")
		body.WriteString("            res.uninitialized_addr = 0x008000;\n")
		body.WriteString("            goto region_exit;\n")
		body.WriteString("        }\n")

		hasTerminator := false
		for _, stmt := range block.Statements {
			body.WriteString(fmt.Sprintf("        /* $%06X: %s (%s) */\n", stmt.Address, stmt.Mnemonic, stmt.InstructionID))
			switch stmt.Kind {
			case "assign_reg":
				reg := strings.ToLower(string(stmt.TargetReg))
				if stmt.AffectsC && stmt.AffectsV {
					var operandStr string
					if bin, ok := stmt.Expr.(*BinaryExpr); ok {
						if innerBin, ok := bin.Left.(*BinaryExpr); ok {
							operandStr = exprToCompilableC(innerBin.Right, stmt.Width)
						}
						if operandStr != "" {
							if bin.Op == OpAdd || stmt.Mnemonic == "ADC" {
								if stmt.Width == Width8 {
									body.WriteString("        {\n")
									body.WriteString("            uint32_t _a = (uint32_t)(s.a & 0xFF);\n")
									body.WriteString(fmt.Sprintf("            uint32_t _m = (uint32_t)((%s) & 0xFF);\n", operandStr))
									body.WriteString("            uint32_t _c = (uint32_t)(s.p & 0x01);\n")
									body.WriteString("            uint32_t _r = _a + _m + _c;\n")
									body.WriteString("            uint8_t _res = (uint8_t)(_r & 0xFF);\n")
									body.WriteString("            s.a = (s.a & 0xFF00) | _res;\n")
									body.WriteString("            if (_r > 0xFF) s.p |= 0x01; else s.p &= ~0x01;\n")
									body.WriteString("            if (_res == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
									body.WriteString("            if (_res & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n")
									body.WriteString("            if (~(_a ^ _m) & (_a ^ _r) & 0x80) s.p |= 0x40; else s.p &= ~0x40;\n")
									body.WriteString("        }\n")
								} else {
									body.WriteString("        {\n")
									body.WriteString("            uint32_t _a = (uint32_t)s.a;\n")
									body.WriteString(fmt.Sprintf("            uint32_t _m = (uint32_t)((%s) & 0xFFFF);\n", operandStr))
									body.WriteString("            uint32_t _c = (uint32_t)(s.p & 0x01);\n")
									body.WriteString("            uint32_t _r = _a + _m + _c;\n")
									body.WriteString("            uint16_t _res = (uint16_t)(_r & 0xFFFF);\n")
									body.WriteString("            s.a = _res;\n")
									body.WriteString("            if (_r > 0xFFFF) s.p |= 0x01; else s.p &= ~0x01;\n")
									body.WriteString("            if (_res == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
									body.WriteString("            if (_res & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n")
									body.WriteString("            if (~(_a ^ _m) & (_a ^ _r) & 0x8000) s.p |= 0x40; else s.p &= ~0x40;\n")
									body.WriteString("        }\n")
								}
								break
							} else if bin.Op == OpSub || stmt.Mnemonic == "SBC" {
								if stmt.Width == Width8 {
									body.WriteString("        {\n")
									body.WriteString("            uint32_t _a = (uint32_t)(s.a & 0xFF);\n")
									body.WriteString(fmt.Sprintf("            uint32_t _m = (uint32_t)((%s) & 0xFF);\n", operandStr))
									body.WriteString("            uint32_t _c = (uint32_t)(s.p & 0x01);\n")
									body.WriteString("            uint32_t _r = _a + (~_m & 0xFF) + _c;\n")
									body.WriteString("            uint8_t _res = (uint8_t)(_r & 0xFF);\n")
									body.WriteString("            s.a = (s.a & 0xFF00) | _res;\n")
									body.WriteString("            if (_r > 0xFF) s.p |= 0x01; else s.p &= ~0x01;\n")
									body.WriteString("            if (_res == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
									body.WriteString("            if (_res & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n")
									body.WriteString("            if ((_a ^ _m) & (_a ^ _r) & 0x80) s.p |= 0x40; else s.p &= ~0x40;\n")
									body.WriteString("        }\n")
								} else {
									body.WriteString("        {\n")
									body.WriteString("            uint32_t _a = (uint32_t)s.a;\n")
									body.WriteString(fmt.Sprintf("            uint32_t _m = (uint32_t)((%s) & 0xFFFF);\n", operandStr))
									body.WriteString("            uint32_t _c = (uint32_t)(s.p & 0x01);\n")
									body.WriteString("            uint32_t _r = _a + (~_m & 0xFFFF) + _c;\n")
									body.WriteString("            uint16_t _res = (uint16_t)(_r & 0xFFFF);\n")
									body.WriteString("            s.a = _res;\n")
									body.WriteString("            if (_r > 0xFFFF) s.p |= 0x01; else s.p &= ~0x01;\n")
									body.WriteString("            if (_res == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
									body.WriteString("            if (_res & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n")
									body.WriteString("            if ((_a ^ _m) & (_a ^ _r) & 0x8000) s.p |= 0x40; else s.p &= ~0x40;\n")
									body.WriteString("        }\n")
								}
								break
							}
						}
					}
				}
				cExpr := exprToCompilableC(stmt.Expr, stmt.Width)
				if stmt.TargetReg == RegA && stmt.Width == Width8 {
					body.WriteString(fmt.Sprintf("        s.a = (s.a & 0xFF00) | ((%s) & 0xFF);\n", cExpr))
				} else if stmt.Width == Width8 {
					body.WriteString(fmt.Sprintf("        s.%s = (%s) & 0xFF;\n", reg, cExpr))
				} else {
					body.WriteString(fmt.Sprintf("        s.%s = (%s) & 0xFFFF;\n", reg, cExpr))
				}
				if stmt.AffectsZ {
					if stmt.Width == Width8 {
						body.WriteString(fmt.Sprintf("        if ((s.%s & 0xFF) == 0) s.p |= 0x02; else s.p &= ~0x02;\n", reg))
					} else {
						body.WriteString(fmt.Sprintf("        if ((s.%s & 0xFFFF) == 0) s.p |= 0x02; else s.p &= ~0x02;\n", reg))
					}
				}
				if stmt.AffectsN {
					if stmt.Width == Width8 {
						body.WriteString(fmt.Sprintf("        if (s.%s & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n", reg))
					} else {
						body.WriteString(fmt.Sprintf("        if (s.%s & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n", reg))
					}
				}

			case "store_mem":
				addrExpr := exprToCompilableC(stmt.MemAddress, Width24)
				valExpr := exprToCompilableC(stmt.Expr, stmt.Width)
				if stmt.Width == Width8 {
					body.WriteString(fmt.Sprintf("        mem_write8(&res, %s, (%s) & 0xFF);\n", addrExpr, valExpr))
				} else {
					body.WriteString(fmt.Sprintf("        mem_write16(&res, %s, (%s) & 0xFFFF);\n", addrExpr, valExpr))
				}

			case "update_flags":
				exprStr := exprToCompilableC(stmt.Expr, stmt.Width)
				if stmt.Width == Width8 {
					body.WriteString(fmt.Sprintf("        {\n            uint8_t _v = (uint8_t)(%s);\n", exprStr))
					if stmt.AffectsZ {
						body.WriteString("            if (_v == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
					}
					if stmt.AffectsN {
						body.WriteString("            if (_v & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n")
					}
					if stmt.AffectsC {
						if bin, ok := stmt.Expr.(*BinaryExpr); ok && bin.Op == OpSub {
							leftStr := exprToCompilableC(bin.Left, stmt.Width)
							rightStr := exprToCompilableC(bin.Right, stmt.Width)
							body.WriteString(fmt.Sprintf("            if ((uint8_t)(%s) >= (uint8_t)(%s)) s.p |= 0x01; else s.p &= ~0x01;\n", leftStr, rightStr))
						}
					}
					body.WriteString("        }\n")
				} else {
					body.WriteString(fmt.Sprintf("        {\n            uint16_t _v = (uint16_t)(%s);\n", exprStr))
					if stmt.AffectsZ {
						body.WriteString("            if (_v == 0) s.p |= 0x02; else s.p &= ~0x02;\n")
					}
					if stmt.AffectsN {
						body.WriteString("            if (_v & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n")
					}
					if stmt.AffectsC {
						if bin, ok := stmt.Expr.(*BinaryExpr); ok && bin.Op == OpSub {
							leftStr := exprToCompilableC(bin.Left, stmt.Width)
							rightStr := exprToCompilableC(bin.Right, stmt.Width)
							body.WriteString(fmt.Sprintf("            if ((uint16_t)(%s) >= (uint16_t)(%s)) s.p |= 0x01; else s.p &= ~0x01;\n", leftStr, rightStr))
						}
					}
					body.WriteString("        }\n")
				}

			case "set_flag":
				mask := flagMask(stmt.TargetFlag)
				if stmt.Expr != nil {
					exprStr := exprToCompilableC(stmt.Expr, stmt.Width)
					body.WriteString(fmt.Sprintf("        if (%s) s.p |= 0x%02X; else s.p &= ~0x%02X;\n", exprStr, mask, mask))
				} else if stmt.FlagVal {
					body.WriteString(fmt.Sprintf("        s.p |= 0x%02X; /* set %s */\n", mask, stmt.TargetFlag))
					if mask == 0x10 {
						body.WriteString("        s.x &= 0xFF;\n        s.y &= 0xFF;\n")
					}
				} else {
					body.WriteString(fmt.Sprintf("        s.p &= ~0x%02X; /* clear %s */\n", mask, stmt.TargetFlag))
				}

			case "clear_flag_mask":
				if cEx, ok := stmt.Expr.(*ConstExpr); ok {
					body.WriteString(fmt.Sprintf("        s.p &= ~0x%02X; /* clear flags */\n", cEx.Value))
				}

			case "set_flag_mask":
				if cEx, ok := stmt.Expr.(*ConstExpr); ok {
					body.WriteString(fmt.Sprintf("        s.p |= 0x%02X; /* set flags */\n", cEx.Value))
					if (cEx.Value & 0x10) != 0 {
						body.WriteString("        s.x &= 0xFF;\n        s.y &= 0xFF;\n")
					}
				}

			case "branch":
				hasTerminator = true
				cond := flagConditionC(stmt.Condition)
				targetBranch := fmt.Sprintf("goto block_%06x;", stmt.TargetAddr)
				if !internalAddrs[stmt.TargetAddr] {
					targetBranch = fmt.Sprintf("res.has_next = true; res.next_pc = 0x%06X; goto region_exit;", stmt.TargetAddr)
				}
				fallthroughBranch := fmt.Sprintf("goto block_%06x;", stmt.FallthroughAddr)
				if !internalAddrs[stmt.FallthroughAddr] {
					fallthroughBranch = fmt.Sprintf("res.has_next = true; res.next_pc = 0x%06X; goto region_exit;", stmt.FallthroughAddr)
				}
				body.WriteString(fmt.Sprintf("        if (%s) {\n            %s\n        } else {\n            %s\n        }\n",
					cond, targetBranch, fallthroughBranch))

			case "jump":
				hasTerminator = true
				if internalAddrs[stmt.TargetAddr] {
					body.WriteString(fmt.Sprintf("        goto block_%06x;\n", stmt.TargetAddr))
				} else {
					body.WriteString(fmt.Sprintf("        res.has_next = true;\n        res.next_pc = 0x%06X;\n        goto region_exit;\n", stmt.TargetAddr))
				}

			case "return":
				hasTerminator = true
				if stmt.TargetTemp == "rts" {
					body.WriteString("        {\n")
					body.WriteString("            uint32_t _s1 = ((uint32_t)s.s + 1) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s2 = ((uint32_t)s.s + 2) & 0xFFFF;\n")
					body.WriteString("            uint8_t _lo = read8(_s1);\n")
					body.WriteString("            uint8_t _hi = read8(_s2);\n")
					body.WriteString("            s.s = (uint16_t)_s2;\n")
					body.WriteString("            s.pc = (uint16_t)((((uint16_t)_hi << 8) | _lo) + 1);\n")
					body.WriteString("            res.has_next = true;\n")
					body.WriteString("            res.next_pc = ((uint32_t)s.pb << 16) | s.pc;\n")
					body.WriteString("            goto region_exit;\n")
					body.WriteString("        }\n")
				} else if stmt.TargetTemp == "rtl" {
					body.WriteString("        {\n")
					body.WriteString("            uint32_t _s1 = ((uint32_t)s.s + 1) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s2 = ((uint32_t)s.s + 2) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s3 = ((uint32_t)s.s + 3) & 0xFFFF;\n")
					body.WriteString("            uint8_t _lo = read8(_s1);\n")
					body.WriteString("            uint8_t _hi = read8(_s2);\n")
					body.WriteString("            uint8_t _pb = read8(_s3);\n")
					body.WriteString("            s.s = (uint16_t)_s3;\n")
					body.WriteString("            s.pb = _pb;\n")
					body.WriteString("            s.pc = (uint16_t)((((uint16_t)_hi << 8) | _lo) + 1);\n")
					body.WriteString("            res.has_next = true;\n")
					body.WriteString("            res.next_pc = ((uint32_t)s.pb << 16) | s.pc;\n")
					body.WriteString("            goto region_exit;\n")
					body.WriteString("        }\n")
				} else {
					body.WriteString("        res.has_next = false;\n        res.next_pc = 0; /* return */\n        goto region_exit;\n")
				}

			case "nop":
				body.WriteString("        /* nop */;\n")

			case "unsupported":
				return "", fmt.Errorf("cannot generate compilable C with unsupported instruction: %s", stmt.Reason)
			}
		}

		if !hasTerminator {
			if len(block.Successors) > 0 {
				succ := block.Successors[0]
				if internalAddrs[succ] {
					body.WriteString(fmt.Sprintf("        goto block_%06x;\n", succ))
				} else {
					body.WriteString(fmt.Sprintf("        res.next_pc = 0x%06X;\n        goto region_exit;\n", succ))
				}
			} else {
				body.WriteString("        goto region_exit;\n")
			}
		}

		body.WriteString("    }\n\n")
	}

	// Format pinned ROM bytes if present
	var romDecl strings.Builder
	romSize := len(region.ROMBytes)
	romDecl.WriteString(fmt.Sprintf("#define PINNED_ROM_SIZE %d\n", romSize))
	if romSize > 0 {
		romDecl.WriteString(fmt.Sprintf("static const uint8_t pinned_rom[%d] = {\n    ", romSize))
		for i, b := range region.ROMBytes {
			if i > 0 {
				if i%16 == 0 {
					romDecl.WriteString(",\n    ")
				} else {
					romDecl.WriteString(", ")
				}
			}
			romDecl.WriteString(fmt.Sprintf("0x%02X", b))
		}
		romDecl.WriteString("\n};\n")
	}

	template := `/* Machine-semantic C translation for region %s ($%06X) */
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdio.h>

#define MAX_WRITES 256
#define MAX_STEPS %d

typedef struct {
    uint16_t a;
    uint16_t x;
    uint16_t y;
    uint16_t s;
    uint16_t pc;
    uint16_t d;
    uint8_t db;
    uint8_t pb;
    uint8_t p;
    bool e;
} cpu_state_t;

typedef struct {
    uint32_t address;
    uint8_t value;
} mem_write_t;

typedef struct {
    cpu_state_t state;
    uint32_t next_pc;
    bool has_next;
    int num_writes;
    bool write_overflow;
    uint32_t total_writes;
    bool uninitialized_read;
    uint32_t uninitialized_addr;
    bool mmio_access;
    uint32_t mmio_addr;
    bool fuel_exhausted;
    mem_write_t writes[MAX_WRITES];
} exec_result_t;

typedef uint8_t (*mem_read_fn)(void *ctx, uint32_t addr, bool *missing);

static inline uint32_t bus_canonical_addr(uint32_t addr) {
    uint32_t a = addr & 0xFFFFFF;
    uint8_t bank = (uint8_t)((a >> 16) & 0xFF);
    uint16_t offset = (uint16_t)(a & 0xFFFF);
    /* In banks $00-$3F and $80-$BF, $0000-$1FFF mirrors WRAM $7E0000-$7E1FFF */
    if ((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000) {
        return 0x7E0000 | offset;
    }
    return a;
}

static inline bool is_mmio_addr(uint32_t addr) {
    uint32_t a = addr & 0xFFFFFF;
    uint8_t bank = (uint8_t)((a >> 16) & 0xFF);
    uint16_t offset = (uint16_t)(a & 0xFFFF);
    if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) {
        if ((offset >= 0x2100 && offset <= 0x21FF) || (offset >= 0x4200 && offset <= 0x43FF)) {
            return true;
        }
    }
    return false;
}

static inline void mem_write8(exec_result_t *res, uint32_t addr, uint8_t val) {
    uint32_t a = bus_canonical_addr(addr);
    if (is_mmio_addr(addr)) {
        res->mmio_access = true;
        res->mmio_addr = addr;
    }
    res->total_writes++;
    if (res->num_writes < MAX_WRITES) {
        res->writes[res->num_writes].address = a;
        res->writes[res->num_writes].value = val;
        res->num_writes++;
    } else {
        res->write_overflow = true;
    }
}

static inline void mem_write16(exec_result_t *res, uint32_t addr, uint16_t val) {
    mem_write8(res, addr, (uint8_t)(val & 0xFF));
    mem_write8(res, (addr + 1) & 0xFFFFFF, (uint8_t)((val >> 8) & 0xFF));
}

%s

static inline uint8_t mem_read8_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint32_t a = bus_canonical_addr(addr);
    if (is_mmio_addr(addr)) {
        res->mmio_access = true;
        res->mmio_addr = addr;
    }
    for (int i = res->num_writes - 1; i >= 0; i--) {
        if (res->writes[i].address == a) {
            return res->writes[i].value;
        }
    }
#if PINNED_ROM_SIZE > 0
    uint8_t bank = (uint8_t)((a >> 16) & 0xFF);
    uint16_t offset = (uint16_t)(a & 0xFFFF);
    if ((bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x8000) {
        uint32_t rom_off = ((uint32_t)(bank & 0x7F) * 0x8000) + (offset - 0x8000);
        if (rom_off < PINNED_ROM_SIZE) {
            return pinned_rom[rom_off];
        }
    }
#endif
    if (read_cb) {
        bool missing = false;
        uint8_t val = read_cb(mem_ctx, a, &missing);
        if (missing) {
            if (!res->uninitialized_read) {
                res->uninitialized_addr = a;
            }
            res->uninitialized_read = true;
        }
        return val;
    }
    if (!res->uninitialized_read) {
        res->uninitialized_addr = a;
    }
    res->uninitialized_read = true;
    return 0;
}

static inline uint16_t mem_read16_raw(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint8_t low = mem_read8_raw(res, addr, read_cb, mem_ctx);
    uint8_t high = mem_read8_raw(res, (addr + 1) & 0xFFFFFF, read_cb, mem_ctx);
    return (uint16_t)low | ((uint16_t)high << 8);
}

exec_result_t execute_%s(cpu_state_t init_state, mem_read_fn read_cb, void *mem_ctx) {
    cpu_state_t s = init_state;
    exec_result_t res;
    memset(&res, 0, sizeof(res));

    #define read8(addr) mem_read8_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16(addr) mem_read16_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define P_C ((s.p & 0x01) != 0)
    #define P_Z ((s.p & 0x02) != 0)
    #define P_I ((s.p & 0x04) != 0)
    #define P_D ((s.p & 0x08) != 0)
    #define P_X ((s.p & 0x10) != 0)
    #define P_M ((s.p & 0x20) != 0)
    #define P_V ((s.p & 0x40) != 0)
    #define P_N ((s.p & 0x80) != 0)

    int steps = 0;

%s

region_exit:
    #undef read8
    #undef read16
    #undef P_C
    #undef P_Z
    #undef P_I
    #undef P_D
    #undef P_X
    #undef P_M
    #undef P_V
    #undef P_N
    if (res.has_next) {
        s.pc = (uint16_t)(res.next_pc & 0xFFFF);
        s.pb = (uint8_t)((res.next_pc >> 16) & 0xFF);
    }
    res.state = s;
    return res;
}
`

	fnName := region.Name
	if fnName == "" {
		fnName = fmt.Sprintf("region_%06x", region.EntryAddress)
	}

	cCode := fmt.Sprintf(template, region.ID, region.EntryAddress, maxSteps, romDecl.String(), fnName, body.String())
	return cCode, nil
}
