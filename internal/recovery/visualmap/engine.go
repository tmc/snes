package visualmap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

// Engine indexes trace events and resolves coordinate queries to decompiled C statements.
type Engine struct {
	mu           sync.RWMutex
	doc          *recovery.Document
	blocks       []*structure.BasicBlock
	blockByAddr  []*structure.BasicBlock // sorted by StartAddress
	dmaIndex     []DMAEntry
	wramWrites   map[uint32][]WriteEntry // WRAM address -> sorted writes
	frameBounds  map[int]FrameBounds
	oamSnapshots map[int][544]uint8
	oamWrites    []OAMWriteEntry
}

// OAMWriteEntry records an indexed PPU OAM write.
type OAMWriteEntry struct {
	Cycle uint64
	Frame int
	Addr  uint32
	Value uint8
}

// DMAEntry records indexed metadata for an observed DMA transfer.
type DMAEntry struct {
	Cycle     uint64
	Frame     int
	Channel   int
	TriggerPC uint32
	Target    uint8
	SrcAddr   uint32
	DstSpace  string
	DstStart  uint32
	DstEnd    uint32
	Count     int
}

// WriteEntry records an indexed CPU store event.
type WriteEntry struct {
	Cycle uint64
	Frame int
	PC    uint32
	Value uint8
}

// FrameBounds holds execution boundaries for a frame.
type FrameBounds struct {
	StartCycle  uint64
	VBlankCycle uint64
	EndCycle    uint64
}

// NewEngine creates a new visual provenance engine over a recovery document.
func NewEngine(doc *recovery.Document, blocks []*structure.BasicBlock) *Engine {
	sortedBlocks := make([]*structure.BasicBlock, len(blocks))
	copy(sortedBlocks, blocks)
	sort.Slice(sortedBlocks, func(i, j int) bool {
		return sortedBlocks[i].StartAddress < sortedBlocks[j].StartAddress
	})

	return &Engine{
		doc:          doc,
		blocks:       blocks,
		blockByAddr:  sortedBlocks,
		wramWrites:   make(map[uint32][]WriteEntry),
		frameBounds:  make(map[int]FrameBounds),
		oamSnapshots: make(map[int][544]uint8),
	}
}

// SetFrameBounds records the cycle limits for a given frame.
func (e *Engine) SetFrameBounds(frame int, bounds FrameBounds) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.frameBounds[frame] = bounds
}

// SetOAMSnapshot stores the latched 544-byte OAM table for a frame.
func (e *Engine) SetOAMSnapshot(frame int, oam [544]uint8) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.oamSnapshots[frame] = oam
}

func (e *Engine) oamForFrame(frame int) ([544]uint8, int, int, bool) {
	if snap, ok := e.oamSnapshots[frame]; ok {
		return snap, 544, 0, true
	}
	bounds, ok := e.frameBounds[frame]
	if !ok {
		return [544]uint8{}, 0, 0, false
	}
	var oam [544]uint8
	known := [544]bool{}
	knownCount := 0
	displayMutations := 0
	for _, w := range e.oamWrites {
		if w.Addr < 544 && w.Cycle <= bounds.StartCycle {
			oam[w.Addr] = w.Value
			if !known[w.Addr] {
				known[w.Addr] = true
				knownCount++
			}
		} else if w.Addr < 544 && w.Cycle > bounds.StartCycle && (bounds.VBlankCycle == 0 || w.Cycle < bounds.VBlankCycle) {
			displayMutations++
		}
	}
	return oam, knownCount, displayMutations, knownCount > 0
}

