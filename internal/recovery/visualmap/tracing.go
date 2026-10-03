package visualmap

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/trace"
)

// TraceQuery specifies the target sprite or screen coordinate for visual-to-assembly tracing.
type TraceQuery struct {
	Frame       int  `json:"frame"`
	X           *int `json:"x,omitempty"`
	Y           *int `json:"y,omitempty"`
	SpriteIndex *int `json:"sprite_index,omitempty"`
}

// TraceResult represents candidate visual-to-assembly tracing.
type TraceResult struct {
	PPUFrame               int                 `json:"ppu_frame"`
	Query                  TraceQuery          `json:"query"`
	CandidateType          string              `json:"candidate_type"` // "geometric_candidate", "named_sprite"
	OwnershipQualification string              `json:"ownership_qualification"`
	WinningPixelWitness    string              `json:"winning_pixel_witness"`
	ValueConsistency       string              `json:"value_consistency"`
	RetirementAssociation  string              `json:"retirement_association"`
	OAM                    OAMTraceEntry       `json:"oam"`
	DMATransfer            *DMATraceInfo       `json:"dma_transfer,omitempty"`
	DMARegisters           *DMARegisters       `json:"dma_registers,omitempty"`
	ShadowBuffer           *ShadowBufferTrace  `json:"shadow_buffer,omitempty"`
	CPUWrite               *CPUWriteTrace      `json:"cpu_write,omitempty"`
	Retirement             *RetirementSequence `json:"retirement_sequence,omitempty"`
	Code                   *CodeProvenanceInfo `json:"code_provenance,omitempty"`
	Status                 string              `json:"status"` // "candidate_correlated", "candidate_unmatched", "no_dma_transfer"
}

// OAMTraceEntry describes the reconstructed OAM sprite attributes and geometry.
type OAMTraceEntry struct {
	Index       int         `json:"index"`
	X           int         `json:"x"`
	Y           int         `json:"y"`
	Tile        int         `json:"tile"`
	Priority    int         `json:"priority"`
	Palette     int         `json:"palette"`
	HFlip       bool        `json:"hflip"`
	VFlip       bool        `json:"vflip"`
	Large       bool        `json:"large"`
	BoundingBox BoundingBox `json:"bounding_box"`
	RawBytes    [4]uint8    `json:"raw_bytes"`
	HighByte    uint8       `json:"high_byte"`
	HighBits    uint8       `json:"high_bits"`
}

// DMATraceInfo describes the VBLANK DMA transfer uploading OAM data.
type DMATraceInfo struct {
	Channel           int         `json:"channel"`
	Frame             int         `json:"frame"`
	Cycle             uint64      `json:"cycle"`
	EventID           uint64      `json:"event_id,omitempty"`
	TriggerPC         string      `json:"trigger_pc,omitempty"`
	CurrentPC         string      `json:"current_pc,omitempty"`
	DestRegister      string      `json:"dest_register"`
	SourceRange       trace.Range `json:"source_range"`
	DestRange         trace.Range `json:"dest_range"`
	WRAMSourceAddress string      `json:"wram_source_address,omitempty"`
}

// DMARegisters records the channel registers ($43x0-$43xA) configuring the OAM DMA upload.
type DMARegisters struct {
	Channel      int                `json:"channel"`
	BaseRegister string             `json:"base_register"`
	DMAP         uint8              `json:"dmap"`
	BBAD         uint8              `json:"bbad"`
	A1T          uint16             `json:"a1t"`
	A1B          uint8              `json:"a1b"`
	DAS          uint16             `json:"das"`
	DASB         uint8              `json:"dasb"`
	A2A          uint16             `json:"a2a"`
	NTRL         uint8              `json:"ntrl"`
	Raw          [11]uint8          `json:"raw_registers"`
	Writes       []DMARegisterWrite `json:"observed_writes,omitempty"`
}

// DMARegisterWrite records an observed write to a DMA channel register ($43x0-$43xA).
type DMARegisterWrite struct {
	EventID  uint64 `json:"event_id"`
	Cycle    uint64 `json:"cycle"`
	Channel  int    `json:"channel"`
	Register string `json:"register"`
	RegNum   int    `json:"reg_num"`
	Value    uint8  `json:"value"`
	PC       string `json:"pc,omitempty"`
}

// ShadowBufferTrace traces the sprite's slice in the WRAM shadow buffer ($7E:0000-$7E:1FFF).
type ShadowBufferTrace struct {
	BaseAddress    uint32   `json:"base_address"`
	BaseAddressHex string   `json:"base_address_hex"`
	SpriteLowAddr  uint32   `json:"sprite_low_addr"`
	SpriteLowHex   string   `json:"sprite_low_hex"`
	SpriteHighAddr uint32   `json:"sprite_high_addr"`
	SpriteHighHex  string   `json:"sprite_high_hex"`
	SliceBytes     [4]uint8 `json:"slice_bytes"`
	HighTableByte  uint8    `json:"high_table_byte"`
}

