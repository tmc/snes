package gsu

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

const (
	cpuRegBase = 0x3000
	cpuRegEnd  = 0x32ff
	cacheBase  = 0x3100
)

// Read implements the cartridge coprocessor interface for the Super FX CPU
// register window. Per bsnes/sfc/coprocessor/superfx/io.cpp the register
// window is only at banks $00-$3F and $80-$BF (where the cart maps the
// GSU register file). Banks $40-$7D and $C0-$FF route to ROM mirror; banks
// $60-$7F (and $E0-$FF) route to shared RAM. Without the bank filter, a
// CPU write to $70:301F (shared-RAM range that happens to alias the
// register-file offset) was incorrectly latched as a $301F write to R15
// and called Go(), launching the GSU prematurely.
func (d *Device) Read(addr uint32) (uint8, bool) {
	bank := (addr >> 16) & 0xff
	if !((bank <= 0x3f) || (bank >= 0x80 && bank <= 0xbf)) {
		return 0, false
	}
	off := addr & 0xffff
	if off < cpuRegBase || off > cpuRegEnd {
		return 0, false
	}
	reg := off - cpuRegBase
	if reg < 0x20 {
		v := d.R[reg/2]
		if reg&1 == 0 {
			return uint8(v), true
		}
		return uint8(v >> 8), true
	}
	switch reg {
	case 0x30:
		return uint8(d.SFR), true
	case 0x31:
		v := uint8(d.SFR >> 8)
		d.SFR &^= SFRIRQ
		return v, true
	case 0x33:
		return d.BRAMR, true
	case 0x34:
		return d.PBR, true
	case 0x36:
		return d.ROMBR, true
	case 0x37:
		return d.CFGR, true
	case 0x38:
		return d.SCBR, true
	case 0x39:
		return d.CLSR, true
	case 0x3a:
		return d.SCMR, true
	case 0x3b:
		return d.VCR, true
	case 0x3c:
		return d.RAMBR, true
	case 0x3e:
		return uint8(d.CBR), true
	case 0x3f:
		return uint8(d.CBR >> 8), true
	default:
		if off >= cacheBase {
			return d.readCache(uint16(off - cacheBase)), true
		}
		return 0, false
	}
}

// Write implements the cartridge coprocessor interface for the Super FX CPU
// register window. See Read for the bank-filter rationale.
func (d *Device) Write(addr uint32, val uint8) bool {
	bank := (addr >> 16) & 0xff
	if !((bank <= 0x3f) || (bank >= 0x80 && bank <= 0xbf)) {
		return false
	}
	off := addr & 0xffff
	if off < cpuRegBase || off > cpuRegEnd {
		return false
	}
	reg := off - cpuRegBase
	if reg < 0x20 {
		n := reg / 2
		r := &d.R[n]
		if reg&1 == 0 {
			*r = (*r & 0xff00) | uint16(val)
		} else {
			*r = (*r & 0x00ff) | uint16(val)<<8
			if n == 15 {
				d.Go()
			}
		}
		if n == 14 {
			d.updateROMBuffer()
		}
		return true
	}
	switch reg {
	case 0x30:
		wasRunning := d.SFR&SFRG != 0
		d.SFR = (d.SFR & 0xff00) | uint16(val)
		if wasRunning && d.SFR&SFRG == 0 {
			d.syncROMBuffer()
			d.syncRAMBuffer()
			d.CBR = 0
			d.flushCache()
		}
	case 0x31:
		d.SFR = (d.SFR & 0x00ff) | uint16(val)<<8
	case 0x33:
		d.BRAMR = val & 0x01
	case 0x34:
		// Per ares/bsnes superfx/io.cpp $3034: PBR write always flushes the
		// opcode cache, even when the value is unchanged.
		d.PBR = val & 0x7f
		d.flushCache()
	case 0x37:
		d.CFGR = val & 0xa0
	case 0x38:
		d.SCBR = val
	case 0x39:
		d.CLSR = val & 0x01
	case 0x3a:
		// Per bsnes/processor/gsu/registers.hpp:58-75 SCMR struct, only
		// bits 0,1 (md), 2 (ht low), 3 (ran), 4 (ron), 5 (ht high) are
		// kept; bits 6,7 are unused on real hardware and dropped on
		// write.
		d.SCMR = val & 0x3F
		// $3036 (ROMBR), $303c (RAMBR), $303e/$303f (CBR) are read-only on
		// real hardware: ares/bsnes superfx/io.cpp writeIO has no cases
		// for them.
	default:
		if off >= cacheBase {
			d.writeCache(uint16(off-cacheBase), val)
			return true
		}
		return false
	}
	return true
}

