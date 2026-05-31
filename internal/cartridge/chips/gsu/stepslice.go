package gsu

const stepSlicePhaseFMULTWait = 1

// StepSliceResult reports the work completed by StepSlice.
type StepSliceResult struct {
	Cycles         uint64
	RetiredOpcodes int
	Running        bool
	Partial        bool
}

// StepSlice advances a bounded slice of a currently running GSU opcode.
//
// The first implementation only slices plain FMULT's deterministic multiply
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

	if !d.canStartFMULTStepSlice() {
		return result
	}
	dispatchCycles := d.nextOpcodeFetchCycles()
	if masterCycles < dispatchCycles {
		return result
	}
	if !d.startFMULTStepSlice() {
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

func (d *Device) startFMULTStepSlice() bool {
	pbr, pc := d.PBR, d.R[15]
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePrePeek, pbr, pc, d.Pipeline, d.cycles)
	}
	op := d.peekpipe()
	if op != 0x9f {
		return false
	}
	if d.TraceHook != nil {
		d.TraceHook(pbr, pc, op)
	}
	if d.TraceHookEx != nil {
		d.TraceHookEx(TracePhasePostPeek, pbr, pc, op, d.cycles)
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

func (d *Device) advanceStepSliceFrame(masterCycles uint64) int {
	if d.stepSlice.Op != 0x9f || d.stepSlice.Phase != stepSlicePhaseFMULTWait {
		return 0
	}
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