// CPUWriteTrace describes the CPU write event that populated the shadow buffer.
type CPUWriteTrace struct {
	EventID       uint64 `json:"event_id"`
	Frame         int    `json:"frame"`
	Cycle         uint64 `json:"cycle"`
	PC            string `json:"pc"`
	Address       string `json:"address"`
	StoredValue   uint8  `json:"stored_value"`
	Disassembly   string `json:"disassembly,omitempty"`
	InstructionID string `json:"instruction_id,omitempty"`
}

// RetirementSequence captures the CPU instruction retirement that executed the store
// and its preceding retirement sequence context.
type RetirementSequence struct {
	EventID   uint64         `json:"event_id"`
	Seq       uint64         `json:"seq"`
	PC        string         `json:"pc"`
	Disasm    string         `json:"disasm,omitempty"`
	Opcode    uint8          `json:"opcode"`
	Status    string         `json:"status"`
	EntryA    uint16         `json:"entry_a"`
	EntryX    uint16         `json:"entry_x"`
	EntryY    uint16         `json:"entry_y"`
	EntryP    uint8          `json:"entry_p"`
	EntrySP   uint16         `json:"entry_sp"`
	EntryDP   uint16         `json:"entry_dp"`
	ExitA     uint16         `json:"exit_a"`
	ExitX     uint16         `json:"exit_x"`
	ExitY     uint16         `json:"exit_y"`
	ExitP     uint8          `json:"exit_p"`
	ExitSP    uint16         `json:"exit_sp"`
	ExitDP    uint16         `json:"exit_dp"`
	Cycles    uint64         `json:"cycles"`
	Preceding *PrecedingInsn `json:"preceding_retirement,omitempty"`
}

// PrecedingInsn records the retirement immediately preceding the writing instruction.
type PrecedingInsn struct {
	EventID uint64 `json:"event_id"`
	Seq     uint64 `json:"seq"`
	PC      string `json:"pc"`
	Disasm  string `json:"disasm,omitempty"`
}

// RetirementEntry records an indexed CPU instruction retirement.
type RetirementEntry struct {
	EventID uint64
	Seq     uint64
	Cycle   uint64
	Frame   int
	PC      string
	Insn    trace.Insn
}

// IngestRetirement indexes an instruction retirement for causal lookup.
func (e *Engine) IngestRetirement(r RetirementEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retirements = append(e.retirements, r)
}

// TracePixel traces visual provenance for screen coordinates (x, y) at a given frame.
func (e *Engine) TracePixel(ctx context.Context, frame, x, y int) (*TraceResult, error) {
	return e.Trace(ctx, TraceQuery{Frame: frame, X: &x, Y: &y})
}

// TraceSprite traces visual provenance for a sprite index (0..127) at a given frame.
func (e *Engine) TraceSprite(ctx context.Context, frame, spriteIndex int) (*TraceResult, error) {
	return e.Trace(ctx, TraceQuery{Frame: frame, SpriteIndex: &spriteIndex})
}

