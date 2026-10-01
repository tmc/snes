package decomp

import (
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// CodeSpan is a half-open physical ROM instruction range.
type CodeSpan struct{ Start, End uint32 }

// ConnectedConfig bounds decoding across ROM banks. IndirectTargets is an
// explicit allowlist for each indirect jump site, not an inferred dispatch proof.
// ROM is copied during decoding. Native mode and resolved M/X are required.
type ConnectedConfig struct {
	ROM                       []byte
	Spans                     []CodeSpan
	Entry                     uint32
	Context                   recovery.Context
	MaxInstructions, MaxSteps int
	IndirectTargets           map[uint32][]uint32
	RefusalTargets            map[uint32]string
}

type connectedFrame struct {
	resume    uint32
	opcode    byte
	remaining int
}
type connectedItem struct {
	address uint32
	context recovery.Context
	frames  []connectedFrame
}

// DecodeConnected decodes bounded, discontiguous code with abstract return
// frames. Explicit pulls may consume a call frame before an indirect tail jump.
// It does not admit evidence or establish dynamic dispatch ancestry.
func DecodeConnected(c ConnectedConfig) (*RegionIR, error) {
	if len(c.ROM) == 0 || len(c.ROM) > 16<<20 || c.MaxInstructions < 1 || c.MaxInstructions > 1<<16 || c.MaxSteps < 1 || c.MaxSteps > 1<<20 || len(c.Spans) == 0 || len(c.Spans) > 64 || c.Entry >= 1<<24 {
		return nil, fmt.Errorf("decode connected: invalid bounds")
	}
	if c.Context.C != "" && c.Context.C != "unknown" && c.Context.C != "set" && c.Context.C != "clear" {
		return nil, fmt.Errorf("decode connected: malformed carry contract")
	}
	if c.Context.E != "clear" || !(c.Context.M == "set" || c.Context.M == "clear") || !(c.Context.X == "set" || c.Context.X == "clear") {
		return nil, fmt.Errorf("decode connected: unresolved native widths")
	}
	rom := append([]byte(nil), c.ROM...)
	spans := append([]CodeSpan(nil), c.Spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	for i, s := range spans {
		if s.Start >= s.End || s.End > 1<<24 || s.Start>>16 != (s.End-1)>>16 || !mappedPolicyROM(s.Start) || !mappedPolicyROM(s.End-1) || (i > 0 && spans[i-1].End > s.Start) {
			return nil, fmt.Errorf("decode connected: invalid or overlapping span")
		}
		off := int((((s.End-1)>>16)&127)*32768 + ((s.End - 1) & 32767))
		if off >= len(rom) {
			return nil, fmt.Errorf("decode connected: span exceeds ROM")
		}
	}
	fetch := func(a uint32, n int) ([]byte, error) {
		for _, s := range spans {
			if a >= s.Start && uint64(a)+uint64(n) <= uint64(s.End) {
				off := int((a>>16&127)*32768 + (a & 32767))
				return rom[off : off+n], nil
			}
		}
		return nil, fmt.Errorf("decode connected: address $%06X outside spans", a)
	}
	region := &RegionIR{ID: fmt.Sprintf("connected-%06x", c.Entry), Name: fmt.Sprintf("sub_%06x", c.Entry), EntryAddress: c.Entry, EntryContext: c.Context, MaxSteps: c.MaxSteps, RefusalTargets: map[uint32]string{}, StackAwareCalls: true}
	for a, reason := range c.RefusalTargets {
		if a >= 1<<24 || reason == "" {
			return nil, fmt.Errorf("decode connected: invalid refusal")
		}
		region.RefusalTargets[a] = reason
	}
	queue := []connectedItem{{address: c.Entry, context: c.Context}}
	seen := map[string]bool{}
	widths := map[uint32]recovery.Context{}
	blocks := map[uint32]*BlockIR{}
	owners := map[uint32]uint32{}
	returns := map[uint32]bool{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		a, ctx := item.address, item.context
		// Carry is a runtime value after control flow; only the region entry
		// contract may constrain it. Decoded block widths do not imply carry.
		ctx.C = "unknown"
		if region.RefusalTargets[a] != "" {
			continue
		}
		key := fmt.Sprintf("%06x/%s/%s/%v", a, ctx.M, ctx.X, item.frames)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > c.MaxInstructions {
			return nil, fmt.Errorf("decode connected: instruction budget exceeded")
		}
		if old, ok := widths[a]; ok && (old.M != ctx.M || old.X != ctx.X) {
			return nil, fmt.Errorf("decode connected: conflicting widths at $%06X", a)
		}
		widths[a] = ctx
		b, err := fetch(a, 1)
		if err != nil {
			return nil, err
		}
		op := b[0]
		if op == 0x9a {
			return nil, fmt.Errorf("decode connected: stack pointer assignment unsupported at $%06X", a)
		}
		if op != 0x22 && op != 0xdc {
			if reason := unsupportedRegionControl(op, true); reason != "" {
				return nil, fmt.Errorf("decode connected: unsupported %s at $%06X", reason, a)
			}
		}
		n, err := calcInstructionSize(op, cpu.Opcodes[op], context8(ctx.M), context8(ctx.X), a)
		if err != nil {
			return nil, err
		}
		b, err = fetch(a, n)
		if err != nil {
			return nil, err
		}
		for k := 0; k < n; k++ {
			addr := a + uint32(k)
			if owner, ok := owners[addr]; ok && owner != a {
				return nil, fmt.Errorf("decode connected: overlapping instruction at $%06X", a)
			}
			owners[addr] = a
		}
		nextCtx := ctx
		if op == 0xc2 || op == 0xe2 {
			if op == 0xe2 && b[1]&8 != 0 {
				return nil, fmt.Errorf("decode connected: decimal unsupported")
			}
			value := "clear"
			if op == 0xe2 {
				value = "set"
			}
			if b[1]&0x20 != 0 {
				nextCtx.M = value
			}
			if b[1]&0x10 != 0 {
				nextCtx.X = value
			}
		}
		next := a&0xff0000 | uint32(uint16(a)+uint16(n))
		frames := append([]connectedFrame(nil), item.frames...)
		var succ []uint32
		add := func(target uint32, fr []connectedFrame) {
			succ = append(succ, target)
			queue = append(queue, connectedItem{target, nextCtx, append([]connectedFrame(nil), fr...)})
		}
		switch op {
		case 0x20, 0x22:
			target := a&0xff0000 | uint32(b[1]) | uint32(b[2])<<8
			size := 2
			if op == 0x22 {
				target = uint32(b[1]) | uint32(b[2])<<8 | uint32(b[3])<<16
				size = 3
			}
			if len(frames) >= 64 {
				return nil, fmt.Errorf("decode connected: call depth exceeded")
			}
			frames = append(frames, connectedFrame{next, op, size})
			returns[next] = true
			add(target, frames)
		case 0x8b, 0x4b:
			if len(frames) >= 64 {
				return nil, fmt.Errorf("decode connected: stack depth exceeded")
			}
			frames = append(frames, connectedFrame{remaining: 1})
			add(next, frames)
		case 0x68, 0x7a, 0xfa, 0xab:
			size := 1
			if op == 0x68 && !context8(ctx.M) || (op == 0x7a || op == 0xfa) && !context8(ctx.X) {
				size = 2
			}
			if len(frames) == 0 {
				return nil, fmt.Errorf("decode connected: unmodeled outer stack pull at $%06X", a)
			}
			for size > 0 && len(frames) > 0 {
				top := &frames[len(frames)-1]
				take := min(size, top.remaining)
				top.remaining -= take
				size -= take
				if top.remaining == 0 {
					frames = frames[:len(frames)-1]
				}
			}
			if size != 0 {
				return nil, fmt.Errorf("decode connected: stack pull crosses unmodeled outer frame at $%06X", a)
			}
			add(next, frames)
		case 0x60, 0x6b:
			if len(frames) > 0 {
				top := frames[len(frames)-1]
				want := byte(0x20)
				size := 2
				if op == 0x6b {
					want = 0x22
					size = 3
				}
				if top.opcode != want || top.remaining != size {
					return nil, fmt.Errorf("decode connected: incomplete return frame at $%06X", a)
				}
				add(top.resume, frames[:len(frames)-1])
			}
		case 0xdc:
			targets := c.IndirectTargets[a]
			if len(targets) == 0 || len(targets) > 64 {
				return nil, fmt.Errorf("decode connected: indirect jump lacks bounded targets at $%06X", a)
			}
			for _, t := range targets {
				if t >= 1<<24 {
					return nil, fmt.Errorf("decode connected: invalid indirect target")
				}
				add(t, frames)
			}
		case 0x80, 0x82, 0x10, 0x30, 0x50, 0x70, 0x90, 0xb0, 0xd0, 0xf0:
			var rel int32
			if op == 0x82 {
				rel = int32(int16(uint16(b[1]) | uint16(b[2])<<8))
			} else {
				rel = int32(int8(b[1]))
			}
			target := a&0xff0000 | uint32(uint16(int32(uint16(next))+rel))
			add(target, frames)
			if op != 0x80 && op != 0x82 {
				add(next, frames)
			}
		default:
			add(next, frames)
		}
		if _, exists := blocks[a]; !exists {
			inst := recovery.Instruction{ID: fmt.Sprintf("inst-%06x", a), Architecture: "wdc65816", Address: a, Bytes: hex.EncodeToString(b), Opcode: op, Mnemonic: cpu.Opcodes[op].Name, Context: ctx}
			block, err := LiftBlock(&structure.BasicBlock{ID: fmt.Sprintf("bb-%06x", a), StartAddress: a, EndAddress: a + uint32(n), Instructions: []recovery.Instruction{inst}, Successors: succ}, ctx)
			if err != nil {
				return nil, err
			}
			if block.UnsupportedCount != 0 {
				return nil, fmt.Errorf("decode connected: unsupported lifted opcode $%02X at $%06X", op, a)
			}
			if op == 0xdc {
				for i := range block.Statements {
					if block.Statements[i].Kind == "jump_indirect" {
						block.Statements[i].AllowedTargets = append([]uint32(nil), c.IndirectTargets[a]...)
					}
				}
			}
			blocks[a] = block
		} else {
			for _, target := range succ {
				found := false
				for _, existing := range blocks[a].Successors {
					if existing == target {
						found = true
					}
				}
				if !found {
					blocks[a].Successors = append(blocks[a].Successors, target)
				}
			}
		}
	}
	for a := range returns {
		if blocks[a] != nil {
			region.CallSites = append(region.CallSites, a)
		}
	}
	sort.Slice(region.CallSites, func(i, j int) bool { return region.CallSites[i] < region.CallSites[j] })
	var addresses []uint32
	for a := range blocks {
		addresses = append(addresses, a)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	for _, a := range addresses {
		region.Blocks = append(region.Blocks, blocks[a])
	}
	return region, nil
}