// IngestEvent processes and indexes a trace event for causal lookup.
func (e *Engine) IngestEvent(ev trace.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Ingest DMA transfers
	if ev.Kind == "dma" && ev.DMA != nil {
		var pc uint32
		if ev.PC != nil {
			pc = uint32(ev.PC.Bank)<<16 | uint32(ev.PC.Addr)
		}
		// Canonicalize DMA source address to 17-bit WRAM offset ($00000..$1FFFF)
		// Snestrace emits Source.Space = "cpu" with 24-bit address (e.g. 0x7E0A00)
		// or Source.Space = "wram" with normalized offset (e.g. 0x0A00).
		srcAddr := ev.Source.Start
		if ev.Source.Space == "cpu" {
			bank := (srcAddr >> 16) & 0xFF
			if bank == 0x7E || bank == 0x7F {
				srcAddr = srcAddr & 0x01FFFF
			} else if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && (srcAddr&0xFFFF) <= 0x1FFF {
				srcAddr = srcAddr & 0x1FFF
			}
		} else if ev.Source.Space == "wram" {
			srcAddr = srcAddr & 0x01FFFF
		}

		e.dmaIndex = append(e.dmaIndex, DMAEntry{
			Cycle:     ev.Cycle,
			Frame:     ev.Frame,
			Channel:   ev.DMA.Channel,
			TriggerPC: pc,
			Target:    ev.DMA.Target,
			SrcAddr:   srcAddr,
			DstSpace:  ev.Dest.Space,
			DstStart:  ev.Dest.Start,
			DstEnd:    ev.Dest.End,
			Count:     ev.DMA.Count,
		})
	}

	// Ingest WRAM writes
	if (ev.Kind == "bus" || ev.Kind == "wram_port") && ev.Op == "write" {
		var addr uint32
		isWRAM := false

		if ev.Space == "wram" {
			// Real snestrace bus events with Space="wram" already carry normalized offset
			addr = ev.Addr & 0x01FFFF
			isWRAM = true
		} else {
			// CPU space address: map through CPUSpace
			space, cAddr := trace.CPUSpace(ev.Addr)
			if space == "wram" {
				addr = cAddr & 0x01FFFF
				isWRAM = true
			}
		}

		if isWRAM {
			var pc uint32
			if ev.PC != nil {
				pc = uint32(ev.PC.Bank)<<16 | uint32(ev.PC.Addr)
			}
			e.wramWrites[addr] = append(e.wramWrites[addr], WriteEntry{
				Cycle: ev.Cycle,
				Frame: ev.Frame,
				PC:    pc,
				Value: uint8(ev.Value),
			})
		}
	}

	// Ingest PPU OAM writes
	if ev.Kind == "ppu" && ev.Space == "oam" && ev.Op == "write" {
		e.oamWrites = append(e.oamWrites, OAMWriteEntry{
			Cycle: ev.Cycle,
			Frame: ev.Frame,
			Addr:  ev.Addr,
			Value: uint8(ev.Value),
		})
	}
}

// HasFrame reports whether an OAM snapshot has been ingested for the given frame.
func (e *Engine) HasFrame(frame int) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.oamSnapshots[frame]; ok {
		return true
	}
	if bounds, ok := e.frameBounds[frame]; ok {
		for _, w := range e.oamWrites {
			if w.Addr < 544 && w.Cycle <= bounds.StartCycle {
				return true
			}
		}
	}
	return false
}