// Trace resolves a visual query to its bidirectional provenance path.
func (e *Engine) Trace(ctx context.Context, q TraceQuery) (*TraceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	frame := q.Frame
	if frame == 0 {
		frame = 333
	}

	oam, known, _, _, hasOAM := e.oamForFrame(frame)
	bounds, hasBounds := e.frameBounds[frame]

	if !hasOAM {
		return nil, fmt.Errorf("visual provenance unavailable: frame %d has no OAM evidence", frame)
	}

	var sprIdx int
	var bbox BoundingBox
	var attrs SpriteAttrs
	if q.SpriteIndex != nil {
		sprIdx = *q.SpriteIndex
		if sprIdx < 0 || sprIdx >= 128 {
			return nil, fmt.Errorf("invalid sprite index %d: must be 0..127", sprIdx)
		}
		attrs, bbox = decodeSpriteAttrs(oam, sprIdx)
	} else if q.X != nil && q.Y != nil {
		var ok bool
		sprIdx, attrs, bbox, ok = evaluatePixelSprite(oam, known, *q.X, *q.Y)
		if !ok {
			return &TraceResult{
				PPUFrame:               frame,
				Query:                  q,
				CandidateType:          "geometric_candidate",
				OwnershipQualification: "candidate_unmatched: no sprite covers coordinate",
				WinningPixelWitness:    "absent",
				Status:                 "candidate_unmatched",
			}, nil
		}
	} else {
		return nil, fmt.Errorf("query must specify sprite index or screen coordinates")
	}

	highByte := oam[512+(sprIdx/4)]
	shift := (sprIdx % 4) * 2
	highBits := (highByte >> shift) & 0x03

	oamEntry := OAMTraceEntry{
		Index:       sprIdx,
		X:           bbox.X,
		Y:           bbox.Y,
		Tile:        attrs.Tile,
		Priority:    attrs.Priority,
		Palette:     attrs.Palette,
		HFlip:       attrs.HFlip,
		VFlip:       attrs.VFlip,
		Large:       attrs.Large,
		BoundingBox: bbox,
		RawBytes:    [4]uint8{oam[sprIdx*4], oam[sprIdx*4+1], oam[sprIdx*4+2], oam[sprIdx*4+3]},
		HighByte:    highByte,
		HighBits:    highBits,
	}

	candidateType := "named_sprite"
	ownership := "named_sprite_inspection: visibility and winning pixel unproven"
	winningWitness := "uninspected: named sprite query"
	if q.X != nil && q.Y != nil {
		candidateType = "geometric_candidate"
		ownership = "candidate_geometric_only: winning pixel ownership unknown without compositor/renderer dot witness"
		winningWitness = "absent: compositor arbitration and tile transparency not modeled"
	}

	res := &TraceResult{
		PPUFrame:               frame,
		Query:                  q,
		CandidateType:          candidateType,
		OwnershipQualification: ownership,
		WinningPixelWitness:    winningWitness,
		ValueConsistency:       "candidate_correlation: WRAM shadow buffer to OAM transfer payload unverified",
		RetirementAssociation:  "candidate_correlation: mapped by PC and cycle proximity",
		OAM:                    oamEntry,
		Status:                 "candidate_correlated",
	}

	// 1. Query latest DMA transfer that uploaded to OAM before display cycle
	targetLowAddr := uint32(sprIdx * 4)
	targetHighAddr := uint32(512 + sprIdx/4)
	var latestDMA *DMAEntry
	var targetOAMAddr uint32
	var maxCycle uint64
	for i := range e.dmaIndex {
		d := &e.dmaIndex[i]
		if d.DstSpace == "oam" {
			matchesLow := d.DstStart <= targetLowAddr && targetLowAddr <= d.DstEnd
			matchesHigh := d.DstStart <= targetHighAddr && targetHighAddr <= d.DstEnd
			if matchesLow || matchesHigh {
				if !hasBounds || d.Cycle <= bounds.StartCycle {
					if latestDMA == nil || d.Cycle > maxCycle || (d.Cycle == maxCycle && d.EventID > latestDMA.EventID) {
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

	if latestDMA == nil {
		res.Status = "no_dma_transfer"
		return res, nil
	}

	// 2. Format DMA transfer info
	var srcRange trace.Range
	var wramSourceAddr string
	if latestDMA.ValidWRAM {
		srcRange = trace.Range{
			Space: "wram",
			Start: latestDMA.SrcAddr,
			End:   latestDMA.SrcAddr + uint32(latestDMA.Count) - 1,
		}
		wramOffset := latestDMA.SrcAddr + (targetOAMAddr - latestDMA.DstStart)
		wramSourceAddr = fmt.Sprintf("$7E:%04X", wramOffset)
	} else {
		srcRange = trace.Range{
			Space: latestDMA.SrcSpace,
			Start: latestDMA.SrcAddr,
			End:   latestDMA.SrcAddr + uint32(latestDMA.Count) - 1,
		}
	}

	var currentPC string
	if latestDMA.CurrentPC != 0 {
		currentPC = fmt.Sprintf("%02X:%04X", latestDMA.CurrentPC>>16, latestDMA.CurrentPC&0xFFFF)
	}

	res.DMATransfer = &DMATraceInfo{
		Channel:           latestDMA.Channel,
		Frame:             latestDMA.Frame,
		Cycle:             latestDMA.Cycle,
		EventID:           latestDMA.EventID,
		CurrentPC:         currentPC,
		DestRegister:      "$2104",
		SourceRange:       srcRange,
		DestRange:         trace.Range{Space: "oam", Start: latestDMA.DstStart, End: latestDMA.DstEnd},
		WRAMSourceAddress: wramSourceAddr,
	}

	// 3. Reconstruct DMA registers ($43x0-$43xA)
	ch := latestDMA.Channel
	baseReg := fmt.Sprintf("$43%d0", ch)
	dmaRegs := &DMARegisters{
		Channel:      ch,
		BaseRegister: baseReg,
		DMAP:         latestDMA.Target,
		BBAD:         0x04,
		A1T:          uint16(latestDMA.SrcAddr & 0xFFFF),
		A1B:          0x7E,
		DAS:          uint16(latestDMA.Count),
	}

	for _, w := range e.dmaRegWrites {
		if w.Channel == ch && (w.Cycle < latestDMA.Cycle || (w.Cycle == latestDMA.Cycle && w.EventID < latestDMA.EventID)) {
			dmaRegs.Writes = append(dmaRegs.Writes, w)
			switch w.RegNum {
			case 0x00:
				dmaRegs.DMAP = w.Value
			case 0x01:
				dmaRegs.BBAD = w.Value
			case 0x02:
				dmaRegs.A1T = (dmaRegs.A1T & 0xFF00) | uint16(w.Value)
			case 0x03:
				dmaRegs.A1T = (dmaRegs.A1T & 0x00FF) | (uint16(w.Value) << 8)
			case 0x04:
				dmaRegs.A1B = w.Value
			case 0x05:
				dmaRegs.DAS = (dmaRegs.DAS & 0xFF00) | uint16(w.Value)
			case 0x06:
				dmaRegs.DAS = (dmaRegs.DAS & 0x00FF) | (uint16(w.Value) << 8)
			case 0x07:
				dmaRegs.DASB = w.Value
			case 0x08:
				dmaRegs.A2A = (dmaRegs.A2A & 0xFF00) | uint16(w.Value)
			case 0x09:
				dmaRegs.A2A = (dmaRegs.A2A & 0x00FF) | (uint16(w.Value) << 8)
			case 0x0A:
				dmaRegs.NTRL = w.Value
			}
		}
	}
	dmaRegs.Raw = [11]uint8{
		dmaRegs.DMAP,
		dmaRegs.BBAD,
		uint8(dmaRegs.A1T & 0xFF),
		uint8(dmaRegs.A1T >> 8),
		dmaRegs.A1B,
		uint8(dmaRegs.DAS & 0xFF),
		uint8(dmaRegs.DAS >> 8),
		dmaRegs.DASB,
		uint8(dmaRegs.A2A & 0xFF),
		uint8(dmaRegs.A2A >> 8),
		dmaRegs.NTRL,
	}
	res.DMARegisters = dmaRegs

	// 4. Trace WRAM shadow buffer ($7E:0000-$7E:1FFF)
	wramBase := latestDMA.SrcAddr
	spriteLowWRAM := wramBase + uint32(sprIdx*4)
	spriteHighWRAM := wramBase + uint32(512+sprIdx/4)

	res.ShadowBuffer = &ShadowBufferTrace{
		BaseAddress:    0x7E0000 + wramBase,
		BaseAddressHex: fmt.Sprintf("$7E:%04X", wramBase),
		SpriteLowAddr:  0x7E0000 + spriteLowWRAM,
		SpriteLowHex:   fmt.Sprintf("$7E:%04X", spriteLowWRAM),
		SpriteHighAddr: 0x7E0000 + spriteHighWRAM,
		SpriteHighHex:  fmt.Sprintf("$7E:%04X", spriteHighWRAM),
		SliceBytes:     [4]uint8{oam[sprIdx*4], oam[sprIdx*4+1], oam[sprIdx*4+2], oam[sprIdx*4+3]},
		HighTableByte:  highByte,
	}

	// 5. Query latest CPU write to the sprite's shadow buffer before DMA
	var lastWrite *WriteEntry
	var writeAddr uint32

	for off := uint32(0); off < 4; off++ {
		addr := spriteLowWRAM + off
		writes := e.wramWrites[addr]
		for i := range writes {
			w := &writes[i]
			if w.Cycle < latestDMA.Cycle || (w.Cycle == latestDMA.Cycle && w.EventID < latestDMA.EventID) {
				if lastWrite == nil || w.Cycle > lastWrite.Cycle || (w.Cycle == lastWrite.Cycle && w.EventID > lastWrite.EventID) {
					lastWrite = w
					writeAddr = addr
				}
			}
		}
	}
	if lastWrite == nil {
		writes := e.wramWrites[spriteHighWRAM]
		for i := range writes {
			w := &writes[i]
			if w.Cycle < latestDMA.Cycle || (w.Cycle == latestDMA.Cycle && w.EventID < latestDMA.EventID) {
				if lastWrite == nil || w.Cycle > lastWrite.Cycle || (w.Cycle == lastWrite.Cycle && w.EventID > lastWrite.EventID) {
					lastWrite = w
					writeAddr = spriteHighWRAM
				}
			}
		}
	}

	if lastWrite == nil {
		return res, nil
	}

	res.CPUWrite = &CPUWriteTrace{
		EventID:       lastWrite.EventID,
		Frame:         lastWrite.Frame,
		Cycle:         lastWrite.Cycle,
		PC:            fmt.Sprintf("%02X:%04X", lastWrite.PC>>16, lastWrite.PC&0xFFFF),
		Address:       fmt.Sprintf("$7E:%04X", writeAddr),
		StoredValue:   lastWrite.Value,
		InstructionID: fmt.Sprintf("inst-%06x", lastWrite.PC),
	}

	// 6. Identify instruction retirement sequence
	var matchedRet *RetirementEntry
	var precedingRet *RetirementEntry

	for i := range e.retirements {
		r := &e.retirements[i]
		retPC := uint32(r.Insn.Entry.PB)<<16 | uint32(r.Insn.Entry.PC)
		pcMatch := retPC == lastWrite.PC
		cycleMatch := (r.Insn.Entry.Cycles <= lastWrite.Cycle && lastWrite.Cycle <= r.Insn.Exit.Cycles) ||
			(r.Cycle >= lastWrite.Cycle && r.Cycle-lastWrite.Cycle < 100)
		if pcMatch && cycleMatch {
			matchedRet = r
			for j := i - 1; j >= 0; j-- {
				if e.retirements[j].Seq == r.Seq-1 {
					precedingRet = &e.retirements[j]
					break
				}
			}
			break
		}
	}

	if matchedRet != nil {
		var prec *PrecedingInsn
		if precedingRet != nil {
			prec = &PrecedingInsn{
				EventID: precedingRet.EventID,
				Seq:     precedingRet.Seq,
				PC:      precedingRet.PC,
			}
		}

		var opcode uint8
		if len(matchedRet.Insn.Fetches) > 0 {
			opcode = matchedRet.Insn.Fetches[0].Value
		}

		res.Retirement = &RetirementSequence{
			EventID:   matchedRet.EventID,
			Seq:       matchedRet.Seq,
			PC:        matchedRet.PC,
			Opcode:    opcode,
			Status:    matchedRet.Insn.Status,
			EntryA:    matchedRet.Insn.Entry.A,
			EntryX:    matchedRet.Insn.Entry.X,
			EntryY:    matchedRet.Insn.Entry.Y,
			EntryP:    matchedRet.Insn.Entry.P,
			EntrySP:   matchedRet.Insn.Entry.S,
			EntryDP:   matchedRet.Insn.Entry.D,
			ExitA:     matchedRet.Insn.Exit.A,
			ExitX:     matchedRet.Insn.Exit.X,
			ExitY:     matchedRet.Insn.Exit.Y,
			ExitP:     matchedRet.Insn.Exit.P,
			ExitSP:    matchedRet.Insn.Exit.S,
			ExitDP:    matchedRet.Insn.Exit.D,
			Cycles:    matchedRet.Insn.Exit.Cycles,
			Preceding: prec,
		}
	}

	// 7. Resolve PC to basic block and decompiled C
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

	res.Code = &CodeProvenanceInfo{
		BlockID:    targetBlock.ID,
		SourceLine: matchedLine,
		Statement:  statement,
		PseudoC:    pseudoC,
		SourceMap:  sourceMap,
	}

	return res, nil
}

func decodeSpriteAttrs(oam [544]uint8, sprIdx int) (SpriteAttrs, BoundingBox) {
	addr := sprIdx * 4
	xLow := int(oam[addr])
	yPos := int(oam[addr+1])
	tileLow := int(oam[addr+2])
	attr := oam[addr+3]

	highByte := oam[512+(sprIdx/4)]
	shift := (sprIdx % 4) * 2
	xHigh := int((highByte >> shift) & 1)
	sizeBit := ((highByte >> (shift + 1)) & 1) != 0

	posX := xLow | (xHigh << 8)
	if posX >= 256 {
		posX -= 512
	}
	tile := tileLow | (int(attr&1) << 8)
	palette := int((attr >> 1) & 7)
	priority := int((attr >> 4) & 3)
	hflip := (attr & 0x40) != 0
	vflip := (attr & 0x80) != 0

	w, h := 8, 8
	if sizeBit {
		w, h = 16, 16
	}
	return SpriteAttrs{
		Tile:     tile,
		Palette:  palette,
		Priority: priority,
		HFlip:    hflip,
		VFlip:    vflip,
		Large:    sizeBit,
	}, BoundingBox{
		X:      posX,
		Y:      yPos,
		Width:  w,
		Height: h,
	}
}