// Step advances the GSU while it is running.
func (d *Device) Step(masterCycles uint64) {
	if masterCycles == 0 || !d.Running() {
		return
	}
	if d.stepDebt != 0 {
		if masterCycles < d.stepDebt {
			d.stepDebt -= masterCycles
			return
		}
		masterCycles -= d.stepDebt
		d.stepDebt = 0
	}
	d.stepBudget += masterCycles
	for i := 0; i < 1024 && d.Running(); i++ {
		if d.stepBudget < d.nextOpcodeFetchCycles() {
			return
		}
		start := d.cycles
		d.Run(1)
		used := d.cycles - start
		if used > d.stepBudget {
			d.stepDebt = used - d.stepBudget
			d.stepBudget = 0
			return
		}
		d.stepBudget -= used
	}
}

type boardState struct {
	R       [16]uint16
	SFR     uint16
	PBR     uint8
	ROMBR   uint8
	RAMBR   uint8
	CBR     uint16
	SCBR    uint8
	SCMR    uint8
	BRAMR   uint8
	VCR     uint8
	CFGR    uint8
	CLSR    uint8
	COLR    uint8
	POR     uint8
	SREG    uint8
	DREG    uint8
	RAMAddr uint16
	// Pipeline serialises the prefetch pipeline byte (d.Pipeline);
	// bsnes serializes regs.pipeline per processor/gsu/serialization.cpp,
	// so a save/load mid-execution restores with the same byte queued
	// for the next dispatch.
	Pipeline    uint8
	R15Modified bool
	WithPrefix  bool
	ToPrefix    bool
	FromPrefix  bool
	WithReg     uint8
	RAM         []byte
	Pixels      [8]uint8
	ValidMask   uint8
	CacheRow    uint16
	CacheHas    bool
	Commits     uint32
	Cache       [512]uint8
	CacheValid  [32]bool
	VRAMRows    map[uint16][8]byte
	Cycles      uint64
	StepBudget  uint64
	StepDebt    uint64
	StepSlice   stepSliceFrame
	RAMPending  bool
	RAMDelay    uint64
	RAMBufBank  uint8
	RAMBufAddr  uint16
	RAMBufData  uint8
	ROMPending  bool
	ROMDelay    uint64
	ROMData     uint8
}