// Query resolves a screen coordinate (x, y) at a given frame to candidate visual provenance.
// It fails closed if context is canceled or no OAM snapshot is ingested for the frame.
func (e *Engine) Query(ctx context.Context, frame, x, y int) (*PixelProvenance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	oam, knownCount, displayMutations, hasOAM := e.oamForFrame(frame)
	bounds, hasBounds := e.frameBounds[frame]

	if !hasOAM {
		return nil, fmt.Errorf("visual provenance unavailable: frame %d has no pre-display OAM evidence at Start %d", frame, bounds.StartCycle)
	}

	res := &PixelProvenance{
		Query:               QueryCoords{Frame: frame, X: x, Y: y},
		KnownOAMBytes:       knownCount,
		DisplayOAMMutations: displayMutations,
	}

	// 1. Identify candidate sprite at (x, y)
	sprIdx, attrs, bbox, ok := evaluatePixelSprite(oam, x, y)
	if !ok {
		res.VisualEntity = VisualEntityInfo{Kind: "candidate_unmatched"}
		return res, nil
	}

	res.VisualEntity = VisualEntityInfo{
		Kind:        "sprite",
		SpriteIndex: sprIdx,
		BoundingBox: bbox,
		Attributes:  attrs,
		PhysicalOAM: oam[sprIdx*4 : sprIdx*4+4],
	}

	// 2. Query DMA transfer that loaded this OAM slice before frame presentation
	// Requires explicit frame bounds to establish temporal precedence.
	targetLowAddr := uint32(sprIdx * 4)
	targetHighAddr := uint32(512 + sprIdx/4)
	var latestDMA *DMAEntry
	var targetOAMAddr uint32
	var maxCycle uint64
	if hasBounds {
		for i := range e.dmaIndex {
			d := &e.dmaIndex[i]
			if d.DstSpace == "oam" {
				matchesLow := d.DstStart <= targetLowAddr && targetLowAddr <= d.DstEnd
				matchesHigh := d.DstStart <= targetHighAddr && targetHighAddr <= d.DstEnd
				if matchesLow || matchesHigh {
					if d.Cycle <= bounds.StartCycle {
						if latestDMA == nil || d.Cycle > maxCycle {
							latestDMA = d
							maxCycle = d.Cycle
							if matchesHigh {
								targetOAMAddr = targetHighAddr
							} else {
								targetOAMAddr = targetLowAddr
							}
						}
					}
				}
			}
		}
	}

	if latestDMA == nil {
		return res, nil
	}

	wramOffset := latestDMA.SrcAddr + (targetOAMAddr - latestDMA.DstStart)
	pcStr := fmt.Sprintf("%02X:%04X", latestDMA.TriggerPC>>16, latestDMA.TriggerPC&0xFFFF)
	res.DMATransfer = &DMATransferInfo{
		Channel:           latestDMA.Channel,
		Frame:             latestDMA.Frame,
		Cycle:             latestDMA.Cycle,
		TriggerPC:         pcStr,
		CurrentPC:         pcStr,
		DestRegister:      "$2104",
		SourceRange:       trace.Range{Space: "wram", Start: latestDMA.SrcAddr, End: latestDMA.SrcAddr + uint32(latestDMA.Count) - 1},
		DestRange:         trace.Range{Space: "oam", Start: latestDMA.DstStart, End: latestDMA.DstEnd},
		WRAMSourceAddress: fmt.Sprintf("%06X", 0x7E0000+wramOffset),
	}

	// 3. Query last CPU write to that WRAM buffer before DMA cycle
	writes := e.wramWrites[wramOffset]
	var lastWrite *WriteEntry
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].Cycle < latestDMA.Cycle {
			lastWrite = &writes[i]
			break
		}
	}

	if lastWrite == nil {
		return res, nil
	}

	res.CPUWrite = &CPUWriteInfo{
		Frame:         lastWrite.Frame,
		Cycle:         lastWrite.Cycle,
		PC:            fmt.Sprintf("%02X:%04X", lastWrite.PC>>16, lastWrite.PC&0xFFFF),
		Address:       fmt.Sprintf("%06X", 0x7E0000+wramOffset),
		StoredValue:   lastWrite.Value,
		InstructionID: fmt.Sprintf("inst-%06x", lastWrite.PC),
	}

	if targetOAMAddr < 544 {
		if lastWrite.Value == oam[targetOAMAddr] {
			res.ValueConsistency = "value_match"
		} else {
			res.ValueConsistency = "value_mismatch"
		}
	}

	// 4. Resolve PC to BasicBlock and Lifted C statement
	targetBlock := e.findBlock(lastWrite.PC)
	if targetBlock == nil {
		return res, nil
	}

	entryCtx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	if len(targetBlock.Instructions) > 0 && targetBlock.Instructions[0].Context.M != "" {
		entryCtx = targetBlock.Instructions[0].Context
	}

	ir, err := decomp.LiftBlock(targetBlock, entryCtx)
	if err != nil {
		return res, nil
	}

	pseudoC, sourceMap := decomp.GeneratePseudoC(ir)

	matchedLine := 0
	for _, entry := range sourceMap {
		if entry.Address == lastWrite.PC {
			matchedLine = entry.Line
			break
		}
	}

	statement := ""
	if matchedLine > 0 {
		lines := strings.Split(pseudoC, "\n")
		if matchedLine <= len(lines) {
			statement = strings.TrimSpace(lines[matchedLine-1])
		}
	}

	res.CodeProvenance = &CodeProvenanceInfo{
		BlockID:    targetBlock.ID,
		SourceLine: matchedLine,
		Statement:  statement,
		PseudoC:    pseudoC,
		SourceMap:  sourceMap,
	}

	return res, nil
}

func (e *Engine) findBlock(pc uint32) *structure.BasicBlock {
	n := sort.Search(len(e.blockByAddr), func(i int) bool {
		return e.blockByAddr[i].EndAddress > pc
	})
	if n < len(e.blockByAddr) && e.blockByAddr[n].StartAddress <= pc {
		return e.blockByAddr[n]
	}
	return nil
}

func evaluatePixelSprite(oam [544]uint8, x, y int) (int, SpriteAttrs, BoundingBox, bool) {
	for i := 0; i < 128; i++ {
		addr := i * 4
		xLow := int(oam[addr])
		yPos := int(oam[addr+1])
		tile := int(oam[addr+2])
		attrs := oam[addr+3]

		highByte := oam[512+(i/4)]
		xHigh := int((highByte >> ((i % 4) * 2)) & 1)
		sizeBit := (highByte>>((i%4)*2+1))&1 != 0

		posX := xLow | (xHigh << 8)
		if posX >= 256 {
			posX -= 512
		}

		width, height := 8, 8
		if sizeBit {
			width, height = 16, 16
		}

		if x >= posX && x < posX+width && y >= yPos && y < yPos+height {
			return i, SpriteAttrs{
				Tile:     tile | int(attrs&1)<<8,
				Palette:  int((attrs >> 1) & 7),
				Priority: int((attrs >> 4) & 3),
				HFlip:    attrs&0x40 != 0,
				VFlip:    attrs&0x80 != 0,
				Large:    sizeBit,
			}, BoundingBox{X: posX, Y: yPos, Width: width, Height: height}, true
		}
	}
	return 0, SpriteAttrs{}, BoundingBox{}, false
}
