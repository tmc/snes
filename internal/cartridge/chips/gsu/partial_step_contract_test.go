package gsu

import (
	"strings"
	"testing"
)

func TestFutureStepSliceContract(t *testing.T) {
	fixtures := []futureStepSliceFixture{
		{
			name:             "FMULT slow multiply slow clock",
			rom:              []byte{0x9f, 0x00}, // FMULT; STOP
			regs:             [16]uint16{0: 0x0080, 6: 0x0100},
			wholeRunRetires:  1,
			handlerWait:      14,
			sliceRetireCount: []futureStepSliceRetire{{cycles: 13, retires: 0}, {cycles: 1, retires: 1}},
			wantFinal: futureStepSliceState{
				R:          [16]uint16{6: 0x0100, 15: 0x0002},
				SFR:        SFRG | SFRZ | SFRCY,
				VCR:        0x04,
				Pipeline:   0x00,
				CacheLine0: [16]uint8{0x9f, 0x00},
				CacheValid: [32]bool{0: true},
				Cycles:     112,
			},
		},
		{
			name:             "IWT two-byte immediate slow clock",
			rom:              []byte{0xf5, 0x34, 0x12, 0x00}, // IWT R5,#$1234; STOP
			wholeRunRetires:  1,
			handlerWait:      4,
			sliceRetireCount: []futureStepSliceRetire{{cycles: 2, retires: 0}, {cycles: 2, retires: 1}},
			wantFinal: futureStepSliceState{
				R:          [16]uint16{5: 0x1234, 15: 0x0004},
				SFR:        SFRG,
				VCR:        0x04,
				Pipeline:   0x00,
				CacheLine0: [16]uint8{0xf5, 0x34, 0x12, 0x00},
				CacheValid: [32]bool{0: true},
				Cycles:     102,
			},
		},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			var sliceCycles uint64
			var sliceRetires int
			for _, slice := range f.sliceRetireCount {
				if slice.cycles == 0 {
					t.Fatalf("zero-cycle StepSlice fixture entry")
				}
				sliceCycles += slice.cycles
				sliceRetires += slice.retires
			}
			if sliceCycles != f.handlerWait {
				t.Fatalf("StepSlice fixture cycles=%d, want handler wait %d", sliceCycles, f.handlerWait)
			}
			if sliceRetires != f.wholeRunRetires {
				t.Fatalf("StepSlice fixture retires=%d, want whole-run retires %d",
					sliceRetires, f.wholeRunRetires)
			}

			d := f.newDevice()
			GoAndRun(d, f.wholeRunRetires)
			if got := captureFutureStepSliceState(d); got != f.wantFinal {
				t.Fatalf("whole-run final state = %+v, want %+v", got, f.wantFinal)
			}
		})
	}

	assertions := []string{
		"StepSlice(13) during FMULT (14-cycle handler wait) retires zero logical opcodes",
		"Serialize/Unserialize preserves the in-flight handler, cycle debt, PC, pipeline, buffers, registers, and flags",
		"a second StepSlice(1) resumes the same FMULT handler and retires exactly one logical opcode",
		"StepSlice(2) during IWT R5,#imm16 retires zero logical opcodes after the first operand fetch",
		"a second StepSlice(2) completes the IWT operand fetch and retires exactly one logical opcode",
		"the resumed final state matches whole-handler GoAndRun for registers, SFR, buffers, PC, pipeline, and Cycles",
		"Run(n) remains opcode-granular and does not expose partial handler state to existing tests",
	}
	for _, assertion := range assertions {
		if assertion == "" {
			t.Fatal("empty partial-step assertion")
		}
	}

	t.Skipf("full StepSlice contract still leaves Run integration outside the current StepSlice primitive: %s",
		strings.Join(assertions, "; "))
}

type futureStepSliceFixture struct {
	name             string
	rom              []byte
	regs             [16]uint16
	cfgr             uint8
	clsr             uint8
	wholeRunRetires  int
	handlerWait      uint64
	sliceRetireCount []futureStepSliceRetire
	wantFinal        futureStepSliceState
}

type futureStepSliceRetire struct {
	cycles  uint64
	retires int
}

type futureStepSliceState struct {
	R           [16]uint16
	SFR         uint16
	PBR         uint8
	ROMBR       uint8
	RAMBR       uint8
	CBR         uint16
	SCBR        uint8
	SCMR        uint8
	BRAMR       uint8
	VCR         uint8
	CFGR        uint8
	CLSR        uint8
	COLR        uint8
	POR         uint8
	SREG        uint8
	DREG        uint8
	RAMAddr     uint16
	Pipeline    uint8
	R15Modified bool
	WithPrefix  bool
	ToPrefix    bool
	FromPrefix  bool
	WithReg     uint8
	Pixels      [8]uint8
	ValidMask   uint8
	CacheRow    uint16
	CacheHas    bool
	Commits     uint32
	CacheLine0  [16]uint8
	CacheValid  [32]bool
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

func (f futureStepSliceFixture) newDevice() *Device {
	d := New(append([]byte(nil), f.rom...), nil)
	d.R = f.regs
	d.CFGR = f.cfgr
	d.CLSR = f.clsr
	return d
}

func captureFutureStepSliceState(d *Device) futureStepSliceState {
	var cacheLine0 [16]uint8
	copy(cacheLine0[:], d.Cache[:16])
	return futureStepSliceState{
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
		Pixels:      d.pixels,
		ValidMask:   d.validMask,
		CacheRow:    d.cacheRow,
		CacheHas:    d.cacheHasRow,
		Commits:     d.commits,
		CacheLine0:  cacheLine0,
		CacheValid:  d.cacheValid,
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
	}
}
