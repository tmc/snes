package gsu

const (
	stepSlicePhaseFMULTWait    = 1
	stepSlicePhaseIWTFetchLow  = 2
	stepSlicePhaseIWTFetchHigh = 3
	stepSlicePhaseSTWWaitHigh  = 4
)

// StepSliceResult reports the work completed by StepSlice.
type StepSliceResult struct {
	Cycles         uint64
	RetiredOpcodes int
	Running        bool
	Partial        bool
}

// StepSlice advances a bounded slice of a currently running GSU opcode.
//
// The current implementation slices plain FMULT's deterministic multiply wait,
// plain IWT's two operand-byte fetches, and plain STW's inter-byte RAM write
// wait. Unsupported opcodes and unsupported boundaries make no progress.
func (d *Device) StepSlice(masterCycles uint64) StepSliceResult {
	result := d.stepSliceResult(0, 0)
	if masterCycles == 0 || !d.Running() {
		return result
	}

	start := d.cycles
	if d.stepSlice.Active {
		retired := d.advanceStepSliceFrame(masterCycles)
		return d.stepSliceResult(d.cycles-start, retired)
	}

	if !d.canStartFMULTStepSlice() &&
		!d.canStartIWTStepSlice() &&
		!d.canStartSTWStepSlice() {
		return result
	}
	dispatchCycles := d.nextOpcodeFetchCycles()
	if masterCycles < dispatchCycles {
		return result
	}
	if !d.startStepSliceFrame() {
		return result
	}
	spent := d.cycles - start
	if spent >= masterCycles {
		return d.stepSliceResult(spent, 0)
	}
	retired := d.advanceStepSliceFrame(masterCycles - spent)
	return d.stepSliceResult(d.cycles-start, retired)
}

func (d *Device) stepSliceResult(cycles uint64, retired int) StepSliceResult {
	return StepSliceResult{
		Cycles:         cycles,
		RetiredOpcodes: retired,
		Running:        d.Running(),
		Partial:        d.stepSlice.Active,
	}
}

func (d *Device) canStartFMULTStepSlice() bool {
	return d.Pipeline == 0x9f &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartIWTStepSlice() bool {
	return d.Pipeline >= 0xf0 &&
		d.Pipeline <= 0xff &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix
}

func (d *Device) canStartSTWStepSlice() bool {
	return d.Pipeline >= 0x30 &&
		d.Pipeline <= 0x3b &&
		d.alt() == AltNone &&
		!d.withPrefix &&
		!d.toPrefix &&
		!d.fromPrefix &&
		!d.ramPending
}

func (d *Device) startStepSliceFrame() bool {
	switch {
	case d.canStartFMULTStepSlice():
		return d.startFMULTStepSlice()
	case d.canStartIWTStepSlice():
		return d.startIWTStepSlice()
	case d.canStartSTWStepSlice():
		return d.startSTWStepSlice()
	default:
		return false
	}
}

func (d *Device) startStepSliceDispatch() (uint8, uint8, uint16) {
	pbr, pc := d.PBR, d.R[15]
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePrePeek, pbr, pc, d.Pipeline, d.cycles)
	}
	op := d.peekpipe()
	if d.TraceHook != nil {
		d.TraceHook(pbr, pc, op)
	}
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePostPeek, pbr, pc, op, d.cycles)
	}
	return op, pbr, pc
}

func (d *Device) startFMULTStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op != 0x9f {
		return false
	}
	src, dst := d.srcReg(), d.dstReg()
	d.executeMultResult(AltNone)
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseFMULTWait,
		Mode:            AltNone,
		SrcReg:          src,
		DstReg:          dst,
		RemainingCycles: d.multWaitCycles(),
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) startIWTStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0xf0 || op > 0xff {
		return false
	}
	d.stepSlice = stepSliceFrame{
		Active:        true,
		Op:            op,
		PBR:           pbr,
		PC:            pc,
		Phase:         stepSlicePhaseIWTFetchLow,
		Mode:          AltNone,
		DstReg:        op & 0x0f,
		Nibble:        op & 0x0f,
		PostPending:   true,
		PrefixPending: true,
	}
	return true
}