// Serialize captures GSU state. The VRAM writer is deliberately not serialized;
// the owning system rebinds it when the cartridge is loaded.
func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(boardState{
		R:           d.R,
		SFR:         d.SFR,
		PBR:         d.PBR,
		ROMBR:       d.ROMBR,
		RAMBR:       d.RAMBR,
		CBR:         d.CBR,
		SCBR:        d.SCBR,
		SCMR:        d.SCMR,
		BRAMR:       d.BRAMR,
		VCR:         d.VCR,
		CFGR:        d.CFGR,
		CLSR:        d.CLSR,
		COLR:        d.COLR,
		POR:         d.POR,
		SREG:        d.SREG,
		DREG:        d.DREG,
		RAMAddr:     d.RAMAddr,
		Pipeline:    d.Pipeline,
		R15Modified: d.r15Modified,
		WithPrefix:  d.withPrefix,
		ToPrefix:    d.toPrefix,
		FromPrefix:  d.fromPrefix,
		WithReg:     d.withReg,
		RAM:         append([]byte(nil), d.RAM...),
		Pixels:      d.pixels,
		ValidMask:   d.validMask,
		CacheRow:    d.cacheRow,
		CacheHas:    d.cacheHasRow,
		Commits:     d.commits,
		Cache:       d.Cache,
		CacheValid:  d.cacheValid,
		VRAMRows:    cloneRows(d.vramRows),
		Cycles:      d.cycles,
		StepBudget:  d.stepBudget,
		StepDebt:    d.stepDebt,
		StepSlice:   d.stepSlice,
		RAMPending:  d.ramPending,
		RAMDelay:    d.ramDelay,
		RAMBufBank:  d.ramBank,
		RAMBufAddr:  d.ramAddr,
		RAMBufData:  d.ramData,
		ROMPending:  d.romPending,
		ROMDelay:    d.romDelay,
		ROMData:     d.romData,
	}); err != nil {
		return nil, fmt.Errorf("serialize gsu: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores GSU state produced by Serialize.
func (d *Device) Unserialize(data []byte) error {
	var s boardState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize gsu: %w", err)
	}
	if len(s.RAM) != len(d.RAM) {
		return fmt.Errorf("unserialize gsu: ram size %d, want %d", len(s.RAM), len(d.RAM))
	}
	if s.SREG > 15 || s.DREG > 15 || s.WithReg > 15 {
		return fmt.Errorf("unserialize gsu: invalid register selector")
	}
	if s.StepSlice.Active && (s.StepSlice.SrcReg > 15 || s.StepSlice.DstReg > 15) {
		return fmt.Errorf("unserialize gsu: invalid partial-instruction register selector")
	}
	d.R = s.R
	d.SFR = s.SFR
	d.PBR = s.PBR
	d.ROMBR = s.ROMBR
	d.RAMBR = s.RAMBR
	d.CBR = s.CBR
	d.SCBR = s.SCBR
	d.SCMR = s.SCMR
	d.BRAMR = s.BRAMR
	d.VCR = s.VCR
	d.CFGR = s.CFGR
	d.CLSR = s.CLSR
	d.COLR = s.COLR
	d.POR = s.POR
	d.SREG = s.SREG
	d.DREG = s.DREG
	d.RAMAddr = s.RAMAddr
	d.Pipeline = s.Pipeline
	d.r15Modified = s.R15Modified
	d.withPrefix = s.WithPrefix
	d.toPrefix = s.ToPrefix
	d.fromPrefix = s.FromPrefix
	d.withReg = s.WithReg
	copy(d.RAM, s.RAM)
	d.pixels = s.Pixels
	d.validMask = s.ValidMask
	d.cacheRow = s.CacheRow
	d.cacheHasRow = s.CacheHas
	d.commits = s.Commits
	d.Cache = s.Cache
	d.cacheValid = s.CacheValid
	d.vramRows = cloneRows(s.VRAMRows)
	d.cycles = s.Cycles
	d.stepBudget = s.StepBudget
	d.stepDebt = s.StepDebt
	d.stepSlice = s.StepSlice
	d.ramPending = s.RAMPending
	d.ramDelay = s.RAMDelay
	d.ramBank = s.RAMBufBank
	d.ramAddr = s.RAMBufAddr
	d.ramData = s.RAMBufData
	d.romPending = s.ROMPending
	d.romDelay = s.ROMDelay
	d.romData = s.ROMData
	d.vramShadow = d.vramShadow[:0]
	return nil
}

func cloneRows(rows map[uint16][8]byte) map[uint16][8]byte {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[uint16][8]byte, len(rows))
	for addr, row := range rows {
		out[addr] = row
	}
	return out
}

// stepSliceFrame is the serialized state for a StepSlice partial-opcode frame.
// The zero value means no in-flight opcode.
type stepSliceFrame struct {
	Active bool
	Op     uint8
	PBR    uint8
	PC     uint16
	Phase  uint8

	Mode   AltMode
	SrcReg uint8
	DstReg uint8
	Nibble uint8

	OperandLow      uint8
	OperandHigh     uint8
	Bank            uint8
	Address         uint16
	RemainingCycles uint64
	PostPending     bool
	PrefixPending   bool
}
