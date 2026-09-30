package decomp

import (
	"encoding/hex"
	"fmt"
	"sort"
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
	// RefusalTargets stop before the target instruction and preserve its CPU boundary.
	// Other refusal flags (missing memory, stack bounds, or fuel) do not promise
	// a resumable instruction boundary.
	RefusalTargets map[uint32]string `json:"refusal_targets,omitempty"`
	CallSites      []uint32          `json:"call_sites,omitempty"`
}

// RegionManifest documents the identity, contracts, and blocks of a generated region.
type RegionManifest struct {
	SchemaVersion string                `json:"schema_version"`
	RegionID      string                `json:"region_id"`
	EntryPC       uint32                `json:"entry_pc"`
	EntryPCHex    string                `json:"entry_pc_hex"`
	ReturnPC      uint32                `json:"return_pc,omitempty"`
	ReturnPCHex   string                `json:"return_pc_hex,omitempty"`
	BlockCount    int                   `json:"block_count"`
	TotalInsn     int                   `json:"total_instructions"`
	BasicBlocks   []RegionBlockSummary  `json:"basic_blocks"`
	EntryContract RegionContractSummary `json:"entry_contract"`
	ExitContract  RegionExitSummary     `json:"exit_contract"`
	CSource       CSourceSummary        `json:"c_source"`
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

// DecodeRegionConfig configures region decoding options.
type DecodeRegionConfig struct {
	CodeBytes        []byte
	EntryAddr        uint32
	EntryCtx         recovery.Context
	PinnedROM        []byte
	ROMBaseAddr      uint32
	MaxSteps         int
	AllowInternalJSR bool
	RefusalTargets   map[uint32]string
}

// DecodeRegionFromBytes decodes code bytes into a RegionIR with configurable pinned ROM environment.
func DecodeRegionFromBytes(codeBytes []byte, entryAddr uint32, entryCtx recovery.Context, pinnedROM []byte, romBaseAddr uint32, maxSteps int) (*RegionIR, error) {
	return DecodeRegionWithConfig(DecodeRegionConfig{
		CodeBytes:   codeBytes,
		EntryAddr:   entryAddr,
		EntryCtx:    entryCtx,
		PinnedROM:   pinnedROM,
		ROMBaseAddr: romBaseAddr,
		MaxSteps:    maxSteps,
	})
}

// calcInstructionSize returns the byte length of an instruction given current M and X width flags.
func calcInstructionSize(opByte byte, op cpu.Opcode, m8, x8 bool, addr uint32) (int, error) {
	switch op.Mode {
	case cpu.AddrImm:
		if op.Name == "BIT" || op.Name == "LDA" || op.Name == "ADC" || op.Name == "SBC" ||
			op.Name == "AND" || op.Name == "ORA" || op.Name == "EOR" || op.Name == "CMP" {
			if m8 {
				return 2, nil
			}
			return 3, nil
		} else if op.Name == "LDX" || op.Name == "LDY" || op.Name == "CPX" || op.Name == "CPY" {
			if x8 {
				return 2, nil
			}
			return 3, nil
		} else if op.Name == "REP" || op.Name == "SEP" {
			return 2, nil
		}
		return 0, fmt.Errorf("decode region: unrecognized immediate opcode $%02X (%s) at $%06X", opByte, op.Name, addr)
	case cpu.AddrAbs, cpu.AddrAbsX, cpu.AddrAbsY, cpu.AddrAbsInd, cpu.AddrAbsIndX, cpu.AddrAbsIndLong, cpu.AddrInd:
		return 3, nil
	case cpu.AddrLong, cpu.AddrLongX:
		return 4, nil
	case cpu.AddrDir, cpu.AddrDirX, cpu.AddrDirY, cpu.AddrDirInd, cpu.AddrDirIndL, cpu.AddrDirIndLIdxY,
		cpu.AddrIndX, cpu.AddrIndY, cpu.AddrSr, cpu.AddrSrIndY:
		return 2, nil
	case cpu.AddrRel:
		return 2, nil
	case cpu.AddrRelL:
		return 3, nil
	case cpu.AddrBlock:
		return 3, nil
	case cpu.AddrImpl, cpu.AddrAcc:
		return 1, nil
	default:
		return 0, fmt.Errorf("decode region: unsupported addressing mode %v for opcode $%02X (%s) at $%06X", op.Mode, opByte, op.Name, addr)
	}
}

// DecodeRegionWithConfig decodes code bytes into a RegionIR with full configuration options.
func DecodeRegionWithConfig(cfg DecodeRegionConfig) (*RegionIR, error) {
	codeBytes := cfg.CodeBytes
	entryAddr := cfg.EntryAddr
	entryCtx := cfg.EntryCtx
	pinnedROM := cfg.PinnedROM
	romBaseAddr := cfg.ROMBaseAddr
	maxSteps := cfg.MaxSteps

	byteLen := len(codeBytes)
	if byteLen == 0 {
		return nil, fmt.Errorf("decode region: empty code bytes")
	}

	// Bank boundary check: 65816 bank is 64KB (offset $0000..$FFFF)
	bank := entryAddr & 0xFF0000
	entryOffset := entryAddr & 0xFFFF
	if uint64(entryOffset)+uint64(byteLen) > 0x10000 {
		return nil, fmt.Errorf("decode region: region byte range $%06X..+$%04X crosses bank boundary", entryAddr, byteLen)
	}

	// 1. Validate entry context
	if entryCtx.E != "clear" && entryCtx.E != "0" {
		return nil, fmt.Errorf("decode region: unresolved entry context E: %q (native mode required)", entryCtx.E)
	}
	if entryCtx.M != "set" && entryCtx.M != "clear" && entryCtx.M != "1" && entryCtx.M != "0" {
		return nil, fmt.Errorf("decode region: unresolved entry context M: %q", entryCtx.M)
	}
	if entryCtx.X != "set" && entryCtx.X != "clear" && entryCtx.X != "1" && entryCtx.X != "0" {
		return nil, fmt.Errorf("decode region: unresolved entry context X: %q", entryCtx.X)
	}
	// 2. Decode instruction stream via control-flow worklist
	type decodedInsn struct {
		inst     recovery.Instruction
		addr     uint32
		size     int
		ctx      recovery.Context
		isBranch bool
		target   uint32
		isReturn bool
	}

	type workItem struct {
		addr uint32
		ctx  recovery.Context
	}

	visitedCtx := make(map[uint32]recovery.Context)
	insnByAddr := make(map[uint32]*decodedInsn)
	byteOwner := make(map[uint32]uint32)
	branchTargets := make(map[uint32]bool)
	branchTargets[entryAddr] = true

	callContexts := &callContextAnalyzer{code: codeBytes, entry: entryAddr, cfg: cfg, cache: make(map[callContextKey]callContextWidths), active: make(map[uint32]bool), remaining: maxCallContextInstructions}
	var callSites []uint32
	worklist := []workItem{{addr: entryAddr, ctx: entryCtx}}

	for len(worklist) > 0 {
		cur := worklist[0]
		worklist = worklist[1:]
		addr := cur.addr
		ctx := cur.ctx

		if cfg.RefusalTargets[addr] != "" {
			continue
		}
		// 1. Boundary check: must be inside region bytes
		if addr < entryAddr || addr >= entryAddr+uint32(byteLen) {
			return nil, fmt.Errorf("decode region: control flow targets address $%06X outside region range [$%06X..$%06X)",
				addr, entryAddr, entryAddr+uint32(byteLen))
		}
		if (addr >> 16) != (bank >> 16) {
			return nil, fmt.Errorf("decode region: address $%06X crosses bank boundary", addr)
		}

		// 2. Conflict check at joins
		if prevCtx, seen := visitedCtx[addr]; seen {
			prevM := prevCtx.M == "set" || prevCtx.M == "1"
			prevX := prevCtx.X == "set" || prevCtx.X == "1"
			curM := ctx.M == "set" || ctx.M == "1"
			curX := ctx.X == "set" || ctx.X == "1"
			if prevM != curM || prevX != curX {
				return nil, fmt.Errorf("decode region: conflicting incoming contexts at $%06X: M=%v,X=%v vs M=%v,X=%v",
					addr, prevM, prevX, curM, curX)
			}
			continue
		}

		// 3. Target inside operand check
		if owner, owned := byteOwner[addr]; owned && owner != addr {
			return nil, fmt.Errorf("decode region: target address $%06X lands inside operand of instruction at $%06X",
				addr, owner)
		}

		// 4. Decode instruction at addr
		pc := addr - entryAddr
		opByte := codeBytes[pc]
		op := cpu.Opcodes[opByte]
		if op.Name == "" && opByte != 0x00 {
			return nil, fmt.Errorf("decode region: unrecognized opcode $%02X at offset +$%04X ($%06X)", opByte, pc, addr)
		}
		if name := unsupportedRegionControl(opByte, cfg.AllowInternalJSR); name != "" {
			return nil, fmt.Errorf("decode region: unsupported control opcode $%02X (%s) at $%06X", opByte, name, addr)
		}

		m8 := ctx.M == "set" || ctx.M == "1"
		x8 := ctx.X == "set" || ctx.X == "1"
		size, err := calcInstructionSize(opByte, op, m8, x8, addr)
		if err != nil {
			return nil, err
		}

		if int(pc)+size > byteLen {
			return nil, fmt.Errorf("decode region: instruction at offset +$%04X extends past region length", pc)
		}

		// Check bank boundary for the full instruction
		if uint64(addr&0xFFFF)+uint64(size) > 0x10000 {
			return nil, fmt.Errorf("decode region: instruction at $%06X with size %d crosses bank boundary", addr, size)
		}

		// 5. Overlap check with existing instructions
		for k := 1; k < size; k++ {
			bAddr := bank | uint32(uint16(addr)+uint16(k))
			if owner, owned := byteOwner[bAddr]; owned {
				return nil, fmt.Errorf("decode region: instruction at $%06X overlaps instruction at $%06X", addr, owner)
			}
		}

		// Claim bytes
		for k := 0; k < size; k++ {
			bAddr := bank | uint32(uint16(addr)+uint16(k))
			byteOwner[bAddr] = addr
		}
		visitedCtx[addr] = ctx

		instBytes := codeBytes[pc : pc+uint32(size)]
		if opByte == 0xE2 && instBytes[1]&0x08 != 0 {
			return nil, fmt.Errorf("decode region: unsupported decimal mode at $%06X", addr)
		}
		hexBytes := hex.EncodeToString(instBytes)

		// 6. Flag tracking for subsequent instructions
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
		isBranch := false
		target := uint32(0)
		isReturn := false

		switch opByte {
		case 0x60, 0x6B: // RTS, RTL
			isReturn = true

		case 0x20: // JSR abs
			if cfg.AllowInternalJSR {
				target16 := uint16(instBytes[1]) | (uint16(instBytes[2]) << 8)
				target = bank | uint32(target16)
				isBranch = true
				branchTargets[target] = true
				branchTargets[fallthroughAddr] = true
				callSites = append(callSites, fallthroughAddr)

				retCtx, err := callContexts.returnContext(target, nextCtx)
				if err != nil {
					return nil, fmt.Errorf("decode region: callee $%06X return context: %w", target, err)
				}

				worklist = append(worklist, workItem{addr: target, ctx: nextCtx})
				worklist = append(worklist, workItem{addr: fallthroughAddr, ctx: retCtx})
			}

		case 0x80: // BRA rel8
			rel := int8(instBytes[1])
			target16 := uint16(int32(uint16(addr+2)) + int32(rel))
			target = bank | uint32(target16)
			isBranch = true
			branchTargets[target] = true
			worklist = append(worklist, workItem{addr: target, ctx: nextCtx})

		case 0x82: // BRL rel16
			rel16 := int16(uint16(instBytes[1]) | (uint16(instBytes[2]) << 8))
			target16 := uint16(int32(uint16(addr+3)) + int32(rel16))
			target = bank | uint32(target16)
			isBranch = true
			branchTargets[target] = true
			worklist = append(worklist, workItem{addr: target, ctx: nextCtx})

		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(instBytes[1])
			target16 := uint16(int32(uint16(addr+2)) + int32(rel))
			target = bank | uint32(target16)
			isBranch = true
			branchTargets[fallthroughAddr] = true
			worklist = append(worklist, workItem{addr: fallthroughAddr, ctx: nextCtx})
			if cfg.RefusalTargets != nil && cfg.RefusalTargets[target] != "" {
				// Refusal target boundary: do not traverse into target
			} else {
				branchTargets[target] = true
				worklist = append(worklist, workItem{addr: target, ctx: nextCtx})
			}

		default:
			worklist = append(worklist, workItem{addr: fallthroughAddr, ctx: nextCtx})
		}

		insnByAddr[addr] = &decodedInsn{
			inst: recovery.Instruction{
				ID:           fmt.Sprintf("inst-%06x", addr),
				Architecture: "wdc65816",
				Address:      addr,
				Offset:       pc,
				Bytes:        hexBytes,
				Opcode:       opByte,
				Mnemonic:     op.Name,
				Context:      ctx,
			},
			addr:     addr,
			size:     size,
			ctx:      ctx,
			isBranch: isBranch,
			target:   target,
			isReturn: isReturn,
		}
	}

	// Sort instructions by address
	var addrs []uint32
	for a := range insnByAddr {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })

	var insns []*decodedInsn
	for _, a := range addrs {
		insns = append(insns, insnByAddr[a])
	}

	// 3. Mark block leaders
	isLeader := make(map[uint32]bool)
	isLeader[entryAddr] = true
	for t := range branchTargets {
		isLeader[t] = true
	}
	for i, dec := range insns {
		if dec.isReturn || dec.isBranch {
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

	// Wire block successors and verify terminators
	for i, b := range blocks {
		last := b.Instructions[len(b.Instructions)-1]
		lastBytes, _ := hex.DecodeString(last.Bytes)
		bank := last.Address & 0xFF0000
		fallthroughAddr := bank | uint32(uint16(last.Address)+uint16(len(lastBytes)))
		op := last.Opcode

		switch op {
		case 0x60, 0x6B: // RTS, RTL
			// Exits region

		case 0x20: // JSR abs
			if cfg.AllowInternalJSR {
				target16 := uint16(lastBytes[1]) | (uint16(lastBytes[2]) << 8)
				target := bank | uint32(target16)
				b.Successors = append(b.Successors, target)
			}

		case 0x80: // BRA rel8
			rel := int8(lastBytes[1])
			target16 := uint16(int32(uint16(fallthroughAddr)) + int32(rel))
			target := bank | uint32(target16)
			b.Successors = append(b.Successors, target)

		case 0x82: // BRL rel16
			rel16 := int16(uint16(lastBytes[1]) | (uint16(lastBytes[2]) << 8))
			target16 := uint16(int32(uint16(fallthroughAddr)) + int32(rel16))
			target := bank | uint32(target16)
			b.Successors = append(b.Successors, target)

		case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // Conditional branches
			rel := int8(lastBytes[1])
			target16 := uint16(int32(uint16(fallthroughAddr)) + int32(rel))
			target := bank | uint32(target16)
			b.Successors = append(b.Successors, target, fallthroughAddr)

		default: // Sequential fallthrough
			if cfg.RefusalTargets[fallthroughAddr] != "" {
				b.Successors = append(b.Successors, fallthroughAddr)
			} else if i+1 < len(blocks) && blocks[i+1].StartAddress == fallthroughAddr {
				b.Successors = append(b.Successors, fallthroughAddr)
			} else {
				return nil, fmt.Errorf("decode region: block %s at $%06X falls off without a valid terminator or successor",
					b.ID, b.StartAddress)
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

	region := &RegionIR{
		ID:             fmt.Sprintf("region-%06x", entryAddr),
		Name:           fmt.Sprintf("sub_%06x", entryAddr),
		EntryAddress:   entryAddr,
		EntryContext:   entryCtx,
		Blocks:         liftedBlocks,
		MaxSteps:       maxSteps,
		ROMBaseAddr:    romBaseAddr,
		ROMBytes:       pinnedROM,
		RefusalTargets: cfg.RefusalTargets,
		CallSites:      callSites,
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
		if b == nil {
			return "", fmt.Errorf("generate region C: nil basic block")
		}
		if (b.EntryContext.M != "set" && b.EntryContext.M != "clear" && b.EntryContext.M != "1" && b.EntryContext.M != "0") ||
			(b.EntryContext.X != "set" && b.EntryContext.X != "clear" && b.EntryContext.X != "1" && b.EntryContext.X != "0") {
			return "", fmt.Errorf("generate region C: unresolved block widths at $%06X", b.StartAddress)
		}
		internalAddrs[b.StartAddress] = true
	}
	for _, pc := range region.CallSites {
		if !internalAddrs[pc] {
			return "", fmt.Errorf("generate region C: missing call continuation $%06X", pc)
		}
	}

	jump := func(target uint32) string {
		if region.RefusalTargets[target] != "" {
			return fmt.Sprintf("res.uninitialized_read = true; res.uninitialized_addr = 0x%06X; res.has_next = true; res.next_pc = 0x%06X; goto region_exit;", target, target)
		}
		if internalAddrs[target] {
			return fmt.Sprintf("goto block_%06x;", target)
		}
		return fmt.Sprintf("res.has_next = true; res.next_pc = 0x%06X; goto region_exit;", target)
	}
	stackGuard := func(addr string) string {
		if len(region.CallSites) == 0 {
			return ""
		}
		return fmt.Sprintf("        if ((%s) > 0x1FFF) { res.uninitialized_read = true; res.uninitialized_addr = (%s); goto region_exit; }\n", addr, addr)
	}
	var body strings.Builder
	if len(region.CallSites) > 0 {
		body.WriteString(fmt.Sprintf("    uint32_t call_returns[%d];\n    int call_depth = 0;\n", maxCallContextDepth))
	}

	// Entry contract check
	body.WriteString("    /* Strict entry contract enforcement */\n")
	if len(region.CallSites) > 0 {
		body.WriteString(fmt.Sprintf("    if (init_state.pc != 0x%04X || init_state.pb != 0x%02X) {\n", region.EntryAddress&0xFFFF, region.EntryAddress>>16))
		body.WriteString(fmt.Sprintf("        res.uninitialized_read = true; res.uninitialized_addr = 0x%06X;\n", region.EntryAddress))
		body.WriteString("        res.state = init_state; res.has_next = true; res.next_pc = ((uint32_t)init_state.pb << 16) | init_state.pc;\n        return res;\n    }\n")
	}
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
	if len(region.CallSites) > 0 {
		body.WriteString("        res.state = init_state; res.has_next = true; res.next_pc = ((uint32_t)init_state.pb << 16) | init_state.pc;\n")
	}
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

		if len(region.CallSites) > 0 {
			expected := uint8(0)
			if context8(block.EntryContext.M) {
				expected |= 0x20
			}
			if context8(block.EntryContext.X) {
				expected |= 0x10
			}
			body.WriteString(fmt.Sprintf("        if ((s.p & 0x30) != 0x%02X || s.e) { res.uninitialized_read = true; res.uninitialized_addr = 0x%06X; res.has_next = true; res.next_pc = 0x%06X; goto region_exit; }\n", expected, block.StartAddress, block.StartAddress))
		}
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
					helper := "mem_write16"
					if stmt.WordAddressing == WordBankZero16 {
						helper = "mem_write16_bank0"
					}
					body.WriteString(fmt.Sprintf("        %s(&res, %s, (%s) & 0xFFFF);\n", helper, addrExpr, valExpr))
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
				targetBranch := jump(stmt.TargetAddr)
				fallthroughBranch := jump(stmt.FallthroughAddr)
				body.WriteString(fmt.Sprintf("        if (%s) {\n            %s\n        } else {\n            %s\n        }\n",
					cond, targetBranch, fallthroughBranch))

			case "jump":
				hasTerminator = true
				body.WriteString("        " + jump(stmt.TargetAddr) + "\n")

			case "push_reg":
				var valStr string
				switch stmt.TargetReg {
				case RegDB:
					valStr = "s.db"
				case RegPB:
					valStr = "s.pb"
				default:
					valStr = fmt.Sprintf("s.%s", strings.ToLower(string(stmt.TargetReg)))
				}
				body.WriteString(stackGuard("(uint32_t)s.s"))
				body.WriteString(fmt.Sprintf("        mem_write8(&res, (uint32_t)s.s, (uint8_t)%s);\n", valStr))
				body.WriteString("        s.s = (s.s - 1) & 0xFFFF;\n")

			case "pull_reg":
				var regStr string
				switch stmt.TargetReg {
				case RegDB:
					regStr = "s.db"
				case RegPB:
					regStr = "s.pb"
				default:
					regStr = fmt.Sprintf("s.%s", strings.ToLower(string(stmt.TargetReg)))
				}
				body.WriteString("        s.s = (s.s + 1) & 0xFFFF;\n")
				body.WriteString(stackGuard("(uint32_t)s.s"))
				body.WriteString(fmt.Sprintf("        %s = read8((uint32_t)s.s);\n", regStr))
				if stmt.AffectsZ {
					body.WriteString(fmt.Sprintf("        if (%s == 0) s.p |= 0x02; else s.p &= ~0x02;\n", regStr))
				}
				if stmt.AffectsN {
					body.WriteString(fmt.Sprintf("        if (%s & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n", regStr))
				}

			case "call":
				hasTerminator = true
				retPC := (stmt.FallthroughAddr - 1) & 0xFFFF
				if len(region.CallSites) == 0 {
					return "", fmt.Errorf("generate region C: call lacks continuation metadata at $%06X", stmt.Address)
				}
				body.WriteString("        {\n")
				body.WriteString(fmt.Sprintf("            if (call_depth >= %d) { res.uninitialized_read = true; res.uninitialized_addr = 0x%06X; res.has_next = true; res.next_pc = 0x%06X; goto region_exit; }\n", maxCallContextDepth, stmt.Address, stmt.Address))
				body.WriteString(fmt.Sprintf("            call_returns[call_depth++] = 0x%06X;\n", stmt.FallthroughAddr))
				body.WriteString(fmt.Sprintf("            uint16_t _ret_pc = (uint16_t)0x%04X;\n", retPC))
				body.WriteString(stackGuard("(uint32_t)s.s"))
				body.WriteString("            mem_write8(&res, (uint32_t)s.s, (uint8_t)(_ret_pc >> 8));\n")
				body.WriteString("            s.s = (s.s - 1) & 0xFFFF;\n")
				body.WriteString(stackGuard("(uint32_t)s.s"))
				body.WriteString("            mem_write8(&res, (uint32_t)s.s, (uint8_t)(_ret_pc & 0xFF));\n")
				body.WriteString("            s.s = (s.s - 1) & 0xFFFF;\n")
				if internalAddrs[stmt.TargetAddr] {
					body.WriteString(fmt.Sprintf("            goto block_%06x;\n", stmt.TargetAddr))
				} else {
					body.WriteString(fmt.Sprintf("            res.has_next = true;\n            res.next_pc = 0x%06X;\n            goto region_exit;\n", stmt.TargetAddr))
				}
				body.WriteString("        }\n")

			case "return":
				hasTerminator = true
				if stmt.TargetTemp == "rts" {
					body.WriteString("        {\n")
					body.WriteString("            uint32_t _s1 = ((uint32_t)s.s + 1) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s2 = ((uint32_t)s.s + 2) & 0xFFFF;\n")
					body.WriteString(stackGuard("_s1"))
					body.WriteString("            uint8_t _lo = read8(_s1);\n")
					body.WriteString(stackGuard("_s2"))
					body.WriteString("            uint8_t _hi = read8(_s2);\n")
					body.WriteString("            s.s = (uint16_t)_s2;\n")
					body.WriteString("            s.pc = (uint16_t)((((uint16_t)_hi << 8) | _lo) + 1);\n")
					if len(region.CallSites) > 0 {
						body.WriteString("            uint32_t _ret_target = ((uint32_t)s.pb << 16) | s.pc;\n")
						body.WriteString("            res.has_next = true; res.next_pc = _ret_target;\n")
						body.WriteString("            if (call_depth == 0) goto region_exit;\n")
						body.WriteString("            if (_ret_target != call_returns[call_depth - 1]) { res.uninitialized_read = true; res.uninitialized_addr = _ret_target; goto region_exit; }\n")
						body.WriteString("            call_depth--;\n")
						body.WriteString("            switch (_ret_target) {\n")
						for _, cs := range region.CallSites {
							body.WriteString(fmt.Sprintf("            case 0x%06X: goto block_%06x;\n", cs, cs))
						}
						body.WriteString("            default:\n")
						body.WriteString("                res.uninitialized_read = true; res.uninitialized_addr = _ret_target;\n")
						body.WriteString("                res.has_next = true;\n")
						body.WriteString("                res.next_pc = _ret_target;\n")
						body.WriteString("                goto region_exit;\n")
						body.WriteString("            }\n")
					} else {
						body.WriteString("            res.has_next = true;\n")
						body.WriteString("            res.next_pc = ((uint32_t)s.pb << 16) | s.pc;\n")
						body.WriteString("            goto region_exit;\n")
					}
					body.WriteString("        }\n")
				} else if stmt.TargetTemp == "rtl" {
					body.WriteString("        {\n")
					if len(region.CallSites) > 0 {
						body.WriteString(fmt.Sprintf("            if (call_depth != 0) { res.uninitialized_read = true; res.uninitialized_addr = 0x%06X; res.has_next = true; res.next_pc = 0x%06X; goto region_exit; }\n", stmt.Address, stmt.Address))
					}
					body.WriteString("            uint32_t _s1 = ((uint32_t)s.s + 1) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s2 = ((uint32_t)s.s + 2) & 0xFFFF;\n")
					body.WriteString("            uint32_t _s3 = ((uint32_t)s.s + 3) & 0xFFFF;\n")
					body.WriteString(stackGuard("_s1"))
					body.WriteString("            uint8_t _lo = read8(_s1);\n")
					body.WriteString(stackGuard("_s2"))
					body.WriteString("            uint8_t _hi = read8(_s2);\n")
					body.WriteString(stackGuard("_s3"))
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
				body.WriteString("        " + jump(block.Successors[0]) + "\n")
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

static inline void mem_write16_bank0(exec_result_t *res, uint32_t addr, uint16_t val) {
    mem_write8(res, addr & 0xFFFF, (uint8_t)val);
    mem_write8(res, (addr + 1) & 0xFFFF, (uint8_t)(val >> 8));
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

static inline uint16_t mem_read16_bank0(exec_result_t *res, uint32_t addr, mem_read_fn read_cb, void *mem_ctx) {
    uint8_t low = mem_read8_raw(res, addr & 0xFFFF, read_cb, mem_ctx);
    uint8_t high = mem_read8_raw(res, (addr + 1) & 0xFFFF, read_cb, mem_ctx);
    return (uint16_t)low | ((uint16_t)high << 8);
}

exec_result_t execute_%s(cpu_state_t init_state, mem_read_fn read_cb, void *mem_ctx) {
    cpu_state_t s = init_state;
    exec_result_t res;
    memset(&res, 0, sizeof(res));

    #define read8(addr) mem_read8_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16(addr) mem_read16_raw(&res, (uint32_t)(addr), read_cb, mem_ctx)
    #define read16_bank0(addr) mem_read16_bank0(&res, (uint32_t)(addr), read_cb, mem_ctx)
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
    #undef read16_bank0
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