func (d *Device) startSTWStepSlice() bool {
	op, pbr, pc := d.startStepSliceDispatch()
	if op < 0x30 || op > 0x3b {
		return false
	}
	n := op & 0x0f
	addr := d.R[n]
	v := d.R[d.srcReg()]
	bank := d.RAMBR
	d.RAMAddr = addr
	d.writeRAMBufferBank(bank, addr, uint8(v))
	d.stepSlice = stepSliceFrame{
		Active:          true,
		Op:              op,
		PBR:             pbr,
		PC:              pc,
		Phase:           stepSlicePhaseSTWWaitHigh,
		Mode:            AltNone,
		SrcReg:          d.srcReg(),
		Nibble:          n,
		OperandHigh:     uint8(v >> 8),
		Bank:            bank,
		Address:         addr ^ 1,
		RemainingCycles: d.ramDelay,
		PostPending:     true,
		PrefixPending:   true,
	}
	return true
}

func (d *Device) advanceStepSliceFrame(masterCycles uint64) int {
	switch {
	case d.stepSlice.Op == 0x9f && d.stepSlice.Phase == stepSlicePhaseFMULTWait:
		return d.advanceFMULTStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0xf0 && d.stepSlice.Op <= 0xff:
		return d.advanceIWTStepSliceFrame(masterCycles)
	case d.stepSlice.Op >= 0x30 && d.stepSlice.Op <= 0x3b &&
		d.stepSlice.Phase == stepSlicePhaseSTWWaitHigh:
		return d.advanceSTWStepSliceFrame(masterCycles)
	default:
		return 0
	}
}

func (d *Device) advanceFMULTStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) advanceIWTStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	for {
		switch d.stepSlice.Phase {
		case stepSlicePhaseIWTFetchLow:
			lo, spent, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.stepSlice.OperandLow = lo
			d.stepSlice.Phase = stepSlicePhaseIWTFetchHigh
			masterCycles -= spent
		case stepSlicePhaseIWTFetchHigh:
			hi, _, ok := d.stepSliceFetch8(masterCycles)
			if !ok {
				return 0
			}
			d.setReg(d.stepSlice.DstReg, uint16(d.stepSlice.OperandLow)|uint16(hi)<<8)
			d.finishStepSliceFrame()
			return 1
		default:
			return 0
		}
	}
}

func (d *Device) advanceSTWStepSliceFrame(masterCycles uint64) int {
	if masterCycles == 0 {
		return 0
	}
	if d.stepSlice.RemainingCycles != 0 {
		if masterCycles < d.stepSlice.RemainingCycles {
			d.advanceCycles(masterCycles)
			d.stepSlice.RemainingCycles -= masterCycles
			return 0
		}
		d.advanceCycles(d.stepSlice.RemainingCycles)
		d.stepSlice.RemainingCycles = 0
	}
	d.writeRAMBufferBank(d.stepSlice.Bank, d.stepSlice.Address, d.stepSlice.OperandHigh)
	d.finishStepSliceFrame()
	return 1
}

func (d *Device) stepSliceFetch8(masterCycles uint64) (uint8, uint64, bool) {
	cycles := d.nextOpcodeFetchCycles()
	if masterCycles < cycles {
		return 0, 0, false
	}
	start := d.cycles
	v := d.fetch8()
	return v, d.cycles - start, true
}

func (d *Device) finishStepSliceFrame() {
	frame := d.stepSlice
	if frame.PrefixPending {
		d.consumePrefixes()
	}
	if frame.PostPending {
		if d.r15Modified {
			d.r15Modified = false
		} else {
			d.R[15]++
		}
	}
	d.stepSlice = stepSliceFrame{}
}
